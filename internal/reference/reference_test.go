package reference

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeDownloader は、URL に関わらず固定の内容を返す ports.Downloader です。
// 呼ばれた URL を記録するので、gs:// が取得へ回っていないことも確認できます。
type fakeDownloader struct {
	body []byte
	err  error
	got  []string
}

func (d *fakeDownloader) GetStream(_ context.Context, url string) (io.ReadCloser, error) {
	d.got = append(d.got, url)
	if d.err != nil {
		return nil, d.err
	}
	return io.NopCloser(bytes.NewReader(d.body)), nil
}

// pngBytes は、MIME type 判定が通る最小の PNG を返します。
func pngBytes(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buf.Bytes()
}

func TestNewRequiresDownloader(t *testing.T) {
	t.Parallel()

	if _, err := New(nil, 0, 0); !errors.Is(err, ErrDownloaderRequired) {
		t.Errorf("New(nil) error = %v, want %v", err, ErrDownloaderRequired)
	}
}

// TestResolveKeepsGCSURIWithoutFetching は、gs:// が取得を経ずに URI のまま渡ることを
// 検証します。Vertex AI がモデル側で解決するため、バイト列を往復させる理由がありません。
func TestResolveKeepsGCSURIWithoutFetching(t *testing.T) {
	t.Parallel()

	downloader := &fakeDownloader{body: pngBytes(t)}
	resolver, err := New(downloader, 0, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := resolver.Resolve(t.Context(), []string{"gs://bucket/a.png", "GS://bucket/b.png"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for i, want := range []string{"gs://bucket/a.png", "GS://bucket/b.png"} {
		if got[i].URI != want || len(got[i].Data) != 0 {
			t.Errorf("got[%d] = %+v, want URI %q とバイト列なし", i, got[i], want)
		}
	}
	if len(downloader.got) != 0 {
		t.Errorf("取得が走っています: %v", downloader.got)
	}
}

// TestResolveFetchesHTTPAndDetectsMIMEType は、http(s) を取得してバイト列で渡し、
// MIME type を内容から判定することを検証します。拡張子は当てにならず、誤った申告は
// 受け取り側の解釈を壊します。
func TestResolveFetchesHTTPAndDetectsMIMEType(t *testing.T) {
	t.Parallel()

	data := pngBytes(t)
	downloader := &fakeDownloader{body: data}
	resolver, err := New(downloader, 0, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// 拡張子は .jpg だが中身は PNG。内容側が勝つこと。
	got, err := resolver.Resolve(t.Context(), []string{"https://example.com/a.jpg"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png（拡張子ではなく内容で判定すること）", got[0].MIMEType)
	}
	if !bytes.Equal(got[0].Data, data) || got[0].URI != "" {
		t.Errorf("got = %+v, want バイト列のみ", got[0])
	}
}

// TestResolveKeepsOrderAndDropsEmpty は、並び順が保たれ、空文字列が黙って外れることを
// 検証します。並び順はモデルの解釈に影響し、空要素は「参照画像が無い」の表現です。
func TestResolveKeepsOrderAndDropsEmpty(t *testing.T) {
	t.Parallel()

	downloader := &fakeDownloader{body: pngBytes(t)}
	resolver, err := New(downloader, 0, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := resolver.Resolve(t.Context(), []string{
		"gs://bucket/a.png",
		"",
		"https://example.com/b.png",
		"gs://bucket/c.png",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3（空要素が落ちること）", len(got))
	}
	if got[0].URI != "gs://bucket/a.png" || len(got[1].Data) == 0 || got[2].URI != "gs://bucket/c.png" {
		t.Errorf("並び順が保たれていません: %+v", got)
	}
}

// TestResolveRejectsOversizedReference は、サイズ上限を超える参照を弾くことを検証します。
// 上限なしの読み込みは、遅い・巨大なリモートにメモリと時間を際限なく費やします。
func TestResolveRejectsOversizedReference(t *testing.T) {
	t.Parallel()

	downloader := &fakeDownloader{body: bytes.Repeat([]byte("x"), 100)}
	resolver, err := New(downloader, 0, 10)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = resolver.Resolve(t.Context(), []string{"https://example.com/big.png"})

	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Resolve() error = %v, want %v", err, ErrTooLarge)
	}
}

// TestResolveRejectsNonImage は、画像でない内容を取得段階で弾くことを検証します。
// そのまま送っても生成が失敗するだけで、どの URL が原因かは分からなくなります。
func TestResolveRejectsNonImage(t *testing.T) {
	t.Parallel()

	downloader := &fakeDownloader{body: []byte("<html>not an image</html>")}
	resolver, err := New(downloader, 0, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = resolver.Resolve(t.Context(), []string{"https://example.com/a.png"})

	if !errors.Is(err, ErrNotAnImage) {
		t.Errorf("Resolve() error = %v, want %v", err, ErrNotAnImage)
	}
}

// TestResolvePropagatesFetchError は、取得の失敗が URL 付きで伝わることを検証します。
func TestResolvePropagatesFetchError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	downloader := &fakeDownloader{err: sentinel}
	resolver, err := New(downloader, 0, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = resolver.Resolve(t.Context(), []string{"https://example.com/a.png"})

	if !errors.Is(err, sentinel) {
		t.Fatalf("Resolve() error = %v, want %v をラップしたもの", err, sentinel)
	}
	if !strings.Contains(err.Error(), "https://example.com/a.png") {
		t.Errorf("error = %v, want どの URL で失敗したかを含むこと", err)
	}
}

// TestNewFallsBackToDefaults は、0 以下の指定が既定値へ倒れることを検証します。
// 上限が 0 のまま使われると、1 バイトも読めないか無制限になります。
func TestNewFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	got, err := New(&fakeDownloader{}, 0, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if got.timeout != DefaultFetchTimeout {
		t.Errorf("timeout = %v, want %v", got.timeout, DefaultFetchTimeout)
	}
	if got.maxBytes != DefaultMaxBytes {
		t.Errorf("maxBytes = %d, want %d", got.maxBytes, DefaultMaxBytes)
	}

	explicit, err := New(&fakeDownloader{}, 5*time.Second, 1024)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if explicit.timeout != 5*time.Second || explicit.maxBytes != 1024 {
		t.Errorf("指定値が反映されていません: %+v", explicit)
	}
}
