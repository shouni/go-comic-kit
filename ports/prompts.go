package ports

import "github.com/shouni/go-comic-kit/comic"

// This file defines the prompt-building contracts and the data passed to their
// implementations.
//
// All five must be injected through workflow.Args; the kit has no default
// implementation. Prompts are wording refined per work, so they live on the side that
// can change a character without a kit release in between (the app), for the same
// reason as model names and art direction.

// OutlinePromptData is the data passed to the outline generation prompt template.
type OutlinePromptData struct {
	// InputText is the source text.
	InputText string
	// CharacterRoster is the list of available characters (bulleted text).
	CharacterRoster string
	// MaxChapters is the chapter limit.
	MaxChapters int
}

// ChapterPromptData is the data passed to the per-chapter script generation prompt template.
type ChapterPromptData struct {
	// WorkTitle / WorkDescription describe the whole work.
	WorkTitle       string
	WorkDescription string
	// OutlineDigest is the list of all chapters (a bulleted list of ID, title and
	// summary), passed as context to keep the flow consistent across chapters.
	OutlineDigest string
	// Chapter is the chapter whose panels are being generated.
	Chapter comic.Chapter
	// CharacterRoster is the list of available characters (bulleted text).
	CharacterRoster string
	// MaxPanels is the panel limit per chapter.
	MaxPanels int
}

// OutlinePrompt is the contract that builds the outline generation prompt.
type OutlinePrompt interface {
	BuildOutline(mode string, data *OutlinePromptData) (string, error)
}

// ChapterScriptPrompt is the contract that builds the per-chapter script generation prompt.
type ChapterScriptPrompt interface {
	BuildChapterScript(mode string, data *ChapterPromptData) (string, error)
}

// DesignSheetPromptData is the data passed to design sheet prompt assembly.
type DesignSheetPromptData struct {
	// Descriptions describe the target characters (name + VisualCues).
	// For several characters (a combined design sheet) it has two or more elements.
	Descriptions []string
	// Referenced[i] reports whether Descriptions[i]'s character has a reference image
	// attached. The attached images follow the referenced subjects in order. A subject with
	// false has no image yet (a new character's first sheet) and is drawn from her
	// description alone, so the implementation must not tell the model to follow an
	// attached image for her — doing so with no image, or with someone else's, invents a look.
	// Same length as Descriptions.
	Referenced []bool
	// Layout set to DesignLayoutSingleView means a single-pose layout; an empty string
	// means a three-view turnaround.
	Layout string
	// StyleMode is the art style mode the caller specified (DesignSheetRequest.StyleMode).
	// An empty string means unspecified, and the implementation's default applies. The
	// kit does not interpret it.
	StyleMode string
}

// DesignSheetPrompt is the contract that builds the system/user/negative prompts of
// design sheet generation. The result is referenced as the character identity anchor by
// the other workflows (panels, pages and so on), so an implementation should favour
// accuracy and consistency over staging.
type DesignSheetPrompt interface {
	BuildDesignSheet(data *DesignSheetPromptData) (systemPrompt, userPrompt, negativePrompt string, err error)
}

// PanelPromptData is the data passed to panel image prompt assembly.
type PanelPromptData struct {
	// Panel is the target panel (Shot / Setting / VisualAnchor / Characters).
	Panel comic.Panel
	// Characters are the character definitions, used to resolve names and visual_cues.
	Characters *comic.Characters
	// SubjectIDs are the IDs of the characters whose reference images were attached, in
	// attachment order. Implementations must number references in this order. If the
	// order drifts, the model draws while looking at someone else's reference image.
	SubjectIDs []string
	// StyleMode is the art style mode the caller specified (GenerateOptions.StyleMode).
	// An empty string means unspecified, and the implementation's default applies. The
	// kit does not interpret it.
	//
	// The mode name is passed instead of the style direction itself because the style
	// and "what to avoid in that style" (the negative prompt) are decided as a pair, and
	// the implementation holds both.
	StyleMode string
}

// PanelPrompt is the contract that builds the panel image generation prompt.
//
// Even structural instructions — identity with the reference images, drawing no text,
// guarding against broken fingers — are refined differently per work, so the
// implementation holds them.
type PanelPrompt interface {
	BuildPanel(data *PanelPromptData) (systemPrompt, userPrompt, negativePrompt string, err error)
	// BuildPanelEdit builds the prompt for an edit instruction on a generated panel image.
	// The aim is to change only the instructed parts while keeping the composition, pose
	// and background.
	BuildPanelEdit(editPrompt string) (systemPrompt, userPrompt, negativePrompt string, err error)
}

// PagePromptData is the data passed to page composition prompt assembly.
type PagePromptData struct {
	// Panels are the panels on this page (in display order).
	Panels []comic.Panel
	// Characters are the character definitions.
	Characters *comic.Characters
	// CharacterFile maps a character ID to the number (1-based) of its attached reference
	// image. Characters without an attachment are not included.
	CharacterFile map[string]int
	// PanelFile maps a panel ID to the number (1-based) of its attached generated panel image.
	PanelFile map[string]int
	// StyleMode is the art style mode the caller specified (treated like PanelPromptData.StyleMode).
	StyleMode string
}

// PagePrompt is the contract that builds the page composition prompt.
//
// The CharacterFile / PanelFile numbers match the order of the images actually attached.
// Always use these numbers when referring to images in the prompt.
type PagePrompt interface {
	BuildPage(data *PagePromptData) (systemPrompt, userPrompt, negativePrompt string, err error)
	// BuildPageEdit builds the prompt for an edit instruction on a generated page image.
	BuildPageEdit(editPrompt string) (systemPrompt, userPrompt, negativePrompt string, err error)
}
