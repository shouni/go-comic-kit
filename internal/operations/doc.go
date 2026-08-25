// Package operations は、go-comic-kit の各操作（章立て・章台本・デザインシート・
// パネル画像・ページ合成）の実行ロジックを提供します。すべての操作は MangaState を
// 受け取り、更新済みの MangaState を返す冪等な契約（ports パッケージ参照）に従います。
package operations
