package operations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type mockImageGenerator struct {
	lastReq ImageRequest
}

func (m *mockImageGenerator) Generate(_ context.Context, req ImageRequest) (*ImageResponse, error) {
	m.lastReq = req
	return &ImageResponse{Data: []byte("fake-png"), MIMEType: "image/png", UsedSeed: 555}, nil
}

// --- Helpers ---

func panelTestState() *comic.MangaState {
	return &comic.MangaState{
		Version: comic.StateSchemaVersion,
		Chapters: []comic.Chapter{
			{ID: "ch01", Title: "導入"},
		},
		Panels: []comic.Panel{
			{
				ID:           "ch01-p01",
				ChapterID:    "ch01",
				Page:         1,
				Shot:         "wide",
				Setting:      "放課後の音楽室",
				VisualAnchor: "sunset light through windows, dynamic angle",
				Characters: []comic.PanelCharacter{
					{CharacterID: "zundamon", Prominence: comic.ProminencePrimary, Emotion: "驚き", Action: "めたんを指差す", Position: "left foreground"},
					{CharacterID: "metan", Prominence: comic.ProminenceSecondary, Emotion: "冷静", Position: "right"},
					{CharacterID: "students", Prominence: comic.ProminenceBackground, Action: "ざわめく"},
				},
			},
		},
	}
}

