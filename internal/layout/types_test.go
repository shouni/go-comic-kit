package layout

import (
	"slices"
	"testing"
)

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
