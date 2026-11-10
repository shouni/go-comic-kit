package operations

import (
	"context"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Helpers ---

func pageTestState() *comic.MangaState {
	return &comic.MangaState{
		Version: comic.StateSchemaVersion,
		Title:   "夜明けのデプロイ",
		Panels: []comic.Panel{
			{
				ID:           "ch01-p01",
				ChapterID:    "ch01",
				Page:         1,
				Shot:         "wide",
				Setting:      "放課後の音楽室",
				VisualAnchor: "sunset light, dynamic angle",
				Characters: []comic.PanelCharacter{
					{CharacterID: "zundamon", Prominence: comic.ProminencePrimary, Emotion: "驚き", Position: "left"},
				},
				Dialogues: []comic.DialogueLine{
					{SpeakerID: "zundamon", Text: "なんなのだ！？", Kind: comic.DialogueKindShout},
					{Text: "その日、すべてが変わった", Kind: comic.DialogueKindNarration},
				},
				Generation: &comic.GenerationRecord{ImageURL: "gs://b/panels/p01.png", UsedSeed: 11},
			},
			{
				ID:        "ch01-p02",
				ChapterID: "ch01",
				Page:      1,
				Characters: []comic.PanelCharacter{
					{CharacterID: "metan", Prominence: comic.ProminencePrimary, Emotion: "冷静"},
					{CharacterID: "zundamon", Prominence: comic.ProminenceSecondary},
				},
				Dialogues: []comic.DialogueLine{
					{SpeakerID: "metan", Text: "落ち着きなさい。", Kind: comic.DialogueKindSpeech},
				},
			},
			{
				ID:        "ch02-p01",
				ChapterID: "ch02",
				Page:      2, // a different page (not a target)
				Characters: []comic.PanelCharacter{
					{CharacterID: "zundamon", Prominence: comic.ProminencePrimary},
				},
			},
		},
	}
}

func newPageRunner(t *testing.T, prompt ports.PagePrompt) (*PageImageRunner, *mockImageGenerator, *mockWriter) {
	t.Helper()
	zundaSeed := int64(10001)
	cm, err := characterkit.NewCharacters([]comic.Character{
		{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://b/zunda.png", VisualCues: []string{"green hair"}, Seed: &zundaSeed, IsDefault: true},
		{ID: "metan", Name: "めたん", ReferenceURL: "gs://b/metan.png", VisualCues: []string{"purple hair"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	gen := &mockImageGenerator{}
	writer := &mockWriter{}
	r := NewPageImageRunner(PageImageRunnerArgs{
		Characters: cm,
		Prompt:     prompt,
		Generator:  gen,
		Writer:     writer,
	})
	return r, gen, writer
}

// --- Tests ---

func TestComposePageBuildsLayoutAndReferences(t *testing.T) {
	t.Parallel()
	prompt := &fakePagePrompt{}
	r, gen, writer := newPageRunner(t, prompt)
	state := pageTestState()

	state, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{OutputDir: "gs://bucket/out", Model: "page-model"})
	if err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}

	// References: 2 characters (zundamon, metan) + 1 generated panel (p01 only)
	if len(gen.lastReq.Images) != 3 {
		t.Fatalf("Images = %+v, want 3 references", gen.lastReq.Images)
	}

	// The prompt text belongs to the app's implementation, so this checks only "the
	// structured data passed to the implementation" and "whether the three returned prompts were used as is".
	d := prompt.data
	if d == nil {
		t.Fatal("PagePrompt was not called")
	}
	// Unless the reference numbers match the attachment order, the model draws from someone else's reference image.
	if d.CharacterFile["zundamon"] != 1 || d.CharacterFile["metan"] != 2 {
		t.Errorf("CharacterFile = %v, want zundamon=1 / metan=2 (attachment order)", d.CharacterFile)
	}
	if d.PanelFile["ch01-p01"] != 3 {
		t.Errorf("PanelFile = %v, want the generated panel at 3", d.PanelFile)
	}
	// Only this page's panels are passed; another page's panels do not mix in.
	if got := panelIDs(d.Panels); len(got) != 2 || got[0] != "ch01-p01" || got[1] != "ch01-p02" {
		t.Errorf("Panels = %v, want only page 1's panels in order", got)
	}
	if gen.lastReq.SystemPrompt != fakeSystemPrompt || gen.lastReq.NegativePrompt != fakeNegativePrompt {
		t.Errorf("system/negative prompt = %q / %q, want them passed through unchanged",
			gen.lastReq.SystemPrompt, gen.lastReq.NegativePrompt)
	}

	if gen.lastReq.AspectRatio != "3:4" || gen.lastReq.ImageSize != "2K" {
		t.Errorf("AspectRatio/ImageSize = %q/%q, want 3:4/2K", gen.lastReq.AspectRatio, gen.lastReq.ImageSize)
	}

	// The lead character's Seed in the first panel is used as the default
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 10001 {
		t.Errorf("Seed = %v, want primary character seed 10001", gen.lastReq.Seed)
	}

	// The PageArtifact record
	artifact := state.PageArtifactByNumber(1)
	if artifact == nil || artifact.Generation == nil {
		t.Fatal("PageArtifact not recorded")
	}
	if artifact.Generation.ImageURL != writer.lastPath || !strings.Contains(artifact.Generation.ImageURL, "comic_page_1.png") {
		t.Errorf("ImageURL = %q, want indexed page path", artifact.Generation.ImageURL)
	}
	if len(artifact.PanelIDs) != 2 || artifact.PanelIDs[0] != "ch01-p01" {
		t.Errorf("PanelIDs = %v, want the page's panel IDs", artifact.PanelIDs)
	}
}

func TestComposePageUpsertsArtifactAndReusesSeed(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPageRunner(t, &fakePagePrompt{})
	state := pageTestState()
	state.Pages = []comic.PageArtifact{
		{PageNumber: 1, Generation: &comic.GenerationRecord{ImageURL: "gs://old.png", UsedSeed: 777}},
	}

	state, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{Model: "page-model"})
	if err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}

	// Reuse the previous UsedSeed
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 777 {
		t.Errorf("Seed = %v, want previous 777", gen.lastReq.Seed)
	}
	// upsert (no duplicate entries)
	if len(state.Pages) != 1 {
		t.Errorf("Pages = %+v, want 1 entry (upsert)", state.Pages)
	}
	if state.Pages[0].Generation.ImageURL == "gs://old.png" {
		t.Error("PageArtifact was not updated")
	}
}

func TestComposePageEditMode(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPageRunner(t, &fakePagePrompt{})
	state := pageTestState()
	state.Pages = []comic.PageArtifact{
		{PageNumber: 1, Generation: &comic.GenerationRecord{ImageURL: "gs://b/pages/page1.png"}},
	}

	_, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{
		EditPrompt: "1コマ目の空を夕焼けにする", Model: "page-model"})
	if err != nil {
		t.Fatalf("ComposePage(edit) failed: %v", err)
	}

	if len(gen.lastReq.Images) != 1 || gen.lastReq.Images[0] != "gs://b/pages/page1.png" {
		t.Errorf("Images = %+v, want existing page image only", gen.lastReq.Images)
	}
	if !strings.Contains(gen.lastReq.Prompt, "FAKE-PAGE-EDIT") || !strings.Contains(gen.lastReq.Prompt, "夕焼け") {
		t.Errorf("Prompt = %q, want edit instruction", gen.lastReq.Prompt)
	}
}

func TestComposePageEditModeRequiresExistingImage(t *testing.T) {
	t.Parallel()
	r, _, _ := newPageRunner(t, &fakePagePrompt{})

	_, err := r.ComposePage(context.Background(), pageTestState(), 1, ports.GenerateOptions{EditPrompt: "変更", Model: "page-model"})
	if err == nil || !strings.Contains(err.Error(), "to edit") {
		t.Errorf("err = %v, want missing-image error", err)
	}
}

func TestComposePageEmptyPageFails(t *testing.T) {
	t.Parallel()
	r, _, _ := newPageRunner(t, &fakePagePrompt{})

	if _, err := r.ComposePage(context.Background(), pageTestState(), 99, ports.GenerateOptions{Model: "page-model"}); err == nil {
		t.Error("ComposePage(empty page) succeeded, want error")
	}
	if _, err := r.ComposePage(context.Background(), nil, 1, ports.GenerateOptions{Model: "page-model"}); err == nil {
		t.Error("ComposePage(nil state) succeeded, want error")
	}
}

// TestComposePageCapsCharacterReferences checks that the character references attached to
// one page are capped at comic.MaxReferencedCharactersPerPage, keeping those appearing in
// the most panels. Without the cap, the excess references the panel cap held back return
// at composition time.
func TestComposePageCapsCharacterReferences(t *testing.T) {
	t.Parallel()

	ids := []string{"c1", "c2", "c3", "c4", "c5", "c6"}
	defs := make([]comic.Character, 0, len(ids))
	for _, id := range ids {
		defs = append(defs, comic.Character{
			ID: id, Name: id,
			ReferenceURL: "gs://b/" + id + ".png",
			VisualCues:   []string{id + " hair"},
		})
	}
	defs[0].IsDefault = true
	cm, err := characterkit.NewCharacters(defs)
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}

	prompt := &fakePagePrompt{}
	gen := &mockImageGenerator{}
	r := NewPageImageRunner(PageImageRunnerArgs{
		Characters: cm,
		Prompt:     prompt,
		Generator:  gen,
		Writer:     &mockWriter{},
	})

	// c1 appears first but in one panel only; c6 appears last but in all three panels.
	// A plain head-truncation drops c6; choosing by panel count keeps it.
	state := &comic.MangaState{
		Version: comic.StateSchemaVersion,
		Panels: []comic.Panel{
			{ID: "p01", Page: 1, Characters: panelChars("c1", "c2", "c6")},
			{ID: "p02", Page: 1, Characters: panelChars("c3", "c4", "c6")},
			{ID: "p03", Page: 1, Characters: panelChars("c5", "c6")},
		},
	}

	if _, err := r.ComposePage(t.Context(), state, 1, ports.GenerateOptions{
		OutputDir: "gs://bucket/out", Model: "page-model",
	}); err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}

	if got := len(gen.lastReq.Images); got != comic.MaxReferencedCharactersPerPage {
		t.Errorf("Images = %d, want %d", got, comic.MaxReferencedCharactersPerPage)
	}
	if _, ok := prompt.data.CharacterFile["c6"]; !ok {
		t.Errorf("CharacterFile = %v, want c6 kept (it appears in the most panels on this page)", prompt.data.CharacterFile)
	}
	if len(prompt.data.CharacterFile) != comic.MaxReferencedCharactersPerPage {
		t.Errorf("CharacterFile = %v, want %d entries", prompt.data.CharacterFile, comic.MaxReferencedCharactersPerPage)
	}
	// Unless the attachment order and reference numbers match, the model draws from someone else's reference image.
	for id, n := range prompt.data.CharacterFile {
		if n < 1 || n > len(gen.lastReq.Images) {
			t.Errorf("CharacterFile[%s] = %d, want a valid attachment index", id, n)
		}
	}
}

