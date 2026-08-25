package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-gemini-client/gemini"
)

// blockingImageGenerator は release されるまで応答を返さない fake です。
type blockingImageGenerator struct {
	calls   atomic.Int32
	release chan struct{}
}

func (g *blockingImageGenerator) Generate(ctx context.Context, _ imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	g.calls.Add(1)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &imagePorts.ImageResponse{Data: []byte("img"), MimeType: "image/png", UsedSeed: 7}, nil
}

func imageReq(seed *int64) imagePorts.ImageRequest {
	return imagePorts.ImageRequest{
		Model:  "m",
		Prompt: "p",
		Seed:   seed,
		Images: []imagePorts.ImageURI{{ReferenceURL: "gs://b/ref.png"}},
	}
}

func TestSingleflightFusionDeduplicatesConcurrentCalls(t *testing.T) {
	t.Parallel()

	// synctest.Test はこの関数を隔離された「バブル」で実行します。合流待ちを
	// synctest.Wait で表現できるため、待ち時間を秒数で見積もらずに済みます。
	synctest.Test(t, func(t *testing.T) {
		inner := &blockingImageGenerator{release: make(chan struct{})}
		g := &singleflightImageGenerator{inner: inner}

		const callers = 5
		var wg sync.WaitGroup
		results := make([]*imagePorts.ImageResponse, callers)
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

		// 全ゴルーチンが in-flight に相乗りするまで待ってから解放する。
		// Wait はバブル内の他のゴルーチンがすべて継続的にブロックした時点で返ります。
		synctest.Wait()
		close(inner.release)
		wg.Wait()

		if got := inner.calls.Load(); got != 1 {
			t.Errorf("inner calls = %d, want 1 (deduplicated)", got)
		}

		// 応答は呼び出し元ごとに複製され、変更が他に波及しない
		results[0].Data[0] = 'X'
		if results[1].Data[0] == 'X' {
			t.Error("response Data is shared between callers, want cloned")
		}
	})
}

func TestSingleflightFusionDifferentSeedsAreSeparate(t *testing.T) {
	t.Parallel()

	inner := &blockingImageGenerator{release: make(chan struct{})}
	close(inner.release) // 即時応答
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

		// 呼び出し元A: すぐキャンセルする
		ctxA, cancelA := context.WithCancel(context.Background())
		errA := make(chan error, 1)
		go func() {
			_, err := g.Generate(ctxA, imageReq(nil))
			errA <- err
		}()

		// 呼び出し元B: 同一キーで相乗りし、完走を期待する
		respB := make(chan *imagePorts.ImageResponse, 1)
		go func() {
			resp, err := g.Generate(context.Background(), imageReq(nil))
			if err != nil {
				t.Errorf("caller B failed: %v", err)
			}
			respB <- resp
		}()

		// A と B の双方が in-flight に入るまで待つ
		synctest.Wait()
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
			// バブル内の時計は仮想時間なので、この 5 秒で実時間は消費しません。
			t.Fatal("caller B timed out, shared execution was killed by caller A's cancel")
		}
	})
}

// countingStructuredGenerator は呼び出し回数を数える fake です。
type countingStructuredGenerator struct {
	calls   atomic.Int32
	release chan struct{}
}

func (g *countingStructuredGenerator) GenerateWithAttachments(ctx context.Context, _ string, _ string, _ []gemini.Attachment, _ gemini.GenerateOptions) (*gemini.Response, error) {
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
				if _, err := g.GenerateWithAttachments(context.Background(), "m", prompt, nil, opts); err != nil {
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
