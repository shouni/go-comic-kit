package operations

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-comic-kit/ports"
)

// ChapterScriptRunner runs per-chapter script generation (the GenerateChapterScript operation).
type ChapterScriptRunner struct {
	prompt           ports.ChapterScriptPrompt
	aiClient         StructuredGenerator
	characters       *comic.Characters
	maxPanels        int
	maxPanelsPerPage int
}

var _ ports.ChapterScriptGenerator = (*ChapterScriptRunner)(nil)

// NewChapterScriptRunner initializes the runner with its dependencies injected. Values of
// 0 or less use ports.DefaultMaxPanelsPerChapter / comic.DefaultMaxPanelsPerPage.
func NewChapterScriptRunner(
	prompt ports.ChapterScriptPrompt,
	aiClient StructuredGenerator,
	characters *comic.Characters,
	maxPanels int,
	maxPanelsPerPage int,
) *ChapterScriptRunner {
	if maxPanels <= 0 {
		maxPanels = ports.DefaultMaxPanelsPerChapter
	}
	if maxPanelsPerPage <= 0 {
		maxPanelsPerPage = comic.DefaultMaxPanelsPerPage
	}
	return &ChapterScriptRunner{
		prompt:           prompt,
		aiClient:         aiClient,
		characters:       characters,
		maxPanels:        maxPanels,
		maxPanelsPerPage: maxPanelsPerPage,
	}
}

// chapterScriptResponse is the schema of the AI response for chapter script generation.
// A Panel's ID / ChapterID / Page are assigned by the system, so the AI does not output them.
type chapterScriptResponse struct {
	Panels []struct {
		Shot         string                 `json:"shot"`
		Setting      string                 `json:"setting"`
		VisualAnchor string                 `json:"visual_anchor"`
		Characters   []comic.PanelCharacter `json:"characters"`
		Dialogues    []comic.DialogueLine   `json:"dialogues"`
	} `json:"panels"`
}

// GenerateChapterScript generates the panels of the given chapter with the whole outline
// as context, replaces that chapter's existing panels and returns the state (idempotent).
func (r *ChapterScriptRunner) GenerateChapterScript(ctx context.Context, state *comic.MangaState, chapterID string, opts ports.ChapterScriptOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state is nil (run GenerateOutline first)", ports.ErrInvalidRequest)
	}
	chapter := state.ChapterByID(chapterID)
	if chapter == nil {
		return nil, fmt.Errorf("%w: chapter %q", ports.ErrNotFound, chapterID)
	}

	maxPanels := r.maxPanels
	if opts.MaxPanels > 0 {
		maxPanels = opts.MaxPanels
	}

	data := &ports.ChapterPromptData{
		WorkTitle:       state.Title,
		WorkDescription: state.Description,
		OutlineDigest:   outlineDigest(state.Chapters, chapterID),
		Chapter:         *chapter,
		CharacterRoster: characterRoster(r.characters, state.Cast),
		MaxPanels:       maxPanels,
		Lang:            state.Lang,
	}
	finalPrompt, err := r.prompt.BuildChapterScript(state.ScriptMode, data)
	if err != nil {
		return nil, fmt.Errorf("%w: build chapter script prompt: %w", ports.ErrGeneration, err)
	}

	if err := requireModel(opts.Model, "chapter script"); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "ChapterScriptRunner: calling the Gemini API",
		"model", opts.Model, "chapter", chapterID)
	resp, err := r.aiClient.Generate(ctx, opts.Model, finalPrompt, nil, buildJSONGenerateOptions(chapterScriptSchema()))
	if err != nil {
		return nil, fmt.Errorf("%w: generate script for chapter %q: %w", ports.ErrGeneration, chapterID, err)
	}

	parsed, err := parseJSONResponse[chapterScriptResponse](resp.Text)
	if err != nil {
		return nil, err
	}
	if len(parsed.Panels) == 0 {
		return nil, fmt.Errorf("%w: chapter %q has no panels (the AI response has no panels)", ports.ErrGeneration, chapterID)
	}
	if len(parsed.Panels) > maxPanels {
		slog.WarnContext(ctx, "truncating panels over the limit",
			"chapter", chapterID, "got", len(parsed.Panels), "max", maxPanels)
		parsed.Panels = parsed.Panels[:maxPanels]
	}

	panels := make([]comic.Panel, 0, len(parsed.Panels))
	for i, p := range parsed.Panels {
		panels = append(panels, comic.Panel{
			ID:           fmt.Sprintf("%s-p%02d", chapterID, i+1),
			ChapterID:    chapterID,
			Shot:         p.Shot,
			Setting:      p.Setting,
			VisualAnchor: p.VisualAnchor,
			Characters:   r.normalizeCharacters(ctx, state, chapterID, p.Characters),
			Dialogues:    r.normalizeDialogues(ctx, state, chapterID, p.Dialogues),
		})
	}

	if !state.ReplaceChapterPanels(chapterID, panels) {
		return nil, fmt.Errorf("%w: chapter %q", ports.ErrNotFound, chapterID)
	}
	state.Repaginate(r.maxPanelsPerPage)
	state.UpdatedAt = time.Now().UTC()

	slog.InfoContext(ctx, "ChapterScriptRunner: generated chapter script",
		"chapter", chapterID, "panels", len(panels))
	return state, nil
}

