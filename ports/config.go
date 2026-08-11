// Package ports は、go-comic-kit の操作の契約（インターフェース）と、
// その入出力・設定を定義します。データモデルそのものは comic パッケージが持ちます。
package ports

import (
	"time"

	"github.com/shouni/go-comic-kit/comic"
)

// デフォルト値の定義
const (
	// DefaultMaxConcurrency は一括生成（GenerateAllPanels / ComposeAllPages）の
	// 既定の最大並列数です。既定を1にしているのは、明示的に上げるまで従来どおりの
	// 逐次実行を保ち、API クォータの消費のしかたを変えないためです。
	DefaultMaxConcurrency = 1
	// DefaultRequestTimeout は外部 AI 呼び出し1回あたりの既定の上限時間です。
	// 画像生成は数十秒かかることがあるため、余裕を持たせています。
	DefaultRequestTimeout = 5 * time.Minute
	// DefaultMaxChapters は章立て生成（GenerateOutline）の既定の章数上限です。
	DefaultMaxChapters = 8
	// DefaultMaxPanelsPerChapter は章単位の台本生成の既定のパネル数上限です。
	DefaultMaxPanelsPerChapter = 8

	// DefaultCacheControl は、生成画像を保存する際の既定の Cache-Control です。
	// "public" は生成物を公開配信してよいという前提なので、非公開バケットへ書くデプロイでは
	// Config.CacheControl に "private" 等を指定してください。
	DefaultCacheControl = "public, max-age=1800"
)

// Config は Go Comic Kit の各操作を動作させるための基本設定です。
type Config struct {
	// --- Generation Settings ---
	// MaxConcurrency は一括生成（GenerateAllPanels / ComposeAllPages）の最大並列数です。
	// 1コマ・1ページ単位の操作は元から1回の生成しか行わないため影響を受けません。
	MaxConcurrency int
	// RateInterval は AI 呼び出しの発射間隔の下限です（0 で無制限）。テキスト生成・画像生成を
	// まとめて1つのリミッターで制御します。クォータはプロジェクト単位のためです。
	// スループットの上限は MaxConcurrency ではなく 1/RateInterval で決まる点に注意してください
	// （例: RateInterval=10s なら並列数をいくつにしても毎分6回までになります）。
	RateInterval time.Duration
	// CacheControl は生成画像を保存する際の Cache-Control です。
	// 空の場合は DefaultCacheControl（public, max-age=1800）を使います。
	CacheControl string

	// --- Script Settings ---
	// MaxChapters は章立て生成の章数上限です。
	MaxChapters int
	// MaxPanelsPerChapter は章単位の台本生成のパネル数上限です。
	MaxPanelsPerChapter int

	// --- Layout Settings ---
	MaxPanelsPerPage int

	// --- Timeout & Retries ---
	// RequestTimeout は外部 AI 呼び出し1回あたりの上限時間です（テキスト生成・画像生成・
	// 参照画像のアップロードに適用されます）。工程列全体を包む上限ではないので、
	// 一番遅い1回の生成が収まる長さにしてください。0 以下なら DefaultRequestTimeout。
	RequestTimeout time.Duration
}

// ApplyDefaults は未設定（ゼロ値）の項目にデフォルト値を適用します。
func (c *Config) ApplyDefaults() {
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.MaxChapters <= 0 {
		c.MaxChapters = DefaultMaxChapters
	}
	if c.MaxPanelsPerChapter <= 0 {
		c.MaxPanelsPerChapter = DefaultMaxPanelsPerChapter
	}
	if c.MaxPanelsPerPage <= 0 {
		c.MaxPanelsPerPage = comic.DefaultMaxPanelsPerPage
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
}

// Validate は設定値の妥当性を検証します。
//
// 現在このメソッドが弾くものはありません。Config に残るのはキットを壊さず動かすための
// 実行制御だけで、いずれも ApplyDefaults が埋めるためです。モデル名・画風・比率・解像度は
// 呼び出しごとの値になり、検証も各操作が実行前に行います（ErrInvalidRequest）。
// workflow.New が呼ぶ口は残してあります。設定に必須項目が戻ったときの置き場です。
func (c *Config) Validate() error {
	return nil
}
