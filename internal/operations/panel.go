package operations

import (
	"context"
	"fmt"
	"log/slog"
	"path"
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

// PanelResourceProvider は、PanelImageRunner が layout.ComicComposer に依存する範囲だけを
// 切り出した契約です。参照画像の事前アップロードと、アスペクト比別のアップロード済み URI
// 解決を提供します。
type PanelResourceProvider interface {
	PrepareCharacterResources(ctx context.Context, state *ports.MangaState) error
	GetCharacterResourceURIFor(charID, aspectRatio string) string
}

// PanelImageRunner はパネル画像の生成/再生成（GeneratePanel 操作）を実行します。
type PanelImageRunner struct {
	characters  *ports.Characters
	resources   PanelResourceProvider
	generator   ImageFusionGenerator
	writer      remoteio.Writer
	model       string
	styleSuffix string
	aspectRatio string
	imageSize   string
}

var _ ports.PanelImageGenerator = (*PanelImageRunner)(nil)

// PanelImageRunnerArgs は PanelImageRunner の構築に必要な依存と設定の集合です。
type PanelImageRunnerArgs struct {
	Characters *ports.Characters
	Resources  PanelResourceProvider
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
}

// NewPanelImageRunner は依存関係を注入して初期化します。
func NewPanelImageRunner(args PanelImageRunnerArgs) *PanelImageRunner {
	if args.AspectRatio == "" {
		args.AspectRatio = layout.PanelAspectRatio
	}
	if args.ImageSize == "" {
		args.ImageSize = layout.ImageSize1K
	}
	return &PanelImageRunner{
		characters:  args.Characters,
		resources:   args.Resources,
		generator:   args.Generator,
		writer:      args.Writer,
		model:       args.Model,
		styleSuffix: args.StyleSuffix,
		aspectRatio: args.AspectRatio,
		imageSize:   args.ImageSize,
	}
}

// GeneratePanel は指定パネルの画像を生成し、結果を GenerationRecord として state に記録します。
// opts.Seed が nil の場合は前回の UsedSeed（あれば）を再利用し「同条件での再生成」になります。
// opts.EditPrompt を指定すると、既存の生成済み画像を入力とした編集モードになります。
func (pr *PanelImageRunner) GeneratePanel(ctx context.Context, state *ports.MangaState, panelID string, opts ports.GenerateOptions) (*ports.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("state が nil です")
	}
	panel := state.PanelByID(panelID)
	if panel == nil {
		return nil, fmt.Errorf("パネル %q が見つかりません", panelID)
	}

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
		prompt, images, err = pr.buildGenerateRequest(ctx, state, panel, opts)
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

	// 生成実行
	resp, err := pr.generator.GenerateFusedImage(ctx, imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          targetModel,
			Prompt:         prompt,
			SystemPrompt:   panelSystemPrompt,
			NegativePrompt: panelNegativePrompt,
			AspectRatio:    pr.aspectRatio,
			ImageSize:      pr.imageSize,
			Seed:           seed,
		},
		Images: images,
	})
	if err != nil {
		return nil, fmt.Errorf("パネル %q の画像生成に失敗しました: %w", panelID, err)
	}

	// 保存（パネルIDに紐づく安定したパスに上書きする）
	fileName := fmt.Sprintf("panel_%s%s", fileNameSanitizer.Replace(panelID), getPreferredExtension(resp.MimeType))
	finalPath, err := asset.ResolveOutputPath(opts.OutputDir, path.Join(asset.DefaultImageDir, fileName))
	if err != nil {
		return nil, fmt.Errorf("パネル画像の保存パス生成に失敗しました: %w", err)
	}
	if err := writeGeneratedImage(ctx, pr.writer, finalPath, resp); err != nil {
		return nil, fmt.Errorf("パネル画像の保存に失敗しました (path: %s): %w", finalPath, err)
	}

	// 生成条件を記録（再生成の基礎）
	now := time.Now().UTC()
	panel.Generation = &ports.GenerationRecord{
		ImageURL:       finalPath,
		UsedSeed:       resp.UsedSeed,
		Prompt:         prompt,
		NegativePrompt: panelNegativePrompt,
		Model:          targetModel,
		GeneratedAt:    now,
	}
	state.UpdatedAt = now

	slog.Info("Panel image generation completed", "panel", panelID, "path", finalPath)
	return state, nil
}

// buildGenerateRequest は通常生成のプロンプトと参照画像リストを構築します。
// 参照画像とプロンプト内の [Subject N] は同じ順序で対応させます。
func (pr *PanelImageRunner) buildGenerateRequest(ctx context.Context, state *ports.MangaState, panel *ports.Panel, opts ports.GenerateOptions) (string, []imagePorts.ImageURI, error) {
	// 参照キャラクターの画像を事前アップロード（アップロード済みはキャッシュされる）
	if err := pr.resources.PrepareCharacterResources(ctx, state); err != nil {
		return "", nil, fmt.Errorf("参照画像の事前準備に失敗しました: %w", err)
	}

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
		images = append(images, imagePorts.ImageURI{
			ReferenceURL: referenceURL,
			FileAPIURI:   pr.resources.GetCharacterResourceURIFor(id, pr.aspectRatio),
		})
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
		return "", nil, fmt.Errorf("パネル %q には編集対象の生成済み画像がありません", panel.ID)
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
