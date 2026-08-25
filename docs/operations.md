# 🔁 操作セット (Operations)

[← README](../README.md)

すべて冪等です。`GenerateOutline` が原稿から state を新規作成し、以降の操作は state を受け取って更新済み state を返します（state in / state out）。

| 操作 (`ops.` フィールド) | インターフェース | 内容 |
| --- | --- | --- |
| `Outline.GenerateOutline` | `ports.OutlineGenerator` | 原稿から章立て（Chapters）のみの MangaState を生成 |
| `ChapterScript.GenerateChapterScript` | `ports.ChapterScriptGenerator` | 指定章のネーム（登場キャラ・セリフ・構図）を生成・置換 |
| `DesignSheet.GenerateDesignSheet` | `ports.DesignSheetGenerator` | キャラのDNA（Seed/特徴）を固定するデザインシートを生成 |
| `Panel.GeneratePanel` | `ports.PanelImageGenerator` | 指定パネルを個別に生成/再生成（同条件・新Seed・編集指示） |
| `Page.ComposePage` | `ports.PageImageComposer` | ページ単位で再レイアウト・合成 |
| `Panel.GenerateAllPanels` | `ports.PanelImageGenerator` | パネルを `MaxConcurrency` 並列で一括生成（章で絞り込み可） |
| `Page.ComposeAllPages` | `ports.PageImageComposer` | ページを `MaxConcurrency` 並列で一括合成（章で絞り込み可） |

台本生成は2段階（章立て → 章ごとのパネル）に分かれています。1回の LLM 呼び出しに載せる JSON スキーマを小さく保つためと、章単位での再生成の粒度を得るためです。

**画像側も同じ粒度で絞れます。** `BatchOptions.ChapterID` を指定すると、その章のコマ・ページだけが対象になります。画像はこの工程でいちばん高価なので、「1章だけ作って確かめてから残りへ進む」を台本と同じ単位でできるようにしてあります。ページを章で絞れるのは、`Repaginate` が章境界でページを割る（1ページに2章を混ぜない）ためです。存在しない章 ID は `ErrNotFound` で、黙って0件成功にはしません。

HTML/Markdown 等への出力工程はキットに含めません。閲覧・配信はアプリ側の責務で、state ドキュメントと GCS 上の画像を直接読んで表現します。

## 呼び出し単位の上書き

| オプション | 対象 | 内容 |
| --- | --- | --- |
| `GenerateOptions.Seed` | パネル・ページ | シードの振り直し |
| `GenerateOptions.EditPrompt` | パネル・ページ | 既存の生成済み画像に対する指示ベースの部分編集（「構図はそのままで表情だけ笑顔に」） |
| `GenerateOptions.PromptOverride` | パネル・ページ | プロンプト本文の差し替え（システム指示・ネガティブプロンプトは残ります） |
| `Model` | すべての操作 | 使うモデル名（**必須**）。空は `ErrInvalidRequest` で、AI API は呼びません。1作品の台本は同じモデルが書くべきなので、章立てと全章へ同じ値を渡してください |
| `AspectRatio` / `ImageSize` | 各画像操作 | 比率と解像度。空ならキット既定（3:4 / パネル 1K / ページ・シート 2K）、未サポート値は `ErrInvalidRequest` |
| `GenerateOptions.StyleMode` / `BatchOptions.StyleMode` | パネル・ページ | この生成の画風モード。キットは中身を解釈せず、プロンプト実装へ `PanelPromptData.StyleMode` / `PagePromptData.StyleMode` として素通しします。解決済みの文言ではなくモード名を運ぶのは、画風指定と「その画風で避けたいもの」（ネガティブプロンプト）が対で決まり、両方を持っているのがプロンプト実装だからです |
| `DesignSheetRequest.Override`（`ports.DesignOverride`） | デザインシート | 参照画像・`visual_cues` をその場限りで差し替え（`characters.json` は変更しません）。**単一キャラクター指定時のみ有効** |

## 一括生成と再開

一括生成（`ops.PanelBatch` / `ops.PageBatch`）は、一部が失敗しても**成功分を記録した state とエラーの両方**を返します。画像生成は高価なので、state を保存してから `BatchOptions{SkipGenerated: true}` で呼び直せば、未生成分だけをやり直せます。

並列実行するため、`PanelResourceProvider` / `PageResourceProvider` の実装は並行呼び出し安全である必要があります。

## 🚨 エラーの分類

各操作のエラーは番兵エラーで包まれているため、消費側は `errors.Is` で分類だけを見て応答を決められます（メッセージの文字列マッチは不要です）。

| 番兵エラー | 意味 | 想定する応答 |
| --- | --- | --- |
| `ports.ErrNotFound` | 指定の章・パネル・ページが state に無い | 404 |
| `ports.ErrInvalidRequest` | 必須項目の欠落、編集対象の画像が未生成、モデル名が既定も上書きも無い 等 | 400 |
| `ports.ErrGeneration` | AI 呼び出しまたは応答の解釈に失敗、生成画像の保存に失敗 | 502（再試行の価値あり） |
| `ports.ErrConfigInvalid` | 現在この番兵を返す設定はありません（`Config` に必須項目が無いため）。将来設定を増やしたときの置き場です | 構築時のみ。返るようになれば `workflow.New` から出るので、起動時に落とします |

画像の保存先パス生成の失敗（不正な `OutputDir`、ページ番号など）は引数が原因で再試行しても直らないため `ErrInvalidRequest` に分類されます。保存そのものの失敗は一時的なことが多いので `ErrGeneration` です。
