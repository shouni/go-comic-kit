package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// PanelImageRunner runs panel image generation and regeneration (the GeneratePanel operation).
type PanelImageRunner struct {
	characters     *comic.Characters
	prompt         ports.PanelPrompt
	generator      ImageGenerator
	writer         remoteio.Writer
	maxConcurrency int
	cacheControl   string
}

var _ ports.PanelImageGenerator = (*PanelImageRunner)(nil)

// PanelImageRunnerArgs is the set of dependencies and settings needed to build a PanelImageRunner.
type PanelImageRunnerArgs struct {
	Characters *comic.Characters
	// Prompt builds the panel generation prompt (required; the kit has no default).
	Prompt    ports.PanelPrompt
	Generator ImageGenerator
	Writer    remoteio.Writer
	// MaxConcurrency is the parallelism of GenerateAllPanels (ports.Config.MaxConcurrency).
	// 0 or less becomes 1 (sequential execution).
	MaxConcurrency int
	// CacheControl is the Cache-Control used when saving (ports.Config.CacheControl).
	CacheControl string
}

// NewPanelImageRunner initializes the runner with its dependencies injected.
func NewPanelImageRunner(args PanelImageRunnerArgs) *PanelImageRunner {
	if args.MaxConcurrency <= 0 {
		args.MaxConcurrency = 1
	}
	return &PanelImageRunner{
		characters:     args.Characters,
		prompt:         args.Prompt,
		generator:      args.Generator,
		writer:         args.Writer,
		maxConcurrency: args.MaxConcurrency,
		cacheControl:   args.CacheControl,
	}
}

// GeneratePanel generates the image of the given panel and records the result on the
// state as a GenerationRecord. Seed / EditPrompt behave as described on ports.GenerateOptions.
func (pr *PanelImageRunner) GeneratePanel(ctx context.Context, state *comic.MangaState, panelID string, opts ports.GenerateOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state is nil", ports.ErrInvalidRequest)
	}
	panel := state.PanelByID(panelID)
	if panel == nil {
		return nil, fmt.Errorf("%w: panel %q", ports.ErrNotFound, panelID)
	}

	record, err := pr.renderPanel(ctx, panel, opts)
	if err != nil {
		return nil, err
	}
	panel.Generation = record
	state.UpdatedAt = record.GeneratedAt
	return state, nil
}

