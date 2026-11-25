package operations

import (
	"fmt"
	"slices"
	"strings"

	"github.com/shouni/go-comic-kit/comic"
)

// characterRoster builds the character list (bulleted) injected into the prompt.
// Its text is prompt data sent to the model, so it stays Japanese.
//
// A non-empty cast narrows the list to those characters. The model is shown only who
// the work may use: a roster that also lists everyone else leaves "use only these two"
// to a sentence in the prompt, which the model may not follow.
func characterRoster(characters *comic.Characters, cast []string) string {
	if characters.Len() == 0 {
		return "（キャラクター定義なし）"
	}
	var sb strings.Builder
	for _, char := range characters.All() {
		if len(cast) > 0 && !slices.Contains(cast, char.ID) {
			continue
		}
		fmt.Fprintf(&sb, "- id: %s / 名前: %s", char.ID, char.Name)
		if len(char.VisualCues) > 0 {
			fmt.Fprintf(&sb, " / 特徴: %s", strings.Join(char.VisualCues, ", "))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
