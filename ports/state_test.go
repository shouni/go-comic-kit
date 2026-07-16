package ports

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func sampleState() *MangaState {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	return &MangaState{
		Version:     StateSchemaVersion,
		ID:          "job-001",
		Title:       "夜明けのフォトン",
		Description: "テスト用の作品",
		StyleMode:   "default",
		DesignSheets: []DesignSheetRef{
			{CharacterID: "zundamon", ImageURL: "gs://bucket/design_zundamon.png", UsedSeed: 10001},
		},
		Panels: []Panel{
			{
				ID:           "p01",
				Page:         1,
				Shot:         "wide",
				Setting:      "放課後の音楽室、夕方",
				VisualAnchor: "夕陽が差し込む音楽室",
				Characters: []PanelCharacter{
					{CharacterID: "zundamon", Prominence: ProminencePrimary, Emotion: "驚き", Position: "left foreground"},
					{CharacterID: "metan", Prominence: ProminenceSecondary, Action: "ずんだもんを睨みつける", Position: "right"},
					{CharacterID: "mob-students", Prominence: ProminenceBackground},
				},
				Dialogues: []DialogueLine{
					{SpeakerID: "zundamon", Text: "なんなのだ！？", Kind: DialogueKindShout},
					{Text: "その日、すべてが変わった——", Kind: DialogueKindNarration},
				},
			},
			{
				ID:           "p02",
				Page:         1,
				VisualAnchor: "沈黙する二人",
				Characters: []PanelCharacter{
					{CharacterID: "metan", Prominence: ProminencePrimary},
					{CharacterID: "zundamon", Prominence: ProminenceSecondary},
				},
				Generation: &GenerationRecord{
					ImageURL:    "gs://bucket/panels/p02.png",
					UsedSeed:    42,
					Prompt:      "prompt used",
					Model:       "test-model",
					GeneratedAt: now,
				},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestMangaStateJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := sampleState()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var restored MangaState
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !reflect.DeepEqual(original, &restored) {
		t.Errorf("round trip mismatch:\noriginal: %+v\nrestored: %+v", original, &restored)
	}
}

func TestMangaStatePanelByID(t *testing.T) {
	t.Parallel()

	s := sampleState()

	p := s.PanelByID("p02")
	if p == nil || p.ID != "p02" {
		t.Fatalf("PanelByID(p02) = %+v, want panel p02", p)
	}

	// 返されたポインタ経由の更新が state に反映されること（再生成フローの前提）
	p.Generation.UsedSeed = 777
	if s.Panels[1].Generation.UsedSeed != 777 {
		t.Errorf("mutation via PanelByID pointer did not reflect into state")
	}

	if got := s.PanelByID("missing"); got != nil {
		t.Errorf("PanelByID(missing) = %+v, want nil", got)
	}
	var nilState *MangaState
	if got := nilState.PanelByID("p01"); got != nil {
		t.Errorf("nil state PanelByID = %+v, want nil", got)
	}
}

func TestMangaStateUniqueCharacterIDs(t *testing.T) {
	t.Parallel()

	s := sampleState()
	want := []string{"zundamon", "metan", "mob-students"}
	if got := s.UniqueCharacterIDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("UniqueCharacterIDs = %v, want %v", got, want)
	}
}

func TestPanelReferencedCharacterIDsExcludesBackground(t *testing.T) {
	t.Parallel()

	s := sampleState()
	want := []string{"zundamon", "metan"}
	if got := s.Panels[0].ReferencedCharacterIDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("ReferencedCharacterIDs = %v, want %v (background must be excluded)", got, want)
	}
}
