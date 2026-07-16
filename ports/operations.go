package ports

import "context"

// 本ファイルは go-comic-kit の操作セット（docs/comic-kit-design.md §5）の契約を定義します。
// すべての操作は冪等で、MangaState を受け取り更新済み MangaState を返します。

// ScriptRequest は台本生成（GenerateScript）への入力です。
type ScriptRequest struct {
	// SourceURL は原稿の取得元 URI です（SourceText と排他）。
	SourceURL string
	// SourceText は原稿テキストそのものです（SourceURL と排他）。
	SourceText string
	// Mode は台本生成プロンプトのモード（テンプレート選択）です。
	Mode string
	// StyleMode は画像生成時のスタイル選択で、生成された MangaState に記録されます。
	StyleMode string
}

// ScriptGenerator は、原稿から構造化された MangaState（台本）を生成する契約です。
type ScriptGenerator interface {
	GenerateScript(ctx context.Context, req ScriptRequest) (*MangaState, error)
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
	// Seed は生成シードです。0 の場合はモデル側に委ねます。
	Seed int64
	// OutputDir はシート画像の保存先ベースディレクトリ（ローカルまたは gs://）です。
	OutputDir string
	// AspectRatio は "1:1" / "9:16" / "16:9" のいずれかで、未サポート値や空文字の場合は
	// 既定値（16:9）にフォールバックします。
	AspectRatio string
	// Layout に DesignLayoutSingleView を渡すと単一ポーズ（参照アンカー向け）、
	// 空文字なら3面図ターンアラウンドになります。
	Layout string
	// Override は単一キャラクター指定時のみ適用されるその場限りの上書きです。
	Override DesignOverride
}

// DesignLayoutSingleView は DesignSheetRequest.Layout に渡す、単一ポーズレイアウトの指定値です。
const DesignLayoutSingleView = "single"

// DesignSheetGenerator は、キャラクターの同一性アンカーとなるデザインシートを生成し、
// その記録を MangaState に反映する契約です。state が nil の場合は新しい state を作成します。
type DesignSheetGenerator interface {
	GenerateDesignSheet(ctx context.Context, state *MangaState, req DesignSheetRequest) (*MangaState, error)
}

// GenerateOptions はパネル・ページ生成系操作の共通オプションです。
type GenerateOptions struct {
	// Seed は生成シードです。nil の場合、対象の GenerationRecord.UsedSeed（前回値）があれば
	// それを再利用し「同条件での再生成」になります。指定すると振り直しです。
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

// PanelImageGenerator は、指定したパネル1コマを生成/再生成し、結果を
// MangaState の GenerationRecord に記録する契約です。
type PanelImageGenerator interface {
	GeneratePanel(ctx context.Context, state *MangaState, panelID string, opts GenerateOptions) (*MangaState, error)
}

// PageImageComposer は、指定ページのパネル群を1枚のページ画像として合成し、
// 結果を MangaState の PageArtifact に記録する契約です。
type PageImageComposer interface {
	ComposePage(ctx context.Context, state *MangaState, page int, opts GenerateOptions) (*MangaState, error)
}

// PublishResult はパブリッシュ処理の結果として生成されたファイルの情報を保持します。
type PublishResult struct {
	MarkdownPath string   // 生成された Markdown のパス
	HTMLPath     string   // 生成された HTML のパス
	ImagePaths   []string // 保存された全画像のパスリスト
}

// Publisher は、MangaState を統合し、指定された形式（HTML/Markdown 等）で出力する契約です。
type Publisher interface {
	Publish(ctx context.Context, state *MangaState, outputDir string) (*PublishResult, error)
}
