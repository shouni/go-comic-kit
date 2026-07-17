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
	// 全ビューが同一キャラクターであることと、衣装が隠れないニュートラルな
	// Aポーズを明示し、面ごとの細部ブレを抑えます。
	designLayoutMultiView = "multiple views (front, side, back) of the same character, standing full body in a neutral A-pose with arms held slightly away from the body so the costume stays fully visible, views arranged side-by-side and evenly spaced, separate character charts"
	// designLayoutSingleView は、他の生成物（キーフレーム、カバーアート等）の参照アンカーとして
	// 使うための、単一ポーズ・正面向きのレイアウトです。3面図シートは複数ポーズが1枚の画像に
	// 混在するため、それと異なるアスペクト比の生成先の参照に使うと色・小物配置・髪型などの
	// 細部がブレやすい問題があり、単一ポーズはそのアンカー用途に特化したオプションです。
	designLayoutSingleView = "single view, front-facing, standing full body in a neutral relaxed pose, centered composition, the entire body from head to toe inside the frame"

	designLayoutPromptFormat = "Layout: %s"

	// designSystemPromptDefault はデザインシート生成時にモデルへ与える既定のシステム指示です。
	// 生成物は他ワークフロー（カバーアート、キーフレーム、パネル等）のキャラクター同一性
	// アンカーとして参照されるため、演出的な絵作りよりも正確さ・一貫性を最優先させます。
	designSystemPromptDefault = `You are a professional character designer creating official model sheets for animation and manga production.
This sheet is the canonical identity reference that other artists and AI generators will rely on, so accuracy and consistency outweigh artistic flair:
- Anatomical correctness is critical. Draw every hand with exactly five fingers, correct limb proportions, and clean readable silhouettes.
- Every view on the sheet must depict the SAME character with identical hairstyle, hair color, eye color, skin tone, outfit, and accessories.
- Use flat, even, neutral studio lighting only. No dramatic shadows, rim light, lens flares, or color grading — lighting baked into this sheet contaminates every downstream generation that references it.
- The full body must be visible from head to toe and must never be cropped by the frame.
- Render absolutely no text, labels, arrows, color swatches, logos, or annotations of any kind.`

	// designNegativePromptDefault はデザインシートに含めたくない要素を指定する既定の負のプロンプトです。
	// 指の本数・手の崩れ対策と、シート特有の文字注釈・スウォッチ混入対策を含みます。
	designNegativePromptDefault = "text, labels, annotations, arrows, color swatches, watermark, logo, signature, malformed hands, fused fingers, extra fingers, missing fingers, extra limbs, deformed anatomy, asymmetrical eyes, cropped body, cut-off feet, dramatic lighting, strong shadows, rim light, lens flare, inconsistent details between views, different character per view, background scenery, props, low quality, blurry"
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
	designLayout := designLayoutMultiView
	if data.Layout == ports.DesignLayoutSingleView {
		designLayout = designLayoutSingleView
	}
	layoutPrompt := fmt.Sprintf(designLayoutPromptFormat, designLayout)

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
