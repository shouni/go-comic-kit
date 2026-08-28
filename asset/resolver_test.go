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

// TestPageImagePathUsesExtension は、ページ画像の拡張子が呼び出し側の指定
// （生成結果の MIME type 由来）に従うことを確認します。ここを ".png" 決め打ちに
// していた頃は、モデルが JPEG を返したページだけ中身と拡張子が食い違っていました。
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

// DesignSheetPath と DesignSheetJobID が往復することを固定します。
//
// 片方向しか無かった頃は、一覧する側が「ファイル名は {jobID}{拡張子}」という規約を
// 自前で逆算していました。ここでファイル名の付け方を変えると、呼び出し側の一覧が
// エラーも出さずに空になります。往復で縛っておけば、変えたときにここが落ちます。
func TestDesignSheetPathRoundTrip(t *testing.T) {
	const baseDir = "gs://bucket"

	tests := []struct {
		name      string
		character string
		jobID     string
		extension string
	}{
		{"通常", "zundamon", "c20260718-113045-1a2b3c4d", ".png"},
		{"旧採番形式", "zundamon", "c20260718-113045-1a2b3c4d", ".jpg"},
		{"記号を含むID", "zundamon", "c2026/07/18-113045", ".png"},
		{"拡張子なし", "zundamon", "c20260718-113045-1a2b3c4d", ""},
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
				t.Fatalf("DesignSheetJobID(%q, %q) = _, false（プレフィックス配下と認識されていない）", prefix, uri)
			}
			// 保存されるのは SanitizeFileName を通したあとの ID です。
			if want := SanitizeFileName(tt.jobID); got != want {
				t.Errorf("DesignSheetJobID() = %q, want %q", got, want)
			}
		})
	}
}

// プレフィックス配下でないもの・階層を挟むものは拾わないこと。
func TestDesignSheetJobIDRejectsUnrelatedPaths(t *testing.T) {
	const prefix = "gs://bucket/character/zundamon/"

	tests := []struct {
		name string
		uri  string
	}{
		{"別キャラクター", "gs://bucket/character/metan/job-1.png"},
		{"階層を挟む", "gs://bucket/character/zundamon/sub/job-1.png"},
		{"プレフィックスそのもの", "gs://bucket/character/zundamon/"},
		{"前方一致するだけの別ディレクトリ", "gs://bucket/character/zundamon2/job-1.png"},
		{"無関係", "gs://bucket/comics/job-1/comic_state.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := DesignSheetJobID(prefix, tt.uri); ok {
				t.Errorf("DesignSheetJobID(%q) = %q, true; want false", tt.uri, got)
			}
		})
	}
}
