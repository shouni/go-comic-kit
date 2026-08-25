// Package workflow は、設定とクライアント群から go-comic-kit の全操作
// （章立て・章台本・デザインシート・パネル・ページ）を組み立てる DI 層を提供します。
package workflow

import (
	"fmt"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/gemini-image-kit/generator"
	"github.com/shouni/go-gemini-client/callguard"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/internal/operations"
	"github.com/shouni/go-comic-kit/ports"
)

// defaultCacheExpiration は、File API へのアップロード結果を保持する期間です。
//
// 保持期間はこの 1 か所だけで決めます。gemini-image-kit の CacheTTL には何も渡さず
// 0 のままにしており、ttlcache では 0 が DefaultTTL、つまり「キャッシュに設定した
// 既定の有効期間に従う」を意味するためです。CacheTTL にも値を書くと、同じ意味の
// 数字が 2 か所に増えて片方だけ変わります。
//
// File API 側の保持期限より短くしておく必要があります（失効した files/... の URI を
// 参照し続けると生成が失敗するため）。無期限にしたい場合でも 0 ではなく
// ttlcache.NoTTL を使ってください — 0 は「無期限」ではありません。
const defaultCacheExpiration = 10 * time.Minute

// Args は、全操作の組み立てに必要な依存の集合です。
type Args struct {
	Config     ports.Config
	HTTPClient httpkit.HTTPClient
	Reader     ports.ContentReader
	Writer     remoteio.Writer
	// AIClient はテキスト生成（台本）と画像生成（デザインシート・パネル・ページ）に使います。
	AIClient   gemini.Model
	Characters *comic.Characters

	// プロンプトは5つとも必須です。キットは内蔵テンプレートを持ちません。
	// 作品ごとに調整する文言なので、キットのリリースを挟まずに変えられる側が持ちます。
	OutlinePrompt       ports.OutlinePrompt
	ChapterScriptPrompt ports.ChapterScriptPrompt
	DesignSheetPrompt   ports.DesignSheetPrompt
	PanelPrompt         ports.PanelPrompt
	PagePrompt          ports.PagePrompt
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

	// AI 呼び出しの発射間隔はワークフロー全体で1つのガードに集約する
	// （クォータはプロジェクト単位で、操作の種類ごとではないため）。
	guard := callguard.New(
		callguard.WithRateInterval(cfg.RateInterval),
		callguard.WithExecTimeout(cfg.RequestTimeout),
	)

	imageGenerator, cache, err := buildImageGenerator(&args, args.AIClient, guard, cfg.RequestTimeout)
	if err != nil {
		return nil, err
	}

	// 同一内容のテキスト生成の同時実行を1回にまとめる（重複タスク・リトライ対策）
	textGenerator := &singleflightStructuredGenerator{inner: args.AIClient, guard: guard}

	panelRunner := operations.NewPanelImageRunner(operations.PanelImageRunnerArgs{
		Characters:     args.Characters,
		Prompt:         args.PanelPrompt,
		Generator:      imageGenerator,
		Writer:         args.Writer,
		MaxConcurrency: cfg.MaxConcurrency,
		CacheControl:   cfg.CacheControl,
	})
	pageRunner := operations.NewPageImageRunner(operations.PageImageRunnerArgs{
		Characters:     args.Characters,
		Prompt:         args.PagePrompt,
		Generator:      imageGenerator,
		Writer:         args.Writer,
		MaxConcurrency: cfg.MaxConcurrency,
		CacheControl:   cfg.CacheControl,
	})

	ops := &ports.Operations{
		Outline: operations.NewOutlineRunner(
			args.OutlinePrompt, textGenerator, args.Reader, args.Characters,
			cfg.MaxChapters,
		),
		ChapterScript: operations.NewChapterScriptRunner(
			args.ChapterScriptPrompt, textGenerator, args.Characters,
			cfg.MaxPanelsPerChapter, cfg.MaxPanelsPerPage,
		),
		DesignSheet: operations.NewDesignSheetRunner(operations.DesignSheetRunnerArgs{
			Prompt:       args.DesignSheetPrompt,
			Characters:   args.Characters,
			Generator:    imageGenerator,
			Writer:       args.Writer,
			CacheControl: cfg.CacheControl,
		}),
		Panel: panelRunner,
		Page:  pageRunner,
	}
	// Vertex AI 経路では cache が nil で、Stop は nil レシーバでも安全に何もしません。
	ops.SetCloseFunc(cache.Stop)
	return ops, nil
}

