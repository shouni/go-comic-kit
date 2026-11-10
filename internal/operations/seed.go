package operations

import (
	"math"
	"math/rand/v2"

	"github.com/shouni/go-comic-kit/comic"
)

// resolveSeedChain decides the generation seed in the priority "explicit > previous
// UsedSeed > lead character's Seed > newly drawn". It is the resolution rule shared by
// panel generation and page composition.
//
// It draws a new one last because, when generating without a seed, the API picks one at
// random and does not return it in the response. GenerationRecord.UsedSeed would be
// recorded as 0, and passing it to this function next time would not adopt it as the
// "previous UsedSeed" (0 cannot be told apart from "unset"). As a result, in a work
// whose seeds are never specified, "regenerate under the same conditions" would always
// draw a different picture. Drawing here means the recorded value can always reproduce it.
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

// newSeed returns a new generation seed.
// The layer below (genai-kit) rejects values outside int32, so it stays within that range.
func newSeed() *int64 {
	seed := rand.Int64N(math.MaxInt32)
	return &seed
}
