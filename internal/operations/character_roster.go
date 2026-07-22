package operations

import (
	"fmt"
	"strings"

	"github.com/shouni/go-comic-kit/ports"
)

// characterRoster はプロンプトに注入するキャラクター一覧（箇条書き）を構築します。
func characterRoster(characters *ports.Characters) string {
	if characters == nil || len(characters.List) == 0 {
		return "（キャラクター定義なし）"
	}
	var sb strings.Builder
	for i := range characters.List {
		char := &characters.List[i]
		fmt.Fprintf(&sb, "- id: %s / 名前: %s", char.ID, char.Name)
		if len(char.VisualCues) > 0 {
			fmt.Fprintf(&sb, " / 特徴: %s", strings.Join(char.VisualCues, ", "))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
