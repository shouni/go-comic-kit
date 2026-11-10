# 🎨 Go Comic Kit

[![CI](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/go-comic-kit/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/go-comic-kit)](https://go.dev/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/go-comic-kit)](https://github.com/shouni/go-comic-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/go-comic-kit.svg)](https://pkg.go.dev/github.com/shouni/go-comic-kit)

English | [日本語](README.ja.md)

## 🚀 About - one state per work, comics you can re-shoot one panel at a time

**Go Comic Kit** is a toolkit for **AI comic generation that keeps characters consistent**.

It keeps every piece of state of one work in a single state document (`MangaState`) and provides each step — outline, chapter script, design sheet, panel and page — as an **idempotent operation**. "Regenerate only panel 3 of 12 with a re-rolled seed" is expressed directly as an API call.

---

## ✨ Features

* **📄 MangaState = the single source of truth**
  Every piece of state of one work (the script, the characters who appear, the generation conditions of panels and pages, the artifact URLs) is persisted as one state document. History listings and detail views are just the app reading the state.

* **🔁 Idempotent, step-level operations**
  `GenerateOutline` creates a new state from a manuscript; every later operation takes the state and returns the updated state. They map one-to-one onto MCP tools (`regenerate_panel` and so on).

* **👥 Multi-character panels**
  A panel is not "one speaker" but **the set of characters who appear** (`Characters []PanelCharacter`). Emotion, action (relationships), position and treatment (primary/secondary/background) can be set individually, and primary/secondary characters get their reference images attached even when they do not speak, so their identity holds (background characters get no reference image).

* **🧬 3-Factor Consistency Control**
  Character consistency is controlled by three factors: the **seed** (the foundation), **reference assets** (appearance) and **VisualCues / verbal instructions** (details). The generation conditions are persisted in the state as a `GenerationRecord`.
  **The seed is always recorded.** Even when none is specified, a definite value is always sent, resolved in the order "previous value → the lead character's seed → newly drawn", so re-reading `UsedSeed` reproduces the same picture at any time (if no seed is sent, the value the API picks is never returned and the result cannot be reproduced).

* **📐 Structured output (constrained decoding)**
  Script generation uses `ResponseJSONSchema` (plain JSON Schema) so the model output is **constrained to the schema at the grammar level**. Broken JSON is prevented at the source instead of repaired afterwards, and enum constraints keep invalid values out of `prominence` and `kind`.

* **✏️ Regeneration with edit mode**
  Besides re-rolling the seed, **instruction-based partial edits** of an existing generated image (`EditPrompt`) are supported. Fixes like "keep the composition, just make the expression a smile" work per panel and per page.

* **📝 The app owns every prompt (DI required)**
  The prompts of all five operations are **always injected** through `workflow.Args`. The kit has no built-in prompts. Prompts are wording refined per work, and keeping them in the kit would require a kit release for every one-character change ([details](docs/configuration.md#prompt-injection)).

* **🌍 Multi-Backend Asset Support**
  `workflow` assembles how reference images are sent. `gs://` is not transferred but passed as a URI (Vertex AI resolves it model-side, so nothing is fetched or uploaded); only non-`gs://` references (temporary override URLs and the like) are fetched and sent inline. Fetching has time and size limits, and the MIME type is detected from the content. Resolution sits inside the deduplication (singleflight), so concurrent identical calls do not download the same image once per caller. The operations layer deals only with "which image was attached at which position".

* **🔂 Deduplicated AI calls**
  Concurrent identical text/image generation requests are collapsed into one API call by `singleflight` (against Cloud Tasks' at-least-once delivery and retries). Only in-flight calls within the process are covered; durable idempotency is the app's decision, made with `GenerationRecord`.

---

## 📦 Package Structure

The boundary is the **abstraction through ports**, which makes each generation step a swappable strategy. The public packages are narrowed to five to match actual use; everything else (the step implementations, the ratio vocabulary) lives under `internal/`.

```text
go-comic-kit/
├── comic/                 # [Data model] MangaState and its methods. The starting point of everything.
├── ports/                 # [Contracts] Operation interfaces, input/output types, Config.
├── workflow/              # [Assembly] Builds the five operations and implements Operations. Also singleflight deduplication.
├── store/                 # [Persistence] Load/Save of MangaState (comic_state.json).
├── asset/                 # [Placement rules] The one place that decides artifact paths.
└── internal/
    ├── operations/        # [Implementations] The concrete Outline/Chapter/Design/Panel/Page processes.
    └── layout/            # [Constants] Aspect ratio and image size definitions and normalization.
```

Everything under `internal/` is an implementation detail used only by `workflow`. If a consumer ever needs direct access to them, a package can be made public just by moving it out of `internal/`.

---

## 🚦 Usage

`workflow.New` assembles every operation from the configuration and the clients.

```go
ops, err := workflow.New(workflow.Args{
	// Config is run control only (parallelism, spacing, timeouts, limits). All optional;
	// ApplyDefaults fills zero values. Model, art style, ratio and resolution are passed per call
	Config:          ports.Config{},
	Downloader:      httpClient,      // ports.Downloader (used only to fetch reference images)
	Reader:          reader,          // ports.ContentReader (GCS/local/HTTP via go-remote-io)
	Writer:          writer,
	AIClient:        aiClient,        // genai-kit's gemini.Generator, used for both script and image generation
	Characters:      characters,      // go-character-kit (characters.json)

	// All five prompts are required. The kit has none built in ("The app owns every prompt" above)
	OutlinePrompt:       outlinePrompt,
	ChapterScriptPrompt: chapterPrompt,
	DesignSheetPrompt:   designPrompt,
	PanelPrompt:         panelPrompt,
	PagePrompt:          pagePrompt,
})
if err != nil {
	return err
}

// Outline → per-chapter script → design sheet → panels → pages
// Pass the model name every time (required). Art style, ratio and resolution are specified here too
state, _ := ops.Outline.GenerateOutline(ctx, ports.OutlineRequest{
	SourceURL: "gs://bucket/article.md", Model: textModel,
})
state.ID = workID // the kit does not set the work ID, so the app mints and sets it
state, _ = ops.ChapterScript.GenerateChapterScript(ctx, state, "ch01", ports.ChapterScriptOptions{Model: textModel})
state, _ = ops.DesignSheet.GenerateDesignSheet(ctx, state, ports.DesignSheetRequest{
	CharacterIDs: []string{"zundamon"}, JobID: jobID, OutputDir: outDir, Model: imageModel,
})
state, _ = ops.Panel.GeneratePanel(ctx, state, "ch01-p01", ports.GenerateOptions{OutputDir: outDir, Model: imageModel})
state, _ = ops.Page.ComposePage(ctx, state, 1, ports.GenerateOptions{OutputDir: outDir, Model: imageModel})

// Batch generation of all panels and pages (parallelised by Config.MaxConcurrency)
state, _ = ops.Panel.GenerateAllPanels(ctx, state, ports.BatchOptions{OutputDir: outDir, Model: imageModel})
state, _ = ops.Page.ComposeAllPages(ctx, state, ports.BatchOptions{OutputDir: outDir, Model: imageModel})

// Save the state (the single source of truth; regenerating is just re-reading it and calling the same operation)
_, _ = store.Save(ctx, writer, state, outDir)
```

Regeneration examples: `ops.Panel.GeneratePanel(ctx, state, "ch01-p03", ports.GenerateOptions{Seed: &newSeed})` (re-roll the seed), `ports.GenerateOptions{EditPrompt: "make the expression a smile"}` (partial edit of the existing image).

---

## 📚 Documentation

| Document | Contents |
| --- | --- |
| [Schema (comic.MangaState)](docs/schema.md) | The data model, state helpers, pagination and character normalization rules |
| [Configuration and DI (Config / DI)](docs/configuration.md) | `ports.Config`, prompt DI, the dependencies in `workflow.Args` |
| [Operation set (Operations)](docs/operations.md) | The five operations and batch generation, per-call overrides, error classification |
| [Artifact placement (asset)](docs/assets.md) | Save path conventions and file name generation |

---

## 🤝 Dependencies

* [shouni/genai-kit](https://github.com/shouni/genai-kit) - Vertex AI client (with structured output) and image generation (`imagegen`)
* [shouni/go-character-kit](https://github.com/shouni/go-character-kit) - Character asset (characters.json) management
* [shouni/go-remote-io](https://github.com/shouni/go-remote-io) - Read/write abstraction over GCS, local and HTTP

## 📜 License

This project is released under the [MIT License](https://opensource.org/licenses/MIT).
