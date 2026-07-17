// Package prompts は、キット内蔵のプロンプトテンプレート（go:embed）と、その実行による
// プロンプト構築を提供します。templates/{outline,chapter}/ に .md ファイルを追加すると、
// ファイル名（拡張子除く）がそのままモード名になります。
// アプリ側で独自のプロンプトを使う場合は、ports.OutlinePrompt / ports.ChapterScriptPrompt を
// 実装して差し替えてください。
package prompts

import (
	"embed"
	"fmt"
	"path"
	"strings"
	"text/template"

	"github.com/shouni/go-comic-kit/ports"
)

//go:embed templates/outline/*.md templates/chapter/*.md
var templateFiles embed.FS

// ModeDefault は既定のテンプレートモード名です。
const ModeDefault = "default"

// ScriptPrompts は内蔵テンプレートによる ports.OutlinePrompt / ports.ChapterScriptPrompt の実装です。
type ScriptPrompts struct {
	outline map[string]*template.Template
	chapter map[string]*template.Template
}

var (
	_ ports.OutlinePrompt       = (*ScriptPrompts)(nil)
	_ ports.ChapterScriptPrompt = (*ScriptPrompts)(nil)
)

// NewScriptPrompts は内蔵テンプレートを読み込んで ScriptPrompts を初期化します。
func NewScriptPrompts() (*ScriptPrompts, error) {
	outline, err := loadTemplates("templates/outline")
	if err != nil {
		return nil, fmt.Errorf("章立てテンプレートの読み込みに失敗しました: %w", err)
	}
	chapter, err := loadTemplates("templates/chapter")
	if err != nil {
		return nil, fmt.Errorf("章台本テンプレートの読み込みに失敗しました: %w", err)
	}
	return &ScriptPrompts{outline: outline, chapter: chapter}, nil
}

// BuildOutline は指定モードのテンプレートで章立て生成プロンプトを構築します。
// mode が空の場合は default を使います。
func (p *ScriptPrompts) BuildOutline(mode string, data *ports.OutlinePromptData) (string, error) {
	return execute(p.outline, mode, data)
}

// BuildChapterScript は指定モードのテンプレートで章単位の台本生成プロンプトを構築します。
// mode が空の場合は default を使います。
func (p *ScriptPrompts) BuildChapterScript(mode string, data *ports.ChapterPromptData) (string, error) {
	return execute(p.chapter, mode, data)
}

// OutlineModes は利用可能な章立てテンプレートのモード名一覧を返します。
func (p *ScriptPrompts) OutlineModes() []string {
	return modeNames(p.outline)
}

// ChapterModes は利用可能な章台本テンプレートのモード名一覧を返します。
func (p *ScriptPrompts) ChapterModes() []string {
	return modeNames(p.chapter)
}

func modeNames(m map[string]*template.Template) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

func execute(templates map[string]*template.Template, mode string, data any) (string, error) {
	if mode == "" {
		mode = ModeDefault
	}
	tpl, ok := templates[mode]
	if !ok {
		return "", fmt.Errorf("プロンプトモード %q が見つかりません", mode)
	}
	var sb strings.Builder
	if err := tpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("プロンプトテンプレートの実行に失敗しました (mode: %s): %w", mode, err)
	}
	return sb.String(), nil
}

func loadTemplates(dir string) (map[string]*template.Template, error) {
	entries, err := templateFiles.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*template.Template, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		content, err := templateFiles.ReadFile(path.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		tpl, err := template.New(name).Parse(string(content))
		if err != nil {
			return nil, fmt.Errorf("テンプレート %s のパースに失敗しました: %w", entry.Name(), err)
		}
		result[name] = tpl
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("テンプレートが見つかりません (dir: %s)", dir)
	}
	return result, nil
}
