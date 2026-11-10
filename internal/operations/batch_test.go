package operations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// concurrencyProbe is an ImageGenerator that records the maximum number of generations
// running at once. Requests containing a prompt fragment in failOn are failed.
type concurrencyProbe struct {
	mu      sync.Mutex
	inFlite int
	peak    int
	calls   atomic.Int32
	hold    time.Duration
	failOn  string
}

func (p *concurrencyProbe) Generate(_ context.Context, req ImageRequest) (*ImageResponse, error) {
	p.mu.Lock()
	p.inFlite++
	if p.inFlite > p.peak {
		p.peak = p.inFlite
	}
	p.mu.Unlock()

	p.calls.Add(1)
	if p.hold > 0 {
		time.Sleep(p.hold)
	}

	p.mu.Lock()
	p.inFlite--
	p.mu.Unlock()

	if p.failOn != "" && strings.Contains(req.Prompt, p.failOn) {
		return nil, fmt.Errorf("boom")
	}
	return &ImageResponse{Data: []byte("fake-png"), MIMEType: "image/png", UsedSeed: 777}, nil
}

// concurrentWriter is a mockWriter replacement that tolerates parallel writes.
type concurrentWriter struct {
	mu    sync.Mutex
	paths []string
}

func (w *concurrentWriter) Write(_ context.Context, path string, _ io.Reader, _ ...remoteio.WriteOption) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.paths = append(w.paths, path)
	return nil
}

// batchState builds a state with the given number of panels (one chapter, all panels on one page).
func batchState(t *testing.T, panelCount int) *comic.MangaState {
	t.Helper()
	state := &comic.MangaState{
		Version:  comic.StateSchemaVersion,
		Chapters: []comic.Chapter{{ID: "ch01", Title: "導入"}},
	}
	for i := 1; i <= panelCount; i++ {
		state.Panels = append(state.Panels, comic.Panel{
			ID:           fmt.Sprintf("ch01-p%02d", i),
			ChapterID:    "ch01",
			Page:         1,
			VisualAnchor: fmt.Sprintf("scene-%02d", i),
			Characters: []comic.PanelCharacter{
				{CharacterID: "zundamon", Prominence: comic.ProminencePrimary},
			},
		})
	}
	return state
}

func TestGenerateAllPanelsRunsConcurrently(t *testing.T) {
	t.Parallel()

	// Inside the bubble, hold's time.Sleep only advances virtual time, which guarantees the
	// next panel does not start until a slot frees up. Not depending on real time, the test
	// can expect exactly MaxConcurrency rather than "between 2 and 4".
	synctest.Test(t, func(t *testing.T) {
		probe := &concurrencyProbe{hold: 30 * time.Millisecond}
		runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
		runner.generator = probe
		runner.writer = &concurrentWriter{}
		runner.maxConcurrency = 4

		state := batchState(t, 8)
		state, err := runner.GenerateAllPanels(context.Background(), state, ports.BatchOptions{OutputDir: "gs://b/job", Model: "batch-model"})
		if err != nil {
			t.Fatalf("GenerateAllPanels failed: %v", err)
		}

		if got := probe.calls.Load(); got != 8 {
			t.Errorf("generations = %d, want 8", got)
		}
		if probe.peak != 4 {
			t.Errorf("peak concurrency = %d, want 4 (lower means not running in parallel, higher means exceeding MaxConcurrency)", probe.peak)
		}
		for i := range state.Panels {
			if state.Panels[i].Generation == nil {
				t.Fatalf("panel %q has no generation record", state.Panels[i].ID)
			}
		}
	})
}

func TestGenerateAllPanelsSerialByDefault(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		probe := &concurrencyProbe{hold: 10 * time.Millisecond}
		runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
		runner.generator = probe
		runner.writer = &concurrentWriter{}
		// Mimic NewPanelImageRunner's default (MaxConcurrency unset → 1)
		runner.maxConcurrency = 1

		if _, err := runner.GenerateAllPanels(context.Background(), batchState(t, 4), ports.BatchOptions{Model: "batch-model"}); err != nil {
			t.Fatalf("GenerateAllPanels failed: %v", err)
		}
		if probe.peak != 1 {
			t.Errorf("peak concurrency = %d, want 1 (the default is sequential)", probe.peak)
		}
	})
}

