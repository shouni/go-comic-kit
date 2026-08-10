# 📦 成果物の配置 (asset)

[← README](../README.md)

生成物の保存先は `asset` パッケージが**唯一の決定者**です。アプリ側で `fmt.Sprintf` や `path.Join` を書かず、必ずこれらを使ってください。規約が2か所に分かれると、片方の変更で既存の成果物が見つからなくなります。

| 関数 | 返すパス |
| --- | --- |
| `asset.StatePath(baseDir)` | state ドキュメント（`comic_state.json`） |
| `asset.PanelImagePath(baseDir, panelID, ext)` | パネル画像（`images/panel_{id}{ext}`） |
| `asset.PageImagePath(baseDir, page)` | ページ画像（`images/comic_page_{n}.png`） |
| `asset.DesignSheetPath(baseDir, characterIDs, jobID, ext)` | デザインシート（`character/{tag}/{jobID}{ext}`） |
| `asset.CharacterDesignPrefix(baseDir, characterID)` | 1キャラクター分のシートのディレクトリ（履歴の一覧に使います） |

`DesignSheetPath` はキャラクターID群（合成シートなら複数）を受け取るのに対し、`CharacterDesignPrefix` は**単一のキャラクターID**を受け取ります。

## ファイル名の規約

| 関数 | 役割 |
| --- | --- |
| `asset.DesignFileTag(characterIDs)` | キャラクターID群からディレクトリ名を作ります。ファイル名長の上限を超えないようルーン境界で切り詰め、CRC32 を付けて衝突を避けます |
| `asset.SanitizeFileName(name)` | ファイル名として使えない文字の除去 |
| `asset.IsStateFileName(name)` | state ドキュメントのファイル名かの判定（履歴の列挙に使います） |

`go-utils/urlpath` はこのパッケージの実装詳細で、外へは出しません。新しい成果物の種類を追加するときは、その配置関数もここに足してください。
