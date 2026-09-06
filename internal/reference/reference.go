// Package reference は、参照画像の URL を生成リクエストへ添える添付へ解決します。
//
// 解決先は 2 通りです。gs:// は Vertex AI がモデル側で解決するため URI のまま渡し、
// 取得も転送も起こりません。http(s) はこのパッケージが取得し、バイト列として渡します。
//
// この経路がキット側にあるのは、取得してよい URL かの判断（SSRF 対策・許可リスト）を
// 呼び出し側の Downloader 実装が持つ一方、取得の上限（時間・バイト数）は参照画像という
// 用途に固有だからです。上限を持たない io.ReadAll は、遅い・巨大なリモートに対して
// メモリと時間を際限なく費やします。
//
// gs:// の取得口（ports.ContentReader）を受け取らないのは、gs:// を一度も読まないため
// です。以前は取得してインラインで送っていましたが、URI のまま渡せる相手に対して
// バイト列を往復させる理由がありません。
package reference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/go-comic-kit/ports"
)

const (
	// DefaultFetchTimeout は、参照画像 1 枚の取得に許す時間の既定値です。
	DefaultFetchTimeout = time.Minute
	// DefaultMaxBytes は、参照画像 1 枚のサイズ上限の既定値です。
	DefaultMaxBytes int64 = 32 << 20 // 32 MiB
	// sniffLen は MIME type の判定に使う先頭バイト数です。
	// http.DetectContentType がこの長さまでしか見ません。
	sniffLen = 512
)

var (
	// ErrDownloaderRequired は、New に Downloader が渡されなかった場合に返されます。
	ErrDownloaderRequired = errors.New("reference: downloader is required")
	// ErrTooLarge は、参照画像がサイズ上限を超えた場合に返されます。
	ErrTooLarge = errors.New("reference: image exceeds the size limit")
	// ErrNotAnImage は、取得した内容が画像として判定できなかった場合に返されます。
	//
	// 画像でないものを画像として送っても生成は失敗します。取得した側で弾くほうが、
	// 何が起きたのかを URL 付きで報告できます。
	ErrNotAnImage = errors.New("reference: fetched content is not an image")
)

// Resolver は参照画像の URL を添付へ解決します。
type Resolver struct {
	downloader ports.Downloader
	timeout    time.Duration
	maxBytes   int64
}

// New は Resolver を作ります。timeout と maxBytes は 0 以下なら既定値へ倒します。
func New(downloader ports.Downloader, timeout time.Duration, maxBytes int64) (*Resolver, error) {
	if downloader == nil {
		return nil, ErrDownloaderRequired
	}
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	return &Resolver{downloader: downloader, timeout: timeout, maxBytes: maxBytes}, nil
}

// Resolve は参照画像の URL 列を添付へ変換します。
//
// 並び順は保ちます。参照画像の並び順はモデルの解釈に影響するためです。空文字列の
// 要素はエラーではなく黙って外れます（「このキャラクターには参照画像が無い」を、
// 呼び出し側が要素の欠落として表現できるようにするため）。
//
// 取得は直列です。gs:// は取得を伴わないため待ち時間が積み上がるのは http(s) の
// 参照だけで、それは 1 リクエストにつき 1 枚の上書き指定という使われ方だからです。
// http(s) の参照を複数並べる使い方が増えたら、ここを並行化してください。
func (r *Resolver) Resolve(ctx context.Context, urls []string) ([]gemini.Attachment, error) {
	attachments := make([]gemini.Attachment, 0, len(urls))
	for _, rawURL := range urls {
		if rawURL == "" {
			continue
		}

		if isGCSURI(rawURL) {
			attachments = append(attachments, gemini.Attachment{URI: rawURL})
			continue
		}

		attachment, err := r.fetch(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}

	return attachments, nil
}

// fetch は http(s) の参照画像を取得し、内容から判定した MIME type を付けて返します。
func (r *Resolver) fetch(ctx context.Context, rawURL string) (gemini.Attachment, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	stream, err := r.downloader.GetStream(fetchCtx, rawURL)
	if err != nil {
		return gemini.Attachment{}, fmt.Errorf("参照画像の取得に失敗しました (%s): %w", rawURL, err)
	}
	defer func() { _ = stream.Close() }()

	// 上限 +1 バイトまで読み、超過したかどうかを読み切らずに判定する。
	data, err := io.ReadAll(io.LimitReader(stream, r.maxBytes+1))
	if err != nil {
		return gemini.Attachment{}, fmt.Errorf("参照画像の読み込みに失敗しました (%s): %w", rawURL, err)
	}
	if int64(len(data)) > r.maxBytes {
		return gemini.Attachment{}, fmt.Errorf("%w (%d bytes, %s)", ErrTooLarge, r.maxBytes, rawURL)
	}

	// MIME type は内容から判定します。拡張子は当てにならず、誤った申告は
	// 受け取り側の解釈を壊すためです。
	mimeType := http.DetectContentType(data[:min(len(data), sniffLen)])
	if !strings.HasPrefix(mimeType, "image/") {
		return gemini.Attachment{}, fmt.Errorf("%w (%s, %s)", ErrNotAnImage, mimeType, rawURL)
	}

	return gemini.Attachment{Data: data, MIMEType: mimeType}, nil
}

// isGCSURI は、URI が GCS を指すかを返します。スキームの大文字小文字は区別しません。
func isGCSURI(uri string) bool {
	return strings.HasPrefix(strings.ToLower(uri), "gs://")
}
