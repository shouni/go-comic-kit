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

// fakeDownloader is a ports.Downloader that returns fixed content regardless of the URL.
// It records the URLs it was called with, so it also shows that gs:// was never fetched.
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

// pngBytes returns the smallest PNG that passes MIME type detection.
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

// TestResolveKeepsGCSURIWithoutFetching verifies that gs:// is passed as a URI without
// being fetched. Vertex AI resolves it model-side, so there is no reason to round-trip bytes.
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
			t.Errorf("got[%d] = %+v, want URI %q and no bytes", i, got[i], want)
		}
	}
	if len(downloader.got) != 0 {
		t.Errorf("a fetch ran: %v", downloader.got)
	}
}

// TestResolveFetchesHTTPAndDetectsMIMEType verifies that http(s) is fetched and passed as
// bytes, with the MIME type detected from the content. The extension is unreliable, and a
// wrong declaration breaks how the receiver interprets the data.
func TestResolveFetchesHTTPAndDetectsMIMEType(t *testing.T) {
	t.Parallel()

	data := pngBytes(t)
	downloader := &fakeDownloader{body: data}
	resolver, err := New(downloader, 0, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// The extension is .jpg but the content is PNG. The content must win.
	got, err := resolver.Resolve(t.Context(), []string{"https://example.com/a.jpg"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png (detected from content, not extension)", got[0].MIMEType)
	}
	if !bytes.Equal(got[0].Data, data) || got[0].URI != "" {
		t.Errorf("got = %+v, want bytes only", got[0])
	}
}

// TestResolveKeepsOrderAndDropsEmpty verifies that the order is kept and empty strings are
// silently dropped. The order affects how the model reads the images, and an empty
// element expresses "no reference image".
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
		t.Fatalf("len = %d, want 3 (the empty element must be dropped)", len(got))
	}
	if got[0].URI != "gs://bucket/a.png" || len(got[1].Data) == 0 || got[2].URI != "gs://bucket/c.png" {
		t.Errorf("order not preserved: %+v", got)
	}
}

// TestResolveRejectsOversizedReference verifies that a reference over the size limit is
// rejected. An unbounded read spends memory and time without limit on a slow or huge remote.
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

// TestResolveRejectsNonImage verifies that non-image content is rejected at fetch time.
// Sending it on would just fail the generation, with no trace of which URL caused it.
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

// TestResolvePropagatesFetchError verifies that a fetch failure is reported with its URL.
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
		t.Fatalf("Resolve() error = %v, want one wrapping %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "https://example.com/a.png") {
		t.Errorf("error = %v, want it to include the URL that failed", err)
	}
}

// TestNewFallsBackToDefaults verifies that values of 0 or less fall back to the defaults.
// A limit left at 0 would either read nothing at all or be unlimited.
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
		t.Errorf("explicit values not applied: %+v", explicit)
	}
}
