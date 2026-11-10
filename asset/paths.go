// Package asset defines where generated comic assets (panels, pages, design sheets
// and the state document) are placed.
//
// This package is the only place that knows where artifacts live. Scatter path
// building across the operations or the consuming apps, and one of history listing,
// deletion or regeneration drifts from the convention and breaks quietly. The generic
// path handling (go-remote-io/remoteio) is confined here, and only purpose-named
// functions are exposed.
package asset

import (
	"fmt"
	"hash/crc32"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/shouni/go-remote-io/remoteio"
)

const (
	// CharacterDesignDir is the directory that holds design sheets.
	// Characters are shared assets independent of any one work, so it usually sits at the bucket root.
	CharacterDesignDir = "character"
	// DefaultImageDir is the directory that holds panel and page images.
	DefaultImageDir = "images"
	// DefaultStateJSON is the file name of the MangaState (state document).
	DefaultStateJSON = "comic_state.json"

	// panelFilePrefix is the file name prefix of panel images (panel_{panelID}.ext).
	panelFilePrefix = "panel_"
	// pageFileBaseName is the base file name of page images (the page number is inserted as an index).
	// The extension is decided from the generated MIME type, so it is not part of this name.
	pageFileBaseName = "comic_page"
)

// StatePath returns the path of the state document (comic_state.json).
// baseDir may be a local path or a gs:// URI.
func StatePath(baseDir string) (string, error) {
	return remoteio.Join(baseDir, DefaultStateJSON), nil
}

// PanelImagePath returns the path of a panel image.
// The path is stable for a panel ID, so a regeneration overwrites the same location.
func PanelImagePath(baseDir, panelID, extension string) (string, error) {
	fileName := panelFilePrefix + SanitizeFileName(panelID) + extension
	return remoteio.Join(baseDir, path.Join(DefaultImageDir, fileName)), nil
}

// PageImagePath returns the path of a page image (images/comic_page_{page}{extension}).
// page must be 1 or greater.
//
// extension is decided from the generated MIME type, as for panels and design sheets.
// When this one alone hardcoded ".png", a page image the model returned as JPEG was
// the only kind saved under an extension that contradicted its contents.
func PageImagePath(baseDir string, page int, extension string) (string, error) {
	base := remoteio.Join(baseDir, path.Join(DefaultImageDir, pageFileBaseName+extension))
	return remoteio.IndexedPath(base, page)
}

// DesignSheetPath returns the path of a design sheet
// (character/{tag}/{jobID}.ext). The tag is built from the combination of character IDs.
// Repeated generations for the same character are kept per jobID as history rather than
// overwriting one another.
func DesignSheetPath(baseDir string, characterIDs []string, jobID, extension string) (string, error) {
	relative := path.Join(CharacterDesignDir, DesignFileTag(characterIDs), SanitizeFileName(jobID)+extension)
	return remoteio.Join(baseDir, relative), nil
}

// DesignSheetJobID extracts the generation job ID from an object URI under
// CharacterDesignPrefix. A URI not directly under that prefix, or one with an
// intermediate level, yields ("", false).
//
// It is the inverse of DesignSheetPath. With only the builder exposed, a lister has to
// reverse-engineer the "file name is {jobID}{extension}" convention on its own, and
// when the naming changes here, the caller's listing turns empty without any error
// (a consumer actually had it written that way).
//
// The ID returned is the one after SanitizeFileName. DesignSheetPath saves it in that
// form, so the original string cannot be recovered.
func DesignSheetJobID(prefix, uri string) (string, bool) {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" || !strings.HasPrefix(uri, prefix+"/") {
		return "", false
	}

	name := strings.TrimPrefix(uri, prefix+"/")
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return strings.TrimSuffix(name, path.Ext(name)), true
}

// CharacterDesignPrefix returns the URI of the directory holding one character's design
// sheets, with a trailing slash. It is the prefix key a consumer uses to list or delete
// the generation history.
func CharacterDesignPrefix(baseDir, characterID string) (string, error) {
	return remoteio.Join(baseDir, path.Join(CharacterDesignDir, SanitizeFileName(characterID))) + "/", nil
}

// maxDesignFileTagBytes is the maximum byte length of the character tag embedded in a
// file name. It leaves room within the file system's file name limit (commonly 255
// bytes) for the directory, prefix and extension.
const maxDesignFileTagBytes = 100

// DesignFileTag builds a design-sheet directory name from character IDs.
// So that many or long IDs do not run into the file name limit, a tag over the limit is
// truncated at a rune boundary, and a checksum keeps the combination unique.
func DesignFileTag(characterIDs []string) string {
	tag := SanitizeFileName(strings.Join(characterIDs, "_"))
	if len(tag) <= maxDesignFileTagBytes {
		return tag
	}

	sum := crc32.ChecksumIEEE([]byte(tag))
	cut := tag[:maxDesignFileTagBytes]
	// If the byte-position cut split a multi-byte character, trim the tail to repair it
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return fmt.Sprintf("%s_%08x", cut, sum)
}

// fileNameSanitizer replaces characters that cannot be used in a file name.
var fileNameSanitizer = strings.NewReplacer(
	"/", "_",
	`\`, "_",
	":", "_",
	"*", "_",
	"?", "_",
	`"`, "_",
	"<", "_",
	">", "_",
	"|", "_",
)

// SanitizeFileName replaces characters that cannot be used in a file name with "_".
func SanitizeFileName(name string) string {
	return fileNameSanitizer.Replace(name)
}
