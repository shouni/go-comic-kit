package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-gemini-client/gemini"
	"golang.org/x/sync/singleflight"
	"google.golang.org/genai"

	"github.com/shouni/go-comic-kit/runner"
)

// singleflightExecTimeout は、singleflight で共有される生成処理1回あたりの実行タイムアウトです。
// 呼び出し元の context から切り離した実行用 context に適用されます。
const singleflightExecTimeout = 5 * time.Minute

// singleflightFusionGenerator は、同一内容の画像生成リクエストの同時実行を1回にまとめる
// ImageFusionGenerator のデコレータです（go-gemini-client/lyria と同方式）。
// Cloud Tasks の at-least-once 配信や MCP クライアントのリトライによる重複呼び出しから、
// 高価な画像生成 API 呼び出しを守ります。プロセス内の in-flight のみが対象で、
// 恒久的な重複排除は state の GenerationRecord によるジョブ側の冪等性で行います。
type singleflightFusionGenerator struct {
	inner runner.ImageFusionGenerator
	group singleflight.Group
}

var _ runner.ImageFusionGenerator = (*singleflightFusionGenerator)(nil)

// GenerateFusedImage はリクエスト内容のハッシュをキーに同時実行をまとめます。
// 共有される応答は呼び出し元ごとに複製して返します。
func (g *singleflightFusionGenerator) GenerateFusedImage(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error) {
	key := fusionRequestKey(&req)
	resp, err := doSingleflight(ctx, &g.group, key, func(execCtx context.Context) (*imagePorts.ImageResponse, error) {
		return g.inner.GenerateFusedImage(execCtx, req)
	})
	if err != nil {
		return nil, err
	}
	return cloneImageResponse(resp), nil
}

// singleflightStructuredGenerator は、同一内容のテキスト生成リクエストの同時実行を
// 1回にまとめる StructuredGenerator のデコレータです。
type singleflightStructuredGenerator struct {
	inner runner.StructuredGenerator
	group singleflight.Group
}

var _ runner.StructuredGenerator = (*singleflightStructuredGenerator)(nil)

// GenerateWithParts はリクエスト内容のハッシュをキーに同時実行をまとめます。
func (g *singleflightStructuredGenerator) GenerateWithParts(ctx context.Context, modelName string, parts []*genai.Part, opts gemini.GenerateOptions) (*gemini.Response, error) {
	key := structuredRequestKey(modelName, parts, &opts)
	resp, err := doSingleflight(ctx, &g.group, key, func(execCtx context.Context) (*gemini.Response, error) {
		return g.inner.GenerateWithParts(execCtx, modelName, parts, opts)
	})
	if err != nil {
		return nil, err
	}
	// NOTE: 浅いコピーで返します。呼び出し側（runner）は Text しか参照しない前提です。
	// gemini.Response の参照型フィールドを書き換える利用が増えた場合は深いコピーに変更すること。
	cloned := *resp
	return &cloned, nil
}

// fusionRequestKey は画像生成リクエストの内容から singleflight 用キーを作ります。
func fusionRequestKey(req *imagePorts.ImageFusionRequest) string {
	parts := []string{
		req.Model,
		req.Prompt,
		req.SystemPrompt,
		req.NegativePrompt,
		req.AspectRatio,
		req.ImageSize,
		singleflightSeedKey(req.Seed),
	}
	for _, img := range req.Images {
		parts = append(parts, img.ReferenceURL, img.FileAPIURI)
	}
	return singleflightKey("fusion", parts...)
}

// structuredRequestKey はテキスト生成リクエストの内容から singleflight 用キーを作ります。
func structuredRequestKey(modelName string, parts []*genai.Part, opts *gemini.GenerateOptions) string {
	keyParts := []string{modelName, opts.ResponseMIMEType, singleflightSeedKey(opts.Seed)}
	for _, part := range parts {
		if part != nil {
			keyParts = append(keyParts, part.Text)
		}
	}
	return singleflightKey("structured", keyParts...)
}

// singleflightKey は namespace と可変長の部品から衝突しにくい singleflight 用キーを作ります。
// 各部品を長さプレフィックス付きでハッシュするため、部品の境界の曖昧さがありません。
func singleflightKey(namespace string, parts ...string) string {
	hasher := sha256.New()
	for _, part := range parts {
		hasher.Write([]byte(strconv.Itoa(len(part))))
		hasher.Write([]byte{0})
		hasher.Write([]byte(part))
		hasher.Write([]byte{0})
	}

	return namespace + ":" + hex.EncodeToString(hasher.Sum(nil))
}

// singleflightSeedKey は nil と実値を区別できる seed 用キー部品を作ります。
func singleflightSeedKey(seed *int64) string {
	if seed == nil {
		return "seed:nil"
	}
	return "seed:" + strconv.FormatInt(*seed, 10)
}

// doSingleflight は同じ key の同時実行をまとめ、呼び出し元のキャンセルも尊重します。
// 実行用 context は共有実行のクロージャ内で呼び出し元から切り離して（WithoutCancel）
// 生成するため、どの呼び出し元がキャンセルしても、相乗りしている他の呼び出し元が
// 巻き添えになりません（実行の打ち切りは singleflightExecTimeout でのみ行われます）。
func doSingleflight[T any](ctx context.Context, group *singleflight.Group, key string, fn func(execCtx context.Context) (T, error)) (T, error) {
	ch := group.DoChan(key, func() (any, error) {
		execCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), singleflightExecTimeout)
		defer cancel()
		return fn(execCtx)
	})

	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			var zero T
			return zero, result.Err
		}

		value, ok := result.Val.(T)
		if !ok {
			var zero T
			return zero, fmt.Errorf("singleflight result type mismatch for key %s", key)
		}
		return value, nil
	}
}

// cloneImageResponse は singleflight で共有される応答を呼び出し元が安全に扱えるよう複製します。
func cloneImageResponse(src *imagePorts.ImageResponse) *imagePorts.ImageResponse {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Data = append([]byte(nil), src.Data...)
	return &dst
}
