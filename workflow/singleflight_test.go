package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-gemini-client/gemini"
	"google.golang.org/genai"
)

// blockingFusionGenerator は release されるまで応答を返さない fake です。
type blockingFusionGenerator struct {
	calls   int32
	release chan struct{}
}

func (g *blockingFusionGenerator) GenerateFusedImage(ctx context.Context, _ imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error) {
	atomic.AddInt32(&g.calls, 1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &imagePorts.ImageResponse{Data: []byte("img"), MimeType: "image/png", UsedSeed: 7}, nil
}

func fusionReq(seed *int64) imagePorts.ImageFusionRequest {
	return imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:  "m",
			Prompt: "p",
			Seed:   seed,
		},
		Images: []imagePorts.ImageURI{{ReferenceURL: "gs://b/ref.png"}},
	}
}

func TestSingleflightFusionDeduplicatesConcurrentCalls(t *testing.T) {
	t.Parallel()

	inner := &blockingFusionGenerator{release: make(chan struct{})}
	g := &singleflightFusionGenerator{inner: inner}

	const callers = 5
	var wg sync.WaitGroup
	results := make([]*imagePorts.ImageResponse, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := g.GenerateFusedImage(context.Background(), fusionReq(nil))
			if err != nil {
				t.Errorf("GenerateFusedImage failed: %v", err)
				return
			}
			results[i] = resp
		}()
	}

	// 全ゴルーチンが in-flight に相乗りするまで少し待ってから解放する
	time.Sleep(100 * time.Millisecond)
	close(inner.release)
	wg.Wait()

	if got := atomic.LoadInt32(&inner.calls); got != 1 {
		t.Errorf("inner calls = %d, want 1 (deduplicated)", got)
	}

	// 応答は呼び出し元ごとに複製され、変更が他に波及しない
	results[0].Data[0] = 'X'
	if results[1].Data[0] == 'X' {
		t.Error("response Data is shared between callers, want cloned")
	}
}

func TestSingleflightFusionDifferentSeedsAreSeparate(t *testing.T) {
	t.Parallel()

	inner := &blockingFusionGenerator{release: make(chan struct{})}
	close(inner.release) // 即時応答
	g := &singleflightFusionGenerator{inner: inner}

	seed1, seed2 := int64(1), int64(2)
	if _, err := g.GenerateFusedImage(context.Background(), fusionReq(&seed1)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GenerateFusedImage(context.Background(), fusionReq(&seed2)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GenerateFusedImage(context.Background(), fusionReq(nil)); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(&inner.calls); got != 3 {
		t.Errorf("inner calls = %d, want 3 (different seeds must not be deduplicated)", got)
	}
}

func TestSingleflightCallerCancelDoesNotKillSharedExecution(t *testing.T) {
	t.Parallel()

	inner := &blockingFusionGenerator{release: make(chan struct{})}
	g := &singleflightFusionGenerator{inner: inner}

	// 呼び出し元A: すぐキャンセルする
	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := g.GenerateFusedImage(ctxA, fusionReq(nil))
		errA <- err
	}()

	// 呼び出し元B: 同一キーで相乗りし、完走を期待する
	respB := make(chan *imagePorts.ImageResponse, 1)
	go func() {
		resp, err := g.GenerateFusedImage(context.Background(), fusionReq(nil))
		if err != nil {
			t.Errorf("caller B failed: %v", err)
		}
		respB <- resp
	}()

	time.Sleep(100 * time.Millisecond)
	cancelA()
	if err := <-errA; err == nil {
		t.Error("caller A returned nil error after cancel, want context error")
	}

	// A のキャンセル後に実行を解放しても B は結果を受け取れる
	close(inner.release)
	select {
	case resp := <-respB:
		if resp == nil || string(resp.Data) != "img" {
			t.Errorf("caller B response = %+v, want shared result", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("caller B timed out, shared execution was killed by caller A's cancel")
	}
}

// countingStructuredGenerator は呼び出し回数を数える fake です。
type countingStructuredGenerator struct {
	calls   int32
	release chan struct{}
}

func (g *countingStructuredGenerator) GenerateWithParts(ctx context.Context, _ string, _ []*genai.Part, _ gemini.GenerateOptions) (*gemini.Response, error) {
	atomic.AddInt32(&g.calls, 1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &gemini.Response{Text: `{"ok":true}`}, nil
}

func TestSingleflightStructuredDeduplicatesConcurrentCalls(t *testing.T) {
	t.Parallel()

	inner := &countingStructuredGenerator{release: make(chan struct{})}
	g := &singleflightStructuredGenerator{inner: inner}

	parts := []*genai.Part{{Text: "same prompt"}}
	opts := gemini.GenerateOptions{ResponseMIMEType: "application/json"}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.GenerateWithParts(context.Background(), "m", parts, opts); err != nil {
				t.Errorf("GenerateWithParts failed: %v", err)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(inner.release)
	wg.Wait()

	if got := atomic.LoadInt32(&inner.calls); got != 1 {
		t.Errorf("inner calls = %d, want 1 (deduplicated)", got)
	}
}
