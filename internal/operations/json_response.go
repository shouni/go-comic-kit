package operations

import (
	"fmt"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/go-comic-kit/ports"
)

// parseJSONResponse decodes the AI response into T.
//
// The empty check, repair, decoding and the excerpt in the error text belong to
// gemini.DecodeJSON. This only adds this kit's class (ErrGeneration, which a retry may
// fix) to the chain.
func parseJSONResponse[T any](raw string) (T, error) {
	parsed, err := gemini.DecodeJSON[T](raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%w: parse AI response JSON: %w", ports.ErrGeneration, err)
	}
	return parsed, nil
}
