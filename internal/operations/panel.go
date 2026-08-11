package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// PanelImageRunner はパネル画像の生成/再生成（GeneratePanel 操作）を実行します。
type PanelImageRunner struct {
	characters     *comic.Characters
	prompt         ports.PanelPrompt
	generator      ImageGenerator
	writer         remoteio.Writer
	model          string
	styleSuffix    string
	aspectRatio    string
	imageSize      string
	maxConcurrency int
	cacheControl   string
}

var _ ports.PanelImageGenerator = (*PanelImageRunner)(nil)

// PanelImageRunnerArgs は PanelImageRunner の構築に必要な依存と設定の集合です。
type PanelImageRunnerArgs struct {
	Characters *comic.Characters
	// Prompt はパネル生成プロンプトの構築器です（nil ならキット内蔵の簡潔な既定）。
	Prompt    ports.PanelPrompt
	Generator ImageGenerator
	Writer    remoteio.Writer
	// Model は画像生成に使うモデル名です（ports.Config.ImageModel）。
	Model string
	// StyleSuffix にはパネル用の画風指定（ports.Config.StyleSuffix）を渡してください。
	StyleSuffix string
	// AspectRatio が空の場合は layout.PanelAspectRatio を使います。
	AspectRatio string
	// ImageSize が空の場合は layout.ImageSize1K を使います。
	ImageSize string
	// MaxConcurrency は GenerateAllPanels の並列数です（ports.Config.MaxConcurrency）。
	// 0 以下の場合は 1（逐次実行）になります。
	MaxConcurrency int
	// CacheControl は保存時の Cache-Control です（ports.Config.CacheControl）。
	CacheControl string
}