func TestGenerateAllPanelsKeepsSuccessesOnPartialFailure(t *testing.T) {
	t.Parallel()

	// Fail only the scene-02 panel
	probe := &concurrencyProbe{failOn: "scene-02"}
	runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}
	runner.maxConcurrency = 3

	state, err := runner.GenerateAllPanels(context.Background(), batchState(t, 3), ports.BatchOptions{Model: "batch-model"})
	if err == nil {
		t.Fatal("GenerateAllPanels succeeded, want error")
	}
	if state == nil {
		t.Fatal("state = nil, want the state with the successes recorded")
	}
	if !errors.Is(err, ports.ErrGeneration) {
		t.Errorf("err = %v, want one containing ports.ErrGeneration", err)
	}

	if state.Panels[0].Generation == nil || state.Panels[2].Generation == nil {
		t.Error("the successful panel's generation record is missing from the state")
	}
	if state.Panels[1].Generation != nil {
		t.Error("the failed panel has a generation record")
	}
}

func TestGenerateAllPanelsSkipGenerated(t *testing.T) {
	t.Parallel()

	probe := &concurrencyProbe{}
	runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}

	state := batchState(t, 3)
	state.Panels[0].Generation = &comic.GenerationRecord{ImageURL: "gs://b/done.png"}

	if _, err := runner.GenerateAllPanels(context.Background(), state,
		ports.BatchOptions{SkipGenerated: true, Model: "batch-model"}); err != nil {
		t.Fatalf("GenerateAllPanels failed: %v", err)
	}

	if got := probe.calls.Load(); got != 2 {
		t.Errorf("generations = %d, want 2 (the already generated panel is skipped)", got)
	}
	if state.Panels[0].Generation.ImageURL != "gs://b/done.png" {
		t.Error("the already generated panel's record was overwritten")
	}
}

func TestGenerateAllPanelsNilState(t *testing.T) {
	t.Parallel()

	runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
	if _, err := runner.GenerateAllPanels(context.Background(), nil, ports.BatchOptions{Model: "batch-model"}); !errors.Is(err, ports.ErrInvalidRequest) {
		t.Errorf("err = %v, want ports.ErrInvalidRequest", err)
	}
}

// An element whose turn comes after the cutoff must not start generating. If it did,
// every remaining item would still be billed after the job timed out.
func TestRunBatchStopsStartingAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	results, errs := runBatch(ctx, 1, []int{0, 1, 2, 3}, func(_ context.Context, index int) (int, error) {
		calls.Add(1)
		if index == 1 {
			cancel() // the job is cut off while the 2nd item is generating
		}
		return index + 10, nil
	})

	if got := calls.Load(); got != 2 {
		t.Errorf("render calls = %d, want 2 (nothing starts after the cutoff)", got)
	}
	if results[0] != 10 || results[1] != 11 {
		t.Errorf("results = %v, want the 2 finished results kept", results)
	}
	for _, i := range []int{2, 3} {
		if !errors.Is(errs[i], context.Canceled) {
			t.Errorf("errs[%d] = %v, want context.Canceled", i, errs[i])
		}
	}
}

func TestUniquePageNumbersSortedAndDeduplicated(t *testing.T) {
	t.Parallel()

	panels := []comic.Panel{{Page: 3}, {Page: 1}, {Page: 3}, {Page: 2}, {Page: 1}}
	got := uniquePageNumbers(panels)
	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("uniquePageNumbers() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("uniquePageNumbers() = %v, want %v", got, want)
		}
	}
}

// batchPageState builds a state with the given number of pages (one generated panel per page).
func batchPageState(t *testing.T, pageCount int) *comic.MangaState {
	t.Helper()
	state := &comic.MangaState{
		Version:  comic.StateSchemaVersion,
		Chapters: []comic.Chapter{{ID: "ch01", Title: "導入"}},
	}
	for i := 1; i <= pageCount; i++ {
		state.Panels = append(state.Panels, comic.Panel{
			ID:           fmt.Sprintf("ch01-p%02d", i),
			ChapterID:    "ch01",
			Page:         i,
			VisualAnchor: fmt.Sprintf("page-%02d", i),
			Characters: []comic.PanelCharacter{
				{CharacterID: "zundamon", Prominence: comic.ProminencePrimary},
			},
			Generation: &comic.GenerationRecord{ImageURL: fmt.Sprintf("gs://b/panels/p%02d.png", i)},
		})
	}
	return state
}

