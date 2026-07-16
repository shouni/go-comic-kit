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
> 合成デザインシート等）は go-manga-kit から順次移植します。

📐 **設計文書**: [docs/comic-kit-design.md](docs/comic-kit-design.md) — スキーマ・操作セット・移行方針の詳細はこちら。

---

## ✨ コア・コンセプト (Core Concepts)

* **📄 MangaState = 唯一の真実源**:
    * 1作品の全状態（台本・登場キャラ・生成条件・成果物URL）を1つの状態ドキュメントとして永続化。
      履歴一覧・詳細参照はアプリ側が state 一覧を読むだけで実現できます。
* **🔁 冪等・工程単位の操作**:
    * `GenerateScript` / `GenerateDesignSheet` / `GeneratePanel` / `ComposePage` / `Publish` —
      すべて state を受け取り更新済み state を返します。**「12パネル中3番だけシードを振り直して再生成」**が
      API として表現でき、MCP ツール（`regenerate_panel` 等）と1対1で対応します。
* **👥 マルチキャラクター・パネル**:
    * パネルは「発話者1人」ではなく **登場キャラクターの集合**（`Characters []PanelCharacter`）として表現。
      感情・アクション（関係性）・配置・扱い（primary/secondary/background）を個別に指定でき、
      発話しないキャラクターにも参照画像が添付されるため同一性が崩れません。
* **🧬 3-Factor Consistency Control**:
    * **Seed値**（基盤）、**参照アセット**（外見）、**VisualCues/言語指示**（詳細）の3要素で
      キャラクターの一貫性を制御。生成条件は `GenerationRecord` として state に永続化されます。
* **🌍 Multi-Backend Asset Support**:
    * Gemini API モードでは **File API**、Vertex AI モードでは **Cloud Storage (GCS)** 上の画像を直接参照。
* **🛡 Production-Ready Concurrency Control**:
    * セマフォによる並列実行制御と `singleflight` による二重アップロード防止で、
      クォータ制限下でも安定した大規模生成パイプラインを構築できます。

---

## 🔁 操作セット (Operations)

すべて冪等・state in/out。ap-comic の MCP ツールと1対1で対応します。

| 操作 | 内容 | 対応する MCP ツール |
| --- | --- | --- |
| `GenerateScript` | 原稿から、登場キャラ・セリフ・構図を含む MangaState を生成 | `compose_comic`（第1工程） |
| `GenerateDesignSheet` | キャラのDNA（Seed/特徴）を固定するデザインシートを生成 | `generate_design_sheet` |
| `GeneratePanel` | 指定パネルを個別に生成/再生成（同条件 or 新Seed） | `regenerate_panel` |
| `ComposePage` | ページ単位で再レイアウト・合成 | `regenerate_page` |
| `Publish` | 画像とテキストを統合し、HTML/Markdown 等で出力 | `publish_comic` |

---

## 🚧 開発ステータス

設計フェーズ。実装は [docs/comic-kit-design.md](docs/comic-kit-design.md) に基づき、
go-manga-kit からの基盤コード移植 → 新データモデル（`MangaState` / `Panel` / `PanelCharacter`）の実装 →
操作セットの実装、の順で進めます。
