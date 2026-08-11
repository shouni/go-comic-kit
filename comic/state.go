// Package comic は、go-comic-kit の中核データモデルを定義します。
//
// 中心となるのは MangaState（1作品の全状態を保持する永続ドキュメント）で、
// 章・コマ・台詞・デザインシート記録・ページ成果物と、それらを操作するメソッドを持ちます。
// 操作の契約（インターフェース）は ports パッケージが持ちます。
// 設計の詳細は README.md を参照してください。
package comic

import (
	"slices"
	"time"
)

// StateSchemaVersion は MangaState の現行スキーマバージョンです。
const StateSchemaVersion = 1

// DefaultMaxPanelsPerPage は1ページに載せるパネル数の既定値です（Repaginate 用）。
// ページ割りの規則そのものなので、生成側の上限（ports.Config）ではなくここが持ちます。
const DefaultMaxPanelsPerPage = 6

// MaxReferencedCharactersPerPanel は、1コマで参照画像（デザインシート）を添付する
// キャラクター（primary + secondary）の上限です。参照画像が増えるほど複数キャラクター同士の
// 同一性維持は難しくなり、顔や衣装の取り違えが起きます。これを超える登場者は
// ProminenceBackground（参照なしのモブ）として描かせます。
const MaxReferencedCharactersPerPanel = 3

// PanelCharacter.Prominence に指定できる値です。
// ProminencePrimary / ProminenceSecondary のキャラクターには参照画像（デザインシート）が
// 添付され、ProminenceBackground は参照なしのモブとして描画されます。
const (
	ProminencePrimary    = "primary"
	ProminenceSecondary  = "secondary"
	ProminenceBackground = "background"
)

// DialogueLine.Kind に指定できる値です。
const (
	DialogueKindSpeech    = "speech"
	DialogueKindThought   = "thought"
	DialogueKindShout     = "shout"
	DialogueKindNarration = "narration"
	DialogueKindSFX       = "sfx"
)

