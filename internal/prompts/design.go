package prompts

import (
	"fmt"
	"strings"

	"github.com/shouni/go-comic-kit/ports"
)

const (
	// designPromptBaseTemplate はデザインシートプロンプトの基本形です。
	designPromptBaseTemplate = "Masterpiece character design sheet of %s"

	// designLayoutMultiView は既定のターンアラウンド（前・横・後の3面図）レイアウトです。
	// 「同一スケール・共通の接地線・1体ずつ完結した全身」を明示し、体がパーツ分割されたり
	// ビュー間で分裂・融合したりする生成崩れを抑えます。
	designLayoutMultiView = "a character turnaround with exactly three views of the same character — front view, side view, and back view — each view standing full body in a neutral A-pose with arms held slightly away from the body so the costume stays fully visible, the three views placed side by side at identical scale on a shared ground line, each view drawn as one complete connected figure from head to toe"
	// designLayoutMultiViewMultiChar は複数キャラクター合成シート用の3面図レイアウトです。
	// キャラクターごとにビューを1つのグループにまとめさせ、キャラクター間の特徴の
	// 混同・融合を明示的に禁止します。
	designLayoutMultiViewMultiChar = "for every character exactly three views — front view, side view, and back view — standing full body in a neutral A-pose with arms held slightly away from the body so the costume stays fully visible, each character's three views grouped together in its own horizontal row, all figures at identical scale on a shared ground line, each view drawn as one complete connected figure from head to toe, never blending or mixing features between different characters"
	// designLayoutSingleView は、他の生成物（キーフレーム、カバーアート等）の参照アンカーとして
	// 使うための、単一ポーズ・正面向きのレイアウトです。3面図シートは複数ポーズが1枚の画像に
	// 混在するため、それと異なるアスペクト比の生成先の参照に使うと色・小物配置・髪型などの
	// 細部がブレやすい問題があり、単一ポーズはそのアンカー用途に特化したオプションです。
	designLayoutSingleView = "single view, front-facing, standing full body in a neutral relaxed pose, centered composition, the entire body from head to toe inside the frame"
	// designLayoutSingleViewMultiChar は複数キャラクター合成シート用の単一ポーズレイアウトです。
	designLayoutSingleViewMultiChar = "single view, front-facing, all characters standing side by side full body in a neutral relaxed pose at identical scale on a shared ground line, centered composition, every body entirely inside the frame from head to toe, never blending or mixing features between different characters"

	designLayoutPromptFormat = "Layout: %s"

	// designSystemPromptDefault はデザインシート生成時にモデルへ与える既定のシステム指示です。
	// 生成物は他ワークフロー（カバーアート、キーフレーム、パネル等）のキャラクター同一性
	// アンカーとして参照されるため、演出的な絵作りよりも正確さ・一貫性を最優先させます。
	designSystemPromptDefault = `You are a professional character designer creating official model sheets for animation and manga production.
This sheet is the canonical identity reference that other artists and AI generators will rely on, so accuracy and consistency outweigh artistic flair:
- Anatomical correctness is critical. Draw every hand with exactly five fingers, correct limb proportions, and clean readable silhouettes.
- Draw each figure as one complete, physically connected body: head, torso, both arms, and both legs attached in a single continuous silhouette. Never split a figure into pieces, and never add close-up detail insets, detached limbs, floating heads, or partial bodies.
- Every view of a character must depict the SAME character with identical hairstyle, hair color, eye color, skin tone, outfit, and accessories.
- Use flat, even, neutral studio lighting only. No dramatic shadows, rim light, lens flares, or color grading — lighting baked into this sheet contaminates every downstream generation that references it.
- The full body must be visible from head to toe and must never be cropped by the frame.
- Render absolutely no text, labels, arrows, color swatches, logos, or annotations of any kind.`

	// designNegativePromptDefault はデザインシートに含めたくない要素の既定の指定です。
	// 注意: Gemini 系画像モデルには負条件付けのAPIチャネルがなく、ネガティブプロンプトは
	// 平文としてプロンプト末尾に連結されます。そのため "extra limbs" や "fused fingers" の
	// ような欠陥語彙を並べると通常のプロンプトトークンとして作用し、かえってその崩れを
	// 誘発します。解剖学的な品質はシステムプロンプト側で肯定形で指示し、ここでは
	// 指示追従モデルが解釈できる「含めてはならない内容物」の列挙のみに留めます。
	designNegativePromptDefault = "Do not include any of the following in the image: text, letters, labels, annotations, arrows, diagrams, color swatches, watermarks, logos, signatures, speech bubbles, background scenery or background objects, extra duplicate figures, close-up detail insets, dramatic lighting, strong shadows, rim light, lens flare, color grading, blur"
)

// DefaultDesignPrompt は、go-comic-kit 内蔵のデザインシートプロンプト実装です。
// アプリ側で ports.DesignSheetPrompt を実装すれば完全に差し替えられます。
type DefaultDesignPrompt struct{}

var _ ports.DesignSheetPrompt = DefaultDesignPrompt{}

// BuildDesignSheet はキャラクターデザインシート生成用のシステム/ユーザー/ネガティブプロンプトを
// 構築します。data.Layout に ports.DesignLayoutSingleView を渡すと単一ポーズレイアウトになります。
func (DefaultDesignPrompt) BuildDesignSheet(data *ports.DesignSheetPromptData) (systemPrompt, userPrompt, negativePrompt string, err error) {
	if data == nil || len(data.Descriptions) == 0 {
		return "", "", "", fmt.Errorf("description is required to build a design sheet prompt")
	}

	numChars := len(data.Descriptions)
	var subjects string
	if numChars > 1 {
		subjectParts := make([]string, numChars)
		for i, d := range data.Descriptions {
			subjectParts[i] = fmt.Sprintf("[Subject %d: %s]", i+1, d)
		}
		subjects = fmt.Sprintf("%d DIFFERENT characters: %s", numChars, strings.Join(subjectParts, " "))
	} else {
		subjects = data.Descriptions[0]
	}

	base := fmt.Sprintf(designPromptBaseTemplate, subjects)
	layoutPrompt := fmt.Sprintf(designLayoutPromptFormat, designLayout(numChars, data.Layout))

	promptParts := []string{base, layoutPrompt}
	if data.StyleSuffix != "" {
		promptParts = append(promptParts, data.StyleSuffix)
	}
	// StyleSuffix に演出用の指定が紛れ込んでも、参照アンカーとしての制約
	// （フラットな照明・白背景・手の正確さ）を後置して優先させる。
	promptParts = append(promptParts,
		"plain uniform white studio background",
		"flat even neutral lighting",
		"sharp focus",
		"perfectly drawn hands with five fingers per hand",
	)

	userPrompt = strings.Join(promptParts, ", ")
	return designSystemPromptDefault, userPrompt, designNegativePromptDefault, nil
}

// designLayout は被写体数とレイアウト指定に応じたレイアウト文を返します。
// 複数キャラクターの合成シートでは「same character」の3面図文がそのまま使われると
// 被写体指定（N DIFFERENT characters）と矛盾して融合・分裂を誘発するため、
// キャラクターごとのグループ化を指示する専用の文言に切り替えます。
func designLayout(numChars int, layout string) string {
	if layout == ports.DesignLayoutSingleView {
		if numChars > 1 {
			return designLayoutSingleViewMultiChar
		}
		return designLayoutSingleView
	}
	if numChars > 1 {
		return designLayoutMultiViewMultiChar
	}
	return designLayoutMultiView
}
