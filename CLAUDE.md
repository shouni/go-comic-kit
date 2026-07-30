# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

go-comic-kit is a Go library for AI manga/comic generation with character-identity consistency. It is the successor to [go-manga-kit](https://github.com/shouni/go-manga-kit) (now frozen, kept only for the ap-manga-web showcase) and is the core library for **ap-comic**, an MCP-enabled orchestrator service.

**`README.md` is the authoritative reference** — schema, operation set, and MCP tool mapping. Read it before making non-trivial changes, and keep it in sync when changing contracts.

## Common commands

```bash
go build ./...
go test ./...
go test ./internal/operations/ -run TestGenerateChapterScript -v   # single test
go test -race ./...                                    # CI runs with -race
go vet ./...
gofmt -l .
golangci-lint run    # config in .golangci.yml (v2 format; errcheck, staticcheck, gocritic, revive, ...)
```

CI (`.github/workflows/ci.yml`) runs exactly: `go build`, `go vet`, gofmt check, `go test -race`, golangci-lint, and govulncheck. revive requires doc comments on all exported symbols (this repo's comments are written in Japanese).

Dependencies (`gemini-image-kit`, `go-character-kit`, `go-remote-io`, `go-utils`) are pinned to the versions proven in go-manga-kit — don't bump casually. `go-gemini-client` is at v1.15.1+ on purpose: script generation relies on its `GenerateOptions.ResponseJSONSchema` (structured output / constrained decoding, same approach as its `lyria` package) so the model's JSON is grammar-constrained instead of regex-repaired.

**Nothing in this repo imports `google.golang.org/genai`.** The SDK stays inside `go-gemini-client`: `operations.StructuredGenerator` is an alias of `gemini.MultimodalGenerator` (prompt + attachments, no `genai.Part`), schemas in `internal/operations/schemas.go` are plain `map[string]any` JSON Schema, safety thresholds come from `gemini.SafetyBlockNone`, and the image core takes `gemini.MultimodalModel`. Re-introducing `genai.Schema` or `GenerateWithParts` would pull the SDK back into this kit's signatures and into every mock that implements them. `internal/operations/json_response.go`'s `parseJSONResponse` calls `gemini.CleanJSONResponse` (go-gemini-client) as a defensive layer against trailing noise the model still occasionally emits despite constrained decoding.

## Architecture

### Core principle: MangaState + idempotent operations

`ports.MangaState` (persisted as `comic_state.json`) is the single source of truth for one work: chapters, panels, design-sheet records, page artifacts, and the generation conditions (`GenerationRecord`: seed/prompt/model) of every image. All operations are idempotent and state-in/state-out:

| Operation (ports interface) | Status |
|---|---|
| `OutlineGenerator.GenerateOutline` — source text → chapters-only state | implemented (`internal/operations/outline.go`) |
| `ChapterScriptGenerator.GenerateChapterScript` — panels for ONE chapter, replaces existing | implemented (`internal/operations/chapter.go`) |
| `DesignSheetGenerator.GenerateDesignSheet` — identity-anchor sheets | implemented (`internal/operations/design.go`) |
| `PanelImageGenerator.GeneratePanel` — per-panel image gen/regen (seed re-roll or `EditPrompt` image-to-image edit) | implemented (`internal/operations/panel.go`) |
| `PageImageComposer.ComposePage` — compose one page from its panels (layout map, balloons, `EditPrompt` edit) | implemented (`internal/operations/page.go`) |
| `PanelBatchGenerator.GenerateAllPanels` / `PageBatchComposer.ComposeAllPages` — batch generation at `Config.MaxConcurrency` | implemented (same runners; `internal/operations/batch.go` holds the shared `runBatch`) |

Batch operations return the state **and** a joined error on partial failure — the successes are already recorded, so the caller saves the state and re-runs with `BatchOptions{SkipGenerated: true}` to retry only what failed. Because they run panels/pages in parallel, `PanelResourceProvider` / `PageResourceProvider` implementations must be safe for concurrent use, and the per-item render (`renderPanel` / `renderPage`) must not write to shared state — it returns a record that the caller applies after `Wait`.

There is deliberately NO publish/export operation (v1's HTML/Markdown output was dropped): presentation is the consuming app's job, reading the state document and GCS images directly.

Script generation is deliberately **two-stage** (outline → per-chapter panels) to keep each LLM call's JSON schema small and to give chapter-level regeneration granularity. Downstream regeneration (`regenerate_panel` etc.) works by re-running the same operation against the state.

### Error classification

Operations wrap their errors with the sentinels in `ports/errors.go` (`ErrNotFound`, `ErrInvalidRequest`, `ErrGeneration`) so the consuming app maps them to HTTP status with `errors.Is` instead of matching message strings. New failure paths must pick one of the three — an unwrapped `fmt.Errorf` is invisible to the caller's classification.

### Data-model invariants

- **IDs are assigned server-side, never trusted from AI output**: chapters get `ch01`-style IDs, panels get `ch01-p03`-style IDs. These are regeneration targets and must stay stable.
- A `Panel` holds `Characters []PanelCharacter` (who appears — independent of who speaks) and `Dialogues []DialogueLine` (multiple balloons; empty `SpeakerID` = narration). Character relationships are free text in `PanelCharacter.Action`.
- `Prominence` controls reference-image attachment: `primary`/`secondary` characters get their design sheets attached at image-gen time; `background` (mobs) get none. `ReferencedCharacterIDs()` / `UniqueReferencedCharacterIDs()` encode this rule.
- Character IDs unknown to characters.json are **demoted to background** during chapter-script normalization — otherwise reference resolution silently falls back to the default character and draws the wrong person.
- Dialogue `SpeakerID`s unknown to characters.json are converted to narration (empty speaker + `narration` kind) during normalization — same "never trust AI-supplied IDs" rule as `CharacterID`; otherwise the raw ID gets drawn where the speaker's name belongs.
- `primary` + `secondary` per panel is capped at `ports.MaxReferencedCharactersPerPanel` (3); the overflow is demoted to `background`, keeping primaries and preserving in-panel order. The README documented this limit long before the code enforced it.
- After changing a chapter's panels, call `state.ReplaceChapterPanels` (keeps chapter order, updates `Chapter.PanelIDs`) then `state.Repaginate`. `Repaginate` breaks pages at chapter boundaries (never mixes two chapters onto one page) and drops `PageArtifact`s whose panel composition changed, so stale page images can't survive a chapter regeneration. Artifacts whose composition is unchanged are kept, so regenerating one chapter doesn't force re-composing unrelated pages.

### Packages

Public API surface is deliberately small: `ports`, `asset`, `store`, `workflow`. Everything else lives under `internal/` because go-comic-kit currently has exactly one consumer (`ap-comic`), which only ever imports those four — there's no reason to expose implementation packages nobody outside this module touches. If a second consumer ever needs direct access to `internal/operations`, `internal/prompts`, or `internal/layout`, un-hiding a package is a one-line move; keeping them internal until then costs nothing.

- **`ports`** — contracts and the MangaState data model. No dependencies on other packages here; everything else imports ports.
- **`internal/prompts`** — kit-embedded prompt implementations, all overridable via `workflow.Args` (nil = kit default): `ScriptPrompts` for outline/chapter (`go:embed templates/{outline,chapter}/*.md`; dropping a new `.md` file adds a new mode, filename = mode name, like ap-comp's visual_mode) implementing `ports.OutlinePrompt` / `ports.ChapterScriptPrompt`, and `DefaultDesignPrompt` (plain Go, no templates — subject list and layout are structural, not template-friendly) implementing `ports.DesignSheetPrompt`. The outline/chapter mode used is persisted in `MangaState.ScriptMode` so regeneration uses the same prompt.
- **`internal/operations`** — one file per operation (`outline.go`, `chapter.go`, `design.go`, `panel.go`, `page.go`), plus purpose-named shared helpers (`gen_opts.go`, `json_response.go`, `source_text.go`, `character_roster.go`, `image_output.go`, `seed.go`, `panel_desc.go` — no grab-bag `common.go`). Operations (`OutlineRunner`, `ChapterScriptRunner`, etc. — the `Runner` suffix stays on the types even though the package is `operations`) depend on narrow interfaces (`CharacterResourceProvider`, `DesignImageGenerator`, `gemini.ContentGenerator`), not concrete layout types, so tests use lightweight fakes.
- **`internal/layout`** — `ComicComposer`: pre-upload and caching of reference images (singleflight dedup; Vertex AI + `gs://` URIs bypass the File API upload entirely and resolve to empty string). Aspect-ratio constants and normalization live in `types.go`.
- **`asset`** — the single place that knows where artifacts live. Exposes purpose-named builders (`StatePath`, `PanelImagePath`, `PageImagePath`, `DesignSheetPath`, `CharacterDesignPrefix`) rather than re-exporting `go-utils/urlpath`; `go-utils` is an implementation detail confined here. Layout rules that used to be smeared across `design.go` / `panel.go` / `page.go` / `store.go` (and re-derived by ap-comic via `fmt.Sprintf`) now live here, including `DesignFileTag`'s rune-boundary truncation and `SanitizeFileName`. When adding an artifact kind, add its path function here — do not `path.Join` in an operation.
- **`store`** — Load/Save of the MangaState document (`comic_state.json`, upsert-style overwrite; Load rejects newer schema versions).
- **`workflow`** — the DI layer: `workflow.New(Args)` assembles all operations (two generation units: standard model for panels, quality model for design sheets and pages). Outline/ChapterScript/DesignSheet prompts are kit-embedded by default and overridable via `Args.OutlinePrompt` / `ChapterScriptPrompt` / `DesignSheetPrompt`; Panel/Page prompts are built internally from the structured `Panel` data and are not DI-overridable at that level, though `GenerateOptions.PromptOverride` allows a per-call override. Call `Operations.Close()` when done to stop the internal TTL caches. All AI calls (text + image) are wrapped in **singleflight decorators** (`workflow/singleflight.go`): identical in-flight requests are collapsed to one API call, shared responses are cloned per caller, and the shared execution runs on a detached context so one caller's cancel can't kill piggybacking callers. This only dedupes within one process — durable idempotency is the job of the consuming app via `GenerationRecord`. The same decorators carry a `callGuard` that makes two previously-dead `Config` fields real: `RateInterval` (minimum spacing between AI calls, one limiter shared by text + image because quota is per project) and `RequestTimeout` (per-call cap, replacing the old hardcoded 5-minute constant; also passed to `layout.NewComicComposer` for reference uploads). The rate-limit wait happens *outside* the timeout so a busy queue can't manufacture timeouts. Note that throughput is bounded by `1/RateInterval` regardless of `MaxConcurrency` — setting both high-interval and high-concurrency is contradictory.

### Prompt-quality rules (hard-won, do not regress)

- `Config.DesignStyleSuffix` is separate from `Config.StyleSuffix`: panel/page prompts may use cinematic lighting, but design sheets must NOT — sheets are identity anchors and baked-in lighting contaminates every downstream generation. `internal/operations/design.go` additionally force-appends flat-lighting/white-background/five-fingers constraints after any suffix and sets a system prompt + negative prompt (finger anatomy, text/label/swatch exclusion).
- Design-sheet filenames from many/long character IDs are truncated at rune boundaries with a CRC32 suffix (`designFileTag`) to respect filesystem name limits without collisions.

## Related projects (local checkouts)

- `../go-manga-kit` — the frozen predecessor; port source for remaining code (panel/page generators, publisher, workflow DI).
- `../go-veo-orchestrator` — pattern reference: its `EditCut` (image-to-image keyframe edit) is the model for `GenerateOptions.EditPrompt`; its `CharacterResourceProvider` interface inspired the operations decoupling.
- `../ap-comp` + ap-mcp — the architecture template for the future ap-comic service (Cloud Run + Cloud Tasks async jobs, MCP tools returning job IDs, history read from state documents on GCS). History/job management belongs in the app, not this kit.
