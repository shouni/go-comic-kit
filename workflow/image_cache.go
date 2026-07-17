package workflow

import (
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// imageCache は gemini-image-kit に渡す TTL 付き画像キャッシュです。
type imageCache struct {
	cache   *ttlcache.Cache[string, any]
	started bool
	mu      sync.Mutex
}

func newImageCache(defaultExpiration time.Duration) *imageCache {
	c := ttlcache.New[string, any](
		ttlcache.WithTTL[string, any](defaultExpiration),
		ttlcache.WithDisableTouchOnHit[string, any](),
	)

	return &imageCache{cache: c}
}

// Start は TTL 失効処理のバックグラウンドループを開始します（多重開始は無視されます）。
func (c *imageCache) Start() {
	if c == nil || c.cache == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	c.started = true
	go c.cache.Start()
}

// Get は指定キーに対応するキャッシュ値を取得します。
func (c *imageCache) Get(key string) (any, bool) {
	item := c.cache.Get(key)
	if item == nil {
		return nil, false
	}

	return item.Value(), true
}

// Set は指定キーの値を有効期間付きでキャッシュに保存します。
func (c *imageCache) Set(key string, value any, ttl time.Duration) {
	c.cache.Set(key, value, ttl)
}

// Stop は TTL 失効処理を停止します（未開始・多重停止は無視されます）。
func (c *imageCache) Stop() {
	if c == nil || c.cache == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return
	}

	c.started = false
	c.cache.Stop()
}
