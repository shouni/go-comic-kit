package comic

import "slices"

// Repaginate は全パネルの Page 番号を振り直します。1ページあたりのコマ数は maxPerPage を
// 上限とし、加えて章が変わるところでは必ず改ページします（前章の残りコマと次章の冒頭コマが
// 同じページに同居すると、ページとして読めなくなるため）。章の生成・再生成後に呼ぶことで、
// ページ割りを常に決定的に保ちます。maxPerPage が 0 以下の場合は DefaultMaxPanelsPerPage を
// 使います。
//
// ページ割りが変わると既存の PageArtifact は実体とずれるため、コマ構成が変わったページと
// コマが無くなったページの記録は取り除きます。構成が変わっていないページの記録は残るので、
// 1章だけを再生成しても無関係なページ画像を作り直す必要はありません。
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

// pruneStalePageArtifacts は、現在のコマ構成と一致しなくなった PageArtifact を取り除きます。
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
