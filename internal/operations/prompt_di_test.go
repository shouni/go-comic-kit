package operations

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/ports"
)

// recordingPanelPrompt は、ランナーが渡す構造データを記録する ports.PanelPrompt です。
type recordingPanelPrompt struct {
	data *ports.PanelPromptData
	edit string
}

func (r *recordingPanelPrompt) BuildPanel(data *ports.PanelPromptData) (string, string, string, error) {
	r.data = data
	return "sys", "user", "neg", nil
}

func (r *recordingPanelPrompt) BuildPanelEdit(editPrompt string) (string, string, string, error) {
	r.edit = editPrompt
	return "sys", "edit-user", "neg", nil
}

// recordingPagePrompt は、ランナーが渡す構造データを記録する ports.PagePrompt です。
type recordingPagePrompt struct {
	data *ports.PagePromptData
}

func (r *recordingPagePrompt) BuildPage(data *ports.PagePromptData) (string, string, string, error) {
	r.data = data
	return "sys", "user", "neg", nil
}

func (r *recordingPagePrompt) BuildPageEdit(editPrompt string) (string, string, string, error) {
	return "sys", editPrompt, "neg", nil
}

// TestPanelPromptIsInjectable は、注入したプロンプト実装が使われ、参照画像の添付順が
// SubjectIDs としてそのまま渡ることを確認します。順序がずれると、モデルは別人の
// 参照画像を見ながら描くことになります。
func TestPanelPromptIsInjectable(t *testing.T) {
	t.Parallel()
	prompt := &recordingPanelPrompt{}
	r, gen, _ := newPanelRunner(t, prompt)

	if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{Model: "di-model"}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}

	if gen.lastReq.SystemPrompt != "sys" || gen.lastReq.Prompt != "user" || gen.lastReq.NegativePrompt != "neg" {
		t.Errorf("prompts = %q/%q/%q, want the injected implementation's output",
			gen.lastReq.SystemPrompt, gen.lastReq.Prompt, gen.lastReq.NegativePrompt)
	}
	if prompt.data == nil {
		t.Fatal("BuildPanel was not called")
	}
	if got := fmt.Sprint(prompt.data.SubjectIDs); got != "[zundamon metan]" {
		t.Errorf("SubjectIDs = %s, want attachment order [zundamon metan]", got)
	}
	if len(prompt.data.SubjectIDs) != len(gen.lastReq.Images) {
		t.Errorf("SubjectIDs = %d but %d images were attached; the numbering would not match",
			len(prompt.data.SubjectIDs), len(gen.lastReq.Images))
	}
}

// TestPagePromptReceivesAttachmentIndexes は、添付した画像の番号が実際の添付順と
// 一致してプロンプト実装へ渡ることを確認します。
func TestPagePromptReceivesAttachmentIndexes(t *testing.T) {
	t.Parallel()
	prompt := &recordingPagePrompt{}
	r, gen, _ := newPageRunner(t, prompt)

	if _, err := r.ComposePage(context.Background(), pageTestState(), 1, ports.GenerateOptions{Model: "di-model"}); err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}

	if prompt.data == nil {
		t.Fatal("BuildPage was not called")
	}
	if prompt.data.CharacterFile["zundamon"] != 1 || prompt.data.CharacterFile["metan"] != 2 {
		t.Errorf("CharacterFile = %v, want the attachment order", prompt.data.CharacterFile)
	}
	if prompt.data.PanelFile["ch01-p01"] != 3 {
		t.Errorf("PanelFile = %v, want the generated panel attached third", prompt.data.PanelFile)
	}
	// 番号は添付枚数の範囲に収まっていなければならない
	for id, idx := range prompt.data.CharacterFile {
		if idx < 1 || idx > len(gen.lastReq.Images) {
			t.Errorf("CharacterFile[%s] = %d, out of range for %d attachments", id, idx, len(gen.lastReq.Images))
		}
	}
}

// TestPanelPromptOverrideReplacesUserPromptOnly は、PromptOverride が本文だけを
// 置き換え、システム指示とネガティブプロンプトは実装のものが残ることを確認します。
func TestPanelPromptOverrideReplacesUserPromptOnly(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &recordingPanelPrompt{})

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01",
		ports.GenerateOptions{PromptOverride: "独自のプロンプト", Model: "di-model"})
	if err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}

	if gen.lastReq.Prompt != "独自のプロンプト" {
		t.Errorf("Prompt = %q, want the override", gen.lastReq.Prompt)
	}
	if gen.lastReq.SystemPrompt != "sys" || gen.lastReq.NegativePrompt != "neg" {
		t.Error("override must not replace the system or negative prompt")
	}
	if strings.TrimSpace(gen.lastReq.NegativePrompt) == "" {
		t.Error("negative prompt was lost")
	}
}
