package workflow

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestImageCacheZeroTTLFollowsCacheDefault は、TTL 0 の Set が
// defaultCacheExpiration に従うことを確認します。
//
// gemini-image-kit は CacheTTL 未指定（0）をそのままキャッシュへ渡します。
// ttlcache では 0 が DefaultTTL、-1 (NoTTL) が無期限なので、この 2 つを取り違えると
// アップロード済み URI が永久に残り、File API 側で失効した files/... を参照し続けて
// 生成が失敗します。保持期間の指定を 1 か所に畳んだのはこの前提の上です。
func TestImageCacheZeroTTLFollowsCacheDefault(t *testing.T) {
	t.Parallel()

	// バブル内の time.Sleep は仮想時間を進めるだけなので、有効期間の経過を
	// 実時間を待たずに再現できます。
	synctest.Test(t, func(t *testing.T) {
		c := newImageCache(20 * time.Millisecond)
		c.Set("k", "v", 0)

		if _, ok := c.Get("k"); !ok {
			t.Fatal("保存直後に取得できません")
		}

		synctest.Sleep(60 * time.Millisecond)
		if _, ok := c.Get("k"); ok {
			t.Error("TTL 0 が無期限として扱われています（キャッシュ既定の有効期間に従うべき）")
		}
	})
}