func TestComposeAllPagesRunsConcurrently(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		probe := &concurrencyProbe{hold: 30 * time.Millisecond}
		runner, _, _ := newPageRunner(t, &fakePagePrompt{})
		runner.generator = probe
		runner.writer = &concurrentWriter{}
		runner.maxConcurrency = 3

		state, err := runner.ComposeAllPages(context.Background(), batchPageState(t, 6),
			ports.BatchOptions{OutputDir: "gs://b/job", Model: "batch-model"})
		if err != nil {
			t.Fatalf("ComposeAllPages failed: %v", err)
		}

		if got := probe.calls.Load(); got != 6 {
			t.Errorf("compositions = %d, want 6", got)
		}
		if probe.peak != 3 {
			t.Errorf("peak concurrency = %d, want 3 (lower means not running in parallel, higher means exceeding MaxConcurrency)", probe.peak)
		}

		if len(state.Pages) != 6 {
			t.Fatalf("Pages = %d, want 6", len(state.Pages))
		}
		// One upsert per page number
		for page := 1; page <= 6; page++ {
			artifact := state.PageArtifactByNumber(page)
			if artifact == nil || artifact.Generation == nil {
				t.Errorf("no record for page %d", page)
			}
		}
	})
}

func TestComposeAllPagesKeepsSuccessesOnPartialFailure(t *testing.T) {
	t.Parallel()

	probe := &concurrencyProbe{failOn: "page-02"}
	runner, _, _ := newPageRunner(t, &fakePagePrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}
	runner.maxConcurrency = 3

	state, err := runner.ComposeAllPages(context.Background(), batchPageState(t, 3), ports.BatchOptions{Model: "batch-model"})
	if err == nil {
		t.Fatal("ComposeAllPages succeeded, want error")
	}
	if state == nil {
		t.Fatal("state = nil, want the state with the successes recorded")
	}
	if len(state.Pages) != 2 {
		t.Errorf("Pages = %d, want 2 (only the successes are recorded)", len(state.Pages))
	}
	if state.PageArtifactByNumber(2) != nil {
		t.Error("the failed page 2 has a record")
	}
}

func TestComposeAllPagesSkipGenerated(t *testing.T) {
	t.Parallel()

	probe := &concurrencyProbe{}
	runner, _, _ := newPageRunner(t, &fakePagePrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}

	state := batchPageState(t, 3)
	state.SetPageArtifact(comic.PageArtifact{
		PageNumber: 1,
		PanelIDs:   []string{"ch01-p01"},
		Generation: &comic.GenerationRecord{ImageURL: "gs://b/page_1.png"},
	})

	if _, err := runner.ComposeAllPages(context.Background(), state,
		ports.BatchOptions{SkipGenerated: true, Model: "batch-model"}); err != nil {
		t.Fatalf("ComposeAllPages failed: %v", err)
	}

	if got := probe.calls.Load(); got != 2 {
		t.Errorf("compositions = %d, want 2 (the already composed page is skipped)", got)
	}
	if state.PageArtifactByNumber(1).Generation.ImageURL != "gs://b/page_1.png" {
		t.Error("the already composed page's record was overwritten")
	}
}

// Checks that the art style mode reaches the prompt implementation in a batch too.
// A batch repacks BatchOptions into GenerateOptions and runs them one by one, so one item
// is checked to confirm the repacking does not drop it.
func TestGenerateAllPanelsPassesStyleMode(t *testing.T) {
	t.Parallel()

	prompt := &fakePanelPrompt{}
	runner, _, _ := newPanelRunner(t, prompt)

	if _, err := runner.GenerateAllPanels(context.Background(), batchState(t, 2),
		ports.BatchOptions{StyleMode: "watercolor", Model: "batch-model"}); err != nil {
		t.Fatalf("GenerateAllPanels failed: %v", err)
	}
	if got := prompt.data.StyleMode; got != "watercolor" {
		t.Errorf("StyleMode = %q, want watercolor", got)
	}
}
