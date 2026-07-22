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

// maxInputSize は読み込みを許可する最大テキストサイズ (5MB) です。
const maxInputSize = 5 * 1024 * 1024

// resolveSourceText は OutlineRequest のソース指定（テキスト直接 or URL）を解決します。
func resolveSourceText(ctx context.Context, reader ports.ContentReader, sourceText, sourceURL string) (string, error) {
	if strings.TrimSpace(sourceText) != "" {
		return sourceText, nil
	}
	if strings.TrimSpace(sourceURL) == "" {
		return "", fmt.Errorf("SourceText または SourceURL のいずれかを指定してください")
	}
	if reader == nil {
		return "", fmt.Errorf("SourceURL を読み込むための ContentReader が設定されていません")
	}
	return readContent(ctx, reader, sourceURL)
}

// readContent は、指定されたソースURLからコンテンツを取得します。
// maxInputSize を超える場合は UTF-8 の文字境界で安全に切り捨てます。
func readContent(ctx context.Context, reader ports.ContentReader, url string) (string, error) {
	rc, err := reader.Open(ctx, url)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "ストリームのクローズに失敗しました", "error", closeErr)
		}
	}()
	limitedReader := io.LimitReader(rc, int64(maxInputSize))
	content, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("読み込みに失敗しました: %w", err)
	}

	// 追加の読み込みを試みて切り捨てを判定
	oneMoreByte := make([]byte, 1)
	n, readErr := rc.Read(oneMoreByte)
	if readErr != nil && readErr != io.EOF {
		return "", fmt.Errorf("サイズ確認中にエラーが発生しました: %w", readErr)
	}

	if n > 0 {
		slog.WarnContext(ctx, "制限サイズに達したため切り捨てられました",
			"url", url,
			"limit_bytes", maxInputSize)

		// UTF-8の文字境界に合わせて末尾の不正なバイトを取り除く
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
