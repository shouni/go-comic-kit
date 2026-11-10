package operations

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-comic-kit/ports"
)

// OutlineRunner runs outline generation (the GenerateOutline operation).
type OutlineRunner struct {
	prompt      ports.OutlinePrompt
	aiClient    StructuredGenerator
	reader      ports.ContentReader
	characters  *comic.Characters
	maxChapters int
}

var _ ports.OutlineGenerator = (*OutlineRunner)(nil)

// NewOutlineRunner initializes the runner with its dependencies injected.
// A maxChapters of 0 or less uses ports.DefaultMaxChapters.
func NewOutlineRunner(
	prompt ports.OutlinePrompt,
	aiClient StructuredGenerator,
	reader ports.ContentReader,
	characters *comic.Characters,
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
		maxChapters: maxChapters,
	}
}

// outlineResponse is the schema of the AI response for outline generation.
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

// GenerateOutline generates, from the source text, a MangaState holding only the outline.
// Chapter IDs are reassigned by the system in "ch01" form (IDs in the AI output are not trusted).
func (r *OutlineRunner) GenerateOutline(ctx context.Context, req ports.OutlineRequest) (*comic.MangaState, error) {
	inputText, err := resolveSourceText(ctx, r.reader, req.SourceText, req.SourceURL)
	if err != nil {
		return nil, err
	}

	maxChapters := r.maxChapters
	if req.MaxChapters > 0 {
		maxChapters = req.MaxChapters
	}

	data := &ports.OutlinePromptData{
		InputText:       inputText,
		CharacterRoster: characterRoster(r.characters),
		MaxChapters:     maxChapters,
	}
	finalPrompt, err := r.prompt.BuildOutline(req.Mode, data)
	if err != nil {
		return nil, fmt.Errorf("%w: build outline prompt: %w", ports.ErrGeneration, err)
	}

	if err := requireModel(req.Model, "outline"); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "OutlineRunner: calling the Gemini API", "model", req.Model, "max_chapters", maxChapters)
	resp, err := r.aiClient.Generate(ctx, req.Model, finalPrompt, nil, buildJSONGenerateOptions(outlineSchema()))
	if err != nil {
		return nil, fmt.Errorf("%w: generate outline: %w", ports.ErrGeneration, err)
	}

	parsed, err := parseJSONResponse[outlineResponse](resp.Text)
	if err != nil {
		return nil, err
	}
	if len(parsed.Chapters) == 0 {
		return nil, fmt.Errorf("%w: outline is empty (the AI response has no chapters)", ports.ErrGeneration)
	}
	if len(parsed.Chapters) > maxChapters {
		slog.WarnContext(ctx, "truncating chapters over the limit",
			"got", len(parsed.Chapters), "max", maxChapters)
		parsed.Chapters = parsed.Chapters[:maxChapters]
	}

	now := time.Now().UTC()
	state := &comic.MangaState{
		Version:     comic.StateSchemaVersion,
		Title:       parsed.Title,
		Description: parsed.Description,
		StyleMode:   req.StyleMode,
		ScriptMode:  req.Mode,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	for i, ch := range parsed.Chapters {
		state.Chapters = append(state.Chapters, comic.Chapter{
			ID:            fmt.Sprintf("ch%02d", i+1),
			Title:         ch.Title,
			Summary:       ch.Summary,
			SourceExcerpt: ch.SourceExcerpt,
		})
	}

	slog.InfoContext(ctx, "OutlineRunner: generated outline",
		"title", state.Title, "chapters", len(state.Chapters))
	return state, nil
}
