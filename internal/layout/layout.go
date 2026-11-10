// Package layout defines the vocabulary of aspect ratios and resolutions used for
// panel, page and design-sheet images.
package layout

import (
	"slices"
)

const (
	// DefaultAspectRatio is the default aspect ratio of panels, pages and design sheets.
	// It is portrait because panels are stacked onto portrait pages. Generating landscape
	// means the top and bottom get cut during composition, or the gap gets filled with
	// padding.
	//
	// Panels, pages and sheets used to have separate constants; that was dropped. Unless
	// all three match, the drift suppression from reference images is silently disabled,
	// so a mismatch is the abnormal case. When a sheet at a different ratio is needed for
	// a single run, specify it individually with DesignSheetRequest.AspectRatio.
	DefaultAspectRatio = "3:4"

	// ImageSize1K is the standard resolution setting (about 1024x1024).
	ImageSize1K = "1K"
	// ImageSize2K is the high resolution setting (about 2048x2048).
	ImageSize2K = "2K"
)

// aspectRatios are the accepted aspect ratios.
// They are the options that let a character's reference images (go-character-kit's
// ReferenceURLs) be prepared at the same aspect ratio as wherever the image is actually
// used (go-veo-orchestrator's keyframes, a music generation service's cover art, and so
// on), and they include the music generation service's cover-art ratio.
var aspectRatios = []string{"1:1", "3:4", "9:16", "16:9"}

// AspectRatios returns the list of accepted aspect ratios.
func AspectRatios() []string { return slices.Clone(aspectRatios) }

// IsAspectRatio reports whether value is a supported aspect ratio.
func IsAspectRatio(value string) bool {
	return slices.Contains(aspectRatios, value)
}
