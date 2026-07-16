# 🎨 Go Comic Kit

[![CI](https://github.com/shouni/go-manga-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-manga-kit/actions/workflows/ci.yml)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Status](https://img.shields.io/badge/Status-Completed-brightgreen)](#)

## 🚀 概要 (About) - キャラクターDNA維持・画像生成Workflow

**Go Comic Kit** は、非構造化ドキュメントを解析し、AIによる**キャラクターDNAの一貫性を維持した画像生成**を行うためのツールキットです。

[Gemini Image Kit](https://github.com/shouni/gemini-image-kit) を描画コアに採用。独自の**Seedシンクロナイズ機能**と**Dynamic Asset Mapping**により、複数ページにわたる作品でもキャラクターを固定することが可能です。

また、**並列実行制御（Semaphore）** と **APIレート制限** により、Vertex AI などのクォータ制限下でも、安定した大規模生成パイプラインの構築を実現します。

---

## ✨ コア・コンセプト (Core Concepts)

* **🧬 3-Factor Consistency Control**:
    * キャラクターの一貫性を担保するため、**Seed値**（基盤）、**参照アセット**（外見）、**VisualCues/言語指示**（詳細）の3要素を組み合わせて制御します。
* **🌍 Multi-Backend Asset Support**:
    * Gemini API モードでは **File API**、Vertex AI モードでは **Cloud Storage (GCS)** 上の画像を直接参照可能です。
* **🛡 Production-Ready Concurrency Control**:
    * セマフォ（Semaphore）を用いた細やかな並列実行制御を内包。API の `RESOURCE_EXHAUSTED` (429) エラーを未然に防ぎ、スロットルを効かせた堅牢なバッチ処理を可能にします。
* **⚡  Smart Asset Management**:
    * Vertex AI 利用時は `gs://` パスをそのまま使用することで、アップロードのオーバーヘッドを軽減します。
    * Gemini API 利用時は `singleflight` により同一URLの二重アップロードを防止。Gemini File API クォータを節約しながら、並列アセット準備を実現します。

---

## 🎨 5つのワークフロー (Workflows)

| ワークフロー | 担当インターフェース | 内容 |
| --- | --- | --- |
| **1. Designing** | `DesignRunner` | キャラのDNA（Seed/特徴）を固定し、デザインシートを生成。 |
| **2. Scripting** | `ScriptRunner` | 原稿から、キャラ・セリフ・構図を含むJSON台本を生成。 |
| **3. Panel Gen** | `PanelImageRunner` |各パネルを、キャラ固有Seedを用いて個別に高精度生成。 |
| **4. Page Gen**   | `PageImageRunner` | 台本に基づき、ページ単位で再レイアウト・一括作画。 |
| **5. Publishing** | `PublishRunner` | 画像とテキストを統合し、HTML/Markdown等で出力。 |

---