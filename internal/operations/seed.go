package operations

import (
	"math"
	"math/rand/v2"

	"github.com/shouni/go-comic-kit/comic"
)

// resolveSeedChain は「明示指定 > 前回の UsedSeed > 主役キャラクターの Seed > 新規採番」の
// 優先順位で生成シードを決定します。パネル生成とページ合成で共通の解決規則です。
//
// 最後に新規採番するのは、シードを渡さずに生成すると API 側がランダムに選び、その値が
// レスポンスに返らないためです。GenerationRecord.UsedSeed は 0 のまま記録され、次に
// この関数へ渡しても「前回の UsedSeed」として採用されません（0 は「未設定」と区別が
// つかない）。結果として、シードを一度も明示しない作品では「同条件での再生成」が
// 永久に別の絵になります。ここで採番しておけば、記録された値でいつでも再現できます。
func resolveSeedChain(explicit *int64, prev *comic.GenerationRecord, characters *comic.Characters, panelChars []comic.PanelCharacter) *int64 {
	if explicit != nil {
		return explicit
	}
	if prev != nil && prev.UsedSeed != 0 {
		seed := prev.UsedSeed
		return &seed
	}
	if characters != nil {
		for _, pc := range panelChars {
			if pc.Prominence != comic.ProminencePrimary {
				continue
			}
			if char := characters.GetCharacter(pc.CharacterID); char != nil && char.Seed != nil {
				return char.Seed
			}
		}
	}
	return newSeed()
}

// newSeed は新しい生成シードを返します。
// 下層（go-gemini-client）が int32 の範囲外を弾くため、範囲内に収めます。
func newSeed() *int64 {
	seed := rand.Int64N(math.MaxInt32)
	return &seed
}
