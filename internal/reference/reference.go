// Package reference resolves reference image URLs into attachments for a generation
// request.
//
// There are two outcomes. A gs:// URI is passed through as is, because Vertex AI
// resolves it model-side; nothing is fetched or transferred. An http(s) URL is fetched
// by this package and passed as bytes.
//
// This path lives in the kit because, while the decision of whether a URL may be fetched
// (SSRF protection, allowlists) belongs to the caller's Downloader implementation, the
// fetch limits (time, bytes) are specific to the use as a reference image. An unbounded
// io.ReadAll spends memory and time without limit on a slow or huge remote.
//
// It takes no gs:// reader (ports.ContentReader) because it never reads gs://. It used to
// fetch and send inline, but there is no reason to round-trip bytes to a receiver that
// accepts the URI.
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
	// DefaultFetchTimeout is the default time allowed for fetching one reference image.
	DefaultFetchTimeout = time.Minute
	// DefaultMaxBytes is the default size limit of one reference image.
	DefaultMaxBytes int64 = 32 << 20 // 32 MiB
	// sniffLen is the number of leading bytes used to detect the MIME type.
	// http.DetectContentType looks no further than this.
	sniffLen = 512
)

var (
	// ErrDownloaderRequired is returned when New is given no Downloader.
	ErrDownloaderRequired = errors.New("reference: downloader is required")
	// ErrTooLarge is returned when a reference image exceeds the size limit.
	ErrTooLarge = errors.New("reference: image exceeds the size limit")
	// ErrNotAnImage is returned when the fetched content cannot be identified as an image.
	//
	// Sending something that is not an image as one makes the generation fail anyway.
	// Rejecting it at fetch time can report what happened, with the URL.
	ErrNotAnImage = errors.New("reference: fetched content is not an image")
)

// Resolver resolves reference image URLs into attachments.
type Resolver struct {
	downloader ports.Downloader
	timeout    time.Duration
	maxBytes   int64
}

// New creates a Resolver. A timeout or maxBytes of 0 or less falls back to the default.
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

// Resolve converts a list of reference image URLs into attachments.
//
// The order is kept, because the order of reference images affects how the model reads
// them. An empty string is not an error; it is silently dropped (so a caller can express
// "this character has no reference image" as a missing element).
//
// Fetching is sequential. gs:// involves no fetch, so waiting only accumulates for
// http(s) references, and those are used as a single override per request. If using
// several http(s) references becomes common, parallelise this.
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

// fetch fetches an http(s) reference image and returns it with the MIME type detected from its content.
func (r *Resolver) fetch(ctx context.Context, rawURL string) (gemini.Attachment, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	stream, err := r.downloader.GetStream(fetchCtx, rawURL)
	if err != nil {
		return gemini.Attachment{}, fmt.Errorf("fetch reference image (%s): %w", rawURL, err)
	}
	defer func() { _ = stream.Close() }()

	// Read up to the limit + 1 byte, so exceeding it is detected without reading everything.
	data, err := io.ReadAll(io.LimitReader(stream, r.maxBytes+1))
	if err != nil {
		return gemini.Attachment{}, fmt.Errorf("read reference image (%s): %w", rawURL, err)
	}
	if int64(len(data)) > r.maxBytes {
		return gemini.Attachment{}, fmt.Errorf("%w (%d bytes, %s)", ErrTooLarge, r.maxBytes, rawURL)
	}

	// The MIME type is detected from the content. The extension is unreliable, and a
	// wrong declaration breaks how the receiver interprets the data.
	mimeType := http.DetectContentType(data[:min(len(data), sniffLen)])
	if !strings.HasPrefix(mimeType, "image/") {
		return gemini.Attachment{}, fmt.Errorf("%w (%s, %s)", ErrNotAnImage, mimeType, rawURL)
	}

	return gemini.Attachment{Data: data, MIMEType: mimeType}, nil
}

// isGCSURI reports whether the URI points to GCS. The scheme is matched case-insensitively.
func isGCSURI(uri string) bool {
	return strings.HasPrefix(strings.ToLower(uri), "gs://")
}
