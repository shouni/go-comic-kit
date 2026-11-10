package operations

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/shouni/go-comic-kit/comic"
	"github.com/shouni/go-comic-kit/ports"

	characterkit "github.com/shouni/go-character-kit/character"
)

// This file pins the invariant that "the library's logs carry the context".
//
// The app installs go-utils/slogctx's handler in the default logger and puts job_id on
// the request context. Using slog.Info here (the variant without a context) makes slog
// pass context.Background() to the handler, so that line alone loses its job_id. The
// lines lost are the generation logs themselves — the ones you most want when filtering
// by job_id. This passes as long as the calls are the Context variants, and fails if
// even one reverts.

// correlationKey is the marker the test handler picks up. Importing slogctx would add a
// dependency edge on go-utils, so the same mechanism (take an attribute from the context
// and add it to the record) is reproduced minimally.
type correlationKey struct{}

// capturingHandler collects only the messages of records emitted from a context carrying
// the marker. Records without it are dropped, so logs from other tests running in
// parallel do not mix in.
type capturingHandler struct {
	mu       *sync.Mutex
	messages *[]string
}

func (h capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h capturingHandler) Handle(ctx context.Context, record slog.Record) error {
	if ctx.Value(correlationKey{}) == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.messages = append(*h.messages, record.Message)
	return nil
}

func (h capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h capturingHandler) WithGroup(string) slog.Handler      { return h }

// captureCorrelatedLogs swaps the default logger and hands back a function returning the collected messages.
func captureCorrelatedLogs(t *testing.T) func() []string {
	t.Helper()

	var mu sync.Mutex
	var messages []string

	previous := slog.Default()
	slog.SetDefault(slog.New(capturingHandler{mu: &mu, messages: &messages}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(messages)
	}
}

// correlationChapterJSON is a response that exercises every log path of chapter script
// generation at once. It has 2 panels (truncated against a limit of 1), and the first
// contains 1 unknown character, 4 referenced characters (demoted against a limit of 3)
// and 1 unknown speaker.
const correlationChapterJSON = `{
  "panels": [
    {
      "shot": "wide",
      "visual_anchor": "no text",
      "characters": [
        {"character_id": "c1", "prominence": "primary"},
        {"character_id": "c2", "prominence": "secondary"},
        {"character_id": "c3", "prominence": "secondary"},
        {"character_id": "c4", "prominence": "secondary"},
        {"character_id": "ghost", "prominence": "secondary"}
      ],
      "dialogues": [
        {"speaker_id": "phantom", "text": "誰なのだ"}
      ]
    },
    {
      "shot": "close-up",
      "visual_anchor": "no text",
      "characters": [{"character_id": "c1", "prominence": "primary"}],
      "dialogues": []
    }
  ]
}`

func TestChapterScriptLogsCarryCallerContext(t *testing.T) {
	// This test swaps the default logger, so it does not run in parallel.
	logged := captureCorrelatedLogs(t)

	cm, err := characterkit.NewCharacters([]comic.Character{
		{ID: "c1", Name: "一号", ReferenceURL: "gs://b/1.png", VisualCues: []string{"cue1"}, IsDefault: true},
		{ID: "c2", Name: "二号", ReferenceURL: "gs://b/2.png", VisualCues: []string{"cue2"}},
		{ID: "c3", Name: "三号", ReferenceURL: "gs://b/3.png", VisualCues: []string{"cue3"}},
		{ID: "c4", Name: "四号", ReferenceURL: "gs://b/4.png", VisualCues: []string{"cue4"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}

	ai := &fakeContentGenerator{text: correlationChapterJSON}
	runner := NewChapterScriptRunner(&fakeScriptPrompt{}, ai, cm, 1, 0)

	ctx := context.WithValue(context.Background(), correlationKey{}, "job-1")
	state := outlineState()
	if _, err := runner.GenerateChapterScript(ctx, state, "ch01", ports.ChapterScriptOptions{Model: "text-model"}); err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	// Messages that arrive only through the caller's context.
	// The last 3 come from helpers that need ctx passed in as an argument, which makes them prone to regressing.
	want := []string{
		"ChapterScriptRunner: calling the Gemini API",
		"ChapterScriptRunner: generated chapter script",
		"truncating panels over the limit",
		"demoting unknown character ID to background",
		"demoting referenced character over the limit to background",
		"converting unknown speaker ID to narration",
	}

	got := logged()
	for _, message := range want {
		if !slices.Contains(got, message) {
			t.Errorf("%q does not carry the caller's context; use the Context variants instead of slog.Info/Warn. Received: %q", message, got)
		}
	}
}
