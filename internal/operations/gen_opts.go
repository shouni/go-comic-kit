package operations

import (
	"fmt"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-comic-kit/ports"
)

// StructuredGenerator は、構造化出力オプション付きのテキスト生成を行う依存インターフェースです。
type StructuredGenerator = gemini.Generator

// requireModel は、呼び出しごとに指定されるモデル名を検証します。
func requireModel(model, operation string) error {
	if model == "" {
		return fmt.Errorf("%w: %sのモデル名が指定されていません", ports.ErrInvalidRequest, operation)
	}
	return nil
}

// resolveAspectRatio は、呼び出しごとの比率指定を検証して返します。空なら fallback です。
func resolveAspectRatio(requested, fallback string) (string, error) {
	if requested == "" {
		return fallback, nil
	}
	if !ports.IsAspectRatio(requested) {
		return "", fmt.Errorf("%w: AspectRatio (%q) は %s のいずれかである必要があります",
			ports.ErrInvalidRequest, requested, strings.Join(ports.AspectRatios(), " / "))
	}
	return requested, nil
}

// resolveImageSize は、呼び出しごとの解像度指定を検証して返します。空なら fallback です。
func resolveImageSize(requested, fallback string) (string, error) {
	if requested == "" {
		return fallback, nil
	}
	if !ports.IsImageSize(requested) {
		return "", fmt.Errorf("%w: ImageSize (%q) は %s / %s のいずれかである必要があります",
			ports.ErrInvalidRequest, requested, ports.ImageSize1K, ports.ImageSize2K)
	}
	return requested, nil
}

// buildJSONGenerateOptions は、schema による構造化出力（constrained decoding）に
// 安全フィルタの無効化を添えた、台本生成用の生成オプションを返します。
func buildJSONGenerateOptions(schema map[string]any) gemini.GenerateOptions {
	return gemini.GenerateOptions{
		ResponseMIMEType:   "application/json",
		ResponseJSONSchema: schema,
		SafetySettings:     gemini.NewSafetySettings(gemini.SafetyBlockNone),
	}
}
