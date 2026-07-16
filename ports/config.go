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
	// DefaultMaxConcurrency は画像生成の既定の最大並列数です。
	DefaultMaxConcurrency = 1

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
	MaxConcurrency int
	RateInterval   time.Duration
	// StyleSuffix はパネル・ページ画像生成に付与する画風指定です。
	StyleSuffix string
	// DesignStyleSuffix はデザインシート生成に付与する画風指定です。
	// パネル用の StyleSuffix とは分離されています（演出照明の混入を防ぐため）。
	DesignStyleSuffix string

	// --- Layout Settings ---
	MaxPanelsPerPage int

	// --- Timeout & Retries ---
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
}
