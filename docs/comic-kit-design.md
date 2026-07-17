# go-comic-kit 設計案（go-manga-kit 後継 / ap-comic 前提）

Status: Draft（2026-07-17）

> go-manga-kit の `/v2` として実装する案も検討したが、消費者が完全に分かれる
> （go-manga-kit = ap-manga-web ショーケース専用として凍結、go-comic-kit = ap-comic 用）こと、
> ap-comic とのブランド整合から、**新リポジトリ go-comic-kit** として開始した。
> ゼロから書き直すのではなく、go-manga-kit の実証済み基盤コードを順次移植し、
> 契約（API・データモデル）だけを本設計に差し替える。

## 1. 背景と目的

MCP 対応の新プロジェクト **ap-comic** では、以下が中核要件になる。

- **履歴一覧**: 作品（ジョブ）単位の一覧・詳細参照
- **パネル単位の再生成**: 動画側の `regenerate_cut_keyframe` と同様に、「12パネル中3番だけシードを振り直して再生成」できること
- **1パネル内の複数キャラクターと関係性の表現**（本設計の最重要変更点）

現行 v1 の限界:

| 現行の構造 | 問題 |
|---|---|
| `Panel.SpeakerID string`（単一） | 1パネル=1キャラを暗黙に固定。同席キャラ・聞き手・背景キャラを構造として表現できない |
| 参照画像収集が `UniqueSpeakerIDs` 基準 | **発話しないキャラには参照画像（デザインシート）が添付されず、同一性が崩れる** |
| `Dialogue string`（単一） | 1パネル複数吹き出し、ナレーション、心の声、SFX を区別できない |
| 生成条件（seed / prompt / model）が非永続 | `UsedSeed` は design 以外で破棄。plot JSON を読み直しても同条件再生成が不可能 |
| 全パネル一括生成 API（`Execute(panels)`） | 単一パネルの再生成を API として表現できない |

## 2. 設計原則

1. **MangaState を唯一の真実源（source of truth）にする。** GCS 上の state ドキュメント一覧がそのまま履歴になる。履歴一覧・ジョブ管理・キャッシュはアプリ（ap-comic）の責務で、kit は state の形式と操作だけを定義する。
2. **操作は工程単位で冪等。** すべて state を受け取り、更新済み state を返す。
3. **パネル内のキャラクターは「発話者」ではなく「登場者」として第一級で表現する。** 発話は登場とは独立した属性。

## 3. スキーマ

```go
// MangaState は1作品の全状態を保持する永続ドキュメントです（旧 plot JSON の後継）。
type MangaState struct {
	Version      int               `json:"version"`       // state スキーマバージョン（=1 から開始）
	ID           string            `json:"id"`            // 作品/ジョブID
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	StyleMode    string            `json:"style_mode"`    // 画像生成スタイルの選択（visual_mode 相当）
	ScriptMode   string            `json:"script_mode"`   // 台本プロンプトテンプレートの選択（再生成時に同一モードを使うため永続化）
	Chapters     []Chapter         `json:"chapters"`      // ★章立て（台本生成の第1段の成果物）
	DesignSheets []DesignSheetRef  `json:"design_sheets"` // 使用したデザインシートの記録
	Panels       []Panel           `json:"panels"`
	Pages        []PageArtifact    `json:"pages"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// Chapter は作品の1章（台本生成の単位）を表します。
