package operations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

const (
	// panelSystemPrompt はパネル画像生成時にモデルへ与えるシステム指示です。
	// 添付される参照画像とプロンプト内の [Subject N] は同じ順序で対応します。
	panelSystemPrompt = `You are a professional manga panel illustrator.
Draw a single manga panel following the scene direction, with these rules:
- Attached reference images correspond to [Subject N] in the prompt, in the same order. Strictly preserve each subject's identity from its reference image: hairstyle, hair color, eye color, outfit, and accessories. Never mix identities between subjects.
- Translate each subject's stated emotion and action into expression, gaze, and pose. Place subjects according to their stated positions.
- Anatomical correctness is critical: draw every hand with exactly five fingers and correct limb proportions.
- Render absolutely no text, speech bubbles, sound effects lettering, or logos — dialogue is composited separately.`

	// panelNegativePrompt はパネル画像に含めたくない要素を指定する負のプロンプトです。
	// フキダシ・文字の排除（go-manga-kit / go-veo-orchestrator で実証済みの語彙）に加え、
	// 手・指の崩れ対策を含みます。
	panelNegativePrompt = "speech bubble, dialogue balloon, text, alphabet, letters, words, signatures, watermark, username, malformed hands, fused fingers, extra fingers, missing fingers, extra limbs, deformed anatomy, low quality, distorted, bad anatomy, monochrome, black and white, greyscale"

	// panelEditInstruction は編集モードで構図の維持を指示する共通プレフィックスです。
	panelEditInstruction = "Edit the attached manga panel image. Keep the composition, character poses, background, and art style unchanged. Apply ONLY this change: "
)

// PanelImageRunner はパネル画像の生成/再生成（GeneratePanel 操作）を実行します。
type PanelImageRunner struct {
	characters     *ports.Characters
	generator      ImageFusionGenerator
	writer         remoteio.Writer
	model          string
	styleSuffix    string
	aspectRatio    string
	imageSize      string
	maxConcurrency int
}

var (
	_ ports.PanelImageGenerator = (*PanelImageRunner)(nil)
	_ ports.PanelBatchGenerator = (*PanelImageRunner)(nil)
)

// PanelImageRunnerArgs は PanelImageRunner の構築に必要な依存と設定の集合です。
type PanelImageRunnerArgs struct {
	Characters *ports.Characters
	Generator  ImageFusionGenerator
	Writer     remoteio.Writer
	// Model は画像生成に使うモデル名です（標準系: ports.Config.ImageStandardModel 推奨）。
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
}

// NewPanelImageRunner は依存関係を注入して初期化します。
func NewPanelImageRunner(args PanelImageRunnerArgs) *PanelImageRunner {
	if args.AspectRatio == "" {
		args.AspectRatio = layout.PanelAspectRatio
	}
	if args.ImageSize == "" {
		args.ImageSize = layout.ImageSize1K
	}
	if args.MaxConcurrency <= 0 {
		args.MaxConcurrency = 1
	}
	return &PanelImageRunner{
		characters:     args.Characters,
		generator:      args.Generator,
		writer:         args.Writer,
		model:          args.Model,
		styleSuffix:    args.StyleSuffix,
		aspectRatio:    args.AspectRatio,
		imageSize:      args.ImageSize,
		maxConcurrency: args.MaxConcurrency,
	}
}

