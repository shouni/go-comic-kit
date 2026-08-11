package ports

import "github.com/shouni/go-comic-kit/comic"

// OutlinePromptData は章立て生成プロンプトのテンプレートに渡すデータです。
type OutlinePromptData struct {
	// InputText は元文章です。
	InputText string
	// CharacterRoster は使用可能なキャラクターの一覧（箇条書きテキスト）です。
	CharacterRoster string
	// MaxChapters は章数の上限です。
	MaxChapters int
}

// ChapterPromptData は章単位の台本生成プロンプトのテンプレートに渡すデータです。
type ChapterPromptData struct {
	// WorkTitle / WorkDescription は作品全体の情報です。
	WorkTitle       string
	WorkDescription string
	// OutlineDigest は全章の一覧（ID・タイトル・要約の箇条書き）で、
	// 章をまたぐ流れの一貫性を保つための文脈として渡します。
	OutlineDigest string
	// Chapter は今回パネルを生成する対象の章です。
	Chapter comic.Chapter
	// CharacterRoster は使用可能なキャラクターの一覧（箇条書きテキスト）です。
	CharacterRoster string
	// MaxPanels は1章あたりのパネル数の上限です。
	MaxPanels int
}

// OutlinePrompt は章立て生成プロンプトを構築する契約です。
// キット内蔵のテンプレート実装（prompts パッケージ）を既定とし、アプリ側で差し替え可能です。
type OutlinePrompt interface {
	BuildOutline(mode string, data *OutlinePromptData) (string, error)
}

// ChapterScriptPrompt は章単位の台本生成プロンプトを構築する契約です。
type ChapterScriptPrompt interface {
	BuildChapterScript(mode string, data *ChapterPromptData) (string, error)
}

// DesignSheetPromptData はデザインシート生成プロンプトの組み立てに渡すデータです。
type DesignSheetPromptData struct {
	// Descriptions は対象キャラクターの記述（名前 + VisualCues）です。
	// 複数キャラクター（合成デザインシート）の場合は要素数が2以上になります。
	Descriptions []string
	// Layout に DesignLayoutSingleView を渡すと単一ポーズレイアウト、
	// 空文字なら3面図ターンアラウンドを意図します。
	Layout string
	// StyleMode は呼び出し側が指定した画風モード（DesignSheetRequest.StyleMode）です。
	// 空文字なら指定なしで、実装の既定に従います。キットは中身を解釈しません。
	StyleMode string
}

// DesignSheetPrompt はデザインシート生成のシステム/ユーザー/ネガティブプロンプトを
// 構築する契約です。生成物は他ワークフロー（パネル・ページ等）のキャラクター同一性
// アンカーとして参照されるため、実装は演出よりも正確さ・一貫性を優先すべきです。
type DesignSheetPrompt interface {
	BuildDesignSheet(data *DesignSheetPromptData) (systemPrompt, userPrompt, negativePrompt string, err error)
}

// PanelPromptData はパネル画像生成プロンプトの組み立てに渡すデータです。
type PanelPromptData struct {
	// Panel は対象のコマです（Shot / Setting / VisualAnchor / Characters）。
	Panel comic.Panel
	// Characters はキャラクター定義です。名前や visual_cues の解決に使います。
	Characters *comic.Characters
	// SubjectIDs は参照画像を添付したキャラクターIDで、**添付順**に並びます。
	// 実装はこの順序で参照番号を振ってください。順序がずれると、モデルは別人の
	// 参照画像を見ながら描くことになります。
	SubjectIDs []string
	// StyleMode は呼び出し側が指定した画風モード（GenerateOptions.StyleMode）です。
	// 空文字なら指定なしで、実装の既定に従います。キットは中身を解釈しません。
	//
	// 画風指定そのものではなくモード名を渡すのは、画風と「その画風で避けたいもの」
	// （ネガティブプロンプト）が対で決まり、両方を持っているのが実装側だからです。
	StyleMode string
}

// PanelPrompt はパネル画像生成のプロンプトを構築する契約です。
//
// キット内蔵の実装（prompts パッケージ）は、参照画像との同一性・文字を描かないこと・
// 指の破綻対策といった構造的な指示だけを持つ簡潔な既定です。作品ごとの作り込みは
// アプリ側でこのインターフェースを実装して差し替えてください。
type PanelPrompt interface {
	BuildPanel(data *PanelPromptData) (systemPrompt, userPrompt, negativePrompt string, err error)
	// BuildPanelEdit は、生成済みパネル画像に対する編集指示のプロンプトを構築します。
	// 構図・ポーズ・背景を保ったまま指示箇所だけを変更させるのが目的です。
	BuildPanelEdit(editPrompt string) (systemPrompt, userPrompt, negativePrompt string, err error)
}

// PagePromptData はページ合成プロンプトの組み立てに渡すデータです。
type PagePromptData struct {
	// Panels はこのページに載るコマです（表示順）。
	Panels []comic.Panel
	// Characters はキャラクター定義です。
	Characters *comic.Characters
	// CharacterFile は キャラクターID → 添付した参照画像の番号（1始まり）です。
	// 添付していないキャラクターは含まれません。
	CharacterFile map[string]int
	// PanelFile は パネルID → 添付した生成済みパネル画像の番号（1始まり）です。
	PanelFile map[string]int
	// StyleMode は呼び出し側が指定した画風モードです（PanelPromptData.StyleMode と同じ扱い）。
	StyleMode string
}

// PagePrompt はページ合成のプロンプトを構築する契約です。
//
// CharacterFile / PanelFile の番号は、実際に添付される画像の順序と一致します。
// プロンプト内で参照するときは必ずこの番号を使ってください。
type PagePrompt interface {
	BuildPage(data *PagePromptData) (systemPrompt, userPrompt, negativePrompt string, err error)
	// BuildPageEdit は、生成済みページ画像に対する編集指示のプロンプトを構築します。
	BuildPageEdit(editPrompt string) (systemPrompt, userPrompt, negativePrompt string, err error)
}
