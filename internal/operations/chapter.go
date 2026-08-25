package operations

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-comic-kit/ports"
)

// ChapterScriptRunner は章単位の台本生成（GenerateChapterScript 操作）を実行します。
type ChapterScriptRunner struct {
	prompt           ports.ChapterScriptPrompt
	aiClient         StructuredGenerator
	characters       *comic.Characters
	maxPanels        int
	maxPanelsPerPage int
}

var _ ports.ChapterScriptGenerator = (*ChapterScriptRunner)(nil)

// NewChapterScriptRunner は依存関係を注入して初期化します。0 以下の場合は
// ports.DefaultMaxPanelsPerChapter / comic.DefaultMaxPanelsPerPage を使います。
func NewChapterScriptRunner(
	prompt ports.ChapterScriptPrompt,
	aiClient StructuredGenerator,
	characters *comic.Characters,
	maxPanels int,
	maxPanelsPerPage int,
) *ChapterScriptRunner {
	if maxPanels <= 0 {
		maxPanels = ports.DefaultMaxPanelsPerChapter
	}
	if maxPanelsPerPage <= 0 {
		maxPanelsPerPage = comic.DefaultMaxPanelsPerPage
	}
	return &ChapterScriptRunner{
		prompt:           prompt,
		aiClient:         aiClient,
		characters:       characters,
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
		Characters   []comic.PanelCharacter `json:"characters"`
		Dialogues    []comic.DialogueLine   `json:"dialogues"`
	} `json:"panels"`
}

// GenerateChapterScript は章立て全体を文脈として指定章のパネル群を生成し、
// 既存の同章パネルを置き換えて state を返します（冪等）。
func (r *ChapterScriptRunner) GenerateChapterScript(ctx context.Context, state *comic.MangaState, chapterID string, opts ports.ChapterScriptOptions) (*comic.MangaState, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: state が nil です（先に GenerateOutline を実行してください）", ports.ErrInvalidRequest)
	}
	chapter := state.ChapterByID(chapterID)
	if chapter == nil {
		return nil, fmt.Errorf("%w: 章 %q", ports.ErrNotFound, chapterID)
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
		return nil, fmt.Errorf("%w: 章台本プロンプトの構築に失敗しました: %w", ports.ErrGeneration, err)
	}

	// 2. 生成（構造化出力: スキーマで文法レベルに制約する）
	if err := requireModel(opts.Model, "章台本"); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "ChapterScriptRunner: Gemini APIを呼び出し中",
		"model", opts.Model, "chapter", chapterID)
	resp, err := r.aiClient.GenerateWithAttachments(ctx, opts.Model, finalPrompt, nil, buildJSONGenerateOptions(chapterScriptSchema()))
	if err != nil {
		return nil, fmt.Errorf("%w: 章 %q の台本生成に失敗しました: %w", ports.ErrGeneration, chapterID, err)
	}

	// 3. パースと正規化
	var parsed chapterScriptResponse
	if err := parseJSONResponse(resp.Text, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Panels) == 0 {
		return nil, fmt.Errorf("%w: 章 %q のパネルが空です（AI応答に panels がありません）", ports.ErrGeneration, chapterID)
	}
	if len(parsed.Panels) > r.maxPanels {
		slog.WarnContext(ctx, "パネル数が上限を超えたため切り詰めます",
			"chapter", chapterID, "got", len(parsed.Panels), "max", r.maxPanels)
		parsed.Panels = parsed.Panels[:r.maxPanels]
	}

	panels := make([]comic.Panel, 0, len(parsed.Panels))
	for i, p := range parsed.Panels {
		panels = append(panels, comic.Panel{
			ID:           fmt.Sprintf("%s-p%02d", chapterID, i+1),
			ChapterID:    chapterID,
			Shot:         p.Shot,
			Setting:      p.Setting,
			VisualAnchor: p.VisualAnchor,
			Characters:   r.normalizeCharacters(ctx, chapterID, p.Characters),
			Dialogues:    r.normalizeDialogues(ctx, chapterID, p.Dialogues),
		})
	}

	// 4. state へ反映（同章の既存パネルを置き換え、ページ番号を振り直す）
	if !state.ReplaceChapterPanels(chapterID, panels) {
		return nil, fmt.Errorf("%w: 章 %q", ports.ErrNotFound, chapterID)
	}
	state.Repaginate(r.maxPanelsPerPage)
	state.UpdatedAt = time.Now().UTC()

	slog.InfoContext(ctx, "ChapterScriptRunner: 章台本を生成しました",
		"chapter", chapterID, "panels", len(panels))
	return state, nil
}

