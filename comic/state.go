// Package comic defines the core data model of go-comic-kit.
//
// At its center is MangaState (the persisted document holding every piece of state of
// one work): chapters, panels, dialogue, design-sheet records and page artifacts, with
// the methods that operate on them. The operation contracts (interfaces) live in the
// ports package. See README.md for the design.
package comic

import (
	"slices"
	"time"
)

// StateSchemaVersion is the current schema version of MangaState.
const StateSchemaVersion = 1

// DefaultMaxPanelsPerPage is the default number of panels on one page (for Repaginate).
// It is the pagination rule itself, so it lives here rather than among the generation
// limits (ports.Config).
const DefaultMaxPanelsPerPage = 6

// MaxReferencedCharactersPerPanel is the maximum number of characters (primary +
// secondary) whose reference images (design sheets) are attached to one panel. The more
// reference images there are, the harder it gets to keep several characters' identities
// apart, and faces and outfits get swapped. Characters beyond it are drawn as
// ProminenceBackground (mobs without a reference).
const MaxReferencedCharactersPerPanel = 3

// MaxReferencedCharactersPerPage is the maximum number of characters whose reference
// images (design sheets) are attached when composing one page.
//
// The per-panel cap (MaxReferencedCharactersPerPanel) alone does not protect page
// composition. A page holds up to DefaultMaxPanelsPerPage panels, and composition also
// attaches each generated panel image as a layout guide, so without a cap one
// composition carries more than 10 references. Pages need a cap for exactly the same
// reason panels do: the more references, the more identity breaks down.
//
// The overflow gets no reference (it is specified only in the prompt text). The
// characters kept are the ones appearing most on that page, ties broken by first
// appearance.
const MaxReferencedCharactersPerPage = 4

// Values accepted by PanelCharacter.Prominence.
// ProminencePrimary / ProminenceSecondary characters get their reference images (design
// sheets) attached; ProminenceBackground ones are drawn as mobs without a reference.
const (
	ProminencePrimary    = "primary"
	ProminenceSecondary  = "secondary"
	ProminenceBackground = "background"
)

// Values accepted by DialogueLine.Kind.
const (
	DialogueKindSpeech    = "speech"
	DialogueKindThought   = "thought"
	DialogueKindShout     = "shout"
	DialogueKindNarration = "narration"
	DialogueKindSFX       = "sfx"
)