// GeneratePanel は指定パネルの画像を生成し、結果を GenerationRecord として state に記録します。
// opts.Seed が nil の場合は前回の UsedSeed（あれば）を再利用し「同条件での再生成」になります。
// opts.EditPrompt を指定すると、既存の生成済み画像を入力とした編集モードになります。
func (pr *PanelImageRunner) GeneratePanel(ctx context.Context, state *ports.MangaState, panelID string, opts ports.GenerateOptions) (*ports.MangaState, error) {
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
func (pr *PanelImageRunner) renderPanel(ctx context.Context, panel *ports.Panel, opts ports.GenerateOptions) (*ports.GenerationRecord, error) {
	panelID := panel.ID

	targetModel := pr.model
	if opts.ModelOverride != "" {
		targetModel = opts.ModelOverride
	}
	seed := resolveSeedChain(opts.Seed, panel.Generation, pr.characters, panel.Characters)

	var prompt string
	var images []imagePorts.ImageURI
	var err error
	if opts.EditPrompt != "" {
		prompt, images, err = pr.buildEditRequest(panel, opts)
	} else {
		prompt, images, err = pr.buildGenerateRequest(panel, opts)
	}
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
		Prompt:         prompt,
		SystemPrompt:   panelSystemPrompt,
		NegativePrompt: panelNegativePrompt,
		AspectRatio:    pr.aspectRatio,
		ImageSize:      pr.imageSize,
		Seed:           seed,
		Images:         images,
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
func (pr *PanelImageRunner) GenerateAllPanels(ctx context.Context, state *ports.MangaState, opts ports.BatchOptions) (*ports.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state が nil です", ports.ErrInvalidRequest)
	}

	targets := make([]int, 0, len(state.Panels))
	for i := range state.Panels {
		if opts.SkipGenerated && state.Panels[i].Generation != nil {
			continue
		}
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return state, nil
	}

	slog.Info("Starting batch panel generation",
		"panels", len(targets), "concurrency", pr.maxConcurrency)

	single := ports.GenerateOptions{
		Seed:          opts.Seed,
		ModelOverride: opts.ModelOverride,
		OutputDir:     opts.OutputDir,
	}
	records, errs := runBatch(ctx, pr.maxConcurrency, targets,
		func(ctx context.Context, index int) (*ports.GenerationRecord, error) {
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

// buildGenerateRequest は通常生成のプロンプトと参照画像リストを構築します。
// 参照画像とプロンプト内の [Subject N] は同じ順序で対応させます。
//
// 参照の解決（GCS 直接参照 / File API へのアップロード）は gemini-image-kit が担うため、
// ここは参照元 URL を並べるだけの純粋な組み立てです（context も state も要りません）。
func (pr *PanelImageRunner) buildGenerateRequest(panel *ports.Panel, opts ports.GenerateOptions) (string, []imagePorts.ImageURI, error) {
	var images []imagePorts.ImageURI
	var subjects []string
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
		subjects = append(subjects, subjectLine(len(images), char, findPanelCharacter(panel, id)))
	}

	if opts.PromptOverride != "" {
		return opts.PromptOverride, images, nil
	}
	return pr.buildPanelPrompt(panel, subjects), images, nil
}

// buildEditRequest は編集モード（既存画像への指示ベースの変更）のプロンプトと入力画像を構築します。
// 構図・ポーズ・背景を保ったまま指示箇所だけを変更します（go-veo-orchestrator の EditCut と同方式）。
func (pr *PanelImageRunner) buildEditRequest(panel *ports.Panel, opts ports.GenerateOptions) (string, []imagePorts.ImageURI, error) {
	if panel.Generation == nil || panel.Generation.ImageURL == "" {
		return "", nil, fmt.Errorf("%w: パネル %q には編集対象の生成済み画像がありません", ports.ErrInvalidRequest, panel.ID)
	}
	prompt := panelEditInstruction + opts.EditPrompt
	images := []imagePorts.ImageURI{{ReferenceURL: panel.Generation.ImageURL}}
	return prompt, images, nil
}

// buildPanelPrompt はパネルの演出情報からユーザープロンプトを組み立てます。
func (pr *PanelImageRunner) buildPanelPrompt(panel *ports.Panel, subjects []string) string {
	var sb strings.Builder
	sb.WriteString("Manga panel illustration.")
	if panel.Shot != "" {
		fmt.Fprintf(&sb, " Shot: %s.", panel.Shot)
	}
	if panel.Setting != "" {
		fmt.Fprintf(&sb, " Setting: %s.", panel.Setting)
	}
	if anchor := strings.TrimSpace(panel.VisualAnchor); anchor != "" {
		sb.WriteString("\nScene direction: ")
		sb.WriteString(anchor)
	}
	for _, subject := range subjects {
		sb.WriteString("\n")
		sb.WriteString(subject)
	}
	if extras := backgroundExtras(panel); extras != "" {
		sb.WriteString("\nBackground extras (no reference, generic): ")
		sb.WriteString(extras)
	}
	if pr.styleSuffix != "" {
		sb.WriteString("\nStyle: ")
		sb.WriteString(pr.styleSuffix)
	}
	sb.WriteString("\nNo speech bubbles, no text.")
	return sb.String()
}

// subjectLine は1キャラクター分の [Subject N] 記述を構築します。
func subjectLine(index int, char *ports.Character, pc *ports.PanelCharacter) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Subject %d: %s", index, char.Name)
	if len(char.VisualCues) > 0 {
		fmt.Fprintf(&sb, " (%s)", strings.Join(char.VisualCues, ", "))
	}
	sb.WriteString("]")
	if pc == nil {
		return sb.String()
	}
	if pc.Emotion != "" {
		fmt.Fprintf(&sb, " emotion: %s.", pc.Emotion)
	}
	if pc.Action != "" {
		fmt.Fprintf(&sb, " action: %s.", pc.Action)
	}
	if pc.Position != "" {
		fmt.Fprintf(&sb, " position: %s.", pc.Position)
	}
	return sb.String()
}

// findPanelCharacter はパネル内の指定キャラクターの登場情報を返します。
func findPanelCharacter(panel *ports.Panel, charID string) *ports.PanelCharacter {
	for i := range panel.Characters {
		if panel.Characters[i].CharacterID == charID {
			return &panel.Characters[i]
		}
	}
	return nil
}

// backgroundExtras は background（モブ）キャラクターの記述をまとめます。
func backgroundExtras(panel *ports.Panel) string {
	var parts []string
	for i := range panel.Characters {
		if panel.Characters[i].Prominence != ports.ProminenceBackground {
			continue
		}
		parts = append(parts, backgroundExtraDesc(&panel.Characters[i]))
	}
	return strings.Join(parts, ", ")
}
