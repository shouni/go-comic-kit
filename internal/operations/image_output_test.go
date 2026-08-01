package operations

import (
	"context"
	"errors"
	"testing"

	imagePorts "github.com/shouni/gemini-image-kit/ports"

	"github.com/shouni/go-comic-kit/ports"
)

// TestRenderImageClassifiesFailures は、生成以外の失敗（保存・保存先パス）にも
// ports の番兵エラーが付くことを確認します。裸のエラーで返すと、消費側が errors.Is で
// HTTP ステータスへ振り分けられなくなります（ports/errors.go の規約）。
func TestRenderImageClassifiesFailures(t *testing.T) {
	t.Parallel()

	okPath := func(string) (string, error) { return "images/out.png", nil }

	tests := []struct {
		name    string
		gen     ImageFusionGenerator
		writer  *mockWriter
		pathFor func(string) (string, error)
		want    error
	}{
		{
			name:    "生成の失敗は ErrGeneration",
			gen:     &failingFusionGenerator{err: errors.New("upstream 503")},
			writer:  &mockWriter{},
			pathFor: okPath,
			want:    ports.ErrGeneration,
		},
		{
			name:    "保存先パスの失敗は ErrInvalidRequest",
			gen:     &mockFusionGenerator{},
			writer:  &mockWriter{},
			pathFor: func(string) (string, error) { return "", errors.New("bad output dir") },
			want:    ports.ErrInvalidRequest,
		},
		{
			name:    "保存の失敗は ErrGeneration",
			gen:     &mockFusionGenerator{},
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

// TestRenderImageRecordsGenerationConditions は、再生成に必要な条件が記録されることを
// 確認します（GenerationRecord は再生成の基礎です）。
func TestRenderImageRecordsGenerationConditions(t *testing.T) {
	t.Parallel()

	gen := &mockFusionGenerator{}
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

type failingFusionGenerator struct {
	err error
}

func (f *failingFusionGenerator) GenerateFusedImage(context.Context, imagePorts.ImageFusionRequest) (*imagePorts.ImageResponse, error) {
	return nil, f.err
}
