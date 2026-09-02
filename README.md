# 🎨 Go Comic Kit

[![CI](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-comic-kit)](https://go.dev/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-comic-kit)](https://github.com/shouni/go-comic-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-comic-kit.svg)](https://pkg.go.dev/github.com/shouni/go-comic-kit)

## 🚀 概要 (About) - 1 作品 1 state、コマ単位で撃ち直せる漫画生成

**Go Comic Kit** は、AI による**キャラクターの一貫性を維持した漫画生成**のためのツールキットです。

1作品の全状態を1つの state ドキュメント（`MangaState`）に持ち、章立て・章台本・デザインシート・パネル・ページの各工程を**冪等な操作**として提供します。「12パネル中3番だけシードを振り直して再生成」が API としてそのまま表現できます。

---

## ✨ コア・コンセプト (Core Concepts)

* **📄 MangaState = 唯一の真実源**
  1作品の全状態（台本・登場キャラ・パネル/ページの生成条件・成果物URL）を1つの状態ドキュメントとして永続化。履歴一覧・詳細参照はアプリ側が state を読むだけで実現できます。

* **🔁 冪等・工程単位の操作**
  `GenerateOutline` が原稿から state を新規作成し、以降の操作は state を受け取って更新済み state を返します。MCP ツール（`regenerate_panel` 等）と1対1で対応します。

* **👥 マルチキャラクター・パネル**
  パネルは「発話者1人」ではなく **登場キャラクターの集合**（`Characters []PanelCharacter`）として表現。感情・アクション（関係性）・配置・扱い（primary/secondary/background）を個別に指定でき、発話しない primary/secondary キャラクターにも参照画像が添付されるため同一性が崩れません（background は参照画像の対象外）。

* **🧬 3-Factor Consistency Control**
  **Seed値**（基盤）、**参照アセット**（外見）、**VisualCues/言語指示**（詳細）の3要素でキャラクターの一貫性を制御し、生成条件は `GenerationRecord` として state に永続化されます。
  **シードは必ず記録されます。** 明示指定が無い場合も「前回値 → 主役キャラクターの Seed → 新規採番」の順に必ず決まった値を送るため、`UsedSeed` を読み直せばいつでも同じ絵を再現できます（シードを送らないと API 側が選んだ値は返ってこず、再現できなくなります）。

* **📐 構造化出力（Constrained Decoding）**
  台本生成は `ResponseJSONSchema`（素の JSON Schema）によりモデル出力が**文法レベルでスキーマに制約**されます。JSON の破綻を事後修復ではなく発生源で防ぎ、`prominence` や `kind` は Enum 制約で不正値を排除します。

* **✏️ 編集モードによる再生成**
  シードの振り直しに加え、既存の生成済み画像に対する**指示ベースの部分編集**（`EditPrompt`）に対応。「構図はそのままで表情だけ笑顔に」のような修正がパネル・ページ単位で可能です。

* **📝 プロンプトはすべてアプリが持つ（DI 必須）**
  5操作すべてのプロンプトを `workflow.Args` で**必ず注入**します。キットは内蔵プロンプトを持ちません。プロンプトは作品ごとに作り込む文言で、キットに置くと1文字変えるたびにキットのリリースが必要になるためです（[詳細](docs/configuration.md#プロンプトの注入)）。

* **🌍 Multi-Backend Asset Support**
  参照画像の送り方は、注入された AI クライアントのバックエンドに応じて `workflow` が組み立てます。Vertex AI なら `gs://` を転送せず直接参照し（キャッシュ不要）、Gemini API なら File API へ1回だけアップロードして使い回します。どちらの経路も、`gs://` 以外の参照（一時的な上書き URL など）は取得してインライン送信する受け皿を最後段に持ちます。実際の解決とキャッシュ・二重アップロード防止（singleflight）は gemini-image-kit の resolver が担い、このキットは「どの画像を何番目に添付したか」だけを扱います。

* **🔂 AI 呼び出しの重複排除**
  同一内容のテキスト/画像生成リクエストの同時実行は `singleflight` で1回の API 呼び出しにまとめられます（Cloud Tasks の at-least-once 配信やリトライ対策）。対象はプロセス内の in-flight のみで、恒久的な冪等性は `GenerationRecord` を用いたアプリ側の判断で行います。

---

## 📂 プロジェクト構造 (Project Structure)

**ports による抽象化**を境界とし、生成の各工程を独立した戦略として入れ替え可能にしています。公開パッケージは利用実態に合わせて5つに絞り、それ以外（工程の実行実体・比率の語彙）は `internal/` 配下に置いています。

```text
go-comic-kit/
├── comic/                 # 【データモデル】MangaState とその操作メソッド。※全ての起点。
├── ports/                 # 【契約】操作の Interface、入出力の型、Config。
├── workflow/              # 【統合管理】5つの操作を組み立て、Operations を実装。singleflight による重複排除もここ。
├── store/                 # 【永続化】MangaState (comic_state.json) の Load/Save。
├── asset/                 # 【配置規約】成果物の配置パスを決める唯一の場所。
└── internal/
    ├── operations/        # 【実行実体】Outline/Chapter/Design/Panel/Page の具体的なプロセス実装。
    └── layout/            # 【定数】アスペクト比・画像サイズの定義と正規化。
```

`internal/` 配下は `workflow` からしか使われない実装の詳細です。将来これらへの直接アクセスが必要な消費側が現れた場合は、パッケージを `internal/` の外へ移動するだけで公開できます。

---

## 🚀 クイックスタート (Quick Start)

`workflow.New` が設定とクライアント群から全操作を組み立てます。

```go
ops, err := workflow.New(workflow.Args{
	// Config は実行制御だけ（並列数・間隔・タイムアウト・各種上限）。すべて任意で、
	// ゼロ値なら ApplyDefaults が補完する。モデル・画風・比率・解像度は呼び出しごとに渡す
	Config:          ports.Config{},
	HTTPClient:      httpClient,      // httpkit.Streamer（参照画像の取得だけに使います）
	Reader:          reader,          // ports.ContentReader（go-remote-io で GCS/ローカル/HTTP）
	Writer:          writer,
	AIClient:        aiClient,        // go-gemini-client。台本生成・画像生成の両方に使用
	Characters:      characters,      // go-character-kit (characters.json)
})
if err != nil {
	return err
}

// 章立て → 章ごとの台本 → デザインシート → パネル → ページ
// モデル名は毎回渡す（必須）。画風・比率・解像度も同じくここで指定する
state, _ := ops.Outline.GenerateOutline(ctx, ports.OutlineRequest{
	SourceURL: "gs://bucket/article.md", Model: textModel,
})
state.ID = workID // 作品IDはキットが設定しないため、アプリ側で採番して設定する
state, _ = ops.ChapterScript.GenerateChapterScript(ctx, state, "ch01", ports.ChapterScriptOptions{Model: textModel})
state, _ = ops.DesignSheet.GenerateDesignSheet(ctx, state, ports.DesignSheetRequest{
	CharacterIDs: []string{"zundamon"}, JobID: jobID, OutputDir: outDir, Model: imageModel,
})
state, _ = ops.Panel.GeneratePanel(ctx, state, "ch01-p01", ports.GenerateOptions{OutputDir: outDir, Model: imageModel})
state, _ = ops.Page.ComposePage(ctx, state, 1, ports.GenerateOptions{OutputDir: outDir, Model: imageModel})

// 全パネル・全ページの一括生成（Config.MaxConcurrency で並列化）
state, _ = ops.Panel.GenerateAllPanels(ctx, state, ports.BatchOptions{OutputDir: outDir, Model: imageModel})
state, _ = ops.Page.ComposeAllPages(ctx, state, ports.BatchOptions{OutputDir: outDir, Model: imageModel})

// state を保存（これが唯一の真実源。再生成はこの state を読み直して同じ操作を呼ぶだけ）
_, _ = store.Save(ctx, writer, state, outDir)
```

再生成の例: `ops.Panel.GeneratePanel(ctx, state, "ch01-p03", ports.GenerateOptions{Seed: &newSeed})`（シード振り直し）、`ports.GenerateOptions{EditPrompt: "表情を笑顔に変える"}`（既存画像の部分編集）。

---

## 📚 ドキュメント

| ドキュメント | 内容 |
| --- | --- |
| [スキーマ (comic.MangaState)](docs/schema.md) | データモデル、state ヘルパー、ページ割りとキャラクター正規化の規則 |
| [設定と差し替え (Config / DI)](docs/configuration.md) | `ports.Config`、プロンプトの DI、`workflow.Args` の依存 |
| [操作セット (Operations)](docs/operations.md) | 5操作と一括生成、呼び出し単位の上書き、エラーの分類 |
| [成果物の配置 (asset)](docs/assets.md) | 保存パスの規約とファイル名の生成 |

---

## 🤝 依存関係 (Dependencies)

* [shouni/gemini-image-kit](https://github.com/shouni/gemini-image-kit) - Gemini画像生成コア（参照画像の解決もここが担います）
* [shouni/go-character-kit](https://github.com/shouni/go-character-kit) - キャラクター資産（characters.json）管理
* [shouni/go-gemini-client](https://github.com/shouni/go-gemini-client) - Gemini API/Vertex AI クライアント（構造化出力対応）
* [shouni/go-remote-io](https://github.com/shouni/go-remote-io) - GCS/ローカル/HTTP 対応の読み書き抽象化
* [shouni/go-http-kit](https://github.com/shouni/go-http-kit) - HTTP クライアント抽象化

## 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
