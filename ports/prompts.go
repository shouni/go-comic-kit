package ports

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
	Chapter Chapter
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
