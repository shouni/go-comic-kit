// Package asset は、生成された漫画アセット（画像・状態ドキュメント）のファイル名規約と
// 出力パス解決を提供します。
package asset

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shouni/go-utils/urlpath"
)

const (
	// CharacterDesignDir は生成されたキャラクター画像を格納するディレクトリ名です。
	CharacterDesignDir = "character"
	// DefaultImageDir は生成された画像を格納するデフォルトのディレクトリ名です。
	DefaultImageDir = "images"
	// DefaultStateJSON は MangaState（状態ドキュメント）のデフォルト JSON ファイル名です。
	DefaultStateJSON = "comic_state.json"
	// DefaultPanelFileName はパネル画像の共通のベースファイル名です。
	DefaultPanelFileName = "panel.png"
	// DefaultPageFileName はページ画像の共通のベースファイル名です。
	DefaultPageFileName = "comic_page.png"
)

var (
	// PanelFileRegex はパネル画像 (panel_1.png 等) に一致します
	PanelFileRegex = createIndexedRegex(DefaultPanelFileName)
	// PageFileRegex はページ画像 (comic_page_1.png 等) に一致します
	PageFileRegex = createIndexedRegex(DefaultPageFileName)
)

// DefaultPanelImagePath は、パネル画像を格納するデフォルトの相対パスを返します。
func DefaultPanelImagePath() string {
	return path.Join(DefaultImageDir, DefaultPanelFileName)
}

// DefaultPageImagePath は、ページ画像を格納するデフォルトの相対パスを返します。
func DefaultPageImagePath() string {
	return path.Join(DefaultImageDir, DefaultPageFileName)
}

// ResolveOutputPath は、ベースとなるディレクトリパスとファイル名から、
// GCS/ローカルを考慮した最終的な出力パスを生成します。
func ResolveOutputPath(baseDir, fileName string) (string, error) {
	return urlpath.ResolvePath(baseDir, fileName)
}

// ResolveBaseURL は、入力パス（URLまたはローカルパス）から
// 親ディレクトリのパスを解決し、末尾がセパレータで終わるように正規化します。
func ResolveBaseURL(rawPath string) string {
	return urlpath.ResolveBaseDir(rawPath)
}

// GenerateIndexedPath は、指定されたベースパスの拡張子の前に連番を挿入し、
// 新しいパス文字列を生成します。index は1以上の整数である必要があります。
// 例: "path/to/image.png", 1 -> "path/to/image_1.png"
func GenerateIndexedPath(basePath string, index int) (string, error) {
	return urlpath.GenerateIndexedPath(basePath, index)
}

// createIndexedRegex は、ファイル名に基づきインデックス付きファイル用の正規表現を生成します。
// 例: "panel.png" -> ^panel_\d+\.png$
func createIndexedRegex(fileName string) *regexp.Regexp {
	ext := filepath.Ext(fileName)
	baseName := strings.TrimSuffix(fileName, ext)

	// baseName と ext の両方を QuoteMeta でエスケープすることで
	// ドットや特殊文字が含まれていても正しくリテラルとしてマッチします。
	pattern := fmt.Sprintf(`^%s_\d+%s$`, regexp.QuoteMeta(baseName), regexp.QuoteMeta(ext))
	return regexp.MustCompile(pattern)
}
