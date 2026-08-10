package operations

import (
	"fmt"
	"strings"
	"sync"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-comic-kit/ports"
)

// 本ファイルのフェイクは、キットが**プロンプト本文を持たない**ことの裏返しです。
// 文言はアプリ側の実装が持つので、ここで確かめるのは「操作がプロンプト実装へ
// 何を渡したか」と「返ってきた3本をそのまま生成リクエストへ載せたか」だけです。

const (
	fakeSystemPrompt   = "FAKE-SYSTEM"
	fakeNegativePrompt = "FAKE-NEGATIVE"
)

// fakeScriptPrompt は台本系（章立て・章台本）のプロンプト実装です。
type fakeScriptPrompt struct {
	mu          sync.Mutex
	outlineData *ports.OutlinePromptData
	chapterData *ports.ChapterPromptData
	mode        string
}

func (f *fakeScriptPrompt) BuildOutline(mode string, data *ports.OutlinePromptData) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode, f.outlineData = mode, data
	return "FAKE-OUTLINE\n" + data.InputText, nil
}

func (f *fakeScriptPrompt) BuildChapterScript(mode string, data *ports.ChapterPromptData) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode, f.chapterData = mode, data
	return "FAKE-CHAPTER\n" + data.Chapter.ID, nil
}

// fakeDesignPrompt はデザインシートのプロンプト実装です。
type fakeDesignPrompt struct {
	mu   sync.Mutex
	data *ports.DesignSheetPromptData
}

func (f *fakeDesignPrompt) BuildDesignSheet(data *ports.DesignSheetPromptData) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = data
	return fakeSystemPrompt, "FAKE-DESIGN " + strings.Join(data.Descriptions, " / "), fakeNegativePrompt, nil
}

// fakePanelPrompt はパネルのプロンプト実装です。
// 一括生成はコマ／ページを並列に処理するため、記録はロックで守ります。
type fakePanelPrompt struct {
	mu         sync.Mutex
	data       *ports.PanelPromptData
	editPrompt string
}

func (f *fakePanelPrompt) BuildPanel(data *ports.PanelPromptData) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = data
	return fakeSystemPrompt, fmt.Sprintf("FAKE-PANEL %s anchor=%s setting=%s subjects=%v",
		data.Panel.ID, data.Panel.VisualAnchor, data.Panel.Setting, data.SubjectIDs), fakeNegativePrompt, nil
}

func (f *fakePanelPrompt) BuildPanelEdit(editPrompt string) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editPrompt = editPrompt
	return fakeSystemPrompt, "FAKE-PANEL-EDIT " + editPrompt, fakeNegativePrompt, nil
}

// fakePagePrompt はページ合成のプロンプト実装です。
type fakePagePrompt struct {
	mu         sync.Mutex
	data       *ports.PagePromptData
	editPrompt string
}

func (f *fakePagePrompt) BuildPage(data *ports.PagePromptData) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = data
	ids := make([]string, 0, len(data.Panels))
	for _, p := range data.Panels {
		ids = append(ids, p.ID)
	}
	anchors := make([]string, 0, len(data.Panels))
	for _, p := range data.Panels {
		anchors = append(anchors, p.VisualAnchor)
	}
	return fakeSystemPrompt, fmt.Sprintf("FAKE-PAGE panels=%v anchors=%v", ids, anchors), fakeNegativePrompt, nil
}

func (f *fakePagePrompt) BuildPageEdit(editPrompt string) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editPrompt = editPrompt
	return fakeSystemPrompt, "FAKE-PAGE-EDIT " + editPrompt, fakeNegativePrompt, nil
}

// panelIDs は、プロンプト実装へ渡ったコマ一覧の ID を返します。
func panelIDs(panels []comic.Panel) []string {
	ids := make([]string, 0, len(panels))
	for _, p := range panels {
		ids = append(ids, p.ID)
	}
	return ids
}
