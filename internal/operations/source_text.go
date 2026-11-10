package operations

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/shouni/go-comic-kit/ports"
)

// maxInputSize is the maximum text size allowed to be read (5MB).
const maxInputSize = 5 * 1024 * 1024

// resolveSourceText resolves the source of an OutlineRequest (text given directly, or a URL).
func resolveSourceText(ctx context.Context, reader ports.ContentReader, sourceText, sourceURL string) (string, error) {
	if strings.TrimSpace(sourceText) != "" {
		return sourceText, nil
	}
	if strings.TrimSpace(sourceURL) == "" {
		return "", fmt.Errorf("%w: specify either SourceText or SourceURL", ports.ErrInvalidRequest)
	}
	if reader == nil {
		return "", fmt.Errorf("%w: no ContentReader is configured to read SourceURL", ports.ErrInvalidRequest)
	}
	return readContent(ctx, reader, sourceURL)
}

// readContent retrieves the content from the given source URL.
// Content over maxInputSize is truncated safely at a UTF-8 character boundary.
func readContent(ctx context.Context, reader ports.ContentReader, url string) (string, error) {
	rc, err := reader.Open(ctx, url)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "failed to close stream", "error", closeErr)
		}
	}()
	limitedReader := io.LimitReader(rc, int64(maxInputSize))
	content, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("read source: %w", err)
	}

	// Try one more read to detect truncation
	oneMoreByte := make([]byte, 1)
	n, readErr := rc.Read(oneMoreByte)
	if readErr != nil && readErr != io.EOF {
		return "", fmt.Errorf("check source size: %w", readErr)
	}

	if n > 0 {
		slog.WarnContext(ctx, "source truncated at the size limit",
			"url", url,
			"limit_bytes", maxInputSize)

		// Drop the invalid trailing bytes to align with a UTF-8 character boundary
		if !utf8.Valid(content) {
			for len(content) > 0 {
				isStart := utf8.RuneStart(content[len(content)-1])
				content = content[:len(content)-1]
				if isStart {
					break
				}
			}
		}
	}

	return string(content), nil
}
