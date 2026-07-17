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

func TestMangaStateUniqueReferencedCharacterIDs(t *testing.T) {
	t.Parallel()

	s := sampleState()
	want := []string{"zundamon", "metan"}
	if got := s.UniqueReferencedCharacterIDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("UniqueReferencedCharacterIDs = %v, want %v", got, want)
	}
}

func TestMangaStateReplaceChapterPanels(t *testing.T) {
	t.Parallel()

	s := &MangaState{
		Chapters: []Chapter{
			{ID: "ch01", Title: "第一章"},
			{ID: "ch02", Title: "第二章"},
			{ID: "ch03", Title: "第三章"},
		},
		Panels: []Panel{
			{ID: "ch01-p01", ChapterID: "ch01"},
			{ID: "ch02-p01", ChapterID: "ch02"},
			{ID: "ch02-p02", ChapterID: "ch02"},
			{ID: "ch03-p01", ChapterID: "ch03"},
			{ID: "orphan"}, // 章に属さないパネルは末尾に保持される
		},
	}

	replaced := s.ReplaceChapterPanels("ch02", []Panel{
		{ID: "ch02-p01", ChapterID: "ch02", Shot: "wide"},
		{ID: "ch02-p02", ChapterID: "ch02"},
		{ID: "ch02-p03", ChapterID: "ch02"},
	})
	if !replaced {
		t.Fatal("ReplaceChapterPanels returned false, want true")
	}

	gotIDs := make([]string, len(s.Panels))
	for i, p := range s.Panels {
		gotIDs[i] = p.ID
	}
	wantIDs := []string{"ch01-p01", "ch02-p01", "ch02-p02", "ch02-p03", "ch03-p01", "orphan"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("panel order = %v, want %v", gotIDs, wantIDs)
	}
	if s.Panels[1].Shot != "wide" {
		t.Error("new panel content was not applied")
	}

	ch := s.ChapterByID("ch02")
	if !reflect.DeepEqual(ch.PanelIDs, []string{"ch02-p01", "ch02-p02", "ch02-p03"}) {
		t.Errorf("chapter PanelIDs = %v, want new panel IDs", ch.PanelIDs)
	}

	if s.ReplaceChapterPanels("missing", nil) {
		t.Error("ReplaceChapterPanels(missing) = true, want false")
	}
}

func TestMangaStateRepaginate(t *testing.T) {
	t.Parallel()

	s := &MangaState{Panels: make([]Panel, 7)}
	s.Repaginate(3)

	wantPages := []int{1, 1, 1, 2, 2, 2, 3}
	for i, want := range wantPages {
		if s.Panels[i].Page != want {
			t.Errorf("Panels[%d].Page = %d, want %d", i, s.Panels[i].Page, want)
		}
	}
}

func TestMangaStateSetDesignSheetUpserts(t *testing.T) {
	t.Parallel()

	s := &MangaState{}
	s.SetDesignSheet(DesignSheetRef{CharacterID: "zundamon", ImageURL: "gs://a.png", UsedSeed: 1})
	s.SetDesignSheet(DesignSheetRef{CharacterID: "metan", ImageURL: "gs://b.png", UsedSeed: 2})
	s.SetDesignSheet(DesignSheetRef{CharacterID: "zundamon", ImageURL: "gs://c.png", UsedSeed: 3})

	if len(s.DesignSheets) != 2 {
		t.Fatalf("DesignSheets = %+v, want 2 entries (upsert)", s.DesignSheets)
	}
	if s.DesignSheets[0].ImageURL != "gs://c.png" || s.DesignSheets[0].UsedSeed != 3 {
		t.Errorf("zundamon entry = %+v, want updated to gs://c.png / seed 3", s.DesignSheets[0])
	}
}
