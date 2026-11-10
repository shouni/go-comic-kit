package comic

import "slices"

// Repaginate renumbers the Page of every panel. A page holds at most maxPerPage panels,
// and a new page always starts where the chapter changes (a page that mixes the last
// panels of one chapter with the opening panels of the next cannot be read as a page).
// Calling it after a chapter is generated or regenerated keeps pagination deterministic.
// A maxPerPage of 0 or less uses DefaultMaxPanelsPerPage.
//
// Once pagination changes, existing PageArtifacts no longer match what is on the page,
// so the records of pages whose panels changed and of pages left with no panels are
// removed. Records of pages whose composition is unchanged stay, so regenerating one
// chapter does not force unrelated page images to be rebuilt.
func (s *MangaState) Repaginate(maxPerPage int) {
	if s == nil {
		return
	}
	if maxPerPage <= 0 {
		maxPerPage = DefaultMaxPanelsPerPage
	}

	page := 0
	countOnPage := 0
	prevChapterID := ""
	for i := range s.Panels {
		if page == 0 || countOnPage >= maxPerPage || s.Panels[i].ChapterID != prevChapterID {
			page++
			countOnPage = 0
		}
		prevChapterID = s.Panels[i].ChapterID
		s.Panels[i].Page = page
		countOnPage++
	}

	s.pruneStalePageArtifacts()
}

// pruneStalePageArtifacts removes the PageArtifacts that no longer match the current panel composition.
func (s *MangaState) pruneStalePageArtifacts() {
	if len(s.Pages) == 0 {
		return
	}

	current := make(map[int][]string)
	for i := range s.Panels {
		panel := &s.Panels[i]
		current[panel.Page] = append(current[panel.Page], panel.ID)
	}

	s.Pages = slices.DeleteFunc(s.Pages, func(artifact PageArtifact) bool {
		return !slices.Equal(artifact.PanelIDs, current[artifact.PageNumber])
	})
}
