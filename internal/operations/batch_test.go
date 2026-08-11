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
	"time"

	"github.com/shouni/go-comic-kit/comic"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// concurrencyProbe は、同時に走った生成の最大数を記録する ImageGenerator です。
// failOn に含まれるプロンプト断片を持つリクエストは失敗させます。
type concurrencyProbe struct {
	mu      sync.Mutex
	inFlite int
	peak    int
	calls   int32
	hold    time.Duration
	failOn  string
}

func (p *concurrencyProbe) Generate(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	p.mu.Lock()
	p.inFlite++
	if p.inFlite > p.peak {
		p.peak = p.inFlite
	}
	p.mu.Unlock()

	atomic.AddInt32(&p.calls, 1)
	if p.hold > 0 {
		time.Sleep(p.hold)
	}

	p.mu.Lock()
	p.inFlite--
	p.mu.Unlock()

	if p.failOn != "" && strings.Contains(req.Prompt, p.failOn) {
		return nil, fmt.Errorf("boom")
	}
	return &imagePorts.ImageResponse{Data: []byte("fake-png"), MimeType: "image/png", UsedSeed: 777}, nil
}

// concurrentWriter は並列書き込みに耐える mockWriter 代替です。
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

// batchState は指定コマ数の state を作ります（1章・全コマ同一ページ）。
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

	probe := &concurrencyProbe{hold: 30 * time.Millisecond}
	runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}
	runner.maxConcurrency = 4

	state := batchState(t, 8)
	state, err := runner.GenerateAllPanels(context.Background(), state, ports.BatchOptions{OutputDir: "gs://b/job"})
	if err != nil {
		t.Fatalf("GenerateAllPanels failed: %v", err)
	}

	if got := atomic.LoadInt32(&probe.calls); got != 8 {
		t.Errorf("生成回数 = %d, want 8", got)
	}
	if probe.peak < 2 {
		t.Errorf("同時実行の最大数 = %d, want 2 以上（並列に走っていない）", probe.peak)
	}
	if probe.peak > 4 {
		t.Errorf("同時実行の最大数 = %d, want 4 以下（MaxConcurrency を超えている）", probe.peak)
	}
	for i := range state.Panels {
		if state.Panels[i].Generation == nil {
			t.Fatalf("パネル %q に生成記録がない", state.Panels[i].ID)
		}
	}
}

func TestGenerateAllPanelsSerialByDefault(t *testing.T) {
	t.Parallel()

	probe := &concurrencyProbe{hold: 10 * time.Millisecond}
	runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}
	// NewPanelImageRunner の既定（MaxConcurrency 未指定 → 1）を模す
	runner.maxConcurrency = 1

	if _, err := runner.GenerateAllPanels(context.Background(), batchState(t, 4), ports.BatchOptions{}); err != nil {
		t.Fatalf("GenerateAllPanels failed: %v", err)
	}
	if probe.peak != 1 {
		t.Errorf("同時実行の最大数 = %d, want 1（既定は逐次実行）", probe.peak)
	}
}

func TestGenerateAllPanelsKeepsSuccessesOnPartialFailure(t *testing.T) {
	t.Parallel()

	// scene-02 のコマだけ失敗させる
	probe := &concurrencyProbe{failOn: "scene-02"}
	runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}
	runner.maxConcurrency = 3

	state, err := runner.GenerateAllPanels(context.Background(), batchState(t, 3), ports.BatchOptions{})
	if err == nil {
		t.Fatal("GenerateAllPanels succeeded, want error")
	}
	if state == nil {
		t.Fatal("state = nil, want 成功分を記録した state")
	}
	if !errors.Is(err, ports.ErrGeneration) {
		t.Errorf("err = %v, want ports.ErrGeneration を含む", err)
	}

	if state.Panels[0].Generation == nil || state.Panels[2].Generation == nil {
		t.Error("成功したコマの生成記録が state に残っていない")
	}
	if state.Panels[1].Generation != nil {
		t.Error("失敗したコマに生成記録が付いている")
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
		ports.BatchOptions{SkipGenerated: true}); err != nil {
		t.Fatalf("GenerateAllPanels failed: %v", err)
	}

	if got := atomic.LoadInt32(&probe.calls); got != 2 {
		t.Errorf("生成回数 = %d, want 2（生成済みの1コマは飛ばす）", got)
	}
	if state.Panels[0].Generation.ImageURL != "gs://b/done.png" {
		t.Error("生成済みコマの記録が上書きされている")
	}
}

