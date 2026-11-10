# 📐 Schema (MangaState)

[← README](../README.md)

`comic.MangaState` is the single source of truth for one work. The script is built in two stages, "outline (Chapters) → panel generation per chapter", and each panel (`Panel`) holds **the set of characters who appear** (`Characters []PanelCharacter`), independent of who speaks, plus `Dialogues []DialogueLine`, which supports multiple speech balloons.

## Data model

```go
type MangaState struct {
	Version      int              // state schema version (comic.StateSchemaVersion)
	ID           string           // work/job ID (the kit does not set it; the caller sets it after GenerateOutline)
	Title        string
	Description  string
	StyleMode    string           // image style identifier used by the app (only recorded; not used for generation inside the kit)
	ScriptMode   string           // selection of the script prompt template (persisted so regeneration uses the same mode)
	TextModel    string           // the model that wrote this work's script (record only; set and read by the caller)
	ImageModel   string           // the model that drew this work's images (record only; set and read by the caller)
	Chapters     []Chapter        // the outline (the output of GenerateOutline)
	DesignSheets []DesignSheetRef // record of the design sheets used
	Panels       []Panel
	Pages        []PageArtifact
	CreatedAt, UpdatedAt time.Time
}

type Chapter struct {
	ID            string   // e.g. "ch01"
	Title         string
	Summary       string   // the points, aim and punchline this chapter covers
	SourceExcerpt string   // the matching part of the source text (quoted or summarised)
	PanelIDs      []string // linked after GenerateChapterScript runs
}

type Panel struct {
	ID           string            // stable ID for targeting regeneration (e.g. "ch01-p03")
	ChapterID    string
	Page         int
	Shot         string            // "close-up" | "medium" | "wide" | "bird's-eye" etc.
	Setting      string            // place and time of day (e.g. "the music room after school, evening")
	VisualAnchor string            // free-form description of the panel's staging and composition
	Characters   []PanelCharacter  // characters who appear (independent of who speaks)
	Dialogues    []DialogueLine    // supports multiple balloons
	Generation   *GenerationRecord // record of the generation result (the basis of regeneration)
}

type PanelCharacter struct {
	CharacterID string
	Prominence  string // "primary" | "secondary" | "background"
	Emotion     string
	Action      string // relationships go here as free text (e.g. "grabs Metan by the shoulders and shakes her")
	Position    string
}

type DialogueLine struct {
	SpeakerID string // empty means narration/caption
	Text      string
	Kind      string // "speech" | "thought" | "shout" | "narration" | "sfx"
}

type GenerationRecord struct {
	ImageURL, Prompt, NegativePrompt, Model string
	UsedSeed    int64
	GeneratedAt time.Time
}

type DesignSheetRef struct {
	CharacterID string // one per character (regenerating for the same ID overwrites it)
	ImageURL    string
	UsedSeed    int64
}

type PageArtifact struct {
	PageNumber int
	PanelIDs   []string          // the panels this page was built from (discarded when the composition changes)
	Generation *GenerationRecord
}
```

`Version` is `comic.StateSchemaVersion`. `store.Load` rejects a state with a newer schema (so an older kit does not break a newer state).

## Helpers for reading the state

| Method | Use |
| --- | --- |
| `PanelByID` / `ChapterByID` / `PageArtifactByNumber` | Lookup by ID or number |
| `PanelsForPage` / `PanelsForChapter` | Panels of the given page or chapter (in display order) |
| `PagesForChapter` | Page numbers carrying the given chapter's panels (the target of chapter-scoped batch composition) |
| `ReplaceChapterPanels` → `Repaginate` | Replace a chapter's panels and reassign pages (**call them in this order**) |
| `SetPageArtifact` / `SetDesignSheet` | Upsert for the same page number or the same character |

## Pagination rules

`Repaginate(maxPerPage)` uses `MaxPanelsPerPage` as the cap and **always breaks the page at a chapter boundary** (so no page mixes the last panels of one chapter with the opening panels of the next).

A `PageArtifact` whose composition changed is discarded at the same time, so no stale page image that no longer matches remains in the state. Artifacts of pages whose composition is unchanged are kept, so regenerating one chapter does not trigger recomposition of unrelated pages.

## Character normalization rules

Relationships between characters (who is doing what to whom) are expressed as free text in `PanelCharacter.Action`. As a prompt for a generative AI, natural language is reflected more faithfully than structured edges.

Script generation normalization does not trust the IDs the AI produces and mechanically corrects two things:

* A `character_id` missing from `characters.json` is demoted to `background` (drawn as a mob without a reference image). This prevents reference resolution from silently falling back to the default character and drawing the wrong person.
* A `speaker_id` missing from `characters.json` is converted to narration. This prevents the raw ID from being drawn where the speaker's name belongs.

## Limits on characters with attached reference images

The more reference images there are, the more character identities break down, and faces and outfits get swapped. So the count is capped in two stages.

| Limit | Constant | When it applies | What happens to the overflow |
| --- | --- | --- | --- |
| 3 primary + secondary per panel | `comic.MaxReferencedCharactersPerPanel` | Script generation normalization | Demoted to `background`. primary is kept first, and the order of appearance within the panel is preserved |
| 4 per page | `comic.MaxReferencedCharactersPerPage` | Page composition | No reference image (specified only in the prompt text). The characters appearing most on that page are kept, ties broken by first appearance |

Pages need a cap too because a page carries several panels, and composition also attaches each generated panel image as a layout guide. With only the per-panel cap, one composition would carry more than 10 references.
