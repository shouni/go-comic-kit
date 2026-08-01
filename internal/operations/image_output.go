package operations

import (
	"bytes"
	"context"
	"fmt"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// ImageFusionGenerator は、複数参照画像を融合して画像を生成する依存インターフェースです。
// デザインシート（複数キャラの合成）とパネル（複数キャラの同席コマ）の両方で使います。
type ImageFusionGenerator interface {
	GenerateFusedImage(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error)
}

// defaultCacheControl は生成物を保存する際の既定の Cache-Control です。
const defaultCacheControl = "public, max-age=1800"

// getPreferredExtension は MimeType に対応するファイル拡張子を返します。
func getPreferredExtension(mimeType string) string {
	preferred := map[string]string{"image/png": ".png", "image/jpeg": ".jpg"}
	if ext, ok := preferred[mimeType]; ok {
		return ext
	}
	return ".png"
}

// writeGeneratedImage は生成された画像データを Content-Type と Cache-Control 付きで
// 指定パスへ書き込みます。
func writeGeneratedImage(ctx context.Context, writer remoteio.Writer, path string, resp *imagePorts.ImageResponse) error {
	return writer.Write(ctx, path, bytes.NewReader(resp.Data),
		remoteio.WithContentType(resp.MimeType),
		remoteio.WithCacheControl(defaultCacheControl),
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
func renderImage(ctx context.Context, generator ImageFusionGenerator, writer remoteio.Writer, req imageRenderRequest) (*ports.GenerationRecord, error) {
	resp, err := generator.GenerateFusedImage(ctx, imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          req.Model,
			Prompt:         req.Prompt,
			SystemPrompt:   req.SystemPrompt,
			NegativePrompt: req.NegativePrompt,
			AspectRatio:    req.AspectRatio,
			ImageSize:      req.ImageSize,
			Seed:           req.Seed,
		},
		Images: req.Images,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: 画像の生成に失敗しました: %w", ports.ErrGeneration, err)
	}

	finalPath, err := req.PathFor(resp.MimeType)
	if err != nil {
		return nil, fmt.Errorf("%w: 画像の保存パス生成に失敗しました: %w", ports.ErrInvalidRequest, err)
	}
	if err := writeGeneratedImage(ctx, writer, finalPath, resp); err != nil {
		return nil, fmt.Errorf("%w: 画像の保存に失敗しました (path: %s): %w", ports.ErrGeneration, finalPath, err)
	}

	return &ports.GenerationRecord{
		ImageURL:       finalPath,
		UsedSeed:       resp.UsedSeed,
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		Model:          req.Model,
		GeneratedAt:    time.Now().UTC(),
	}, nil
}
