package comic

import (
	"slices"
	"testing"
)

func chapterPagesState() *MangaState {
	return &MangaState{
		Version: StateSchemaVersion,
		Chapters: []Chapter{
			{ID: "ch01"},
			{ID: "ch02"},
		},
		Panels: []Panel{
			{ID: "ch01-p01", ChapterID: "ch01", Page: 1},
			{ID: "ch01-p02", ChapterID: "ch01", Page: 1},
			{ID: "ch01-p03", ChapterID: "ch01", Page: 2},
			{ID: "ch02-p01", ChapterID: "ch02", Page: 3},
			// ページ未割り当て（Repaginate 前）のコマは対象外。
			{ID: "ch02-p02", ChapterID: "ch02", Page: 0},
		},
	}
}

func TestPagesForChapter(t *testing.T) {
	t.Parallel()
	s := chapterPagesState()

	if got := s.PagesForChapter("ch01"); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("PagesForChapter(ch01) = %v, want [1 2]（重複なし・昇順）", got)
	}
	if got := s.PagesForChapter("ch02"); !slices.Equal(got, []int{3}) {
		t.Errorf("PagesForChapter(ch02) = %v, want [3]（ページ未割り当ては除く）", got)
	}
	if got := s.PagesForChapter("ch99"); len(got) != 0 {
		t.Errorf("PagesForChapter(ch99) = %v, want empty", got)
	}
	var nilState *MangaState
	if got := nilState.PagesForChapter("ch01"); got != nil {
		t.Errorf("nil レシーバ = %v, want nil", got)
	}
}

func TestPanelsForChapter(t *testing.T) {
	t.Parallel()
	s := chapterPagesState()

	got := s.PanelsForChapter("ch01")
	if len(got) != 3 || got[0].ID != "ch01-p01" || got[2].ID != "ch01-p03" {
		t.Errorf("PanelsForChapter(ch01) = %+v, want ch01 のコマを state の並びで", got)
	}
	if got := s.PanelsForChapter("ch99"); len(got) != 0 {
		t.Errorf("PanelsForChapter(ch99) = %+v, want empty", got)
	}
}