// 台本は「章立て（GenerateOutline）→ 章ごとのパネル生成（GenerateChapterScript）」の
// 2段階で生成します。リッチな Panel スキーマを一発で大量生成すると JSON の破綻率と
// 品質のブレが上がるため、1回の LLM 呼び出しの複雑度を章単位に抑えます。
// 章単位の台本再生成（regenerate_chapter_script）の粒度もこれで揃います。
type Chapter struct {
	ID            string   `json:"id"`             // 例: "ch01"
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`        // この章で扱う論点・狙い・オチ
	SourceExcerpt string   `json:"source_excerpt"` // 元文章の該当部分（引用または要約）
	PanelIDs      []string `json:"panel_ids,omitempty"` // GenerateChapterScript 実行後に紐づく
}

// DesignSheetRef は、この作品の同一性アンカーとして使ったシートの記録です。
type DesignSheetRef struct {
	CharacterID string `json:"character_id"`
	ImageURL    string `json:"image_url"`
	UsedSeed    int64  `json:"used_seed"`
}

// Panel は漫画の1コマを表します。
type Panel struct {
	ID           string            `json:"id"`   // 再生成ターゲティング用の安定ID（例: "ch01-p03"）
	ChapterID    string            `json:"chapter_id,omitempty"` // 所属する章
	Page         int               `json:"page"`
	Shot         string            `json:"shot,omitempty"`    // "close-up" | "medium" | "wide" | "bird's-eye" 等
	Setting      string            `json:"setting,omitempty"` // 場所・時間帯（例: "放課後の音楽室、夕方"）
	VisualAnchor string            `json:"visual_anchor"`     // コマ全体の演出・構図の自由記述（v1踏襲）
	Characters   []PanelCharacter  `json:"characters"`        // ★登場キャラクター（発話の有無と独立）
	Dialogues    []DialogueLine    `json:"dialogues"`         // ★複数吹き出し
	Generation   *GenerationRecord `json:"generation,omitempty"` // 生成結果の記録（再生成の基礎）
}

// PanelCharacter は、コマへの1キャラクターの登場のしかたを表します。
type PanelCharacter struct {
	CharacterID string `json:"character_id"`
	Prominence  string `json:"prominence,omitempty"` // "primary" | "secondary" | "background"
	Emotion     string `json:"emotion,omitempty"`    // 例: "驚き", "怒りを堪えている"
	Action      string `json:"action,omitempty"`     // 例: "メタンの肩を掴んで揺さぶる" ← 関係性はここに自由記述
	Position    string `json:"position,omitempty"`   // 例: "left foreground", "background right"
}

// DialogueLine は1つの吹き出し・ナレーションを表します。
type DialogueLine struct {
	SpeakerID string `json:"speaker_id,omitempty"` // 空文字はナレーション/キャプション
	Text      string `json:"text"`
	Kind      string `json:"kind,omitempty"` // "speech" | "thought" | "shout" | "narration" | "sfx"
}

// GenerationRecord は画像がどの条件で生成されたかの完全な記録です。
// これがあることで「同条件で再生成」「シードだけ変えて再生成」が可能になります。
type GenerationRecord struct {
	ImageURL       string    `json:"image_url"`
	UsedSeed       int64     `json:"used_seed"`
	Prompt         string    `json:"prompt"`
	NegativePrompt string    `json:"negative_prompt,omitempty"`
	Model          string    `json:"model"`
	GeneratedAt    time.Time `json:"generated_at"`
}

// PageArtifact は複数パネルを1枚に合成したページ画像の記録です。
type PageArtifact struct {
	PageNumber int               `json:"page_number"`
	PanelIDs   []string          `json:"panel_ids"`
	Generation *GenerationRecord `json:"generation,omitempty"`
}
```

### 関係性の表現方針

キャラクター間の関係性（誰が誰に何をしているか）は、初期版では **`PanelCharacter.Action` の自由記述**で表現する（例: `"ずんだもんを睨みつける"`）。

- 生成AIへのプロンプトとしては構造化エッジより自然文の方が忠実に反映される
- 台本生成モデルの出力スキーマを複雑にするほど JSON 生成の失敗モードが増える
- 将来、関係性の機械的な検証・活用（相関図生成等）が必要になった時点で
  `Interactions []struct{From, To, Kind string}` を追加すればよい（後方互換の追加になる）

### 1パネルあたりのキャラクター数の実務上の上限

参照画像を添付できる数と、複数キャラ同時生成の同一性維持の難度から、**primary + secondary で3体まで**を推奨上限とし、それを超える分は `Prominence: "background"`（参照画像なし・モブとして描画）とする。この制約は台本生成プロンプトに明記する。

## 4. 波及効果（go-manga-kit からの主な変更 / 実装済み）

1. **参照画像収集**: `UniqueSpeakerIDs()` → `Panel.ReferencedCharacterIDs()`（primary/secondary のみ、
   background は除外）に変更。発話しないキャラにもデザインシートが添付され、同一性が
   維持される（本設計の最大の実益）。
2. **画像プロンプトはキット内蔵**: v1 でアプリ側ポートだった `ImagePrompt` は廃止し、
   パネル/ページのプロンプト構築は構造化された Panel からキット内部で組み立てる
   （`runner/panel.go` / `runner/page.go`）。複数参照画像とキャラ記述は design と同じ
   `[Subject N: ...]` / `input_file_N` 方式で対応付ける。
3. **台本生成は2段階 + 構造化出力**: 章立て（GenerateOutline）→ 章ごとのネーム
   （GenerateChapterScript）に分割し、`ResponseSchema` による constrained decoding で
   JSON を文法レベルに制約する。Shot / Emotion / Position をモデルに書かせることは、
   画像品質だけでなく台本自体の演出品質も引き上げる。
4. **セリフの描き分け**: `Dialogues` 複数対応。ページ合成時に kind 別
   （speech / shout / thought / narration / sfx）で吹き出し・キャプション・描き文字を描き分ける。
5. **バリデーション**: 台本生成時に characters.json に解決できない `character_id` は
   background（参照なしのモブ）へ降格する（デフォルトキャラへの暗黙フォールバックで
   別人が描かれる事故の防止）。

## 5. 操作セット（go-comic-kit API 案）

すべて冪等・state in/out。MCP ツールと1対1対応。

| 操作 | 対応する MCP ツール（ap-comic） |
|---|---|
| `GenerateOutline(ctx, req) → *MangaState` | `compose_comic`（の第1工程: 章立てのみ生成） |
| `GenerateChapterScript(ctx, state, chapterID) → state` | `regenerate_chapter_script` ★（章単位の台本再生成） |
| `GenerateDesignSheet(ctx, state, req) → state` | `generate_design_sheet` |
| `GeneratePanel(ctx, state, panelID, opts) → state` | `regenerate_panel` ★ |
| `ComposePage(ctx, state, page, opts) → state` | `regenerate_page` |

> HTML/Markdown 等への出力（v1 の Publish 工程）はキットの操作セットに含めない。
> 閲覧・配信は ap-comic 側の責務で、state ドキュメントと GCS 上の画像を直接読んで表現する。

`GenerateOutline` は元文章（URL またはテキスト）から Chapters のみを持つ state を作る。
`GenerateChapterScript` は章立て全体を文脈として渡しつつ指定章のパネル群を生成し、
既存の同章パネルを置き換える（冪等）。プロンプトはキット内蔵のテンプレート
（go:embed、キャラクター一覧は characters.json から自動注入）を既定とし、
アプリはプロンプトビルダーのポートを差し替えることでカスタマイズできる。

`GenerateDesignSheet` の `req`（`DesignSheetRequest`）は `CharacterIDs []string` を取り、
複数指定時は1枚の合成シート（v1 の機能を継承）として生成して各キャラクターに同じ画像を記録する。
state が nil の場合は新しい state を作成する（台本より先にアンカーだけ作る運用向け）。

`GeneratePanel` の `opts`（`GenerateOptions`）:
- `Seed *int64` — nil なら前回と同じ（GenerationRecord.UsedSeed を再利用）、指定すれば振り直し
- `EditPrompt string` — 指定すると既存の生成済み画像を入力とした**編集モード**になり、
  構図・ポーズ・背景を保ったまま指示箇所だけを変更する
  （go-veo-orchestrator の `EditCut` と同方式。`regenerate_cut_keyframe` で実証済みのパターン）
- `PromptOverride string` / `ModelOverride string`

## 6. 立ち上げ・移行方針（移植は完了）

- ✅ go-manga-kit からの実証済みコードの移植は完了: File API アップロードキャッシュ
  （singleflight）、Vertex/API バックエンド分岐、合成デザインシート、DesignOverride、
  v1.12.2 で入れたデザインプロンプト改善（SystemPrompt / NegativePrompt / 指の本数対策）。
  契約は本設計のデータモデル・操作セットへ差し替え済み。
- ✅ 先行改善候補として挙げていた4点（デザインプロンプトのテンプレート化、StyleSuffix の
  ワークフロー別分離、DesignRequest 構造体化、runner→layout の狭いインターフェース化）は
  すべて本設計に吸収して実装済み。
- **go-manga-kit は ap-manga-web（ショーケース）専用として凍結**（バグ修正のみ）。
  ap-comic は最初から go-comic-kit のみを使う。
- 未実装（必要になったら追加）: 旧 plot JSON → MangaState の変換関数
  （`SpeakerID` → `Characters: [{CharacterID: SpeakerID, Prominence: "primary"}]`、
  `Dialogue` → `Dialogues: [{SpeakerID, Text: Dialogue, Kind: "speech"}]`）。
  ap-manga-web の過去資産を ap-comic に取り込む場合のみ必要。
