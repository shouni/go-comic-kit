package runner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/layout"
	"github.com/shouni/go-comic-kit/ports"
)

const (
	// pageSystemPrompt はページ合成時にモデルへ与えるシステム指示です。
	// パネル数・レイアウトの厳守と、参照画像とのキャラクター同一性を最優先させます。
	pageSystemPrompt = `You are a master digital manga artist. You MUST follow the exact panel count and layout rules. Character identity MUST match the character master reference files.

### FORMAT RULES: FULL COLOR ANIME MANGA ###
- STYLE: Vibrant Full Color Digital Anime Style. High saturation, cinematic lighting.
- RENDERING: Sharp clean lineart with professional digital coloring. NO screentones.
- LAYOUT: Strict multi-panel composition. Use ONLY the specified number of panels.
- NO FILLER: Do not add extra panels or decorative small frames. Fill the page with the given count.
- BORDERS: Deep black, crisp frame borders for EVERY panel.
- GUTTERS: Pure white space between panels.
- READING FLOW: Right-to-Left, Top-to-Bottom.`

	// pageNegativePrompt はページ画像に含めたくない要素を指定する負のプロンプトです。
	// モノクロ・スクリーントーンの排除（フルカラー強制）と、パネル数の暴走・手の崩れ対策を含みます。
	pageNegativePrompt = "monochrome, black and white, greyscale, screentone, hatching, dot shades, ink sketch, line art only, realistic photos, 3d render, watermark, signature, deformed faces, bad anatomy, disfigured, poorly drawn hands, extra fingers, missing fingers, extra panels, unexpected panels, more than specified panels, split panels"

	// pageEditInstruction は編集モードでレイアウトの維持を指示する共通プレフィックスです。
	pageEditInstruction = "Edit the attached manga page image. Keep the panel layout, compositions, dialogue balloons, and art style unchanged. Apply ONLY this change: "
)

// PageResourceProvider は、PageImageRunner が layout.ComicComposer に依存する範囲だけを
// 切り出した契約です。キャラクター参照とパネル画像参照の事前アップロードと URI 解決を提供します。
type PageResourceProvider interface {
	PrepareCharacterResources(ctx context.Context, state *ports.MangaState) error
	PreparePanelResources(ctx context.Context, state *ports.MangaState) error
	GetCharacterResourceURIFor(charID, aspectRatio string) string
	GetPanelResourceURI(referenceURL string) string
}

// PageImageRunner はページ画像の合成（ComposePage 操作）を実行します。
type PageImageRunner struct {
	characters  *ports.Characters
	resources   PageResourceProvider
	generator   ImageFusionGenerator
	writer      remoteio.Writer
	model       string
	styleSuffix string
	aspectRatio string
	imageSize   string
}

var _ ports.PageImageComposer = (*PageImageRunner)(nil)

// PageImageRunnerArgs は PageImageRunner の構築に必要な依存と設定の集合です。
type PageImageRunnerArgs struct {
	Characters *ports.Characters
	Resources  PageResourceProvider
	Generator  ImageFusionGenerator
	Writer     remoteio.Writer
	// Model には高品質系モデル（ports.Config.ImageQualityModel）を渡すことを推奨します。
	Model string
	// StyleSuffix にはページ用の画風指定（ports.Config.StyleSuffix）を渡してください。
	StyleSuffix string
	// AspectRatio が空の場合は layout.PageAspectRatio を使います。
	AspectRatio string
	// ImageSize が空の場合は layout.ImageSize2K を使います。
	ImageSize string
}

