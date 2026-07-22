package operations

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/shouni/go-gemini-client/gemini"
	"google.golang.org/genai"

	"github.com/shouni/go-comic-kit/internal/prompts"
	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type fakeContentGenerator struct {
	text       string
	err        error
	lastPrompt string
	lastModel  string
	lastOpts   gemini.GenerateOptions
}

func (f *fakeContentGenerator) GenerateWithParts(_ context.Context, model string, parts []*genai.Part, opts gemini.GenerateOptions) (*gemini.Response, error) {
	f.lastModel = model
	if len(parts) > 0 && parts[0] != nil {
		f.lastPrompt = parts[0].Text
	}
	f.lastOpts = opts
	if f.err != nil {
		return nil, f.err
	}
	return &gemini.Response{Text: f.text}, nil
}

type fakeReader struct {
	content string
}

func (f *fakeReader) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.content)), nil
}

const outlineJSON = `{
  "title": "夜明けのデプロイ",
  "description": "新米エンジニアの成長物語",
  "chapters": [
    {"id": "weird-id", "title": "導入", "summary": "つかみ", "source_excerpt": "抜粋1"},
    {"title": "核心", "summary": "本題", "source_excerpt": "抜粋2"}
  ]
}`

func newOutlineRunner(t *testing.T, ai StructuredGenerator, reader ports.ContentReader) *OutlineRunner {
	t.Helper()
	p, err := prompts.NewScriptPrompts()
	if err != nil {
		t.Fatalf("NewScriptPrompts failed: %v", err)
	}
	return NewOutlineRunner(p, ai, reader, nil, "test-model", 0)
}

// --- Tests ---

func TestGenerateOutlineFromSourceText(t *testing.T) {
	t.Parallel()

	ai := &fakeContentGenerator{text: "```json\n" + outlineJSON + "\n```"}
	r := newOutlineRunner(t, ai, nil)

	state, err := r.GenerateOutline(context.Background(), ports.OutlineRequest{
		SourceText: "元文章のテキスト",
		Mode:       "",
		StyleMode:  "mecha",
	})
	if err != nil {
		t.Fatalf("GenerateOutline failed: %v", err)
	}

	if state.Title != "夜明けのデプロイ" || state.Description == "" {
		t.Errorf("state title/description = %q/%q, want parsed values", state.Title, state.Description)
	}
	if state.Version != ports.StateSchemaVersion || state.StyleMode != "mecha" {
		t.Errorf("Version/StyleMode = %d/%q, want %d/mecha", state.Version, state.StyleMode, ports.StateSchemaVersion)
	}
	if len(state.Chapters) != 2 {
		t.Fatalf("Chapters = %+v, want 2", state.Chapters)
	}
	// AI出力のIDは信用せず ch01 形式で採番し直す
	if state.Chapters[0].ID != "ch01" || state.Chapters[1].ID != "ch02" {
		t.Errorf("chapter IDs = %q, %q, want ch01, ch02", state.Chapters[0].ID, state.Chapters[1].ID)
	}
	if state.Chapters[1].SourceExcerpt != "抜粋2" {
		t.Errorf("SourceExcerpt = %q, want 抜粋2", state.Chapters[1].SourceExcerpt)
	}
	if !strings.Contains(ai.lastPrompt, "元文章のテキスト") {
		t.Error("prompt does not contain source text")
	}
	if state.CreatedAt.IsZero() || state.UpdatedAt.IsZero() {
		t.Error("CreatedAt/UpdatedAt must be set")
	}
	// 構造化出力オプションの検証
	if ai.lastOpts.ResponseMIMEType != "application/json" || ai.lastOpts.ResponseSchema == nil {
		t.Errorf("opts = %+v, want application/json with ResponseSchema", ai.lastOpts)
	}
	// セーフティブロックによる生成失敗を抑えるため BlockNone を指定する
	if len(ai.lastOpts.SafetySettings) != 4 {
		t.Errorf("SafetySettings = %+v, want 4 categories with BlockNone", ai.lastOpts.SafetySettings)
	}
	for _, s := range ai.lastOpts.SafetySettings {
		if s.Threshold != genai.HarmBlockThresholdBlockNone {
			t.Errorf("SafetySettings[%s] = %v, want BlockNone", s.Category, s.Threshold)
		}
	}
}

func TestGenerateOutlineFromSourceURL(t *testing.T) {
	t.Parallel()

	ai := &fakeContentGenerator{text: outlineJSON}
	reader := &fakeReader{content: "URLから読んだ本文"}
	r := newOutlineRunner(t, ai, reader)

	_, err := r.GenerateOutline(context.Background(), ports.OutlineRequest{SourceURL: "gs://bucket/article.md"})
	if err != nil {
		t.Fatalf("GenerateOutline failed: %v", err)
	}
	if !strings.Contains(ai.lastPrompt, "URLから読んだ本文") {
		t.Error("prompt does not contain content read from URL")
	}
}

func TestGenerateOutlineRequiresSource(t *testing.T) {
	t.Parallel()

	r := newOutlineRunner(t, &fakeContentGenerator{text: outlineJSON}, nil)
	if _, err := r.GenerateOutline(context.Background(), ports.OutlineRequest{}); err == nil {
		t.Error("GenerateOutline without source succeeded, want error")
	}
}

func TestGenerateOutlineClampsChapterCount(t *testing.T) {
	t.Parallel()

	var sb strings.Builder
	sb.WriteString(`{"title":"t","description":"d","chapters":[`)
	for i := range 12 {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"title":"c%d","summary":"s","source_excerpt":"e"}`, i)
	}
	sb.WriteString("]}")

	ai := &fakeContentGenerator{text: sb.String()}
	r := newOutlineRunner(t, ai, nil)

	state, err := r.GenerateOutline(context.Background(), ports.OutlineRequest{
		SourceText:  "text",
		MaxChapters: 3,
	})
	if err != nil {
		t.Fatalf("GenerateOutline failed: %v", err)
	}
	if len(state.Chapters) != 3 {
		t.Errorf("Chapters = %d, want clamped to 3", len(state.Chapters))
	}
}

func TestGenerateOutlineEmptyChaptersFails(t *testing.T) {
	t.Parallel()

	ai := &fakeContentGenerator{text: `{"title":"t","description":"d","chapters":[]}`}
	r := newOutlineRunner(t, ai, nil)

	if _, err := r.GenerateOutline(context.Background(), ports.OutlineRequest{SourceText: "text"}); err == nil {
		t.Error("GenerateOutline with empty chapters succeeded, want error")
	}
}
