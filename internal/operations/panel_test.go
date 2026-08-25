package operations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/comic"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	characterkit "github.com/shouni/go-character-kit/character"

	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// --- Mocks ---

type mockImageGenerator struct {
	lastReq imagePorts.ImageRequest
}

func (m *mockImageGenerator) Generate(_ context.Context, req imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
	m.lastReq = req
	return &imagePorts.ImageResponse{Data: []byte("fake-png"), MimeType: "image/png", UsedSeed: 555}, nil
}

// --- Helpers ---

func panelTestState() *comic.MangaState {
	return &comic.MangaState{
		Version: comic.StateSchemaVersion,
		Chapters: []comic.Chapter{
			{ID: "ch01", Title: "導入"},
		},
		Panels: []comic.Panel{
			{
				ID:           "ch01-p01",
				ChapterID:    "ch01",
				Page:         1,
				Shot:         "wide",
				Setting:      "放課後の音楽室",
				VisualAnchor: "sunset light through windows, dynamic angle",
				Characters: []comic.PanelCharacter{
					{CharacterID: "zundamon", Prominence: comic.ProminencePrimary, Emotion: "驚き", Action: "めたんを指差す", Position: "left foreground"},
					{CharacterID: "metan", Prominence: comic.ProminenceSecondary, Emotion: "冷静", Position: "right"},
					{CharacterID: "students", Prominence: comic.ProminenceBackground, Action: "ざわめく"},
				},
			},
		},
	}
}

