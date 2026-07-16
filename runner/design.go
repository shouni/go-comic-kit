package runner

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"log/slog"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/layout"
	"github.com/shouni/go-comic-kit/ports"
)

const (
	// designPromptBaseTemplate はデザインシートプロンプトの基本形です。
	designPromptBaseTemplate = "Masterpiece character design sheet of %s"

	// designLayoutMultiView は既定のターンアラウンド（前・横・後の3面図）レイアウトです。
	// 全ビューが同一キャラクターであることと、衣装が隠れないニュートラルな
	// Aポーズを明示し、面ごとの細部ブレを抑えます。
	designLayoutMultiView = "multiple views (front, side, back) of the same character, standing full body in a neutral A-pose with arms held slightly away from the body so the costume stays fully visible, views arranged side-by-side and evenly spaced, separate character charts"
	// designLayoutSingleView は、他の生成物（go-veo-orchestratorのキーフレーム、ap-compの
	// カバーアート等）の参照アンカーとして使うための、単一ポーズ・正面向きのレイアウトです。
	// 3面図シートは複数ポーズが1枚の画像に混在するため、それと異なるアスペクト比の生成先の
	// 参照に使うと色・小物配置・髪型などの細部がブレやすい問題があり、単一ポーズは
	// そのアンカー用途に特化したオプションです。
	designLayoutSingleView = "single view, front-facing, standing full body in a neutral relaxed pose, centered composition, the entire body from head to toe inside the frame"

	designLayoutPromptFormat = "Layout: %s"

	// designSystemPrompt はデザインシート生成時にモデルへ与えるシステム指示です。
	// 生成物は他ワークフロー（カバーアート、キーフレーム、パネル等）のキャラクター同一性
	// アンカーとして参照されるため、演出的な絵作りよりも正確さ・一貫性を最優先させます。
	designSystemPrompt = `You are a professional character designer creating official model sheets for animation and manga production.
This sheet is the canonical identity reference that other artists and AI generators will rely on, so accuracy and consistency outweigh artistic flair:
- Anatomical correctness is critical. Draw every hand with exactly five fingers, correct limb proportions, and clean readable silhouettes.
- Every view on the sheet must depict the SAME character with identical hairstyle, hair color, eye color, skin tone, outfit, and accessories.
- Use flat, even, neutral studio lighting only. No dramatic shadows, rim light, lens flares, or color grading — lighting baked into this sheet contaminates every downstream generation that references it.
- The full body must be visible from head to toe and must never be cropped by the frame.
- Render absolutely no text, labels, arrows, color swatches, logos, or annotations of any kind.`

	// designNegativePrompt はデザインシートに含めたくない要素を指定する負のプロンプトです。
	// 指の本数・手の崩れ対策と、シート特有の文字注釈・スウォッチ混入対策を含みます。
	designNegativePrompt = "text, labels, annotations, arrows, color swatches, watermark, logo, signature, malformed hands, fused fingers, extra fingers, missing fingers, extra limbs, deformed anatomy, asymmetrical eyes, cropped body, cut-off feet, dramatic lighting, strong shadows, rim light, lens flare, inconsistent details between views, different character per view, background scenery, props, low quality, blurry"
)

// DesignImageGenerator は、複数参照画像を融合してデザインシート画像を生成する依存インターフェースです。
type DesignImageGenerator interface {
	GenerateFusedImage(ctx context.Context, req imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error)
}

// CharacterResourceProvider は、DesignSheetRunner が layout.ComicComposer に依存する範囲
// だけを切り出した契約です（go-veo-orchestrator の同名インターフェースと同じ方針）。
// 事前アップロード済みのキャラクター参照画像 URI を解決します。
type CharacterResourceProvider interface {
	GetCharacterResourceURI(charID string) string
}

// DesignSheetRunner はキャラクターデザインシート生成（GenerateDesignSheet 操作）を実行します。
type DesignSheetRunner struct {
	characters  *ports.Characters
	resources   CharacterResourceProvider
	generator   DesignImageGenerator
	writer      remoteio.Writer
	model       string
	styleSuffix string
}

var _ ports.DesignSheetGenerator = (*DesignSheetRunner)(nil)

// NewDesignSheetRunner は依存関係を注入して初期化します。styleSuffix にはデザインシート用の
// 画風指定（ports.Config.DesignStyleSuffix）を渡してください。パネル用の StyleSuffix
// （cinematic lighting 等の演出を含む）を渡すと、参照アンカーに演出照明が焼き付きます。
func NewDesignSheetRunner(
	characters *ports.Characters,
	resources CharacterResourceProvider,
	generator DesignImageGenerator,
	writer remoteio.Writer,
	model string,
	styleSuffix string,
) *DesignSheetRunner {
	return &DesignSheetRunner{
		characters:  characters,
		resources:   resources,
		generator:   generator,
		writer:      writer,
		model:       model,
		styleSuffix: styleSuffix,
	}
}

