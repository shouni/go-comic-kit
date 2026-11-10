package ports

import (
	"context"

	"github.com/shouni/go-comic-kit/comic"
)

// This file defines the contracts of go-comic-kit's operation set (see the operations
// section of README.md). Every operation is idempotent: it takes a MangaState and
// returns the updated MangaState.

// OutlineRequest is the input to outline generation (GenerateOutline).
type OutlineRequest struct {
	// SourceURL is the URI the manuscript is read from (mutually exclusive with SourceText).
	SourceURL string
	// SourceText is the manuscript text itself (mutually exclusive with SourceURL).
	SourceText string
	// Mode is the outline prompt's mode (template selection). Empty means the default template.
	Mode string
	// StyleMode is the style selection for image generation, recorded on the generated MangaState.
	StyleMode string
	// MaxChapters is the chapter limit. 0 or less uses the default.
	MaxChapters int
	// Model is the text model that writes the outline (required).
	// A call with an empty string is ErrInvalidRequest, and the AI API is not called.
	Model string
}

// OutlineGenerator is the contract that generates, from a manuscript, a MangaState
// holding only the outline (Chapters). It is the first stage of script generation; each
// chapter's panels are generated per chapter by ChapterScriptGenerator.
type OutlineGenerator interface {
	GenerateOutline(ctx context.Context, req OutlineRequest) (*comic.MangaState, error)
}

// ChapterScriptOptions are the options of chapter script generation (GenerateChapterScript).
// The prompt mode is taken from state.ScriptMode (decided at outline time), so it is not here.
type ChapterScriptOptions struct {
	// Model is the text model that writes the chapter script (required).
	//
	// One work's script should be written by the same model for the outline and every
	// chapter, so pass the same value as OutlineRequest.Model.
	Model string
}

// ChapterScriptGenerator is the contract that generates the panels (script) of the given
// chapter, using the whole outline as context, and replaces that chapter's existing
// panels (idempotent, supports per-chapter regeneration).
type ChapterScriptGenerator interface {
	GenerateChapterScript(ctx context.Context, state *comic.MangaState, chapterID string, opts ChapterScriptOptions) (*comic.MangaState, error)
}

// DesignOverride is a one-off override that swaps a character's reference image and
// visual_cues for a single call. It does not change the character definition
// (characters.json) itself. When ReferenceURL / VisualCues is empty, that field alone
// uses the character definition's value. When CharacterIDs has several entries (a
// combined design sheet), the override is ignored, since which character it applies to
// is ambiguous.
type DesignOverride struct {
	ReferenceURL string
	VisualCues   []string
}

// DesignSheetRequest is the input to design sheet generation (GenerateDesignSheet).
type DesignSheetRequest struct {
	// CharacterIDs are the target characters. Several make one combined sheet.
	CharacterIDs []string
	// JobID uniquely identifies this generation call (minted by the caller).
	// It is used in the save path (character/{tag}/{JobID}.ext under OutputDir), so that
	// repeated generations for the same character are kept as history instead of
	// overwriting one another. An empty string is not allowed.
	JobID string
	// Seed is the generation seed. When nil, the generator draws one and records it in
	// DesignSheetRef.UsedSeed (passing that value again reproduces the same sheet).
	// It is the same representation as the Seed of GenerateOptions / BatchOptions.
	Seed *int64
	// OutputDir is the base directory (local or gs://) where sheet images are saved.
	// Characters are shared assets independent of any job (work), so it is usually the
	// bucket root (for example "gs://bucket"). The relative path is resolved against
	// OutputDir as "character/{tag}/{JobID}.ext".
	OutputDir string
	// AspectRatio is one of "1:1" / "3:4" / "9:16" / "16:9". Empty means the kit
	// default; an unsupported value is ErrInvalidRequest. Generate a sheet used as a
	// reference anchor for panels or pages at the same ratio as those panels and pages
	// (at a different ratio, the detail-drift suppression that comes from aspect-matched
	// reference images does not work).
	AspectRatio string
	// ImageSize is the resolution. Empty means the kit default (2K). The sheet is the
	// identity anchor of everything generated, so the default follows pages, not panels.
	ImageSize string
	// Layout set to DesignLayoutSingleView produces a single pose (for reference anchors);
	// an empty string produces a three-view turnaround.
	Layout string
	// Override is a one-off override applied only when a single character is specified.
	Override DesignOverride
	// Model is the image generation model that draws the sheet (required).
	Model string
	// StyleMode is this sheet's art style mode (treated like GenerateOptions.StyleMode).
	// The kit does not interpret it and passes it through to DesignSheetPromptData.StyleMode.
	//
	// The sheet is the identity anchor of everything else generated, so an implementation
	// should not reuse the panel style as is, but resolve it to a sheet-specific direction
	// without lighting or staging.
	StyleMode string
}

// DesignLayoutSingleView is the DesignSheetRequest.Layout value for a single-pose layout.
const DesignLayoutSingleView = "single"

// DesignSheetGenerator is the contract that generates a design sheet serving as a
// character's identity anchor and records it on the MangaState. A nil state creates a
// new state.
type DesignSheetGenerator interface {
	GenerateDesignSheet(ctx context.Context, state *comic.MangaState, req DesignSheetRequest) (*comic.MangaState, error)
}

