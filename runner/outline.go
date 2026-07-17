package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/genai"

	"github.com/shouni/go-comic-kit/ports"
)

// OutlineRunner は章立て生成（GenerateOutline 操作）を実行します。
type OutlineRunner struct {
	prompt      ports.OutlinePrompt
	aiClient    StructuredGenerator
	reader      ports.ContentReader
	characters  *ports.Characters
	model       string
	maxChapters int
}

var _ ports.OutlineGenerator = (*OutlineRunner)(nil)

// NewOutlineRunner は依存関係を注入して初期化します。
// maxChapters が 0 以下の場合は ports.DefaultMaxChapters を使います。
func NewOutlineRunner(
	prompt ports.OutlinePrompt,
	aiClient StructuredGenerator,
	reader ports.ContentReader,
	characters *ports.Characters,
	model string,
	maxChapters int,
) *OutlineRunner {
	if maxChapters <= 0 {
		maxChapters = ports.DefaultMaxChapters
	}
	return &OutlineRunner{
		prompt:      prompt,
		aiClient:    aiClient,
		reader:      reader,
		characters:  characters,
		model:       model,
		maxChapters: maxChapters,
	}
}

// outlineResponse は章立て生成の AI 応答のスキーマです。
type outlineResponse struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Chapters    []struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		Summary       string `json:"summary"`
		SourceExcerpt string `json:"source_excerpt"`
	} `json:"chapters"`
}

// GenerateOutline は元文章から章立てのみを持つ MangaState を生成します。
// 章の ID はシステム側で "ch01" 形式に採番し直します（AI 出力の ID は信用しない）。
func (r *OutlineRunner) GenerateOutline(ctx context.Context, req ports.OutlineRequest) (*ports.MangaState, error) {
	// 1. ソーステキストの解決
	inputText, err := resolveSourceText(ctx, r.reader, req.SourceText, req.SourceURL)
	if err != nil {
		return nil, err
	}

	maxChapters := r.maxChapters
	if req.MaxChapters > 0 {
		maxChapters = req.MaxChapters
	}

	// 2. プロンプト構築
	data := &ports.OutlinePromptData{
		InputText:       inputText,
		CharacterRoster: characterRoster(r.characters),
		MaxChapters:     maxChapters,
	}
	finalPrompt, err := r.prompt.BuildOutline(req.Mode, data)
	if err != nil {
		return nil, fmt.Errorf("章立てプロンプトの構築に失敗しました: %w", err)
	}

	// 3. 生成（構造化出力: スキーマで文法レベルに制約する）
	slog.Info("OutlineRunner: Gemini APIを呼び出し中", "model", r.model, "max_chapters", maxChapters)
	parts := []*genai.Part{{Text: finalPrompt}}
	resp, err := r.aiClient.GenerateWithParts(ctx, r.model, parts, buildJSONGenerateOptions(outlineSchema()))
	if err != nil {
		return nil, fmt.Errorf("章立ての生成に失敗しました: %w", err)
	}

	// 4. パースと正規化
	var parsed outlineResponse
	if err := parseJSONResponse(resp.Text, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Chapters) == 0 {
		return nil, fmt.Errorf("章立てが空です（AI応答に chapters がありません）")
	}
	if len(parsed.Chapters) > maxChapters {
		slog.Warn("章数が上限を超えたため切り詰めます",
			"got", len(parsed.Chapters), "max", maxChapters)
		parsed.Chapters = parsed.Chapters[:maxChapters]
	}

	now := time.Now().UTC()
	state := &ports.MangaState{
		Version:     ports.StateSchemaVersion,
		Title:       parsed.Title,
		Description: parsed.Description,
		StyleMode:   req.StyleMode,
		ScriptMode:  req.Mode,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	for i, ch := range parsed.Chapters {
		state.Chapters = append(state.Chapters, ports.Chapter{
			ID:            fmt.Sprintf("ch%02d", i+1),
			Title:         ch.Title,
			Summary:       ch.Summary,
			SourceExcerpt: ch.SourceExcerpt,
		})
	}

	slog.Info("OutlineRunner: 章立てを生成しました",
		"title", state.Title, "chapters", len(state.Chapters))
	return state, nil
}
