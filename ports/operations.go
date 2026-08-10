package ports

import (
	"context"

	"github.com/shouni/go-comic-kit/comic"
)

// 本ファイルは go-comic-kit の操作セット（README.md の操作セット節）の契約を定義します。
// すべての操作は冪等で、MangaState を受け取り更新済み MangaState を返します。

// OutlineRequest は章立て生成（GenerateOutline）への入力です。
type OutlineRequest struct {
	// SourceURL は原稿の取得元 URI です（SourceText と排他）。
	SourceURL string
	// SourceText は原稿テキストそのものです（SourceURL と排他）。
	SourceText string
	// Mode は章立てプロンプトのモード（テンプレート選択）です。空なら既定テンプレート。
	Mode string
	// StyleMode は画像生成時のスタイル選択で、生成された MangaState に記録されます。
	StyleMode string
	// MaxChapters は章数の上限です。0 以下なら既定値を使います。
	MaxChapters int
}

// OutlineGenerator は、原稿から章立て（Chapters）のみを持つ MangaState を生成する契約です。
// 台本生成の第1段で、各章のパネルは ChapterScriptGenerator が章単位で生成します。
type OutlineGenerator interface {
	GenerateOutline(ctx context.Context, req OutlineRequest) (*comic.MangaState, error)
}

// ChapterScriptGenerator は、章立て全体を文脈としつつ指定章のパネル群（台本）を生成し、
// 既存の同章パネルを置き換える契約です（冪等・章単位の再生成に対応）。
type ChapterScriptGenerator interface {
	GenerateChapterScript(ctx context.Context, state *comic.MangaState, chapterID string) (*comic.MangaState, error)
}

// DesignOverride は、1回の呼び出しに限定してキャラクターの参照画像・visual_cues を
// 差し替えるためのその場限りの上書き指定です。キャラクター定義（characters.json）
// そのものは変更しません。ReferenceURL / VisualCues が空の場合はそのフィールドのみ
// キャラクター定義の値を使います。CharacterIDs が複数（合成デザインシート）の場合、
// 上書きはどのキャラクターに適用すべきか一意に決まらないため無視されます。
type DesignOverride struct {
	ReferenceURL string
	VisualCues   []string
}

// DesignSheetRequest はデザインシート生成（GenerateDesignSheet）への入力です。
type DesignSheetRequest struct {
	// CharacterIDs は対象キャラクターです。複数指定すると1枚の合成シートになります。
	CharacterIDs []string
	// JobID は、この生成呼び出しを一意に識別する ID です（呼び出し側が採番）。
	// 保存パス（OutputDir 配下の character/{tag}/{JobID}.ext）に使われ、同一キャラクターへの
	// 複数回の生成を上書きせず履歴として残すためのものです。空文字は許可しません。
	JobID string
	// Seed は生成シードです。nil の場合は生成側で採番し、DesignSheetRef.UsedSeed に
	// 記録します（その値を渡し直せば同じシートを再現できます）。
	// GenerateOptions / BatchOptions の Seed と同じ表現です。
	Seed *int64
	// OutputDir はシート画像の保存先ベースディレクトリ（ローカルまたは gs://）です。
	// キャラクターはジョブ（作品）非依存の共有アセットのため、通常はバケットのルート
	// （例: "gs://bucket"）を渡します。相対パスは OutputDir に対して
	// "character/{tag}/{JobID}.ext" として解決されます。
	OutputDir string
	// AspectRatio は "1:1" / "3:4" / "9:16" / "16:9" のいずれかで、未サポート値や空文字の
	// 場合は既定値（16:9）にフォールバックします。パネルやページの参照アンカーとして使う
	// シートは "3:4" で生成してください（コマ・ページと同じ比率でないと、参照画像の
	// アスペクト比一致による細部のブレ抑制が効きません）。
	AspectRatio string
	// Layout に DesignLayoutSingleView を渡すと単一ポーズ（参照アンカー向け）、
	// 空文字なら3面図ターンアラウンドになります。
	Layout string
	// Override は単一キャラクター指定時のみ適用されるその場限りの上書きです。
	Override DesignOverride
	// ModelOverride は設定済みモデル（DesignSheetRunner 構築時の model）を差し替えます。
	// 空文字なら既定のモデルを使います。
	ModelOverride string
}

// DesignLayoutSingleView は DesignSheetRequest.Layout に渡す、単一ポーズレイアウトの指定値です。
const DesignLayoutSingleView = "single"

