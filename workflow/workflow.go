// Package workflow provides the DI layer that assembles every go-comic-kit operation
// (outline, chapter script, design sheet, panel, page) from the configuration and clients.
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

// Args is the set of dependencies needed to assemble every operation.
type Args struct {
	Config ports.Config
	// Downloader is used only to fetch reference images over http(s).
	// Its surface is the single method GetStream because this kit only ever fetches
	// streams. A *httpkit.Client can be passed as is.
	Downloader ports.Downloader
	Reader     ports.ContentReader
	Writer     remoteio.Writer
	// AIClient is used for text generation (scripts) and image generation (design sheets, panels, pages).
	AIClient   gemini.Generator
	Characters *comic.Characters

	// All five prompts are required. The kit has no built-in templates.
	// They are wording tuned per work, so they live on the side that can change them
	// without a kit release in between.
	OutlinePrompt       ports.OutlinePrompt
	ChapterScriptPrompt ports.ChapterScriptPrompt
	DesignSheetPrompt   ports.DesignSheetPrompt
	PanelPrompt         ports.PanelPrompt
	PagePrompt          ports.PagePrompt
}

// New assembles and returns every operation from the configuration and character
// definitions. The returned Operations need no cleanup (see ports.Operations).
func New(args Args) (*ports.Operations, error) {
	if err := validateArgs(&args); err != nil {
		return nil, err
	}

	cfg := args.Config
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Spacing between AI calls is gathered into one guard for the whole workflow
	// (quota is per project, not per kind of operation).
	guard := callguard.New(
		callguard.WithRateInterval(cfg.RateInterval),
		callguard.WithExecTimeout(cfg.RequestTimeout),
	)

	imageGenerator, err := buildImageGenerator(&args, guard, cfg.RequestTimeout)
	if err != nil {
		return nil, err
	}

	// Collapse concurrent identical text generations into one call (against duplicate tasks and retries)
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

// buildImageGenerator assembles the image generator.
//
// The stacking order matters. Reference image resolution (the http(s) fetch) sits inside
// the deduplication. Put it outside, and concurrent identical calls would each still do
// their own fetch, downloading the same image once per caller for a single generation.
func buildImageGenerator(args *Args, guard *callguard.Guard, fetchTimeout time.Duration) (operations.ImageGenerator, error) {
	resolver, err := reference.New(args.Downloader, fetchTimeout, 0)
	if err != nil {
		return nil, fmt.Errorf("build reference resolver: %w", err)
	}

	images, err := imagegen.New(args.AIClient)
	if err != nil {
		return nil, fmt.Errorf("initialize image generator: %w", err)
	}

	resolving := &resolvingImageGenerator{inner: images, resolver: resolver}

	// Collapse concurrent identical image generations into one call (against duplicate tasks and retries)
	return &singleflightImageGenerator{inner: resolving, guard: guard}, nil
}

// validateArgs validates the arguments.
//
// The messages use Args's actual field names. Callers find what to fix from the message,
// so a name that drifts no longer turns up in a grep.
func validateArgs(args *Args) error {
	if args.Downloader == nil {
		return fmt.Errorf("Args.Downloader is required")
	}
	if args.Reader == nil {
		return fmt.Errorf("Args.Reader is required")
	}
	if args.Writer == nil {
		return fmt.Errorf("Args.Writer is required")
	}
	if args.AIClient == nil {
		return fmt.Errorf("Args.AIClient is required")
	}
	if args.Characters == nil {
		return fmt.Errorf("Args.Characters is required")
	}
	// The kit holds no prompts. They are wording tuned per work, so the side that can
	// change them without a kit release (the app) implements them (the same reason as
	// art direction and model names).
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
			return fmt.Errorf("%s is required", p.name)
		}
	}
	return nil
}