func newPanelRunner(t *testing.T, prompt ports.PanelPrompt) (*PanelImageRunner, *mockImageGenerator, *mockWriter) {
	t.Helper()
	zundaSeed := int64(10001)
	cm, err := characterkit.NewCharacters([]comic.Character{
		{
			ID:           "zundamon",
			Name:         "ずんだもん",
			ReferenceURL: "gs://b/zunda.png",
			ReferenceURLs: map[string]string{
				"3:4": "gs://b/zunda-3x4.png",
			},
			VisualCues: []string{"green hair"},
			Seed:       &zundaSeed,
			IsDefault:  true,
		},
		{ID: "metan", Name: "めたん", ReferenceURL: "gs://b/metan.png", VisualCues: []string{"purple hair"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}
	gen := &mockImageGenerator{}
	writer := &mockWriter{}
	r := NewPanelImageRunner(PanelImageRunnerArgs{
		Characters: cm,
		Prompt:     prompt,
		Generator:  gen,
		Writer:     writer,
	})
	return r, gen, writer
}

// --- Tests ---

func TestGeneratePanelBuildsMultiSubjectRequest(t *testing.T) {
	t.Parallel()
	prompt := &fakePanelPrompt{}
	r, gen, writer := newPanelRunner(t, prompt)
	state := panelTestState()

	state, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{OutputDir: "gs://bucket/out", Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}

	// 参照画像: primary + secondary の2枚（background は除外）
	if len(gen.lastReq.Images) != 2 {
		t.Fatalf("Images = %+v, want 2 references", gen.lastReq.Images)
	}
	// アスペクト比一致の参照画像が優先される
	if gen.lastReq.Images[0].ReferenceURL != "gs://b/zunda-3x4.png" {
		t.Errorf("Images[0] = %q, want aspect-specific reference", gen.lastReq.Images[0].ReferenceURL)
	}
	// 参照の解決方法（GCS 直接参照 / File API へのアップロード）は gemini-image-kit の
	// 責務なので、ここでは参照元 URL をそのまま渡していることだけを確認する。
	if gen.lastReq.Images[0].FileAPIURI != "" {
		t.Errorf("Images[0].FileAPIURI = %q, want it left to the image kit", gen.lastReq.Images[0].FileAPIURI)
	}

	// プロンプト本文はアプリ側の実装が持つので、ここで見るのは
	// 「実装へ渡した構造データ」と「返った3本をそのまま載せたか」だけ。
	d := prompt.data
	if d == nil {
		t.Fatal("PanelPrompt was not called")
	}
	// 参照番号は添付順と一致していなければ、モデルは別人の参照画像を見て描く。
	if len(d.SubjectIDs) != 2 || d.SubjectIDs[0] != "zundamon" || d.SubjectIDs[1] != "metan" {
		t.Errorf("SubjectIDs = %v, want [zundamon metan]（添付順）", d.SubjectIDs)
	}
	if d.Panel.ID != "ch01-p01" || d.Panel.Setting != "放課後の音楽室" ||
		!strings.Contains(d.Panel.VisualAnchor, "sunset light through windows") {
		t.Errorf("Panel = %+v, want the target panel with its setting and anchor", d.Panel)
	}
	if gen.lastReq.SystemPrompt != fakeSystemPrompt || gen.lastReq.NegativePrompt != fakeNegativePrompt {
		t.Errorf("system/negative prompt = %q / %q, want them passed through unchanged",
			gen.lastReq.SystemPrompt, gen.lastReq.NegativePrompt)
	}

	if gen.lastReq.AspectRatio != "3:4" || gen.lastReq.ImageSize != "1K" {
		t.Errorf("AspectRatio/ImageSize = %q/%q, want defaults 3:4/1K", gen.lastReq.AspectRatio, gen.lastReq.ImageSize)
	}

	// 主役キャラクターの Seed が既定として使われる
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 10001 {
		t.Errorf("Seed = %v, want primary character seed 10001", gen.lastReq.Seed)
	}

	// GenerationRecord の記録
	rec := state.PanelByID("ch01-p01").Generation
	if rec == nil {
		t.Fatal("GenerationRecord not recorded")
	}
	if rec.ImageURL != writer.lastPath || !strings.Contains(rec.ImageURL, "images/panel_ch01-p01.png") {
		t.Errorf("ImageURL = %q, want stable panel path (saved: %q)", rec.ImageURL, writer.lastPath)
	}
	if rec.UsedSeed != 555 || rec.Model != "panel-model" || rec.Prompt == "" {
		t.Errorf("GenerationRecord = %+v, want full generation conditions", rec)
	}
}

func TestGeneratePanelReusesPreviousSeed(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Generation = &comic.GenerationRecord{ImageURL: "gs://old.png", UsedSeed: 777}

	if _, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{Model: "panel-model"}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 777 {
		t.Errorf("Seed = %v, want previous UsedSeed 777", gen.lastReq.Seed)
	}
}

func TestGeneratePanelExplicitSeedWins(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Generation = &comic.GenerationRecord{UsedSeed: 777}

	newSeed := int64(42)
	if _, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{Seed: &newSeed, Model: "panel-model"}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Seed == nil || *gen.lastReq.Seed != 42 {
		t.Errorf("Seed = %v, want explicit 42", gen.lastReq.Seed)
	}
}

func TestGeneratePanelEditMode(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Generation = &comic.GenerationRecord{ImageURL: "gs://bucket/out/images/panel_ch01-p01.png", UsedSeed: 777}

	_, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{
		EditPrompt: "ずんだもんの表情を笑顔に変える",
		OutputDir:  "gs://bucket/out", Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel(edit) failed: %v", err)
	}

	// 編集モードは既存画像1枚だけを入力にする
	if len(gen.lastReq.Images) != 1 || gen.lastReq.Images[0].ReferenceURL != "gs://bucket/out/images/panel_ch01-p01.png" {
		t.Errorf("Images = %+v, want the existing panel image only", gen.lastReq.Images)
	}
	if !strings.Contains(gen.lastReq.Prompt, "FAKE-PANEL-EDIT") || !strings.Contains(gen.lastReq.Prompt, "笑顔") {
		t.Errorf("Prompt = %q, want edit instruction", gen.lastReq.Prompt)
	}
}

func TestGeneratePanelEditModeRequiresExistingImage(t *testing.T) {
	t.Parallel()
	r, _, _ := newPanelRunner(t, &fakePanelPrompt{})

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{
		EditPrompt: "表情を変える", Model: "panel-model"})
	if err == nil || !strings.Contains(err.Error(), "編集対象") {
		t.Errorf("err = %v, want missing-image error", err)
	}
}

func TestGeneratePanelPromptOverride(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{
		PromptOverride: "custom prompt", Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if gen.lastReq.Prompt != "custom prompt" {
		t.Errorf("Prompt = %q, want override", gen.lastReq.Prompt)
	}
	// 参照画像は override でも維持される
	if len(gen.lastReq.Images) != 2 {
		t.Errorf("Images = %+v, want references kept with prompt override", gen.lastReq.Images)
	}
}

func TestGeneratePanelUnknownPanelFails(t *testing.T) {
	t.Parallel()
	r, _, _ := newPanelRunner(t, &fakePanelPrompt{})

	if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch99-p01", ports.GenerateOptions{Model: "panel-model"}); err == nil {
		t.Error("GeneratePanel(unknown) succeeded, want error")
	}
	if _, err := r.GeneratePanel(context.Background(), nil, "ch01-p01", ports.GenerateOptions{Model: "panel-model"}); err == nil {
		t.Error("GeneratePanel(nil state) succeeded, want error")
	}
}

func TestGeneratePanelSceneryPanelWithoutCharacters(t *testing.T) {
	t.Parallel()
	r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})
	state := panelTestState()
	state.Panels[0].Characters = nil // 風景のみのコマ

	_, err := r.GeneratePanel(context.Background(), state, "ch01-p01", ports.GenerateOptions{Model: "panel-model"})
	if err != nil {
		t.Fatalf("GeneratePanel(scenery) failed: %v", err)
	}
	if len(gen.lastReq.Images) != 0 {
		t.Errorf("Images = %+v, want no references for scenery panel", gen.lastReq.Images)
	}
	// キャラクター由来のシードは無いが、記録から再現できるようシードは必ず送る
	// （resolveSeedChain の最後の採番。UsedSeed が 0 のまま記録されると再生成が別物になる）。
	if gen.lastReq.Seed == nil {
		t.Error("Seed = nil, want a freshly drawn seed so the panel can be reproduced")
	}
}

