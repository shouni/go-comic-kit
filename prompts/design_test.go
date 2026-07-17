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
	if !strings.Contains(negative, "extra fingers") {
		t.Errorf("negative prompt = %q, want finger-anatomy negatives", negative)
	}
	if !strings.Contains(user, "Zundamon (green hair)") || !strings.Contains(user, "anime style") {
		t.Errorf("user prompt = %q, want description and style suffix", user)
	}
	if strings.Contains(user, "multiple views") == false {
		t.Errorf("user prompt = %q, want default multi-view layout", user)
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
	if strings.Contains(user, "multiple views") {
		t.Errorf("user prompt = %q, must not contain multi-view layout", user)
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
