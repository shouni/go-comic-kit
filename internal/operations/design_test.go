package operations

import (
	"context"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/internal/prompts"
	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type mockDesignGenerator struct {
	lastReq imagePorts.ImageFusionRequest
}

func (m *mockDesignGenerator) GenerateFusedImage(_ context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error) {
	m.lastReq = req
	return &imagePorts.ImageResponse{Data: []byte("fake-png"), MimeType: "image/png", UsedSeed: 123}, nil
}

type mockWriter struct {
	lastPath string
}

func (m *mockWriter) Write(_ context.Context, path string, _ io.Reader, _ ...remoteio.WriteOption) error {
	m.lastPath = path
	return nil
}

type mockResources struct {
	uris map[string]string
}

func (m *mockResources) GetCharacterResourceURI(charID string) string {
	return m.uris[charID]
}

// --- Helpers ---

func newTestRunner(t *testing.T) (*DesignSheetRunner, *mockDesignGenerator, *mockWriter) {
	t.Helper()
	cm, err := characterkit.NewCharacters([]ports.Character{
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
	resources := &mockResources{uris: map[string]string{"tsumugi": "https://file-api.google.com/tsumugi"}}
	dr := NewDesignSheetRunner(prompts.DefaultDesignPrompt{}, cm, resources, genMock, writer, "test-image-model", ports.DefaultDesignStyleSuffix)
	return dr, genMock, writer
}

// --- Tests ---

func TestGenerateDesignSheetCreatesStateAndRecordsRef(t *testing.T) {
	t.Parallel()
	dr, genMock, writer := newTestRunner(t)

	state, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-1",
		Seed:         42,
		OutputDir:    "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if state == nil {
		t.Fatal("state = nil, want a newly created state")
	}
	if state.Version != ports.StateSchemaVersion {
		t.Errorf("Version = %d, want %d", state.Version, ports.StateSchemaVersion)
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

	// 生成リクエストの検証（内蔵デフォルトプロンプトの内容）
	if !strings.Contains(genMock.lastReq.SystemPrompt, "canonical identity reference") {
		t.Errorf("SystemPrompt = %q, want identity-consistency instructions", genMock.lastReq.SystemPrompt)
	}
	if !strings.Contains(genMock.lastReq.NegativePrompt, "Do not include") {
		t.Errorf("NegativePrompt = %q, want instruction-style exclusion list", genMock.lastReq.NegativePrompt)
	}
	if !strings.Contains(genMock.lastReq.Prompt, "flat even neutral lighting") {
		t.Errorf("Prompt = %q, want flat lighting constraint", genMock.lastReq.Prompt)
	}
	if !strings.Contains(genMock.lastReq.Prompt, "orange hair") {
		t.Errorf("Prompt = %q, want visual cues", genMock.lastReq.Prompt)
	}
	if genMock.lastReq.AspectRatio != "16:9" {
		t.Errorf("AspectRatio = %q, want default 16:9", genMock.lastReq.AspectRatio)
	}
	if genMock.lastReq.Seed == nil || *genMock.lastReq.Seed != 42 {
		t.Errorf("Seed = %v, want 42", genMock.lastReq.Seed)
	}
	if len(genMock.lastReq.Images) != 1 || genMock.lastReq.Images[0].FileAPIURI != "https://file-api.google.com/tsumugi" {
		t.Errorf("Images = %+v, want pre-uploaded File API URI", genMock.lastReq.Images)
	}
}

func TestGenerateDesignSheetUpsertsExistingRef(t *testing.T) {
	t.Parallel()
	dr, _, _ := newTestRunner(t)

	state := &ports.MangaState{
		Version:      ports.StateSchemaVersion,
		DesignSheets: []ports.DesignSheetRef{{CharacterID: "tsumugi", ImageURL: "gs://old.png", UsedSeed: 1}},
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
	dr, genMock, _ := newTestRunner(t)

	state, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi", "metan"},
		JobID:        "job-3",
		OutputDir:    "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if !strings.Contains(genMock.lastReq.Prompt, "2 DIFFERENT characters") {
		t.Errorf("Prompt = %q, want multi-subject prompt", genMock.lastReq.Prompt)
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
	dr, genMock, _ := newTestRunner(t)

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
	if !strings.Contains(genMock.lastReq.Prompt, "temporary test outfit") {
		t.Errorf("Prompt = %q, want overridden visual cues", genMock.lastReq.Prompt)
	}
	if strings.Contains(genMock.lastReq.Prompt, "orange hair") {
		t.Errorf("Prompt = %q, must not contain original cues once overridden", genMock.lastReq.Prompt)
	}
}

func TestGenerateDesignSheetIgnoresOverrideForMultipleCharacters(t *testing.T) {
	t.Parallel()
	dr, genMock, _ := newTestRunner(t)

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
	dr, genMock, _ := newTestRunner(t)

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

	if !strings.Contains(genMock.lastReq.Prompt, "single view, front-facing") {
		t.Errorf("Prompt = %q, want single-view layout", genMock.lastReq.Prompt)
	}
	if strings.Contains(genMock.lastReq.Prompt, "multiple views") {
		t.Errorf("Prompt = %q, must not contain multi-view layout", genMock.lastReq.Prompt)
	}
	if genMock.lastReq.AspectRatio != "9:16" {
		t.Errorf("AspectRatio = %q, want 9:16", genMock.lastReq.AspectRatio)
	}
}

func TestGenerateDesignSheetAppliesModelOverride(t *testing.T) {
	t.Parallel()
	dr, genMock, _ := newTestRunner(t)

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
	dr, genMock, _ := newTestRunner(t)

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

func TestDesignFileTagTruncatesLongIDs(t *testing.T) {
	t.Parallel()

	// 短い場合はそのまま
	if got := designFileTag([]string{"tsumugi", "metan"}); got != "tsumugi_metan" {
		t.Errorf("designFileTag = %q, want tsumugi_metan", got)
	}

	// 上限超過はチェックサム付きで切り詰め（ファイル名長制限対策）
	var many []string
	for range 30 {
		many = append(many, "very-long-character-id")
	}
	got := designFileTag(many)
	if len(got) > maxDesignFileTagBytes+9 { // "_%08x" 分
		t.Errorf("designFileTag length = %d, want <= %d", len(got), maxDesignFileTagBytes+9)
	}

	// 異なる組み合わせはチェックサムで区別される
	other := designFileTag(append(many[:29:29], "different-id"))
	if got == other {
		t.Error("different long ID sets must produce different tags")
	}

	// マルチバイトIDを境界で切ってもUTF-8として正しい
	var jp []string
	for range 30 {
		jp = append(jp, "ずんだもん")
	}
	jpTag := designFileTag(jp)
	if !utf8.ValidString(jpTag) {
		t.Errorf("designFileTag(%q...) produced invalid UTF-8: %q", jp[0], jpTag)
	}
}

func TestGenerateDesignSheetUnknownCharacterFails(t *testing.T) {
	t.Parallel()
	dr, _, _ := newTestRunner(t)

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
	dr, _, _ := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		OutputDir:    "gs://bucket/out",
	})
	if err == nil || !strings.Contains(err.Error(), "job_id") {
		t.Errorf("err = %v, want job_id-required error", err)
	}
}