// GenerateDesignSheet はデザインシートを生成・保存し、その記録を state に反映して返します。
// state が nil の場合は新しい MangaState を作成します。複数キャラクター指定時は1枚の
// 合成シートを生成し、各キャラクターに同じ画像の DesignSheetRef を記録します。
func (dr *DesignSheetRunner) GenerateDesignSheet(ctx context.Context, state *ports.MangaState, req ports.DesignSheetRequest) (*ports.MangaState, error) {
	// 1. 複数キャラの情報を集約
	imageURIs, descriptions, err := dr.collectCharacterURIs(req.CharacterIDs, req.Override)
	if err != nil {
		return nil, fmt.Errorf("キャラクター資産の収集に失敗しました: %w", err)
	}

	slog.Info("Executing design sheet generation",
		slog.Any("chars", req.CharacterIDs),
		slog.Int("ref_count", len(imageURIs)),
		slog.String("aspect_ratio", req.AspectRatio),
		slog.String("layout", req.Layout),
	)

	// 2. プロンプト構築
	designPrompt := dr.buildDesignPrompt(descriptions, req.Layout)
	if designPrompt == "" {
		return nil, fmt.Errorf("キャラクター情報が空のため、プロンプトを生成できませんでした")
	}

	// 3. 生成リクエスト
	fusionReq := imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          dr.model,
			Prompt:         designPrompt,
			SystemPrompt:   designSystemPrompt,
			NegativePrompt: designNegativePrompt,
			AspectRatio:    layout.NormalizeDesignAspectRatio(req.AspectRatio),
			ImageSize:      layout.ImageSize2K,
			Seed:           ptrInt64(req.Seed),
		},
		Images: imageURIs,
	}

	// 4. 生成実行
	resp, err := dr.generator.GenerateFusedImage(ctx, fusionReq)
	if err != nil {
		return nil, fmt.Errorf("画像の生成に失敗しました: %w", err)
	}

	// 5. 画像の保存
	outputPath, err := dr.saveResponseImage(ctx, resp, req.CharacterIDs, req.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("画像の保存に失敗しました: %w", err)
	}

	// 6. state への記録（冪等: 同一キャラクターの記録は上書き）
	now := time.Now().UTC()
	if state == nil {
		state = &ports.MangaState{
			Version:   ports.StateSchemaVersion,
			CreatedAt: now,
		}
	}
	for _, id := range req.CharacterIDs {
		state.SetDesignSheet(ports.DesignSheetRef{
			CharacterID: id,
			ImageURL:    outputPath,
			UsedSeed:    resp.UsedSeed,
		})
	}
	state.UpdatedAt = now

	return state, nil
}

// maxDesignFileTagBytes はファイル名に埋め込むキャラクタータグの最大バイト長です。
// ファイルシステムのファイル名長制限（一般に255バイト）に、ディレクトリや接頭辞・拡張子を
// 加えても収まる余裕を持たせた値です。
const maxDesignFileTagBytes = 100

// saveResponseImage は、生成された画像データを指定されたディレクトリに保存します。
func (dr *DesignSheetRunner) saveResponseImage(ctx context.Context, resp *imagePorts.ImageResponse, charIDs []string, outputDir string) (string, error) {
	charTags := designFileTag(charIDs)

	extension := getPreferredExtension(resp.MimeType)
	relativePath := path.Join(asset.CharacterDesignDir, fmt.Sprintf("design_%s%s", charTags, extension))
	finalPath, err := asset.ResolveOutputPath(outputDir, relativePath)
	if err != nil {
		return "", fmt.Errorf("画像保存パスの生成に失敗しました (baseDir: %s, relativePath: %s): %w", outputDir, relativePath, err)
	}

	if err = dr.writer.Write(ctx, finalPath, bytes.NewReader(resp.Data),
		remoteio.WithContentType(resp.MimeType),
		remoteio.WithCacheControl(defaultCacheControl),
	); err != nil {
		return "", fmt.Errorf("画像の保存に失敗しました (path: %s): %w", finalPath, err)
	}

	return finalPath, nil
}

