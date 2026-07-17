package runner

import (
	"google.golang.org/genai"

	"github.com/shouni/go-comic-kit/ports"
)

// outlineSchema は章立て生成（GenerateOutline）の構造化出力スキーマです。
// ResponseMIMEType "application/json" と併用することで、モデル出力がこのスキーマに
// 文法レベルで制約されます。章の ID はシステム側で採番するため含めていません。
func outlineSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"title":       {Type: genai.TypeString},
			"description": {Type: genai.TypeString},
			"chapters": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"title":          {Type: genai.TypeString},
						"summary":        {Type: genai.TypeString},
						"source_excerpt": {Type: genai.TypeString},
					},
					Required: []string{"title", "summary", "source_excerpt"},
				},
			},
		},
		Required: []string{"title", "description", "chapters"},
	}
}

// chapterScriptSchema は章単位の台本生成（GenerateChapterScript）の構造化出力スキーマです。
// パネルの ID / ChapterID / Page はシステム側で採番するため含めていません。
func chapterScriptSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"panels": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"shot":          {Type: genai.TypeString},
						"setting":       {Type: genai.TypeString},
						"visual_anchor": {Type: genai.TypeString},
						"characters": {
							Type: genai.TypeArray,
							Items: &genai.Schema{
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"character_id": {Type: genai.TypeString},
									"prominence": {
										Type: genai.TypeString,
										Enum: []string{ports.ProminencePrimary, ports.ProminenceSecondary, ports.ProminenceBackground},
									},
									"emotion":  {Type: genai.TypeString},
									"action":   {Type: genai.TypeString},
									"position": {Type: genai.TypeString},
								},
								Required: []string{"character_id", "prominence"},
							},
						},
						"dialogues": {
							Type: genai.TypeArray,
							Items: &genai.Schema{
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"speaker_id": {Type: genai.TypeString},
									"text":       {Type: genai.TypeString},
									"kind": {
										Type: genai.TypeString,
										Enum: []string{
											ports.DialogueKindSpeech,
											ports.DialogueKindThought,
											ports.DialogueKindShout,
											ports.DialogueKindNarration,
											ports.DialogueKindSFX,
										},
									},
								},
								Required: []string{"text", "kind"},
							},
						},
					},
					Required: []string{"visual_anchor", "characters", "dialogues"},
				},
			},
		},
		Required: []string{"panels"},
	}
}
