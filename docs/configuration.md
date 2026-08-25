# ⚙️ 設定と差し替え (Config / DI)

[← README](../README.md)

## ports.Config

`workflow.New(Args)` に渡す設定です。**必須項目はありません**。すべてゼロ値で構いません（`ApplyDefaults` が補完します）。

ここに残るのは、キットを壊さず動かすための実行制御だけです。**モデル名・画風・比率・解像度は Config にありません** — どれも作品ごとに変わる値なので、`GenerateOptions` / `BatchOptions` / `DesignSheetRequest` / `OutlineRequest` / `ChapterScriptOptions` が呼び出しごとに受け取ります。デプロイ単位の既定をここに置くと、毎回指定するアプリでは一度も使われない値を管理させることになり、しかも指定漏れをその値が黙って隠します。

呼び出しごとの値の検証は各操作が実行前に行い、`ErrInvalidRequest` を返します（AI API は呼びません）。モデル名の空、未サポートの比率・解像度がこれにあたります。**書き間違いを既定へ落とさない**のは従来どおりで、落とすと「指定したつもりの比率で生成されない」状態が気付かれずに続くためです。

| 設定項目 | 役割 |
| --- | --- |
| `MaxConcurrency` | 一括生成の最大並列数（1コマ・1ページ単位の操作には影響しません） |
| `RateInterval` | AI 呼び出しの発射間隔の下限。テキストと画像で**1つのリミッターを共有**します（クォータがプロジェクト単位のため）。**スループットの上限は `MaxConcurrency` ではなく 1/RateInterval で決まります** |
| `RequestTimeout` | AI 呼び出し1回あたりの上限（参照画像のアップロードにも適用）。工程列全体の上限ではありません |
| `CacheControl` | 生成画像を保存する際の `Cache-Control`。空なら `ports.DefaultCacheControl`（`public, max-age=1800`）。非公開バケットへ書くデプロイでは `private` 等を指定してください |
| `MaxChapters` / `MaxPanelsPerChapter` / `MaxPanelsPerPage` | 章数・章あたりのコマ数・1ページあたりのコマ数の上限 |

### 呼び出しごとに渡す値

| 項目 | 渡す先 | 既定 |
| --- | --- | --- |
| `Model` | `OutlineRequest` / `ChapterScriptOptions` / `GenerateOptions` / `BatchOptions` / `DesignSheetRequest` | **必須**（空は `ErrInvalidRequest`） |
| `StyleMode` | `GenerateOptions` / `BatchOptions` / `DesignSheetRequest` | 空ならプロンプト実装の既定 |
| `AspectRatio` | 同上 | 空なら `3:4`。パネル・ページ・シートで**揃えてください**。揃っていないと参照画像によるブレ抑制が黙って無効になります |
| `ImageSize` | 同上 | 空ならパネル 1K・ページ／シート 2K |

## プロンプトの注入

5操作すべてのプロンプトを `workflow.Args` で注入します。**5つとも必須**で、nil があると `workflow.New` が構築に失敗します。キットは内蔵プロンプトを持ちません。

| Args フィールド | インターフェース | 実装が受け取るデータ |
| --- | --- | --- |
| `OutlinePrompt` | `ports.OutlinePrompt` | `OutlinePromptData` |
| `ChapterScriptPrompt` | `ports.ChapterScriptPrompt` | `ChapterPromptData` |
| `DesignSheetPrompt` | `ports.DesignSheetPrompt` | `DesignSheetPromptData` |
| `PanelPrompt` | `ports.PanelPrompt` | `PanelPromptData` |
| `PagePrompt` | `ports.PagePrompt` | `PagePromptData` |

**キットがプロンプトを一切持たないのは意図的です。** 参照画像との対応順・コマ数・読み順といった構造的な指示ですら、作品によって作り込みが変わります。キットに置くとプロンプトを1文字変えるたびにキットのリリースが必要になるため、モデル名・画風指定と同じく**アプリが持つ**方針に統一しました。利用側では `internal/adapters/prompts` のような層を設けて、そこにプロンプトの組み立てをまとめてください。

`GenerateOptions.PromptOverride` は呼び出し単位で**本文だけ**を差し替えます（システム指示とネガティブプロンプトは実装のものが残ります）。

### ⚠️ 参照画像の番号

画像系の `PanelPromptData.SubjectIDs` と `PagePromptData.CharacterFile` / `PanelFile` は、**実際に添付される画像の順序**そのものです。プロンプト内で参照番号を書くときは必ずこれを使ってください。自前で数え直すと、モデルが別人の参照画像を見ながら描くことになります。

## workflow.Args の依存

| フィールド | 役割 |
| --- | --- |
| `HTTPClient` | go-http-kit の HTTP クライアント |
| `Reader` / `Writer` | `ports.ContentReader` / `remoteio.Writer`（原稿の読み込み・成果物の保存） |
| `AIClient` | 台本生成と画像生成の両方に使う `gemini.Model`（デザインシート・パネル・ページで共有します） |
| `Characters` | `*comic.Characters`（go-character-kit の `characters.json`） |

`workflow.New` が返す `*ports.Operations` に**後始末は要りません**。参照画像のキャッシュは失効を読み出し時に判定する方式で、掃除のためのバックグラウンド goroutine を持たないためです（かつては `Close() error` の呼び出しが必要でした）。
