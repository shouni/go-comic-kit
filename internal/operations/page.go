package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// PageImageRunner runs page image composition (the ComposePage operation).
type PageImageRunner struct {
	characters     *comic.Characters
	prompt         ports.PagePrompt
	generator      ImageGenerator
	writer         remoteio.Writer
	maxConcurrency int
	cacheControl   string
}

var _ ports.PageImageComposer = (*PageImageRunner)(nil)

// PageImageRunnerArgs is the set of dependencies and settings needed to build a PageImageRunner.
type PageImageRunnerArgs struct {
	Characters *comic.Characters
	// Prompt builds the page composition prompt (required; the kit has no default).
	Prompt    ports.PagePrompt
	Generator ImageGenerator
	Writer    remoteio.Writer
	// MaxConcurrency is the parallelism of ComposeAllPages (ports.Config.MaxConcurrency).
	// 0 or less becomes 1 (sequential execution).
	MaxConcurrency int
	// CacheControl is the Cache-Control used when saving (ports.Config.CacheControl).
	CacheControl string
}

// NewPageImageRunner initializes the runner with its dependencies injected.
func NewPageImageRunner(args PageImageRunnerArgs) *PageImageRunner {
	if args.MaxConcurrency <= 0 {
		args.MaxConcurrency = 1
	}
	return &PageImageRunner{
		characters:     args.Characters,
		prompt:         args.Prompt,
		generator:      args.Generator,
		writer:         args.Writer,
		maxConcurrency: args.MaxConcurrency,
		cacheControl:   args.CacheControl,
	}
}

// pageResources holds the reference images passed to page composition and the indices the prompt uses to refer to them.
type pageResources struct {
	images        []string
	characterFile map[string]int // characterID -> input_file number (1-based)
	panelFile     map[string]int // panelID -> input_file number (1-based)
}

// ComposePage composes the panels of the given page into one page image and records the
// result on the state as a PageArtifact (idempotent, upsert).
// Seed / EditPrompt behave as described on ports.GenerateOptions.
func (pg *PageImageRunner) ComposePage(ctx context.Context, state *comic.MangaState, page int, opts ports.GenerateOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state is nil", ports.ErrInvalidRequest)
	}

	artifact, err := pg.renderPage(ctx, state, page, opts)
	if err != nil {
		return nil, err
	}
	state.SetPageArtifact(*artifact)
	state.UpdatedAt = artifact.Generation.GeneratedAt
	return state, nil
}

