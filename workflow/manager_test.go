package workflow

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"google.golang.org/genai"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type fakeAIClient struct{}

func (f *fakeAIClient) GenerateContent(_ context.Context, _, _ string) (*gemini.Response, error) {
	return &gemini.Response{Text: "{}"}, nil
}

func (f *fakeAIClient) GenerateWithParts(_ context.Context, _ string, _ []*genai.Part, _ gemini.GenerateOptions) (*gemini.Response, error) {
	return &gemini.Response{Text: "{}"}, nil
}

func (f *fakeAIClient) IsVertexAI() bool { return true }

func (f *fakeAIClient) UploadFile(_ context.Context, _ io.Reader, _, _ string) (string, string, error) {
	return "file-uri", "file-name", nil
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
	cm, err := characterkit.NewCharacters([]ports.Character{
		{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://b/z.png", VisualCues: []string{"green hair"}, IsDefault: true},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	return Args{
		Config:     ports.Config{},
		HTTPClient: httpkit.New(5 * time.Second),
		Reader:     &fakeWorkflowReader{},
		Writer:     &fakeWorkflowWriter{},
		AIClient:   &fakeAIClient{},
		Characters: cm,
	}
}

// --- Tests ---

func TestNewBuildsAllOperations(t *testing.T) {
	t.Parallel()

	ops, err := New(validArgs(t))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer ops.Close()

	if ops.Outline == nil || ops.ChapterScript == nil || ops.DesignSheet == nil || ops.Panel == nil || ops.Page == nil {
		t.Errorf("Operations = %+v, want all operations wired", ops)
	}
	if ops.CloseFunc == nil {
		t.Error("CloseFunc not set")
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
	ops.Close()
	ops.Close() // 二重 Close で panic しないこと

	var nilOps *ports.Operations
	nilOps.Close() // nil レシーバでも panic しないこと
}
