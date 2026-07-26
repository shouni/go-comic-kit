package operations

import (
	"github.com/shouni/go-gemini-client/gemini"
)

// StructuredGenerator は、構造化出力オプション付きのテキスト生成を行う依存インターフェースです。
//
// gemini.MultimodalGenerator の別名です。この kit が渡すのはテキストプロンプトだけなので、
// genai.Part を組み立てる Generator ではなく、SDK の型を含まないこちらに依存します。
// 独自に宣言し直すと、同じ 1 メソッドのインターフェースが 2 か所に増えます。
type StructuredGenerator = gemini.MultimodalGenerator

// buildJSONGenerateOptions は、schema による構造化出力（constrained decoding）と、
// セーフティブロックによる空応答を防ぐための BlockNone 統一設定を適用した
// JSON 生成オプションを返します（go-gemini-client/lyria と同方式）。
//
// schema は素の JSON Schema です。genai.Schema で組み立てると、スキーマを書くだけの
// コードが SDK の型に縛られ、go-gemini-client を挟んでいる意味が薄れます。
func buildJSONGenerateOptions(schema map[string]any) gemini.GenerateOptions {
	return gemini.GenerateOptions{
		ResponseMIMEType:   "application/json",
		ResponseJSONSchema: schema,
		SafetySettings:     gemini.NewSafetySettings(gemini.SafetyBlockNone),
	}
}