// NewPageImageRunner は依存関係を注入して初期化します。
func NewPageImageRunner(args PageImageRunnerArgs) *PageImageRunner {
	if args.AspectRatio == "" {
		args.AspectRatio = layout.PageAspectRatio
	}
	if args.ImageSize == "" {
		args.ImageSize = layout.ImageSize2K
	}
	return &PageImageRunner{
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
func (pg *PageImageRunner) ComposePage(ctx context.Context, state *ports.MangaState, page int, opts ports.GenerateOptions) (*ports.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("state が nil です")
	}
	panels := state.PanelsForPage(page)
	if len(panels) == 0 {
		return nil, fmt.Errorf("ページ %d にパネルがありません", page)
	}

	targetModel := pg.model
	if opts.ModelOverride != "" {
		targetModel = opts.ModelOverride
	}
	existing := state.PageArtifactByNumber(page)
	var prevGeneration *ports.GenerationRecord
	if existing != nil {
		prevGeneration = existing.Generation
	}
	seed := resolveSeedChain(opts.Seed, prevGeneration, pg.characters, panels[0].Characters)

	var prompt string
	var images []imagePorts.ImageURI
	var err error
	if opts.EditPrompt != "" {
		prompt, images, err = pg.buildEditRequest(page, existing, opts)
	} else {
		prompt, images, err = pg.buildComposeRequest(ctx, state, panels, opts)
	}
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

	resp, err := pg.generator.GenerateFusedImage(ctx, imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          targetModel,
			Prompt:         prompt,
			SystemPrompt:   pg.buildSystemPrompt(),
			NegativePrompt: pageNegativePrompt,
			AspectRatio:    pg.aspectRatio,
			ImageSize:      pg.imageSize,
			Seed:           seed,
		},
		Images: images,
	})
	if err != nil {
		return nil, fmt.Errorf("ページ %d の合成に失敗しました: %w", page, err)
	}

	// 保存（ページ番号に紐づく安定したパスに上書きする）
	basePath, err := asset.ResolveOutputPath(opts.OutputDir, asset.DefaultPageImagePath())
	if err != nil {
		return nil, fmt.Errorf("ページ画像の保存パス生成に失敗しました: %w", err)
	}
	finalPath, err := asset.GenerateIndexedPath(basePath, page)
	if err != nil {
		return nil, fmt.Errorf("ページ画像の保存パス生成に失敗しました: %w", err)
	}
	if err := writeGeneratedImage(ctx, pg.writer, finalPath, resp); err != nil {
		return nil, fmt.Errorf("ページ画像の保存に失敗しました (path: %s): %w", finalPath, err)
	}

	// 記録（同一ページ番号は upsert）
	panelIDs := make([]string, len(panels))
	for i := range panels {
		panelIDs[i] = panels[i].ID
	}
	now := time.Now().UTC()
	state.SetPageArtifact(ports.PageArtifact{
		PageNumber: page,
		PanelIDs:   panelIDs,
		Generation: &ports.GenerationRecord{
			ImageURL:       finalPath,
			UsedSeed:       resp.UsedSeed,
			Prompt:         prompt,
			NegativePrompt: pageNegativePrompt,
			Model:          targetModel,
			GeneratedAt:    now,
		},
	})
	state.UpdatedAt = now

	slog.Info("Page composition completed", "page", page, "path", finalPath)
	return state, nil
}

// buildEditRequest は編集モード（既存ページ画像への指示ベースの変更）を構築します。
func (pg *PageImageRunner) buildEditRequest(page int, existing *ports.PageArtifact, opts ports.GenerateOptions) (string, []imagePorts.ImageURI, error) {
	if existing == nil || existing.Generation == nil || existing.Generation.ImageURL == "" {
		return "", nil, fmt.Errorf("ページ %d には編集対象の生成済み画像がありません", page)
	}
	prompt := pageEditInstruction + opts.EditPrompt
	images := []imagePorts.ImageURI{{ReferenceURL: existing.Generation.ImageURL}}
	return prompt, images, nil
}

