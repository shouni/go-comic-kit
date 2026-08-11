package ports

import (
	"errors"
	"testing"
)

func validConfig() Config {
	c := Config{
		GeminiModel: "gemini-test",
		ImageModel:  "image-test",
	}
	c.ApplyDefaults()
	return c
}

// 既定は従来の固定値と同じ（パネル 1K・ページ 2K・3:4）であること。
// ここが変わると、設定を足していない既存デプロイの出力が黙って変わります。
func TestApplyDefaultsKeepsPreviousLayout(t *testing.T) {
	c := validConfig()

	if c.PanelImageSize != ImageSize1K {
		t.Errorf("PanelImageSize = %q, want %q", c.PanelImageSize, ImageSize1K)
	}
	if c.PageImageSize != ImageSize2K {
		t.Errorf("PageImageSize = %q, want %q", c.PageImageSize, ImageSize2K)
	}
	if c.AspectRatio != DefaultAspectRatio {
		t.Errorf("AspectRatio = %q, want %q", c.AspectRatio, DefaultAspectRatio)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

// 書き間違いは黙って既定へ落とさず、起動時に落とします。
// 落とすと「指定したつもりの比率で生成されない」状態が気付かれずに続きます。
func TestValidateRejectsUnknownLayoutValues(t *testing.T) {
	cases := map[string]func(*Config){
		"AspectRatio":    func(c *Config) { c.AspectRatio = "4:3" },
		"PanelImageSize": func(c *Config) { c.PanelImageSize = "4K" },
		"PageImageSize":  func(c *Config) { c.PageImageSize = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := validConfig()
			mutate(&c)

			err := c.Validate()
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("Validate() error = %v, want ErrConfigInvalid", err)
			}
		})
	}
}
