package operations

import (
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
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// CharacterResourceProvider は、DesignSheetRunner が layout.ComicComposer に依存する範囲
// だけを切り出した契約です（go-veo-orchestrator の同名インターフェースと同じ方針）。
// 事前アップロード済みのキャラクター参照画像 URI を解決します。
type CharacterResourceProvider interface {
	GetCharacterResourceURI(charID string) string
}

// DesignSheetRunner はキャラクターデザインシート生成（GenerateDesignSheet 操作）を実行します。
type DesignSheetRunner struct {
	prompt      ports.DesignSheetPrompt
	characters  *ports.Characters
	resources   CharacterResourceProvider
	generator   ImageFusionGenerator
	writer      remoteio.Writer
	model       string
	styleSuffix string
}

var _ ports.DesignSheetGenerator = (*DesignSheetRunner)(nil)

// NewDesignSheetRunner は依存関係を注入して初期化します。styleSuffix にはデザインシート用の
// 画風指定（ports.Config.DesignStyleSuffix）を渡してください。パネル用の StyleSuffix
// （cinematic lighting 等の演出を含む）を渡すと、参照アンカーに演出照明が焼き付きます。
// prompt にはキット内蔵の prompts.DefaultDesignPrompt{} を渡すか、アプリ側で
// ports.DesignSheetPrompt を実装して独自のプロンプトに差し替えられます。
func NewDesignSheetRunner(
	prompt ports.DesignSheetPrompt,
	characters *ports.Characters,
	resources CharacterResourceProvider,
	generator ImageFusionGenerator,
	writer remoteio.Writer,
	model string,
	styleSuffix string,
) *DesignSheetRunner {
	return &DesignSheetRunner{
		prompt:      prompt,
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
	if strings.TrimSpace(req.JobID) == "" {
		return nil, fmt.Errorf("job_id is required to generate a design sheet")
	}

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
	systemPrompt, userPrompt, negativePrompt, err := dr.prompt.BuildDesignSheet(&ports.DesignSheetPromptData{
		Descriptions: descriptions,
		Layout:       req.Layout,
		StyleSuffix:  dr.styleSuffix,
	})
	if err != nil {
		return nil, fmt.Errorf("デザインシートプロンプトの構築に失敗しました: %w", err)
	}

	// 3. 生成リクエスト
	targetModel := dr.model
	if req.ModelOverride != "" {
		targetModel = req.ModelOverride
	}
	fusionReq := imagePorts.ImageFusionRequest{
		GenerationOptions: imagePorts.GenerationOptions{
			Model:          targetModel,
			Prompt:         userPrompt,
			SystemPrompt:   systemPrompt,
			NegativePrompt: negativePrompt,
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
	outputPath, err := dr.saveResponseImage(ctx, resp, req.CharacterIDs, req.JobID, req.OutputDir)
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
// 保存先はキャラクター（の組み合わせ）ごとのディレクトリの下に JobID をファイル名として
// 配置する構成（character/{tag}/{jobID}.ext）で、同一キャラクターへの複数回の生成を
// 上書きせず履歴として残します。
func (dr *DesignSheetRunner) saveResponseImage(ctx context.Context, resp *imagePorts.ImageResponse, charIDs []string, jobID string, outputDir string) (string, error) {
	charTags := designFileTag(charIDs)
	safeJobID := fileNameSanitizer.Replace(jobID)

	extension := getPreferredExtension(resp.MimeType)
	relativePath := path.Join(asset.CharacterDesignDir, charTags, safeJobID+extension)
	finalPath, err := asset.ResolveOutputPath(outputDir, relativePath)
	if err != nil {
		return "", fmt.Errorf("画像保存パスの生成に失敗しました (baseDir: %s, relativePath: %s): %w", outputDir, relativePath, err)
	}

	if err = writeGeneratedImage(ctx, dr.writer, finalPath, resp); err != nil {
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

// ptrInt64 は 0 を nil として扱う int64 ポインタ変換です。
func ptrInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
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
