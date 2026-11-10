package operations

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type mockDesignGenerator struct {
	lastReq ImageRequest
}

func (m *mockDesignGenerator) Generate(_ context.Context, req ImageRequest) (*ImageResponse, error) {
	m.lastReq = req
	return &ImageResponse{Data: []byte("fake-png"), MIMEType: "image/png", UsedSeed: 123}, nil
}

// mockWriter is a spy that records writes.
//
// It does not need to imitate storage behaviour (folding listings, how absence is
// returned), so it is kept in this form rather than using memio. memio's job is
// imitating storage; recording calls is the user's concern.
type mockWriter struct {
	lastPath string
	// lastOptions are the write options after applying them (for checking Content-Type / Cache-Control).
	lastOptions remoteio.WriteOptions
	err         error // fails the save when non-nil
}

func (m *mockWriter) Write(_ context.Context, path string, _ io.Reader, opts ...remoteio.WriteOption) error {
	m.lastPath = path
	m.lastOptions = remoteio.WriteOptions{}
	for _, opt := range opts {
		opt(&m.lastOptions)
	}
	return m.err
}

// --- Helpers ---

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
		Prompt:     designPrompt,
		Characters: cm,
		Generator:  genMock,
		Writer:     writer,
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
		Model:        "design-model",
		Seed:         new(int64(42)),
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

	// The prompt text belongs to the app's implementation, so this checks only "what was
	// passed to the implementation" and "whether the three returned prompts were used as is".
	if designPrompt.data == nil || len(designPrompt.data.Descriptions) != 1 ||
		!strings.Contains(designPrompt.data.Descriptions[0], "orange hair") {
		t.Errorf("Descriptions = %+v, want the character's visual cues", designPrompt.data)
	}
	if designPrompt.data.StyleMode != "" {
		t.Errorf("StyleMode = %q, want the requested mode passed straight through", designPrompt.data.StyleMode)
	}
	if genMock.lastReq.SystemPrompt != fakeSystemPrompt || genMock.lastReq.NegativePrompt != fakeNegativePrompt {
		t.Errorf("system/negative prompt = %q / %q, want them passed through unchanged",
			genMock.lastReq.SystemPrompt, genMock.lastReq.NegativePrompt)
	}
	// Unspecified falls back to the kit's default ratio.
	// Unless it matches panels and pages, the drift suppression from reference images is silently disabled.
	if genMock.lastReq.AspectRatio != "3:4" {
		t.Errorf("AspectRatio = %q, want the configured default 3:4", genMock.lastReq.AspectRatio)
	}
	if genMock.lastReq.Seed == nil || *genMock.lastReq.Seed != 42 {
		t.Errorf("Seed = %v, want 42", genMock.lastReq.Seed)
	}
	// Resolving references is the workflow layer's job, so only the source URLs are passed here.
	if len(genMock.lastReq.Images) != 1 || genMock.lastReq.Images[0] != "gs://bucket/tsumugi.png" {
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
		Model:        "design-model",
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
		Model:        "design-model",
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
	// The same sheet image is recorded for both characters
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
		Model:        "design-model",
		OutputDir:    "gs://bucket/out",
		Override:     override,
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if genMock.lastReq.Images[0] != override.ReferenceURL {
		t.Errorf("ReferenceURL = %q, want override", genMock.lastReq.Images[0])
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
		Model:        "design-model",
		OutputDir:    "gs://bucket/out",
		Override:     override,
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	for _, img := range genMock.lastReq.Images {
		if img == override.ReferenceURL {
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
		Model:        "design-model",
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
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-model",
		OutputDir:    "gs://bucket/out",
		Model:        "gemini-override-model",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if genMock.lastReq.Model != "gemini-override-model" {
		t.Errorf("Model = %q, want overridden model", genMock.lastReq.Model)
	}
}

func TestGenerateDesignSheetUsesRequestedModel(t *testing.T) {
	t.Parallel()
	dr, genMock, _, _ := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-model-default",
		Model:        "design-model",
		OutputDir:    "gs://bucket/out",
	})
	if err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if genMock.lastReq.Model != "design-model" {
		t.Errorf("Model = %q, want the model named by the request", genMock.lastReq.Model)
	}
}

func TestGenerateDesignSheetUnknownCharacterFails(t *testing.T) {
	t.Parallel()
	dr, _, _, _ := newTestRunner(t)

	_, err := dr.GenerateDesignSheet(context.Background(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"unknown"},
		JobID:        "job-7",
		Model:        "design-model",
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

// TestGenerateDesignSheetPrefersAspectMatchedReference checks that the sheet's source
// image is the reference matching the generation ratio (the same rule as panel.go /
// page.go). Drawing from a master at a different ratio distorts the sheet itself, which
// is the identity anchor of everything generated.
func TestGenerateDesignSheetPrefersAspectMatchedReference(t *testing.T) {
	t.Parallel()

	cm, err := characterkit.NewCharacters([]comic.Character{{
		ID:           "tsumugi",
		Name:         "Tsumugi",
		ReferenceURL: "gs://bucket/tsumugi.png",
		ReferenceURLs: map[string]string{
			"3:4": "gs://bucket/tsumugi-3x4.png",
		},
		VisualCues: []string{"orange hair"},
		IsDefault:  true,
	}})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}

	gen := &mockDesignGenerator{}
	dr := NewDesignSheetRunner(DesignSheetRunnerArgs{
		Prompt:     &fakeDesignPrompt{},
		Characters: cm,
		Generator:  gen,
		Writer:     &mockWriter{},
	})

	if _, err := dr.GenerateDesignSheet(t.Context(), nil, ports.DesignSheetRequest{
		CharacterIDs: []string{"tsumugi"},
		JobID:        "job-1",
		OutputDir:    "gs://bucket",
		Model:        "design-model",
		AspectRatio:  "3:4",
	}); err != nil {
		t.Fatalf("GenerateDesignSheet failed: %v", err)
	}

	if len(gen.lastReq.Images) != 1 {
		t.Fatalf("Images = %+v, want 1 reference", gen.lastReq.Images)
	}
	if got := gen.lastReq.Images[0]; got != "gs://bucket/tsumugi-3x4.png" {
		t.Errorf("ReferenceURL = %q, want the 3:4 reference", got)
	}
}
