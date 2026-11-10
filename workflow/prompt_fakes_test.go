package workflow

import "github.com/shouni/go-comic-kit/ports"

// The kit holds no prompts, so the tests inject all five too.
// The body text is not workflow's concern, so minimal implementations suffice.

type stubPrompts struct{}

func (stubPrompts) BuildOutline(string, *ports.OutlinePromptData) (string, error) {
	return "stub-outline", nil
}

func (stubPrompts) BuildChapterScript(string, *ports.ChapterPromptData) (string, error) {
	return "stub-chapter", nil
}

func (stubPrompts) BuildDesignSheet(*ports.DesignSheetPromptData) (string, string, string, error) {
	return "sys", "stub-design", "neg", nil
}

func (stubPrompts) BuildPanel(*ports.PanelPromptData) (string, string, string, error) {
	return "sys", "stub-panel", "neg", nil
}

func (stubPrompts) BuildPanelEdit(string) (string, string, string, error) {
	return "sys", "stub-panel-edit", "neg", nil
}

func (stubPrompts) BuildPage(*ports.PagePromptData) (string, string, string, error) {
	return "sys", "stub-page", "neg", nil
}

func (stubPrompts) BuildPageEdit(string) (string, string, string, error) {
	return "sys", "stub-page-edit", "neg", nil
}