// buildComposeRequest は通常合成のプロンプトと参照画像リストを構築します。
func (pg *PageImageRunner) buildComposeRequest(ctx context.Context, state *ports.MangaState, panels []ports.Panel, opts ports.GenerateOptions) (string, []imagePorts.ImageURI, error) {
	if err := pg.resources.PrepareCharacterResources(ctx, state); err != nil {
		return "", nil, fmt.Errorf("キャラクター参照の事前準備に失敗しました: %w", err)
	}
	if err := pg.resources.PreparePanelResources(ctx, state); err != nil {
		return "", nil, fmt.Errorf("パネル参照の事前準備に失敗しました: %w", err)
	}

	res := pg.collectPageResources(panels)
	if opts.PromptOverride != "" {
		return opts.PromptOverride, res.images, nil
	}
	return pg.buildPagePrompt(panels, res), res.images, nil
}

// collectPageResources はページ内の参照画像を「キャラクター → 生成済みパネル」の順で集約し、
// プロンプトから参照する input_file 番号（1始まり）を割り振ります。
func (pg *PageImageRunner) collectPageResources(panels []ports.Panel) *pageResources {
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
			res.images = append(res.images, imagePorts.ImageURI{
				ReferenceURL: referenceURL,
				FileAPIURI:   pg.resources.GetCharacterResourceURIFor(id, pg.aspectRatio),
			})
			res.characterFile[id] = len(res.images)
		}
	}

	// 2. 生成済みパネル画像（構図ガイド）
	for _, panel := range panels {
		if panel.Generation == nil || panel.Generation.ImageURL == "" {
			continue
		}
		url := panel.Generation.ImageURL
		res.images = append(res.images, imagePorts.ImageURI{
			ReferenceURL: url,
			FileAPIURI:   pg.resources.GetPanelResourceURI(url),
		})
		res.panelFile[panel.ID] = len(res.images)
	}

	return res
}

// buildSystemPrompt はスタイル指定を含むシステムプロンプトを構築します。
func (pg *PageImageRunner) buildSystemPrompt() string {
	if pg.styleSuffix == "" {
		return pageSystemPrompt
	}
	return pageSystemPrompt + "\n\n### ARTISTIC STYLE ###\n" + pg.styleSuffix
}

// buildPagePrompt はページ合成のユーザープロンプトを組み立てます。
func (pg *PageImageRunner) buildPagePrompt(panels []ports.Panel, res *pageResources) string {
	var sb strings.Builder
	numPanels := len(panels)

	sb.WriteString("# FULL COLOR PAGE PRODUCTION REQUEST\n")
	sb.WriteString("- OUTPUT: ONE single portrait manga page image.\n")
	sb.WriteString("- COLOR: STRICTLY VIBRANT FULL COLOR. NO monochrome, NO screentones.\n")
	fmt.Fprintf(&sb, "- PANEL COUNT: [ %d ] (STRICTLY ONLY %d PANELS. DO NOT ADD ANY MORE).\n\n", numPanels, numPanels)

	pg.writeLayoutStructure(&sb, numPanels)
	pg.writeCharacterReferences(&sb, panels, res)
	pg.writePanelBreakdown(&sb, panels, res)

	return strings.TrimRight(sb.String(), "\n")
}

// writeLayoutStructure は右開き・2列グリッドのパネル配置マップを出力します。
// パネル数が奇数の場合、最後のパネルは下段の全幅（見せゴマ）にします。
func (pg *PageImageRunner) writeLayoutStructure(sb *strings.Builder, numPanels int) {
	sb.WriteString("## MANDATORY PAGE STRUCTURE\n")
	sb.WriteString("- READING ORDER: Japanese Style (Right-to-Left, then Top-to-Bottom).\n")
	sb.WriteString("- PANEL PLACEMENT MAP:\n")

	if numPanels == 1 {
		sb.WriteString("  * PANEL 1: SINGLE FULL-PAGE PANEL (covers entire image area).\n")
	} else {
		for i := range numPanels {
			if numPanels%2 == 1 && i == numPanels-1 {
				fmt.Fprintf(sb, "  * PANEL %d: BOTTOM ROW, FULL-WIDTH.\n", i+1)
				continue
			}
			side := "RIGHT"
			if i%2 == 1 {
				side = "LEFT"
			}
			fmt.Fprintf(sb, "  * PANEL %d: ROW %d, %s column.\n", i+1, i/2+1, side)
		}
	}
	sb.WriteString("- FRAME STYLE: Deep black borders. GUTTERS: Pure white.\n\n")
}

