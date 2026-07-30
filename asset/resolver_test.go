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

func TestIsStateFileName(t *testing.T) {
	if !IsStateFileName(DefaultStateJSON) {
		t.Errorf("IsStateFileName(%q) = false, want true", DefaultStateJSON)
	}
	if IsStateFileName("panel_ch01-p01.png") {
		t.Error("IsStateFileName(パネル画像) = true, want false")
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
	// パネルIDにパス区切りが混ざっても images 直下から出ないこと
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
	got, err := PageImagePath("gs://bucket/job", 3)
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
	// 前方一致での一覧・削除に使うため末尾はスラッシュで終わること
	got, err := CharacterDesignPrefix("gs://bucket", "zundamon")
	if err != nil {
		t.Fatalf("CharacterDesignPrefix() unexpected error: %v", err)
	}
	want := "gs://bucket/character/zundamon/"
	if got != want {
		t.Errorf("CharacterDesignPrefix() = %q, want %q", got, want)
	}

	// 同じキャラクターの DesignSheetPath はこの接頭辞の下に入ること
	// （一覧・削除がデザインシートを取りこぼさないための不変条件）
	sheet, err := DesignSheetPath("gs://bucket", []string{"zundamon"}, "job-001", ".png")
	if err != nil {
		t.Fatalf("DesignSheetPath() unexpected error: %v", err)
	}
	if !strings.HasPrefix(sheet, got) {
		t.Errorf("DesignSheetPath() = %q は CharacterDesignPrefix() = %q の下にない", sheet, got)
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
		if len(got) > maxDesignFileTagBytes+9 { // 切り詰め + "_" + 8桁のCRC32
			t.Errorf("DesignFileTag() の長さ = %d バイト, want %d 以下", len(got), maxDesignFileTagBytes+9)
		}
		if !utf8.ValidString(got) {
			t.Errorf("DesignFileTag() = %q が不正な UTF-8（rune 境界で切れていない）", got)
		}
	})

	t.Run("DifferentLongIDsGetDifferentTags", func(t *testing.T) {
		a := DesignFileTag([]string{strings.Repeat("x", 200) + "a"})
		b := DesignFileTag([]string{strings.Repeat("x", 200) + "b"})
		if a == b {
			t.Error("長いIDの組み合わせ違いが同じタグになっている（チェックサムが効いていない）")
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
