// Package ports は、go-comic-kit の中核データモデルと契約を定義します。
//
// 中心となるのは MangaState（1作品の全状態を保持する永続ドキュメント）です。
// 設計の詳細は docs/comic-kit-design.md を参照してください。
package ports

import "time"

// StateSchemaVersion は MangaState の現行スキーマバージョンです。
const StateSchemaVersion = 1

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
	DesignSheets []DesignSheetRef `json:"design_sheets,omitempty"`
	Panels       []Panel          `json:"panels"`
	Pages        []PageArtifact   `json:"pages,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
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
