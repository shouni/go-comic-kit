# 📐 スキーマ (MangaState)

[← README](../README.md)

`comic.MangaState` が1作品の唯一の真実源です。台本は「章立て（Chapters）→ 章ごとのパネル生成」の2段階で組み立てられ、1コマ（`Panel`）は発話の有無と独立した**登場キャラクターの集合**（`Characters []PanelCharacter`）と、複数吹き出しに対応した `Dialogues []DialogueLine` を持ちます。

## データモデル

```go
type MangaState struct {
	Version      int              // state スキーマバージョン（comic.StateSchemaVersion）
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

`Version` は `comic.StateSchemaVersion` です。`store.Load` はこれより新しいスキーマの state を拒否します（古いキットで新しい state を壊さないため）。

## state を読むためのヘルパー

| メソッド | 用途 |
| --- | --- |
| `PanelByID` / `ChapterByID` / `PageArtifactByNumber` | ID・番号での取得 |
| `PanelsForPage` | 指定ページのパネル一覧（表示順） |
| `UniqueCharacterIDs` / `UniqueReferencedCharacterIDs` | 登場キャラの集合（後者は参照画像添付対象のみ） |
| `ReplaceChapterPanels` → `Repaginate` | 章のパネル差し替えとページ再割り当て（**この順で呼びます**） |
| `SetPageArtifact` / `SetDesignSheet` | 同一ページ番号・同一キャラクターへの upsert |

## ページ割りの規則

`Repaginate(maxPerPage)` は `MaxPanelsPerPage` を上限としつつ、**章の境界で必ず改ページ**します（前章の残りコマと次章の冒頭コマが同居したページを作らないため）。

ページ構成が変わった `PageArtifact` は同時に破棄されるので、実体とずれた古いページ画像が state に残りません。構成が変わっていないページの成果物は保持されるため、1章を再生成しても無関係なページの再合成は発生しません。

## キャラクターの正規化ルール

キャラクター間の関係性（誰が誰に何をしているか）は `PanelCharacter.Action` の自由記述で表現します。構造化エッジより、生成 AI へのプロンプトとして自然文の方が忠実に反映されるためです。

台本生成の正規化では、AI の出力をそのまま信用せず次の2つを機械的に補正します。

* **primary + secondary は3体まで**（`comic.MaxReferencedCharactersPerPanel`）。参照画像添付と複数キャラ同時生成の同一性維持の難度が理由です。超過分は `background`（参照画像なし・モブとして描画）へ降格され、primary が優先して残り、コマ内の登場順は保たれます。
* `characters.json` に無い `speaker_id` はナレーションへ変換されます（生の ID が話者名として描かれるのを防ぐため）。同じく未知の `character_id` は `background` へ降格されます（参照解決が既定キャラクターへフォールバックして別人を描くのを防ぐため）。
