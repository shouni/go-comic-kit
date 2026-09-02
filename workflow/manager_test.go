package workflow

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	imagePorts "github.com/shouni/gemini-image-kit/ports"

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
		HTTPClient: httpkit.New(httpkit.WithTimeout(5 * time.Second)),
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

// geminiAPIClient / vertexUploadCounter は、バックエンド判定を変えつつ
// File API へのアップロード回数を数える AI クライアントです。
type geminiAPIClient struct {
	fakeAIClient
	uploads int
}

func (f *geminiAPIClient) IsVertexAI() bool { return false }

func (f *geminiAPIClient) UploadFile(ctx context.Context, r io.Reader, mimeType, name string) (gemini.UploadedFile, error) {
	f.uploads++
	return f.fakeAIClient.UploadFile(ctx, r, mimeType, name)
}

type vertexUploadCounter struct{ geminiAPIClient }

func (f *vertexUploadCounter) IsVertexAI() bool { return true }

// TestBuildReferenceResolverKeepsGCSURIOnVertex は、Vertex AI では参照画像を
// 転送せず gs:// のまま渡すことを確認します。モデル側が gs:// を解決できるため、
// アップロードもキャッシュも不要という経路の前提そのものです。
func TestBuildReferenceResolverKeepsGCSURIOnVertex(t *testing.T) {
	t.Parallel()

	args := validArgs(t)
	client := &vertexUploadCounter{}
	resolver, err := buildReferenceResolver(&args, client, time.Minute)
	if err != nil {
		t.Fatalf("buildReferenceResolver() error = %v", err)
	}

	attachment, err := resolver.Resolve(t.Context(), imagePorts.ImageURI{ReferenceURL: "gs://b/z.png"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if attachment.URI != "gs://b/z.png" {
		t.Errorf("URI = %q, want the gs:// URI passed through untouched", attachment.URI)
	}
	if client.uploads != 0 {
		t.Errorf("uploads = %d, want 0（Vertex AI では転送しない）", client.uploads)
	}
}

// TestBuildReferenceResolverUploadsAndReusesOnGeminiAPI は、Gemini API バックエンドでは
// 参照画像を File API へ上げ、2 回目以降はキャッシュから使い回すことを確認します。
// アップロードが毎回走ると、同じ画像を何度も転送したうえに File API の枠を食い潰します。
func TestBuildReferenceResolverUploadsAndReusesOnGeminiAPI(t *testing.T) {
	t.Parallel()

	args := validArgs(t)
	args.Reader = &fakePNGReader{}
	client := &geminiAPIClient{}
	resolver, err := buildReferenceResolver(&args, client, time.Minute)
	if err != nil {
		t.Fatalf("buildReferenceResolver() error = %v", err)
	}

	for i := range 2 {
		attachment, err := resolver.Resolve(t.Context(), imagePorts.ImageURI{ReferenceURL: "gs://b/z.png"})
		if err != nil {
			t.Fatalf("Resolve() #%d error = %v", i+1, err)
		}
		if attachment.URI != "file-uri" {
			t.Errorf("Resolve() #%d URI = %q, want the File API URI", i+1, attachment.URI)
		}
	}
	if client.uploads != 1 {
		t.Errorf("uploads = %d, want 1（2回目はキャッシュから使い回すこと）", client.uploads)
	}
}

// fakePNGReader は、参照画像として実際にデコードできる PNG を返します。
// File API 経路は取得したバイト列から MIME type を判定するため、中身が要ります。
type fakePNGReader struct{}

func (f *fakePNGReader) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		return nil, err
	}
	return io.NopCloser(&buf), nil
}
