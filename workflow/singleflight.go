package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-gemini-client/callguard"
	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-comic-kit/internal/operations"
)

// 本ファイルは、キットの口（operations.ImageGenerator / StructuredGenerator）に
// 呼び出しガードを被せるデコレータと、リクエスト内容からキーを作る部分を持ちます。
// 発射間隔・上限時間・同時実行の重複排除そのものは callguard が持っており、
// go-veo-orchestrator や go-gemini-client/lyria と同じ実装を共有します。

// singleflightImageGenerator は、同一内容の画像生成リクエストの同時実行を1回にまとめる
// ImageGenerator のデコレータです。
// Cloud Tasks の at-least-once 配信や MCP クライアントのリトライによる重複呼び出しから、
// 高価な画像生成 API 呼び出しを守ります。プロセス内の in-flight のみが対象で、
// 恒久的な重複排除は state の GenerationRecord によるジョブ側の冪等性で行います。
type singleflightImageGenerator struct {
	inner operations.ImageGenerator
	guard *callguard.Guard
	group callguard.Group
}

var _ operations.ImageGenerator = (*singleflightImageGenerator)(nil)

// Generate はリクエスト内容のハッシュをキーに同時実行をまとめます。
// 共有される応答は呼び出し元ごとに複製して返します。
func (g *singleflightImageGenerator) Generate(ctx context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	key := imageRequestKey(&req)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*imagePorts.ImageResponse, error) {
		return g.inner.Generate(execCtx, req)
	})
	if err != nil {
		return nil, err
	}
	return cloneImageResponse(resp), nil
}

// singleflightStructuredGenerator は、同一内容のテキスト生成リクエストの同時実行を
// 1回にまとめる StructuredGenerator のデコレータです。
type singleflightStructuredGenerator struct {
	inner operations.StructuredGenerator
	guard *callguard.Guard
	group callguard.Group
}

var _ operations.StructuredGenerator = (*singleflightStructuredGenerator)(nil)

// GenerateWithAttachments はリクエスト内容のハッシュをキーに同時実行をまとめます。
func (g *singleflightStructuredGenerator) GenerateWithAttachments(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	key := structuredRequestKey(modelName, prompt, attachments, &opts)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*gemini.Response, error) {
		return g.inner.GenerateWithAttachments(execCtx, modelName, prompt, attachments, opts)
	})
	if err != nil {
		return nil, err
	}
	// NOTE: 浅いコピーで返します。呼び出し側（operations）は Text しか参照しない前提です。
	// gemini.Response の参照型フィールドを書き換える利用が増えた場合は深いコピーに変更すること。
	cloned := *resp
	return &cloned, nil
}

// imageRequestKey は画像生成リクエストの内容から singleflight 用キーを作ります。
func imageRequestKey(req *imagePorts.ImageRequest) string {
	parts := []string{
		req.Model,
		req.Prompt,
		req.SystemPrompt,
		req.NegativePrompt,
		req.AspectRatio,
		req.ImageSize,
		callguard.SeedKey(req.Seed),
	}
	for _, img := range req.Images {
		parts = append(parts, img.ReferenceURL, img.FileAPIURI)
	}
	return callguard.Key("image", parts...)
}

// structuredRequestKey はテキスト生成リクエストの内容から singleflight 用キーを作ります。
//
// 添付は URI かバイト列のどちらかなので、URI はそのまま、バイト列は中身のハッシュを
// キーに含めます。長さだけで代用すると、同じサイズの別画像が同じキーになります。
func structuredRequestKey(modelName string, prompt string, attachments []gemini.Attachment, opts *gemini.GenerateOptions) string {
	keyParts := []string{modelName, opts.ResponseMIMEType, callguard.SeedKey(opts.Seed), prompt}
	for _, attachment := range attachments {
		keyParts = append(keyParts, attachment.MIMEType, attachment.URI)
		if len(attachment.Data) > 0 {
			sum := sha256.Sum256(attachment.Data)
			keyParts = append(keyParts, hex.EncodeToString(sum[:]))
		}
	}
	return callguard.Key("structured", keyParts...)
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
