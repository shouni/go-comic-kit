package operations

import (
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
)

func TestResolveSeedChain(t *testing.T) {
	t.Parallel()

	characterSeed := int64(42)
	characters, err := characterkit.NewCharacters([]ports.Character{
		{ID: "tsumugi", Name: "つむぎ", ReferenceURL: "gs://bucket/tsumugi.png", VisualCues: []string{"緑髪"}, Seed: &characterSeed},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	primary := []ports.PanelCharacter{{CharacterID: "tsumugi", Prominence: ports.ProminencePrimary}}

	t.Run("明示指定が最優先", func(t *testing.T) {
		explicit := int64(7)
		got := resolveSeedChain(&explicit, &ports.GenerationRecord{UsedSeed: 99}, characters, primary)
		if got == nil || *got != 7 {
			t.Errorf("seed = %v, want 7", got)
		}
	})

	t.Run("前回の UsedSeed を再利用", func(t *testing.T) {
		got := resolveSeedChain(nil, &ports.GenerationRecord{UsedSeed: 99}, characters, primary)
		if got == nil || *got != 99 {
			t.Errorf("seed = %v, want 99", got)
		}
	})

	t.Run("主役キャラクターの Seed", func(t *testing.T) {
		got := resolveSeedChain(nil, nil, characters, primary)
		if got == nil || *got != characterSeed {
			t.Errorf("seed = %v, want %d", got, characterSeed)
		}
	})

	// 何も無い場合に nil を返すと、下層は API 側のランダムシードで生成し、その値は
	// レスポンスに返らないため UsedSeed が 0 で記録される。次回の再生成は「前回の
	// UsedSeed」を採用できず、また別の絵になる。ここで採番することでその連鎖を断つ。
	t.Run("何も無ければ新規採番する", func(t *testing.T) {
		got := resolveSeedChain(nil, nil, characters, nil)
		if got == nil {
			t.Fatal("seed = nil, want a freshly drawn seed")
		}
		if *got <= 0 {
			t.Errorf("seed = %d, want a positive value", *got)
		}
	})

	// 採番したシードが毎回同じだと、シード未指定の生成がすべて同じ絵に寄ってしまう。
	t.Run("採番は呼び出しごとに変わる", func(t *testing.T) {
		seen := make(map[int64]struct{}, 8)
		for range 8 {
			got := resolveSeedChain(nil, nil, nil, nil)
			if got == nil {
				t.Fatal("seed = nil, want a freshly drawn seed")
			}
			seen[*got] = struct{}{}
		}
		if len(seen) == 1 {
			t.Error("採番されたシードが全て同じです")
		}
	})

	// UsedSeed が 0 の記録は「未設定」と区別がつかないため、再利用してはいけない。
	t.Run("UsedSeed が 0 の記録は再利用しない", func(t *testing.T) {
		got := resolveSeedChain(nil, &ports.GenerationRecord{UsedSeed: 0}, characters, primary)
		if got == nil || *got != characterSeed {
			t.Errorf("seed = %v, want the character seed %d", got, characterSeed)
		}
	})
}
