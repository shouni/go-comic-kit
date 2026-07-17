package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/shouni/go-comic-kit/ports"
)

const (
	// maxInputSize は読み込みを許可する最大テキストサイズ (5MB) です。
	maxInputSize = 5 * 1024 * 1024
	// maxErrorResponseLength はエラーログに含める応答抜粋の最大文字数です。
	maxErrorResponseLength = 200
)

// jsonBlockRegex は、Markdown 形式の JSON ブロックを抽出するための正規表現です。
var jsonBlockRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(.*\\S)\\s*```")

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

// parseJSONResponse は AI の応答から JSON を抽出し、out にデコードします。
func parseJSONResponse(raw string, out any) error {
	jsonStr := extractJSONString(raw)
	if jsonStr == "" {
		slog.Warn("AIの応答からJSONを抽出できませんでした。応答全体を対象にパースを試みます。",
			"response_snippet", truncateString(raw, 100))
		jsonStr = raw
	}

	if err := json.Unmarshal([]byte(jsonStr), out); err != nil {
		return fmt.Errorf("AI応答JSONの解析に失敗しました (抜粋: %q): %w",
			truncateString(raw, maxErrorResponseLength), err)
	}
	return nil
}

// extractJSONString は文字列から JSON 部分を抽出します。
func extractJSONString(raw string) string {
	cleanRaw := strings.TrimSpace(raw)

	if matches := jsonBlockRegex.FindStringSubmatch(cleanRaw); len(matches) > 1 {
		return matches[1]
	}

	first := strings.Index(cleanRaw, "{")
	last := strings.LastIndex(cleanRaw, "}")
	if first != -1 && last != -1 && last > first {
		return cleanRaw[first : last+1]
	}

	return ""
}

// truncateString は指定された長さで文字列を安全に切り捨てます。
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// characterRoster はプロンプトに注入するキャラクター一覧（箇条書き）を構築します。
func characterRoster(characters *ports.Characters) string {
	if characters == nil || len(characters.List) == 0 {
		return "（キャラクター定義なし）"
	}
	var sb strings.Builder
	for i := range characters.List {
		char := &characters.List[i]
		fmt.Fprintf(&sb, "- id: %s / 名前: %s", char.ID, char.Name)
		if len(char.VisualCues) > 0 {
			fmt.Fprintf(&sb, " / 特徴: %s", strings.Join(char.VisualCues, ", "))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
