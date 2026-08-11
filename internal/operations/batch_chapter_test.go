package operations

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/shouni/go-comic-kit/comic"
	"github.com/shouni/go-comic-kit/ports"
)

// chapterBatchState は2章ぶんの state を作ります（ch01 = 1ページ目、ch02 = 2ページ目）。
// Repaginate と同じく、1ページに2章を混ぜません。
func chapterBatchState() *comic.MangaState {
	state := &comic.MangaState{
		Version: comic.StateSchemaVersion,
		Chapters: []comic.Chapter{
			{ID: "ch01", Title: "導入"},
			{ID: "ch02", Title: "展開"},
		},
	}
	for _, c := range []struct {
		chapterID string
		page      int
		count     int
	}{{"ch01", 1, 2}, {"ch02", 2, 3}} {
		for i := 1; i <= c.count; i++ {
			state.Panels = append(state.Panels, comic.Panel{
				ID:           fmt.Sprintf("%s-p%02d", c.chapterID, i),
				ChapterID:    c.chapterID,
				Page:         c.page,
				VisualAnchor: fmt.Sprintf("%s-scene-%02d", c.chapterID, i),
				Characters: []comic.PanelCharacter{
					{CharacterID: "zundamon", Prominence: comic.ProminencePrimary},
				},
			})
		}
	}
	return state
}

// 章を指定した一括生成は、その章のコマ・ページだけを対象にします。
// 画像はいちばん高価な工程なので、確認の単位を台本（章単位）と揃えるための絞り込みです。
func TestBatchChapterScope(t *testing.T) {
	t.Parallel()

	t.Run("パネルは指定章のみ", func(t *testing.T) {
		runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})

		state, err := runner.GenerateAllPanels(context.Background(), chapterBatchState(),
			ports.BatchOptions{ChapterID: "ch01"})
		if err != nil {
			t.Fatalf("GenerateAllPanels() error = %v", err)
		}

		for _, p := range state.Panels {
			generated := p.Generation != nil
			if want := p.ChapterID == "ch01"; generated != want {
				t.Errorf("panel %s generated = %v, want %v", p.ID, generated, want)
			}
		}
	})

	t.Run("ページは指定章のページのみ", func(t *testing.T) {
		runner, _, _ := newPageRunner(t, &fakePagePrompt{})

		state, err := runner.ComposeAllPages(context.Background(), chapterBatchState(),
			ports.BatchOptions{ChapterID: "ch02"})
		if err != nil {
			t.Fatalf("ComposeAllPages() error = %v", err)
		}

		// ch02 は2ページ目だけ。1ページ目（ch01）は合成されない。
		if len(state.Pages) != 1 || state.Pages[0].PageNumber != 2 {
			t.Errorf("Pages = %+v, want only page 2", state.Pages)
		}
	})

	t.Run("存在しない章は ErrNotFound", func(t *testing.T) {
		panelRunner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
		pageRunner, _, _ := newPageRunner(t, &fakePagePrompt{})
		opts := ports.BatchOptions{ChapterID: "ch99"}

		if _, err := panelRunner.GenerateAllPanels(context.Background(), chapterBatchState(), opts); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("GenerateAllPanels() error = %v, want ErrNotFound", err)
		}
		if _, err := pageRunner.ComposeAllPages(context.Background(), chapterBatchState(), opts); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("ComposeAllPages() error = %v, want ErrNotFound", err)
		}
	})
}
