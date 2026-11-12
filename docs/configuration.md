# ⚙️ Configuration and DI (Config / DI)

[← README](../README.md)

## ports.Config

The configuration passed to `workflow.New(Args)`. **There are no required fields**; everything may be left at its zero value (`ApplyDefaults` fills it in).

What remains here is only the run control that keeps the kit working without breaking. **Model names, art style, aspect ratio and resolution are not in Config** — they all vary per work, so `GenerateOptions` / `BatchOptions` / `DesignSheetRequest` / `OutlineRequest` / `ChapterScriptOptions` take them per call. Putting a deployment-level default here would make an app that always specifies them manage a value it never uses, and that value would silently hide any omission.

Per-call values are validated by each operation before it runs, returning `ErrInvalidRequest` (no AI API call is made). An empty model name and an unsupported ratio or resolution are such cases. As before, **a typo is never dropped to the default**, because doing so lets "not generated at the ratio I asked for" go on unnoticed.

| Setting | Role |
| --- | --- |
| `MaxConcurrency` | Maximum parallelism of batch generation (does not affect single-panel or single-page operations) |
| `RateInterval` | Minimum spacing between AI calls. Text and image **share one limiter** (quota is per project). **Throughput is capped by 1/RateInterval, not by `MaxConcurrency`** |
| `RequestTimeout` | Limit per AI call (also applied to reference image uploads). Not a limit on the whole sequence of steps |
| `CacheControl` | `Cache-Control` used when saving generated images. Empty means `ports.DefaultCacheControl` (`public, max-age=1800`). A deployment writing to a private bucket should set `private` or similar |
| `MaxChapters` / `MaxPanelsPerChapter` / `MaxPanelsPerPage` | Limits on the number of chapters, panels per chapter and panels per page |

### Values passed per call

| Field | Passed to | Default |
| --- | --- | --- |
| `Model` | `OutlineRequest` / `ChapterScriptOptions` / `GenerateOptions` / `BatchOptions` / `DesignSheetRequest` | **Required** (empty is `ErrInvalidRequest`) |
| `StyleMode` | `GenerateOptions` / `BatchOptions` / `DesignSheetRequest` | Empty means the prompt implementation's default |
| `AspectRatio` | Same as above | Empty means `3:4`. **Keep it the same** for panels, pages and sheets; otherwise the drift suppression from reference images is silently disabled |
| `ImageSize` | Same as above | Empty means 1K for panels and 2K for pages and sheets |

## Prompt injection

The prompts of all five operations are injected through `workflow.Args`. **All five are required**; if any is nil, `workflow.New` fails to build. The kit has no built-in prompts.

| Args field | Interface | Data the implementation receives |
| --- | --- | --- |
| `OutlinePrompt` | `ports.OutlinePrompt` | `OutlinePromptData` |
| `ChapterScriptPrompt` | `ports.ChapterScriptPrompt` | `ChapterPromptData` |
| `DesignSheetPrompt` | `ports.DesignSheetPrompt` | `DesignSheetPromptData` |
| `PanelPrompt` | `ports.PanelPrompt` | `PanelPromptData` |
| `PagePrompt` | `ports.PagePrompt` | `PagePromptData` |

**The kit holding no prompts at all is deliberate.** Even structural instructions — the order matching the reference images, the panel count, the reading order — are refined differently per work. Keeping them in the kit would require a kit release for every one-character change to a prompt, so, like model names and art direction, the policy is unified on **the app owning them**. On the consuming side, set up a layer such as `internal/adapters/prompts` and gather prompt assembly there.

`GenerateOptions.PromptOverride` replaces **only the body** per call (the implementation's system instruction and negative prompt remain).

### ⚠️ Reference image numbers

For images, `PanelPromptData.SubjectIDs` and `PagePromptData.CharacterFile` / `PanelFile` are **the order in which images are actually attached**. Always use them when writing reference numbers in a prompt. If you recount on your own, the model ends up drawing while looking at someone else's reference image. For design sheets, `DesignSheetPromptData.Referenced` plays this role: `Referenced[i]` says whether `Descriptions[i]`'s character has an image attached, and the attached images follow the subjects marked true, in order. A subject marked false (a new character with no reference yet) is drawn from her description alone — do not tell the model to follow an image for her.

## Dependencies in workflow.Args

| Field | Role |
| --- | --- |
| `Downloader` | `ports.Downloader` (the single method `GetStream`, fetching reference images over http(s); `*httpkit.Client` satisfies it as is) |
| `Reader` / `Writer` | `ports.ContentReader` / `remoteio.Writer` (reading the manuscript, saving artifacts) |
| `AIClient` | The `gemini.Model` used for both script and image generation (shared by design sheets, panels and pages) |
| `Characters` | `*comic.Characters` (go-character-kit's `characters.json`) |

The `*ports.Operations` returned by `workflow.New` **needs no cleanup**. The reference image cache decides expiry on read and has no background goroutine for cleaning up (a `Close() error` call used to be required).