// buildImageGenerator は画像生成器を組み立て、併せて解放が必要なキャッシュを返します。
// キャッシュは Gemini API 経路でしか作られないため、Vertex AI では nil です。
func buildImageGenerator(args *Args, client gemini.Model, guard *callguard.Guard, uploadTimeout time.Duration) (operations.ImageGenerator, *imageCache, error) {
	resolver, cache, err := buildReferenceResolver(args, client, uploadTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("参照画像の解決経路の構築に失敗しました: %w", err)
	}

	gen, err := generator.New(client, resolver)
	if err != nil {
		cache.Stop()
		return nil, nil, fmt.Errorf("画像生成エンジンの初期化に失敗しました: %w", err)
	}

	// 同一内容の画像生成の同時実行を1回にまとめる（重複タスク・リトライ対策）
	return &singleflightImageGenerator{inner: gen, guard: guard}, cache, nil
}

// buildReferenceResolver は、バックエンドに応じた参照画像の解決経路を組み立てます。
//
// 経路はバックエンドで変わります。Vertex AI は gs:// をモデル側で解決できるため、
// 参照画像を一切転送せずに済み、アップロードもキャッシュも要りません（返る cache は
// nil で、停止すべき goroutine もありません）。Gemini API には gs:// を直接渡す手段が
// 無いので、File API へ上げて URI で使い回します。
//
// どちらの経路でも最後段に取得 → インライン送信を置きます。参照画像は gs:// とは
// 限らず、呼び出し側が一時的な上書きとして http(s) の URL を渡してくるためです。
func buildReferenceResolver(args *Args, client gemini.Model, uploadTimeout time.Duration) (*generator.ResolverChain, *imageCache, error) {
	inline, err := generator.NewFetchResolver(generator.FetchResolverConfig{
		Reader:     args.Reader,
		Downloader: args.HTTPClient,
	})
	if err != nil {
		return nil, nil, err
	}

	if client.IsVertexAI() {
		return generator.NewResolverChain(generator.NewGCSResolver(), inline), nil, nil
	}

	cache := newImageCache(defaultCacheExpiration)
	upload, err := generator.NewFileAPIResolver(generator.FileAPIResolverConfig{
		Files:      client,
		Reader:     args.Reader,
		Downloader: args.HTTPClient,
		Cache:      cache,
		// CacheTTL は渡さない。0 のままにすると cache 側の既定
		// （defaultCacheExpiration）が使われる。
		// アップロードは callguard を通らない（gemini-image-kit の resolver が自前の
		// singleflight を持つ）ため、同じ Config.RequestTimeout をここへ直接渡します。
		UploadTimeout: uploadTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	cache.Start()

	return generator.NewResolverChain(upload, inline), cache, nil
}

// validateArgs は引数のバリデーションを行います。
//
// 文面は Args の実際のフィールド名で書きます。呼び出し側がメッセージを頼りに
// 直す場所を探すため、名前がずれると grep しても当たりません。
func validateArgs(args *Args) error {
	if args.HTTPClient == nil {
		return fmt.Errorf("Args.HTTPClient は必須です")
	}
	if args.Reader == nil {
		return fmt.Errorf("Args.Reader は必須です")
	}
	if args.Writer == nil {
		return fmt.Errorf("Args.Writer は必須です")
	}
	if args.AIClient == nil {
		return fmt.Errorf("Args.AIClient は必須です")
	}
	if args.Characters == nil {
		return fmt.Errorf("Args.Characters は必須です")
	}
	// プロンプトはキットが持ちません。作品ごとに調整する文言なので、キットのリリースを
	// 挟まずに変えられる側（アプリ）が実装します（画風指定・モデル名と同じ理由）。
	for _, p := range []struct {
		name  string
		value any
	}{
		{"Args.OutlinePrompt", args.OutlinePrompt},
		{"Args.ChapterScriptPrompt", args.ChapterScriptPrompt},
		{"Args.DesignSheetPrompt", args.DesignSheetPrompt},
		{"Args.PanelPrompt", args.PanelPrompt},
		{"Args.PagePrompt", args.PagePrompt},
	} {
		if p.value == nil {
			return fmt.Errorf("%s は必須です", p.name)
		}
	}
	return nil
}
