package operations

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/comic"
	"github.com/shouni/go-comic-kit/ports"

	characterkit "github.com/shouni/go-character-kit/character"
)

// This file covers the choices that belong to one work rather than to the deployment:
// its cast, its language, its ratio, its panel limit, sung lines, and the supplementary
// sheets a panel may borrow.

func workCast(t *testing.T) *comic.Characters {
	t.Helper()
	cm, err := characterkit.NewCharacters([]comic.Character{
		{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://b/z.png", VisualCues: []string{"green hair"}, IsDefault: true},
		{ID: "sora", Name: "Sora", ReferenceURL: "gs://b/a.png", VisualCues: []string{"short silver hair"}},
		{ID: "umi", Name: "Umi", ReferenceURL: "gs://b/h.png", VisualCues: []string{"long navy hair"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	return cm
}

func TestGenerateOutlineRecordsTheWorkChoices(t *testing.T) {
	t.Parallel()
	ai := &fakeContentGenerator{text: outlineJSON}
	p := &fakeScriptPrompt{}
	r := NewOutlineRunner(p, ai, nil, workCast(t), 0)

	state, err := r.GenerateOutline(context.Background(), ports.OutlineRequest{
		SourceText:   "歌詞",
		Model:        "text-model",
		CharacterIDs: []string{"sora", " umi ", "sora", ""},
		Lang:         "en",
		AspectRatio:  "9:16",
	})
	if err != nil {
		t.Fatalf("GenerateOutline failed: %v", err)
	}

	if want := []string{"sora", "umi"}; !slices.Equal(state.Cast, want) {
		t.Errorf("Cast = %v, want %v (trimmed, no duplicates, in the order given)", state.Cast, want)
	}
	if state.Lang != "en" || state.AspectRatio != "9:16" {
		t.Errorf("Lang = %q, AspectRatio = %q, want en and 9:16 recorded on the state", state.Lang, state.AspectRatio)
	}
	if p.outlineData.Lang != "en" {
		t.Errorf("prompt Lang = %q, want en", p.outlineData.Lang)
	}
	roster := p.outlineData.CharacterRoster
	if !strings.Contains(roster, "id: sora") || !strings.Contains(roster, "id: umi") {
		t.Errorf("roster misses the cast:\n%s", roster)
	}
	if strings.Contains(roster, "zundamon") {
		t.Errorf("roster lists a character outside the cast:\n%s", roster)
	}
}

func TestGenerateOutlineWithoutACastListsEveryone(t *testing.T) {
	t.Parallel()
	ai := &fakeContentGenerator{text: outlineJSON}
	p := &fakeScriptPrompt{}
	r := NewOutlineRunner(p, ai, nil, workCast(t), 0)

	state, err := r.GenerateOutline(context.Background(), ports.OutlineRequest{SourceText: "x", Model: "m"})
	if err != nil {
		t.Fatalf("GenerateOutline failed: %v", err)
	}
	if state.Cast != nil || state.Lang != "" || state.AspectRatio != "" {
		t.Errorf("state records choices nobody made: cast=%v lang=%q ratio=%q", state.Cast, state.Lang, state.AspectRatio)
	}
	for _, id := range []string{"zundamon", "sora", "umi"} {
		if !strings.Contains(p.outlineData.CharacterRoster, "id: "+id) {
			t.Errorf("roster misses %s:\n%s", id, p.outlineData.CharacterRoster)
		}
	}
}

func TestGenerateOutlineRejectsBadWorkChoicesBeforeCallingTheModel(t *testing.T) {
	t.Parallel()
	tests := map[string]ports.OutlineRequest{
		"undefined cast member": {SourceText: "x", Model: "m", CharacterIDs: []string{"sora", "nobody"}},
		"unsupported ratio":     {SourceText: "x", Model: "m", AspectRatio: "5:4"},
	}
	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ai := &fakeContentGenerator{text: outlineJSON}
			r := NewOutlineRunner(&fakeScriptPrompt{}, ai, nil, workCast(t), 0)

			_, err := r.GenerateOutline(context.Background(), req)
			if !errors.Is(err, ports.ErrInvalidRequest) {
				t.Fatalf("err = %v, want ErrInvalidRequest", err)
			}
			if ai.lastPrompt != "" {
				t.Error("the model was called for a request that should have been refused first")
			}
		})
	}
}

const castChapterJSON = `{
  "panels": [
    {
      "shot": "wide",
      "visual_anchor": "empty street before dawn",
      "characters": [
        {"character_id": "sora", "prominence": "primary"},
        {"character_id": "zundamon", "prominence": "secondary"}
      ],
      "dialogues": [
        {"speaker_id": "sora", "text": "まだ、歌える", "kind": "speech"},
        {"speaker_id": "zundamon", "text": "なのだ", "kind": "speech"},
        {"speaker_id": "zundamon", "text": "夜明けまで歌うよ", "kind": "lyric"},
        {"text": "声が届くまで", "kind": "lyric"}
      ]
    },
    {"visual_anchor": "two", "characters": [], "dialogues": []},
    {"visual_anchor": "three", "characters": [], "dialogues": []}
  ]
}`

func TestChapterScriptKeepsTheWorkToItsCast(t *testing.T) {
	t.Parallel()
	ai := &fakeContentGenerator{text: castChapterJSON}
	p := &fakeScriptPrompt{}
	r := NewChapterScriptRunner(p, ai, workCast(t), 0, 0)
	state := outlineState()
	state.Cast = []string{"sora", "umi"}
	state.Lang = "en"

	state, err := r.GenerateChapterScript(context.Background(), state, "ch01", ports.ChapterScriptOptions{Model: "m"})
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	if strings.Contains(p.chapterData.CharacterRoster, "zundamon") {
		t.Errorf("roster lists a character outside the cast:\n%s", p.chapterData.CharacterRoster)
	}
	if p.chapterData.Lang != "en" {
		t.Errorf("prompt Lang = %q, want the state's en", p.chapterData.Lang)
	}

	panel := state.Panels[0]
	if got := panel.ReferencedCharacterIDs(); !slices.Equal(got, []string{"sora"}) {
		t.Errorf("referenced = %v, want only sora (a defined character outside the cast is a mob)", got)
	}
	want := []comic.DialogueLine{
		{SpeakerID: "sora", Text: "まだ、歌える", Kind: comic.DialogueKindSpeech},
		{Text: "なのだ", Kind: comic.DialogueKindNarration},
		{Text: "夜明けまで歌うよ", Kind: comic.DialogueKindLyric},
		{Text: "声が届くまで", Kind: comic.DialogueKindLyric},
	}
	if !slices.Equal(panel.Dialogues, want) {
		t.Errorf("dialogues = %+v\nwant %+v (a sung line keeps its kind when its speaker is dropped)", panel.Dialogues, want)
	}
}

func TestChapterScriptPanelLimitRidesOnTheCall(t *testing.T) {
	t.Parallel()
	ai := &fakeContentGenerator{text: castChapterJSON}
	p := &fakeScriptPrompt{}
	r := NewChapterScriptRunner(p, ai, workCast(t), 0, 0)

	state, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01",
		ports.ChapterScriptOptions{Model: "m", MaxPanels: 2})
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}
	if p.chapterData.MaxPanels != 2 {
		t.Errorf("prompt MaxPanels = %d, want 2", p.chapterData.MaxPanels)
	}
	if len(state.Panels) != 2 {
		t.Errorf("panels = %d, want truncated to 2", len(state.Panels))
	}

	if _, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01",
		ports.ChapterScriptOptions{Model: "m"}); err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}
	if p.chapterData.MaxPanels != ports.DefaultMaxPanelsPerChapter {
		t.Errorf("prompt MaxPanels = %d, want the configured default when the call names none", p.chapterData.MaxPanels)
	}
}

