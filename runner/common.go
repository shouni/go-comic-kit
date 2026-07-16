// Package runner は、go-comic-kit の各操作（デザインシート・台本・パネル/ページ画像・
// パブリッシュ）の実行ロジックを提供します。すべての操作は MangaState を受け取り、
// 更新済みの MangaState を返す冪等な契約（ports パッケージ参照）に従います。
package runner

import "strings"

// defaultCacheControl は生成物を保存する際の既定の Cache-Control です。
const defaultCacheControl = "public, max-age=1800"

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

// getPreferredExtension は MimeType に対応するファイル拡張子を返します。
func getPreferredExtension(mimeType string) string {
	preferred := map[string]string{"image/png": ".png", "image/jpeg": ".jpg"}
	if ext, ok := preferred[mimeType]; ok {
		return ext
	}
	return ".png"
}

// ptrInt64 は 0 を nil として扱う int64 ポインタ変換です。
func ptrInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