// panelChars converts the given IDs into primary panel characters.
func panelChars(ids ...string) []comic.PanelCharacter {
	chars := make([]comic.PanelCharacter, 0, len(ids))
	for _, id := range ids {
		chars = append(chars, comic.PanelCharacter{CharacterID: id, Prominence: comic.ProminencePrimary})
	}
	return chars
}

// jpegImageGenerator is an image generator that returns JPEG.
type jpegImageGenerator struct{}

func (jpegImageGenerator) Generate(_ context.Context, _ ImageRequest) (*ImageResponse, error) {
	return &ImageResponse{Data: []byte("fake-jpeg"), MIMEType: "image/jpeg", UsedSeed: 7}, nil
}

// TestComposePageSavesWithResponseExtension checks that the page image's file extension
// follows the generated MIME type (the same rule as panels and design sheets).
func TestComposePageSavesWithResponseExtension(t *testing.T) {
	t.Parallel()

	cm, err := characterkit.NewCharacters([]comic.Character{{
		ID: "zundamon", Name: "ずんだもん",
		ReferenceURL: "gs://b/zunda.png",
		VisualCues:   []string{"green hair"},
		IsDefault:    true,
	}})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}

	writer := &mockWriter{}
	r := NewPageImageRunner(PageImageRunnerArgs{
		Characters: cm,
		Prompt:     &fakePagePrompt{},
		Generator:  jpegImageGenerator{},
		Writer:     writer,
	})
	state := &comic.MangaState{
		Version: comic.StateSchemaVersion,
		Panels:  []comic.Panel{{ID: "p01", Page: 1, Characters: panelChars("zundamon")}},
	}

	if _, err := r.ComposePage(t.Context(), state, 1, ports.GenerateOptions{
		OutputDir: "gs://bucket/out", Model: "page-model",
	}); err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}
	if want := "gs://bucket/out/images/comic_page_1.jpg"; writer.lastPath != want {
		t.Errorf("saved path = %q, want %q", writer.lastPath, want)
	}
}