// renderPanel generates and saves one panel's image and returns its generation record.
// It only reads panel and never writes to it, so it can be called in parallel for
// different targets (the caller alone applies the record; see GenerateAllPanels).
func (pr *PanelImageRunner) renderPanel(ctx context.Context, panel *comic.Panel, opts ports.GenerateOptions) (*comic.GenerationRecord, error) {
	panelID := panel.ID

	if err := requireModel(opts.Model, "panel image"); err != nil {
		return nil, err
	}
	aspectRatio, err := resolveAspectRatio(opts.AspectRatio, layout.DefaultAspectRatio)
	if err != nil {
		return nil, err
	}
	imageSize, err := resolveImageSize(opts.ImageSize, layout.ImageSize1K)
	if err != nil {
		return nil, err
	}

	seed := resolveSeedChain(opts.Seed, panel.Generation, pr.characters, panel.Characters)

	set, images, err := pr.buildRequest(ctx, panel, opts, aspectRatio)
	if err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "Starting panel image generation",
		"panel", panelID,
		"model", opts.Model,
		"edit", opts.EditPrompt != "",
		"ref_count", len(images),
	)

	record, err := renderImage(ctx, pr.generator, pr.writer, imageRenderRequest{
		Model:          opts.Model,
		Prompt:         set.user,
		SystemPrompt:   set.system,
		NegativePrompt: set.negative,
		AspectRatio:    aspectRatio,
		ImageSize:      imageSize,
		Seed:           seed,
		Images:         images,
		CacheControl:   pr.cacheControl,
		PathFor: func(mimeType string) (string, error) {
			return asset.PanelImagePath(opts.OutputDir, panelID, imagegen.ExtensionByMIMEType(mimeType))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("panel %q: %w", panelID, err)
	}

	slog.InfoContext(ctx, "Panel image generation completed", "panel", panelID, "path", record.ImageURL)
	return record, nil
}

// GenerateAllPanels generates every panel in the state with maxConcurrency in parallel.
// Even when some fail, the successes are recorded on the state and returned together
// with an error joining the failures.
func (pr *PanelImageRunner) GenerateAllPanels(ctx context.Context, state *comic.MangaState, opts ports.BatchOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state is nil", ports.ErrInvalidRequest)
	}

	if err := validateBatchChapter(state, opts.ChapterID); err != nil {
		return nil, err
	}

	targets := make([]int, 0, len(state.Panels))
	for i := range state.Panels {
		if opts.ChapterID != "" && state.Panels[i].ChapterID != opts.ChapterID {
			continue
		}
		if opts.SkipGenerated && state.Panels[i].Generation != nil {
			continue
		}
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return state, nil
	}

	slog.InfoContext(ctx, "Starting batch panel generation",
		"panels", len(targets), "chapter", opts.ChapterID, "concurrency", pr.maxConcurrency)

	single := ports.GenerateOptions{
		Seed:        opts.Seed,
		Model:       opts.Model,
		StyleMode:   opts.StyleMode,
		AspectRatio: opts.AspectRatio,
		ImageSize:   opts.ImageSize,
		OutputDir:   opts.OutputDir,
	}
	records, errs := runBatch(ctx, pr.maxConcurrency, targets,
		func(ctx context.Context, index int) (*comic.GenerationRecord, error) {
			panel := &state.Panels[index]
			record, err := pr.renderPanel(ctx, panel, single)
			if err != nil {
				return nil, fmt.Errorf("panel %q: %w", panel.ID, err)
			}
			return record, nil
		})

	applied := 0
	for i, index := range targets {
		if records[i] == nil {
			continue
		}
		state.Panels[index].Generation = records[i]
		applied++
	}
	if applied > 0 {
		state.UpdatedAt = time.Now().UTC()
	}

	slog.InfoContext(ctx, "Batch panel generation completed",
		"succeeded", applied, "failed", len(targets)-applied)
	return state, errors.Join(errs...)
}

// buildRequest builds the prompt set and reference images for either edit mode or
// normal generation. Assembling the prompt text is left to the ports.PanelPrompt
// implementation; this only conveys "whose reference images were attached, in what order".
func (pr *PanelImageRunner) buildRequest(ctx context.Context, panel *comic.Panel, opts ports.GenerateOptions, aspectRatio string) (promptSet, []string, error) {
	if opts.EditPrompt != "" {
		if panel.Generation == nil || panel.Generation.ImageURL == "" {
			return promptSet{}, nil, fmt.Errorf("%w: panel %q has no generated image to edit", ports.ErrInvalidRequest, panel.ID)
		}
		set, err := newPromptSet(pr.prompt.BuildPanelEdit(opts.EditPrompt))
		if err != nil {
			return promptSet{}, nil, err
		}
		return set.withOverride(opts.PromptOverride), []string{panel.Generation.ImageURL}, nil
	}

	var images []string
	var subjectIDs []string
	for _, id := range panel.ReferencedCharacterIDs() {
		char := pr.characters.GetCharacter(id)
		if char == nil {
			// Normally unreachable, since ChapterScriptRunner demotes these to background,
			// but if an unknown ID slips in (a hand-written state, etc.), continue without a reference.
			slog.WarnContext(ctx, "excluding unknown character ID from references", "character_id", id)
			continue
		}
		// Prefer a reference image matching the generation aspect ratio (if any) to suppress detail drift
		referenceURL := char.ReferenceURLFor(aspectRatio)
		if referenceURL == "" {
			slog.WarnContext(ctx, "character has no reference image", "character_id", id)
			continue
		}
		images = append(images, referenceURL)
		subjectIDs = append(subjectIDs, id)
	}

	set, err := newPromptSet(pr.prompt.BuildPanel(&ports.PanelPromptData{
		Panel:      *panel,
		Characters: pr.characters,
		SubjectIDs: subjectIDs,
		StyleMode:  opts.StyleMode,
	}))
	if err != nil {
		return promptSet{}, nil, err
	}
	return set.withOverride(opts.PromptOverride), images, nil
}
