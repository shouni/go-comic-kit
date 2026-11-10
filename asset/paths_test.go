package asset

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStatePath(t *testing.T) {
	tests := []struct {
		name    string
		baseDir string
		want    string
	}{
		{"LocalPath", "output", "output/comic_state.json"},
		{"GCSPath", "gs://bucket/jobs/job-001", "gs://bucket/jobs/job-001/comic_state.json"},
		{"EmptyBase", "", "comic_state.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StatePath(tt.baseDir)
			if err != nil {
				t.Fatalf("StatePath() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("StatePath(%q) = %q, want %q", tt.baseDir, got, tt.want)
			}
		})
	}
}

func TestPanelImagePath(t *testing.T) {
	got, err := PanelImagePath("gs://bucket/job", "ch01-p03", ".png")
	if err != nil {
		t.Fatalf("PanelImagePath() unexpected error: %v", err)
	}
	want := "gs://bucket/job/images/panel_ch01-p03.png"
	if got != want {
		t.Errorf("PanelImagePath() = %q, want %q", got, want)
	}
}

func TestPanelImagePathSanitizesID(t *testing.T) {
	// A path separator in the panel ID must not escape the images directory
	got, err := PanelImagePath("out", "ch01/p03", ".png")
	if err != nil {
		t.Fatalf("PanelImagePath() unexpected error: %v", err)
	}
	want := "out/images/panel_ch01_p03.png"
	if got != want {
		t.Errorf("PanelImagePath() = %q, want %q", got, want)
	}
}

func TestPageImagePath(t *testing.T) {
	got, err := PageImagePath("gs://bucket/job", 3, ".png")
	if err != nil {
		t.Fatalf("PageImagePath() unexpected error: %v", err)
	}
	want := "gs://bucket/job/images/comic_page_3.png"
	if got != want {
		t.Errorf("PageImagePath() = %q, want %q", got, want)
	}
}

func TestDesignSheetPath(t *testing.T) {
	got, err := DesignSheetPath("gs://bucket", []string{"zundamon", "metan"}, "job-001", ".png")
	if err != nil {
		t.Fatalf("DesignSheetPath() unexpected error: %v", err)
	}
	want := "gs://bucket/character/zundamon_metan/job-001.png"
	if got != want {
		t.Errorf("DesignSheetPath() = %q, want %q", got, want)
	}
}

func TestCharacterDesignPrefix(t *testing.T) {
	// It must end with a slash, since it is used for prefix listing and deletion
	got, err := CharacterDesignPrefix("gs://bucket", "zundamon")
	if err != nil {
		t.Fatalf("CharacterDesignPrefix() unexpected error: %v", err)
	}
	want := "gs://bucket/character/zundamon/"
	if got != want {
		t.Errorf("CharacterDesignPrefix() = %q, want %q", got, want)
	}

	// The same character's DesignSheetPath must fall under this prefix
	// (the invariant that keeps listing and deletion from missing a design sheet)
	sheet, err := DesignSheetPath("gs://bucket", []string{"zundamon"}, "job-001", ".png")
	if err != nil {
		t.Fatalf("DesignSheetPath() unexpected error: %v", err)
	}
	if !strings.HasPrefix(sheet, got) {
		t.Errorf("DesignSheetPath() = %q is not under CharacterDesignPrefix() = %q", sheet, got)
	}
}

func TestDesignFileTag(t *testing.T) {
	t.Run("ShortIDsAreJoined", func(t *testing.T) {
		if got, want := DesignFileTag([]string{"a", "b"}), "a_b"; got != want {
			t.Errorf("DesignFileTag() = %q, want %q", got, want)
		}
	})

	t.Run("LongIDsAreTruncatedAtRuneBoundary", func(t *testing.T) {
		got := DesignFileTag([]string{strings.Repeat("ずんだもん", 40)})
		if len(got) > maxDesignFileTagBytes+9 { // truncation + "_" + 8-digit CRC32
			t.Errorf("DesignFileTag() length = %d bytes, want at most %d", len(got), maxDesignFileTagBytes+9)
		}
		if !utf8.ValidString(got) {
			t.Errorf("DesignFileTag() = %q is invalid UTF-8 (not cut at a rune boundary)", got)
		}
	})

	t.Run("DifferentLongIDsGetDifferentTags", func(t *testing.T) {
		a := DesignFileTag([]string{strings.Repeat("x", 200) + "a"})
		b := DesignFileTag([]string{strings.Repeat("x", 200) + "b"})
		if a == b {
			t.Error("different combinations of long IDs produced the same tag (the checksum is not working)")
		}
	})
}

