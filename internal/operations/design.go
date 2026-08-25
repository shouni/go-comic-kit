package operations

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// DesignSheetRunner はキャラクターデザインシート生成（GenerateDesignSheet 操作）を実行します。
type DesignSheetRunner struct {
	prompt       ports.DesignSheetPrompt
	characters   *comic.Characters
	generator    ImageGenerator
	writer       remoteio.Writer
	cacheControl string
}

var _ ports.DesignSheetGenerator = (*DesignSheetRunner)(nil)

// DesignSheetRunnerArgs は DesignSheetRunner の構築に必要な依存と設定の集合です。
// PanelImageRunnerArgs / PageImageRunnerArgs と同じ形にしてあります。位置引数のままだと
// model / styleSuffix / cacheControl という無関係な文字列が3つ並び、取り違えても
// コンパイルが通ってしまいます。
type DesignSheetRunnerArgs struct {
	// Prompt はアプリ側が実装する ports.DesignSheetPrompt です（キットは内蔵しません）。
	Prompt     ports.DesignSheetPrompt
	Characters *comic.Characters
	Generator  ImageGenerator
	Writer     remoteio.Writer
	ImageSize  string
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
		cacheControl: args.CacheControl,
	}
}

// GenerateDesignSheet はデザインシートを生成・保存し、その記録を state に反映して返します。
// state が nil の場合は新しい MangaState を作成します。複数キャラクター指定時は1枚の
// 合成シートを生成し、各キャラクターに同じ画像の DesignSheetRef を記録します。
func (dr *DesignSheetRunner) GenerateDesignSheet(ctx context.Context, state *comic.MangaState, req ports.DesignSheetRequest) (*comic.MangaState, error) {
	if strings.TrimSpace(req.JobID) == "" {
		return nil, fmt.Errorf("%w: デザインシート生成には job_id が必要です", ports.ErrInvalidRequest)
	}
	if err := requireModel(req.Model, "デザインシート"); err != nil {
		return nil, err
	}
	aspectRatio, err := resolveAspectRatio(req.AspectRatio, layout.DefaultAspectRatio)
	if err != nil {
		return nil, err
	}
	imageSize, err := resolveImageSize(req.ImageSize, layout.ImageSize2K)
	if err != nil {
		return nil, err
	}

	// 1. 複数キャラの情報を集約
	imageURIs, descriptions, err := dr.collectCharacterURIs(ctx, req.CharacterIDs, req.Override)
	if err != nil {
		return nil, fmt.Errorf("キャラクター資産の収集に失敗しました: %w", err)
	}

	slog.InfoContext(ctx, "Executing design sheet generation",
		slog.Any("chars", req.CharacterIDs),
		slog.Int("ref_count", len(imageURIs)),
		slog.String("aspect_ratio", req.AspectRatio),
		slog.String("layout", req.Layout),
	)

	// 2. プロンプト構築
	systemPrompt, userPrompt, negativePrompt, err := dr.prompt.BuildDesignSheet(&ports.DesignSheetPromptData{
		Descriptions: descriptions,
		Layout:       req.Layout,
		StyleMode:    req.StyleMode,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: デザインシートプロンプトの構築に失敗しました: %w", ports.ErrGeneration, err)
	}

	// 3. 生成リクエスト
	// 4. 生成と保存
	// 保存先はキャラクター（の組み合わせ）ごとのディレクトリの下に JobID をファイル名として
	// 配置する構成（character/{tag}/{jobID}.ext）で、同一キャラクターへの複数回の生成を
	// 上書きせず履歴として残します。
	record, err := renderImage(ctx, dr.generator, dr.writer, imageRenderRequest{
		Model:          req.Model,
		Prompt:         userPrompt,
		SystemPrompt:   systemPrompt,
		NegativePrompt: negativePrompt,
		AspectRatio:    aspectRatio,
		ImageSize:      imageSize,
		Seed:           designSeed(req.Seed),
		Images:         imageURIs,
		CacheControl:   dr.cacheControl,
		PathFor: func(mimeType string) (string, error) {
			return asset.DesignSheetPath(req.OutputDir, req.CharacterIDs, req.JobID, imagePorts.ExtensionByMIMEType(mimeType))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("デザインシート (%v): %w", req.CharacterIDs, err)
	}

	// 6. state への記録（冪等: 同一キャラクターの記録は上書き）
	now := time.Now().UTC()
	if state == nil {
		state = &comic.MangaState{
			Version:   comic.StateSchemaVersion,
			CreatedAt: now,
		}
	}
	for _, id := range req.CharacterIDs {
		state.SetDesignSheet(comic.DesignSheetRef{
			CharacterID: id,
			ImageURL:    record.ImageURL,
			UsedSeed:    record.UsedSeed,
		})
	}
	state.UpdatedAt = now

	return state, nil
}

// designSeed は、指定が無ければ（nil）新しいシードを採番します。
func designSeed(v *int64) *int64 {
	if v == nil {
		return newSeed()
	}
	return v
}

// collectCharacterURIs はキャラクター情報を収集し、ImageURIスライスと説明文を返します。
// override は ids が単一（合成デザインシートでない）場合のみ適用されます。
func (dr *DesignSheetRunner) collectCharacterURIs(ctx context.Context, ids []string, override ports.DesignOverride) ([]imagePorts.ImageURI, []string, error) {
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
			slog.WarnContext(ctx, "キャラクターに有効な参照画像がないためスキップします", "id", id)
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
