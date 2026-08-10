// Package workflow は、設定とクライアント群から go-comic-kit の全操作
// （章立て・章台本・デザインシート・パネル・ページ）を組み立てる DI 層を提供します。
package workflow

import (
	"fmt"
	"time"

	"github.com/shouni/gemini-image-kit/generator"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/internal/operations"
	"github.com/shouni/go-comic-kit/internal/prompts"
	"github.com/shouni/go-comic-kit/ports"
)

const (
	// defaultTTL は gemini-image-kit の内部キャッシュに適用する既定の有効期間です。
	defaultTTL = 10 * time.Minute
	// defaultCacheExpiration は画像キャッシュの既定の失効期間です。
	defaultCacheExpiration = 10 * time.Minute
)

// Args は、全操作の組み立てに必要な依存の集合です。
type Args struct {
	Config     ports.Config
	HTTPClient httpkit.HTTPClient
	Reader     ports.ContentReader
	Writer     remoteio.Writer
	// AIClient はテキスト生成（台本）と標準画質の画像生成（パネル）に使います。
	AIClient gemini.Model
	// AIClientQuality は高品質系の画像生成（デザインシート・ページ合成）に使います。
	// nil の場合は AIClient を使います。
	AIClientQuality gemini.Model
	Characters      *ports.Characters

	// OutlinePrompt / ChapterScriptPrompt / DesignSheetPrompt を指定するとプロンプト構築を
	// 差し替えられます。nil の場合はキット内蔵テンプレート（prompts パッケージ）を使います。
	OutlinePrompt       ports.OutlinePrompt
	ChapterScriptPrompt ports.ChapterScriptPrompt
	DesignSheetPrompt   ports.DesignSheetPrompt
	// PanelPrompt / PagePrompt も同様に差し替え可能です。キット内蔵の既定は、
	// 参照画像との対応やコマ数といった構造的な指示だけを持つ簡潔なものです。
	// 作品ごとの作り込みはアプリ側で実装してください。
	PanelPrompt ports.PanelPrompt
	PagePrompt  ports.PagePrompt
}

// generationUnit は、1つの AI クライアント・モデルに紐づく画像生成一式です。
type generationUnit struct {
	imageGenerator operations.ImageGenerator
	model          string
	cache          *imageCache
}

func (u *generationUnit) stop() {
	if u != nil {
		u.cache.Stop()
	}
}

