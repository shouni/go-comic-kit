package ports

import (
	"context"
	"io"
)

// ContentReader は、指定されたURIからコンテンツを取得するためのインターフェースです。
type ContentReader interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

// Downloader は、URL からデータストリームを取得するためのインターフェースです。
//
// go-http-kit の httpkit.Streamer を直接受け取らずここで宣言しているのは、口が
// stdlib の型だけで閉じており、それだけのために依存辺を 1 本増やす必要がないためです。
// *httpkit.Client はそのまま満たします。
//
// 取得先の検証（SSRF 対策・ドメイン許可リスト）は実装側の責務です。参照画像の URL は
// 呼び出し側の入力がそのまま渡るため、キットは取りに行ってよいかを判断しません。
type Downloader interface {
	// GetStream は指定された URL からデータストリームを取得します。
	// 呼び出し元は、使用後に必ず戻り値の io.ReadCloser を Close() する責任があります。
	GetStream(ctx context.Context, url string) (io.ReadCloser, error)
}
