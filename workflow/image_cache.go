package workflow

import (
	"sync"
	"time"
)

// referenceCache は gemini-image-kit に渡す TTL 付きの参照キャッシュです
// （gemini-image-kit/ports.ImageCacher を満たします）。
//
// 保持するのは File API へアップロード済みの URI で、件数はキャラクターの参照画像の
// 種類ぶんしかありません。失効はバックグラウンドの掃除係ではなく Get の時点で判定し、
// Set のついでにまとめて捨てます。定期実行の goroutine を持たない代わりに、
// ワークフロー全体から停止処理（Operations.Close）が要らなくなります。
//
// 参照の解決は複数の画像に対して並行に走るため、すべての操作をロックで守ります。
type referenceCache struct {
	defaultTTL time.Duration

	mu      sync.Mutex
	entries map[string]cacheEntry
}

// cacheEntry は1件分の値と失効時刻です。
type cacheEntry struct {
	value     any
	expiresAt time.Time
}

// sweepThreshold は、Set のついでに失効分を捨てる件数のしきい値です。
// 参照画像の種類はたかが知れているので、この数に届くこと自体がまれです。
const sweepThreshold = 64

// newReferenceCache は既定の有効期間を持つキャッシュを作ります。
func newReferenceCache(defaultTTL time.Duration) *referenceCache {
	return &referenceCache{
		defaultTTL: defaultTTL,
		entries:    make(map[string]cacheEntry),
	}
}

// Get は指定キーの値を返します。有効期間を過ぎたものは無かったことにして捨てます。
func (c *referenceCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !time.Now().Before(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.value, true
}

// Set は指定キーの値を有効期間付きで保存します。
//
// ttl が 0 以下なら既定の有効期間を使います。gemini-image-kit は CacheTTL 未指定を
// そのまま 0 として渡してくるため、ここが「無期限」ではなく「既定に従う」で
// なければなりません。無期限にすると、File API 側で失効した URI を参照し続けます。
func (c *referenceCache) Set(key string, value any, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.defaultTTL
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if len(c.entries) >= sweepThreshold {
		for k, entry := range c.entries {
			if !now.Before(entry.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[key] = cacheEntry{value: value, expiresAt: now.Add(ttl)}
}
