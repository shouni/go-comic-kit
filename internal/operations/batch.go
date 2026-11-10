package operations

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/shouni/go-comic-kit/comic"
	"github.com/shouni/go-comic-kit/ports"
)

// runBatch runs render for each element of targets with at most maxConcurrency in
// parallel, and returns the results and errors in the same order as targets (a failed
// element's result is the zero value).
//
// It does not abort the rest on the first failure, so that the successes can be recorded
// and only the missing ones re-run (see ports.PanelImageGenerator.GenerateAllPanels).
// An element whose turn comes after ctx is done does not call render; ctx's error is
// recorded instead.
func runBatch[T any](
	ctx context.Context,
	maxConcurrency int,
	targets []int,
	render func(ctx context.Context, index int) (T, error),
) ([]T, []error) {
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	results := make([]T, len(targets))
	errs := make([]error, len(targets))

	var eg errgroup.Group
	eg.SetLimit(maxConcurrency)
	for i, target := range targets {
		eg.Go(func() error {
			// After the caller cancels, no new generation starts (finished results are kept).
			// The AI call behind render runs detached from ctx, so without stopping here,
			// every remaining item would still be fired one by one, and billed, after the
			// job was cut off.
			if err := ctx.Err(); err != nil {
				errs[i] = err
				return nil
			}
			result, err := render(ctx, target)
			if err != nil {
				errs[i] = err
				return nil
			}
			results[i] = result
			return nil
		})
	}
	// The closures passed to eg.Go always return nil, so Wait returns no error either
	// (individual failures are gathered in errs).
	_ = eg.Wait()

	return results, errs
}

// validateBatchChapter checks that BatchOptions.ChapterID exists in the state.
// For why an unknown ID is not passed as a zero-item success, see ports.BatchOptions.ChapterID.
func validateBatchChapter(state *comic.MangaState, chapterID string) error {
	if chapterID == "" {
		return nil
	}
	if state.ChapterByID(chapterID) == nil {
		return fmt.Errorf("%w: chapter %q", ports.ErrNotFound, chapterID)
	}
	return nil
}