// GenerateOptions are the options shared by the panel and page generation operations.
type GenerateOptions struct {
	// Seed is the generation seed. Specifying it re-rolls.
	//
	// When nil, it is resolved in the order "previous GenerationRecord.UsedSeed → the
	// lead character's Seed → newly drawn". With a previous value, this is "regenerate
	// under the same conditions"; otherwise the drawn value is recorded in
	// GenerationRecord.UsedSeed, so later runs can reproduce it.
	Seed *int64
	// PromptOverride replaces the automatically built prompt (empty means build it automatically).
	PromptOverride string
	// EditPrompt switches to an edit mode that takes the existing generated image
	// (GenerationRecord.ImageURL) as input instead of regenerating from scratch. Only the
	// instructed parts change, keeping the composition, pose and background (the same
	// approach as go-veo-orchestrator's EditCut). It is an error when the target panel has
	// no generated image.
	EditPrompt string
	// Model is the image generation model for this generation (required).
	// A call with an empty string is ErrInvalidRequest, and the image API is not called.
	Model string
	// AspectRatio / ImageSize are the ratio and resolution of the generated image.
	// Empty means the kit default (3:4, panels 1K, pages 2K). An unsupported value is
	// ErrInvalidRequest (silently falling back to the default leaves "not generated at
	// the ratio I asked for" going unnoticed).
	//
	// Use the same ratio for panels, pages and design sheets (see the Config description).
	AspectRatio string
	ImageSize   string
	// StyleMode is this generation's art style mode. An empty string means the prompt
	// implementation's default.
	//
	// The kit does not interpret it and passes it as is to the prompt implementation as
	// PanelPromptData.StyleMode / PagePromptData.StyleMode. Art style is per-work visual
	// direction, and the style direction and "what to avoid in that style" (the negative
	// prompt) are decided as a pair. The prompt implementation holds both, so the kit
	// carries the mode name rather than resolved text.
	StyleMode string
	// OutputDir is the base directory where generated images are saved.
	OutputDir string
}

// PanelImageGenerator is the contract that generates or regenerates panel images, one
// panel at a time or all at once, and records the result in the comic.MangaState's
// comic.GenerationRecord.
type PanelImageGenerator interface {
	GeneratePanel(ctx context.Context, state *comic.MangaState, panelID string, opts GenerateOptions) (*comic.MangaState, error)

	// GenerateAllPanels generates every panel in the state in one go.
	// Parallelism follows Config.MaxConcurrency (the default of 1 behaves like sequential
	// execution).
	//
	// Even when some fail, it returns both the state with the successes recorded and an
	// error joining the failures with errors.Join (the state is nil only when the state
	// itself was invalid). Image generation is expensive, so save the successes and
	// re-run only the rest with SkipGenerated.
	GenerateAllPanels(ctx context.Context, state *comic.MangaState, opts BatchOptions) (*comic.MangaState, error)
}

// PageImageComposer is the contract that composes panels into pages, one page at a time
// or all at once, and records the result in the comic.MangaState's comic.PageArtifact.
type PageImageComposer interface {
	ComposePage(ctx context.Context, state *comic.MangaState, page int, opts GenerateOptions) (*comic.MangaState, error)

	// ComposeAllPages composes every page in the state in one go.
	// Parallelism and the return values on error are the same as GenerateAllPanels.
	ComposeAllPages(ctx context.Context, state *comic.MangaState, opts BatchOptions) (*comic.MangaState, error)
}

// BatchOptions are the options of the batch operations (GenerateAllPanels /
// ComposeAllPages). It does not embed GenerateOptions because EditPrompt / PromptOverride,
// which target a single item, mean nothing in a batch (the same edit instruction applied
// to every target is pointless).
type BatchOptions struct {
	// Seed is the generation seed applied to every target. When nil, each target follows
	// the same resolution rule as a nil GenerateOptions.Seed
	// (previous value → lead character → newly drawn).
	Seed *int64
	// Model is the image generation model for this batch (required).
	Model string
	// AspectRatio / ImageSize are treated as in GenerateOptions.
	AspectRatio string
	ImageSize   string
	// StyleMode is this batch's art style mode (treated like GenerateOptions.StyleMode).
	StyleMode string
	// OutputDir is the base directory where generated images are saved.
	OutputDir string
	// SkipGenerated, when true, skips targets already generated (those with a
	// comic.GenerationRecord). Use it to redo only the missing part of a batch that
	// partly succeeded.
	SkipGenerated bool
	// ChapterID, when set, limits the targets to that chapter's panels and pages (empty
	// means everything).
	//
	// If the script can be built per chapter but images only all at once, "try one
	// chapter and check it" is impossible. Images are the most expensive part of the
	// process, so the unit of checking matches the script's. An unknown chapter ID is
	// ErrNotFound (a silent zero-item success would hide that nothing happened when
	// something was meant to).
	ChapterID string
}

// Operations holds every assembled operation (built and returned by workflow.New).
//
// It holds no resources to release. The reference image cache decides expiry on read
// and has no background goroutine for cleanup, so creating and dropping it is enough,
// and the consumer has no cleanup duty (the former Close).
type Operations struct {
	Outline       OutlineGenerator
	ChapterScript ChapterScriptGenerator
	DesignSheet   DesignSheetGenerator
	Panel         PanelImageGenerator
	Page          PageImageComposer
}
