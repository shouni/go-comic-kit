# 🎨 Go Comic Kit

[![CI](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Status](https://img.shields.io/badge/Status-In%20Development-orange)](#)

## 🚀 概要 (About)

**Go Comic Kit** は、AIによる**キャラクターの一貫性を維持した漫画生成**のためのツールキットです。
[go-manga-kit](https://github.com/shouni/go-manga-kit) の後継として、MCP 対応オーケストレータ **ap-comic** を第一の利用者に想定し、契約（データモデル・API）を刷新して開発しています。

> **go-manga-kit との関係**: go-manga-kit は ap-manga-web（ショーケース）専用として凍結し、
> 新機能はすべて本リポジトリで開発します。実証済みの基盤コード
> （singleflight による File API アップロード重複排除、Vertex AI / Gemini API バックエンド分岐、
> 合成デザインシート等）は移植済みです。

📐 **設計文書**: [docs/comic-kit-design.md](docs/comic-kit-design.md) — スキーマ・操作セット・移行方針の詳細はこちら。

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
* **📝 内蔵プロンプトテンプレート**:
    * 章立て・章台本のプロンプトは go:embed のテンプレートを内蔵（`.md` を置くだけでモード追加）。
      アプリ側でポートを実装すれば完全に差し替えることもできます。
* **🌍 Multi-Backend Asset Support**:
    * Gemini API モードでは **File API**、Vertex AI モードでは **Cloud Storage (GCS)** 上の画像を直接参照。
      `singleflight` による二重アップロード防止つき。

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

---

## 🚧 開発ステータス

コア実装は完了しています（[docs/comic-kit-design.md](docs/comic-kit-design.md) 参照）:

- ✅ `MangaState` データモデルと state 永続化（`store` パッケージ）
- ✅ 操作セット5つ（章立て / 章台本 / デザインシート / パネル / ページ）+ 編集モード
- ✅ 構造化出力（ResponseSchema）による台本 JSON の安定化
- ✅ DI 層（`workflow.New`）と内蔵プロンプトテンプレート

今後: 実環境（Gemini / Vertex AI）での E2E 検証、初回リリースタグ、ap-comic（MCP オーケストレータ）の開発。
