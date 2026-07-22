package operations

import (
	"bytes"
	"context"
	"strings"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"
)

// ImageFusionGenerator は、複数参照画像を融合して画像を生成する依存インターフェースです。
// デザインシート（複数キャラの合成）とパネル（複数キャラの同席コマ）の両方で使います。
type ImageFusionGenerator interface {
	GenerateFusedImage(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error)
}

// defaultCacheControl は生成物を保存する際の既定の Cache-Control です。
const defaultCacheControl = "public, max-age=1800"

// fileNameSanitizer はファイル名として使用できない文字を置換します。
var fileNameSanitizer = strings.NewReplacer(
	"/", "_",
	`\`, "_",
	":", "_",
	"*", "_",
	"?", "_",
	`"`, "_",
	"<", "_",
	">", "_",
	"|", "_",
)

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
