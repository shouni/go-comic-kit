package operations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/ports"
)

// This file pins that "a chapter script is not lost when quoted text contains raw newlines".
//
// Even with structured output (ResponseSchema), the model puts raw newlines inside JSON
// strings when quoting multi-line dialogue or scene descriptions. The breakage happens
// after the response is complete, so an API retry does not fix it, and without repair the
// whole chapter fails with ErrGeneration. The repair lives in genai-kit's
// gemini.CleanJSONResponse.

// chapterJSONWithRawNewline is a chapter script response with raw newlines in the
// dialogue and the scene description. It uses an ordinary string literal rather than
// backquotes so that \n goes into the payload as an actual newline, not an escape.
const chapterJSONWithRawNewline = "{\n" +
	"  \"panels\": [\n" +
	"    {\n" +
	"      \"shot\": \"wide\",\n" +
	"      \"setting\": \"放課後の音楽室\",\n" +
	"      \"visual_anchor\": \"夕暮れの教室\n窓辺に人影\",\n" +
	"      \"characters\": [{\"character_id\": \"zundamon\", \"prominence\": \"primary\"}],\n" +
	"      \"dialogues\": [{\"speaker_id\": \"zundamon\", \"text\": \"待って。\nまだ終わってないのだ\", \"kind\": \"speech\"}]\n" +
	"    }\n" +
	"  ]\n" +
	"}"

func TestGenerateChapterScriptSurvivesRawNewlinesInQuotedText(t *testing.T) {
	t.Parallel()

	// Pin the premise: this payload cannot be read as JSON as is.
	// If this passed, everything after would succeed without the repair and test nothing.
	if json.Valid([]byte(chapterJSONWithRawNewline)) {
		t.Fatal("the test payload is valid JSON; check that the raw newlines have not been lost")
	}

	ai := &fakeContentGenerator{text: chapterJSONWithRawNewline}
	runner, _ := newChapterRunner(t, ai)

	state, err := runner.GenerateChapterScript(context.Background(), outlineState(), "ch01",
		ports.ChapterScriptOptions{Model: "text-model"})
	if err != nil {
		t.Fatalf("a raw newline in quoted text lost the chapter script: %v", err)
	}

	panels := state.PanelsForChapter("ch01")
	if len(panels) != 1 {
		t.Fatalf("PanelsForChapter() = %d, want 1", len(panels))
	}

	// The newline stays in the text, escaped (not removed).
	if !strings.Contains(panels[0].VisualAnchor, "\n") {
		t.Errorf("VisualAnchor lost its newline: %q", panels[0].VisualAnchor)
	}
	if len(panels[0].Dialogues) != 1 || !strings.Contains(panels[0].Dialogues[0].Text, "\n") {
		t.Errorf("the dialogue lost its newline: %+v", panels[0].Dialogues)
	}
}
