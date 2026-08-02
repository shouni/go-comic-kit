# 🎨 Go Comic Kit

[![CI](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-comic-kit)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-comic-kit)](https://github.com/shouni/go-comic-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-comic-kit.svg)](https://pkg.go.dev/github.com/shouni/go-comic-kit)
[![Status](https://img.shields.io/badge/Status-In%20Development-yellow)](#)

## 🚀 概要 (About)

**Go Comic Kit** は、AIによる**キャラクターの一貫性を維持した漫画生成**のためのツールキットです。

---

## ✨ コア・コンセプト (Core Concepts)

* **📄 MangaState = 唯一の真実源**:
    * 1作品の全状態（台本・登場キャラ・パネル/ページの生成条件・成果物URL）を1つの状態
      ドキュメントとして永続化。履歴一覧・詳細参照はアプリ側が state 一覧を読むだけで実現できます。
* **🔁 冪等・工程単位の操作**:
    * `GenerateOutline` が原稿から state を新規作成し、`GenerateChapterScript` /
      `GenerateDesignSheet` / `GeneratePanel` / `ComposePage` は以降 state を受け取って
      更新済み state を返します。「12パネル中3番だけシードを振り直して再生成」が
      API として表現でき、MCP ツール（`regenerate_panel` 等）と1対1で対応します。
* **👥 マルチキャラクター・パネル**:
    * パネルは「発話者1人」ではなく **登場キャラクターの集合**（`Characters []PanelCharacter`）として表現。
      感情・アクション（関係性）・配置・扱い（primary/secondary/background）を個別に指定でき、
      発話しない primary/secondary キャラクターにも参照画像が添付されるため同一性が崩れません
      （background は参照画像の対象外）。
* **🧬 3-Factor Consistency Control**:
    * **Seed値**（基盤）、**参照アセット**（外見）、**VisualCues/言語指示**（詳細）の3要素で
      キャラクターの一貫性を制御。パネル・ページの生成条件は `GenerationRecord` として
      state に永続化されます。
    * **シードは必ず記録されます**。明示指定が無い場合も「前回値 → 主役キャラクターの Seed →
      新規採番」の順に必ず決まった値を送るため、`UsedSeed` を読み直せばいつでも同じ絵を再現できます
      （シードを送らないと API 側が選んだ値は返ってこず、再現できなくなります）。
* **📐 構造化出力（Constrained Decoding）**:
    * 台本生成は `ResponseJSONSchema`（素の JSON Schema）によりモデル出力が**文法レベルでスキーマに制約**されます。
      JSON の破綻を事後修復ではなく発生源で防ぎ、`prominence` や `kind` は Enum 制約で不正値を排除します。
* **✏️ 編集モードによる再生成**:
    * シードの振り直しに加え、既存の生成済み画像に対する**指示ベースの部分編集**（`EditPrompt`）に対応。
      「構図はそのままで表情だけ笑顔に」のような修正がパネル・ページ単位で可能です。
* **📝 プロンプトはすべて DI 差し替え可能**:
    * 5操作すべてのプロンプトを `workflow.Args` から差し替えられます（`OutlinePrompt` /
      `ChapterScriptPrompt` / `DesignSheetPrompt` / `PanelPrompt` / `PagePrompt`。nil はキット既定）。
    * **キット内蔵の既定プロンプトは意図的に簡潔**です。参照画像との対応順・コマ数・読み順・
      文字を描かないことといった、外すと**形式が壊れる**指示だけを持ちます。画風の言い回しや
      コマ割りの演出は作品ごとに作り込むものなので、アプリ側で実装してください
      （キット内に置くと、プロンプトを1文字変えるたびにキットのリリースが必要になります）。
    * 章立て・章台本の既定は go:embed のテンプレートで、`.md` を置くだけでモードが増えます。
    * `GenerateOptions.PromptOverride` は呼び出し単位で**本文だけ**を差し替えます
      （システム指示とネガティブプロンプトは実装のものが残ります）。
* **🌍 Multi-Backend Asset Support**:
    * 参照画像の解決（Vertex AI + `gs://` は転送せず直接参照、Gemini API は File API へ1回だけ
      アップロードして使い回す）は gemini-image-kit が担います。キットは「どの画像を何番目に
      添付したか」だけを扱い、キャッシュと二重アップロード防止（singleflight）は
      画像キット側の実装です。
* **🔂 AI 呼び出しの重複排除**:
    * 同一内容のテキスト/画像生成リクエストの同時実行は `singleflight` で1回の API 呼び出しにまとめられます
      （Cloud Tasks の at-least-once 配信やリトライによる重複対策。プロセス内の in-flight が対象で、
      恒久的な冪等性は `GenerationRecord` を用いたアプリ側の判断で行います）。

---

## 📂 プロジェクト構造 (Project Structure)

本ライブラリは、**ports による抽象化**を境界とし、生成の各工程を独立した戦略として入れ替え可能な設計に基づいています。公開パッケージは実際の利用実態に合わせて `ports`・`asset`・`store`・`workflow` の4つに絞り、それ以外(工程の実行実体・プロンプト・レイアウト戦略)は `internal/` 配下に置いて外部から直接参照できないようにしています。

```text
go-comic-kit/
├── ports/                # 【契約・定義】Interface、MangaState データモデル、Config。※全ての起点。
├── workflow/              # 【統合管理】5つの操作を組み立て、Operations インターフェースを実装。singleflight による重複排除もここ。
├── store/                 # 【永続化】MangaState (comic_state.json) の Load/Save。
├── asset/                 # 【配置規約】成果物（パネル/ページ/デザインシート/state）の配置パスを決める唯一の場所。
└── internal/
    ├── operations/        # 【実行実体】Outline/Chapter/Design/Panel/Page の具体的なプロセス実装。
    ├── prompts/           # 【プロンプト】キット内蔵の簡潔な既定実装（workflow.Args で差し替え可能）。
    └── layout/            # 【定数】アスペクト比・画像サイズの定義と正規化。
```

`internal/operations` 等は `workflow` からしか使われない実装の詳細であり、将来これらへの直接アクセスが必要な消費側が現れた場合は、パッケージを `internal/` の外へ移動するだけで公開できます。

---

## 📐 スキーマ (Schema)

`ports.MangaState` が唯一の真実源です。台本は「章立て（Chapters）→ 章ごとのパネル生成」の
2段階で組み立てられ、1コマ（`Panel`）は発話の有無と独立した**登場キャラクターの集合**
（`Characters []PanelCharacter`）と、複数吹き出しに対応した `Dialogues []DialogueLine` を持ちます。

ページ割り（`Repaginate`）は `MaxPanelsPerPage` を上限としつつ、**章の境界で必ず改ページ**します
（前章の残りコマと次章の冒頭コマが同居したページを作らないため）。ページ構成が変わった
`PageArtifact` は同時に破棄されるので、実体とずれた古いページ画像が state に残りません。

```go
type MangaState struct {
	Version      int              // state スキーマバージョン
	ID           string           // 作品/ジョブID（キットは設定しない。呼び出し側が GenerateOutline 後に設定する）
	Title        string
	Description  string
	StyleMode    string           // アプリ側で使う画像スタイル識別子（記録されるのみで、キット内では生成に未使用）
	ScriptMode   string           // 台本プロンプトテンプレートの選択（再生成時に同一モードを使うため永続化）
	Chapters     []Chapter        // 章立て（GenerateOutline の成果物）
	DesignSheets []DesignSheetRef // 使用したデザインシートの記録
	Panels       []Panel
	Pages        []PageArtifact
	CreatedAt, UpdatedAt time.Time
}

type Chapter struct {
	ID            string   // 例: "ch01"
	Title         string
	Summary       string   // この章で扱う論点・狙い・オチ
	SourceExcerpt string   // 元文章の該当部分（引用または要約）
	PanelIDs      []string // GenerateChapterScript 実行後に紐づく
}

type Panel struct {
	ID           string            // 再生成ターゲティング用の安定ID（例: "ch01-p03"）
	ChapterID    string
	Page         int
	Shot         string            // "close-up" | "medium" | "wide" | "bird's-eye" 等
	Setting      string            // 場所・時間帯（例: "放課後の音楽室、夕方"）
	VisualAnchor string            // コマ全体の演出・構図の自由記述
	Characters   []PanelCharacter  // 登場キャラクター（発話の有無と独立）
	Dialogues    []DialogueLine    // 複数吹き出し対応
	Generation   *GenerationRecord // 生成結果の記録（再生成の基礎）
}

type PanelCharacter struct {
	CharacterID string
	Prominence  string // "primary" | "secondary" | "background"
	Emotion     string
	Action      string // 関係性はここに自由記述（例: "メタンの肩を掴んで揺さぶる"）
	Position    string
}

type DialogueLine struct {
	SpeakerID string // 空文字はナレーション/キャプション
	Text      string
	Kind      string // "speech" | "thought" | "shout" | "narration" | "sfx"
}

type GenerationRecord struct {
	ImageURL, Prompt, NegativePrompt, Model string
	UsedSeed    int64
	GeneratedAt time.Time
}

type DesignSheetRef struct {
	CharacterID string // 1キャラクターにつき1件（同じIDへの再生成は上書き）
	ImageURL    string
	UsedSeed    int64
}

type PageArtifact struct {
	PageNumber int
	PanelIDs   []string          // このページを構成したパネル（構成が変わると破棄される）
	Generation *GenerationRecord
}
```

state を読むアプリ向けに、`MangaState` は検索・更新のヘルパーを持ちます。

| メソッド | 用途 |
| --- | --- |
| `PanelByID` / `ChapterByID` / `PageArtifactByNumber` | ID・番号での取得 |
| `PanelsForPage` | 指定ページのパネル一覧（表示順） |
| `UniqueCharacterIDs` / `UniqueReferencedCharacterIDs` | 登場キャラの集合（後者は参照画像添付対象のみ） |
| `ReplaceChapterPanels` → `Repaginate` | 章のパネル差し替えとページ再割り当て（この順で呼びます） |
| `SetPageArtifact` / `SetDesignSheet` | 同一ページ番号・同一キャラクターへの upsert |

`Version` は `ports.StateSchemaVersion` です。`store.Load` はこれより新しいスキーマの state を拒否します（古いキットで新しい state を壊さないため）。

キャラクター間の関係性（誰が誰に何をしているか）は `PanelCharacter.Action` の自由記述で表現します
（構造化エッジより、生成AIへのプロンプトとして自然文の方が忠実に反映されるため）。
参照画像添付・複数キャラ同時生成の同一性維持の難度から、**primary + secondary は3体まで**
（`ports.MaxReferencedCharactersPerPanel`）です。台本生成の正規化で、これを超える登場者は
`background`（参照画像なし・モブとして描画）へ自動的に降格されます（primary を優先して残し、
コマ内の登場順は保たれます）。同じく、`characters.json` に無い `speaker_id` はナレーションに
変換されます（生の ID が話者名として描かれるのを防ぐため）。

---

## ⚙️ 設定と差し替え (Config / DI)

`workflow.New(Args)` に渡す `ports.Config` はゼロ値で構いません（`ApplyDefaults` が補完します）。

| 設定項目 | 役割 |
| --- | --- |
| `GeminiModel` | 台本生成（章立て・章台本）に使うテキストモデル |
| `ImageStandardModel` | パネル画像に使う標準・高速モデル |
| `ImageQualityModel` | デザインシート・ページ合成に使う高品質モデル |
| `MaxConcurrency` | 一括生成の最大並列数（1コマ・1ページ単位の操作には影響しません） |
| `RateInterval` | AI 呼び出しの発射間隔の下限。テキストと画像で**1つのリミッターを共有**します（クォータがプロジェクト単位のため）。**スループットの上限は `MaxConcurrency` ではなく 1/RateInterval で決まります** |
| `RequestTimeout` | AI 呼び出し1回あたりの上限（参照画像のアップロードにも適用）。工程列全体の上限ではありません |
| `StyleSuffix` | パネル・ページ画像の画風指定 |
| `DesignStyleSuffix` | デザインシートの画風指定。`StyleSuffix` と**分離**されています（演出照明がアンカーに焼き付くのを防ぐため） |
| `MaxChapters` / `MaxPanelsPerChapter` / `MaxPanelsPerPage` | 章数・章あたりのコマ数・1ページあたりのコマ数の上限 |

プロンプトは5操作すべて `workflow.Args` から差し替えられます（nil でキット内蔵の既定）。

| Args フィールド | インターフェース | 実装が受け取るデータ | キット内蔵の既定 |
| --- | --- | --- | --- |
| `OutlinePrompt` | `ports.OutlinePrompt` | `OutlinePromptData` | go:embed テンプレート（`.md` を置くだけでモード追加） |
| `ChapterScriptPrompt` | `ports.ChapterScriptPrompt` | `ChapterPromptData` | 同上 |
| `DesignSheetPrompt` | `ports.DesignSheetPrompt` | `DesignSheetPromptData` | 平叙な Go 実装（3面図・単一ポーズ） |
| `PanelPrompt` | `ports.PanelPrompt` | `PanelPromptData` | **簡潔版**（参照順・文字禁止・指5本のみ） |
| `PagePrompt` | `ports.PagePrompt` | `PagePromptData` | **簡潔版**（コマ数・読み順・参照番号のみ） |

画像系の `PanelPromptData.SubjectIDs` と `PagePromptData.CharacterFile` / `PanelFile` は、
**実際に添付される画像の順序**そのものです。プロンプト内で参照番号を書くときは必ずこれを使ってください
（自前で数え直すと、モデルが別人の参照画像を見ながら描くことになります）。

パネル・ページの既定が簡潔なのは意図的です。演出の作り込みは作品ごとに変わるため、
アプリ側で実装してください（キットに置くと、プロンプトを1文字変えるたびにキットの
リリースが必要になります）。利用側では `internal/adapters/prompts` のような層を設けて、
そこにプロンプトの組み立てをまとめる形にしています。

---

## 📦 成果物の配置 (asset)

生成物の保存先は `asset` パッケージが**唯一の決定者**です。アプリ側で `fmt.Sprintf` や
`path.Join` を書かず、必ずこれらを使ってください（規約が2か所に分かれると、片方の変更で
既存の成果物が見つからなくなります）。

| 関数 | 返すパス |
| --- | --- |
| `asset.StatePath(baseDir)` | state ドキュメント（`comic_state.json`） |
| `asset.PanelImagePath(baseDir, panelID, ext)` | パネル画像（`images/panel_{id}{ext}`） |
| `asset.PageImagePath(baseDir, page)` | ページ画像（`images/comic_page_{n}.png`） |
| `asset.DesignSheetPath(baseDir, charIDs, jobID, ext)` | デザインシート（`character/{tag}/{jobID}{ext}`） |
| `asset.CharacterDesignPrefix(baseDir, charIDs)` | 上記シートのディレクトリ（履歴の一覧に使います） |

`DesignFileTag` はキャラクターID群からディレクトリ名を作る際に、ファイル名長の上限を
超えないようルート境界で切り詰め、CRC32 を付けて衝突を避けます。`SanitizeFileName` /
`IsStateFileName` も同じ規約の一部として公開しています。

---

## 🔁 操作セット (Operations)

すべて冪等。`GenerateOutline` は原稿から state を新規作成し、以降の操作は state を受け取って
更新済み state を返します（state in/out）。

| 操作 (`ops.` フィールド) | インターフェース | 内容 |
| --- | --- | --- |
| `Outline.GenerateOutline` | `ports.OutlineGenerator` | 原稿から章立て（Chapters）のみの MangaState を生成 |
| `ChapterScript.GenerateChapterScript` | `ports.ChapterScriptGenerator` | 指定章のネーム（登場キャラ・セリフ・構図）を生成・置換 |
| `DesignSheet.GenerateDesignSheet` | `ports.DesignSheetGenerator` | キャラのDNA（Seed/特徴）を固定するデザインシートを生成 |
| `Panel.GeneratePanel` | `ports.PanelImageGenerator` | 指定パネルを個別に生成/再生成（同条件・新Seed・編集指示） |
| `Page.ComposePage` | `ports.PageImageComposer` | ページ単位で再レイアウト・合成 |
| `PanelBatch.GenerateAllPanels` | `ports.PanelBatchGenerator` | 全パネルを `MaxConcurrency` 並列で一括生成 |
| `PageBatch.ComposeAllPages` | `ports.PageBatchComposer` | 全ページを `MaxConcurrency` 並列で一括合成 |

`DesignSheetRequest.Override`（`ports.DesignOverride`）を使うと、その呼び出しに限って
キャラクターの参照画像・`visual_cues` を差し替えられます（`characters.json` は変更しません）。
単一キャラクター指定時のみ有効です。

一括生成（`ops.PanelBatch` / `ops.PageBatch`）は、一部が失敗しても**成功分を記録した
state とエラーの両方**を返します。state を保存してから `BatchOptions{SkipGenerated: true}`
で呼び直せば、未生成分だけをやり直せます（画像生成は高価なため）。

HTML/Markdown 等への出力工程はキットに含めません。閲覧・配信はアプリ側の責務で、
state ドキュメントと GCS 上の画像を直接読んで表現します。

---

## 🚨 エラーの分類 (Error Classification)

各操作のエラーは番兵エラーで包まれているため、消費側は `errors.Is` で分類だけを見て
応答を決められます（メッセージの文字列マッチは不要です）。

| 番兵エラー | 意味 | 想定する応答 |
| --- | --- | --- |
| `ports.ErrNotFound` | 指定の章・パネル・ページが state に無い | 404 |
| `ports.ErrInvalidRequest` | 必須項目の欠落、編集対象の画像が未生成 等 | 400 |
| `ports.ErrGeneration` | AI 呼び出しまたは応答の解釈に失敗、生成画像の保存に失敗 | 502（再試行の価値あり） |

画像の保存先パス生成の失敗（不正な `OutputDir`、ページ番号など）は引数が原因で再試行しても
直らないため `ErrInvalidRequest` に分類されます。保存そのものの失敗は一時的なことが多いので
`ErrGeneration` です。

---

## 🚀 クイックスタート (Quick Start)

`workflow.New` が設定とクライアント群から全操作を組み立てます。

```go
ops, err := workflow.New(workflow.Args{
	Config:          ports.Config{}, // ゼロ値は ApplyDefaults で補完される
	HTTPClient:      httpClient,     // go-http-kit
	Reader:          reader,         // ports.ContentReader（go-remote-io で GCS/ローカル/HTTP）
	Writer:          writer,
	AIClient:        aiClient,        // go-gemini-client。台本生成・パネル画像（標準品質）に使用
	AIClientQuality: aiClientQuality, // 省略可（nil なら AIClient を使用）。デザインシート・ページ合成（高品質）に使用
	Characters:      characters,      // go-character-kit (characters.json)
})
if err != nil {
	return err
}
defer ops.Close()

// 章立て → 章ごとの台本 → デザインシート → パネル → ページ
state, _ := ops.Outline.GenerateOutline(ctx, ports.OutlineRequest{SourceURL: "gs://bucket/article.md"})
state.ID = workID // 作品IDはキットが設定しないため、アプリ側で採番して設定する
state, _ = ops.ChapterScript.GenerateChapterScript(ctx, state, "ch01")
state, _ = ops.DesignSheet.GenerateDesignSheet(ctx, state, ports.DesignSheetRequest{
	CharacterIDs: []string{"zundamon"}, JobID: jobID, OutputDir: outDir,
})
state, _ = ops.Panel.GeneratePanel(ctx, state, "ch01-p01", ports.GenerateOptions{OutputDir: outDir})
state, _ = ops.Page.ComposePage(ctx, state, 1, ports.GenerateOptions{OutputDir: outDir})

// 全パネル・全ページの一括生成（Config.MaxConcurrency で並列化）
state, _ = ops.PanelBatch.GenerateAllPanels(ctx, state, ports.BatchOptions{OutputDir: outDir})
state, _ = ops.PageBatch.ComposeAllPages(ctx, state, ports.BatchOptions{OutputDir: outDir})

// state を保存（これが唯一の真実源。再生成はこの state を読み直して同じ操作を呼ぶだけ）
_, _ = store.Save(ctx, writer, state, outDir)
```

再生成の例: `ops.Panel.GeneratePanel(ctx, state, "ch01-p03", ports.GenerateOptions{Seed: &newSeed})`
（シード振り直し）、`ports.GenerateOptions{EditPrompt: "表情を笑顔に変える"}`（既存画像の部分編集）。

---

### 🤝 依存関係 (Dependencies)

* [shouni/gemini-image-kit](https://github.com/shouni/gemini-image-kit) - Gemini画像生成コア
* [shouni/go-character-kit](https://github.com/shouni/go-character-kit) - キャラクター資産（characters.json）管理
* [shouni/go-gemini-client](https://github.com/shouni/go-gemini-client) - Gemini API/Vertex AI クライアント（構造化出力対応）
* [shouni/go-remote-io](https://github.com/shouni/go-remote-io) - GCS/ローカル/HTTP 対応の読み書き抽象化
* [shouni/go-http-kit](https://github.com/shouni/go-http-kit) - HTTP クライアント抽象化
* [shouni/go-utils](https://github.com/shouni/go-utils) - 共通ユーティリティ

### 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
