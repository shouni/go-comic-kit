package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// PageImageRunner はページ画像の合成（ComposePage 操作）を実行します。
type PageImageRunner struct {
	characters     *comic.Characters
	prompt         ports.PagePrompt
	generator      ImageGenerator
	writer         remoteio.Writer
	model          string
	styleSuffix    string
	aspectRatio    string
	imageSize      string
	maxConcurrency int
	cacheControl   string
}

var _ ports.PageImageComposer = (*PageImageRunner)(nil)

// PageImageRunnerArgs は PageImageRunner の構築に必要な依存と設定の集合です。
type PageImageRunnerArgs struct {
	Characters *comic.Characters
	// Prompt はページ合成プロンプトの構築器です（nil ならキット内蔵の簡潔な既定）。
	Prompt    ports.PagePrompt
	Generator ImageGenerator
	Writer    remoteio.Writer
	Model     string
	// StyleSuffix にはページ用の画風指定（ports.Config.StyleSuffix）を渡してください。
	StyleSuffix string
	// AspectRatio が空の場合は layout.PageAspectRatio を使います。
	AspectRatio string
	// ImageSize が空の場合は layout.ImageSize2K を使います。
	ImageSize string
	// MaxConcurrency は ComposeAllPages の並列数です（ports.Config.MaxConcurrency）。
	// 0 以下の場合は 1（逐次実行）になります。
	MaxConcurrency int
	// CacheControl は保存時の Cache-Control です（ports.Config.CacheControl）。
	CacheControl string
}

// NewPageImageRunner は依存関係を注入して初期化します。
func NewPageImageRunner(args PageImageRunnerArgs) *PageImageRunner {
	if args.AspectRatio == "" {
		args.AspectRatio = layout.DefaultAspectRatio
	}
	if args.ImageSize == "" {
		args.ImageSize = layout.ImageSize2K
	}
	if args.MaxConcurrency <= 0 {
		args.MaxConcurrency = 1
	}
	return &PageImageRunner{
		characters:     args.Characters,
		prompt:         args.Prompt,
		generator:      args.Generator,
		writer:         args.Writer,
		model:          args.Model,
		styleSuffix:    args.StyleSuffix,
		aspectRatio:    args.AspectRatio,
		imageSize:      args.ImageSize,
		maxConcurrency: args.MaxConcurrency,
		cacheControl:   args.CacheControl,
	}
}

// pageResources はページ合成に渡す参照画像と、プロンプトから参照するためのインデックスを保持します。
type pageResources struct {
	images        []imagePorts.ImageURI
	characterFile map[string]int // characterID -> input_file 番号（1始まり）
	panelFile     map[string]int // panelID -> input_file 番号（1始まり）
}

// ComposePage は指定ページのパネル群を1枚のページ画像として合成し、
// 結果を PageArtifact として state に記録します（冪等・upsert）。
// opts.Seed が nil の場合は前回の UsedSeed（あれば）を再利用します。
// opts.EditPrompt を指定すると、既存のページ画像を入力とした編集モードになります。
func (pg *PageImageRunner) ComposePage(ctx context.Context, state *comic.MangaState, page int, opts ports.GenerateOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state が nil です", ports.ErrInvalidRequest)
	}

	artifact, err := pg.renderPage(ctx, state, page, opts)
	if err != nil {
		return nil, err
	}
	state.SetPageArtifact(*artifact)
	state.UpdatedAt = artifact.Generation.GeneratedAt
	return state, nil
}

