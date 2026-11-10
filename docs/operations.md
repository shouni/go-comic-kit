# 🔁 Operation set (Operations)

[← README](../README.md)

Every operation is idempotent. `GenerateOutline` creates a new state from a manuscript, and every later operation takes the state and returns the updated state (state in / state out).

| Operation (`ops.` field) | Interface | What it does |
| --- | --- | --- |
| `Outline.GenerateOutline` | `ports.OutlineGenerator` | Generates a MangaState holding only the outline (Chapters) from a manuscript |
| `ChapterScript.GenerateChapterScript` | `ports.ChapterScriptGenerator` | Generates or replaces the given chapter's storyboard (characters, dialogue, composition) |
| `DesignSheet.GenerateDesignSheet` | `ports.DesignSheetGenerator` | Generates a design sheet that pins a character's DNA (seed and features) |
| `Panel.GeneratePanel` | `ports.PanelImageGenerator` | Generates or regenerates one panel (same conditions, new seed, or an edit instruction) |
| `Page.ComposePage` | `ports.PageImageComposer` | Re-lays out and composes one page |
| `Panel.GenerateAllPanels` | `ports.PanelImageGenerator` | Generates panels in batch with `MaxConcurrency` parallelism (can be narrowed to a chapter) |
| `Page.ComposeAllPages` | `ports.PageImageComposer` | Composes pages in batch with `MaxConcurrency` parallelism (can be narrowed to a chapter) |

Script generation is split into two stages (outline → panels per chapter), to keep the JSON schema of each LLM call small and to allow regeneration at chapter granularity.

**Images can be narrowed at the same granularity.** With `BatchOptions.ChapterID`, only that chapter's panels and pages are targeted. Images are the most expensive part of the process, so "build one chapter, check it, then move on to the rest" works in the same unit as the script. Pages can be narrowed by chapter because `Repaginate` breaks pages at chapter boundaries (never mixing two chapters on one page). An unknown chapter ID is `ErrNotFound`, never a silent zero-item success.

Output to HTML, Markdown and the like is not part of the kit. Viewing and distribution are the app's responsibility, presented by reading the state document and the images on GCS directly.

## Per-call overrides

| Option | Applies to | What it does |
| --- | --- | --- |
| `GenerateOptions.Seed` | Panels, pages | Re-roll the seed |
| `GenerateOptions.EditPrompt` | Panels, pages | Instruction-based partial edit of an existing generated image ("keep the composition, just make the expression a smile") |
| `GenerateOptions.PromptOverride` | Panels, pages | Replace the prompt body (the system instruction and negative prompt remain) |
| `Model` | Every operation | The model name to use (**required**). Empty is `ErrInvalidRequest`, and no AI API call is made. One work's script should be written by one model, so pass the same value to the outline and every chapter |
| `AspectRatio` / `ImageSize` | Each image operation | Ratio and resolution. Empty means the kit default (3:4 / panels 1K / pages and sheets 2K); an unsupported value is `ErrInvalidRequest` |
| `GenerateOptions.StyleMode` / `BatchOptions.StyleMode` | Panels, pages | The art style mode for this generation. The kit does not interpret it and passes it straight to the prompt implementation as `PanelPromptData.StyleMode` / `PagePromptData.StyleMode`. It carries the mode name rather than resolved text because the style direction and "what to avoid in that style" (the negative prompt) are decided as a pair, and the prompt implementation holds both |
| `DesignSheetRequest.Override` (`ports.DesignOverride`) | Design sheets | One-off replacement of the reference image and `visual_cues` (does not change `characters.json`). **Effective only when a single character is specified** |

## Batch generation and resuming

Batch generation (`ops.Panel.GenerateAllPanels` / `ops.Page.ComposeAllPages`) returns **both the state with the successes recorded and an error** even when some items fail. Image generation is expensive, so after saving the state, calling again with `BatchOptions{SkipGenerated: true}` redoes only what is missing.

Batch generation is another method on the same Runner as the single-item one (it is not split into a separate interface). It runs with `MaxConcurrency` parallelism, so the injected `ports.PanelPrompt` / `ports.PagePrompt` and the `AIClient` implementation must be safe for concurrent calls.

## 🚨 Error classification

Each operation's errors are wrapped with sentinel errors, so a consumer can decide its response by looking only at the class with `errors.Is` (no matching on message strings).

| Sentinel error | Meaning | Expected response |
| --- | --- | --- |
| `ports.ErrNotFound` | The given chapter, panel or page is not in the state | 404 |
| `ports.ErrInvalidRequest` | A required field is missing, the image to edit has not been generated, the model name is empty, an unsupported ratio or resolution, and so on | 400 |
| `ports.ErrGeneration` | The AI call or interpreting its response failed, or saving the generated image failed | 502 (worth retrying) |
| `ports.ErrConfigInvalid` | No setting returns this sentinel today (`Config` has no required fields). It is the place reserved for when settings are added in the future | Construction time only. If it ever returns, it comes out of `workflow.New`, so fail at startup |

A failure to build an image's save path (an invalid `OutputDir`, page number and so on) is caused by the arguments and a retry will not fix it, so it is classified as `ErrInvalidRequest`. A failure of the save itself is often transient, so it is `ErrGeneration`.

`store.Load` / `store.Save` classify with the same sentinels. A missing state file is `ErrNotFound`, broken JSON or a schema version that is too new is `ErrInvalidRequest`, and unreachable storage is `ErrGeneration`.
