// Package ports defines the contracts (interfaces) of go-comic-kit's operations, with
// their inputs, outputs and configuration. The data model itself lives in the comic
// package.
package ports

import (
	"time"

	"github.com/shouni/go-comic-kit/comic"
)

// Default values
const (
	// DefaultMaxConcurrency is the default maximum parallelism of batch generation
	// (GenerateAllPanels / ComposeAllPages). The default is 1 so that, until it is raised
	// explicitly, execution stays sequential as before and API quota is consumed the same
	// way.
	DefaultMaxConcurrency = 1
	// DefaultRequestTimeout is the default time limit of one external AI call.
	// Image generation can take tens of seconds, so it leaves headroom.
	DefaultRequestTimeout = 5 * time.Minute
	// DefaultMaxChapters is the default chapter limit of outline generation (GenerateOutline).
	DefaultMaxChapters = 8
	// DefaultMaxPanelsPerChapter is the default panel limit of per-chapter script generation.
	DefaultMaxPanelsPerChapter = 8

	// DefaultCacheControl is the default Cache-Control used when saving generated images.
	// "public" assumes the artifacts may be served publicly, so a deployment writing to a
	// private bucket should set Config.CacheControl to "private" or similar.
	DefaultCacheControl = "public, max-age=1800"
)

// Config is the basic configuration that runs Go Comic Kit's operations.
type Config struct {
	// --- Generation Settings ---
	// MaxConcurrency is the maximum parallelism of batch generation (GenerateAllPanels /
	// ComposeAllPages). Single-panel and single-page operations only ever run one
	// generation, so they are unaffected.
	MaxConcurrency int
	// RateInterval is the minimum spacing between AI calls (0 means unlimited). Text and
	// image generation share one limiter, because quota is per project.
	// Note that throughput is capped by 1/RateInterval, not by MaxConcurrency
	// (for example, RateInterval=10s allows at most 6 calls a minute at any parallelism).
	RateInterval time.Duration
	// CacheControl is the Cache-Control used when saving generated images.
	// Empty uses DefaultCacheControl (public, max-age=1800).
	CacheControl string

	// --- Script Settings ---
	// MaxChapters is the chapter limit of outline generation.
	MaxChapters int
	// MaxPanelsPerChapter is the panel limit of per-chapter script generation.
	MaxPanelsPerChapter int

	// --- Layout Settings ---
	// MaxPanelsPerPage is the maximum number of panels on one page (passed to Repaginate).
	// A new page always starts where the chapter changes, even below this number.
	MaxPanelsPerPage int

	// --- Timeout & Retries ---
	// RequestTimeout is the time limit of one external AI call (applied to text
	// generation, image generation and reference image uploads). It does not bound the
	// whole sequence of steps, so make it long enough for the slowest single generation.
	// 0 or less means DefaultRequestTimeout.
	RequestTimeout time.Duration
}

// ApplyDefaults fills unset (zero-value) fields with their defaults.
func (c *Config) ApplyDefaults() {
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = DefaultMaxConcurrency
	}
	if c.MaxChapters <= 0 {
		c.MaxChapters = DefaultMaxChapters
	}
	if c.MaxPanelsPerChapter <= 0 {
		c.MaxPanelsPerChapter = DefaultMaxPanelsPerChapter
	}
	if c.MaxPanelsPerPage <= 0 {
		c.MaxPanelsPerPage = comic.DefaultMaxPanelsPerPage
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
}

// Validate checks that the configuration is valid.
//
// Currently it rejects nothing (see ErrConfigInvalid for why). The hook that workflow.New
// calls is kept as the place for checks if required fields return to the configuration.
func (c *Config) Validate() error {
	return nil
}
