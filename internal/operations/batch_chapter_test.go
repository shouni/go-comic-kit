package operations

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/shouni/go-comic-kit/comic"
	"github.com/shouni/go-comic-kit/ports"
)

// chapterBatchState builds a two-chapter state (ch01 = page 1, ch02 = page 2).
// Like Repaginate, it never mixes two chapters on one page.
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

// A batch with a chapter targets only that chapter's panels and pages.
// Images are the most expensive step, so this narrowing matches the unit of checking to
// the script's (per chapter).
func TestBatchChapterScope(t *testing.T) {
	t.Parallel()

	t.Run("panels of the given chapter only", func(t *testing.T) {
		runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})

		state, err := runner.GenerateAllPanels(context.Background(), chapterBatchState(),
			ports.BatchOptions{ChapterID: "ch01", Model: "batch-model"})
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

	t.Run("pages of the given chapter only", func(t *testing.T) {
		runner, _, _ := newPageRunner(t, &fakePagePrompt{})

		state, err := runner.ComposeAllPages(context.Background(), chapterBatchState(),
			ports.BatchOptions{ChapterID: "ch02", Model: "batch-model"})
		if err != nil {
			t.Fatalf("ComposeAllPages() error = %v", err)
		}

		// ch02 is only page 2. Page 1 (ch01) is not composed.
		if len(state.Pages) != 1 || state.Pages[0].PageNumber != 2 {
			t.Errorf("Pages = %+v, want only page 2", state.Pages)
		}
	})

	t.Run("unknown chapter is ErrNotFound", func(t *testing.T) {
		panelRunner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
		pageRunner, _, _ := newPageRunner(t, &fakePagePrompt{})
		opts := ports.BatchOptions{ChapterID: "ch99", Model: "batch-model"}

		if _, err := panelRunner.GenerateAllPanels(context.Background(), chapterBatchState(), opts); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("GenerateAllPanels() error = %v, want ErrNotFound", err)
		}
		if _, err := pageRunner.ComposeAllPages(context.Background(), chapterBatchState(), opts); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("ComposeAllPages() error = %v, want ErrNotFound", err)
		}
	})
}
