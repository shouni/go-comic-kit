package operations

// promptSet は1回の画像生成に渡すプロンプト3点です。
//
// システム・ユーザー・ネガティブは常に同じ実装（ports.PanelPrompt / ports.PagePrompt）が
// 組で返すものなので、3つの戻り値をバラバラに持ち回らずまとめて扱います。
type promptSet struct {
	system   string
	user     string
	negative string
}

// newPromptSet は ports の Build 系メソッドの戻り値をそのまま受け取ります。
func newPromptSet(system, user, negative string, err error) (promptSet, error) {
	if err != nil {
		return promptSet{}, err
	}
	return promptSet{system: system, user: user, negative: negative}, nil
}

// withOverride は、呼び出し側がユーザープロンプトを差し替えた場合のみ置き換えます
// （GenerateOptions.PromptOverride）。システム指示とネガティブプロンプトは
// モデルの出力形式を保つためのものなので置き換えません。
func (p promptSet) withOverride(override string) promptSet {
	if override != "" {
		p.user = override
	}
	return p
}
