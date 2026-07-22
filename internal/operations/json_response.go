package operations

import (
	"encoding/json"
	"fmt"

	"github.com/shouni/go-gemini-client/gemini"
)

// maxErrorResponseLength はエラーログに含める応答抜粋の最大文字数です。
const maxErrorResponseLength = 200

// parseJSONResponse は AI の応答から JSON を抽出し、out にデコードします。
func parseJSONResponse(raw string, out any) error {
	jsonStr := gemini.CleanJSONResponse(raw)
	if err := json.Unmarshal([]byte(jsonStr), out); err != nil {
		return fmt.Errorf("AI応答JSONの解析に失敗しました (抜粋: %q): %w",
			truncateString(raw, maxErrorResponseLength), err)
	}
	return nil
}

// truncateString は指定された長さで文字列を安全に切り捨てます。
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
