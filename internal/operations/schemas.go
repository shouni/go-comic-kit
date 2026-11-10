package operations

import (
	"github.com/shouni/go-comic-kit/comic"
)

// outlineSchema is the structured output schema of outline generation (GenerateOutline).
// Used together with ResponseMIMEType "application/json", it constrains the model output
// to this schema at the grammar level. Chapter IDs are assigned by the system, so they
// are not included.
//
// It returns plain JSON Schema. Built with genai.Schema, code that merely writes a schema
// would be tied to the SDK's types, undercutting the point of going through genai-kit.
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

// chapterScriptSchema is the structured output schema of per-chapter script generation
// (GenerateChapterScript). A panel's ID / ChapterID / Page are assigned by the system, so
// they are not included.
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
										"enum": []string{comic.ProminencePrimary, comic.ProminenceSecondary, comic.ProminenceBackground},
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
											comic.DialogueKindSpeech,
											comic.DialogueKindThought,
											comic.DialogueKindShout,
											comic.DialogueKindNarration,
											comic.DialogueKindSFX,
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