// MangaState は1作品の全状態を保持する永続ドキュメントです。
// GCS 等に保存されたこのドキュメントが唯一の真実源（source of truth）であり、
// 履歴一覧・詳細参照・パネル単位の再生成はすべてこのドキュメントを起点に行います。
type MangaState struct {
	Version      int              `json:"version"`
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	StyleMode    string           `json:"style_mode"`
	ScriptMode   string           `json:"script_mode,omitempty"`
	Chapters     []Chapter        `json:"chapters,omitempty"`
	DesignSheets []DesignSheetRef `json:"design_sheets,omitempty"`
	Panels       []Panel          `json:"panels"`
	Pages        []PageArtifact   `json:"pages,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// Chapter は作品の1章（台本生成の単位）を表します。
// 台本は「章立て（GenerateOutline）→ 章ごとのパネル生成（GenerateChapterScript）」の
// 2段階で生成します。リッチな Panel スキーマを一発で大量生成すると JSON の破綻率と
// 品質のブレが上がるため、1回の生成の複雑度を章単位に抑えます。
type Chapter struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	SourceExcerpt string   `json:"source_excerpt,omitempty"`
	PanelIDs      []string `json:"panel_ids,omitempty"`
}

// DesignSheetRef は、この作品の同一性アンカーとして使ったデザインシートの記録です。
type DesignSheetRef struct {
	CharacterID string `json:"character_id"`
	ImageURL    string `json:"image_url"`
	UsedSeed    int64  `json:"used_seed"`
}

// Panel は漫画の1コマを表します。
// 登場キャラクター（Characters）と発話（Dialogues）は独立しており、
// 発話しないキャラクターも登場者として第一級で表現できます。
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

// PanelCharacter は、コマへの1キャラクターの登場のしかたを表します。
// キャラクター間の関係性（誰が誰に何をしているか）は Action に自由記述します
// （例: "メタンの肩を掴んで揺さぶる"）。
type PanelCharacter struct {
	CharacterID string `json:"character_id"`
	Prominence  string `json:"prominence,omitempty"`
	Emotion     string `json:"emotion,omitempty"`
	Action      string `json:"action,omitempty"`
	Position    string `json:"position,omitempty"`
}

// DialogueLine は1つの吹き出し・ナレーションを表します。
// SpeakerID が空文字の場合はナレーション/キャプションとして扱います。
type DialogueLine struct {
	SpeakerID string `json:"speaker_id,omitempty"`
	Text      string `json:"text"`
	Kind      string `json:"kind,omitempty"`
}

// GenerationRecord は、画像がどの条件で生成されたかの完全な記録です。
// これがあることで「同条件で再生成」「シードだけ変えて再生成」が可能になります。
type GenerationRecord struct {
	ImageURL       string    `json:"image_url"`
	UsedSeed       int64     `json:"used_seed"`
	Prompt         string    `json:"prompt"`
	NegativePrompt string    `json:"negative_prompt,omitempty"`
	Model          string    `json:"model"`
	GeneratedAt    time.Time `json:"generated_at"`
}

// PageArtifact は複数パネルを1枚に合成したページ画像の記録です。
type PageArtifact struct {
	PageNumber int               `json:"page_number"`
	PanelIDs   []string          `json:"panel_ids"`
	Generation *GenerationRecord `json:"generation,omitempty"`
}

// PanelByID は指定 ID のパネルへのポインタを返します。見つからない場合は nil を返します。
// 返されたポインタ経由の変更は state に反映されます（パネル単位の再生成で使用）。
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

// UniqueCharacterIDs は、全パネルの登場キャラクター ID を重複なく登場順で返します。
// デザインシートの生成対象や参照画像の事前アップロード対象の列挙に使います。
func (s *MangaState) UniqueCharacterIDs() []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for i := range s.Panels {
		for _, pc := range s.Panels[i].Characters {
			if pc.CharacterID == "" {
				continue
			}
			if _, ok := seen[pc.CharacterID]; ok {
				continue
			}
			seen[pc.CharacterID] = struct{}{}
			ids = append(ids, pc.CharacterID)
		}
	}
	return ids
}

// ReferencedCharacterIDs は、このパネルで参照画像を添付すべきキャラクター ID を
// 重複なく登場順で返します。ProminenceBackground のキャラクター（モブ）は除外されます。
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

// UniqueReferencedCharacterIDs は、全パネルで参照画像を添付すべきキャラクター ID を
// 重複なく登場順で返します（ProminenceBackground は除外）。
// 参照画像の事前アップロード対象の列挙に使います。
func (s *MangaState) UniqueReferencedCharacterIDs() []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for i := range s.Panels {
		for _, id := range s.Panels[i].ReferencedCharacterIDs() {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}

// SetDesignSheet は指定キャラクターのデザインシート記録を追加または更新（upsert）します。
func (s *MangaState) SetDesignSheet(ref DesignSheetRef) {
	for i := range s.DesignSheets {
		if s.DesignSheets[i].CharacterID == ref.CharacterID {
			s.DesignSheets[i] = ref
			return
		}
	}
	s.DesignSheets = append(s.DesignSheets, ref)
}

// SetPageArtifact は指定ページ番号のページ画像記録を追加または更新（upsert）します。
func (s *MangaState) SetPageArtifact(artifact PageArtifact) {
	for i := range s.Pages {
		if s.Pages[i].PageNumber == artifact.PageNumber {
			s.Pages[i] = artifact
			return
		}
	}
	s.Pages = append(s.Pages, artifact)
}

// PageArtifactByNumber は指定ページ番号のページ画像記録を返します。無ければ nil を返します。
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

// PanelsForPage は指定ページ番号に属するパネルを登場順で返します。
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

// PanelsForChapter は指定章のコマを、state の並び順で返します。
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

// PagesForChapter は指定章のコマが載るページ番号を昇順で返します。
//
// Repaginate が章境界でページを割る（1ページに2章を混ぜない）ため、ページは必ず
// どれか1章だけに属します。この前提が崩れると、章単位の再合成が隣の章のページまで
// 巻き込むことになります。
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

// ChapterByID は指定 ID の章へのポインタを返します。見つからない場合は nil を返します。
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

// ReplaceChapterPanels は指定章のパネル群を置き換えます（冪等な章単位再生成の基礎）。
// 既存の同章パネルを取り除き、state.Chapters の章順を保った位置に newPanels を挿入します。
// 章の PanelIDs も更新します。指定 ID の章が存在しない場合は false を返し、何も変更しません。
func (s *MangaState) ReplaceChapterPanels(chapterID string, newPanels []Panel) bool {
	chapter := s.ChapterByID(chapterID)
	if chapter == nil {
		return false
	}

	// 章順にパネルを並べ直す。対象章の位置に newPanels を差し込み、
	// どの章にも属さないパネルは末尾に保持する。
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
