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
	TextModel    string           // この作品の台本を書いたモデル（記録のみ。呼び出し側が設定・参照する）
	ImageModel   string           // この作品の画像を描いたモデル（記録のみ。呼び出し側が設定・参照する）
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
| `PanelsForPage` / `PanelsForChapter` | 指定ページ・指定章のパネル一覧（表示順） |
| `PagesForChapter` | 指定章のコマが載るページ番号（章単位の一括合成の対象） |
| `UniqueCharacterIDs` / `UniqueReferencedCharacterIDs` | 登場キャラの集合（後者は参照画像添付対象のみ） |
| `ReplaceChapterPanels` → `Repaginate` | 章のパネル差し替えとページ再割り当て（**この順で呼びます**） |
| `SetPageArtifact` / `SetDesignSheet` | 同一ページ番号・同一キャラクターへの upsert |

## ページ割りの規則

`Repaginate(maxPerPage)` は `MaxPanelsPerPage` を上限としつつ、**章の境界で必ず改ページ**します（前章の残りコマと次章の冒頭コマが同居したページを作らないため）。

ページ構成が変わった `PageArtifact` は同時に破棄されるので、実体とずれた古いページ画像が state に残りません。構成が変わっていないページの成果物は保持されるため、1章を再生成しても無関係なページの再合成は発生しません。

## キャラクターの正規化ルール

キャラクター間の関係性（誰が誰に何をしているか）は `PanelCharacter.Action` の自由記述で表現します。構造化エッジより、生成 AI へのプロンプトとして自然文の方が忠実に反映されるためです。

台本生成の正規化では、AI の出した ID をそのまま信用せず次の2点を機械的に補正します。

* `characters.json` に無い `character_id` は `background`（参照画像なし・モブとして描画）へ降格されます。参照解決が既定キャラクターへ暗黙にフォールバックして、別人が描かれるのを防ぐためです。
* `characters.json` に無い `speaker_id` はナレーションへ変換されます。生の ID が話者名の位置に描かれるのを防ぐためです。

## 参照画像を添付するキャラクターの上限

参照画像が増えるほどキャラクター同士の同一性維持は崩れ、顔や衣装の取り違えが起きます。そのため2段階で頭打ちにします。

| 上限 | 定数 | 効くタイミング | あふれた分の扱い |
| --- | --- | --- | --- |
| 1コマあたり primary + secondary で3体 | `comic.MaxReferencedCharactersPerPanel` | 台本生成の正規化 | `background` へ降格。primary が優先して残り、コマ内の登場順は保たれます |
| 1ページあたり4体 | `comic.MaxReferencedCharactersPerPage` | ページ合成 | 参照画像なし（プロンプトの文章での指定のみ）。そのページに多く登場するキャラクターが残り、同数なら初出順です |

ページ側にも上限が要るのは、1ページに複数のコマが載るうえ、合成時にはそれぞれの生成済みコマ画像も構図ガイドとして添付されるためです。コマ側の上限だけでは、1回の合成が10枚を超える参照を抱えます。