// normalizeCharacters は AI 出力の登場キャラクターを検証・正規化します。
// characters.json に存在しない ID は、参照解決時にデフォルトキャラクターへ暗黙に
// フォールバックして別人が描かれる事故を防ぐため、background（参照なしのモブ）に降格します。
// 参照画像を添付する primary/secondary が MaxReferencedCharactersPerPanel を超えた分も
// 同じく background に降格します。
func (r *ChapterScriptRunner) normalizeCharacters(ctx context.Context, chapterID string, chars []comic.PanelCharacter) []comic.PanelCharacter {
	result := make([]comic.PanelCharacter, 0, len(chars))
	for _, pc := range chars {
		if strings.TrimSpace(pc.CharacterID) == "" {
			continue
		}
		if pc.Prominence != comic.ProminenceBackground && !r.knownCharacter(pc.CharacterID) {
			slog.WarnContext(ctx, "未定義のキャラクターIDをbackgroundに降格します",
				"chapter", chapterID, "character_id", pc.CharacterID)
			pc.Prominence = comic.ProminenceBackground
		}
		result = append(result, pc)
	}
	return capReferencedCharacters(ctx, chapterID, result)
}

// capReferencedCharacters は、参照画像を添付するキャラクターを
// MaxReferencedCharactersPerPanel まで絞り込み、あふれた分を background に降格します。
// 残す優先度は primary > secondary で、同じ扱いの中では登場順を保ちます（決定的）。
// スライス自体の並びは変えないため、AI が意図したコマ内の登場順は維持されます。
func capReferencedCharacters(ctx context.Context, chapterID string, chars []comic.PanelCharacter) []comic.PanelCharacter {
	referenced := make([]int, 0, len(chars))
	for i := range chars {
		if chars[i].Prominence != comic.ProminenceBackground {
			referenced = append(referenced, i)
		}
	}
	if len(referenced) <= comic.MaxReferencedCharactersPerPanel {
		return chars
	}

	slices.SortStableFunc(referenced, func(a, b int) int {
		return prominenceRank(chars[a].Prominence) - prominenceRank(chars[b].Prominence)
	})
	for _, i := range referenced[comic.MaxReferencedCharactersPerPanel:] {
		slog.WarnContext(ctx, "参照キャラクターが上限を超えたためbackgroundに降格します",
			"chapter", chapterID,
			"character_id", chars[i].CharacterID,
			"max", comic.MaxReferencedCharactersPerPanel)
		chars[i].Prominence = comic.ProminenceBackground
	}
	return chars
}

// prominenceRank は降格の優先順位（小さいほど残す）を返します。
// primary 以外（secondary・未指定）はまとめて後回しにします。
func prominenceRank(prominence string) int {
	if prominence == comic.ProminencePrimary {
		return 0
	}
	return 1
}

// normalizeDialogues は AI 出力のセリフを検証・正規化します。
// 空テキストの行は取り除き、characters.json に存在しない話者IDはナレーション扱いにします。
// 未知の ID をそのまま話者名としてページ合成プロンプトに載せると、キャラクター名の位置に
// 生の ID が描かれてしまうためで、登場キャラクターIDと同じ「AI が出した ID は検証してから
// 使う」という不変条件に揃えています。
func (r *ChapterScriptRunner) normalizeDialogues(ctx context.Context, chapterID string, lines []comic.DialogueLine) []comic.DialogueLine {
	result := make([]comic.DialogueLine, 0, len(lines))
	for _, line := range lines {
		line.Text = strings.TrimSpace(line.Text)
		if line.Text == "" {
			continue
		}
		line.SpeakerID = strings.TrimSpace(line.SpeakerID)
		if line.SpeakerID != "" && !r.knownCharacter(line.SpeakerID) {
			slog.WarnContext(ctx, "未定義の話者IDをナレーションに変更します",
				"chapter", chapterID, "speaker_id", line.SpeakerID)
			line.SpeakerID = ""
			line.Kind = comic.DialogueKindNarration
		}
		result = append(result, line)
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
func outlineDigest(chapters []comic.Chapter, targetID string) string {
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
