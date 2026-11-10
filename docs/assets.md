# 📦 Artifact placement (asset)

[← README](../README.md)

The `asset` package is the **only decider** of where generated artifacts are saved. Do not write `fmt.Sprintf` or `path.Join` in the app; always use these functions. If the convention lives in two places, a change to one of them makes existing artifacts impossible to find.

| Function | Path returned |
| --- | --- |
| `asset.StatePath(baseDir)` | The state document (`comic_state.json`) |
| `asset.PanelImagePath(baseDir, panelID, ext)` | A panel image (`images/panel_{id}{ext}`) |
| `asset.PageImagePath(baseDir, page, ext)` | A page image (`images/comic_page_{n}{ext}`) |
| `asset.DesignSheetPath(baseDir, characterIDs, jobID, ext)` | A design sheet (`character/{tag}/{jobID}{ext}`) |
| `asset.CharacterDesignPrefix(baseDir, characterID)` | The directory of one character's sheets (used for history listings) |

`DesignSheetPath` takes a set of character IDs (several for a combined sheet), while `CharacterDesignPrefix` takes a **single character ID**.

For the three functions that take an extension (`ext`), always pass **a value decided from the generated MIME type** (`imagePorts.ExtensionByMIMEType(resp.MimeType)`). Save paths stay in URLs and history, so an extension that contradicts the contents cannot be fixed later.

## File name conventions

| Function | Role |
| --- | --- |
| `asset.DesignFileTag(characterIDs)` | Builds a directory name from character IDs. Truncates at a rune boundary to stay under the file name length limit, and appends a CRC32 to avoid collisions |
| `asset.SanitizeFileName(name)` | Removes characters that cannot be used in a file name |

Path resolution in `go-remote-io/remoteio` is an implementation detail of this package and is not exposed. When adding a new kind of artifact, add its placement function here too.
