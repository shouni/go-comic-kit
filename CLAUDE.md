# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

go-comic-kit is a Go library for AI manga/comic generation with character-identity consistency. It is the successor to [go-manga-kit](https://github.com/shouni/go-manga-kit) (now frozen, kept only for the ap-manga-web showcase) and is being built as the core library for **ap-comic**, a planned MCP-enabled orchestrator service. Proven infrastructure code is ported from go-manga-kit package by package while the contracts are redesigned.

**`docs/comic-kit-design.md` is the authoritative design document** — schema, operation set, MCP tool mapping, and migration policy. Read it before making non-trivial changes, and keep it in sync when changing contracts.

## Common commands

```bash
go build ./...
go test ./...
go test ./runner/ -run TestGenerateChapterScript -v   # single test
go test -race ./...                                    # CI runs with -race
go vet ./...
gofmt -l .
golangci-lint run    # config in .golangci.yml (v2 format; errcheck, staticcheck, gocritic, revive, ...)
```

CI (`.github/workflows/ci.yml`) runs exactly: `go build`, `go vet`, gofmt check, `go test -race`, golangci-lint, and govulncheck. revive requires doc comments on all exported symbols (this repo's comments are written in Japanese).

Dependencies (`gemini-image-kit`, `go-character-kit`, `go-remote-io`, `go-utils`) are pinned to the versions proven in go-manga-kit — don't bump casually. `go-gemini-client` is at v1.11.0+ on purpose: script generation relies on its `GenerateOptions.ResponseSchema` (structured output / constrained decoding, same approach as its `lyria` package) so the model's JSON is grammar-constrained instead of regex-repaired. `cleanJSONResponse` in `runner/script_common.go` is the ported lyria decoder-based cleanup kept as a defensive layer.

## Architecture

### Core principle: MangaState + idempotent operations

`ports.MangaState` (persisted as `comic_state.json`) is the single source of truth for one work: chapters, panels, design-sheet records, page artifacts, and the generation conditions (`GenerationRecord`: seed/prompt/model) of every image. All operations are idempotent and state-in/state-out:

| Operation (ports interface) | Status |
|---|---|
| `OutlineGenerator.GenerateOutline` — source text → chapters-only state | implemented (`runner/outline.go`) |
| `ChapterScriptGenerator.GenerateChapterScript` — panels for ONE chapter, replaces existing | implemented (`runner/chapter.go`) |
| `DesignSheetGenerator.GenerateDesignSheet` — identity-anchor sheets | implemented (`runner/design.go`) |
| `PanelImageGenerator.GeneratePanel` — per-panel image gen/regen (seed re-roll or `EditPrompt` image-to-image edit) | implemented (`runner/panel.go`) |
| `PageImageComposer.ComposePage` — compose one page from its panels (layout map, balloons, `EditPrompt` edit) | implemented (`runner/page.go`) |

There is deliberately NO publish/export operation (v1's HTML/Markdown output was dropped): presentation is the consuming app's job, reading the state document and GCS images directly.

Script generation is deliberately **two-stage** (outline → per-chapter panels) to keep each LLM call's JSON schema small and to give chapter-level regeneration granularity. Downstream regeneration (`regenerate_panel` etc.) works by re-running the same operation against the state.

### Data-model invariants

- **IDs are assigned server-side, never trusted from AI output**: chapters get `ch01`-style IDs, panels get `ch01-p03`-style IDs. These are regeneration targets and must stay stable.
- A `Panel` holds `Characters []PanelCharacter` (who appears — independent of who speaks) and `Dialogues []DialogueLine` (multiple balloons; empty `SpeakerID` = narration). Character relationships are free text in `PanelCharacter.Action`.
- `Prominence` controls reference-image attachment: `primary`/`secondary` characters get their design sheets attached at image-gen time; `background` (mobs) get none. `ReferencedCharacterIDs()` / `UniqueReferencedCharacterIDs()` encode this rule.
- Character IDs unknown to characters.json are **demoted to background** during chapter-script normalization — otherwise reference resolution silently falls back to the default character and draws the wrong person.
- After changing a chapter's panels, call `state.ReplaceChapterPanels` (keeps chapter order, updates `Chapter.PanelIDs`) then `state.Repaginate`.

### Packages

- **`ports`** — contracts and the MangaState data model. No dependencies on other packages here; everything else imports ports.
- **`prompts`** — kit-embedded prompt implementations, all overridable via `workflow.Args` (nil = kit default): `ScriptPrompts` for outline/chapter (`go:embed templates/{outline,chapter}/*.md`; dropping a new `.md` file adds a new mode, filename = mode name, like ap-comp's visual_mode) implementing `ports.OutlinePrompt` / `ports.ChapterScriptPrompt`, and `DefaultDesignPrompt` (plain Go, no templates — subject list and layout are structural, not template-friendly) implementing `ports.DesignSheetPrompt`. The outline/chapter mode used is persisted in `MangaState.ScriptMode` so regeneration uses the same prompt.
- **`runner`** — one file per operation. Runners depend on narrow interfaces (`CharacterResourceProvider`, `DesignImageGenerator`, `gemini.ContentGenerator`), not concrete layout types, so tests use lightweight fakes.
- **`layout`** — `ComicComposer`: pre-upload and caching of reference images (singleflight dedup; Vertex AI + `gs://` URIs bypass the File API upload entirely and resolve to empty string). Aspect-ratio constants and normalization live in `types.go`.
- **`asset`** — file-naming conventions and GCS/local output path resolution.
- **`store`** — Load/Save of the MangaState document (`comic_state.json`, upsert-style overwrite; Load rejects newer schema versions).
- **`workflow`** — the DI layer: `workflow.New(Args)` assembles all five operations (two generation units: standard model for panels, quality model for design sheets and pages; kit-embedded prompts by default, overridable via Args). Call `Operations.Close()` when done to stop the internal TTL caches. All AI calls (text + image) are wrapped in **singleflight decorators** (`workflow/singleflight.go`): identical in-flight requests are collapsed to one API call, shared responses are cloned per caller, and the shared execution runs on a detached context so one caller's cancel can't kill piggybacking callers. This only dedupes within one process — durable idempotency is the job of the consuming app via `GenerationRecord`.

### Prompt-quality rules (hard-won, do not regress)

- `Config.DesignStyleSuffix` is separate from `Config.StyleSuffix`: panel/page prompts may use cinematic lighting, but design sheets must NOT — sheets are identity anchors and baked-in lighting contaminates every downstream generation. `runner/design.go` additionally force-appends flat-lighting/white-background/five-fingers constraints after any suffix and sets a system prompt + negative prompt (finger anatomy, text/label/swatch exclusion).
- Design-sheet filenames from many/long character IDs are truncated at rune boundaries with a CRC32 suffix (`designFileTag`) to respect filesystem name limits without collisions.

## Related projects (local checkouts)

- `../go-manga-kit` — the frozen predecessor; port source for remaining code (panel/page generators, publisher, workflow DI).
- `../go-veo-orchestrator` — pattern reference: its `EditCut` (image-to-image keyframe edit) is the model for `GenerateOptions.EditPrompt`; its `CharacterResourceProvider` interface inspired the runner decoupling.
- `../ap-comp` + ap-mcp — the architecture template for the future ap-comic service (Cloud Run + Cloud Tasks async jobs, MCP tools returning job IDs, history read from state documents on GCS). History/job management belongs in the app, not this kit.
