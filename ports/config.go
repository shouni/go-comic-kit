// Package ports は、go-comic-kit の操作の契約（インターフェース）と、
// その入出力・設定を定義します。データモデルそのものは comic パッケージが持ちます。
package ports

import (
	"github.com/shouni/go-comic-kit/comic"

	"fmt"
	"strings"
	"time"
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
	// --- AI Model Settings (Common) ---
	GeminiModel string
	// ImageModel はデザインシート・パネル・ページのすべてに使う画像生成モデルです。
	// 用途ごとにモデルを分ける仕組みは持ちません。どのモデルが「高品質」かは
	// Google のラインナップ次第で、キットが決められる区別ではないためです。
	ImageModel string

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

	// PanelImageSize / PageImageSize は生成画像の解像度です（ImageSize1K / ImageSize2K）。
	// 空なら ApplyDefaults がパネル 1K・ページ 2K を入れます（従来の固定値と同じ）。
	//
	// キットが決めずアプリが選ぶのは、1コマごとに費用が効くためです。どの解像度が
	// 見合うかは作品と予算の話で、キットが持てる判断ではありません。
	PanelImageSize string
	PageImageSize  string

	// AspectRatio はパネル・ページ・デザインシート既定の共通比率です。空なら 3:4。
	//
	// 3つを1つの設定にしているのは、揃っていないと参照画像によるブレ抑制が
	// **黙って無効になる**ためです。キャラクターの参照画像は生成対象と同じ比率のものが
	// 優先されますが（Character.ReferenceURLFor）、一致するものが無いと比率なしの
	// ReferenceURL へ落ちるだけで、エラーにはなりません。
	AspectRatio string

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
	if c.PanelImageSize == "" {
		c.PanelImageSize = ImageSize1K
	}
	if c.PageImageSize == "" {
		// ページはコマを並べた合成先なので、既定はパネルより1段上げます。
		c.PageImageSize = ImageSize2K
	}
	if c.AspectRatio == "" {
		c.AspectRatio = DefaultAspectRatio
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
}

// Validate は既定値を持たない必須項目を検証します。
func (c *Config) Validate() error {
	missing := make([]string, 0, 5)
	if strings.TrimSpace(c.GeminiModel) == "" {
		missing = append(missing, "GeminiModel")
	}
	if strings.TrimSpace(c.ImageModel) == "" {
		missing = append(missing, "ImageModel")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s を指定してください（キットはモデル名と画風指定の既定値を持ちません）", ErrConfigInvalid, strings.Join(missing, ", "))
	}

	// 比率・解像度は黙って既定へ落とさず、書き間違いを起動時に落とします。
	// 落としてしまうと「指定したつもりの比率で生成されない」状態が気付かれずに続きます。
	if !IsAspectRatio(c.AspectRatio) {
		return fmt.Errorf("%w: AspectRatio (%q) は %s のいずれかである必要があります",
			ErrConfigInvalid, c.AspectRatio, strings.Join(AspectRatios(), " / "))
	}
	for _, size := range []struct{ name, value string }{
		{"PanelImageSize", c.PanelImageSize},
		{"PageImageSize", c.PageImageSize},
	} {
		if !IsImageSize(size.value) {
			return fmt.Errorf("%w: %s (%q) は %s / %s のいずれかである必要があります",
				ErrConfigInvalid, size.name, size.value, ImageSize1K, ImageSize2K)
		}
	}
	return nil
}
