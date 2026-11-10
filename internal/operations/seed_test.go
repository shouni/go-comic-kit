package operations

import (
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"
)

func TestResolveSeedChain(t *testing.T) {
	t.Parallel()

	characterSeed := int64(42)
	characters, err := characterkit.NewCharacters([]comic.Character{
		{ID: "tsumugi", Name: "つむぎ", ReferenceURL: "gs://bucket/tsumugi.png", VisualCues: []string{"緑髪"}, Seed: &characterSeed},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	primary := []comic.PanelCharacter{{CharacterID: "tsumugi", Prominence: comic.ProminencePrimary}}

	t.Run("explicit value wins", func(t *testing.T) {
		explicit := int64(7)
		got := resolveSeedChain(&explicit, &comic.GenerationRecord{UsedSeed: 99}, characters, primary)
		if got == nil || *got != 7 {
			t.Errorf("seed = %v, want 7", got)
		}
	})

	t.Run("reuses the previous UsedSeed", func(t *testing.T) {
		got := resolveSeedChain(nil, &comic.GenerationRecord{UsedSeed: 99}, characters, primary)
		if got == nil || *got != 99 {
			t.Errorf("seed = %v, want 99", got)
		}
	})

	t.Run("lead character Seed", func(t *testing.T) {
		got := resolveSeedChain(nil, nil, characters, primary)
		if got == nil || *got != characterSeed {
			t.Errorf("seed = %v, want %d", got, characterSeed)
		}
	})

	// Returning nil when nothing is available makes the layer below generate with the
	// API's random seed, which the response does not return, so UsedSeed is recorded as 0.
	// The next regeneration cannot adopt the "previous UsedSeed" and draws yet another
	// picture. Drawing here breaks that chain.
	t.Run("draws a new seed when nothing is available", func(t *testing.T) {
		got := resolveSeedChain(nil, nil, characters, nil)
		if got == nil {
			t.Fatal("seed = nil, want a freshly drawn seed")
		}
		if *got <= 0 {
			t.Errorf("seed = %d, want a positive value", *got)
		}
	})

	// If the drawn seed were the same every time, every generation without a seed would converge on the same picture.
	t.Run("drawn seed varies per call", func(t *testing.T) {
		seen := make(map[int64]struct{}, 8)
		for range 8 {
			got := resolveSeedChain(nil, nil, nil, nil)
			if got == nil {
				t.Fatal("seed = nil, want a freshly drawn seed")
			}
			seen[*got] = struct{}{}
		}
		if len(seen) == 1 {
			t.Error("every drawn seed is the same")
		}
	})

	// A record with UsedSeed 0 cannot be told apart from "unset", so it must not be reused.
	t.Run("does not reuse a record with UsedSeed 0", func(t *testing.T) {
		got := resolveSeedChain(nil, &comic.GenerationRecord{UsedSeed: 0}, characters, primary)
		if got == nil || *got != characterSeed {
			t.Errorf("seed = %v, want the character seed %d", got, characterSeed)
		}
	})
}