// DesignSheetGenerator は、キャラクターの同一性アンカーとなるデザインシートを生成し、
// その記録を MangaState に反映する契約です。state が nil の場合は新しい state を作成します。
type DesignSheetGenerator interface {
	GenerateDesignSheet(ctx context.Context, state *comic.MangaState, req DesignSheetRequest) (*comic.MangaState, error)
}

// GenerateOptions はパネル・ページ生成系操作の共通オプションです。
type GenerateOptions struct {
	// Seed は生成シードです。指定すると振り直しになります。
	//
	// nil の場合は「前回の GenerationRecord.UsedSeed → 主役キャラクターの Seed → 新規採番」
	// の順に解決します。前回値があれば「同条件での再生成」になり、無ければ採番した値が
	// GenerationRecord.UsedSeed に記録されるので、次回以降は再現できます。
	Seed *int64
	// PromptOverride は自動構築されるプロンプトを差し替えます（空なら自動構築）。
	PromptOverride string
	// EditPrompt を指定すると、ゼロからの再生成ではなく既存の生成済み画像
	// （GenerationRecord.ImageURL）を入力とした編集モードになります。構図・ポーズ・背景を
	// 保ったまま指示した箇所だけを変更します（go-veo-orchestrator の EditCut と同方式）。
	// 対象パネルに生成済み画像が無い場合はエラーになります。
	EditPrompt string
	// ModelOverride は設定済みモデルを差し替えます（空なら既定）。
	ModelOverride string
	// OutputDir は生成画像の保存先ベースディレクトリです。
	OutputDir string
}

// PanelImageGenerator は、パネル画像を1コマ単位・全コマ一括で生成/再生成し、
// 結果を comic.MangaState の comic.GenerationRecord に記録する契約です。
type PanelImageGenerator interface {
	GeneratePanel(ctx context.Context, state *comic.MangaState, panelID string, opts GenerateOptions) (*comic.MangaState, error)

	// GenerateAllPanels は state 内の全パネルをまとめて生成します。
	// 並列数は Config.MaxConcurrency に従います（既定の 1 では逐次実行と同じ挙動です）。
	//
	// 一部が失敗しても、成功した分を記録済みの state と、失敗を errors.Join でまとめた
	// エラーの両方を返します（state が nil になるのは state 自体が不正だった場合だけです）。
	// 画像生成は高価なので、成功分を保存してから SkipGenerated で残りだけ再実行できます。
	GenerateAllPanels(ctx context.Context, state *comic.MangaState, opts BatchOptions) (*comic.MangaState, error)
}

// PageImageComposer は、パネル群を1ページ単位・全ページ一括で合成し、
// 結果を comic.MangaState の comic.PageArtifact に記録する契約です。
type PageImageComposer interface {
	ComposePage(ctx context.Context, state *comic.MangaState, page int, opts GenerateOptions) (*comic.MangaState, error)

	// ComposeAllPages は state 内の全ページをまとめて合成します。
	// 並列数とエラー時の戻り値の扱いは GenerateAllPanels と同じです。
	ComposeAllPages(ctx context.Context, state *comic.MangaState, opts BatchOptions) (*comic.MangaState, error)
}

// BatchOptions は一括生成系操作（GenerateAllPanels / ComposeAllPages）のオプションです。
// GenerateOptions を埋め込まないのは、1件を狙い撃ちするための EditPrompt / PromptOverride が
// 一括処理では意味を持たないためです（全対象に同じ編集指示を当てても無意味です）。
type BatchOptions struct {
	// Seed は全対象に適用する生成シードです。nil の場合は対象ごとに
	// GenerateOptions.Seed が nil のときと同じ解決規則
	// （前回値 → 主役キャラクター → 新規採番）に従います。
	Seed *int64
	// ModelOverride は設定済みモデルを差し替えます（空なら既定）。
	ModelOverride string
	// OutputDir は生成画像の保存先ベースディレクトリです。
	OutputDir string
	// SkipGenerated が true の場合、すでに生成済み（comic.GenerationRecord を持つ）対象を飛ばします。
	// 途中まで成功した一括生成を、未生成分だけやり直すときに使います。
	SkipGenerated bool
}

// Operations は、構築済みの全操作を保持します（workflow.New が組み立てて返します）。
type Operations struct {
	Outline       OutlineGenerator
	ChapterScript ChapterScriptGenerator
	DesignSheet   DesignSheetGenerator
	Panel         PanelImageGenerator
	Page          PageImageComposer
	CloseFunc     func()
}

// Close は、保持しているリソースの解放関数（CloseFunc）を呼び出します。
func (o *Operations) Close() {
	if o != nil && o.CloseFunc != nil {
		o.CloseFunc()
	}
}