// MangaState is the persisted document holding every piece of state of one work.
// This document, stored on GCS or elsewhere, is the single source of truth: history
// listings, detail views and per-panel regeneration all start from it.
type MangaState struct {
	Version      int              `json:"version"`
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	StyleMode    string           `json:"style_mode"`
	ScriptMode   string           `json:"script_mode,omitempty"`
	TextModel    string           `json:"text_model,omitempty"`
	ImageModel   string           `json:"image_model,omitempty"`
	Chapters     []Chapter        `json:"chapters,omitempty"`
	DesignSheets []DesignSheetRef `json:"design_sheets,omitempty"`
	Panels       []Panel          `json:"panels"`
	Pages        []PageArtifact   `json:"pages,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// Chapter is one chapter of a work (the unit of script generation).
// The script is generated in two stages: the outline (GenerateOutline), then the panels
// of each chapter (GenerateChapterScript). Generating the rich Panel schema in bulk in
// one shot raises the rate of broken JSON and the variance in quality, so one
// generation's complexity is held to a chapter.
type Chapter struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	SourceExcerpt string   `json:"source_excerpt,omitempty"`
	PanelIDs      []string `json:"panel_ids,omitempty"`
}

// DesignSheetRef records a design sheet used as an identity anchor for this work.
type DesignSheetRef struct {
	CharacterID string `json:"character_id"`
	ImageURL    string `json:"image_url"`
	UsedSeed    int64  `json:"used_seed"`
}

// Panel is one panel of the comic.
// The characters who appear (Characters) and the speech (Dialogues) are independent, so
// a character who does not speak is still a first-class participant.
type Panel struct {
	ID           string            `json:"id"`
	ChapterID    string            `json:"chapter_id,omitempty"`
	Page         int               `json:"page"`
	Shot         string            `json:"shot,omitempty"`
	Setting      string            `json:"setting,omitempty"`
	VisualAnchor string            `json:"visual_anchor"`
	Characters   []PanelCharacter  `json:"characters"`
	Dialogues    []DialogueLine    `json:"dialogues"`
	Generation   *GenerationRecord `json:"generation,omitempty"`
}

// PanelCharacter describes how one character appears in a panel.
// Relationships between characters (who is doing what to whom) are free text in Action
// (for example, "grabs Metan by the shoulders and shakes her").
type PanelCharacter struct {
	CharacterID string `json:"character_id"`
	Prominence  string `json:"prominence,omitempty"`
	Emotion     string `json:"emotion,omitempty"`
	Action      string `json:"action,omitempty"`
	Position    string `json:"position,omitempty"`
}

// DialogueLine is one speech balloon or narration.
// An empty SpeakerID is treated as narration or a caption.
type DialogueLine struct {
	SpeakerID string `json:"speaker_id,omitempty"`
	Text      string `json:"text"`
	Kind      string `json:"kind,omitempty"`
}

// GenerationRecord is the complete record of the conditions an image was generated under.
// It is what makes "regenerate under the same conditions" and "regenerate with only the
// seed changed" possible.
type GenerationRecord struct {
	ImageURL       string    `json:"image_url"`
	UsedSeed       int64     `json:"used_seed"`
	Prompt         string    `json:"prompt"`
	NegativePrompt string    `json:"negative_prompt,omitempty"`
	Model          string    `json:"model"`
	GeneratedAt    time.Time `json:"generated_at"`
}

// PageArtifact records a page image composed from several panels.
type PageArtifact struct {
	PageNumber int               `json:"page_number"`
	PanelIDs   []string          `json:"panel_ids"`
	Generation *GenerationRecord `json:"generation,omitempty"`
}

// PanelByID returns a pointer to the panel with the given ID, or nil if there is none.
// Changes made through the returned pointer are reflected in the state (used by
// per-panel regeneration).
func (s *MangaState) PanelByID(id string) *Panel {
	if s == nil {
		return nil
	}
	for i := range s.Panels {
		if s.Panels[i].ID == id {
			return &s.Panels[i]
		}
	}
	return nil
}

// ReferencedCharacterIDs returns the IDs of the characters whose reference images should
// be attached for this panel, without duplicates and in order of appearance.
// ProminenceBackground characters (mobs) are excluded.
func (p Panel) ReferencedCharacterIDs() []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, pc := range p.Characters {
		if pc.CharacterID == "" || pc.Prominence == ProminenceBackground {
			continue
		}
		if _, ok := seen[pc.CharacterID]; ok {
			continue
		}
		seen[pc.CharacterID] = struct{}{}
		ids = append(ids, pc.CharacterID)
	}
	return ids
}

// SetDesignSheet adds or updates (upserts) the design-sheet record of the given character.
func (s *MangaState) SetDesignSheet(ref DesignSheetRef) {
	for i := range s.DesignSheets {
		if s.DesignSheets[i].CharacterID == ref.CharacterID {
			s.DesignSheets[i] = ref
			return
		}
	}
	s.DesignSheets = append(s.DesignSheets, ref)
}

// SetPageArtifact adds or updates (upserts) the page-image record of the given page number.
func (s *MangaState) SetPageArtifact(artifact PageArtifact) {
	for i := range s.Pages {
		if s.Pages[i].PageNumber == artifact.PageNumber {
			s.Pages[i] = artifact
			return
		}
	}
	s.Pages = append(s.Pages, artifact)
}

// PageArtifactByNumber returns the page-image record of the given page number, or nil if there is none.
func (s *MangaState) PageArtifactByNumber(page int) *PageArtifact {
	if s == nil {
		return nil
	}
	for i := range s.Pages {
		if s.Pages[i].PageNumber == page {
			return &s.Pages[i]
		}
	}
	return nil
}

// PanelsForPage returns the panels on the given page, in order of appearance.
func (s *MangaState) PanelsForPage(page int) []Panel {
	if s == nil {
		return nil
	}
	var panels []Panel
	for i := range s.Panels {
		if s.Panels[i].Page == page {
			panels = append(panels, s.Panels[i])
		}
	}
	return panels
}

// PanelsForChapter returns the panels of the given chapter, in the state's order.
func (s *MangaState) PanelsForChapter(chapterID string) []Panel {
	if s == nil {
		return nil
	}
	panels := make([]Panel, 0, len(s.Panels))
	for _, p := range s.Panels {
		if p.ChapterID == chapterID {
			panels = append(panels, p)
		}
	}
	return panels
}

// PagesForChapter returns, in ascending order, the page numbers carrying the given
// chapter's panels.
//
// Repaginate breaks pages at chapter boundaries (never mixing two chapters on one page),
// so every page belongs to exactly one chapter. If that assumption breaks,
// chapter-scoped recomposition drags in the neighbouring chapter's pages.
func (s *MangaState) PagesForChapter(chapterID string) []int {
	if s == nil {
		return nil
	}
	seen := make(map[int]struct{})
	pages := make([]int, 0, len(s.Panels))
	for _, p := range s.Panels {
		if p.ChapterID != chapterID || p.Page <= 0 {
			continue
		}
		if _, ok := seen[p.Page]; ok {
			continue
		}
		seen[p.Page] = struct{}{}
		pages = append(pages, p.Page)
	}
	slices.Sort(pages)
	return pages
}

// ChapterByID returns a pointer to the chapter with the given ID, or nil if there is none.
func (s *MangaState) ChapterByID(id string) *Chapter {
	if s == nil {
		return nil
	}
	for i := range s.Chapters {
		if s.Chapters[i].ID == id {
			return &s.Chapters[i]
		}
	}
	return nil
}

// ReplaceChapterPanels replaces the panels of the given chapter (the basis of idempotent
// chapter-level regeneration). It removes the chapter's existing panels and inserts
// newPanels at the position that keeps the chapter order of state.Chapters. It also
// updates the chapter's PanelIDs. If no chapter has the given ID, it returns false and
// changes nothing.
//
// PageArtifacts that carried the replaced chapter's panels are removed here. Panel IDs
// are decided by position within the chapter (ch01-p01 …), so rebuilding with the same
// panel count reproduces the original ID sequence, and Repaginate's "drop only pages
// whose composition changed" check would keep the stale page images. Only this function
// knows that the contents were swapped.
func (s *MangaState) ReplaceChapterPanels(chapterID string, newPanels []Panel) bool {
	chapter := s.ChapterByID(chapterID)
	if chapter == nil {
		return false
	}

	replaced := make(map[string]bool)
	for i := range s.Panels {
		if s.Panels[i].ChapterID == chapterID {
			replaced[s.Panels[i].ID] = true
		}
	}
	s.Pages = slices.DeleteFunc(s.Pages, func(artifact PageArtifact) bool {
		return slices.ContainsFunc(artifact.PanelIDs, func(id string) bool { return replaced[id] })
	})

	// Reorder the panels by chapter: insert newPanels at the target chapter's position,
	// and keep panels that belong to no chapter at the end.
	rebuilt := make([]Panel, 0, len(s.Panels)+len(newPanels))
	for i := range s.Chapters {
		if s.Chapters[i].ID == chapterID {
			rebuilt = append(rebuilt, newPanels...)
			continue
		}
		for j := range s.Panels {
			if s.Panels[j].ChapterID == s.Chapters[i].ID {
				rebuilt = append(rebuilt, s.Panels[j])
			}
		}
	}
	for j := range s.Panels {
		if s.Panels[j].ChapterID == "" || s.ChapterByID(s.Panels[j].ChapterID) == nil {
			rebuilt = append(rebuilt, s.Panels[j])
		}
	}
	s.Panels = rebuilt

	ids := make([]string, len(newPanels))
	for i := range newPanels {
		ids[i] = newPanels[i].ID
	}
	chapter.PanelIDs = ids
	return true
}
