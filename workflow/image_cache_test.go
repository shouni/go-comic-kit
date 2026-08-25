package workflow

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestReferenceCacheZeroTTLFollowsCacheDefault は、TTL 0 の Set が
// 既定の有効期間に従うことを確認します。
//
// gemini-image-kit は CacheTTL 未指定（0）をそのままキャッシュへ渡します。0 を
// 「無期限」と解釈すると、アップロード済み URI が永久に残り、File API 側で失効した
// files/... を参照し続けて生成が失敗します。保持期間の指定を 1 か所に畳んだのは
// この前提の上です。
func TestReferenceCacheZeroTTLFollowsCacheDefault(t *testing.T) {
	t.Parallel()

	// バブル内の Sleep は仮想時間を進めるだけなので、有効期間の経過を
	// 実時間を待たずに再現できます。
	synctest.Test(t, func(t *testing.T) {
		c := newReferenceCache(20 * time.Millisecond)
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

// TestReferenceCacheExplicitTTLWins は、明示された TTL が既定より優先されることを
// 確認します。
func TestReferenceCacheExplicitTTLWins(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := newReferenceCache(time.Hour)
		c.Set("k", "v", 20*time.Millisecond)

		synctest.Sleep(60 * time.Millisecond)
		if _, ok := c.Get("k"); ok {
			t.Error("明示した TTL ではなく既定の有効期間が使われています")
		}
	})
}

// TestReferenceCacheSweepsExpiredEntries は、失効した項目が Set のついでに
// 捨てられることを確認します。定期実行の goroutine を持たない代わりの掃除なので、
// これが効かないと長寿命プロセスで失効分が溜まり続けます。
func TestReferenceCacheSweepsExpiredEntries(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := newReferenceCache(20 * time.Millisecond)
		for i := range sweepThreshold {
			c.Set(string(rune('a'+i%26))+string(rune('a'+i/26)), "v", 0)
		}
		synctest.Sleep(60 * time.Millisecond)

		c.Set("fresh", "v", time.Hour)
		if got := len(c.entries); got != 1 {
			t.Errorf("失効分が残っています: len(entries) = %d, want 1", got)
		}
	})
}
