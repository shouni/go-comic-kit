package operations

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type mockDesignGenerator struct {
	lastReq imagePorts.ImageRequest
}

func (m *mockDesignGenerator) Generate(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	m.lastReq = req
	return &imagePorts.ImageResponse{Data: []byte("fake-png"), MimeType: "image/png", UsedSeed: 123}, nil
}

type mockWriter struct {
	lastPath string
	// lastSettings は適用後の書き込みオプションです（Content-Type / Cache-Control の確認用）。
	lastSettings remoteio.WriteSettings
	err          error // 非 nil なら保存を失敗させる
}

func (m *mockWriter) Write(_ context.Context, path string, _ io.Reader, opts ...remoteio.WriteOption) error {
	m.lastPath = path
	m.lastSettings = remoteio.WriteSettings{}
	for _, opt := range opts {
		opt(&m.lastSettings)
	}
	return m.err
}

// --- Helpers ---

func ptr[T any](v T) *T { return &v }

func newTestRunner(t *testing.T) (*DesignSheetRunner, *mockDesignGenerator, *mockWriter, *fakeDesignPrompt) {
	t.Helper()
	cm, err := characterkit.NewCharacters([]comic.Character{
		{
			ID:           "tsumugi",
			Name:         "Tsumugi",
			ReferenceURL: "gs://bucket/tsumugi.png",
			VisualCues:   []string{"orange hair", "yellow cardigan"},
			IsDefault:    true,
		},
		{
			ID:           "metan",
			Name:         "Metan",
			ReferenceURL: "gs://bucket/metan.png",
			VisualCues:   []string{"purple hair"},
		},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	genMock := &mockDesignGenerator{}
	writer := &mockWriter{}
	designPrompt := &fakeDesignPrompt{}
	dr := NewDesignSheetRunner(DesignSheetRunnerArgs{
		Prompt:      designPrompt,
		Characters:  cm,
		Generator:   genMock,
		Writer:      writer,
		Model:       "test-image-model",
		StyleSuffix: "test design style",
	})
	return dr, genMock, writer, designPrompt
}

// --- Tests ---

func TestGenerateDesignSheetCreatesStateAndRecordsRef(t *testing.T) {
	t.Parallel()
	dr, genMock, writer, designPrompt := newTestRunner(t)

	state, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-1",
		Seed:         ptr(int64(42)),
		OutputDir:    "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if state == nil {
		t.Fatal("state = nil, want a newly created state")
	}
	if state.Version != comic.StateSchemaVersion {
		t.Errorf("Version = %d, want %d", state.Version, comic.StateSchemaVersion)
	}
	if len(state.DesignSheets) != 1 {
		t.Fatalf("DesignSheets = %+v, want 1 entry", state.DesignSheets)
	}
	ref := state.DesignSheets[0]
	if ref.CharacterID != "tsumugi" || ref.UsedSeed != 123 || ref.ImageURL != writer.lastPath {
		t.Errorf("DesignSheetRef = %+v, want tsumugi / seed 123 / path %q", ref, writer.lastPath)
	}
	if !strings.Contains(writer.lastPath, "character/tsumugi/job-1.png") {
		t.Errorf("saved path = %q, want it under character/tsumugi/job-1.png", writer.lastPath)
	}
	if state.CreatedAt.IsZero() || state.UpdatedAt.IsZero() {
		t.Error("CreatedAt/UpdatedAt must be set")
	}

	// プロンプト本文はアプリ側の実装が持つので、ここで確かめるのは
	// 「実装へ何を渡したか」と「返ってきた3本をそのまま載せたか」だけ。
	if designPrompt.data == nil || len(designPrompt.data.Descriptions) != 1 ||
		!strings.Contains(designPrompt.data.Descriptions[0], "orange hair") {
		t.Errorf("Descriptions = %+v, want the character's visual cues", designPrompt.data)
	}
	if designPrompt.data.StyleSuffix != "test design style" {
		t.Errorf("StyleSuffix = %q, want the configured design style", designPrompt.data.StyleSuffix)
	}
	if genMock.lastReq.SystemPrompt != fakeSystemPrompt || genMock.lastReq.NegativePrompt != fakeNegativePrompt {
		t.Errorf("system/negative prompt = %q / %q, want them passed through unchanged",
			genMock.lastReq.SystemPrompt, genMock.lastReq.NegativePrompt)
	}
	if genMock.lastReq.AspectRatio != "16:9" {
		t.Errorf("AspectRatio = %q, want default 16:9", genMock.lastReq.AspectRatio)
	}
	if genMock.lastReq.Seed == nil || *genMock.lastReq.Seed != 42 {
		t.Errorf("Seed = %v, want 42", genMock.lastReq.Seed)
	}
	// 参照の解決は gemini-image-kit が担うため、ここでは参照元 URL を渡すだけ。
	if len(genMock.lastReq.Images) != 1 || genMock.lastReq.Images[0].ReferenceURL != "gs://bucket/tsumugi.png" {
		t.Errorf("Images = %+v, want the character reference URL", genMock.lastReq.Images)
	}
}

func TestGenerateDesignSheetUpsertsExistingRef(t *testing.T) {
	t.Parallel()
	dr, _, _, _ := newTestRunner(t)

	state := &comic.MangaState{
		Version:      comic.StateSchemaVersion,
		DesignSheets: []comic.DesignSheetRef{{CharacterID: "tsumugi", ImageURL: "gs://old.png", UsedSeed: 1}},
	}

	state, err := dr.GenerateDesignSheet(context.Background(), state, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-2",
		OutputDir:    "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if len(state.DesignSheets) != 1 {
		t.Fatalf("DesignSheets = %+v, want upsert (still 1 entry)", state.DesignSheets)
	}
	if state.DesignSheets[0].ImageURL == "gs://old.png" {
		t.Error("DesignSheetRef was not updated")
	}
}

func TestGenerateDesignSheetMultiCharacterFusion(t *testing.T) {
	t.Parallel()
	dr, genMock, _, designPrompt := newTestRunner(t)

	state, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi", "metan"},
		JobID:        "job-3",
		OutputDir:    "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if designPrompt.data == nil || len(designPrompt.data.Descriptions) != 2 {
		t.Errorf("Descriptions = %+v, want one per character", designPrompt.data)
	}
	if len(genMock.lastReq.Images) != 2 {
		t.Errorf("Images = %+v, want 2 reference images", genMock.lastReq.Images)
	}
	// 両キャラクターに同じシート画像が記録される
	if len(state.DesignSheets) != 2 || state.DesignSheets[0].ImageURL != state.DesignSheets[1].ImageURL {
		t.Errorf("DesignSheets = %+v, want both characters recorded with the same sheet", state.DesignSheets)
	}
}