func TestGenerateAllPanelsNilState(t *testing.T) {
	t.Parallel()

	runner, _, _ := newPanelRunner(t, &fakePanelPrompt{})
	if _, err := runner.GenerateAllPanels(context.Background(), nil, ports.BatchOptions{}); !errors.Is(err, ports.ErrInvalidRequest) {
		t.Errorf("err = %v, want ports.ErrInvalidRequest", err)
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

// batchPageState は指定ページ数の state を作ります（各ページ1コマ、生成済み）。
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

	probe := &concurrencyProbe{hold: 30 * time.Millisecond}
	runner, _, _ := newPageRunner(t, &fakePagePrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}
	runner.maxConcurrency = 3

	state, err := runner.ComposeAllPages(context.Background(), batchPageState(t, 6),
		ports.BatchOptions{OutputDir: "gs://b/job"})
	if err != nil {
		t.Fatalf("ComposeAllPages failed: %v", err)
	}

	if got := atomic.LoadInt32(&probe.calls); got != 6 {
		t.Errorf("合成回数 = %d, want 6", got)
	}
	if probe.peak < 2 {
		t.Errorf("同時実行の最大数 = %d, want 2 以上（並列に走っていない）", probe.peak)
	}
	if probe.peak > 3 {
		t.Errorf("同時実行の最大数 = %d, want 3 以下（MaxConcurrency を超えている）", probe.peak)
	}

	if len(state.Pages) != 6 {
		t.Fatalf("Pages = %d 件, want 6", len(state.Pages))
	}
	// ページ番号ごとに1件ずつ upsert されていること
	for page := 1; page <= 6; page++ {
		artifact := state.PageArtifactByNumber(page)
		if artifact == nil || artifact.Generation == nil {
			t.Errorf("ページ %d の記録がない", page)
		}
	}
}

func TestComposeAllPagesKeepsSuccessesOnPartialFailure(t *testing.T) {
	t.Parallel()

	probe := &concurrencyProbe{failOn: "page-02"}
	runner, _, _ := newPageRunner(t, &fakePagePrompt{})
	runner.generator = probe
	runner.writer = &concurrentWriter{}
	runner.maxConcurrency = 3

	state, err := runner.ComposeAllPages(context.Background(), batchPageState(t, 3), ports.BatchOptions{})
	if err == nil {
		t.Fatal("ComposeAllPages succeeded, want error")
	}
	if state == nil {
		t.Fatal("state = nil, want 成功分を記録した state")
	}
	if len(state.Pages) != 2 {
		t.Errorf("Pages = %d 件, want 2（成功した分だけ記録される）", len(state.Pages))
	}
	if state.PageArtifactByNumber(2) != nil {
		t.Error("失敗したページ2の記録が付いている")
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
		ports.BatchOptions{SkipGenerated: true}); err != nil {
		t.Fatalf("ComposeAllPages failed: %v", err)
	}

	if got := atomic.LoadInt32(&probe.calls); got != 2 {
		t.Errorf("合成回数 = %d, want 2（合成済みの1ページは飛ばす）", got)
	}
	if state.PageArtifactByNumber(1).Generation.ImageURL != "gs://b/page_1.png" {
		t.Error("合成済みページの記録が上書きされている")
	}
}

// 一括生成でも画風モードがプロンプト実装まで届くことを確かめます。
// アプリ側が実際に使うのはこちらの経路（PanelBatch / PageBatch）なので、
// GenerateOptions への詰め替えで落ちていないことを1件見ておきます。
func TestGenerateAllPanelsPassesStyleMode(t *testing.T) {
	t.Parallel()

	prompt := &fakePanelPrompt{}
	runner, _, _ := newPanelRunner(t, prompt)

	if _, err := runner.GenerateAllPanels(context.Background(), batchState(t, 2),
		ports.BatchOptions{StyleMode: "watercolor"}); err != nil {
		t.Fatalf("GenerateAllPanels failed: %v", err)
	}
	if got := prompt.data.StyleMode; got != "watercolor" {
		t.Errorf("StyleMode = %q, want watercolor", got)
	}
}