func TestChapterScriptSchemaAllowsSungLines(t *testing.T) {
	t.Parallel()
	panels := chapterScriptSchema()["properties"].(map[string]any)["panels"].(map[string]any)
	item := panels["items"].(map[string]any)["properties"].(map[string]any)
	dialogue := item["dialogues"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	kinds := dialogue["kind"].(map[string]any)["enum"].([]string)
	if !slices.Contains(kinds, comic.DialogueKindLyric) {
		t.Errorf("kind enum = %v, want it to include %q: constrained decoding cannot emit a value outside the enum", kinds, comic.DialogueKindLyric)
	}
}

func TestComposePagePassesTheWorkLanguage(t *testing.T) {
	t.Parallel()
	prompt := &fakePagePrompt{}
	r, _, _ := newPageRunner(t, prompt)
	state := pageTestState()
	state.Lang = "en"

	if _, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{OutputDir: "gs://bucket/out", Model: "m"}); err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}
	if prompt.data.Lang != "en" {
		t.Errorf("prompt Lang = %q, want the state's en", prompt.data.Lang)
	}
	if prompt.data.AspectRatio != "3:4" {
		t.Errorf("prompt AspectRatio = %q, want the resolved default 3:4", prompt.data.AspectRatio)
	}
}

func sheetPanelRunner(t *testing.T, prompt ports.PanelPrompt) (*PanelImageRunner, *mockImageGenerator) {
	t.Helper()
	cm, err := characterkit.NewCharacters([]comic.Character{
		{
			ID: "sora", Name: "Sora", ReferenceURL: "gs://b/a.png", VisualCues: []string{"short silver hair"}, IsDefault: true,
			Sheets: map[string]string{
				characterkit.SheetExpressions: "gs://b/a-face.png",
				characterkit.SheetOutfit:      "gs://b/a-outfit.png",
				characterkit.SheetParts:       "gs://b/a-parts.png",
			},
		},
		{
			ID: "umi", Name: "Umi", ReferenceURL: "gs://b/h.png", VisualCues: []string{"long navy hair"},
			Sheets: map[string]string{characterkit.SheetOutfit: "gs://b/h-outfit.png"},
		},
		{
			ID: "mika", Name: "Mika", ReferenceURL: "gs://b/m.png", VisualCues: []string{"short red hair"},
			Sheets: map[string]string{characterkit.SheetOutfit: "gs://b/m-outfit.png"},
		},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	gen := &mockImageGenerator{}
	return NewPanelImageRunner(PanelImageRunnerArgs{
		Characters: cm, Prompt: prompt, Generator: gen, Writer: &mockWriter{},
	}), gen
}

func TestGeneratePanelAttachesSupplementarySheetsAfterTheSubjects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		shot       string
		characters []comic.PanelCharacter
		wantImages []string
		wantSheets []ports.PanelSheetRef
	}{
		{
			name: "a close-up takes the expression sheet",
			shot: "close-up",
			characters: []comic.PanelCharacter{
				{CharacterID: "sora", Prominence: comic.ProminencePrimary},
			},
			wantImages: []string{"gs://b/a.png", "gs://b/a-face.png"},
			wantSheets: []ports.PanelSheetRef{{CharacterID: "sora", Kind: characterkit.SheetExpressions, File: 2}},
		},
		{
			name: "any other shot takes the outfit sheet, for primary characters only",
			shot: "wide",
			characters: []comic.PanelCharacter{
				{CharacterID: "umi", Prominence: comic.ProminenceSecondary},
				{CharacterID: "sora", Prominence: comic.ProminencePrimary},
			},
			wantImages: []string{"gs://b/h.png", "gs://b/a.png", "gs://b/a-outfit.png"},
			wantSheets: []ports.PanelSheetRef{{CharacterID: "sora", Kind: characterkit.SheetOutfit, File: 3}},
		},
		{
			name: "a character without that kind gets none",
			shot: "close-up",
			characters: []comic.PanelCharacter{
				{CharacterID: "umi", Prominence: comic.ProminencePrimary},
			},
			wantImages: []string{"gs://b/h.png"},
		},
		{
			name: "the number of sheets is capped",
			shot: "medium",
			characters: []comic.PanelCharacter{
				{CharacterID: "sora", Prominence: comic.ProminencePrimary},
				{CharacterID: "umi", Prominence: comic.ProminencePrimary},
				{CharacterID: "mika", Prominence: comic.ProminencePrimary},
			},
			wantImages: []string{"gs://b/a.png", "gs://b/h.png", "gs://b/m.png", "gs://b/a-outfit.png", "gs://b/h-outfit.png"},
			wantSheets: []ports.PanelSheetRef{
				{CharacterID: "sora", Kind: characterkit.SheetOutfit, File: 4},
				{CharacterID: "umi", Kind: characterkit.SheetOutfit, File: 5},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prompt := &fakePanelPrompt{}
			r, gen := sheetPanelRunner(t, prompt)
			state := &comic.MangaState{Panels: []comic.Panel{{ID: "ch01-p01", Shot: tt.shot, Characters: tt.characters}}}

			if _, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{OutputDir: "gs://bucket/out", Model: "m"}); err != nil {
				t.Fatalf("GeneratePanel failed: %v", err)
			}
			if !slices.Equal(gen.lastReq.Images, tt.wantImages) {
				t.Errorf("images = %v\nwant     %v", gen.lastReq.Images, tt.wantImages)
			}
			if !slices.Equal(prompt.data.Sheets, tt.wantSheets) {
				t.Errorf("sheets = %+v\nwant     %+v", prompt.data.Sheets, tt.wantSheets)
			}
			if len(tt.wantSheets) > comic.MaxSupplementarySheetsPerPanel {
				t.Fatalf("the test expects more sheets than the cap allows")
			}
		})
	}
}