// renderPage composes and saves one page's image and returns its record.
// It only reads state and never writes to it, so it can be called in parallel for
// different pages (the caller alone applies the record; see ComposeAllPages).
func (pg *PageImageRunner) renderPage(ctx context.Context, state *comic.MangaState, page int, opts ports.GenerateOptions) (*comic.PageArtifact, error) {
	panels := state.PanelsForPage(page)
	if len(panels) == 0 {
		return nil, fmt.Errorf("%w: page %d has no panels", ports.ErrNotFound, page)
	}
	if err := requireModel(opts.Model, "page image"); err != nil {
		return nil, err
	}
	aspectRatio, err := resolveAspectRatio(opts.AspectRatio, layout.DefaultAspectRatio)
	if err != nil {
		return nil, err
	}
	imageSize, err := resolveImageSize(opts.ImageSize, layout.ImageSize2K)
	if err != nil {
		return nil, err
	}

	existing := state.PageArtifactByNumber(page)
	var prevGeneration *comic.GenerationRecord
	if existing != nil {
		prevGeneration = existing.Generation
	}
	seed := resolveSeedChain(opts.Seed, prevGeneration, pg.characters, panels[0].Characters)

	set, images, err := pg.buildRequest(ctx, page, panels, existing, opts, aspectRatio)
	if err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "Starting page composition",
		"page", page,
		"panels", len(panels),
		"model", opts.Model,
		"edit", opts.EditPrompt != "",
		"ref_count", len(images),
	)

	record, err := renderImage(ctx, pg.generator, pg.writer, imageRenderRequest{
		Model:          opts.Model,
		Prompt:         set.user,
		SystemPrompt:   set.system,
		NegativePrompt: set.negative,
		AspectRatio:    aspectRatio,
		ImageSize:      imageSize,
		Seed:           seed,
		Images:         images,
		CacheControl:   pg.cacheControl,
		PathFor: func(mimeType string) (string, error) {
			return asset.PageImagePath(opts.OutputDir, page, imagegen.ExtensionByMIMEType(mimeType))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("page %d: %w", page, err)
	}

	slog.InfoContext(ctx, "Page composition completed", "page", page, "path", record.ImageURL)

	panelIDs := make([]string, len(panels))
	for i := range panels {
		panelIDs[i] = panels[i].ID
	}
	return &comic.PageArtifact{
		PageNumber: page,
		PanelIDs:   panelIDs,
		Generation: record,
	}, nil
}

// ComposeAllPages composes every page in the state with maxConcurrency in parallel.
// Even when some fail, the successes are recorded on the state and returned together
// with an error joining the failures.
func (pg *PageImageRunner) ComposeAllPages(ctx context.Context, state *comic.MangaState, opts ports.BatchOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state is nil", ports.ErrInvalidRequest)
	}

	if err := validateBatchChapter(state, opts.ChapterID); err != nil {
		return nil, err
	}

	pages := uniquePageNumbers(state.Panels)
	if opts.ChapterID != "" {
		// Relies on pages being broken at chapter boundaries (MangaState.PagesForChapter).
		pages = state.PagesForChapter(opts.ChapterID)
	}
	targets := make([]int, 0, len(pages))
	for _, page := range pages {
		if opts.SkipGenerated {
			if existing := state.PageArtifactByNumber(page); existing != nil && existing.Generation != nil {
				continue
			}
		}
		targets = append(targets, page)
	}
	if len(targets) == 0 {
		return state, nil
	}

	slog.InfoContext(ctx, "Starting batch page composition",
		"pages", len(targets), "chapter", opts.ChapterID, "concurrency", pg.maxConcurrency)

	single := ports.GenerateOptions{
		Seed:        opts.Seed,
		Model:       opts.Model,
		StyleMode:   opts.StyleMode,
		AspectRatio: opts.AspectRatio,
		ImageSize:   opts.ImageSize,
		OutputDir:   opts.OutputDir,
	}
	artifacts, errs := runBatch(ctx, pg.maxConcurrency, targets,
		func(ctx context.Context, page int) (*comic.PageArtifact, error) {
			artifact, err := pg.renderPage(ctx, state, page, single)
			if err != nil {
				return nil, fmt.Errorf("page %d: %w", page, err)
			}
			return artifact, nil
		})

	applied := 0
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		state.SetPageArtifact(*artifact)
		applied++
	}
	if applied > 0 {
		state.UpdatedAt = time.Now().UTC()
	}

	slog.InfoContext(ctx, "Batch page composition completed",
		"succeeded", applied, "failed", len(targets)-applied)
	return state, errors.Join(errs...)
}

// uniquePageNumbers returns the page numbers appearing in the panels, without duplicates, in ascending order.
func uniquePageNumbers(panels []comic.Panel) []int {
	seen := make(map[int]struct{}, len(panels))
	pages := make([]int, 0, len(panels))
	for i := range panels {
		if _, ok := seen[panels[i].Page]; ok {
			continue
		}
		seen[panels[i].Page] = struct{}{}
		pages = append(pages, panels[i].Page)
	}
	slices.Sort(pages)
	return pages
}

// buildRequest builds the prompt set and reference images for either edit mode or page
// composition. Assembling the prompt text is left to the ports.PagePrompt
// implementation; this only conveys "which image was attached at which position".
func (pg *PageImageRunner) buildRequest(ctx context.Context, page int, panels []comic.Panel, existing *comic.PageArtifact, opts ports.GenerateOptions, aspectRatio string) (promptSet, []string, error) {
	if opts.EditPrompt != "" {
		if existing == nil || existing.Generation == nil || existing.Generation.ImageURL == "" {
			return promptSet{}, nil, fmt.Errorf("%w: page %d has no composed image to edit", ports.ErrInvalidRequest, page)
		}
		set, err := newPromptSet(pg.prompt.BuildPageEdit(opts.EditPrompt))
		if err != nil {
			return promptSet{}, nil, err
		}
		return set.withOverride(opts.PromptOverride), []string{existing.Generation.ImageURL}, nil
	}

	res := pg.collectPageResources(ctx, panels, aspectRatio)
	set, err := newPromptSet(pg.prompt.BuildPage(&ports.PagePromptData{
		Panels:        panels,
		Characters:    pg.characters,
		CharacterFile: res.characterFile,
		PanelFile:     res.panelFile,
		StyleMode:     opts.StyleMode,
	}))
	if err != nil {
		return promptSet{}, nil, err
	}
	return set.withOverride(opts.PromptOverride), res.images, nil
}