func newPanelRunner(t *testing.T, prompt ports.PanelPrompt) (*PanelImageRunner, *mockImageGenerator, *mockWriter) {
	t.Helper()
	zundaSeed := int64(10001)
	cm, err := characterkit.NewCharacters([]comic.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			ReferenceURL: "gs://b/zunda.png",
			ReferenceURLs: map[string]string{
				"3:4": "gs://b/zunda-3x4.png",
			},
			VisualCues: []string{"green hair"},
			Seed:       &zundaSeed,
			IsDefault:  true,
		},
		{ID: "metan", Name: "めたん", ReferenceURL: "gs://b/metan.png", VisualCues: []string{"purple hair"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	gen := &mockImageGenerator{}
	writer := &mockWriter{}
	r := NewPanelImageRunner(PanelImageRunnerArgs{
		Characters: cm,
		Prompt:     prompt,
		Generator:  gen,
		Writer:     writer,
	})
	return r, gen, writer
}

// --- Tests ---

func TestGeneratePanelBuildsMultiSubjectRequest(t *testing.T) {
	t.Parallel()
	prompt := &fakePanelPrompt{}
	r, gen, writer := newPanelRunner(t, prompt)
	state := panelTestState()

	state, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{OutputDir: "gs://bucket/out", Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}

	// Reference images: 2 for primary + secondary (background excluded)
	if len(gen.lastReq.Images) != 2 {
		t.Fatalf("Images = %+v, want 2 references", gen.lastReq.Images)
	}
	// The aspect-matched reference image is preferred
	if gen.lastReq.Images[0] != "gs://b/zunda-3x4.png" {
		t.Errorf("Images[0] = %q, want aspect-specific reference", gen.lastReq.Images[0])
	}
	// Resolving references (gs:// as is, http(s) fetched) is the workflow layer's job, so
	// this layer only goes as far as listing the source URLs.

	// The prompt text belongs to the app's implementation, so this checks only "the
	// structured data passed to the implementation" and "whether the three returned prompts were used as is".
	d := prompt.data
	if d == nil {
		t.Fatal("PanelPrompt was not called")
	}
	// Unless the reference numbers match the attachment order, the model draws from someone else's reference image.
	if len(d.SubjectIDs) != 2 || d.SubjectIDs[0] != "zundamon" || d.SubjectIDs[1] != "metan" {
		t.Errorf("SubjectIDs = %v, want [zundamon metan] (attachment order)", d.SubjectIDs)
	}
	if d.Panel.ID != "ch01-p01" || d.Panel.Setting != "放課後の音楽室" ||
		!strings.Contains(d.Panel.VisualAnchor, "sunset light through windows") {
		t.Errorf("Panel = %+v, want the target panel with its setting and anchor", d.Panel)
	}
	if gen.lastReq.SystemPrompt != fakeSystemPrompt || gen.lastReq.NegativePrompt != fakeNegativePrompt {
		t.Errorf("system/negative prompt = %q / %q, want them passed through unchanged",
			gen.lastReq.SystemPrompt, gen.lastReq.NegativePrompt)
	}

	if gen.lastReq.AspectRatio != "3:4" || gen.lastReq.ImageSize != "1K" {
		t.Errorf("AspectRatio/ImageSize = %q/%q, want defaults 3:4/1K", gen.lastReq.AspectRatio, gen.lastReq.ImageSize)
	}

	// The lead character's Seed is used as the default
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 10001 {
		t.Errorf("Seed = %v, want primary character seed 10001", gen.lastReq.Seed)
	}

	// The GenerationRecord record
	rec := state.PanelByID("ch01-p01").Generation
	if rec == nil {
		t.Fatal("GenerationRecord not recorded")
	}
	if rec.ImageURL != writer.lastPath || !strings.Contains(rec.ImageURL, "images/panel_ch01-p01.png") {
		t.Errorf("ImageURL = %q, want stable panel path (saved: %q)", rec.ImageURL, writer.lastPath)
	}
	if rec.UsedSeed != 555 || rec.Model != "panel-model" || rec.Prompt == "" {
		t.Errorf("GenerationRecord = %+v, want full generation conditions", rec)
	}
}

func TestGeneratePanelReusesPreviousSeed(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Generation = &comic.GenerationRecord{ImageURL: "gs://old.png", UsedSeed: 777}

	if _, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{Model: "panel-model"}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 777 {
		t.Errorf("Seed = %v, want previous UsedSeed 777", gen.lastReq.Seed)
	}
}

func TestGeneratePanelExplicitSeedWins(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Generation = &comic.GenerationRecord{UsedSeed: 777}

	newSeed := int64(42)
	if _, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{Seed: &newSeed, Model: "panel-model"}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 42 {
		t.Errorf("Seed = %v, want explicit 42", gen.lastReq.Seed)
	}
}

func TestGeneratePanelEditMode(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Generation = &comic.GenerationRecord{ImageURL: "gs://bucket/out/images/panel_ch01-p01.png", UsedSeed: 777}

	_, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{
		EditPrompt: "ずんだもんの表情を笑顔に変える",
		OutputDir:  "gs://bucket/out", Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel(edit) failed: %v", err)
	}

	// Edit mode takes only the one existing image as input
	if len(gen.lastReq.Images) != 1 || gen.lastReq.Images[0] != "gs://bucket/out/images/panel_ch01-p01.png" {
		t.Errorf("Images = %+v, want the existing panel image only", gen.lastReq.Images)
	}
	if !strings.Contains(gen.lastReq.Prompt, "FAKE-PANEL-EDIT") || !strings.Contains(gen.lastReq.Prompt, "笑顔") {
		t.Errorf("Prompt = %q, want edit instruction", gen.lastReq.Prompt)
	}
}

func TestGeneratePanelEditModeRequiresExistingImage(t *testing.T) {
	t.Parallel()
	r, _, _ := newPanelRunner(t, &fakePanelPrompt{})

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{
		EditPrompt: "表情を変える", Model: "panel-model"})
	if err == nil || !strings.Contains(err.Error(), "to edit") {
		t.Errorf("err = %v, want missing-image error", err)
	}
}

func TestGeneratePanelPromptOverride(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{
		PromptOverride: "custom prompt", Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Prompt != "custom prompt" {
		t.Errorf("Prompt = %q, want override", gen.lastReq.Prompt)
	}
	// The reference images are kept even with an override
	if len(gen.lastReq.Images) != 2 {
		t.Errorf("Images = %+v, want references kept with prompt override", gen.lastReq.Images)
	}
}

func TestGeneratePanelUnknownPanelFails(t *testing.T) {
	t.Parallel()
	r, _, _ := newPanelRunner(t, &fakePanelPrompt{})

	if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch99-p01", ports.GenerateOptions{Model: "panel-model"}); err == nil {
		t.Error("GeneratePanel(unknown) succeeded, want error")
	}
	if _, err := r.GeneratePanel(context.Background(), nil, "ch01-p01", ports.GenerateOptions{Model: "panel-model"}); err == nil {
		t.Error("GeneratePanel(nil state) succeeded, want error")
	}
}

func TestGeneratePanelSceneryPanelWithoutCharacters(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Characters = nil // a scenery-only panel

	_, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel(scenery) failed: %v", err)
	}
	if len(gen.lastReq.Images) != 0 {
		t.Errorf("Images = %+v, want no references for scenery panel", gen.lastReq.Images)
	}
	// There is no character-derived seed, but a seed is always sent so the record can
	// reproduce it (resolveSeedChain's final draw; a UsedSeed recorded as 0 makes
	// regeneration produce something else).
	if gen.lastReq.Seed == nil {
		t.Error("Seed = nil, want a freshly drawn seed so the panel can be reproduced")
	}
}

