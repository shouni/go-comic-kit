package operations

import (
	"fmt"
	"strings"

	"github.com/shouni/go-comic-kit/comic"
)

// characterRoster builds the character list (bulleted) injected into the prompt.
// Its text is prompt data sent to the model, so it stays Japanese.
func characterRoster(characters *comic.Characters) string {
	if characters.Len() == 0 {
		return "（キャラクター定義なし）"
	}
	var sb strings.Builder
	for _, char := range characters.All() {
		fmt.Fprintf(&sb, "- id: %s / 名前: %s", char.ID, char.Name)
		if len(char.VisualCues) > 0 {
			fmt.Fprintf(&sb, " / 特徴: %s", strings.Join(char.VisualCues, ", "))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
