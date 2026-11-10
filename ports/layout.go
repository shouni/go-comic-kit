package ports

import "github.com/shouni/go-comic-kit/internal/layout"

// The vocabulary of image resolutions and aspect ratios. Both are passed as per-call
// values, so they are exported here to spare callers from writing magic strings (the
// underlying internal/layout is internal, so apps cannot reference it).

const (
	// ImageSize1K is the standard resolution setting (about 1024x1024).
	ImageSize1K = layout.ImageSize1K
	// ImageSize2K is the high resolution setting (about 2048x2048).
	ImageSize2K = layout.ImageSize2K

	// DefaultAspectRatio is the default ratio of panels, pages and design sheets.
	// It is portrait because panels are stacked onto portrait pages.
	DefaultAspectRatio = layout.DefaultAspectRatio
)

// AspectRatios returns the list of ratios accepted by the AspectRatio field of
// GenerateOptions / BatchOptions / DesignSheetRequest.
func AspectRatios() []string { return layout.AspectRatios() }

// IsAspectRatio reports whether value is an accepted ratio.
func IsAspectRatio(value string) bool { return layout.IsAspectRatio(value) }

// IsImageSize reports whether value is an accepted resolution.
func IsImageSize(value string) bool { return value == ImageSize1K || value == ImageSize2K }
