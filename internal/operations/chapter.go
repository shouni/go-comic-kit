package operations

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/shouni/go-comic-kit/ports"
)

// ChapterScriptRunner は章単位の台本生成（GenerateChapterScript 操作）を実行します。
type ChapterScriptRunner struct {
	prompt           ports.ChapterScriptPrompt
	aiClient         StructuredGenerator
	characters       *ports.Characters
	model            string
	maxPanels        int
	maxPanelsPerPage int
}

var _ ports.ChapterScriptGenerator = (*ChapterScriptRunner)(nil)

// NewChapterScriptRunner は依存関係を注入して初期化します。
// maxPanels / maxPanelsPerPage が 0 以下の場合は ports の既定値を使います。
func NewChapterScriptRunner(
	prompt ports.ChapterScriptPrompt,
	aiClient StructuredGenerator,
	characters *ports.Characters,
	model string,
	maxPanels int,
	maxPanelsPerPage int,
) *ChapterScriptRunner {
	if maxPanels <= 0 {
		maxPanels = ports.DefaultMaxPanelsPerChapter
	}
	if maxPanelsPerPage <= 0 {
		maxPanelsPerPage = ports.DefaultMaxPanelsPerPage
	}
	return &ChapterScriptRunner{
		prompt:           prompt,
		aiClient:         aiClient,
		characters:       characters,
		model:            model,
		maxPanels:        maxPanels,
		maxPanelsPerPage: maxPanelsPerPage,
	}
}

// chapterScriptResponse は章台本生成の AI 応答のスキーマです。
// Panel の ID / ChapterID / Page はシステム側で採番するため、AI には出力させません。
type chapterScriptResponse struct {
	Panels []struct {
		Shot         string                 `json:"shot"`
		Setting      string                 `json:"setting"`
		VisualAnchor string                 `json:"visual_anchor"`
		Characters   []ports.PanelCharacter `json:"characters"`
		Dialogues    []ports.DialogueLine   `json:"dialogues"`
	} `json:"panels"`
}

// GenerateChapterScript は章立て全体を文脈として指定章のパネル群を生成し、
// 既存の同章パネルを置き換えて state を返します（冪等）。
func (r *ChapterScriptRunner) GenerateChapterScript(ctx context.Context, state *ports.MangaState, chapterID string) (*ports.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("state が nil です（先に GenerateOutline を実行してください）")
	}
	chapter := state.ChapterByID(chapterID)
	if chapter == nil {
		return nil, fmt.Errorf("章 %q が見つかりません", chapterID)
	}

	// 1. プロンプト構築（章立て全体を文脈として渡す）
	data := &ports.ChapterPromptData{
		WorkTitle:       state.Title,
		WorkDescription: state.Description,
		OutlineDigest:   outlineDigest(state.Chapters, chapterID),
		Chapter:         *chapter,
		CharacterRoster: characterRoster(r.characters),
		MaxPanels:       r.maxPanels,
	}
	finalPrompt, err := r.prompt.BuildChapterScript(state.ScriptMode, data)
	if err != nil {
		return nil, fmt.Errorf("章台本プロンプトの構築に失敗しました: %w", err)
	}

	// 2. 生成（構造化出力: スキーマで文法レベルに制約する）
	slog.Info("ChapterScriptRunner: Gemini APIを呼び出し中",
		"model", r.model, "chapter", chapterID)
	parts := []*genai.Part{{Text: finalPrompt}}
	resp, err := r.aiClient.GenerateWithParts(ctx, r.model, parts, buildJSONGenerateOptions(chapterScriptSchema()))
	if err != nil {
		return nil, fmt.Errorf("章 %q の台本生成に失敗しました: %w", chapterID, err)
	}

	// 3. パースと正規化
	var parsed chapterScriptResponse
	if err := parseJSONResponse(resp.Text, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Panels) == 0 {
		return nil, fmt.Errorf("章 %q のパネルが空です（AI応答に panels がありません）", chapterID)
	}
	if len(parsed.Panels) > r.maxPanels {
		slog.Warn("パネル数が上限を超えたため切り詰めます",
			"chapter", chapterID, "got", len(parsed.Panels), "max", r.maxPanels)
		parsed.Panels = parsed.Panels[:r.maxPanels]
	}

	panels := make([]ports.Panel, 0, len(parsed.Panels))
	for i, p := range parsed.Panels {
		panels = append(panels, ports.Panel{
			ID:           fmt.Sprintf("%s-p%02d", chapterID, i+1),
			ChapterID:    chapterID,
			Shot:         p.Shot,
			Setting:      p.Setting,
			VisualAnchor: p.VisualAnchor,
			Characters:   r.normalizeCharacters(chapterID, p.Characters),
			Dialogues:    p.Dialogues,
		})
	}

	// 4. state へ反映（同章の既存パネルを置き換え、ページ番号を振り直す）
	state.ReplaceChapterPanels(chapterID, panels)
	state.Repaginate(r.maxPanelsPerPage)
	state.UpdatedAt = time.Now().UTC()

	slog.Info("ChapterScriptRunner: 章台本を生成しました",
		"chapter", chapterID, "panels", len(panels))
	return state, nil
}

// normalizeCharacters は AI 出力の登場キャラクターを検証・正規化します。
// characters.json に存在しない ID は、参照解決時にデフォルトキャラクターへ暗黙に
// フォールバックして別人が描かれる事故を防ぐため、background（参照なしのモブ）に降格します。
func (r *ChapterScriptRunner) normalizeCharacters(chapterID string, chars []ports.PanelCharacter) []ports.PanelCharacter {
	result := make([]ports.PanelCharacter, 0, len(chars))
	for _, pc := range chars {
		if strings.TrimSpace(pc.CharacterID) == "" {
			continue
		}
		if pc.Prominence != ports.ProminenceBackground && !r.knownCharacter(pc.CharacterID) {
			slog.Warn("未定義のキャラクターIDをbackgroundに降格します",
				"chapter", chapterID, "character_id", pc.CharacterID)
			pc.Prominence = ports.ProminenceBackground
		}
		result = append(result, pc)
	}
	return result
}

func (r *ChapterScriptRunner) knownCharacter(id string) bool {
	if r.characters == nil {
		return true // キャラクター定義が無い構成では検証をスキップ
	}
	return r.characters.GetCharacter(id) != nil
}

// outlineDigest は章立て全体の一覧（文脈用）を構築します。対象章には印を付けます。
func outlineDigest(chapters []ports.Chapter, targetID string) string {
	var sb strings.Builder
	for _, ch := range chapters {
		marker := " "
		if ch.ID == targetID {
			marker = "▶"
		}
		fmt.Fprintf(&sb, "%s %s: %s — %s\n", marker, ch.ID, ch.Title, ch.Summary)
	}
	return strings.TrimRight(sb.String(), "\n")
}
