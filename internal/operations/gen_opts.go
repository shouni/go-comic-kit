package operations

import (
	"context"

	"github.com/shouni/go-gemini-client/gemini"
	"google.golang.org/genai"
)

// StructuredGenerator は、構造化出力オプション付きのテキスト生成を行う依存インターフェースです。
// gemini.Generator（go-gemini-client）がこれを満たします。
type StructuredGenerator interface {
	GenerateWithParts(ctx context.Context, modelName string, parts []*genai.Part, opts gemini.GenerateOptions) (*gemini.Response, error)
}

// buildJSONGenerateOptions は、schema による構造化出力（constrained decoding）と、
// セーフティブロックによる空応答を防ぐための BlockNone 統一設定を適用した
// JSON 生成オプションを返します（go-gemini-client/lyria と同方式）。
func buildJSONGenerateOptions(schema *genai.Schema) gemini.GenerateOptions {
	return gemini.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
		SafetySettings:   gemini.NewSafetySettings(genai.HarmBlockThresholdBlockNone),
	}
}
