package layout

import "testing"

func TestNormalizeDesignAspectRatio(t *testing.T) {
	cases := map[string]string{
		"1:1":  "1:1",
		"9:16": "9:16",
		"16:9": "16:9",
		"":     DesignAspectRatio,
		"4:3":  DesignAspectRatio,
	}
	for input, want := range cases {
		if got := NormalizeDesignAspectRatio(input); got != want {
			t.Errorf("NormalizeDesignAspectRatio(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsDesignAspectRatio(t *testing.T) {
	for _, ratio := range []string{"1:1", "9:16", "16:9"} {
		if !IsDesignAspectRatio(ratio) {
			t.Errorf("IsDesignAspectRatio(%q) = false, want true", ratio)
		}
	}
	if IsDesignAspectRatio("4:3") {
		t.Error("IsDesignAspectRatio(\"4:3\") = true, want false")
	}
}
