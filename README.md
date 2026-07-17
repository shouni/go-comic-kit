# 🎨 Go Comic Kit

[![CI](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-comic-kit)](https://golang.org/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-comic-kit)](https://github.com/shouni/go-comic-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-comic-kit.svg)](https://pkg.go.dev/github.com/shouni/go-comic-kit)

## 🚀 概要 (About)

**Go Comic Kit** は、AIによる**キャラクターの一貫性を維持した漫画生成**のためのツールキットです。

---

## ✨ コア・コンセプト (Core Concepts)

* **📄 MangaState = 唯一の真実源**:
    * 1作品の全状態（台本・登場キャラ・生成条件・成果物URL）を1つの状態ドキュメントとして永続化。
      履歴一覧・詳細参照はアプリ側が state 一覧を読むだけで実現できます。
* **🔁 冪等・工程単位の操作**:
    * `GenerateOutline` / `GenerateChapterScript` / `GenerateDesignSheet` / `GeneratePanel` / `ComposePage` —
      すべて state を受け取り更新済み state を返します。**「12パネル中3番だけシードを振り直して再生成」**が
      API として表現でき、MCP ツール（`regenerate_panel` 等）と1対1で対応します。
* **👥 マルチキャラクター・パネル**:
    * パネルは「発話者1人」ではなく **登場キャラクターの集合**（`Characters []PanelCharacter`）として表現。
      感情・アクション（関係性）・配置・扱い（primary/secondary/background）を個別に指定でき、
      発話しないキャラクターにも参照画像が添付されるため同一性が崩れません。
* **🧬 3-Factor Consistency Control**:
    * **Seed値**（基盤）、**参照アセット**（外見）、**VisualCues/言語指示**（詳細）の3要素で
      キャラクターの一貫性を制御。生成条件は `GenerationRecord` として state に永続化されます。
* **📐 構造化出力（Constrained Decoding）**:
    * 台本生成は `ResponseSchema` によりモデル出力が**文法レベルでスキーマに制約**されます。
      JSON の破綻を事後修復ではなく発生源で防ぎ、`prominence` や `kind` は Enum 制約で不正値を排除します。
* **✏️ 編集モードによる再生成**:
    * シードの振り直しに加え、既存の生成済み画像に対する**指示ベースの部分編集**（`EditPrompt`）に対応。
      「構図はそのままで表情だけ笑顔に」のような修正がパネル・ページ単位で可能です。
* **📝 内蔵プロンプトテンプレート + DI差し替え**:
    * 章立て・章台本のプロンプトは go:embed のテンプレートを内蔵（`.md` を置くだけでモード追加）。
      章立て・章台本・デザインシートの3操作は `workflow.Args`（`OutlinePrompt` /
      `ChapterScriptPrompt` / `DesignSheetPrompt`）でアプリ側から完全に差し替え可能です。
      パネル・ページのプロンプトは構造化された `Panel` からキット内部で組み立てる方式で、
      差し替えの対象外です。
* **🌍 Multi-Backend Asset Support**:
    * Gemini API モードでは **File API**、Vertex AI モードでは **Cloud Storage (GCS)** 上の画像を直接参照。
      `singleflight` による二重アップロード防止つき。
* **🔂 AI 呼び出しの重複排除**:
    * 同一内容のテキスト/画像生成リクエストの同時実行は `singleflight` で1回の API 呼び出しにまとめられます
      （Cloud Tasks の at-least-once 配信やリトライによる重複対策。プロセス内の in-flight が対象で、
      恒久的な冪等性は `GenerationRecord` を用いたアプリ側の判断で行います）。

---

## 📐 スキーマ (Schema)

`ports.MangaState` が唯一の真実源です。台本は「章立て（Chapters）→ 章ごとのパネル生成」の
2段階で組み立てられ、1コマ（`Panel`）は発話の有無と独立した**登場キャラクターの集合**
（`Characters []PanelCharacter`）と、複数吹き出しに対応した `Dialogues []DialogueLine` を持ちます。

```go
type MangaState struct {
	Version      int              // state スキーマバージョン
	ID           string           // 作品/ジョブID
	Title        string
	Description  string
	StyleMode    string           // 画像生成スタイルの選択
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
```

キャラクター間の関係性（誰が誰に何をしているか）は `PanelCharacter.Action` の自由記述で表現します
（構造化エッジより、生成AIへのプロンプトとして自然文の方が忠実に反映されるため）。
参照画像添付・複数キャラ同時生成の同一性維持の難度から、**primary + secondary は3体まで**を
推奨上限とし、それを超える分は `background`（参照画像なし・モブとして描画）とします。

---

## 🔁 操作セット (Operations)

すべて冪等・state in/out。ap-comic の MCP ツールと1対1で対応します。

| 操作 | 内容 | 対応する MCP ツール |
| --- | --- | --- |
| `GenerateOutline` | 原稿から章立て（Chapters）のみの MangaState を生成 | `compose_comic`（第1工程） |
| `GenerateChapterScript` | 指定章のネーム（登場キャラ・セリフ・構図）を生成・置換 | `regenerate_chapter_script` |
| `GenerateDesignSheet` | キャラのDNA（Seed/特徴）を固定するデザインシートを生成 | `generate_design_sheet` |
| `GeneratePanel` | 指定パネルを個別に生成/再生成（同条件・新Seed・編集指示） | `regenerate_panel` |
| `ComposePage` | ページ単位で再レイアウト・合成 | `regenerate_page` |

HTML/Markdown 等への出力工程はキットに含めません。閲覧・配信はアプリ（ap-comic）側の責務で、
state ドキュメントと GCS 上の画像を直接読んで表現します。

---

## 🚀 クイックスタート (Quick Start)

`workflow.New` が設定とクライアント群から全操作を組み立てます。

```go
ops, err := workflow.New(workflow.Args{
	Config:     ports.Config{}, // ゼロ値は ApplyDefaults で補完される
	HTTPClient: httpClient,     // go-http-kit
	Reader:     reader,         // go-remote-io（GCS/ローカル/HTTP）
	Writer:     writer,
	AIClient:   aiClient,       // go-gemini-client (v1.11.0+)
	Characters: characters,     // go-character-kit (characters.json)
})
if err != nil {
	return err
}
defer ops.Close()

// 章立て → 章ごとの台本 → デザインシート → パネル → ページ
state, _ := ops.Outline.GenerateOutline(ctx, ports.OutlineRequest{SourceURL: "gs://bucket/article.md"})
state, _ = ops.ChapterScript.GenerateChapterScript(ctx, state, "ch01")
state, _ = ops.DesignSheet.GenerateDesignSheet(ctx, state, ports.DesignSheetRequest{
	CharacterIDs: []string{"zundamon"}, OutputDir: outDir,
})
state, _ = ops.Panel.GeneratePanel(ctx, state, "ch01-p01", ports.GenerateOptions{OutputDir: outDir})
state, _ = ops.Page.ComposePage(ctx, state, 1, ports.GenerateOptions{OutputDir: outDir})

// state を保存（これが唯一の真実源。再生成はこの state を読み直して同じ操作を呼ぶだけ）
_, _ = store.Save(ctx, writer, state, outDir)
```

再生成の例: `ops.Panel.GeneratePanel(ctx, state, "ch01-p03", ports.GenerateOptions{Seed: &newSeed})`
（シード振り直し）、`ports.GenerateOptions{EditPrompt: "表情を笑顔に変える"}`（既存画像の部分編集）。
