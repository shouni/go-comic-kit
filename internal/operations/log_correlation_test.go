package operations

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/shouni/go-comic-kit/comic"
	"github.com/shouni/go-comic-kit/ports"

	characterkit "github.com/shouni/go-character-kit/character"
)

// 本ファイルは「ライブラリのログが context を運ぶ」という不変条件を固定します。
//
// アプリ側は go-utils/slogctx のハンドラーを既定ロガーに入れ、job_id をリクエストの
// context に積んでいます。ここで slog.Info（context を取らない版）を使うと、slog は
// context.Background() をハンドラーへ渡すため、その行だけ job_id が付きません。
// 落ちるのは生成本体のログ、つまり job_id で絞ったときに一番見たい行です。
// 呼び出しが Context 版であるかぎり通り、片方でも戻ると落ちます。

// correlationKey は、テスト用ハンドラーが拾う目印です。slogctx を import すると
// go-utils への依存辺が増えるため、同じ仕組み（context から属性を取り出して
// レコードへ足す）を最小限で再現します。
type correlationKey struct{}

// capturingHandler は、目印を持つ context から出たレコードのメッセージだけを集めます。
// 目印を持たないレコードは捨てるので、並列に走る他のテストのログとは混ざりません。
type capturingHandler struct {
	mu       *sync.Mutex
	messages *[]string
}

func (h capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h capturingHandler) Handle(ctx context.Context, record slog.Record) error {
	if ctx.Value(correlationKey{}) == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.messages = append(*h.messages, record.Message)
	return nil
}

func (h capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h capturingHandler) WithGroup(string) slog.Handler      { return h }

// captureCorrelatedLogs は既定ロガーを差し替え、集まったメッセージを返す関数を渡します。
func captureCorrelatedLogs(t *testing.T) func() []string {
	t.Helper()

	var mu sync.Mutex
	var messages []string

	previous := slog.Default()
	slog.SetDefault(slog.New(capturingHandler{mu: &mu, messages: &messages}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(messages)
	}
}

// correlationChapterJSON は、章台本生成の全ログ経路を一度に通す応答です。
// パネルは 2 つ（上限 1 に対して切り詰め）、1 つ目に未定義キャラクター 1 名・
// 参照付きキャラクター 4 名（上限 3 に対して降格）・未定義の話者 1 名を含みます。
const correlationChapterJSON = `{
  "panels": [
    {
      "shot": "wide",
      "visual_anchor": "no text",
      "characters": [
        {"character_id": "c1", "prominence": "primary"},
        {"character_id": "c2", "prominence": "secondary"},
        {"character_id": "c3", "prominence": "secondary"},
        {"character_id": "c4", "prominence": "secondary"},
        {"character_id": "ghost", "prominence": "secondary"}
      ],
      "dialogues": [
        {"speaker_id": "phantom", "text": "誰なのだ"}
      ]
    },
    {
      "shot": "close-up",
      "visual_anchor": "no text",
      "characters": [{"character_id": "c1", "prominence": "primary"}],
      "dialogues": []
    }
  ]
}`

func TestChapterScriptLogsCarryCallerContext(t *testing.T) {
	// 既定ロガーを差し替えるため、このテストは並列化しません。
	logged := captureCorrelatedLogs(t)

	cm, err := characterkit.NewCharacters([]comic.Character{
		{ID: "c1", Name: "一号", ReferenceURL: "gs://b/1.png", VisualCues: []string{"cue1"}, IsDefault: true},
		{ID: "c2", Name: "二号", ReferenceURL: "gs://b/2.png", VisualCues: []string{"cue2"}},
		{ID: "c3", Name: "三号", ReferenceURL: "gs://b/3.png", VisualCues: []string{"cue3"}},
		{ID: "c4", Name: "四号", ReferenceURL: "gs://b/4.png", VisualCues: []string{"cue4"}},
	})
	if err != nil {
		t.Fatalf("NewCharacters failed: %v", err)
	}

	ai := &fakeContentGenerator{text: correlationChapterJSON}
	runner := NewChapterScriptRunner(&fakeScriptPrompt{}, ai, cm, 1, 0)

	ctx := context.WithValue(context.Background(), correlationKey{}, "job-1")
	state := outlineState()
	if _, err := runner.GenerateChapterScript(ctx, state, "ch01", ports.ChapterScriptOptions{Model: "text-model"}); err != nil {
		t.Fatalf("GenerateChapterScript failed: %v", err)
	}

	// 呼び出し元の context 経由でしか届かないメッセージ群。
	// 後半 3 つは ctx を引数に持たないヘルパーから出るため、退行しやすい箇所です。
	want := []string{
		"ChapterScriptRunner: Gemini APIを呼び出し中",
		"ChapterScriptRunner: 章台本を生成しました",
		"パネル数が上限を超えたため切り詰めます",
		"未定義のキャラクターIDをbackgroundに降格します",
		"参照キャラクターが上限を超えたためbackgroundに降格します",
		"未定義の話者IDをナレーションに変更します",
	}

	got := logged()
	for _, message := range want {
		if !slices.Contains(got, message) {
			t.Errorf("%q が呼び出し元の context を運んでいません。slog.Info/Warn ではなく Context 版を使ってください。届いたのは %q", message, got)
		}
	}
}