// 画風モードはプロンプト実装へ素通しします（画風指定そのものはキットが持ちません）。
func TestGeneratePanelPassesStyleMode(t *testing.T) {
	t.Parallel()

	prompt := &fakePanelPrompt{}
	r, _, _ := newPanelRunner(t, prompt)

	if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01",
		ports.GenerateOptions{StyleMode: "watercolor", Model: "panel-model"}); err != nil {
		t.Fatalf("GeneratePanel failed: %v", err)
	}
	if got := prompt.data.StyleMode; got != "watercolor" {
		t.Errorf("StyleMode = %q, want watercolor", got)
	}
}

// 画像側も同じく、モデル名が無ければ生成器を呼ばずに ErrInvalidRequest で落とします。
// パネル・ページ・デザインシートは renderImage を共有するので、代表して1件見ます。
func TestGeneratePanelWithoutAnyModelFails(t *testing.T) {
	t.Parallel()

	r, gen, writer := newPanelRunner(t, &fakePanelPrompt{})

	_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", ports.GenerateOptions{})
	if !errors.Is(err, ports.ErrInvalidRequest) {
		t.Errorf("err = %v, want ErrInvalidRequest", err)
	}
	if gen.lastReq.Model != "" {
		t.Error("モデル名が無いのに画像生成を呼んでいます")
	}
	if writer.lastPath != "" {
		t.Error("生成していないのに書き込んでいます")
	}
}

// 比率・解像度は呼び出しごとの指定です。空ならキット既定（3:4 / 1K）に落ち、
// 未サポート値は黙って既定へ落とさず ErrInvalidRequest にします。
// 落とすと「指定したつもりの比率で生成されない」状態が気付かれずに続きます。
func TestGeneratePanelResolvesLayoutPerCall(t *testing.T) {
	t.Parallel()

	t.Run("未指定はキット既定", func(t *testing.T) {
		t.Parallel()
		r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

		if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01",
			ports.GenerateOptions{Model: "m"}); err != nil {
			t.Fatalf("GeneratePanel failed: %v", err)
		}
		if gen.lastReq.AspectRatio != layout.DefaultAspectRatio || gen.lastReq.ImageSize != layout.ImageSize1K {
			t.Errorf("layout = %q/%q, want %q/%q",
				gen.lastReq.AspectRatio, gen.lastReq.ImageSize, layout.DefaultAspectRatio, layout.ImageSize1K)
		}
	})

	t.Run("指定が届く", func(t *testing.T) {
		t.Parallel()
		r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

		if _, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01",
			ports.GenerateOptions{Model: "m", AspectRatio: "16:9", ImageSize: ports.ImageSize2K}); err != nil {
			t.Fatalf("GeneratePanel failed: %v", err)
		}
		if gen.lastReq.AspectRatio != "16:9" || gen.lastReq.ImageSize != ports.ImageSize2K {
			t.Errorf("layout = %q/%q, want 16:9/%s", gen.lastReq.AspectRatio, gen.lastReq.ImageSize, ports.ImageSize2K)
		}
	})

	for name, opts := range map[string]ports.GenerateOptions{
		"未サポートの比率":  {Model: "m", AspectRatio: "4:3"},
		"未サポートの解像度": {Model: "m", ImageSize: "4K"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, gen, _ := newPanelRunner(t, &fakePanelPrompt{})

			_, err := r.GeneratePanel(context.Background(), panelTestState(), "ch01-p01", opts)
			if !errors.Is(err, ports.ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest", err)
			}
			if gen.lastReq.Model != "" {
				t.Error("不正な指定なのに画像生成を呼んでいます")
			}
		})
	}
}
