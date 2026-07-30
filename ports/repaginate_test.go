package ports

import (
	"testing"
)

// panelsForChapters は、章IDごとにコマ数を指定した Panel 列を作ります。
func panelsForChapters(t *testing.T, spec ...any) []Panel {
	t.Helper()
	if len(spec)%2 != 0 {
		t.Fatalf("spec は 章ID・コマ数 の繰り返しで指定してください: %v", spec)
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

// pagesOf は各コマに振られたページ番号を並び順に返します。
func pagesOf(panels []Panel) []int {
	pages := make([]int, len(panels))
	for i := range panels {
		pages[i] = panels[i].Page
	}
	return pages
}

func TestRepaginateBreaksAtChapterBoundary(t *testing.T) {
	// ch01 が上限ちょうどで終わらなくても、ch02 は必ず新しいページから始まること。
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
	// 章IDを持たない（手書き等の）state では、従来どおり上限ごとに詰めるだけ。
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
	// ch01 のコマ構成が変わらなければ、合成済みページ画像は捨てない
	// （無関係な章の再生成で高価なページ合成をやり直さないため）。
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
		t.Fatalf("Pages = %d 件, want 2（構成が変わっていないので両方残るはず）", len(state.Pages))
	}
}

func TestRepaginateDropsStalePageArtifacts(t *testing.T) {
	// ch01 を2コマ→3コマに再生成した状況。ページ1はコマ構成が変わったので記録を捨て、
	// 構成が変わっていないページ2は残す。実体の無いページ9の記録も捨てる。
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
		t.Fatalf("Pages = %+v, want 1件（ページ2だけが残る）", state.Pages)
	}
	if state.Pages[0].PageNumber != 2 {
		t.Errorf("残ったページ = %d, want 2", state.Pages[0].PageNumber)
	}
	if state.PageArtifactByNumber(1) != nil {
		t.Error("コマ構成が変わったページ1の記録が残っている")
	}
	if state.PageArtifactByNumber(9) != nil {
		t.Error("実体の無いページ9の記録が残っている")
	}
}
