package operations

import (
	"context"
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

// DesignSheetRunner はキャラクターデザインシート生成（GenerateDesignSheet 操作）を実行します。
type DesignSheetRunner struct {
	prompt       ports.DesignSheetPrompt
	characters   *ports.Characters
	generator    ImageGenerator
	writer       remoteio.Writer
	model        string
	styleSuffix  string
	cacheControl string
}

var _ ports.DesignSheetGenerator = (*DesignSheetRunner)(nil)

// DesignSheetRunnerArgs は DesignSheetRunner の構築に必要な依存と設定の集合です。
// PanelImageRunnerArgs / PageImageRunnerArgs と同じ形にしてあります。位置引数のままだと
// model / styleSuffix / cacheControl という無関係な文字列が3つ並び、取り違えても
// コンパイルが通ってしまいます。
type DesignSheetRunnerArgs struct {
	// Prompt にはキット内蔵の prompts.DefaultDesignPrompt{} を渡すか、アプリ側で
	// ports.DesignSheetPrompt を実装して独自のプロンプトに差し替えられます。
	Prompt     ports.DesignSheetPrompt
	Characters *ports.Characters
	Generator  ImageGenerator
	Writer     remoteio.Writer
	Model      string
	// StyleSuffix にはデザインシート用の画風指定（ports.Config.DesignStyleSuffix）を
	// 渡してください。パネル用の StyleSuffix（cinematic lighting 等の演出を含む）を渡すと、
	// 参照アンカーに演出照明が焼き付きます。
	StyleSuffix string
	// CacheControl は保存時の Cache-Control です（ports.Config.CacheControl）。
	CacheControl string
}

// NewDesignSheetRunner は依存関係を注入して初期化します。
func NewDesignSheetRunner(args DesignSheetRunnerArgs) *DesignSheetRunner {
	return &DesignSheetRunner{
		prompt:       args.Prompt,
		characters:   args.Characters,
		generator:    args.Generator,
		writer:       args.Writer,
		model:        args.Model,
		styleSuffix:  args.StyleSuffix,
		cacheControl: args.CacheControl,
	}
}

// GenerateDesignSheet はデザインシートを生成・保存し、その記録を state に反映して返します。
// state が nil の場合は新しい MangaState を作成します。複数キャラクター指定時は1枚の
// 合成シートを生成し、各キャラクターに同じ画像の DesignSheetRef を記録します。
func (dr *DesignSheetRunner) GenerateDesignSheet(ctx context.Context, state *ports.MangaState, req ports.DesignSheetRequest) (*ports.MangaState, error) {
	if strings.TrimSpace(req.JobID) == "" {
		return nil, fmt.Errorf("%w: デザインシート生成には job_id が必要です", ports.ErrInvalidRequest)
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
		return nil, fmt.Errorf("%w: デザインシートプロンプトの構築に失敗しました: %w", ports.ErrGeneration, err)
	}

	// 3. 生成リクエスト
	targetModel := dr.model
	if req.ModelOverride != "" {
		targetModel = req.ModelOverride
	}
	// 4. 生成と保存
	// 保存先はキャラクター（の組み合わせ）ごとのディレクトリの下に JobID をファイル名として
	// 配置する構成（character/{tag}/{jobID}.ext）で、同一キャラクターへの複数回の生成を
	// 上書きせず履歴として残します。
	record, err := renderImage(ctx, dr.generator, dr.writer, imageRenderRequest{
		Model:          targetModel,
		Prompt:         userPrompt,
		SystemPrompt:   systemPrompt,
		NegativePrompt: negativePrompt,
		AspectRatio:    layout.NormalizeDesignAspectRatio(req.AspectRatio),
		ImageSize:      layout.ImageSize2K,
		Seed:           designSeed(req.Seed),
		Images:         imageURIs,
		CacheControl:   dr.cacheControl,
		PathFor: func(mimeType string) (string, error) {
			return asset.DesignSheetPath(req.OutputDir, req.CharacterIDs, req.JobID, getPreferredExtension(mimeType))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("デザインシート (%v): %w", req.CharacterIDs, err)
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
			ImageURL:    record.ImageURL,
			UsedSeed:    record.UsedSeed,
		})
	}
	state.UpdatedAt = now

	return state, nil
}

// designSeed は、指定が無ければ（0）新しいシードを採番します。
//
// デザインシートはキャラクターの同一性アンカーなので、後から同じシートを出せることが
// パネル以上に重要です。シードを渡さないと API 側が選んだ値はレスポンスに返らず、
// DesignSheetRef.UsedSeed に 0 が記録されて再現できなくなります
// （パネル・ページ側の resolveSeedChain と同じ理由です）。
func designSeed(v int64) *int64 {
	if v == 0 {
		return newSeed()
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

		if applyOverride && strings.TrimSpace(override.ReferenceURL) != "" {
			referenceURL = override.ReferenceURL
		}
		if applyOverride && len(override.VisualCues) > 0 {
			visualCues = override.VisualCues
		}

		if referenceURL == "" {
			slog.Warn("キャラクターに有効な参照画像がないためスキップします", "id", id)
			continue
		}

		uris = append(uris, imagePorts.ImageURI{ReferenceURL: referenceURL})

		desc := char.Name
		if len(visualCues) > 0 {
			desc = fmt.Sprintf("%s (%s)", char.Name, strings.Join(visualCues, ", "))
		}
		descriptions = append(descriptions, desc)
	}

	if len(missingIDs) > 0 {
		return nil, nil, fmt.Errorf("%w: キャラクターID %s", ports.ErrNotFound, strings.Join(missingIDs, ", "))
	}

	if len(uris) == 0 {
		return nil, nil, fmt.Errorf("%w: 有効な参照画像を持つキャラクターが1つもありません (対象ID: %s)", ports.ErrInvalidRequest, strings.Join(ids, ", "))
	}

	return uris, descriptions, nil
}