// The art style mode passes straight through to the prompt implementation (the kit holds no style direction itself).
func TestGeneratePanelPassesStyleMode(t *testing.T) {
	t.Parallel()

	prompt := &fakePanelPrompt{}
	r, _, _ := newPanelRunner(t, prompt)

	if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01",
		ports.GenerateOptions{StyleMode: "watercolor", Model: "panel-model"}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if got := prompt.data.StyleMode; got != "watercolor" {
		t.Errorf("StyleMode = %q, want watercolor", got)
	}
}

// Likewise for images: without a model name, it fails with ErrInvalidRequest without
// calling the generator. Panels, pages and design sheets share renderImage, so one is checked as representative.
func TestGeneratePanelWithoutAnyModelFails(t *testing.T) {
	t.Parallel()

	r, gen, writer := newPanelRunner(t, &fakePanelPrompt{})

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{})
	if !errors.Is(err, ports.ErrInvalidRequest) {
		t.Errorf("err = %v, want ErrInvalidRequest", err)
	}
	if gen.lastReq.Model != "" {
		t.Error("image generation was called without a model name")
	}
	if writer.lastPath != "" {
		t.Error("wrote without generating")
	}
}

// Ratio and resolution are specified per call. Empty falls back to the kit default
// (3:4 / 1K), and an unsupported value is ErrInvalidRequest rather than silently falling
// back; falling back would leave "not generated at the ratio I asked for" going unnoticed.
func TestGeneratePanelResolvesLayoutPerCall(t *testing.T) {
	t.Parallel()

	t.Run("unspecified uses the kit default", func(t *testing.T) {
		t.Parallel()
		r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

		if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01",
			ports.GenerateOptions{Model: "m"}); err != nil {
			t.Fatalf("GeneratePanel failed: %v", err)
		}
		if gen.lastReq.AspectRatio != layout.DefaultAspectRatio || gen.lastReq.ImageSize != layout.ImageSize1K {
			t.Errorf("layout = %q/%q, want %q/%q",
				gen.lastReq.AspectRatio, gen.lastReq.ImageSize, layout.DefaultAspectRatio, layout.ImageSize1K)
		}
	})

	t.Run("specified values arrive", func(t *testing.T) {
		t.Parallel()
		r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

		if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01",
			ports.GenerateOptions{Model: "m", AspectRatio: "16:9", ImageSize: ports.ImageSize2K}); err != nil {
			t.Fatalf("GeneratePanel failed: %v", err)
		}
		if gen.lastReq.AspectRatio != "16:9" || gen.lastReq.ImageSize != ports.ImageSize2K {
			t.Errorf("layout = %q/%q, want 16:9/%s", gen.lastReq.AspectRatio, gen.lastReq.ImageSize, ports.ImageSize2K)
		}
	})

	for name, opts := range map[string]ports.GenerateOptions{
		"unsupported ratio":      {Model: "m", AspectRatio: "4:3"},
		"unsupported resolution": {Model: "m", ImageSize: "4K"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

			_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", opts)
			if !errors.Is(err, ports.ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest", err)
			}
			if gen.lastReq.Model != "" {
				t.Error("image generation was called despite an invalid value")
			}
		})
	}
}
