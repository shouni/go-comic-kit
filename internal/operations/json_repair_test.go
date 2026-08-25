package operations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shouni/go-comic-kit/ports"
)

// 本ファイルは「引用した本文に生の改行が混ざっても章台本が失われない」ことを固定します。
//
// 構造化出力（ResponseSchema）を指定しても、モデルは**複数行の台詞や情景描写を
// 引用するとき、JSON 文字列の中へ生の改行を入れてきます。** 応答を返しきったあとの
// 崩れなので API の再試行では直らず、補修が無ければ章まるごと ErrGeneration で落ちます。
// 補修は go-gemini-client の gemini.CleanJSONResponse が持っています。

// chapterJSONWithRawNewline は、台詞と情景描写に生の改行を含む章台本の応答です。
// バッククォートではなく通常の文字列リテラルで書いているのは、\n を
// **エスケープではなく実際の改行**としてペイロードへ入れるためです。
const chapterJSONWithRawNewline = "{\n" +
	"  \"panels\": [\n" +
	"    {\n" +
	"      \"shot\": \"wide\",\n" +
	"      \"setting\": \"放課後の音楽室\",\n" +
	"      \"visual_anchor\": \"夕暮れの教室\n窓辺に人影\",\n" +
	"      \"characters\": [{\"character_id\": \"zundamon\", \"prominence\": \"primary\"}],\n" +
	"      \"dialogues\": [{\"speaker_id\": \"zundamon\", \"text\": \"待って。\nまだ終わってないのだ\", \"kind\": \"speech\"}]\n" +
	"    }\n" +
	"  ]\n" +
	"}"

func TestGenerateChapterScriptSurvivesRawNewlinesInQuotedText(t *testing.T) {
	t.Parallel()

	// 前提の固定: このペイロードは素のままでは JSON として読めません。
	// ここが通ってしまうと、以降は補修を通らずに成功するので試験になりません。
	if json.Valid([]byte(chapterJSONWithRawNewline)) {
		t.Fatal("試験用ペイロードが妥当な JSON になっています。生の改行が消えていないか確認してください")
	}

	ai := &fakeContentGenerator{text: chapterJSONWithRawNewline}
	runner, _ := newChapterRunner(t, ai)

	state, err := runner.GenerateChapterScript(context.Background(), outlineState(), "ch01",
		ports.ChapterScriptOptions{Model: "text-model"})
	if err != nil {
		t.Fatalf("引用文中の生の改行で章台本が失われました: %v", err)
	}

	panels := state.PanelsForChapter("ch01")
	if len(panels) != 1 {
		t.Fatalf("PanelsForChapter() = %d 件, want 1", len(panels))
	}

	// 改行はエスケープされて本文に残る（削られない）。
	if !strings.Contains(panels[0].VisualAnchor, "\n") {
		t.Errorf("VisualAnchor から改行が失われています: %q", panels[0].VisualAnchor)
	}
	if len(panels[0].Dialogues) != 1 || !strings.Contains(panels[0].Dialogues[0].Text, "\n") {
		t.Errorf("台詞から改行が失われています: %+v", panels[0].Dialogues)
	}
}