func TestGenerateDesignSheetAppliesOverrideForSingleCharacter(t *testing.T) {
	t.Parallel()
	dr, genMock, _, designPrompt := newTestRunner(t)

	override := ports.DesignOverride{
		ReferenceURL: "gs://bucket/tsumugi-draft.png",
		VisualCues:   []string{"temporary test outfit"},
	}
	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-4",
		OutputDir:    "gs://bucket/out",
		Override:     override,
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if genMock.lastReq.Images[0].ReferenceURL != override.ReferenceURL {
		t.Errorf("ReferenceURL = %q, want override", genMock.lastReq.Images[0].ReferenceURL)
	}
	if genMock.lastReq.Images[0].FileAPIURI != "" {
		t.Errorf("FileAPIURI = %q, want empty (override URLs bypass pre-upload)", genMock.lastReq.Images[0].FileAPIURI)
	}
	desc := strings.Join(designPrompt.data.Descriptions, " ")
	if !strings.Contains(desc, "temporary test outfit") {
		t.Errorf("Descriptions = %q, want overridden visual cues", desc)
	}
	if strings.Contains(desc, "orange hair") {
		t.Errorf("Descriptions = %q, must not contain original cues once overridden", desc)
	}
}

func TestGenerateDesignSheetIgnoresOverrideForMultipleCharacters(t *testing.T) {
	t.Parallel()
	dr, genMock, _, _ := newTestRunner(t)

	override := ports.DesignOverride{ReferenceURL: "gs://bucket/should-be-ignored.png"}
	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi", "metan"},
		JobID:        "job-5",
		OutputDir:    "gs://bucket/out",
		Override:     override,
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	for _, img := range genMock.lastReq.Images {
		if img.ReferenceURL == override.ReferenceURL {
			t.Errorf("override leaked into multi-character request: %+v", genMock.lastReq.Images)
		}
	}
}

func TestGenerateDesignSheetSingleViewLayout(t *testing.T) {
	t.Parallel()
	dr, genMock, _, designPrompt := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-6",
		OutputDir:    "gs://bucket/out",
		Layout:       ports.DesignLayoutSingleView,
		AspectRatio:  "9:16",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if designPrompt.data.Layout != ports.DesignLayoutSingleView {
		t.Errorf("Layout = %q, want %q", designPrompt.data.Layout, ports.DesignLayoutSingleView)
	}
	if genMock.lastReq.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q, want 9:16", genMock.lastReq.AspectRatio)
	}
}

func TestGenerateDesignSheetAppliesModelOverride(t *testing.T) {
	t.Parallel()
	dr, genMock, _, _ := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs:  []string{"tsumugi"},
		JobID:         "job-model",
		OutputDir:     "gs://bucket/out",
		ModelOverride: "gemini-override-model",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if genMock.lastReq.Model != "gemini-override-model" {
		t.Errorf("Model = %q, want overridden model", genMock.lastReq.Model)
	}
}

func TestGenerateDesignSheetUsesDefaultModelWithoutOverride(t *testing.T) {
	t.Parallel()
	dr, genMock, _, _ := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-model-default",
		OutputDir:    "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if genMock.lastReq.Model != "test-image-model" {
		t.Errorf("Model = %q, want default model from runner construction", genMock.lastReq.Model)
	}
}

func TestGenerateDesignSheetUnknownCharacterFails(t *testing.T) {
	t.Parallel()
	dr, _, _, _ := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"unknown"},
		JobID:        "job-7",
		OutputDir:    "gs://bucket/out",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("err = %v, want unknown-character error", err)
	}
}

func TestGenerateDesignSheetRequiresJobID(t *testing.T) {
	t.Parallel()
	dr, _, _, _ := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		OutputDir:    "gs://bucket/out",
	})
	if err == nil || !strings.Contains(err.Error(), "job_id") {
		t.Errorf("err = %v, want job_id-required error", err)
	}
}
