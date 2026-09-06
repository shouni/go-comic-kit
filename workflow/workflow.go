// Package workflow は、設定とクライアント群から go-comic-kit の全操作
// （章立て・章台本・デザインシート・パネル・ページ）を組み立てる DI 層を提供します。
package workflow

import (
	"fmt"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/internal/operations"
	"github.com/shouni/go-comic-kit/internal/reference"
	"github.com/shouni/go-comic-kit/ports"
)

// Args は、全操作の組み立てに必要な依存の集合です。
type Args struct {
	Config ports.Config
	// Downloader は参照画像を http(s) から取得するためだけに使います。
	// 口を GetStream 1 本に絞っているのは、このキットがストリーム取得しか
	// 使わないためです。*httpkit.Client はそのまま渡せます。
	Downloader ports.Downloader
	Reader     ports.ContentReader
	Writer     remoteio.Writer
	// AIClient はテキスト生成（台本）と画像生成（デザインシート・パネル・ページ）に使います。
	AIClient   gemini.Generator
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
// 返された Operations に後始末は要りません（ports.Operations 参照）。
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

	imageGenerator, err := buildImageGenerator(&args, guard, cfg.RequestTimeout)
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
	return ops, nil
}

// buildImageGenerator は画像生成器を組み立てます。
//
// 重ねる順序に意味があります。参照画像の解決（http(s) の取得）は重複排除の内側です。
// 外側に置くと、同じ内容の同時呼び出しが取得だけは各自で行い、生成 1 回のために
// 同じ画像を人数分ダウンロードすることになります。
func buildImageGenerator(args *Args, guard *callguard.Guard, fetchTimeout time.Duration) (operations.ImageGenerator, error) {
	resolver, err := reference.New(args.Downloader, fetchTimeout, 0)
	if err != nil {
		return nil, fmt.Errorf("参照画像の解決経路の構築に失敗しました: %w", err)
	}

	images, err := imagegen.New(args.AIClient)
	if err != nil {
		return nil, fmt.Errorf("画像生成エンジンの初期化に失敗しました: %w", err)
	}

	resolving := &resolvingImageGenerator{inner: images, resolver: resolver}

	// 同一内容の画像生成の同時実行を1回にまとめる（重複タスク・リトライ対策）
	return &singleflightImageGenerator{inner: resolving, guard: guard}, nil
}

// validateArgs は引数のバリデーションを行います。
//
// 文面は Args の実際のフィールド名で書きます。呼び出し側がメッセージを頼りに
// 直す場所を探すため、名前がずれると grep しても当たりません。
func validateArgs(args *Args) error {
	if args.Downloader == nil {
		return fmt.Errorf("Args.Downloader は必須です")
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