// writeCharacterReferences はキャラクターマスター参照の一覧を出力します。
func (pg *PageImageRunner) writeCharacterReferences(sb *strings.Builder, panels []ports.Panel, res *pageResources) {
	if len(res.characterFile) == 0 {
		return
	}
	sb.WriteString("## CHARACTER MASTER REFERENCES\n")
	seen := make(map[string]struct{})
	for _, panel := range panels {
		for _, id := range panel.ReferencedCharacterIDs() {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			idx, ok := res.characterFile[id]
			if !ok {
				continue
			}
			char := pg.characters.GetCharacter(id)
			cues := "vivid anime color palette"
			if len(char.VisualCues) > 0 {
				cues = strings.Join(char.VisualCues, ", ")
			}
			fmt.Fprintf(sb, "- SUBJECT [%s]: Match input_file_%d. Traits: {%s}.\n", char.Name, idx, cues)
		}
	}
	sb.WriteString("\n")
}

// writePanelBreakdown はパネルごとの内訳（配置・演出・登場キャラ・セリフ）を出力します。
func (pg *PageImageRunner) writePanelBreakdown(sb *strings.Builder, panels []ports.Panel, res *pageResources) {
	numPanels := len(panels)
	sb.WriteString("## PANEL BREAKDOWN\n")
	for i := range panels {
		panel := &panels[i]
		pg.writePanelHeader(sb, i, numPanels)
		pg.writePanelScene(sb, panel, res)
		pg.writePanelCharacters(sb, panel, res)
		writePanelDialogues(sb, panel, pg.characters)
		sb.WriteString("\n")
	}
}

// writePanelHeader はパネルの見出しと配置指示を出力します。
func (pg *PageImageRunner) writePanelHeader(sb *strings.Builder, index, numPanels int) {
	switch {
	case numPanels == 1:
		fmt.Fprintf(sb, "### PANEL 1 [FULL-PAGE]\n- POSITION: Entire page area\n")
	case numPanels%2 == 1 && index == numPanels-1:
		fmt.Fprintf(sb, "### PANEL %d [FULL-WIDTH IMPACT]\n", index+1)
		sb.WriteString("- POSITION: Bottom row, covering the entire width of the page\n")
		sb.WriteString("- COMPOSITION: Cinematic wide shot, high impact focus.\n")
	default:
		side := "RIGHT"
		if index%2 == 1 {
			side = "LEFT"
		}
		fmt.Fprintf(sb, "### PANEL %d [Standard]\n- POSITION: Row %d, %s column\n", index+1, index/2+1, side)
	}
}

// writePanelScene はシーン演出と構図ガイド（生成済みパネル画像）を出力します。
func (pg *PageImageRunner) writePanelScene(sb *strings.Builder, panel *ports.Panel, res *pageResources) {
	if panel.Shot != "" {
		fmt.Fprintf(sb, "- SHOT: %s\n", panel.Shot)
	}
	if panel.Setting != "" {
		fmt.Fprintf(sb, "- SETTING: %s\n", panel.Setting)
	}
	if anchor := strings.TrimSpace(panel.VisualAnchor); anchor != "" {
		fmt.Fprintf(sb, "- SCENE: %s\n", anchor)
	}
	if idx, ok := res.panelFile[panel.ID]; ok {
		fmt.Fprintf(sb, "- COMPOSITION_GUIDE: Recreate the composition, posing, and background from input_file_%d inside this panel.\n", idx)
	}
}