// normalizeCharacters validates and normalizes the characters in the AI output.
// An ID missing from characters.json, or outside the work's cast, is demoted to
// background (a mob without a reference), to prevent reference resolution from silently falling back to the default
// character and drawing the wrong person. primary/secondary characters, which get
// reference images attached, are likewise demoted to background beyond
// MaxReferencedCharactersPerPanel.
func (r *ChapterScriptRunner) normalizeCharacters(ctx context.Context, state *comic.MangaState, chapterID string, chars []comic.PanelCharacter) []comic.PanelCharacter {
	result := make([]comic.PanelCharacter, 0, len(chars))
	for _, pc := range chars {
		if strings.TrimSpace(pc.CharacterID) == "" {
			continue
		}
		if pc.Prominence != comic.ProminenceBackground && !r.knownCharacter(state, pc.CharacterID) {
			slog.WarnContext(ctx, "demoting unknown character ID to background",
				"chapter", chapterID, "character_id", pc.CharacterID)
			pc.Prominence = comic.ProminenceBackground
		}
		result = append(result, pc)
	}
	return capReferencedCharacters(ctx, chapterID, result)
}

// capReferencedCharacters narrows the characters that get reference images attached
// down to MaxReferencedCharactersPerPanel and demotes the overflow to background.
// primary is kept over secondary, and within the same prominence the order of
// appearance decides (deterministic). The slice's own order is unchanged, so the
// in-panel order the AI intended is preserved.
func capReferencedCharacters(ctx context.Context, chapterID string, chars []comic.PanelCharacter) []comic.PanelCharacter {
	referenced := make([]int, 0, len(chars))
	for i := range chars {
		if chars[i].Prominence != comic.ProminenceBackground {
			referenced = append(referenced, i)
		}
	}
	if len(referenced) <= comic.MaxReferencedCharactersPerPanel {
		return chars
	}

	slices.SortStableFunc(referenced, func(a, b int) int {
		return prominenceRank(chars[a].Prominence) - prominenceRank(chars[b].Prominence)
	})
	for _, i := range referenced[comic.MaxReferencedCharactersPerPanel:] {
		slog.WarnContext(ctx, "demoting referenced character over the limit to background",
			"chapter", chapterID,
			"character_id", chars[i].CharacterID,
			"max", comic.MaxReferencedCharactersPerPanel)
		chars[i].Prominence = comic.ProminenceBackground
	}
	return chars
}

// prominenceRank returns the demotion priority (lower is kept).
// Anything other than primary (secondary, unspecified) is put after it together.
func prominenceRank(prominence string) int {
	if prominence == comic.ProminencePrimary {
		return 0
	}
	return 1
}

// normalizeDialogues validates and normalizes the dialogue in the AI output.
// Lines with empty text are removed, and a speaker ID missing from characters.json (or
// outside the work's cast) is treated as narration. A sung line (DialogueKindLyric) only
// loses the speaker: it needs none, and turning it into a caption box would change how
// it is drawn. Putting an unknown ID into the page composition prompt as a
// speaker name would get the raw ID drawn where the character's name belongs; this
// follows the same invariant as character IDs: "validate an AI-supplied ID before
// using it".
func (r *ChapterScriptRunner) normalizeDialogues(ctx context.Context, state *comic.MangaState, chapterID string, lines []comic.DialogueLine) []comic.DialogueLine {
	result := make([]comic.DialogueLine, 0, len(lines))
	for _, line := range lines {
		line.Text = strings.TrimSpace(line.Text)
		if line.Text == "" {
			continue
		}
		line.SpeakerID = strings.TrimSpace(line.SpeakerID)
		if line.SpeakerID != "" && !r.knownCharacter(state, line.SpeakerID) {
			slog.WarnContext(ctx, "dropping unknown speaker ID",
				"chapter", chapterID, "speaker_id", line.SpeakerID, "kind", line.Kind)
			line.SpeakerID = ""
			if line.Kind != comic.DialogueKindLyric {
				line.Kind = comic.DialogueKindNarration
			}
		}
		result = append(result, line)
	}
	return result
}

// knownCharacter reports whether the ID names a character this work may use: defined,
// and in the work's cast when it has one.
func (r *ChapterScriptRunner) knownCharacter(state *comic.MangaState, id string) bool {
	if !state.InCast(id) {
		return false
	}
	if r.characters == nil {
		return true // skip validation when no character definitions are configured
	}
	return r.characters.GetCharacter(id) != nil
}

// outlineDigest builds the list of the whole outline (for context), marking the target chapter.
func outlineDigest(chapters []comic.Chapter, targetID string) string {
	var sb strings.Builder
	for _, ch := range chapters {
		marker := " "
		if ch.ID == targetID {
			marker = "▶"
		}
		fmt.Fprintf(&sb, "%s %s: %s — %s\n", marker, ch.ID, ch.Title, ch.Summary)
	}
	return strings.TrimRight(sb.String(), "\n")
}
