package operations

import (
	"context"
	"strings"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/internal/prompts"
	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

// charPrepared / panelPrepared は事前準備が1回以上呼ばれたかを返します。
// --- Helpers ---

func pageTestState() *ports.MangaState {
	return &ports.MangaState{
		Version: ports.StateSchemaVersion,
		Title:   "夜明けのデプロイ",
		Panels: []ports.Panel{
			{
				ID:           "ch01-p01",
				ChapterID:    "ch01",
				Page:         1,
				Shot:         "wide",
				Setting:      "放課後の音楽室",
				VisualAnchor: "sunset light, dynamic angle",
				Characters: []ports.PanelCharacter{
					{CharacterID: "zundamon", Prominence: ports.ProminencePrimary, Emotion: "驚き", Position: "left"},
				},
				Dialogues: []ports.DialogueLine{
					{SpeakerID: "zundamon", Text: "なんなのだ！？", Kind: ports.DialogueKindShout},
					{Text: "その日、すべてが変わった", Kind: ports.DialogueKindNarration},
				},
				Generation: &ports.GenerationRecord{ImageURL: "gs://b/panels/p01.png", UsedSeed: 11},
			},
			{
				ID:        "ch01-p02",
				ChapterID: "ch01",
				Page:      1,
				Characters: []ports.PanelCharacter{
					{CharacterID: "metan", Prominence: ports.ProminencePrimary, Emotion: "冷静"},
					{CharacterID: "zundamon", Prominence: ports.ProminenceSecondary},
				},
				Dialogues: []ports.DialogueLine{
					{SpeakerID: "metan", Text: "落ち着きなさい。", Kind: ports.DialogueKindSpeech},
				},
			},
			{
				ID:        "ch02-p01",
				ChapterID: "ch02",
				Page:      2, // 別ページ（対象外）
				Characters: []ports.PanelCharacter{
					{CharacterID: "zundamon", Prominence: ports.ProminencePrimary},
				},
			},
		},
	}
}

func newPageRunner(t *testing.T, prompt ports.PagePrompt) (*PageImageRunner, *mockFusionGenerator, *mockWriter) {
	t.Helper()
	zundaSeed := int64(10001)
	cm, err := characterkit.NewCharacters([]ports.Character{
		{ID: "zundamon", Name: "ずんだもん", ReferenceURL: "gs://b/zunda.png", VisualCues: []string{"green hair"}, Seed: &zundaSeed, IsDefault: true},
		{ID: "metan", Name: "めたん", ReferenceURL: "gs://b/metan.png", VisualCues: []string{"purple hair"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	gen := &mockFusionGenerator{}
	writer := &mockWriter{}
	r := NewPageImageRunner(PageImageRunnerArgs{
		Characters:  cm,
		Prompt:      prompt,
		Generator:   gen,
		Writer:      writer,
		Model:       "page-model",
		StyleSuffix: "cinematic style",
	})
	return r, gen, writer
}

// --- Tests ---

func TestComposePageBuildsLayoutAndReferences(t *testing.T) {
	t.Parallel()
	r, gen, writer := newPageRunner(t, nil)
	state := pageTestState()

	state, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{OutputDir: "gs://bucket/out"})
	if err != nil {
		t.Fatalf("ComposePage failed: %v", err)
	}

	// 参照: キャラ2（zundamon, metan）+ 生成済みパネル1（p01のみ）
	if len(gen.lastReq.Images) != 3 {
		t.Fatalf("Images = %+v, want 3 references", gen.lastReq.Images)
	}

	// プロンプト本文の作り込みは ports.PagePrompt の実装（既定はキット内蔵の簡潔版、
	// 作品ごとの本文はアプリ側）の責務なので、ここでは構造データが正しく渡ることだけを見る。
	p := gen.lastReq.Prompt
	if !strings.Contains(p, "exactly 2 panel") {
		t.Errorf("prompt does not state the panel count:\n%s", p)
	}
	// 参照番号は添付順と一致していなければ、モデルは別人の参照画像を見て描く。
	for _, want := range []string{
		"identity from input_file_1", // ずんだもん = 1枚目
		"identity from input_file_2", // めたん = 2枚目
		"composition guide: input_file_3",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt does not contain %q\nprompt: %s", want, p)
		}
	}
	// セリフは話者名付きで渡る
	for _, want := range []string{"dialogue (ずんだもん): なんなのだ！？", "dialogue (めたん): 落ち着きなさい。"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt does not contain %q\nprompt: %s", want, p)
		}
	}

	// 別ページのパネルが混ざらない
	if strings.Contains(p, "ch02") {
		t.Error("prompt leaked panels from another page")
	}

	if !strings.Contains(gen.lastReq.SystemPrompt, "right-to-left") {
		t.Error("system prompt missing the reading order rule")
	}
	if !strings.Contains(p, "cinematic style") {
		t.Error("prompt missing the style suffix")
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
	r, gen, _ := newPageRunner(t, nil)
	state := pageTestState()
	state.Pages = []ports.PageArtifact{
		{PageNumber: 1, Generation: &ports.GenerationRecord{ImageURL: "gs://old.png", UsedSeed: 777}},
	}

	state, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{})
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
	r, gen, _ := newPageRunner(t, nil)
	state := pageTestState()
	state.Pages = []ports.PageArtifact{
		{PageNumber: 1, Generation: &ports.GenerationRecord{ImageURL: "gs://b/pages/page1.png"}},
	}

	_, err := r.ComposePage(context.Background(), state, 1, ports.GenerateOptions{
		EditPrompt: "1コマ目の空を夕焼けにする",
	})
	if err != nil {
		t.Fatalf("ComposePage(edit) failed: %v", err)
	}

	if len(gen.lastReq.Images) != 1 || gen.lastReq.Images[0].ReferenceURL != "gs://b/pages/page1.png" {
		t.Errorf("Images = %+v, want existing page image only", gen.lastReq.Images)
	}
	if !strings.Contains(gen.lastReq.Prompt, prompts.PageEditInstruction) || !strings.Contains(gen.lastReq.Prompt, "夕焼け") {
		t.Errorf("Prompt = %q, want edit instruction", gen.lastReq.Prompt)
	}
}

func TestComposePageEditModeRequiresExistingImage(t *testing.T) {
	t.Parallel()
	r, _, _ := newPageRunner(t, nil)

	_, err := r.ComposePage(context.Background(), pageTestState(), 1, ports.GenerateOptions{EditPrompt: "変更"})
	if err == nil || !strings.Contains(err.Error(), "編集対象") {
		t.Errorf("err = %v, want missing-image error", err)
	}
}

func TestComposePageEmptyPageFails(t *testing.T) {
	t.Parallel()
	r, _, _ := newPageRunner(t, nil)

	if _, err := r.ComposePage(context.Background(), pageTestState(), 99, ports.GenerateOptions{}); err == nil {
		t.Error("ComposePage(empty page) succeeded, want error")
	}
	if _, err := r.ComposePage(context.Background(), nil, 1, ports.GenerateOptions{}); err == nil {
		t.Error("ComposePage(nil state) succeeded, want error")
	}
}
