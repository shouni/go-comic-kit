package prompts

import (
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/ports"
)

func TestDefaultDesignPromptBuildsSingleSubject(t *testing.T) {
	t.Parallel()

	system, user, negative, err := DefaultDesignPrompt{}.BuildDesignSheet(&ports.DesignSheetPromptData{
		Descriptions: []string{"Zundamon (green hair)"},
		StyleSuffix:  "anime style",
	})
	if err != nil {
		t.Fatalf("BuildDesignSheet failed: %v", err)
	}
	if !strings.Contains(system, "canonical identity reference") {
		t.Errorf("system prompt = %q, want identity-consistency instructions", system)
	}
	if !strings.Contains(system, "one complete, physically connected body") {
		t.Errorf("system prompt = %q, want connected-body instructions", system)
	}
	if !strings.Contains(negative, "Do not include") {
		t.Errorf("negative prompt = %q, want instruction-style exclusion list", negative)
	}
	// Gemini 系モデルにはネガティブプロンプトの負条件付けチャネルがなく平文として
	// 連結されるため、欠陥語彙（extra fingers 等）はかえって崩れを誘発する。
	// 含まれないことを保証する。
	for _, defect := range []string{"extra fingers", "fused fingers", "extra limbs", "malformed"} {
		if strings.Contains(negative, defect) {
			t.Errorf("negative prompt contains defect vocabulary %q, which can induce the artifact", defect)
		}
	}
	if !strings.Contains(user, "Zundamon (green hair)") || !strings.Contains(user, "anime style") {
		t.Errorf("user prompt = %q, want description and style suffix", user)
	}
	if !strings.Contains(user, "front view, side view, and back view") {
		t.Errorf("user prompt = %q, want default turnaround layout", user)
	}
	if !strings.Contains(user, "complete connected figure") {
		t.Errorf("user prompt = %q, want connected-figure layout constraint", user)
	}
}

func TestDefaultDesignPromptMultiSubject(t *testing.T) {
	t.Parallel()

	_, user, _, err := DefaultDesignPrompt{}.BuildDesignSheet(&ports.DesignSheetPromptData{
		Descriptions: []string{"Zundamon", "Metan"},
	})
	if err != nil {
		t.Fatalf("BuildDesignSheet failed: %v", err)
	}
	if !strings.Contains(user, "2 DIFFERENT characters") {
		t.Errorf("user prompt = %q, want multi-subject framing", user)
	}
	if !strings.Contains(user, "each character's three views grouped together") {
		t.Errorf("user prompt = %q, want per-character view grouping", user)
	}
	if strings.Contains(user, "of the same character") {
		t.Errorf("user prompt = %q, must not contain single-character layout phrasing", user)
	}
}

func TestDefaultDesignPromptSingleViewLayout(t *testing.T) {
	t.Parallel()

	_, user, _, err := DefaultDesignPrompt{}.BuildDesignSheet(&ports.DesignSheetPromptData{
		Descriptions: []string{"Zundamon"},
		Layout:       ports.DesignLayoutSingleView,
	})
	if err != nil {
		t.Fatalf("BuildDesignSheet failed: %v", err)
	}
	if !strings.Contains(user, "single view, front-facing") {
		t.Errorf("user prompt = %q, want single-view layout", user)
	}
	if strings.Contains(user, "three views") {
		t.Errorf("user prompt = %q, must not contain turnaround layout", user)
	}
}

func TestDefaultDesignPromptSingleViewMultiSubject(t *testing.T) {
	t.Parallel()

	_, user, _, err := DefaultDesignPrompt{}.BuildDesignSheet(&ports.DesignSheetPromptData{
		Descriptions: []string{"Zundamon", "Metan"},
		Layout:       ports.DesignLayoutSingleView,
	})
	if err != nil {
		t.Fatalf("BuildDesignSheet failed: %v", err)
	}
	if !strings.Contains(user, "single view, front-facing") {
		t.Errorf("user prompt = %q, want single-view layout", user)
	}
	if !strings.Contains(user, "side by side") {
		t.Errorf("user prompt = %q, want side-by-side multi-character layout", user)
	}
	if strings.Contains(user, "three views") {
		t.Errorf("user prompt = %q, must not contain turnaround layout", user)
	}
}

func TestDefaultDesignPromptRejectsEmptyDescriptions(t *testing.T) {
	t.Parallel()

	if _, _, _, err := (DefaultDesignPrompt{}).BuildDesignSheet(&ports.DesignSheetPromptData{}); err == nil {
		t.Error("BuildDesignSheet with no descriptions succeeded, want error")
	}
	if _, _, _, err := (DefaultDesignPrompt{}).BuildDesignSheet(nil); err == nil {
		t.Error("BuildDesignSheet(nil) succeeded, want error")
	}
}
