package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/shouni/go-gemini-client/gemini"
	"google.golang.org/genai"

	"github.com/shouni/go-comic-kit/ports"
)

// StructuredGenerator は、構造化出力オプション付きのテキスト生成を行う依存インターフェースです。
// gemini.Generator（go-gemini-client）がこれを満たします。
type StructuredGenerator interface {
	GenerateWithParts(ctx context.Context, modelName string, parts []*genai.Part, opts gemini.GenerateOptions) (*gemini.Response, error)
}

const (
	// maxInputSize は読み込みを許可する最大テキストサイズ (5MB) です。
	maxInputSize = 5 * 1024 * 1024
	// maxErrorResponseLength はエラーログに含める応答抜粋の最大文字数です。
	maxErrorResponseLength = 200
)

// buildJSONGenerateOptions は JSON 形式の構造化データ生成に最適化されたオプションを返します。
// schema を指定すると構造化出力（constrained decoding）が有効になり、出力が文法レベルで
// スキーマに制約されます（go-gemini-client/lyria と同方式）。
func buildJSONGenerateOptions(schema *genai.Schema) gemini.GenerateOptions {
	return gemini.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
		SafetySettings:   buildSafetySettings(),
	}
}

// buildSafetySettings はパッケージ共通の安全性設定を返します。
// NOTE: 台本生成の失敗（セーフティブロックによる空応答）を抑えるため、対応カテゴリの
// ブロック閾値は BlockNone に統一しています（go-gemini-client/lyria と同方針）。
// 入力・出力の制御は呼び出し側または後段処理で行う前提です。
func buildSafetySettings() []*genai.SafetySetting {
	return []*genai.SafetySetting{
		{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
		{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
		{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
		{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
	}
}

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
	jsonStr := cleanJSONResponse(raw)
	if err := json.Unmarshal([]byte(jsonStr), out); err != nil {
		return fmt.Errorf("AI応答JSONの解析に失敗しました (抜粋: %q): %w",
			truncateString(raw, maxErrorResponseLength), err)
	}
	return nil
}

// cleanJSONResponse は LLM が出力しがちな Markdown の装飾や末尾ノイズを除去・補正します
// （go-gemini-client/lyria の実証済みロジックの移植）。json.Decoder は文字列リテラル内の
// 括弧も正しく扱いながらバランスの取れた位置で停止するため、正規表現方式と違い
// セリフ内の '}' や値の後ろに続く説明テキストに影響されません。
func cleanJSONResponse(input string) string {
	start := strings.Index(input, "{")
	if start == -1 {
		return input
	}

	// 最初の完結した JSON 値だけを取り出す
	var obj json.RawMessage
	if err := json.NewDecoder(strings.NewReader(input[start:])).Decode(&obj); err == nil {
		return string(obj)
	}

	// LLM が '}' の代わりに ')' などで閉じてしまうケースを補正する
	trimmed := strings.TrimRight(input[start:], " \t\n\r),;")
	repaired := trimmed + "}"
	if json.Valid([]byte(repaired)) {
		return repaired
	}

	return input
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
