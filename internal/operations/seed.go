package operations

import "github.com/shouni/go-comic-kit/ports"

// resolveSeedChain は「明示指定 > 前回の UsedSeed > 主役キャラクターの Seed > なし」の
// 優先順位で生成シードを決定します。パネル生成とページ合成で共通の解決規則です。
func resolveSeedChain(explicit *int64, prev *ports.GenerationRecord, characters *ports.Characters, panelChars []ports.PanelCharacter) *int64 {
	if explicit != nil {
		return explicit
	}
	if prev != nil && prev.UsedSeed != 0 {
		seed := prev.UsedSeed
		return &seed
	}
	if characters != nil {
		for _, pc := range panelChars {
			if pc.Prominence != ports.ProminencePrimary {
				continue
			}
			if char := characters.GetCharacter(pc.CharacterID); char != nil && char.Seed != nil {
				return char.Seed
			}
		}
	}
	return nil
}
