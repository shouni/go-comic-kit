package workflow

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type fakeAIClient struct{}

func (f *fakeAIClient) GenerateContent(_ context.Context, _, _ string) (*gemini.Response, error) {
	return &gemini.Response{Text: "{}"}, nil
}

func (f *fakeAIClient) GenerateWithAttachments(_ context.Context, _ string, _ string, _ []gemini.Attachment, _ gemini.GenerateOptions) (*gemini.Response, error) {
	return &gemini.Response{Text: "{}"}, nil
}

func (f *fakeAIClient) IsVertexAI() bool { return true }

func (f *fakeAIClient) UploadFile(_ context.Context, _ io.Reader, _, _ string) (gemini.UploadedFile, error) {
	return gemini.UploadedFile{URI: "file-uri", Name: "file-name"}, nil
}

func (f *fakeAIClient) DeleteFile(_ context.Context, _ string) error { return nil }

type fakeWorkflowReader struct{}

func (f *fakeWorkflowReader) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("content")), nil
}

type fakeWorkflowWriter struct{}

func (f *fakeWorkflowWriter) Write(_ context.Context, _ string, _ io.Reader, _ ...remoteio.WriteOption) error {
	return nil
}

// --- Helpers ---

func validArgs(t *testing.T) Args {
	t.Helper()
	cm, err := characterkit.NewCharacters([]comic.Character{
		{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://b/z.png", VisualCues: []string{"green hair"}, IsDefault: true},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	return Args{
		// モデル名と画風指定はキットが既定値を持たないため、呼び出し側が必ず指定する。
		Config:     ports.Config{},
		HTTPClient: httpkit.New(5 * time.Second),
		Reader:     &fakeWorkflowReader{},
		Writer:     &fakeWorkflowWriter{},
		AIClient:   &fakeAIClient{},
		Characters: cm,

		OutlinePrompt:       stubPrompts{},
		ChapterScriptPrompt: stubPrompts{},
		DesignSheetPrompt:   stubPrompts{},
		PanelPrompt:         stubPrompts{},
		PagePrompt:          stubPrompts{},
	}
}

// --- Tests ---

func TestNewBuildsAllOperations(t *testing.T) {
	t.Parallel()

	ops, err := New(validArgs(t))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer func() { _ = ops.Close() }()

	if ops.Outline == nil || ops.ChapterScript == nil || ops.DesignSheet == nil || ops.Panel == nil || ops.Page == nil {
		t.Errorf("Operations = %+v, want all operations wired", ops)
	}
}

func TestNewValidatesRequiredArgs(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*Args){
		"HTTPClient": func(a *Args) { a.HTTPClient = nil },
		"Reader":     func(a *Args) { a.Reader = nil },
		"Writer":     func(a *Args) { a.Writer = nil },
		"AIClient":   func(a *Args) { a.AIClient = nil },
		"Characters": func(a *Args) { a.Characters = nil },
		// プロンプトは5つとも必須。1つでも nil なら構築に失敗すること。
		"OutlinePrompt":       func(a *Args) { a.OutlinePrompt = nil },
		"ChapterScriptPrompt": func(a *Args) { a.ChapterScriptPrompt = nil },
		"DesignSheetPrompt":   func(a *Args) { a.DesignSheetPrompt = nil },
		"PanelPrompt":         func(a *Args) { a.PanelPrompt = nil },
		"PagePrompt":          func(a *Args) { a.PagePrompt = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			args := validArgs(t)
			mutate(&args)
			if _, err := New(args); err == nil {
				t.Errorf("New without %s succeeded, want error", name)
			}
		})
	}
}

func TestOperationsCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	ops, err := New(validArgs(t))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := ops.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := ops.Close(); err != nil { // 二重 Close で panic せず、エラーにもならないこと
		t.Fatalf("2回目の Close() error = %v", err)
	}

	var nilOps *ports.Operations
	if err := nilOps.Close(); err != nil { // nil レシーバでも panic しないこと
		t.Fatalf("nil レシーバの Close() error = %v", err)
	}
}
