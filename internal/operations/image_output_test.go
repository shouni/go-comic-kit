package operations

import (
	"context"
	"errors"
	"testing"

	"github.com/shouni/go-comic-kit/ports"
)

// TestRenderImageClassifiesFailures checks that failures other than generation (saving,
// the save path) also carry a ports sentinel error. Returned bare, the consumer can no
// longer map them to an HTTP status with errors.Is (the rule in ports/errors.go).
func TestRenderImageClassifiesFailures(t *testing.T) {
	t.Parallel()

	okPath := func(string) (string, error) { return "images/out.png", nil }

	tests := []struct {
		name    string
		gen     ImageGenerator
		writer  *mockWriter
		pathFor func(string) (string, error)
		want    error
	}{
		{
			name:    "generation failure is ErrGeneration",
			gen:     &failingImageGenerator{err: errors.New("upstream 503")},
			writer:  &mockWriter{},
			pathFor: okPath,
			want:    ports.ErrGeneration,
		},
		{
			name:    "save path failure is ErrInvalidRequest",
			gen:     &mockImageGenerator{},
			writer:  &mockWriter{},
			pathFor: func(string) (string, error) { return "", errors.New("bad output dir") },
			want:    ports.ErrInvalidRequest,
		},
		{
			name:    "save failure is ErrGeneration",
			gen:     &mockImageGenerator{},
			writer:  &mockWriter{err: errors.New("gcs unavailable")},
			pathFor: okPath,
			want:    ports.ErrGeneration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := renderImage(context.Background(), tt.gen, tt.writer, imageRenderRequest{
				Model:   "test-model",
				Prompt:  "prompt",
				PathFor: tt.pathFor,
			})
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want it to wrap %v", err, tt.want)
			}
		})
	}
}

// TestRenderImageRecordsGenerationConditions checks that the conditions needed for
// regeneration are recorded (GenerationRecord is the basis of regeneration).
func TestRenderImageRecordsGenerationConditions(t *testing.T) {
	t.Parallel()

	gen := &mockImageGenerator{}
	writer := &mockWriter{}
	record, err := renderImage(context.Background(), gen, writer, imageRenderRequest{
		Model:          "panel-model",
		Prompt:         "a cat",
		NegativePrompt: "blurry",
		PathFor:        func(string) (string, error) { return "images/out.png", nil },
	})
	if err != nil {
		t.Fatalf("renderImage() error = %v", err)
	}
	if record.Model != "panel-model" || record.Prompt != "a cat" || record.NegativePrompt != "blurry" {
		t.Errorf("record = %+v, want the request conditions recorded", record)
	}
	if record.ImageURL != "images/out.png" {
		t.Errorf("ImageURL = %q, want the saved path", record.ImageURL)
	}
	if record.UsedSeed != 555 {
		t.Errorf("UsedSeed = %d, want the seed reported by the generator", record.UsedSeed)
	}
	if record.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero")
	}
}

type failingImageGenerator struct {
	err error
}

func (f *failingImageGenerator) Generate(context.Context, ImageRequest) (*ImageResponse, error) {
	return nil, f.err
}

// TestRenderImageCacheControl checks that the Cache-Control used when saving can be
// replaced by configuration and falls back to the default when unset. "public" is not
// the library's call to make; whether artifacts may be served publicly is the
// deployment's decision.
func TestRenderImageCacheControl(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct{ given, want string }{
		"unset uses the default":     {"", ports.DefaultCacheControl},
		"explicit value is honoured": {"private, max-age=0", "private, max-age=0"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			writer := &mockWriter{}
			_, err := renderImage(context.Background(), &mockImageGenerator{}, writer, imageRenderRequest{
				Model:        "test-model",
				Prompt:       "prompt",
				CacheControl: tt.given,
				PathFor:      func(string) (string, error) { return "images/out.png", nil },
			})
			if err != nil {
				t.Fatalf("renderImage failed: %v", err)
			}
			if writer.lastOptions.CacheControl != tt.want {
				t.Errorf("CacheControl = %q, want %q", writer.lastOptions.CacheControl, tt.want)
			}
		})
	}
}
