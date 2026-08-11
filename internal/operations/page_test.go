package operations

import (
	"context"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

// charPrepared / panelPrepared は事前準備が1回以上呼ばれたかを返します。
// --- Helpers ---

func pageTestState() *comic.MangaState {
	return &comic.MangaState{
		Version: comic.StateSchemaVersion,
		Title:   "夜明けのデプロイ",
		Panels: []comic.Panel{
			{
				ID:           "ch01-p01",
				ChapterID:    "ch01",
				Page:         1,
				Shot:         "wide",
				Setting:      "放課後の音楽室",
				VisualAnchor: "sunset light, dynamic angle",
				Characters: []comic.PanelCharacter{
					{CharacterID: "zundamon", Prominence: comic.ProminencePrimary, Emotion: "驚き", Position: "left"},
				},
				Dialogues: []comic.DialogueLine{
					{SpeakerID: "zundamon", Text: "なんなのだ！？", Kind: comic.DialogueKindShout},
					{Text: "その日、すべてが変わった", Kind: comic.DialogueKindNarration},
				},
				Generation: &comic.GenerationRecord{ImageURL: "gs://b/panels/p01.png", UsedSeed: 11},
			},
			{
				ID:        "ch01-p02",
				ChapterID: "ch01",
				Page:      1,
				Characters: []comic.PanelCharacter{
					{CharacterID: "metan", Prominence: comic.ProminencePrimary, Emotion: "冷静"},
					{CharacterID: "zundamon", Prominence: comic.ProminenceSecondary},
				},
				Dialogues: []comic.DialogueLine{
					{SpeakerID: "metan", Text: "落ち着きなさい。", Kind: comic.DialogueKindSpeech},
				},
			},
			{
				ID:        "ch02-p01",
				ChapterID: "ch02",
				Page:      2, // 別ページ（対象外）
				Characters: []comic.PanelCharacter{
					{CharacterID: "zundamon", Prominence: comic.ProminencePrimary},
				},
			},
		},
	}
}

func newPageRunner(t *testing.T, prompt ports.PagePrompt) (*PageImageRunner, *mockImageGenerator, *mockWriter) {
	t.Helper()
	zundaSeed := int64(10001)
	cm, err := characterkit.NewCharacters([]comic.Character{
		{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://b/zunda.png", VisualCues: []string{"green hair"}, Seed: &zundaSeed, IsDefault: true},
		{ID: "metan", Name: "めたん", ReferenceURL: "gs://b/metan.png", VisualCues: []string{"purple hair"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	gen := &mockImageGenerator{}
	writer := &mockWriter{}
	r := NewPageImageRunner(PageImageRunnerArgs{
		Characters: cm,
		Prompt:     prompt,
		Generator:  gen,
		Writer:     writer,
	})
	return r, gen, writer
}

// --- Tests ---

func TestComposePageBuildsLayoutAndReferences(t *testing.T) {
	t.Parallel()
	prompt := &fakePagePrompt{}
	r, gen, writer := newPageRunner(t, prompt)
	state := pageTestState()

	state, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{OutputDir: "gs://bucket/out", Model: "page-model"})
	if err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}

	// 参照: キャラ2（zundamon, metan）+ 生成済みパネル1（p01のみ）
	if len(gen.lastReq.Images) != 3 {
		t.Fatalf("Images = %+v, want 3 references", gen.lastReq.Images)
	}

	// プロンプト本文はアプリ側の実装が持つので、ここで見るのは
	// 「実装へ渡した構造データ」と「返った3本をそのまま載せたか」だけ。
	d := prompt.data
	if d == nil {
		t.Fatal("PagePrompt was not called")
	}
	// 参照番号は添付順と一致していなければ、モデルは別人の参照画像を見て描く。
	if d.CharacterFile["zundamon"] != 1 || d.CharacterFile["metan"] != 2 {
		t.Errorf("CharacterFile = %v, want zundamon=1 / metan=2 (添付順)", d.CharacterFile)
	}
	if d.PanelFile["ch01-p01"] != 3 {
		t.Errorf("PanelFile = %v, want the generated panel at 3", d.PanelFile)
	}
	// このページのコマだけが渡り、別ページのコマは混ざらない。
	if got := panelIDs(d.Panels); len(got) != 2 || got[0] != "ch01-p01" || got[1] != "ch01-p02" {
		t.Errorf("Panels = %v, want only page 1's panels in order", got)
	}
	if gen.lastReq.SystemPrompt != fakeSystemPrompt || gen.lastReq.NegativePrompt != fakeNegativePrompt {
		t.Errorf("system/negative prompt = %q / %q, want them passed through unchanged",
			gen.lastReq.SystemPrompt, gen.lastReq.NegativePrompt)
	}

	if gen.lastReq.AspectRatio != "3:4" || gen.lastReq.ImageSize != "2K" {
		t.Errorf("AspectRatio/ImageSize = %q/%q, want 3:4/2K", gen.lastReq.AspectRatio, gen.lastReq.ImageSize)
	}

	// 先頭パネルの主役キャラの Seed が既定として使われる
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 10001 {
		t.Errorf("Seed = %v, want primary character seed 10001", gen.lastReq.Seed)
	}

	// PageArtifact の記録
	artifact := state.PageArtifactByNumber(1)
	if artifact == nil || artifact.Generation == nil {
		t.Fatal("PageArtifact not recorded")
	}
	if artifact.Generation.ImageURL != writer.lastPath || !strings.Contains(artifact.Generation.ImageURL, "comic_page_1.png") {
		t.Errorf("ImageURL = %q, want indexed page path", artifact.Generation.ImageURL)
	}
	if len(artifact.PanelIDs) != 2 || artifact.PanelIDs[0] != "ch01-p01" {
		t.Errorf("PanelIDs = %v, want the page's panel IDs", artifact.PanelIDs)
	}
}

func TestComposePageUpsertsArtifactAndReusesSeed(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPageRunner(t, &fakePagePrompt{})
	state := pageTestState()
	state.Pages = []comic.PageArtifact{
		{PageNumber: 1, Generation: &comic.GenerationRecord{ImageURL: "gs://old.png", UsedSeed: 777}},
	}

	state, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{Model: "page-model"})
	if err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}

	// 前回の UsedSeed を再利用
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 777 {
		t.Errorf("Seed = %v, want previous 777", gen.lastReq.Seed)
	}
	// upsert（重複エントリを作らない）
	if len(state.Pages) != 1 {
		t.Errorf("Pages = %+v, want 1 entry (upsert)", state.Pages)
	}
	if state.Pages[0].Generation.ImageURL == "gs://old.png" {
		t.Error("PageArtifact was not updated")
	}
}

func TestComposePageEditMode(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPageRunner(t, &fakePagePrompt{})
	state := pageTestState()
	state.Pages = []comic.PageArtifact{
		{PageNumber: 1, Generation: &comic.GenerationRecord{ImageURL: "gs://b/pages/page1.png"}},
	}

	_, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{
		EditPrompt: "1コマ目の空を夕焼けにする", Model: "page-model"})
	if err != nil {
		t.Fatalf("ComposePage(edit) failed: %v", err)
	}

	if len(gen.lastReq.Images) != 1 || gen.lastReq.Images[0].ReferenceURL != "gs://b/pages/page1.png" {
		t.Errorf("Images = %+v, want existing page image only", gen.lastReq.Images)
	}
	if !strings.Contains(gen.lastReq.Prompt, "FAKE-PAGE-EDIT") || !strings.Contains(gen.lastReq.Prompt, "夕焼け") {
		t.Errorf("Prompt = %q, want edit instruction", gen.lastReq.Prompt)
	}
}

func TestComposePageEditModeRequiresExistingImage(t *testing.T) {
	t.Parallel()
	r, _, _ := newPageRunner(t, &fakePagePrompt{})

	_, err := r.ComposePage(context.Background(), pageTestState(), 1, ports.GenerateOptions{EditPrompt: "変更", Model: "page-model"})
	if err == nil || !strings.Contains(err.Error(), "編集対象") {
		t.Errorf("err = %v, want missing-image error", err)
	}
}

func TestComposePageEmptyPageFails(t *testing.T) {
	t.Parallel()
	r, _, _ := newPageRunner(t, &fakePagePrompt{})

	if _, err := r.ComposePage(context.Background(), pageTestState(), 99, ports.GenerateOptions{Model: "page-model"}); err == nil {
		t.Error("ComposePage(empty page) succeeded, want error")
	}
	if _, err := r.ComposePage(context.Background(), nil, 1, ports.GenerateOptions{Model: "page-model"}); err == nil {
		t.Error("ComposePage(nil state) succeeded, want error")
	}
}
