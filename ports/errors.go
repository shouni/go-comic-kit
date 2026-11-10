package ports

import "errors"

// This file defines the sentinel errors a caller uses to classify the errors operations
// return. Each operation wraps one of them with %w in a descriptive message, so a
// consumer can decide its response (HTTP status and so on) by looking only at the class
// with errors.Is.
//
//	switch {
//	case errors.Is(err, ports.ErrNotFound):       // 404
//	case errors.Is(err, ports.ErrInvalidRequest): // 400
//	case errors.Is(err, ports.ErrGeneration):     // 502 (worth retrying)
//	}
var (
	// ErrNotFound means the specified chapter, panel or page does not exist in the state.
	// It is the caller's mistake, and retrying with the same arguments does not succeed.
	ErrNotFound = errors.New("comic: target not found")

	// ErrInvalidRequest means the request is invalid (a missing required field, an edit
	// target image not generated yet, no character with a reference image, and so on).
	// Retrying with the same arguments does not fix it either.
	ErrInvalidRequest = errors.New("comic: request is invalid")

	// ErrGeneration means the AI call itself, or interpreting its response, failed.
	// It is often transient and worth retrying.
	ErrGeneration = errors.New("comic: generation failed")

	// ErrConfigInvalid is a class reserved for a misconfiguration by the assembler (a
	// defect in Config, not in a request).
	//
	// Nothing returns it today. What remains in Config is only the run control that keeps
	// the kit from breaking, and ApplyDefaults fills all of it (see Config.Validate).
	// Model names, art direction, aspect ratio and resolution are per-call values, which
	// each operation rejects with ErrInvalidRequest before running. If required fields
	// return to Config, have Validate wrap this with %w.
	ErrConfigInvalid = errors.New("comic: config is invalid")
)
