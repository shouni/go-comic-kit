package workflow

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestNewRateLimiterDisabled(t *testing.T) {
	t.Parallel()

	if l := newRateLimiter(0); l != nil {
		t.Errorf("newRateLimiter(0) = %v, want nil（制限なし）", l)
	}
	if l := newRateLimiter(-time.Second); l != nil {
		t.Errorf("newRateLimiter(負値) = %v, want nil（制限なし）", l)
	}
	// nil レシーバでも呼べること（呼び出し側に分岐を持たせないため）
	var l *rateLimiter
	if err := l.wait(context.Background()); err != nil {
		t.Errorf("nil.wait() = %v, want nil", err)
	}
}

func TestRateLimiterSpacesCalls(t *testing.T) {
	t.Parallel()

	// バブル内は仮想時計なので、実時間を消費せず経過時間が誤差なく決まります。
	synctest.Test(t, func(t *testing.T) {
		const interval = 20 * time.Millisecond
		l := newRateLimiter(interval)
		ctx := context.Background()

		start := time.Now()
		for range 3 {
			if err := l.wait(ctx); err != nil {
				t.Fatalf("wait() = %v", err)
			}
		}
		elapsed := time.Since(start)

		// 1回目は即時、2・3回目がそれぞれ interval 待つ。
		// 仮想時計なので経過時間はちょうど 2 周期に一致する。
		if want := 2 * interval; elapsed != want {
			t.Errorf("3回の待機 = %v, want %v", elapsed, want)
		}
	})
}

func TestRateLimiterRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	l := newRateLimiter(10 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())

	// 1回目は枠が空いているので即時に返る
	if err := l.wait(ctx); err != nil {
		t.Fatalf("1回目の wait() = %v", err)
	}

	// 2回目は10秒待つことになるが、キャンセルで打ち切られる
	cancel()
	if err := l.wait(ctx); err == nil {
		t.Error("キャンセル済み context の wait() = nil, want context.Canceled")
	}
}
