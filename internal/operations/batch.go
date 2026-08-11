package operations

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/shouni/go-comic-kit/comic"
	"github.com/shouni/go-comic-kit/ports"
)

// runBatch は targets の各要素に対して render を最大 maxConcurrency 並列で実行し、
// 結果とエラーを targets と同じ並びで返します（失敗した要素の結果はゼロ値です）。
//
// 最初の失敗で残りを打ち切らないのは、画像生成が高価で、成功した分は呼び出し側が
// state に記録して保存できるためです。そうしておけば、次は未生成分だけを実行できます。
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
			result, err := render(ctx, target)
			if err != nil {
				errs[i] = err
				return nil
			}
			results[i] = result
			return nil
		})
	}
	// render に渡すクロージャは常に nil を返すため、Wait もエラーを返しません
	// （個々の失敗は errs に集約しています）。
	_ = eg.Wait()

	return results, errs
}

// validateBatchChapter は BatchOptions.ChapterID が state に存在するかを確かめます。
// 存在しない ID を 0 件成功として通すと、実行したつもりで何も起きていない状態に
// 気付けません（打ち間違いは静かに全スキップになります）。
func validateBatchChapter(state *comic.MangaState, chapterID string) error {
	if chapterID == "" {
		return nil
	}
	if state.ChapterByID(chapterID) == nil {
		return fmt.Errorf("%w: 章 %q", ports.ErrNotFound, chapterID)
	}
	return nil
}
