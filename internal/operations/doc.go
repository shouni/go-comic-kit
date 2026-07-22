// Package operations は、go-comic-kit の各操作（デザインシート・台本・パネル/ページ画像・
// パブリッシュ）の実行ロジックを提供します。すべての操作は MangaState を受け取り、
// 更新済みの MangaState を返す冪等な契約（ports パッケージ参照）に従います。
package operations
