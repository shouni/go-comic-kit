package comic

import (
	"testing"
)

// panelsForChapters builds a Panel slice from a panel count per chapter ID.
func panelsForChapters(t *testing.T, spec ...any) []Panel {
	t.Helper()
	if len(spec)%2 != 0 {
		t.Fatalf("spec must repeat chapter ID, panel count: %v", spec)
	}
	var panels []Panel
	for i := 0; i < len(spec); i += 2 {
		chapterID := spec[i].(string)
		count := spec[i+1].(int)
		for n := 1; n <= count; n++ {
			panels = append(panels, Panel{
				ID:        chapterID + "-p" + string(rune('0'+n)),
				ChapterID: chapterID,
			})
		}
	}
	return panels
}

// pagesOf returns the page number assigned to each panel, in order.
func pagesOf(panels []Panel) []int {
	pages := make([]int, len(panels))
	for i := range panels {
		pages[i] = panels[i].Page
	}
	return pages
}

func TestRepaginateBreaksAtChapterBoundary(t *testing.T) {
	// Even when ch01 does not end exactly at the limit, ch02 must start on a new page.
	state := &MangaState{Panels: panelsForChapters(t, "ch01", 4, "ch02", 3)}

	state.Repaginate(3)

	got := pagesOf(state.Panels)
	want := []int{1, 1, 1, 2, 3, 3, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Repaginate() pages = %v, want %v", got, want)
		}
	}
}

func TestRepaginateWithoutChapterIDsFillsPages(t *testing.T) {
	// A state without chapter IDs (hand-written, etc.) is just packed up to the limit, as before.
	state := &MangaState{Panels: []Panel{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}, {ID: "p4"}, {ID: "p5"}}}

	state.Repaginate(2)

	got := pagesOf(state.Panels)
	want := []int{1, 1, 2, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Repaginate() pages = %v, want %v", got, want)
		}
	}
}

func TestRepaginateKeepsUnchangedPageArtifacts(t *testing.T) {
	// If ch01's panel composition is unchanged, composed page images are not discarded
	// (so regenerating an unrelated chapter does not redo expensive page composition).
	state := &MangaState{
		Panels: panelsForChapters(t, "ch01", 2, "ch02", 2),
		Pages: []PageArtifact{
			{PageNumber: 1, PanelIDs: []string{"ch01-p1", "ch01-p2"},
				Generation: &GenerationRecord{ImageURL: "gs://b/page_1.png"}},
			{PageNumber: 2, PanelIDs: []string{"ch02-p1", "ch02-p2"},
				Generation: &GenerationRecord{ImageURL: "gs://b/page_2.png"}},
		},
	}

	state.Repaginate(6)

	if len(state.Pages) != 2 {
		t.Fatalf("Pages = %d, want 2 (composition unchanged, so both should remain)", len(state.Pages))
	}
}

func TestRepaginateDropsStalePageArtifacts(t *testing.T) {
	// ch01 regenerated from 2 panels to 3. Page 1's composition changed, so its record is
	// discarded; page 2's is unchanged and stays. The record of page 9, which has no
	// panels, is discarded too.
	state := &MangaState{
		Panels: panelsForChapters(t, "ch01", 3, "ch02", 1),
		Pages: []PageArtifact{
			{PageNumber: 1, PanelIDs: []string{"ch01-p1", "ch01-p2"},
				Generation: &GenerationRecord{ImageURL: "gs://b/page_1.png"}},
			{PageNumber: 2, PanelIDs: []string{"ch02-p1"},
				Generation: &GenerationRecord{ImageURL: "gs://b/page_2.png"}},
			{PageNumber: 9, PanelIDs: []string{"ghost-p1"},
				Generation: &GenerationRecord{ImageURL: "gs://b/page_9.png"}},
		},
	}

	state.Repaginate(6)

	if len(state.Pages) != 1 {
		t.Fatalf("Pages = %+v, want 1 (only page 2 remains)", state.Pages)
	}
	if state.Pages[0].PageNumber != 2 {
		t.Errorf("remaining page = %d, want 2", state.Pages[0].PageNumber)
	}
	if state.PageArtifactByNumber(1) != nil {
		t.Error("the record of page 1, whose composition changed, remains")
	}
	if state.PageArtifactByNumber(9) != nil {
		t.Error("the record of page 9, which has no panels, remains")
	}
}

// Rebuilding a chapter with the same panel count reproduces the original panel ID
// sequence. Judging by ID equality alone would keep the stale page images, and a
// SkipGenerated recomposition would skip those pages.
func TestReplaceChapterPanelsDropsPagesOfSameSizedRegeneration(t *testing.T) {
	state := &MangaState{
		Chapters: []Chapter{{ID: "ch01"}, {ID: "ch02"}},
		Panels:   panelsForChapters(t, "ch01", 2, "ch02", 1),
		Pages: []PageArtifact{
			{PageNumber: 1, PanelIDs: []string{"ch01-p1", "ch01-p2"},
				Generation: &GenerationRecord{ImageURL: "gs://b/page_1.png"}},
			{PageNumber: 2, PanelIDs: []string{"ch02-p1"},
				Generation: &GenerationRecord{ImageURL: "gs://b/page_2.png"}},
		},
	}

	// A chapter with the same IDs and panel count whose contents alone were swapped.
	if !state.ReplaceChapterPanels("ch01", panelsForChapters(t, "ch01", 2)) {
		t.Fatal("ReplaceChapterPanels returned false, want true")
	}
	state.Repaginate(6)

	if state.PageArtifactByNumber(1) != nil {
		t.Error("the record of the rebuilt chapter's page 1 remains (the stale image makes recomposition skip it)")
	}
	if state.PageArtifactByNumber(2) == nil {
		t.Error("the record of page 2 of an unrelated chapter was removed too")
	}
}
