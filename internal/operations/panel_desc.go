package operations

import "github.com/shouni/go-comic-kit/ports"

// backgroundExtraDesc は background（モブ）キャラクター1人分の記述
// （"character_id (action)" 形式）を構築します。
func backgroundExtraDesc(pc *ports.PanelCharacter) string {
	desc := pc.CharacterID
	if pc.Action != "" {
		desc += " (" + pc.Action + ")"
	}
	return desc
}