// New は、設定とキャラクター定義を基に全操作を組み立てて返します。
// 返された Operations は使い終わったら Close を呼んでください（内部キャッシュの停止）。
func New(args Args) (*ports.Operations, error) {
	if err := validateArgs(&args); err != nil {
		return nil, err
	}

	cfg := args.Config
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	aiClientQuality := args.AIClientQuality
	if aiClientQuality == nil {
		aiClientQuality = args.AIClient
	}

	// プロンプトの既定はキット内蔵テンプレート
	outlinePrompt := args.OutlinePrompt
	chapterPrompt := args.ChapterScriptPrompt
	if outlinePrompt == nil || chapterPrompt == nil {
		builtin, err := prompts.NewScriptPrompts()
		if err != nil {
			return nil, fmt.Errorf("内蔵プロンプトテンプレートの読み込みに失敗しました: %w", err)
		}
		if outlinePrompt == nil {
			outlinePrompt = builtin
		}
		if chapterPrompt == nil {
			chapterPrompt = builtin
		}
	}
	designPrompt := args.DesignSheetPrompt
	if designPrompt == nil {
		designPrompt = prompts.DefaultDesignPrompt{}
	}

	// AI 呼び出しの発射間隔はワークフロー全体で1つのリミッターに集約する
	// （クォータはプロジェクト単位で、操作の種類ごとではないため）。
	guard := callGuard{
		limiter: newRateLimiter(cfg.RateInterval),
		timeout: cfg.RequestTimeout,
	}

	standard, err := buildGenerationUnit(&args, args.AIClient, cfg.ImageStandardModel, guard)
	if err != nil {
		return nil, fmt.Errorf("standard 生成ユニットの構築に失敗しました: %w", err)
	}
	quality, err := buildGenerationUnit(&args, aiClientQuality, cfg.ImageQualityModel, guard)
	if err != nil {
		standard.stop()
		return nil, fmt.Errorf("quality 生成ユニットの構築に失敗しました: %w", err)
	}

	// 同一内容のテキスト生成の同時実行を1回にまとめる（重複タスク・リトライ対策）
	textGenerator := &singleflightStructuredGenerator{inner: args.AIClient, guard: guard}

	panelRunner := operations.NewPanelImageRunner(operations.PanelImageRunnerArgs{
		Characters:     args.Characters,
		Prompt:         args.PanelPrompt,
		Generator:      standard.imageGenerator,
		Writer:         args.Writer,
		Model:          standard.model,
		StyleSuffix:    cfg.StyleSuffix,
		MaxConcurrency: cfg.MaxConcurrency,
		CacheControl:   cfg.CacheControl,
	})
	pageRunner := operations.NewPageImageRunner(operations.PageImageRunnerArgs{
		Characters:     args.Characters,
		Prompt:         args.PagePrompt,
		Generator:      quality.imageGenerator,
		Writer:         args.Writer,
		Model:          quality.model,
		StyleSuffix:    cfg.StyleSuffix,
		MaxConcurrency: cfg.MaxConcurrency,
		CacheControl:   cfg.CacheControl,
	})

	ops := &ports.Operations{
		Outline: operations.NewOutlineRunner(
			outlinePrompt, textGenerator, args.Reader, args.Characters,
			cfg.GeminiModel, cfg.MaxChapters,
		),
		ChapterScript: operations.NewChapterScriptRunner(
			chapterPrompt, textGenerator, args.Characters,
			cfg.GeminiModel, cfg.MaxPanelsPerChapter, cfg.MaxPanelsPerPage,
		),
		DesignSheet: operations.NewDesignSheetRunner(operations.DesignSheetRunnerArgs{
			Prompt:       designPrompt,
			Characters:   args.Characters,
			Generator:    quality.imageGenerator,
			Writer:       args.Writer,
			Model:        quality.model,
			StyleSuffix:  cfg.DesignStyleSuffix,
			CacheControl: cfg.CacheControl,
		}),
		Panel:      panelRunner,
		Page:       pageRunner,
		PanelBatch: panelRunner,
		PageBatch:  pageRunner,
		CloseFunc: func() {
			standard.stop()
			quality.stop()
		},
	}
	return ops, nil
}

// buildGenerationUnit は、指定クライアント・モデルの画像生成一式（core・generator）を構築します。
func buildGenerationUnit(args *Args, client gemini.Model, modelName string, guard callGuard) (*generationUnit, error) {
	cache := newImageCache(defaultCacheExpiration)

	core, err := generator.NewGeminiImageCore(generator.GeminiImageCoreConfig{
		AIClient:   client,
		Reader:     args.Reader,
		HTTPClient: args.HTTPClient,
		Cache:      cache,
		CacheTTL:   defaultTTL,
		// 参照画像のアップロードにも AI 呼び出しと同じ上限時間を適用する
		// （Config.RequestTimeout）。
		UploadTimeout: guard.timeout,
	})
	if err != nil {
		cache.Stop()
		return nil, fmt.Errorf("画像生成エンジンの初期化に失敗しました: %w", err)
	}

	gen, err := generator.NewGeminiGenerator(core)
	if err != nil {
		cache.Stop()
		return nil, fmt.Errorf("GeminiGenerator の初期化に失敗しました: %w", err)
	}
	cache.Start()

	return &generationUnit{
		// 同一内容の画像生成の同時実行を1回にまとめる（重複タスク・リトライ対策）
		imageGenerator: &singleflightImageGenerator{inner: gen, guard: guard},
		model:          modelName,
		cache:          cache,
	}, nil
}

// validateArgs は引数のバリデーションを行います。
func validateArgs(args *Args) error {
	if args.HTTPClient == nil {
		return fmt.Errorf("httpClient is required")
	}
	if args.Reader == nil {
		return fmt.Errorf("reader is required")
	}
	if args.Writer == nil {
		return fmt.Errorf("writer is required")
	}
	if args.AIClient == nil {
		return fmt.Errorf("aiClient is required")
	}
	if args.Characters == nil {
		return fmt.Errorf("characters is required")
	}
	return nil
}