// pageCharacterRef is the tally for one character on the page who has a reference image.
type pageCharacterRef struct {
	id  string
	url string
	// panels is the number of panels this character appears in (the order kept when over the limit).
	panels int
}

// collectPageResources gathers the page's reference images in the order "characters →
// generated panels" and assigns the input_file numbers (1-based) the prompt refers to.
func (pg *PageImageRunner) collectPageResources(ctx context.Context, panels []comic.Panel, aspectRatio string) *pageResources {
	res := &pageResources{
		characterFile: make(map[string]int),
		panelFile:     make(map[string]int),
	}

	// 1. Master references of the characters who appear (no duplicates, first-appearance order, capped)
	for _, ref := range capPageCharacterRefs(ctx, pg.pageCharacterRefs(panels, aspectRatio)) {
		res.images = append(res.images, ref.url)
		res.characterFile[ref.id] = len(res.images)
	}

	// 2. Generated panel images (layout guides)
	for _, panel := range panels {
		if panel.Generation == nil || panel.Generation.ImageURL == "" {
			continue
		}
		url := panel.Generation.ImageURL
		res.images = append(res.images, url)
		res.panelFile[panel.ID] = len(res.images)
	}

	return res
}

// pageCharacterRefs tallies, in first-appearance order, the characters on the page whose
// reference images could be resolved. Characters without a reference image and unknown
// IDs drop out here, so the cap applies to the number of images actually attached.
func (pg *PageImageRunner) pageCharacterRefs(panels []comic.Panel, aspectRatio string) []pageCharacterRef {
	index := make(map[string]int)
	refs := make([]pageCharacterRef, 0, len(panels))

	for _, panel := range panels {
		for _, id := range panel.ReferencedCharacterIDs() {
			if i, ok := index[id]; ok {
				refs[i].panels++
				continue
			}
			char := pg.characters.GetCharacter(id)
			if char == nil {
				continue
			}
			referenceURL := char.ReferenceURLFor(aspectRatio)
			if referenceURL == "" {
				continue
			}
			index[id] = len(refs)
			refs = append(refs, pageCharacterRef{id: id, url: referenceURL, panels: 1})
		}
	}
	return refs
}

// capPageCharacterRefs narrows the attached character references down to
// comic.MaxReferencedCharactersPerPage.
//
// The ones kept are those appearing in the most panels, ties broken by first appearance
// (deterministic). A plain head-truncation can hand the anchor to a character who shows
// up in a single panel and leave the lead, who is in nearly every panel of the page,
// without a reference. The returned order stays first-appearance; the attachment order
// does not change.
func capPageCharacterRefs(ctx context.Context, refs []pageCharacterRef) []pageCharacterRef {
	if len(refs) <= comic.MaxReferencedCharactersPerPage {
		return refs
	}

	byImportance := slices.Clone(refs)
	slices.SortStableFunc(byImportance, func(a, b pageCharacterRef) int {
		return b.panels - a.panels
	})

	dropped := make(map[string]struct{}, len(byImportance)-comic.MaxReferencedCharactersPerPage)
	for _, ref := range byImportance[comic.MaxReferencedCharactersPerPage:] {
		dropped[ref.id] = struct{}{}
		slog.WarnContext(ctx, "not attaching reference image for page character over the limit",
			"character_id", ref.id,
			"appears_in_panels", ref.panels,
			"max", comic.MaxReferencedCharactersPerPage)
	}

	return slices.DeleteFunc(refs, func(ref pageCharacterRef) bool {
		_, ok := dropped[ref.id]
		return ok
	})
}
