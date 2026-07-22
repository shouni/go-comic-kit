package layout

import (
	"context"
	"sync/atomic"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type mockAssetManager struct {
	uploadCount int32
	uploadFunc  func(ctx context.Context, refURL string) (string, error)
}

func (m *mockAssetManager) UploadFile(ctx context.Context, refURL string) (string, error) {
	atomic.AddInt32(&m.uploadCount, 1)
	if m.uploadFunc != nil {
		return m.uploadFunc(ctx, refURL)
	}
	return "https://file-api.google.com/" + refURL, nil
}

func (m *mockAssetManager) DeleteFile(_ context.Context, _ string) error { return nil }

type mockBackend struct {
	isVertex bool
}

func (m *mockBackend) IsVertexAI() bool { return m.isVertex }

// --- Helpers ---

func newTestCharacters(t *testing.T) *ports.Characters {
	t.Helper()
	cm, err := characterkit.NewCharacters([]ports.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			ReferenceURL: "gs://bucket/zunda.png",
			VisualCues:   []string{"green hair"},
		},
		{
			ID:           "metan",
			Name:         "めたん",
			ReferenceURL: "gs://bucket/metan.png",
			VisualCues:   []string{"purple hair"},
			IsDefault:    true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cm
}

func stateWithCharacters(charIDs ...string) *ports.MangaState {
	panel := ports.Panel{ID: "p01", Page: 1}
	for _, id := range charIDs {
		panel.Characters = append(panel.Characters, ports.PanelCharacter{
			CharacterID: id,
			Prominence:  ports.ProminencePrimary,
		})
	}
	// background キャラは参照アップロード対象外であることの検証用
	panel.Characters = append(panel.Characters, ports.PanelCharacter{
		CharacterID: "mob",
		Prominence:  ports.ProminenceBackground,
	})
	return &ports.MangaState{
		Version: ports.StateSchemaVersion,
		ID:      "test",
		Panels:  []ports.Panel{panel},
	}
}

// --- Tests ---

func TestComicComposerPrepareCharacterResources(t *testing.T) {
	ctx := context.Background()
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	mc, err := NewComicComposer(assetMgr, backend, newTestCharacters(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := mc.PrepareCharacterResources(ctx, stateWithCharacters("zundamon")); err != nil {
		t.Fatalf("PrepareCharacterResources failed: %v", err)
	}

	if uri := mc.GetCharacterResourceURI("zundamon"); uri == "" {
		t.Error("zundamon resource not cached")
	}
	// デフォルトキャラクター（metan）も常にアップロード対象
	if uri := mc.GetCharacterResourceURI("metan"); uri == "" {
		t.Error("default character (metan) resource not cached")
	}
	if assetMgr.uploadCount != 2 {
		t.Errorf("uploadCount = %d, want 2 (zundamon + default)", assetMgr.uploadCount)
	}
}

func TestComicComposerVertexAIBypassesUpload(t *testing.T) {
	ctx := context.Background()
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: true}

	mc, err := NewComicComposer(assetMgr, backend, newTestCharacters(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := mc.PrepareCharacterResources(ctx, stateWithCharacters("zundamon")); err != nil {
		t.Fatalf("PrepareCharacterResources failed: %v", err)
	}

	if assetMgr.uploadCount != 0 {
		t.Errorf("uploadCount = %d, want 0 (Vertex AI + gs:// bypasses File API)", assetMgr.uploadCount)
	}
	// Vertex モードでは File API URI は空（gs:// を直接参照する）
	if uri := mc.GetCharacterResourceURI("zundamon"); uri != "" {
		t.Errorf("GetCharacterResourceURI = %q, want empty in Vertex mode", uri)
	}
}

func TestComicComposerPreparePanelResources(t *testing.T) {
	ctx := context.Background()
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	mc, err := NewComicComposer(assetMgr, backend, newTestCharacters(t))
	if err != nil {
		t.Fatal(err)
	}

	state := &ports.MangaState{
		Panels: []ports.Panel{
			{ID: "p01", Generation: &ports.GenerationRecord{ImageURL: "gs://bucket/panels/p01.png"}},
			{ID: "p02", Generation: &ports.GenerationRecord{ImageURL: "gs://bucket/panels/p02.png"}},
			{ID: "p03"}, // 未生成パネルはスキップされる
		},
	}

	if err := mc.PreparePanelResources(ctx, state); err != nil {
		t.Fatalf("PreparePanelResources failed: %v", err)
	}

	if assetMgr.uploadCount != 2 {
		t.Errorf("uploadCount = %d, want 2 (generated panels only)", assetMgr.uploadCount)
	}
	if uri := mc.GetPanelResourceURI("gs://bucket/panels/p01.png"); uri == "" {
		t.Error("panel p01 resource not cached")
	}
}

func TestComicComposerDeduplicatesSharedReferenceURL(t *testing.T) {
	ctx := context.Background()
	assetMgr := &mockAssetManager{}
	backend := &mockBackend{isVertex: false}

	cm, err := characterkit.NewCharacters([]ports.Character{
		{ID: "a", Name: "A", ReferenceURL: "gs://bucket/shared.png", VisualCues: []string{"red hair"}, IsDefault: true},
		{ID: "b", Name: "B", ReferenceURL: "gs://bucket/shared.png", VisualCues: []string{"blue hair"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mc, err := NewComicComposer(assetMgr, backend, cm)
	if err != nil {
		t.Fatal(err)
	}

	if err := mc.PrepareCharacterResources(ctx, stateWithCharacters("a", "b")); err != nil {
		t.Fatalf("PrepareCharacterResources failed: %v", err)
	}

	if assetMgr.uploadCount != 1 {
		t.Errorf("uploadCount = %d, want 1 (same URL must be uploaded once)", assetMgr.uploadCount)
	}
}
