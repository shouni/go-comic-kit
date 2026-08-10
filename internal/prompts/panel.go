package prompts

import (
	"fmt"
	"strings"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-comic-kit/ports"
)

const (
	// PanelSystemPrompt はパネル画像生成の既定システム指示です。
	// 参照画像との対応順、文字を描かないこと、指の破綻対策という構造的な制約だけを持ちます。
	PanelSystemPrompt = `You are a manga panel illustrator. Draw a single panel.
- Attached reference images correspond to [Subject N] in the prompt, in the same order. Preserve each subject's identity: hairstyle, hair color, eye color, outfit, accessories. Never mix identities between subjects.
- Draw every hand with exactly five fingers and correct limb proportions.
- Render no text, speech bubbles, sound effects, or logos.`

	// PanelNegativePrompt はパネル画像の既定ネガティブプロンプトです。
	PanelNegativePrompt = "speech bubble, text, letters, watermark, malformed hands, fused fingers, extra fingers, extra limbs, bad anatomy, low quality"

	// PanelEditInstruction は編集モードで構図の維持を指示する既定のプレフィックスです。
	PanelEditInstruction = "Edit the attached manga panel image. Keep the composition, character poses, background, and art style unchanged. Apply ONLY this change: "
)

// DefaultPanelPrompt は ports.PanelPrompt のキット内蔵実装です。
//
// 意図的に簡潔にしてあります。作品ごとの作り込み（画風の言い回し、演出語彙、
// モデル別の癖への対処）はアプリ側の関心で、キットが持つと更新のたびに
// キットのリリースが必要になるためです。差し替えは workflow.Args.PanelPrompt から行います。
type DefaultPanelPrompt struct{}

// BuildPanel はパネル画像生成のプロンプトを構築します。
func (DefaultPanelPrompt) BuildPanel(data *ports.PanelPromptData) (string, string, string, error) {
	if data == nil {
		return "", "", "", fmt.Errorf("panel prompt data is required")
	}

	var sb strings.Builder
	sb.WriteString("Manga panel illustration.")
	if data.Panel.Shot != "" {
		fmt.Fprintf(&sb, " Shot: %s.", data.Panel.Shot)
	}
	if data.Panel.Setting != "" {
		fmt.Fprintf(&sb, " Setting: %s.", data.Panel.Setting)
	}
	if anchor := strings.TrimSpace(data.Panel.VisualAnchor); anchor != "" {
		sb.WriteString("\nScene direction: ")
		sb.WriteString(anchor)
	}

	// 参照画像の添付順に [Subject N] を振る。番号がずれるとモデルは別人を見て描く。
	for i, id := range data.SubjectIDs {
		char := data.Characters.GetCharacter(id)
		if char == nil {
			continue
		}
		sb.WriteString("\n")
		sb.WriteString(subjectLine(i+1, char, findPanelCharacter(&data.Panel, id)))
	}

	if extras := backgroundExtras(&data.Panel); extras != "" {
		sb.WriteString("\nBackground extras (no reference, generic): ")
		sb.WriteString(extras)
	}
	if data.StyleSuffix != "" {
		sb.WriteString("\nStyle: ")
		sb.WriteString(data.StyleSuffix)
	}
	sb.WriteString("\nNo speech bubbles, no text.")

	return PanelSystemPrompt, sb.String(), PanelNegativePrompt, nil
}

// BuildPanelEdit は既存パネル画像への編集指示を構築します。
func (DefaultPanelPrompt) BuildPanelEdit(editPrompt string) (string, string, string, error) {
	return PanelSystemPrompt, PanelEditInstruction + editPrompt, PanelNegativePrompt, nil
}

// subjectLine は1キャラクター分の [Subject N] 記述を構築します。
func subjectLine(index int, char *comic.Character, pc *comic.PanelCharacter) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Subject %d: %s", index, char.Name)
	if len(char.VisualCues) > 0 {
		fmt.Fprintf(&sb, " (%s)", strings.Join(char.VisualCues, ", "))
	}
	sb.WriteString("]")
	if pc == nil {
		return sb.String()
	}
	if pc.Emotion != "" {
		fmt.Fprintf(&sb, " emotion: %s.", pc.Emotion)
	}
	if pc.Action != "" {
		fmt.Fprintf(&sb, " action: %s.", pc.Action)
	}
	if pc.Position != "" {
		fmt.Fprintf(&sb, " position: %s.", pc.Position)
	}
	return sb.String()
}

// findPanelCharacter はパネル内のキャラクター指定を ID で引きます。
func findPanelCharacter(panel *comic.Panel, charID string) *comic.PanelCharacter {
	for i := range panel.Characters {
		if panel.Characters[i].CharacterID == charID {
			return &panel.Characters[i]
		}
	}
	return nil
}

// backgroundExtras は background（モブ）キャラクターの一覧を返します。
func backgroundExtras(panel *comic.Panel) string {
	var extras []string
	for i := range panel.Characters {
		pc := &panel.Characters[i]
		if pc.Prominence != comic.ProminenceBackground {
			continue
		}
		extras = append(extras, backgroundExtraDesc(pc))
	}
	return strings.Join(extras, ", ")
}

// backgroundExtraDesc は background キャラクター1人分の記述を構築します。
func backgroundExtraDesc(pc *comic.PanelCharacter) string {
	desc := pc.CharacterID
	if pc.Action != "" {
		desc += " (" + pc.Action + ")"
	}
	return desc
}
