package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/go-comic-kit/internal/operations"
)

// blockingImageGenerator is a fake that does not respond until released.
type blockingImageGenerator struct {
	calls   atomic.Int32
	release chan struct{}
}

func (g *blockingImageGenerator) Generate(ctx context.Context, _ operations.ImageRequest) (*operations.ImageResponse, error) {
	g.calls.Add(1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &operations.ImageResponse{Data: []byte("img"), MIMEType: "image/png", UsedSeed: 7}, nil
}

func imageReq(seed *int64) operations.ImageRequest {
	return operations.ImageRequest{
		Model:  "m",
		Prompt: "p",
		Seed:   seed,
		Images: []string{"gs://b/ref.png"},
	}
}

func TestSingleflightFusionDeduplicatesConcurrentCalls(t *testing.T) {
	t.Parallel()

	// synctest.Test runs this function in an isolated "bubble". Waiting for callers to
	// merge can be expressed with synctest.Wait, so no wait time has to be guessed in seconds.
	synctest.Test(t, func(t *testing.T) {
		inner := &blockingImageGenerator{release: make(chan struct{})}
		g := &singleflightImageGenerator{inner: inner}

		const callers = 5
		var wg sync.WaitGroup
		results := make([]*operations.ImageResponse, callers)
		for i := range callers {
			wg.Go(func() {
				resp, err := g.Generate(context.Background(), imageReq(nil))
				if err != nil {
					t.Errorf("Generate failed: %v", err)
					return
				}
				results[i] = resp
			})
		}

		// Wait until every goroutine has joined the in-flight call, then release.
		// Wait returns once every other goroutine in the bubble is durably blocked.
		synctest.Wait()
		close(inner.release)
		wg.Wait()

		if got := inner.calls.Load(); got != 1 {
			t.Errorf("inner calls = %d, want 1 (deduplicated)", got)
		}

		// The response is cloned per caller, so a change does not leak to the others
		results[0].Data[0] = 'X'
		if results[1].Data[0] == 'X' {
			t.Error("response Data is shared between callers, want cloned")
		}
	})
}

func TestSingleflightFusionDifferentSeedsAreSeparate(t *testing.T) {
	t.Parallel()

	inner := &blockingImageGenerator{release: make(chan struct{})}
	close(inner.release) // respond immediately
	g := &singleflightImageGenerator{inner: inner}

	seed1, seed2 := int64(1), int64(2)
	if _, err := g.Generate(context.Background(), imageReq(&seed1)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Generate(context.Background(), imageReq(&seed2)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Generate(context.Background(), imageReq(nil)); err != nil {
		t.Fatal(err)
	}

	if got := inner.calls.Load(); got != 3 {
		t.Errorf("inner calls = %d, want 3 (different seeds must not be deduplicated)", got)
	}
}

func TestSingleflightCallerCancelDoesNotKillSharedExecution(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		inner := &blockingImageGenerator{release: make(chan struct{})}
		g := &singleflightImageGenerator{inner: inner}

		// Caller A: cancels right away
		ctxA, cancelA := context.WithCancel(context.Background())
		errA := make(chan error, 1)
		go func() {
			_, err := g.Generate(ctxA, imageReq(nil))
			errA <- err
		}()

		// Caller B: joins on the same key and is expected to finish
		respB := make(chan *operations.ImageResponse, 1)
		go func() {
			resp, err := g.Generate(context.Background(), imageReq(nil))
			if err != nil {
				t.Errorf("caller B failed: %v", err)
			}
			respB <- resp
		}()

		// Wait until both A and B are in flight
		synctest.Wait()
		cancelA()
		if err := <-errA; err == nil {
			t.Error("caller A returned nil error after cancel, want context error")
		}

		// Releasing the execution after A's cancel still gives B the result
		close(inner.release)
		select {
		case resp := <-respB:
			if resp == nil || string(resp.Data) != "img" {
				t.Errorf("caller B response = %+v, want shared result", resp)
			}
		case <-time.After(5 * time.Second):
			// The clock in the bubble is virtual, so these 5 seconds take no real time.
			t.Fatal("caller B timed out, shared execution was killed by caller A's cancel")
		}
	})
}

// countingStructuredGenerator is a fake that counts its calls.
type countingStructuredGenerator struct {
	calls   atomic.Int32
	release chan struct{}
}

func (g *countingStructuredGenerator) Generate(ctx context.Context, _ string, _ string, _ []gemini.Attachment, _ gemini.GenerateOptions) (*gemini.Response, error) {
	g.calls.Add(1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &gemini.Response{Text: `{"ok":true}`}, nil
}

func TestSingleflightStructuredDeduplicatesConcurrentCalls(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		inner := &countingStructuredGenerator{release: make(chan struct{})}
		g := &singleflightStructuredGenerator{inner: inner}

		const prompt = "same prompt"
		opts := gemini.GenerateOptions{ResponseMIMEType: "application/json"}

		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				if _, err := g.Generate(context.Background(), "m", prompt, nil, opts); err != nil {
					t.Errorf("GenerateWithAttachments failed: %v", err)
				}
			})
		}
		synctest.Wait()
		close(inner.release)
		wg.Wait()

		if got := inner.calls.Load(); got != 1 {
			t.Errorf("inner calls = %d, want 1 (deduplicated)", got)
		}
	})
}
