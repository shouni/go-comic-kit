package prompts

import (
	"fmt"
	"strings"

	"github.com/shouni/go-comic-kit/ports"
)

const (
	// PageSystemPrompt はページ合成の既定システム指示です。
	// コマ数・読み順・参照画像との同一性という、崩れると作品が成立しない制約だけを持ちます。
	PageSystemPrompt = `You are a manga artist composing one page from the given panels.
- Use exactly the specified number of panels. Do not add extra or decorative frames.
- Reading order is right-to-left, then top-to-bottom.
- Character identity must match the referenced input files.`

	// PageNegativePrompt はページ画像の既定ネガティブプロンプトです。
	PageNegativePrompt = "watermark, signature, bad anatomy, extra fingers, extra panels, more panels than specified"

	// PageEditInstruction は編集モードでレイアウトの維持を指示する既定のプレフィックスです。
	PageEditInstruction = "Edit the attached manga page image. Keep the panel layout, compositions, dialogue balloons, and art style unchanged. Apply ONLY this change: "
)

// DefaultPagePrompt は ports.PagePrompt のキット内蔵実装です。
//
// DefaultPanelPrompt と同じ方針で、コマの配置・参照番号・セリフといった構造情報を
// 淡々と並べるだけの簡潔な既定です。ページ演出の作り込みはアプリ側で
// workflow.Args.PagePrompt を差し替えて行ってください。
type DefaultPagePrompt struct{}

// BuildPage はページ合成のプロンプトを構築します。
func (DefaultPagePrompt) BuildPage(data *ports.PagePromptData) (string, string, string, error) {
	if data == nil {
		return "", "", "", fmt.Errorf("page prompt data is required")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Compose one manga page with exactly %d panel(s), right-to-left reading order.\n", len(data.Panels))
	if data.StyleSuffix != "" {
		fmt.Fprintf(&sb, "Style: %s\n", data.StyleSuffix)
	}

	for i := range data.Panels {
		panel := &data.Panels[i]
		fmt.Fprintf(&sb, "\nPanel %d:\n", i+1)
		if panel.Shot != "" {
			fmt.Fprintf(&sb, "- shot: %s\n", panel.Shot)
		}
		if panel.Setting != "" {
			fmt.Fprintf(&sb, "- setting: %s\n", panel.Setting)
		}
		if anchor := strings.TrimSpace(panel.VisualAnchor); anchor != "" {
			fmt.Fprintf(&sb, "- scene: %s\n", anchor)
		}
		if idx, ok := data.PanelFile[panel.ID]; ok {
			fmt.Fprintf(&sb, "- composition guide: input_file_%d\n", idx)
		}
		writePageCharacters(&sb, panel, data)
		writePageDialogues(&sb, panel, data.Characters)
	}

	return PageSystemPrompt, strings.TrimRight(sb.String(), "\n"), PageNegativePrompt, nil
}

// BuildPageEdit は既存ページ画像への編集指示を構築します。
func (DefaultPagePrompt) BuildPageEdit(editPrompt string) (string, string, string, error) {
	return PageSystemPrompt, PageEditInstruction + editPrompt, PageNegativePrompt, nil
}

// writePageCharacters は登場キャラクターと、その参照画像の番号を出力します。
func writePageCharacters(sb *strings.Builder, panel *ports.Panel, data *ports.PagePromptData) {
	for i := range panel.Characters {
		pc := &panel.Characters[i]
		if pc.Prominence == ports.ProminenceBackground {
			fmt.Fprintf(sb, "- background extra: %s\n", backgroundExtraDesc(pc))
			continue
		}
		char := data.Characters.GetCharacter(pc.CharacterID)
		if char == nil {
			continue
		}
		if idx, ok := data.CharacterFile[pc.CharacterID]; ok {
			fmt.Fprintf(sb, "- character: %s (identity from input_file_%d)", char.Name, idx)
		} else {
			fmt.Fprintf(sb, "- character: %s", char.Name)
		}
		var traits []string
		if pc.Emotion != "" {
			traits = append(traits, "emotion: "+pc.Emotion)
		}
		if pc.Action != "" {
			traits = append(traits, "action: "+pc.Action)
		}
		if pc.Position != "" {
			traits = append(traits, "position: "+pc.Position)
		}
		if len(traits) > 0 {
			fmt.Fprintf(sb, " — %s", strings.Join(traits, ", "))
		}
		sb.WriteString("\n")
	}
}

// writePageDialogues はセリフを話者名付きで出力します。話者不明はナレーション扱いです。
func writePageDialogues(sb *strings.Builder, panel *ports.Panel, characters *ports.Characters) {
	for i := range panel.Dialogues {
		line := &panel.Dialogues[i]
		if strings.TrimSpace(line.Text) == "" {
			continue
		}
		if name := speakerName(characters, line.SpeakerID); name != "" {
			fmt.Fprintf(sb, "- dialogue (%s): %s\n", name, line.Text)
			continue
		}
		fmt.Fprintf(sb, "- narration: %s\n", line.Text)
	}
}

// speakerName は話者IDから表示名を引きます。空IDや未知IDは空文字列（ナレーション）です。
func speakerName(characters *ports.Characters, speakerID string) string {
	if speakerID == "" || characters == nil {
		return ""
	}
	if char := characters.GetCharacter(speakerID); char != nil {
		return char.Name
	}
	return ""
}
