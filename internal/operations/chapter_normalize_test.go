package operations

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
)

// A response for checking the normalization of speaker IDs and the referenced character count.
// - unknown-speaker is a speaker missing from characters.json
// - lines with empty text are removed
// - primary/secondary exceed MaxReferencedCharactersPerPanel
const normalizeChapterJSON = `{
  "panels": [
    {
      "visual_anchor": "unknown speaker",
      "characters": [{"character_id": "zundamon", "prominence": "primary"}],
      "dialogues": [
        {"speaker_id": "unknown-speaker", "text": "誰なのだ", "kind": "speech"},
        {"speaker_id": "  metan  ", "text": "  前後に空白  ", "kind": "speech"},
        {"speaker_id": "zundamon", "text": "   ", "kind": "speech"}
      ]
    },
    {
      "visual_anchor": "too many referenced characters",
      "characters": [
        {"character_id": "metan", "prominence": "secondary"},
        {"character_id": "zundamon", "prominence": "primary"},
        {"character_id": "tsumugi", "prominence": "secondary"},
        {"character_id": "hau", "prominence": "secondary"},
        {"character_id": "ritsu", "prominence": "primary"}
      ],
      "dialogues": [{"text": "群像劇", "kind": "narration"}]
    }
  ]
}`

// newChapterRunnerWithCast returns a ChapterScriptRunner with every given character
// defined in characters.json. Checking the cap needs four or more known characters, so it
// exists alongside newChapterRunner, which registers only two.
func newChapterRunnerWithCast(t *testing.T, ai *fakeContentGenerator, ids ...string) *ChapterScriptRunner {
	t.Helper()
	p := &fakeScriptPrompt{}
	cast := make([]comic.Character, 0, len(ids))
	for i, id := range ids {
		cast = append(cast, comic.Character{
			ID:           id,
			Name:         id,
			ReferenceURL: "gs://b/" + id + ".png",
			VisualCues:   []string{id + " hair"},
			IsDefault:    i == 0,
		})
	}
	cm, err := characterkit.NewCharacters(cast)
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	return NewChapterScriptRunner(p, ai, cm, 0, 0)
}

func TestGenerateChapterScriptNormalizesDialogues(t *testing.T) {
	t.Parallel()

	r, _ := newChapterRunner(t, &fakeContentGenerator{text: normalizeChapterJSON})
	state, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01", ports.ChapterScriptOptions{Model: "test-model"})
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	lines := state.Panels[0].Dialogues
	if len(lines) != 2 {
		t.Fatalf("Dialogues = %+v, want 2 (lines with empty text are removed)", lines)
	}

	// An unknown speaker ID becomes narration, so the raw ID is not drawn as a speaker name
	if lines[0].SpeakerID != "" {
		t.Errorf("unknown speaker SpeakerID = %q, want empty", lines[0].SpeakerID)
	}
	if lines[0].Kind != comic.DialogueKindNarration {
		t.Errorf("unknown speaker Kind = %q, want %q", lines[0].Kind, comic.DialogueKindNarration)
	}

	// A known speaker is kept as is, with surrounding whitespace trimmed
	if lines[1].SpeakerID != "metan" {
		t.Errorf("SpeakerID = %q, want %q", lines[1].SpeakerID, "metan")
	}
	if lines[1].Text != "前後に空白" {
		t.Errorf("Text = %q, want trimmed", lines[1].Text)
	}
	if lines[1].Kind != comic.DialogueKindSpeech {
		t.Errorf("known speaker Kind = %q, want %q", lines[1].Kind, comic.DialogueKindSpeech)
	}
}

func TestGenerateChapterScriptCapsReferencedCharacters(t *testing.T) {
	t.Parallel()

	r := newChapterRunnerWithCast(t, &fakeContentGenerator{text: normalizeChapterJSON},
		"zundamon", "metan", "tsumugi", "hau", "ritsu")
	state, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01", ports.ChapterScriptOptions{Model: "test-model"})
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	panel := state.Panels[1]
	if got := len(panel.ReferencedCharacterIDs()); got != comic.MaxReferencedCharactersPerPanel {
		t.Fatalf("referenced characters = %d, want %d", got, comic.MaxReferencedCharactersPerPanel)
	}

	// The order of appearance is kept (the in-panel order is what the AI intended)
	order := make([]string, len(panel.Characters))
	for i, pc := range panel.Characters {
		order[i] = pc.CharacterID
	}
	want := []string{"metan", "zundamon", "tsumugi", "hau", "ritsu"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("Characters order = %v, want %v", order, want)
		}
	}

	// primary is kept first, and the overflowing secondary drops to background.
	// (zundamon/ritsu are known primaries; metan is a known secondary that appears first)
	prominence := make(map[string]string, len(panel.Characters))
	for _, pc := range panel.Characters {
		prominence[pc.CharacterID] = pc.Prominence
	}
	for _, id := range []string{"zundamon", "ritsu"} {
		if prominence[id] == comic.ProminenceBackground {
			t.Errorf("%s was demoted to background (primary should be kept first)", id)
		}
	}
	for _, id := range []string{"tsumugi", "hau"} {
		if prominence[id] != comic.ProminenceBackground {
			t.Errorf("%s = %q, want background (over the limit)", id, prominence[id])
		}
	}
}

func TestOperationErrorsAreClassifiable(t *testing.T) {
	t.Parallel()

	r, _ := newChapterRunner(t, &fakeContentGenerator{text: normalizeChapterJSON})
	ctx := context.Background()

	if _, err := r.GenerateChapterScript(ctx, outlineState(), "ch99", ports.ChapterScriptOptions{Model: "test-model"}); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("unknown chapter error = %v, want ports.ErrNotFound", err)
	}
	if _, err := r.GenerateChapterScript(ctx, nil, "ch01", ports.ChapterScriptOptions{Model: "test-model"}); !errors.Is(err, ports.ErrInvalidRequest) {
		t.Errorf("state=nil error = %v, want ports.ErrInvalidRequest", err)
	}

	empty, _ := newChapterRunner(t, &fakeContentGenerator{text: `{"panels":[]}`})
	if _, err := empty.GenerateChapterScript(ctx, outlineState(), "ch01", ports.ChapterScriptOptions{Model: "test-model"}); !errors.Is(err, ports.ErrGeneration) {
		t.Errorf("empty response error = %v, want ports.ErrGeneration", err)
	}
}
