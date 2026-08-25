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
	maxConcurrency int
	cacheControl   string
}

var _ ports.PageImageComposer = (*PageImageRunner)(nil)

// PageImageRunnerArgs は PageImageRunner の構築に必要な依存と設定の集合です。
type PageImageRunnerArgs struct {
	Characters *comic.Characters
	// Prompt はページ合成プロンプトの構築器です（必須。キットは既定を持ちません）。
	Prompt    ports.PagePrompt
	Generator ImageGenerator
	Writer    remoteio.Writer
	// MaxConcurrency は ComposeAllPages の並列数です（ports.Config.MaxConcurrency）。
	// 0 以下の場合は 1（逐次実行）になります。
	MaxConcurrency int
	// CacheControl は保存時の Cache-Control です（ports.Config.CacheControl）。
	CacheControl string
}

// NewPageImageRunner は依存関係を注入して初期化します。
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
	if err := requireModel(opts.Model, "ページ画像"); err != nil {
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

	// 生成・保存・生成条件の記録。保存先はページ番号に紐づく安定したパスで上書きする。
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
			return asset.PageImagePath(opts.OutputDir, page, imagePorts.ExtensionByMIMEType(mimeType))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ページ %d: %w", page, err)
	}

	slog.InfoContext(ctx, "Page composition completed", "page", page, "path", record.ImageURL)

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
// 一部が失敗しても成功分は state に記録し、失敗をまとめたエラーと一緒に返します。
func (pg *PageImageRunner) ComposeAllPages(ctx context.Context, state *comic.MangaState, opts ports.BatchOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state が nil です", ports.ErrInvalidRequest)
	}

	if err := validateBatchChapter(state, opts.ChapterID); err != nil {
		return nil, err
	}

	pages := uniquePageNumbers(state.Panels)
	if opts.ChapterID != "" {
		// 章境界でページが割られている前提（MangaState.PagesForChapter）に乗ります。
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

	slog.InfoContext(ctx, "Batch page composition completed",
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
// プロンプト本文の組み立ては ports.PagePrompt の実装に委ね、
// ここは「何番目にどの画像を添付したか」を伝える役に徹します。
func (pg *PageImageRunner) buildRequest(ctx context.Context, page int, panels []comic.Panel, existing *comic.PageArtifact, opts ports.GenerateOptions, aspectRatio string) (promptSet, []imagePorts.ImageURI, error) {
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

// pageCharacterRef は、ページ内で参照画像を持つキャラクター1人分の集計です。
type pageCharacterRef struct {
	id  string
	url string
	// panels はこのキャラクターが登場したコマ数です（上限を超えたときに残す順の基準）。
	panels int
}

// collectPageResources はページ内の参照画像を「キャラクター → 生成済みパネル」の順で集約し、
// プロンプトから参照する input_file 番号（1始まり）を割り振ります。
func (pg *PageImageRunner) collectPageResources(ctx context.Context, panels []comic.Panel, aspectRatio string) *pageResources {
	res := &pageResources{
		characterFile: make(map[string]int),
		panelFile:     make(map[string]int),
	}

	// 1. 登場キャラクターのマスター参照（重複なし・初出順、上限あり）
	for _, ref := range capPageCharacterRefs(ctx, pg.pageCharacterRefs(panels, aspectRatio)) {
		res.images = append(res.images, imagePorts.ImageURI{ReferenceURL: ref.url})
		res.characterFile[ref.id] = len(res.images)
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

// pageCharacterRefs は、ページ内で参照画像を解決できたキャラクターを初出順で集計します。
// 参照画像を持たないキャラクターと未定義 ID はここで落ちるため、上限は「実際に添付する
// 枚数」に対して効きます。
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

// capPageCharacterRefs は、添付するキャラクター参照を
// comic.MaxReferencedCharactersPerPage まで絞り込みます。
//
// 残すのは登場コマ数の多い順、同数なら初出順です（決定的）。単純な先頭切りだと、
// 1コマだけ顔を出したキャラクターがアンカーを取り、ページのほぼ全コマに出ている
// 主役が参照なしになることがあります。戻り値の並びは初出順のままで、添付順は変えません。
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
		slog.WarnContext(ctx, "ページの参照キャラクターが上限を超えたため参照画像を添付しません",
			"character_id", ref.id,
			"appears_in_panels", ref.panels,
			"max", comic.MaxReferencedCharactersPerPage)
	}

	return slices.DeleteFunc(refs, func(ref pageCharacterRef) bool {
		_, ok := dropped[ref.id]
		return ok
	})
}
