package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"

	"github.com/shouni/go-comic-kit/internal/operations"
	"github.com/shouni/go-comic-kit/internal/reference"
)

// 本ファイルは、キットの口（operations.ImageGenerator / StructuredGenerator）に
// 呼び出しガードを被せるデコレータと、リクエスト内容からキーを作る部分を持ちます。
// 発射間隔・上限時間・同時実行の重複排除そのものは callguard が持っており、
// go-veo-orchestrator や genai-kit/lyria と同じ実装を共有します。
//
// 参照画像を解決する resolvingImageGenerator も同じ形のデコレータです。

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
func (g *singleflightImageGenerator) Generate(ctx context.Context, req operations.ImageRequest) (*operations.ImageResponse, error) {
	key := imageRequestKey(&req)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*operations.ImageResponse, error) {
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
func (g *singleflightStructuredGenerator) Generate(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	key := structuredRequestKey(modelName, prompt, attachments, &opts)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*gemini.Response, error) {
		return g.inner.Generate(execCtx, modelName, prompt, attachments, opts)
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
//
// 参照画像は解決前の URL でキーにします。解決後のバイト列で作ると、同じ URL の
// 呼び出し同士が合流する前にそれぞれ取得を済ませてしまい、重複排除の意味が消えます。
func imageRequestKey(req *operations.ImageRequest) string {
	parts := []string{
		req.Model,
		req.Prompt,
		req.SystemPrompt,
		req.NegativePrompt,
		req.AspectRatio,
		req.ImageSize,
		callguard.SeedKey(req.Seed),
	}
	parts = append(parts, req.Images...)
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
func cloneImageResponse(src *operations.ImageResponse) *operations.ImageResponse {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Data = append([]byte(nil), src.Data...)
	return &dst
}

// resolvingImageGenerator は、未解決の参照画像 URL を送信できる添付へ変換してから
// 画像生成へ渡すデコレータです。
//
// この変換が操作層（operations）ではなくここにあるのは、http(s) の取得という I/O を
// 伴うためです。操作層はプロンプトの組み立てと保存先の決定に閉じています。
type resolvingImageGenerator struct {
	inner    imagegen.Generator
	resolver *reference.Resolver
}

var _ operations.ImageGenerator = (*resolvingImageGenerator)(nil)

// Generate は参照画像を解決してから画像生成を実行します。
func (g *resolvingImageGenerator) Generate(ctx context.Context, req operations.ImageRequest) (*operations.ImageResponse, error) {
	references, err := g.resolver.Resolve(ctx, req.Images)
	if err != nil {
		return nil, err
	}

	return g.inner.Generate(ctx, imagegen.Request{
		Model:           req.Model,
		Prompt:          req.Prompt,
		NegativePrompt:  req.NegativePrompt,
		References:      references,
		GenerateOptions: req.GenerateOptions,
	})
}