func TestSanitizeFileName(t *testing.T) {
	got := SanitizeFileName(`a/b\c:d*e?f"g<h>i|j`)
	want := "a_b_c_d_e_f_g_h_i_j"
	if got != want {
		t.Errorf("SanitizeFileName() = %q, want %q", got, want)
	}
}

// TestPageImagePathUsesExtension checks that a page image's extension follows the
// caller's choice (derived from the generated MIME type). When this hardcoded ".png",
// only the pages the model returned as JPEG had an extension contradicting their contents.
func TestPageImagePathUsesExtension(t *testing.T) {
	t.Parallel()

	got, err := PageImagePath("gs://bucket/job", 12, ".jpg")
	if err != nil {
		t.Fatalf("PageImagePath() unexpected error: %v", err)
	}
	if want := "gs://bucket/job/images/comic_page_12.jpg"; got != want {
		t.Errorf("PageImagePath() = %q, want %q", got, want)
	}
}

// Pins the round trip between DesignSheetPath and DesignSheetJobID.
//
// When only one direction existed, the lister reverse-engineered the "file name is
// {jobID}{extension}" convention on its own. Changing the naming here would make the
// caller's listing turn empty without any error. Pinning the round trip makes this fail
// when it changes.
func TestDesignSheetPathRoundTrip(t *testing.T) {
	const baseDir = "gs://bucket"

	tests := []struct {
		name      string
		character string
		jobID     string
		extension string
	}{
		{"plain", "zundamon", "c20260718-113045-1a2b3c4d", ".png"},
		{"old ID format", "zundamon", "c20260718-113045-1a2b3c4d", ".jpg"},
		{"ID with symbols", "zundamon", "c2026/07/18-113045", ".png"},
		{"no extension", "zundamon", "c20260718-113045-1a2b3c4d", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, err := DesignSheetPath(baseDir, []string{tt.character}, tt.jobID, tt.extension)
			if err != nil {
				t.Fatalf("DesignSheetPath() error = %v", err)
			}

			prefix, err := CharacterDesignPrefix(baseDir, tt.character)
			if err != nil {
				t.Fatalf("CharacterDesignPrefix() error = %v", err)
			}

			got, ok := DesignSheetJobID(prefix, uri)
			if !ok {
				t.Fatalf("DesignSheetJobID(%q, %q) = _, false (not recognised as under the prefix)", prefix, uri)
			}
			// What is saved is the ID after SanitizeFileName.
			if want := SanitizeFileName(tt.jobID); got != want {
				t.Errorf("DesignSheetJobID() = %q, want %q", got, want)
			}
		})
	}
}

// Anything not under the prefix, or with an intermediate level, must not be picked up.
func TestDesignSheetJobIDRejectsUnrelatedPaths(t *testing.T) {
	const prefix = "gs://bucket/character/zundamon/"

	tests := []struct {
		name string
		uri  string
	}{
		{"different character", "gs://bucket/character/metan/job-1.png"},
		{"intermediate level", "gs://bucket/character/zundamon/sub/job-1.png"},
		{"the prefix itself", "gs://bucket/character/zundamon/"},
		{"different directory sharing the prefix", "gs://bucket/character/zundamon2/job-1.png"},
		{"unrelated", "gs://bucket/comics/job-1/comic_state.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := DesignSheetJobID(prefix, tt.uri); ok {
				t.Errorf("DesignSheetJobID(%q) = %q, true; want false", tt.uri, got)
			}
		})
	}
}
