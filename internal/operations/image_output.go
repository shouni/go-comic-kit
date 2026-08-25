package operations

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// ImageGenerator は、参照画像（0〜複数）を添えて画像を生成する依存インターフェースです。
// デザインシート（複数キャラの合成）とパネル（複数キャラの同席コマ）の両方で使います。
//
// gemini-image-kit v1.13 で単発生成と融合生成が Generate へ統合されたため、参照の枚数は
// ImageRequest.Images の長さでしか区別しません。imagePorts.ImageGenerator をそのまま
// 使わないのは、テスト用フェイクをこのパッケージ内で完結させるためです。
type ImageGenerator interface {
	Generate(ctx context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error)
}

// writeGeneratedImage は生成された画像データを Content-Type と Cache-Control 付きで
// 指定パスへ書き込みます。cacheControl が空の場合は既定値を使います。
func writeGeneratedImage(ctx context.Context, writer remoteio.Writer, path string, resp *imagePorts.ImageResponse, cacheControl string) error {
	if cacheControl == "" {
		cacheControl = ports.DefaultCacheControl
	}
	return writer.Write(ctx, path, bytes.NewReader(resp.Data),
		remoteio.WithContentType(resp.MimeType),
		remoteio.WithCacheControl(cacheControl),
	)
}

// imageRenderRequest は画像生成1回分の入力です。
type imageRenderRequest struct {
	Model          string
	Prompt         string
	SystemPrompt   string
	NegativePrompt string
	AspectRatio    string
	ImageSize      string
	Seed           *int64
	Images         []imagePorts.ImageURI
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
	resp, err := generator.Generate(ctx, imagePorts.ImageRequest{
		Model:          req.Model,
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		SystemPrompt:   req.SystemPrompt,
		AspectRatio:    req.AspectRatio,
		ImageSize:      req.ImageSize,
		Seed:           req.Seed,
		Images:         req.Images,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: 画像の生成に失敗しました: %w", ports.ErrGeneration, err)
	}

	finalPath, err := req.PathFor(resp.MimeType)
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
