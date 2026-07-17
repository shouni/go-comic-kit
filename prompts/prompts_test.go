package prompts

import (
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/ports"
)

func TestNewScriptPromptsLoadsDefaults(t *testing.T) {
	t.Parallel()

	p, err := NewScriptPrompts()
	if err != nil {
		t.Fatalf("NewScriptPrompts failed: %v", err)
	}

	hasDefault := func(modes []string) bool {
		for _, m := range modes {
			if m == ModeDefault {
				return true
			}
		}
		return false
	}
	if !hasDefault(p.OutlineModes()) {
		t.Errorf("OutlineModes = %v, want to contain %q", p.OutlineModes(), ModeDefault)
	}
	if !hasDefault(p.ChapterModes()) {
		t.Errorf("ChapterModes = %v, want to contain %q", p.ChapterModes(), ModeDefault)
	}
}

func TestBuildOutlineInjectsData(t *testing.T) {
	t.Parallel()

	p, err := NewScriptPrompts()
	if err != nil {
		t.Fatalf("NewScriptPrompts failed: %v", err)
	}

	got, err := p.BuildOutline("", &ports.OutlinePromptData{
		InputText:       "ここに元文章が入ります",
		CharacterRoster: "- id: zundamon / 名前: ずんだもん",
		MaxChapters:     5,
	})
	if err != nil {
		t.Fatalf("BuildOutline failed: %v", err)
	}
	for _, want := range []string{"ここに元文章が入ります", "zundamon", "最大 5 章", "source_excerpt"} {
		if !strings.Contains(got, want) {
			t.Errorf("outline prompt does not contain %q", want)
		}
	}
}

func TestBuildChapterScriptInjectsData(t *testing.T) {
	t.Parallel()

	p, err := NewScriptPrompts()
	if err != nil {
		t.Fatalf("NewScriptPrompts failed: %v", err)
	}

	got, err := p.BuildChapterScript("", &ports.ChapterPromptData{
		WorkTitle:       "夜明けのデプロイ",
		WorkDescription: "あらすじ",
		OutlineDigest:   "▶ ch01: 導入 — つかみ",
		Chapter: ports.Chapter{
			ID:            "ch01",
			Title:         "導入",
			Summary:       "つかみ",
			SourceExcerpt: "元文章の抜粋",
		},
		CharacterRoster: "- id: metan / 名前: めたん",
		MaxPanels:       6,
	})
	if err != nil {
		t.Fatalf("BuildChapterScript failed: %v", err)
	}
	for _, want := range []string{"夜明けのデプロイ", "ch01", "元文章の抜粋", "metan"} {
		if !strings.Contains(got, want) {
			t.Errorf("chapter prompt does not contain %q", want)
		}
	}
	if strings.Contains(got, "{{") {
		t.Error("chapter prompt contains unexpanded template markers")
	}
}

func TestBuildOutlineUnknownModeFails(t *testing.T) {
	t.Parallel()

	p, err := NewScriptPrompts()
	if err != nil {
		t.Fatalf("NewScriptPrompts failed: %v", err)
	}
	if _, err := p.BuildOutline("no-such-mode", &ports.OutlinePromptData{}); err == nil {
		t.Error("BuildOutline(no-such-mode) succeeded, want error")
	}
}