// renderPage は1ページ分の画像を合成・保存し、その記録を返します。
// state は読むだけで書き換えないため、対象ページが異なれば並列に呼び出せます
// （記録の反映は呼び出し側が単独で行います。ComposeAllPages 参照）。
func (pg *PageImageRunner) renderPage(ctx context.Context, state *comic.MangaState, page int, opts ports.GenerateOptions) (*comic.PageArtifact, error) {
	panels := state.PanelsForPage(page)
	if len(panels) == 0 {
		return nil, fmt.Errorf("%w: ページ %d にパネルがありません", ports.ErrNotFound, page)
	}

	targetModel := pg.model
	if opts.ModelOverride != "" {
		targetModel = opts.ModelOverride
	}
	existing := state.PageArtifactByNumber(page)
	var prevGeneration *comic.GenerationRecord
	if existing != nil {
		prevGeneration = existing.Generation
	}
	seed := resolveSeedChain(opts.Seed, prevGeneration, pg.characters, panels[0].Characters)

	set, images, err := pg.buildRequest(page, panels, existing, opts)
	if err != nil {
		return nil, err
	}

	slog.Info("Starting page composition",
		"page", page,
		"panels", len(panels),
		"model", targetModel,
		"edit", opts.EditPrompt != "",
		"ref_count", len(images),
	)

	// 生成・保存・生成条件の記録。保存先はページ番号に紐づく安定したパスで上書きする。
	record, err := renderImage(ctx, pg.generator, pg.writer, imageRenderRequest{
		Model:          targetModel,
		Prompt:         set.user,
		SystemPrompt:   set.system,
		NegativePrompt: set.negative,
		AspectRatio:    pg.aspectRatio,
		ImageSize:      pg.imageSize,
		Seed:           seed,
		Images:         images,
		CacheControl:   pg.cacheControl,
		PathFor: func(string) (string, error) {
			return asset.PageImagePath(opts.OutputDir, page)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ページ %d: %w", page, err)
	}

	slog.Info("Page composition completed", "page", page, "path", record.ImageURL)

	// 記録（呼び出し側が同一ページ番号に upsert する）
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

// ComposeAllPages は state 内の全ページを maxConcurrency 並列で合成します。
// 一部が失敗しても成功分は state に記録し、失敗をまとめたエラーと一緒に返します
// （ports.PageBatchComposer 参照）。
func (pg *PageImageRunner) ComposeAllPages(ctx context.Context, state *comic.MangaState, opts ports.BatchOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state が nil です", ports.ErrInvalidRequest)
	}

	pages := uniquePageNumbers(state.Panels)
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

	slog.Info("Starting batch page composition",
		"pages", len(targets), "concurrency", pg.maxConcurrency)

	single := ports.GenerateOptions{
		Seed:          opts.Seed,
		ModelOverride: opts.ModelOverride,
		OutputDir:     opts.OutputDir,
	}
	artifacts, errs := runBatch(ctx, pg.maxConcurrency, targets,
		func(ctx context.Context, page int) (*comic.PageArtifact, error) {
			artifact, err := pg.renderPage(ctx, state, page, single)
			if err != nil {
				return nil, fmt.Errorf("ページ %d: %w", page, err)
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

	slog.Info("Batch page composition completed",
		"succeeded", applied, "failed", len(targets)-applied)
	return state, errors.Join(errs...)
}

// uniquePageNumbers は、パネル群に登場するページ番号を重複なく昇順で返します。
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

// buildRequest は、編集モードかページ合成かに応じてプロンプト一式と参照画像を構築します。
// プロンプト本文の組み立ては ports.PagePrompt の実装（既定はキット内蔵の簡潔版）に委ね、
// ここは「何番目にどの画像を添付したか」を伝える役に徹します。
func (pg *PageImageRunner) buildRequest(page int, panels []comic.Panel, existing *comic.PageArtifact, opts ports.GenerateOptions) (promptSet, []imagePorts.ImageURI, error) {
	if opts.EditPrompt != "" {
		if existing == nil || existing.Generation == nil || existing.Generation.ImageURL == "" {
			return promptSet{}, nil, fmt.Errorf("%w: ページ %d には編集対象の合成済み画像がありません", ports.ErrInvalidRequest, page)
		}
		set, err := newPromptSet(pg.prompt.BuildPageEdit(opts.EditPrompt))
		if err != nil {
			return promptSet{}, nil, err
		}
		return set.withOverride(opts.PromptOverride), []imagePorts.ImageURI{{ReferenceURL: existing.Generation.ImageURL}}, nil
	}

	res := pg.collectPageResources(panels)
	set, err := newPromptSet(pg.prompt.BuildPage(&ports.PagePromptData{
		Panels:        panels,
		Characters:    pg.characters,
		CharacterFile: res.characterFile,
		PanelFile:     res.panelFile,
		StyleSuffix:   pg.styleSuffix,
	}))
	if err != nil {
		return promptSet{}, nil, err
	}
	return set.withOverride(opts.PromptOverride), res.images, nil
}

// collectPageResources はページ内の参照画像を「キャラクター → 生成済みパネル」の順で集約し、
// プロンプトから参照する input_file 番号（1始まり）を割り振ります。
func (pg *PageImageRunner) collectPageResources(panels []comic.Panel) *pageResources {
	res := &pageResources{
		characterFile: make(map[string]int),
		panelFile:     make(map[string]int),
	}

	// 1. 登場キャラクターのマスター参照（重複なし・登場順）
	for _, panel := range panels {
		for _, id := range panel.ReferencedCharacterIDs() {
			if _, ok := res.characterFile[id]; ok {
				continue
			}
			char := pg.characters.GetCharacter(id)
			if char == nil {
				continue
			}
			referenceURL := char.ReferenceURLFor(pg.aspectRatio)
			if referenceURL == "" {
				continue
			}
			res.images = append(res.images, imagePorts.ImageURI{ReferenceURL: referenceURL})
			res.characterFile[id] = len(res.images)
		}
	}

	// 2. 生成済みパネル画像（構図ガイド）
	for _, panel := range panels {
		if panel.Generation == nil || panel.Generation.ImageURL == "" {
			continue
		}
		url := panel.Generation.ImageURL
		res.images = append(res.images, imagePorts.ImageURI{ReferenceURL: url})
		res.panelFile[panel.ID] = len(res.images)
	}

	return res
}
