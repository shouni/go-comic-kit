package asset

import (
	"testing"
)

func TestDefaultPathFunctions(t *testing.T) {
	t.Run("DefaultPanelImagePath", func(t *testing.T) {
		expected := "images/panel.png"
		if got := DefaultPanelImagePath(); got != expected {
			t.Errorf("DefaultPanelImagePath() = %v, want %v", got, expected)
		}
	})

	t.Run("DefaultPageImagePath", func(t *testing.T) {
		expected := "images/comic_page.png"
		if got := DefaultPageImagePath(); got != expected {
			t.Errorf("DefaultPageImagePath() = %v, want %v", got, expected)
		}
	})
}

func TestResolveOutputPath(t *testing.T) {
	tests := []struct {
		name     string
		baseDir  string
		fileName string
		want     string
	}{
		{"LocalPath", "output", "test.json", "output/test.json"},
		{"GCSPath", "gs://bucket/data", "test.json", "gs://bucket/data/test.json"},
		{"EmptyBase", "", "test.json", "test.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveOutputPath(tt.baseDir, tt.fileName)
			if err != nil {
				t.Fatalf("ResolveOutputPath() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveOutputPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		rawPath string
		want    string
	}{
		{"URLWithFile", "https://example.com/path/to/file.md", "https://example.com/path/to/"},
		{"GCSWithFile", "gs://my-bucket/folder/config.json", "gs://my-bucket/folder/"},
		{"LocalPath", "docs/manual/index.html", "docs/manual/"},
		{"TrailingSlash", "http://example.com/dir/", "http://example.com/dir/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveBaseURL(tt.rawPath); got != tt.want {
				t.Errorf("ResolveBaseURL(%v) = %v, want %v", tt.rawPath, got, tt.want)
			}
		})
	}
}

func TestGenerateIndexedPath(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
		index    int
		want     string
		wantErr  bool
	}{
		{"NormalPNG", "images/panel.png", 1, "images/panel_1.png", false},
		{"NormalJPG", "comic_page.jpg", 10, "comic_page_10.jpg", false},
		{"PathWithDots", "my.data/image.v1.png", 5, "my.data/image.v1_5.png", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateIndexedPath(tt.basePath, tt.index)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateIndexedPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GenerateIndexedPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegexMatching(t *testing.T) {
	t.Run("PanelFileRegex", func(t *testing.T) {
		tests := []struct {
			input string
			want  bool
		}{
			{"panel_1.png", true},
			{"panel_999.png", true},
			{"panel_0.png", true},
			{"panel.png", false},     // インデックスがない
			{"other_1.png", false},   // プレフィックス違い
			{"panel_1.jpg", false},   // 拡張子違い
			{"panel_abc.png", false}, // 数値以外
		}

		for _, tt := range tests {
			if got := PanelFileRegex.MatchString(tt.input); got != tt.want {
				t.Errorf("PanelFileRegex.MatchString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		}
	})

	t.Run("PageFileRegex", func(t *testing.T) {
		tests := []struct {
			input string
			want  bool
		}{
			{"comic_page_1.png", true},
			{"comic_page_24.png", true},
			{"comic_page.png", false},
			{"panel_1.png", false},
		}

		for _, tt := range tests {
			if got := PageFileRegex.MatchString(tt.input); got != tt.want {
				t.Errorf("PageFileRegex.MatchString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		}
	})
}
