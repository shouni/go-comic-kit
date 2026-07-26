package operations

import (
	"github.com/shouni/go-comic-kit/ports"
)

// outlineSchema は章立て生成（GenerateOutline）の構造化出力スキーマです。
// ResponseMIMEType "application/json" と併用することで、モデル出力がこのスキーマに
// 文法レベルで制約されます。章の ID はシステム側で採番するため含めていません。
//
// 素の JSON Schema を返します。genai.Schema で組み立てると、スキーマを書くだけの
// コードが SDK の型に縛られ、go-gemini-client を挟んでいる意味が薄れます。
func outlineSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"chapters": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":          map[string]any{"type": "string"},
						"summary":        map[string]any{"type": "string"},
						"source_excerpt": map[string]any{"type": "string"},
					},
					"required": []string{"title", "summary", "source_excerpt"},
				},
			},
		},
		"required": []string{"title", "description", "chapters"},
	}
}

// chapterScriptSchema は章単位の台本生成（GenerateChapterScript）の構造化出力スキーマです。
// パネルの ID / ChapterID / Page はシステム側で採番するため含めていません。
func chapterScriptSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"panels": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"shot":          map[string]any{"type": "string"},
						"setting":       map[string]any{"type": "string"},
						"visual_anchor": map[string]any{"type": "string"},
						"characters": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"character_id": map[string]any{"type": "string"},
									"prominence": map[string]any{
										"type": "string",
										"enum": []string{ports.ProminencePrimary, ports.ProminenceSecondary, ports.ProminenceBackground},
									},
									"emotion":  map[string]any{"type": "string"},
									"action":   map[string]any{"type": "string"},
									"position": map[string]any{"type": "string"},
								},
								"required": []string{"character_id", "prominence"},
							},
						},
						"dialogues": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"speaker_id": map[string]any{"type": "string"},
									"text":       map[string]any{"type": "string"},
									"kind": map[string]any{
										"type": "string",
										"enum": []string{
											ports.DialogueKindSpeech,
											ports.DialogueKindThought,
											ports.DialogueKindShout,
											ports.DialogueKindNarration,
											ports.DialogueKindSFX,
										},
									},
								},
								"required": []string{"text", "kind"},
							},
						},
					},
					"required": []string{"visual_anchor", "characters", "dialogues"},
				},
			},
		},
		"required": []string{"panels"},
	}
}
