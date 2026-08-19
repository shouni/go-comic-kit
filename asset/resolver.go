// Package asset は、生成された漫画アセット（パネル・ページ・デザインシート・状態
// ドキュメント）の配置規約を定義します。
//
// このパッケージが「成果物がどこに置かれるか」を知る唯一の場所です。パス組み立てを
// 各操作や消費側アプリに散らすと、履歴一覧・削除・再生成のどれかが規約からずれて
// 静かに壊れます。汎用のパス操作（go-remote-io/remoteio）はここで閉じ込め、外には
// 用途ごとの名前の付いた関数だけを出します。
package asset

import (
	"fmt"
	"hash/crc32"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/shouni/go-remote-io/remoteio"
)

const (
	// CharacterDesignDir はデザインシートを格納するディレクトリ名です。
	// キャラクターは作品非依存の共有アセットのため、通常はバケット直下に置かれます。
	CharacterDesignDir = "character"
	// DefaultImageDir はパネル・ページ画像を格納するディレクトリ名です。
	DefaultImageDir = "images"
	// DefaultStateJSON は MangaState（状態ドキュメント）のファイル名です。
	DefaultStateJSON = "comic_state.json"

	// panelFilePrefix はパネル画像のファイル名接頭辞です（panel_{パネルID}.ext）。
	panelFilePrefix = "panel_"
	// pageFileBaseName はページ画像のベースファイル名です（ページ番号が連番として入ります）。
	pageFileBaseName = "comic_page.png"
)

// StatePath は state ドキュメント（comic_state.json）の保存先パスを返します。
// baseDir はローカルパスでも gs:// URI でもかまいません。
func StatePath(baseDir string) (string, error) {
	return remoteio.ResolvePath(baseDir, DefaultStateJSON)
}

// IsStateFileName は、ファイル名が state ドキュメントのものかを判定します。
// GCS のオブジェクト一覧から作品を拾うときに使います。
func IsStateFileName(name string) bool {
	return name == DefaultStateJSON
}

// PanelImagePath はパネル画像の保存先パスを返します。
// パネルIDに紐づく安定したパスなので、再生成は同じ場所を上書きします。
func PanelImagePath(baseDir, panelID, extension string) (string, error) {
	fileName := panelFilePrefix + SanitizeFileName(panelID) + extension
	return remoteio.ResolvePath(baseDir, path.Join(DefaultImageDir, fileName))
}

// PageImagePath はページ画像の保存先パスを返します（images/comic_page_{page}.png）。
// page は1以上である必要があります。
func PageImagePath(baseDir string, page int) (string, error) {
	base, err := remoteio.ResolvePath(baseDir, path.Join(DefaultImageDir, pageFileBaseName))
	if err != nil {
		return "", err
	}
	return remoteio.GenerateIndexedPath(base, page)
}

// DesignSheetPath はデザインシートの保存先パスを返します
// （character/{タグ}/{jobID}.ext）。タグはキャラクターIDの組み合わせから作られます。
// 同一キャラクターへの複数回の生成を上書きせず、jobID 別に履歴として残すための構成です。
func DesignSheetPath(baseDir string, characterIDs []string, jobID, extension string) (string, error) {
	relative := path.Join(CharacterDesignDir, DesignFileTag(characterIDs), SanitizeFileName(jobID)+extension)
	return remoteio.ResolvePath(baseDir, relative)
}

// CharacterDesignPrefix は、あるキャラクターのデザインシートが並ぶディレクトリの URI を
// 末尾スラッシュ付きで返します。消費側が生成履歴を一覧・削除するときの前方一致キーです。
func CharacterDesignPrefix(baseDir, characterID string) (string, error) {
	resolved, err := remoteio.ResolvePath(baseDir, path.Join(CharacterDesignDir, SanitizeFileName(characterID)))
	if err != nil {
		return "", err
	}
	return resolved + "/", nil
}

// maxDesignFileTagBytes はファイル名に埋め込むキャラクタータグの最大バイト長です。
// ファイルシステムのファイル名長制限（一般に255バイト）に、ディレクトリや接頭辞・拡張子を
// 加えても収まる余裕を持たせた値です。
const maxDesignFileTagBytes = 100

// DesignFileTag はキャラクターID群からデザインシート用のディレクトリ名を作ります。
// ID が多い・長い場合でもファイル名長制限に抵触しないよう、上限を超えたら rune 境界で
// 切り詰め、組み合わせの一意性はチェックサムで担保します。
func DesignFileTag(characterIDs []string) string {
	tag := SanitizeFileName(strings.Join(characterIDs, "_"))
	if len(tag) <= maxDesignFileTagBytes {
		return tag
	}

	sum := crc32.ChecksumIEEE([]byte(tag))
	cut := tag[:maxDesignFileTagBytes]
	// バイト位置での切り詰めがマルチバイト文字を分断した場合は末尾を除去して修復する
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return fmt.Sprintf("%s_%08x", cut, sum)
}

// fileNameSanitizer はファイル名として使用できない文字を置換します。
var fileNameSanitizer = strings.NewReplacer(
	"/", "_",
	`\`, "_",
	":", "_",
	"*", "_",
	"?", "_",
	`"`, "_",
	"<", "_",
	">", "_",
	"|", "_",
)

// SanitizeFileName はファイル名として使えない文字を "_" に置き換えます。
func SanitizeFileName(name string) string {
	return fileNameSanitizer.Replace(name)
}
