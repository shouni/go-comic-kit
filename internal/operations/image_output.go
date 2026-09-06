package operations

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// ImageRequest は画像生成 1 回分の入力です。
//
// imagegen.Request をそのまま使わないのは、Images の中身が違うためです。imagegen は
// 解決済みの参照（gs:// URI か取得済みのバイト列）を受け取りますが、この層が持っている
// のは未解決の URL で、http(s) なら取得が要ります。解決は workflow 層の
// resolvingImageGenerator が引き受けます。
//
// 生成パラメータは gemini.GenerateOptions を埋め込んで持ちます。写し取ると gemini 側で
// フィールドが増えるたびに 2 か所の同期が要るためで、imagegen.Request 自身が同じ
// 埋め込みをしているのと同じ理由です。Go 1.27 で昇格フィールドを複合リテラルのキーに
// 書けるようになったので、呼び出し側は Model: / AspectRatio: と平らに書けます。
type ImageRequest struct {
	// Model は生成に使うモデル名です。
	Model string
	// Prompt は生成指示、NegativePrompt は含めたくない要素です。
	Prompt         string
	NegativePrompt string
	// Images は参照画像の URL です。gs:// と http(s):// を混在させられます。
	// 並び順はモデルの解釈に影響するため保持されます。
	Images []string

	gemini.GenerateOptions
}

// ImageResponse は画像生成 1 回分の結果です（imagegen.Response の別名）。
type ImageResponse = imagegen.Response

// ImageGenerator は、参照画像（0〜複数）を添えて画像を生成する依存インターフェースです。
// デザインシート・パネル・ページの3操作すべてがこれを通ります。
//
// 単発生成と融合生成は同じ口です。参照の枚数は ImageRequest.Images の長さでしか
// 区別しません。imagegen.Generator をそのまま使わないのは、参照画像が未解決の URL で
// あることと、テスト用フェイクをこのパッケージ内で完結させるためです。
type ImageGenerator interface {
	Generate(ctx context.Context, req ImageRequest) (*ImageResponse, error)
}

// writeGeneratedImage は生成された画像データを Content-Type と Cache-Control 付きで
// 指定パスへ書き込みます。cacheControl が空の場合は既定値を使います。
func writeGeneratedImage(ctx context.Context, writer remoteio.Writer, path string, resp *ImageResponse, cacheControl string) error {
	if cacheControl == "" {
		cacheControl = ports.DefaultCacheControl
	}
	return writer.Write(ctx, path, bytes.NewReader(resp.Data),
		remoteio.WithContentType(resp.MIMEType),
		remoteio.WithCacheControl(cacheControl),
	)
}

// imageRenderRequest は、画像生成の入力に保存の指定を足したものです。
type imageRenderRequest struct {
	ImageRequest
	// CacheControl は保存時に付ける Cache-Control です（空なら ports.DefaultCacheControl）。
	CacheControl string
	// PathFor は、生成結果の MIME type から保存先パスを決めます。
	// 拡張子が MIME type に依存するため、生成後にしか決められません。
	PathFor func(mimeType string) (string, error)
}

// renderImage は「生成 → 保存 → 生成記録の作成」という、パネル生成・ページ合成・
// デザインシート生成で共通の流れを実行します。呼び出し側には入力の組み立てと
// 保存先の決め方だけが残ります。
//
// 失敗にはいずれも ports の番兵エラーを付けます。保存やパス生成は AI 呼び出しでは
// ありませんが、裸の fmt.Errorf で返すと呼び出し側の errors.Is 分類から見えなくなるためです
// （ports/errors.go 参照）。パス生成の失敗は引数（OutputDir など）が原因で再試行しても
// 直らないので ErrInvalidRequest、保存の失敗は一時的なことが多いので ErrGeneration です。
func renderImage(ctx context.Context, generator ImageGenerator, writer remoteio.Writer, req imageRenderRequest) (*comic.GenerationRecord, error) {
	resp, err := generator.Generate(ctx, req.ImageRequest)
	if err != nil {
		return nil, fmt.Errorf("%w: 画像の生成に失敗しました: %w", ports.ErrGeneration, err)
	}

	finalPath, err := req.PathFor(resp.MIMEType)
	if err != nil {
		return nil, fmt.Errorf("%w: 画像の保存パス生成に失敗しました: %w", ports.ErrInvalidRequest, err)
	}
	if err := writeGeneratedImage(ctx, writer, finalPath, resp, req.CacheControl); err != nil {
		return nil, fmt.Errorf("%w: 画像の保存に失敗しました (path: %s): %w", ports.ErrGeneration, finalPath, err)
	}

	return &comic.GenerationRecord{
		ImageURL:       finalPath,
		UsedSeed:       resp.UsedSeed,
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		Model:          req.Model,
		GeneratedAt:    time.Now().UTC(),
	}, nil
}
