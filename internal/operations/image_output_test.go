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
		gen     ImageGenerator
		writer  *mockWriter
		pathFor func(string) (string, error)
		want    error
	}{
		{
			name:    "生成の失敗は ErrGeneration",
			gen:     &failingImageGenerator{err: errors.New("upstream 503")},
			writer:  &mockWriter{},
			pathFor: okPath,
			want:    ports.ErrGeneration,
		},
		{
			name:    "保存先パスの失敗は ErrInvalidRequest",
			gen:     &mockImageGenerator{},
			writer:  &mockWriter{},
			pathFor: func(string) (string, error) { return "", errors.New("bad output dir") },
			want:    ports.ErrInvalidRequest,
		},
		{
			name:    "保存の失敗は ErrGeneration",
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

// TestRenderImageRecordsGenerationConditions は、再生成に必要な条件が記録されることを
// 確認します（GenerationRecord は再生成の基礎です）。
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

func (f *failingImageGenerator) Generate(context.Context, imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	return nil, f.err
}

// TestGetPreferredExtension は、モデルが返しうる画像形式に拡張子が追随することを確認します。
// 以前は png/jpeg しか知らず、WebP を .png という名前で保存していました
// （Content-Type は正しいので中身と名前だけが食い違う状態）。
func TestGetPreferredExtension(t *testing.T) {
	t.Parallel()

	tests := []struct{ mimeType, want string }{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/webp", ".webp"},
		{"image/gif", ".gif"},
		{"IMAGE/WEBP ", ".webp"}, // 大文字・前後の空白も揃える
		{"", ".png"},             // 不明なものは既定のまま
		{"text/plain", ".png"},
	}
	for _, tt := range tests {
		if got := getPreferredExtension(tt.mimeType); got != tt.want {
			t.Errorf("getPreferredExtension(%q) = %q, want %q", tt.mimeType, got, tt.want)
		}
	}
}

// TestRenderImageCacheControl は、保存時の Cache-Control が設定で差し替えられ、
// 未設定なら既定値になることを確認します。"public" はライブラリが決めてよい値ではなく、
// 生成物を公開配信してよいかというデプロイ側の判断です。
func TestRenderImageCacheControl(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct{ given, want string }{
		"未設定なら既定値": {"", ports.DefaultCacheControl},
		"指定があれば尊重": {"private, max-age=0", "private, max-age=0"},
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
			if writer.lastSettings.CacheControl != tt.want {
				t.Errorf("CacheControl = %q, want %q", writer.lastSettings.CacheControl, tt.want)
			}
		})
	}
}
