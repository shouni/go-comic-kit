package runner

import (
	"context"
	"strings"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
	"github.com/shouni/go-comic-kit/prompts"
)

const chapterJSON = `{
  "panels": [
    {
      "shot": "wide",
      "setting": "放課後の音楽室",
      "visual_anchor": "sunset classroom, no speech bubbles, no text",
      "characters": [
        {"character_id": "zundamon", "prominence": "primary", "emotion": "驚き", "position": "left"},
        {"character_id": "unknown-hero", "prominence": "secondary", "action": "腕を組む"},
        {"character_id": "mob", "prominence": "background"}
      ],
      "dialogues": [
        {"speaker_id": "zundamon", "text": "なんなのだ！？", "kind": "shout"},
        {"text": "その日、すべてが変わった", "kind": "narration"}
      ]
    },
    {
      "shot": "close-up",
      "visual_anchor": "silent beat, no text",
      "characters": [{"character_id": "metan", "prominence": "primary"}],
      "dialogues": []
    }
  ]
}`

func outlineState() *ports.MangaState {
	return &ports.MangaState{
		Version:     ports.StateSchemaVersion,
		Title:       "夜明けのデプロイ",
		Description: "あらすじ",
		Chapters: []ports.Chapter{
			{ID: "ch01", Title: "導入", Summary: "つかみ", SourceExcerpt: "抜粋1"},
			{ID: "ch02", Title: "核心", Summary: "本題", SourceExcerpt: "抜粋2"},
		},
	}
}

func newChapterRunner(t *testing.T, ai *fakeContentGenerator) *ChapterScriptRunner {
	t.Helper()
	p, err := prompts.NewScriptPrompts()
	if err != nil {
		t.Fatalf("NewScriptPrompts failed: %v", err)
	}
	cm, err := characterkit.NewCharacters([]ports.Character{
		{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://b/z.png", VisualCues: []string{"green hair"}, IsDefault: true},
		{ID: "metan", Name: "めたん", ReferenceURL: "gs://b/m.png", VisualCues: []string{"purple hair"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	return NewChapterScriptRunner(p, ai, cm, "test-model", 0, 0)
}

func TestGenerateChapterScriptAssignsIDsAndReplacesPanels(t *testing.T) {
	t.Parallel()

	ai := &fakeContentGenerator{text: "```json\n" + chapterJSON + "\n```"}
	r := newChapterRunner(t, ai)
	state := outlineState()
	// 既存パネルが置き換わることの検証用
	state.Panels = []ports.Panel{{ID: "ch01-p01", ChapterID: "ch01", Shot: "old"}}

	state, err := r.GenerateChapterScript(context.Background(), state, "ch01")
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	if len(state.Panels) != 2 {
		t.Fatalf("Panels = %d, want 2 (old panel replaced)", len(state.Panels))
	}
	p1 := state.Panels[0]
	if p1.ID != "ch01-p01" || p1.ChapterID != "ch01" || p1.Shot != "wide" {
		t.Errorf("panel[0] = %+v, want re-assigned ID ch01-p01 with new content", p1)
	}
	if state.Panels[1].ID != "ch01-p02" {
		t.Errorf("panel[1].ID = %q, want ch01-p02", state.Panels[1].ID)
	}
	if p1.Page != 1 {
		t.Errorf("panel[0].Page = %d, want repaginated to 1", p1.Page)
	}

	ch := state.ChapterByID("ch01")
	if len(ch.PanelIDs) != 2 || ch.PanelIDs[0] != "ch01-p01" {
		t.Errorf("chapter PanelIDs = %v, want new panel IDs", ch.PanelIDs)
	}

	// ナレーション・複数吹き出しが保持される
	if len(p1.Dialogues) != 2 || p1.Dialogues[1].Kind != ports.DialogueKindNarration {
		t.Errorf("Dialogues = %+v, want narration preserved", p1.Dialogues)
	}

	// プロンプトに章立て文脈とキャラクター一覧が入る
	if !strings.Contains(ai.lastPrompt, "▶ ch01") || !strings.Contains(ai.lastPrompt, "ch02") {
		t.Error("prompt does not contain outline digest with target marker")
	}
	if !strings.Contains(ai.lastPrompt, "ずんだもん") {
		t.Error("prompt does not contain character roster")
	}
	// 構造化出力オプションの検証
	if ai.lastOpts.ResponseMIMEType != "application/json" || ai.lastOpts.ResponseSchema == nil {
		t.Errorf("opts = %+v, want application/json with ResponseSchema", ai.lastOpts)
	}
}

func TestCleanJSONResponse(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		input string
		want  string
	}{
		"plain":          {`{"a":1}`, `{"a":1}`},
		"fenced":         {"```json\n{\"a\":1}\n```", `{"a":1}`},
		"trailing noise": {`{"a":1} 以上が結果です。`, `{"a":1}`},
		"brace in string": {
			`{"text":"セリフに } が入っても壊れない"} trailing`,
			`{"text":"セリフに } が入っても壊れない"}`,
		},
		"wrong closer": {`{"a":1)`, `{"a":1}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := cleanJSONResponse(tc.input); got != tc.want {
				t.Errorf("cleanJSONResponse(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestGenerateChapterScriptDemotesUnknownCharacters(t *testing.T) {
	t.Parallel()

	ai := &fakeContentGenerator{text: chapterJSON}
	r := newChapterRunner(t, ai)

	state, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01")
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	chars := state.Panels[0].Characters
	if len(chars) != 3 {
		t.Fatalf("Characters = %+v, want 3", chars)
	}
	// 未定義IDは background に降格（デフォルトキャラへの暗黙フォールバック事故を防ぐ）
	if chars[1].CharacterID != "unknown-hero" || chars[1].Prominence != ports.ProminenceBackground {
		t.Errorf("unknown character = %+v, want demoted to background", chars[1])
	}
	// 既知IDはそのまま
	if chars[0].Prominence != ports.ProminencePrimary {
		t.Errorf("known character = %+v, want prominence preserved", chars[0])
	}
	// 参照対象は既知キャラだけになる
	ref := state.Panels[0].ReferencedCharacterIDs()
	if len(ref) != 1 || ref[0] != "zundamon" {
		t.Errorf("ReferencedCharacterIDs = %v, want [zundamon]", ref)
	}
}

func TestGenerateChapterScriptUnknownChapterFails(t *testing.T) {
	t.Parallel()

	r := newChapterRunner(t, &fakeContentGenerator{text: chapterJSON})
	if _, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch99"); err == nil {
		t.Error("GenerateChapterScript(ch99) succeeded, want error")
	}
}

func TestGenerateChapterScriptNilStateFails(t *testing.T) {
	t.Parallel()

	r := newChapterRunner(t, &fakeContentGenerator{text: chapterJSON})
	if _, err := r.GenerateChapterScript(context.Background(), nil, "ch01"); err == nil {
		t.Error("GenerateChapterScript(nil state) succeeded, want error")
	}
}

func TestGenerateChapterScriptEmptyPanelsFails(t *testing.T) {
	t.Parallel()

	r := newChapterRunner(t, &fakeContentGenerator{text: `{"panels":[]}`})
	if _, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01"); err == nil {
		t.Error("GenerateChapterScript with empty panels succeeded, want error")
	}
}
