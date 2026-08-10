package operations

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
)

// 話者IDと参照キャラクター数の正規化を検証するための応答。
// - unknown-speaker は characters.json に無い話者
// - 空テキストの行は取り除かれる
// - primary/secondary が MaxReferencedCharactersPerPanel を超える
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

// newChapterRunnerWithCast は、指定した全キャラクターが characters.json に定義済みの
// ChapterScriptRunner を返します。上限の検証には既知キャラクターが4体以上必要なため、
// 2体しか登録しない newChapterRunner とは別に用意しています。
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
	return NewChapterScriptRunner(p, ai, cm, "test-model", 0, 0)
}

func TestGenerateChapterScriptNormalizesDialogues(t *testing.T) {
	t.Parallel()

	r := newChapterRunner(t, &fakeContentGenerator{text: normalizeChapterJSON})
	state, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01")
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	lines := state.Panels[0].Dialogues
	if len(lines) != 2 {
		t.Fatalf("Dialogues = %+v, want 2（空テキストの行は取り除かれる）", lines)
	}

	// 未定義の話者IDは、生のIDが話者名として描かれないようナレーションに落とす
	if lines[0].SpeakerID != "" {
		t.Errorf("未定義話者の SpeakerID = %q, want 空", lines[0].SpeakerID)
	}
	if lines[0].Kind != comic.DialogueKindNarration {
		t.Errorf("未定義話者の Kind = %q, want %q", lines[0].Kind, comic.DialogueKindNarration)
	}

	// 既知の話者は前後の空白を落としたうえでそのまま残す
	if lines[1].SpeakerID != "metan" {
		t.Errorf("SpeakerID = %q, want %q", lines[1].SpeakerID, "metan")
	}
	if lines[1].Text != "前後に空白" {
		t.Errorf("Text = %q, want trim 済み", lines[1].Text)
	}
	if lines[1].Kind != comic.DialogueKindSpeech {
		t.Errorf("既知話者の Kind = %q, want %q", lines[1].Kind, comic.DialogueKindSpeech)
	}
}

func TestGenerateChapterScriptCapsReferencedCharacters(t *testing.T) {
	t.Parallel()

	r := newChapterRunnerWithCast(t, &fakeContentGenerator{text: normalizeChapterJSON},
		"zundamon", "metan", "tsumugi", "hau", "ritsu")
	state, err := r.GenerateChapterScript(context.Background(), outlineState(), "ch01")
	if err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	panel := state.Panels[1]
	if got := len(panel.ReferencedCharacterIDs()); got != comic.MaxReferencedCharactersPerPanel {
		t.Fatalf("参照キャラクター数 = %d, want %d", got, comic.MaxReferencedCharactersPerPanel)
	}

	// 登場順は保たれる（コマ内の並びは AI の意図どおり）
	order := make([]string, len(panel.Characters))
	for i, pc := range panel.Characters {
		order[i] = pc.CharacterID
	}
	want := []string{"metan", "zundamon", "tsumugi", "hau", "ritsu"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("Characters の並び = %v, want %v", order, want)
		}
	}

	// primary を優先して残し、あふれた secondary を background に落とす。
	// （zundamon/ritsu は既知の primary、metan は既知の secondary で最初に登場）
	prominence := make(map[string]string, len(panel.Characters))
	for _, pc := range panel.Characters {
		prominence[pc.CharacterID] = pc.Prominence
	}
	for _, id := range []string{"zundamon", "ritsu"} {
		if prominence[id] == comic.ProminenceBackground {
			t.Errorf("%s が background に降格している（primary は優先して残すはず）", id)
		}
	}
	for _, id := range []string{"tsumugi", "hau"} {
		if prominence[id] != comic.ProminenceBackground {
			t.Errorf("%s = %q, want background（上限を超えた分）", id, prominence[id])
		}
	}
}

func TestOperationErrorsAreClassifiable(t *testing.T) {
	t.Parallel()

	r := newChapterRunner(t, &fakeContentGenerator{text: normalizeChapterJSON})
	ctx := context.Background()

	if _, err := r.GenerateChapterScript(ctx, outlineState(), "ch99"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("未知の章のエラー = %v, want ports.ErrNotFound", err)
	}
	if _, err := r.GenerateChapterScript(ctx, nil, "ch01"); !errors.Is(err, ports.ErrInvalidRequest) {
		t.Errorf("state=nil のエラー = %v, want ports.ErrInvalidRequest", err)
	}

	empty := newChapterRunner(t, &fakeContentGenerator{text: `{"panels":[]}`})
	if _, err := empty.GenerateChapterScript(ctx, outlineState(), "ch01"); !errors.Is(err, ports.ErrGeneration) {
		t.Errorf("空応答のエラー = %v, want ports.ErrGeneration", err)
	}
}
