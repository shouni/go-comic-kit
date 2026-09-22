package operations

import (
	"fmt"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/go-comic-kit/ports"
)

// parseJSONResponse は AI の応答を T にデコードします。
//
// 空判定・補修・デコード・エラー文への抜粋は gemini.DecodeJSON が持ちます。ここでは
// このキットの分類（ErrGeneration。再試行で直りうる）を鎖に足すだけです。
func parseJSONResponse[T any](raw string) (T, error) {
	parsed, err := gemini.DecodeJSON[T](raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%w: AI応答JSONの解析に失敗しました: %w", ports.ErrGeneration, err)
	}
	return parsed, nil
}
