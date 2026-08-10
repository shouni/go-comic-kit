# ⚙️ 設定と差し替え (Config / DI)

[← README](../README.md)

## ports.Config

`workflow.New(Args)` に渡す設定です。**モデル名2種と画風指定2種は必須**で、未設定なら `ports.ErrConfigInvalid` を返して構築に失敗します。モデル ID は Google 側の都合で世代交代する外部の識別子、画風指定は作品ごとに調整する文言で、どちらもキットのリリースを挟まずアプリ側で変えられるべきだからです。それ以外の項目はゼロ値で構いません（`ApplyDefaults` が補完します）。

| 設定項目 | 役割 |
| --- | --- |
| `GeminiModel` | 台本生成（章立て・章台本）に使うテキストモデル（**必須**） |
| `ImageModel` | デザインシート・パネル・ページのすべてに使う画像生成モデル（**必須**） |
| `StyleSuffix` | パネル・ページ画像の画風指定（**必須**） |
| `DesignStyleSuffix` | デザインシートの画風指定（**必須**）。`StyleSuffix` と**分離**されています。シートは同一性アンカーなので、照明・演出系の語を入れると下流の全生成に焼き付きます |
| `MaxConcurrency` | 一括生成の最大並列数（1コマ・1ページ単位の操作には影響しません） |
| `RateInterval` | AI 呼び出しの発射間隔の下限。テキストと画像で**1つのリミッターを共有**します（クォータがプロジェクト単位のため）。**スループットの上限は `MaxConcurrency` ではなく 1/RateInterval で決まります** |
| `RequestTimeout` | AI 呼び出し1回あたりの上限（参照画像のアップロードにも適用）。工程列全体の上限ではありません |
| `CacheControl` | 生成画像を保存する際の `Cache-Control`。空なら `ports.DefaultCacheControl`（`public, max-age=1800`）。非公開バケットへ書くデプロイでは `private` 等を指定してください |
| `MaxChapters` / `MaxPanelsPerChapter` / `MaxPanelsPerPage` | 章数・章あたりのコマ数・1ページあたりのコマ数の上限 |

## プロンプトの注入

5操作すべてのプロンプトを `workflow.Args` で注入します。**5つとも必須**で、nil があると `workflow.New` が構築に失敗します。キットは内蔵プロンプトを持ちません。

| Args フィールド | インターフェース | 実装が受け取るデータ |
| --- | --- | --- | --- |
| `OutlinePrompt` | `ports.OutlinePrompt` | `OutlinePromptData` |
| `ChapterScriptPrompt` | `ports.ChapterScriptPrompt` | `ChapterPromptData` |
| `DesignSheetPrompt` | `ports.DesignSheetPrompt` | `DesignSheetPromptData` |
| `PanelPrompt` | `ports.PanelPrompt` | `PanelPromptData` |
| `PagePrompt` | `ports.PagePrompt` | `PagePromptData` |

**キットがプロンプトを一切持たないのは意図的です。** 参照画像との対応順・コマ数・読み順といった構造的な指示ですら、作品によって作り込みが変わります。キットに置くとプロンプトを1文字変えるたびにキットのリリースが必要になるため、モデル名・画風指定と同じく**アプリが持つ**方針に統一しました。利用側では `internal/adapters/prompts` のような層を設けて、そこにプロンプトの組み立てをまとめてください（`PanelPromptData.SubjectIDs` と `PagePromptData.CharacterFile` / `PanelFile` が参照画像の番号の唯一の情報源です。ここがずれると、モデルは別人の参照画像を見ながら描きます）。

`GenerateOptions.PromptOverride` は呼び出し単位で**本文だけ**を差し替えます（システム指示とネガティブプロンプトは実装のものが残ります）。

### ⚠️ 参照画像の番号

画像系の `PanelPromptData.SubjectIDs` と `PagePromptData.CharacterFile` / `PanelFile` は、**実際に添付される画像の順序**そのものです。プロンプト内で参照番号を書くときは必ずこれを使ってください。自前で数え直すと、モデルが別人の参照画像を見ながら描くことになります。

## workflow.Args の依存

| フィールド | 役割 |
| --- | --- |
| `HTTPClient` | go-http-kit の HTTP クライアント |
| `Reader` / `Writer` | `ports.ContentReader` / `remoteio.Writer`（原稿の読み込み・成果物の保存） |
| `AIClient` | 台本生成と標準品質の画像生成（パネル）に使う `gemini.Model` |
| `Characters` | `*comic.Characters`（go-character-kit の `characters.json`） |

`workflow.New` が返す `*ports.Operations` は、使い終わったら **`Close()`** を呼んでください（内部 TTL キャッシュのバックグラウンド goroutine を停止します。複数回呼んでも安全で、nil レシーバでも panic しません）。
