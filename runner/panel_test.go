package runner

import (
	"context"
	"strings"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type mockFusionGenerator struct {
	lastReq imagePorts.ImageFusionRequest
}

func (m *mockFusionGenerator) GenerateFusedImage(_ context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error) {
	m.lastReq = req
	return &imagePorts.ImageResponse{Data: []byte("fake-png"), MimeType: "image/png", UsedSeed: 555}, nil
}

type mockPanelResources struct {
	prepared bool
	uris     map[string]string
}

func (m *mockPanelResources) PrepareCharacterResources(_ context.Context, _ *ports.MangaState) error {
	m.prepared = true
	return nil
}

func (m *mockPanelResources) GetCharacterResourceURIFor(charID, _ string) string {
	return m.uris[charID]
}

// --- Helpers ---

func panelTestState() *ports.MangaState {
	return &ports.MangaState{
		Version: ports.StateSchemaVersion,
		Chapters: []ports.Chapter{
			{ID: "ch01", Title: "導入"},
		},
		Panels: []ports.Panel{
			{
				ID:           "ch01-p01",
				ChapterID:    "ch01",
				Page:         1,
				Shot:         "wide",
				Setting:      "放課後の音楽室",
				VisualAnchor: "sunset light through windows, dynamic angle",
				Characters: []ports.PanelCharacter{
					{CharacterID: "zundamon", Prominence: ports.ProminencePrimary, Emotion: "驚き", Action: "めたんを指差す", Position: "left foreground"},
					{CharacterID: "metan", Prominence: ports.ProminenceSecondary, Emotion: "冷静", Position: "right"},
					{CharacterID: "students", Prominence: ports.ProminenceBackground, Action: "ざわめく"},
				},
			},
		},
	}
}

func newPanelRunner(t *testing.T) (*PanelImageRunner, *mockFusionGenerator, *mockWriter, *mockPanelResources) {
	t.Helper()
	zundaSeed := int64(10001)
	cm, err := characterkit.NewCharacters([]ports.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			ReferenceURL: "gs://b/zunda.png",
			ReferenceURLs: map[string]string{
				"16:9": "gs://b/zunda-16x9.png",
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
	gen := &mockFusionGenerator{}
	writer := &mockWriter{}
	resources := &mockPanelResources{uris: map[string]string{
		"zundamon": "https://file-api.google.com/zunda",
		"metan":    "https://file-api.google.com/metan",
	}}
	r := NewPanelImageRunner(cm, resources, gen, writer, "panel-model", ports.DefaultStyleSuffix, "", "")
	return r, gen, writer, resources
}

// --- Tests ---

func TestGeneratePanelBuildsMultiSubjectRequest(t *testing.T) {
	t.Parallel()
	r, gen, writer, resources := newPanelRunner(t)
	state := panelTestState()

	state, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{OutputDir: "gs://bucket/out"})
	if err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}

	if !resources.prepared {
		t.Error("PrepareCharacterResources was not called")
	}

	// 参照画像: primary + secondary の2枚（background は除外）
	if len(gen.lastReq.Images) != 2 {
		t.Fatalf("Images = %+v, want 2 references", gen.lastReq.Images)
	}
	// アスペクト比一致の参照画像が優先される
	if gen.lastReq.Images[0].ReferenceURL != "gs://b/zunda-16x9.png" {
		t.Errorf("Images[0] = %q, want aspect-specific reference", gen.lastReq.Images[0].ReferenceURL)
	}
	if gen.lastReq.Images[0].FileAPIURI == "" {
		t.Error("Images[0].FileAPIURI not resolved")
	}

	// プロンプト: [Subject N] と演出、モブ、スタイル、文字禁止
	p := gen.lastReq.Prompt
	for _, want := range []string{
		"[Subject 1: ずんだもん (green hair)]",
		"[Subject 2: めたん (purple hair)]",
		"驚き", "めたんを指差す", "left foreground",
		"Scene direction: sunset light through windows",
		"Background extras", "students",
		"No speech bubbles, no text.",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt does not contain %q\nprompt: %s", want, p)
		}
	}

	if gen.lastReq.SystemPrompt != panelSystemPrompt {
		t.Error("SystemPrompt not set")
	}
	if !strings.Contains(gen.lastReq.NegativePrompt, "speech bubble") || !strings.Contains(gen.lastReq.NegativePrompt, "extra fingers") {
		t.Errorf("NegativePrompt = %q, want balloon and finger negatives", gen.lastReq.NegativePrompt)
	}
	if gen.lastReq.AspectRatio != "16:9" || gen.lastReq.ImageSize != "1K" {
		t.Errorf("AspectRatio/ImageSize = %q/%q, want defaults 16:9/1K", gen.lastReq.AspectRatio, gen.lastReq.ImageSize)
	}

	// 主役キャラクターの Seed が既定として使われる
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 10001 {
		t.Errorf("Seed = %v, want primary character seed 10001", gen.lastReq.Seed)
	}

	// GenerationRecord の記録
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
	r, gen, _, _ := newPanelRunner(t)
	state := panelTestState()
	state.Panels[0].Generation = &ports.GenerationRecord{ImageURL: "gs://old.png", UsedSeed: 777}

	if _, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 777 {
		t.Errorf("Seed = %v, want previous UsedSeed 777", gen.lastReq.Seed)
	}
}

