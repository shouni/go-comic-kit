package ports

import (
	"time"
)

// デフォルト値の定義
const (
	// DefaultGeminiModel はテキスト生成（台本等）の既定モデルです。
	DefaultGeminiModel = "gemini-3-flash-preview"
	// DefaultImageStandardModel は標準・高速な画像生成（パネル用）の既定モデルです。
	DefaultImageStandardModel = "gemini-3-pro-image-preview"
	// DefaultImageQualityModel は高品質な画像生成（ページ・デザインシート用）の既定モデルです。
	DefaultImageQualityModel = "gemini-3-pro-image-preview"
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
	// DefaultMaxPanelsPerPage は1ページに載せるパネル数の既定値です（Repaginate 用）。
	DefaultMaxPanelsPerPage = 6

	// DefaultStyleSuffix は、パネル・ページ画像生成プロンプトに付与する既定の画風指定です。
	// 演出（cinematic lighting 等）を含むため、デザインシートには使いません。
	DefaultStyleSuffix = "Japanese anime style, official art, cel-shaded, clean line art, high-quality manga coloring, expressive eyes, vibrant colors, cinematic lighting, masterpiece, ultra-detailed, flat shading, clear character features, no 3D effect, high resolution"

	// DefaultDesignStyleSuffix は、デザインシート生成プロンプトに付与する既定の画風指定です。
	// シートは他生成物の同一性アンカーとして参照されるため、照明・演出系の指定を含めません
	// （フラットな照明等の制約は DesignSheetRunner 側が常に後置します）。
	DefaultDesignStyleSuffix = "Japanese anime style, official character reference art, cel-shaded, clean line art, vibrant colors, clear character features, no 3D effect, high resolution"
)

// Config は Go Comic Kit の各操作を動作させるための基本設定です。
type Config struct {
	// --- AI Model Settings (Common) ---
	GeminiModel        string
	ImageStandardModel string // 標準・高速（パネル用）
	ImageQualityModel  string // 高品質・高知能（ページ・デザインシート用）

	// --- Generation Settings ---
	// MaxConcurrency は一括生成（GenerateAllPanels / ComposeAllPages）の最大並列数です。
	// 1コマ・1ページ単位の操作は元から1回の生成しか行わないため影響を受けません。
	MaxConcurrency int
	// RateInterval は AI 呼び出しの発射間隔の下限です（0 で無制限）。テキスト生成・画像生成を
	// まとめて1つのリミッターで制御します。クォータはプロジェクト単位のためです。
	// スループットの上限は MaxConcurrency ではなく 1/RateInterval で決まる点に注意してください
	// （例: RateInterval=10s なら並列数をいくつにしても毎分6回までになります）。
	RateInterval time.Duration
	// StyleSuffix はパネル・ページ画像生成に付与する画風指定です。
	StyleSuffix string
	// DesignStyleSuffix はデザインシート生成に付与する画風指定です。
	// パネル用の StyleSuffix とは分離されています（演出照明の混入を防ぐため）。
	DesignStyleSuffix string

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
	if c.GeminiModel == "" {
		c.GeminiModel = DefaultGeminiModel
	}
	if c.ImageStandardModel == "" {
		c.ImageStandardModel = DefaultImageStandardModel
	}
	if c.ImageQualityModel == "" {
		c.ImageQualityModel = DefaultImageQualityModel
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.StyleSuffix == "" {
		c.StyleSuffix = DefaultStyleSuffix
	}
	if c.DesignStyleSuffix == "" {
		c.DesignStyleSuffix = DefaultDesignStyleSuffix
	}
	if c.MaxChapters <= 0 {
		c.MaxChapters = DefaultMaxChapters
	}
	if c.MaxPanelsPerChapter <= 0 {
		c.MaxPanelsPerChapter = DefaultMaxPanelsPerChapter
	}
	if c.MaxPanelsPerPage <= 0 {
		c.MaxPanelsPerPage = DefaultMaxPanelsPerPage
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
}