// designFileTag はキャラクターID群からファイル名用のタグを生成します。
// ID が多い・長い場合でもファイルシステムのファイル名長制限に抵触しないよう、
// 上限を超えたら rune 境界で切り詰め、組み合わせの一意性はチェックサムで担保します。
func designFileTag(charIDs []string) string {
	tag := fileNameSanitizer.Replace(strings.Join(charIDs, "_"))
	if len(tag) <= maxDesignFileTagBytes {
		return tag
	}
	sum := crc32.ChecksumIEEE([]byte(tag))
	cut := tag[:maxDesignFileTagBytes]
	// バイト位置での切り詰めがマルチバイト文字を分断した場合は末尾を除去して修復する
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return fmt.Sprintf("%s_%08x", cut, sum)
}

// buildDesignPrompt はキャラクターデザインシート生成用の詳細なプロンプト文字列を構築します。
// layoutKind に ports.DesignLayoutSingleView を渡すと単一ポーズレイアウトになります。
func (dr *DesignSheetRunner) buildDesignPrompt(descriptions []string, layoutKind string) string {
	numChars := len(descriptions)
	if numChars == 0 {
		slog.Warn("buildDesignPrompt called with empty descriptions")
		return ""
	}

	var subjects string
	if numChars > 1 {
		subjectParts := make([]string, numChars)
		for i, d := range descriptions {
			subjectParts[i] = fmt.Sprintf("[Subject %d: %s]", i+1, d)
		}
		subjects = fmt.Sprintf("%d DIFFERENT characters: %s", numChars, strings.Join(subjectParts, " "))
	} else {
		subjects = descriptions[0]
	}

	base := fmt.Sprintf(designPromptBaseTemplate, subjects)
	designLayout := designLayoutMultiView
	if layoutKind == ports.DesignLayoutSingleView {
		designLayout = designLayoutSingleView
	}
	layoutPrompt := fmt.Sprintf(designLayoutPromptFormat, designLayout)

	promptParts := []string{base, layoutPrompt}
	if dr.styleSuffix != "" {
		promptParts = append(promptParts, dr.styleSuffix)
	}
	// styleSuffix に演出用の指定が紛れ込んでも、参照アンカーとしての制約
	// （フラットな照明・白背景・手の正確さ）を後置して優先させる。
	promptParts = append(promptParts,
		"plain uniform white studio background",
		"flat even neutral lighting",
		"sharp focus",
		"perfectly drawn hands with five fingers per hand",
	)

	return strings.Join(promptParts, ", ")
}

// collectCharacterURIs はキャラクター情報を収集し、ImageURIスライスと説明文を返します。
// override は ids が単一（合成デザインシートでない）場合のみ適用されます。
func (dr *DesignSheetRunner) collectCharacterURIs(ids []string, override ports.DesignOverride) ([]imagePorts.ImageURI, []string, error) {
	var uris []imagePorts.ImageURI
	var descriptions []string
	var missingIDs []string
	processedIDs := make(map[string]struct{})
	applyOverride := len(ids) == 1

	for _, id := range ids {
		if _, exists := processedIDs[id]; exists {
			continue
		}
		processedIDs[id] = struct{}{}

		char := dr.characters.GetCharacter(id)
		if char == nil {
			missingIDs = append(missingIDs, id)
			continue
		}

		referenceURL := char.ReferenceURL
		visualCues := char.VisualCues
		// File API URI があれば取得（既定の参照画像に対して事前アップロード済みのもの）
		fileURI := dr.resources.GetCharacterResourceURI(char.ID)

		if applyOverride && strings.TrimSpace(override.ReferenceURL) != "" {
			referenceURL = override.ReferenceURL
			// 上書きURLは事前アップロード対象に含まれていないため、File API URIは使わず
			// ReferenceURLをそのまま渡す（Vertex AI + GCS URIの直接参照にフォールバックする）。
			fileURI = ""
		}
		if applyOverride && len(override.VisualCues) > 0 {
			visualCues = override.VisualCues
		}

		if referenceURL == "" && fileURI == "" {
			slog.Warn("キャラクターに有効な参照画像がないためスキップします", "id", id)
			continue
		}

		uris = append(uris, imagePorts.ImageURI{
			ReferenceURL: referenceURL,
			FileAPIURI:   fileURI,
		})

		desc := char.Name
		if len(visualCues) > 0 {
			desc = fmt.Sprintf("%s (%s)", char.Name, strings.Join(visualCues, ", "))
		}
		descriptions = append(descriptions, desc)
	}

	if len(missingIDs) > 0 {
		return nil, nil, fmt.Errorf("一部のキャラクターIDが見つかりませんでした: %s", strings.Join(missingIDs, ", "))
	}

	if len(uris) == 0 {
		return nil, nil, fmt.Errorf("有効な参照画像を持つキャラクターが1つも見つかりませんでした (対象ID: %s)", strings.Join(ids, ", "))
	}

	return uris, descriptions, nil
}