func TestGeneratePanelExplicitSeedWins(t *testing.T) {
	t.Parallel()
	r, gen, _, _ := newPanelRunner(t)
	state := panelTestState()
	state.Panels[0].Generation = &ports.GenerationRecord{UsedSeed: 777}

	newSeed := int64(42)
	if _, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{Seed: &newSeed}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 42 {
		t.Errorf("Seed = %v, want explicit 42", gen.lastReq.Seed)
	}
}

func TestGeneratePanelEditMode(t *testing.T) {
	t.Parallel()
	r, gen, _, resources := newPanelRunner(t)
	state := panelTestState()
	state.Panels[0].Generation = &ports.GenerationRecord{ImageURL: "gs://bucket/out/images/panel_ch01-p01.png", UsedSeed: 777}

	_, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{
		EditPrompt: "ずんだもんの表情を笑顔に変える",
		OutputDir:  "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GeneratePanel(edit) failed: %v", err)
	}

	// 編集モードは既存画像1枚だけを入力にする
	if len(gen.lastReq.Images) != 1 || gen.lastReq.Images[0].ReferenceURL != "gs://bucket/out/images/panel_ch01-p01.png" {
		t.Errorf("Images = %+v, want the existing panel image only", gen.lastReq.Images)
	}
	if !strings.Contains(gen.lastReq.Prompt, panelEditInstruction) || !strings.Contains(gen.lastReq.Prompt, "笑顔") {
		t.Errorf("Prompt = %q, want edit instruction", gen.lastReq.Prompt)
	}
	// 編集モードではキャラ参照の事前アップロードは不要
	if resources.prepared {
		t.Error("PrepareCharacterResources should not be called in edit mode")
	}
}

func TestGeneratePanelEditModeRequiresExistingImage(t *testing.T) {
	t.Parallel()
	r, _, _, _ := newPanelRunner(t)

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{
		EditPrompt: "表情を変える",
	})
	if err == nil || !strings.Contains(err.Error(), "編集対象") {
		t.Errorf("err = %v, want missing-image error", err)
	}
}

func TestGeneratePanelPromptOverride(t *testing.T) {
	t.Parallel()
	r, gen, _, _ := newPanelRunner(t)

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{
		PromptOverride: "custom prompt",
	})
	if err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Prompt != "custom prompt" {
		t.Errorf("Prompt = %q, want override", gen.lastReq.Prompt)
	}
	// 参照画像は override でも維持される
	if len(gen.lastReq.Images) != 2 {
		t.Errorf("Images = %+v, want references kept with prompt override", gen.lastReq.Images)
	}
}

func TestGeneratePanelUnknownPanelFails(t *testing.T) {
	t.Parallel()
	r, _, _, _ := newPanelRunner(t)

	if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch99-p01", ports.GenerateOptions{}); err == nil {
		t.Error("GeneratePanel(unknown) succeeded, want error")
	}
	if _, err := r.GeneratePanel(context.Background(), nil, "ch01-p01", ports.GenerateOptions{}); err == nil {
		t.Error("GeneratePanel(nil state) succeeded, want error")
	}
}

func TestGeneratePanelSceneryPanelWithoutCharacters(t *testing.T) {
	t.Parallel()
	r, gen, _, _ := newPanelRunner(t)
	state := panelTestState()
	state.Panels[0].Characters = nil // 風景のみのコマ

	_, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{})
	if err != nil {
		t.Fatalf("GeneratePanel(scenery) failed: %v", err)
	}
	if len(gen.lastReq.Images) != 0 {
		t.Errorf("Images = %+v, want no references for scenery panel", gen.lastReq.Images)
	}
	if gen.lastReq.Seed != nil {
		t.Errorf("Seed = %v, want nil for scenery panel", gen.lastReq.Seed)
	}
}
