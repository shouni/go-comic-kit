package operations

import (
	"fmt"
	"strings"

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
	outlineData *ports.OutlinePromptData
	chapterData *ports.ChapterPromptData
	mode        string
}

func (f *fakeScriptPrompt) BuildOutline(mode string, data *ports.OutlinePromptData) (string, error) {
	f.mode, f.outlineData = mode, data
	return "FAKE-OUTLINE\n" + data.InputText, nil
}

func (f *fakeScriptPrompt) BuildChapterScript(mode string, data *ports.ChapterPromptData) (string, error) {
	f.mode, f.chapterData = mode, data
	return "FAKE-CHAPTER\n" + data.Chapter.ID, nil
}

// fakeDesignPrompt はデザインシートのプロンプト実装です。
type fakeDesignPrompt struct {
	data *ports.DesignSheetPromptData
}

func (f *fakeDesignPrompt) BuildDesignSheet(data *ports.DesignSheetPromptData) (string, string, string, error) {
	f.data = data
	return fakeSystemPrompt, "FAKE-DESIGN " + strings.Join(data.Descriptions, " / "), fakeNegativePrompt, nil
}

// fakePanelPrompt はパネルのプロンプト実装です。
type fakePanelPrompt struct {
	data       *ports.PanelPromptData
	editPrompt string
}

func (f *fakePanelPrompt) BuildPanel(data *ports.PanelPromptData) (string, string, string, error) {
	f.data = data
	return fakeSystemPrompt, fmt.Sprintf("FAKE-PANEL %s subjects=%v", data.Panel.ID, data.SubjectIDs), fakeNegativePrompt, nil
}

func (f *fakePanelPrompt) BuildPanelEdit(editPrompt string) (string, string, string, error) {
	f.editPrompt = editPrompt
	return fakeSystemPrompt, "FAKE-PANEL-EDIT " + editPrompt, fakeNegativePrompt, nil
}

// fakePagePrompt はページ合成のプロンプト実装です。
type fakePagePrompt struct {
	data       *ports.PagePromptData
	editPrompt string
}

func (f *fakePagePrompt) BuildPage(data *ports.PagePromptData) (string, string, string, error) {
	f.data = data
	ids := make([]string, 0, len(data.Panels))
	for _, p := range data.Panels {
		ids = append(ids, p.ID)
	}
	return fakeSystemPrompt, fmt.Sprintf("FAKE-PAGE panels=%v", ids), fakeNegativePrompt, nil
}

func (f *fakePagePrompt) BuildPageEdit(editPrompt string) (string, string, string, error) {
	f.editPrompt = editPrompt
	return fakeSystemPrompt, "FAKE-PAGE-EDIT " + editPrompt, fakeNegativePrompt, nil
}