// NewPanelImageRunner は依存関係を注入して初期化します。
func NewPanelImageRunner(args PanelImageRunnerArgs) *PanelImageRunner {
	if args.AspectRatio == "" {
		args.AspectRatio = layout.DefaultAspectRatio
	}
	if args.ImageSize == "" {
		args.ImageSize = layout.ImageSize1K
	}
	if args.MaxConcurrency <= 0 {
		args.MaxConcurrency = 1
	}
	return &PanelImageRunner{
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

// GeneratePanel は指定パネルの画像を生成し、結果を GenerationRecord として state に記録します。
// opts.Seed が nil の場合は前回の UsedSeed（あれば）を再利用し「同条件での再生成」になります。
// opts.EditPrompt を指定すると、既存の生成済み画像を入力とした編集モードになります。
func (pr *PanelImageRunner) GeneratePanel(ctx context.Context, state *comic.MangaState, panelID string, opts ports.GenerateOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state が nil です", ports.ErrInvalidRequest)
	}
	panel := state.PanelByID(panelID)
	if panel == nil {
		return nil, fmt.Errorf("%w: パネル %q", ports.ErrNotFound, panelID)
	}

	record, err := pr.renderPanel(ctx, panel, opts)
	if err != nil {
		return nil, err
	}
	panel.Generation = record
	state.UpdatedAt = record.GeneratedAt
	return state, nil
}

// renderPanel は1コマ分の画像を生成・保存し、その生成記録を返します。
// panel は読むだけで書き換えないため、対象が異なれば並列に呼び出せます
// （記録の反映は呼び出し側が単独で行います。GenerateAllPanels 参照）。
func (pr *PanelImageRunner) renderPanel(ctx context.Context, panel *comic.Panel, opts ports.GenerateOptions) (*comic.GenerationRecord, error) {
	panelID := panel.ID

	targetModel := pr.model
	if opts.ModelOverride != "" {
		targetModel = opts.ModelOverride
	}
	seed := resolveSeedChain(opts.Seed, panel.Generation, pr.characters, panel.Characters)

	set, images, err := pr.buildRequest(panel, opts)
	if err != nil {
		return nil, err
	}

	slog.Info("Starting panel image generation",
		"panel", panelID,
		"model", targetModel,
		"edit", opts.EditPrompt != "",
		"ref_count", len(images),
	)

	// 生成・保存・生成条件の記録（再生成の基礎）。保存先はパネルIDに紐づく安定したパスで上書きする。
	record, err := renderImage(ctx, pr.generator, pr.writer, imageRenderRequest{
		Model:          targetModel,
		Prompt:         set.user,
		SystemPrompt:   set.system,
		NegativePrompt: set.negative,
		AspectRatio:    pr.aspectRatio,
		ImageSize:      pr.imageSize,
		Seed:           seed,
		Images:         images,
		CacheControl:   pr.cacheControl,
		PathFor: func(mimeType string) (string, error) {
			return asset.PanelImagePath(opts.OutputDir, panelID, getPreferredExtension(mimeType))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("パネル %q: %w", panelID, err)
	}

	slog.Info("Panel image generation completed", "panel", panelID, "path", record.ImageURL)
	return record, nil
}

// GenerateAllPanels は state 内の全パネルを maxConcurrency 並列で生成します。
// 一部が失敗しても成功分は state に記録し、失敗をまとめたエラーと一緒に返します
// （ports.PanelBatchGenerator 参照）。
func (pr *PanelImageRunner) GenerateAllPanels(ctx context.Context, state *comic.MangaState, opts ports.BatchOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state が nil です", ports.ErrInvalidRequest)
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

	slog.Info("Starting batch panel generation",
		"panels", len(targets), "chapter", opts.ChapterID, "concurrency", pr.maxConcurrency)

	single := ports.GenerateOptions{
		Seed:          opts.Seed,
		ModelOverride: opts.ModelOverride,
		OutputDir:     opts.OutputDir,
	}
	records, errs := runBatch(ctx, pr.maxConcurrency, targets,
		func(ctx context.Context, index int) (*comic.GenerationRecord, error) {
			panel := &state.Panels[index]
			record, err := pr.renderPanel(ctx, panel, single)
			if err != nil {
				return nil, fmt.Errorf("パネル %q: %w", panel.ID, err)
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

	slog.Info("Batch panel generation completed",
		"succeeded", applied, "failed", len(targets)-applied)
	return state, errors.Join(errs...)
}

// buildRequest は、編集モードか通常生成かに応じてプロンプト一式と参照画像を構築します。
// プロンプト本文の組み立ては ports.PanelPrompt の実装（既定はキット内蔵の簡潔版）に委ね、
// ここは「どのキャラクターの参照画像をどの順序で添付したか」を伝える役に徹します。
func (pr *PanelImageRunner) buildRequest(panel *comic.Panel, opts ports.GenerateOptions) (promptSet, []imagePorts.ImageURI, error) {
	if opts.EditPrompt != "" {
		if panel.Generation == nil || panel.Generation.ImageURL == "" {
			return promptSet{}, nil, fmt.Errorf("%w: パネル %q には編集対象の生成済み画像がありません", ports.ErrInvalidRequest, panel.ID)
		}
		set, err := newPromptSet(pr.prompt.BuildPanelEdit(opts.EditPrompt))
		if err != nil {
			return promptSet{}, nil, err
		}
		return set.withOverride(opts.PromptOverride), []imagePorts.ImageURI{{ReferenceURL: panel.Generation.ImageURL}}, nil
	}

	var images []imagePorts.ImageURI
	var subjectIDs []string
	for _, id := range panel.ReferencedCharacterIDs() {
		char := pr.characters.GetCharacter(id)
		if char == nil {
			// ChapterScriptRunner が background に降格させるため通常は到達しないが、
			// 手書きの state 等で未知IDが紛れた場合は参照なしで続行する。
			slog.Warn("未定義のキャラクターIDを参照対象から除外します", "character_id", id)
			continue
		}
		// 生成アスペクト比に一致する参照画像（あれば）を優先し、細部のブレを抑える
		referenceURL := char.ReferenceURLFor(pr.aspectRatio)
		if referenceURL == "" {
			slog.Warn("キャラクターに参照画像がありません", "character_id", id)
			continue
		}
		images = append(images, imagePorts.ImageURI{ReferenceURL: referenceURL})
		subjectIDs = append(subjectIDs, id)
	}

	set, err := newPromptSet(pr.prompt.BuildPanel(&ports.PanelPromptData{
		Panel:       *panel,
		Characters:  pr.characters,
		SubjectIDs:  subjectIDs,
		StyleSuffix: pr.styleSuffix,
	}))
	if err != nil {
		return promptSet{}, nil, err
	}
	return set.withOverride(opts.PromptOverride), images, nil
}
