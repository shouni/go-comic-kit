package ports

import (
	"context"
	"io"
)

// ContentReader is the interface for retrieving content from a URI.
type ContentReader interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

// Downloader is the interface for retrieving a data stream from a URL.
//
// It is declared here instead of taking go-http-kit's httpkit.Streamer because its
// signature uses stdlib types only, and that alone does not justify one more dependency
// edge. *httpkit.Client satisfies it as is.
//
// Validating the destination (SSRF protection, domain allowlists) is the
// implementation's responsibility. Reference image URLs are the caller's input passed
// through as is, so the kit does not decide whether they may be fetched.
type Downloader interface {
	// GetStream retrieves a data stream from the given URL.
	// The caller is responsible for calling Close() on the returned io.ReadCloser after use.
	GetStream(ctx context.Context, url string) (io.ReadCloser, error)
}
