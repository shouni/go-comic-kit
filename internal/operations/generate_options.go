package operations

import (
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/go-comic-kit/ports"
)

// StructuredGenerator is the dependency interface for text generation with structured output options.
type StructuredGenerator = gemini.Generator

// requireModel validates the model name specified per call.
func requireModel(model, operation string) error {
	if model == "" {
		return fmt.Errorf("%w: no model name specified for %s", ports.ErrInvalidRequest, operation)
	}
	return nil
}

// resolveAspectRatio validates and returns the per-call ratio. Empty yields fallback.
func resolveAspectRatio(requested, fallback string) (string, error) {
	if requested == "" {
		return fallback, nil
	}
	if !ports.IsAspectRatio(requested) {
		return "", fmt.Errorf("%w: AspectRatio (%q) must be one of %s",
			ports.ErrInvalidRequest, requested, strings.Join(ports.AspectRatios(), " / "))
	}
	return requested, nil
}

// resolveImageSize validates and returns the per-call resolution. Empty yields fallback.
func resolveImageSize(requested, fallback string) (string, error) {
	if requested == "" {
		return fallback, nil
	}
	if !ports.IsImageSize(requested) {
		return "", fmt.Errorf("%w: ImageSize (%q) must be one of %s / %s",
			ports.ErrInvalidRequest, requested, ports.ImageSize1K, ports.ImageSize2K)
	}
	return requested, nil
}

// buildJSONGenerateOptions returns the generation options for script generation:
// structured output (constrained decoding) by schema, with the safety filters disabled.
func buildJSONGenerateOptions(schema map[string]any) gemini.GenerateOptions {
	return gemini.GenerateOptions{
		ResponseMIMEType:   "application/json",
		ResponseJSONSchema: schema,
		SafetySettings:     gemini.NewSafetySettings(gemini.SafetyBlockNone),
	}
}
