package workflow

import (
	"context"
	"sync"
	"time"
)

// rateLimiter は AI 呼び出しの発射間隔に下限を設ける最小限のリミッターです。
// Gemini の RPM クォータに対する保護で、ワークフロー全体（テキスト生成・画像生成）で
// 1つのインスタンスを共有します。クォータはプロジェクト単位で、操作の種類ごとではないためです。
//
// nil レシーバは「制限なし」として扱えるので、Config.RateInterval が未設定のときは
// 呼び出し側に分岐を持たせずそのまま nil を渡せます。
type rateLimiter struct {
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

// newRateLimiter は interval 間隔のリミッターを返します。
// interval が 0 以下の場合は「制限なし」を意味する nil を返します。
func newRateLimiter(interval time.Duration) *rateLimiter {
	if interval <= 0 {
		return nil
	}
	return &rateLimiter{interval: interval}
}

// wait は次に呼び出してよい時刻まで待ちます。
// 呼び出し枠は待機の前に確保するため、同時に待っている呼び出し同士も interval 間隔に並びます。
// ctx がキャンセルされた場合は確保済みの枠を消費したまま ctx.Err() を返します
// （放棄された枠のぶん、次の呼び出しが1周期ぶん余分に待つことがあります）。
func (l *rateLimiter) wait(ctx context.Context) error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	slot := l.next
	if now := time.Now(); slot.Before(now) {
		slot = now
	}
	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