// writePanelCharacters は登場キャラクターの同一性・演出指示を出力します。
func (pg *PageImageRunner) writePanelCharacters(sb *strings.Builder, panel *ports.Panel, res *pageResources) {
	for i := range panel.Characters {
		pc := &panel.Characters[i]
		if pc.Prominence == ports.ProminenceBackground {
			fmt.Fprintf(sb, "- BACKGROUND_EXTRA: %s (generic, no reference)\n", backgroundExtraDesc(pc))
			continue
		}
		char := pg.characters.GetCharacter(pc.CharacterID)
		if char == nil {
			continue
		}
		if idx, ok := res.characterFile[pc.CharacterID]; ok {
			fmt.Fprintf(sb, "- CHARACTER_IDENTITY: [ %s ] from input_file_%d. (Face, hair, and outfit MUST match input_file_%d exactly).\n", char.Name, idx, idx)
		} else {
			fmt.Fprintf(sb, "- SUBJECT: %s\n", char.Name)
		}
		var traits []string
		if pc.Emotion != "" {
			traits = append(traits, "emotion: "+pc.Emotion)
		}
		if pc.Action != "" {
			traits = append(traits, "action: "+pc.Action)
		}
		if pc.Position != "" {
			traits = append(traits, "position: "+pc.Position)
		}
		if len(traits) > 0 {
			fmt.Fprintf(sb, "  - DIRECTION: %s\n", strings.Join(traits, " / "))
		}
	}
}

// writePanelDialogues はセリフ・ナレーション・SFX の描画指示を kind 別に出力します。
func writePanelDialogues(sb *strings.Builder, panel *ports.Panel, characters *ports.Characters) {
	for _, line := range panel.Dialogues {
		text := strings.TrimSpace(line.Text)
		if text == "" {
			continue
		}
		switch line.Kind {
		case ports.DialogueKindNarration:
			sb.WriteString("- NARRATION: Rectangular caption box.\n")
		case ports.DialogueKindThought:
			fmt.Fprintf(sb, "- THOUGHT: Cloud-shaped thought bubble for [%s].\n", speakerName(characters, line.SpeakerID))
		case ports.DialogueKindShout:
			fmt.Fprintf(sb, "- SHOUT: Jagged, explosive speech bubble for [%s].\n", speakerName(characters, line.SpeakerID))
		case ports.DialogueKindSFX:
			sb.WriteString("- SFX: Stylized sound-effect lettering integrated into the artwork.\n")
		default:
			// SpeakerID が空のセリフはナレーション/キャプション扱い（ports.DialogueLine 参照）
			if strings.TrimSpace(line.SpeakerID) == "" {
				sb.WriteString("- NARRATION: Rectangular caption box.\n")
			} else {
				fmt.Fprintf(sb, "- SPEECH: Speech bubble for [%s].\n", speakerName(characters, line.SpeakerID))
			}
		}
		fmt.Fprintf(sb, "  - TEXT_TO_RENDER: %q\n", text)

		direction := "Vertical (Tategaki)"
		layoutDesc := "traditional Japanese manga style layout"
		// 短い叫びなどはインパクト重視で「横書き」も許可する
		if len([]rune(text)) <= 10 && strings.ContainsAny(text, "!?！？") {
			direction = "Horizontal (Yokogaki) or Vertical"
			layoutDesc = "bold and high impact placement"
		}
		fmt.Fprintf(sb, "  - TEXT_DIRECTION: %s\n", direction)
		fmt.Fprintf(sb, "  - TYPOGRAPHY: Use professional Japanese manga font (Gothic/Mincho). %s.\n", layoutDesc)
		sb.WriteString("  - LANGUAGE: Japanese characters. Ensure accurate rendering of Kanji/Kana.\n")
	}
}

// speakerName は話者IDから表示名を解決します。
func speakerName(characters *ports.Characters, speakerID string) string {
	if characters != nil {
		if char := characters.GetCharacter(speakerID); char != nil {
			return char.Name
		}
	}
	return speakerID
}
