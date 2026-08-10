package layout

import (
	"slices"
	"testing"
)

func TestNormalizeAspectRatio(t *testing.T) {
	cases := map[string]string{
		"1:1":  "1:1",
		"3:4":  "3:4",
		"9:16": "9:16",
		"16:9": "16:9",
		"":     DefaultAspectRatio,
		"4:3":  DefaultAspectRatio,
	}
	for input, want := range cases {
		if got := NormalizeAspectRatio(input, DefaultAspectRatio); got != want {
			t.Errorf("NormalizeAspectRatio(%q) = %q, want %q", input, got, want)
		}
	}

	// フォールバック先は呼び出し側が決めます（デザインシートは設定された比率へ落とします）。
	if got := NormalizeAspectRatio("4:3", "16:9"); got != "16:9" {
		t.Errorf("NormalizeAspectRatio with a custom fallback = %q, want 16:9", got)
	}
}

func TestIsAspectRatio(t *testing.T) {
	for _, ratio := range []string{"1:1", "3:4", "9:16", "16:9"} {
		if !IsAspectRatio(ratio) {
			t.Errorf("IsAspectRatio(%q) = false, want true", ratio)
		}
	}
	if IsAspectRatio("4:3") {
		t.Error(`IsAspectRatio("4:3") = true, want false`)
	}
}

// AspectRatios が内部スライスを共有していると、呼び出し側の書き換えが全体に波及します。
func TestAspectRatiosReturnsACopy(t *testing.T) {
	got := AspectRatios()
	if !slices.Contains(got, DefaultAspectRatio) {
		t.Fatalf("AspectRatios() = %v, want it to contain the default %q", got, DefaultAspectRatio)
	}
	got[0] = "mutated"
	if slices.Contains(AspectRatios(), "mutated") {
		t.Error("AspectRatios() shares its backing array with the caller")
	}
}
