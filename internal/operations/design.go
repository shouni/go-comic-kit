package operations

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/internal/layout"
	"github.com/shouni/go-comic-kit/ports"
)

// DesignSheetRunner runs character design sheet generation (the GenerateDesignSheet operation).
type DesignSheetRunner struct {
	prompt       ports.DesignSheetPrompt
	characters   *comic.Characters
	generator    ImageGenerator
	writer       remoteio.Writer
	cacheControl string
}

var _ ports.DesignSheetGenerator = (*DesignSheetRunner)(nil)

// DesignSheetRunnerArgs is the set of dependencies and settings needed to build a
// DesignSheetRunner. It has the same shape as PanelImageRunnerArgs / PageImageRunnerArgs.
// With positional arguments, unrelated strings sit side by side and still compile when
// swapped, and every added dependency shifts the order at the call sites.
type DesignSheetRunnerArgs struct {
	// Prompt is the ports.DesignSheetPrompt the app implements (the kit has no built-in one).
	Prompt     ports.DesignSheetPrompt
	Characters *comic.Characters
	Generator  ImageGenerator
	Writer     remoteio.Writer
	// CacheControl is the Cache-Control used when saving (ports.Config.CacheControl).
	CacheControl string
}

// NewDesignSheetRunner initializes the runner with its dependencies injected.
func NewDesignSheetRunner(args DesignSheetRunnerArgs) *DesignSheetRunner {
	return &DesignSheetRunner{
		prompt:       args.Prompt,
		characters:   args.Characters,
		generator:    args.Generator,
		writer:       args.Writer,
		cacheControl: args.CacheControl,
	}
}

// GenerateDesignSheet generates and saves a design sheet, records it on the state and
// returns the state. A nil state creates a new MangaState. With several characters it
// generates one combined sheet and records a DesignSheetRef to the same image for each
// character.
func (dr *DesignSheetRunner) GenerateDesignSheet(ctx context.Context, state *comic.MangaState, req ports.DesignSheetRequest) (*comic.MangaState, error) {
	if strings.TrimSpace(req.JobID) == "" {
		return nil, fmt.Errorf("%w: design sheet generation requires a job_id", ports.ErrInvalidRequest)
	}
	if err := requireModel(req.Model, "design sheet"); err != nil {
		return nil, err
	}
	aspectRatio, err := resolveAspectRatio(req.AspectRatio, layout.DefaultAspectRatio)
	if err != nil {
		return nil, err
	}
	imageSize, err := resolveImageSize(req.ImageSize, layout.ImageSize2K)
	if err != nil {
		return nil, err
	}

	imageURIs, descriptions, err := dr.collectCharacterURIs(ctx, req.CharacterIDs, req.Override, aspectRatio)
	if err != nil {
		return nil, fmt.Errorf("collect character assets: %w", err)
	}

	slog.InfoContext(ctx, "Executing design sheet generation",
		slog.Any("chars", req.CharacterIDs),
		slog.Int("ref_count", len(imageURIs)),
		slog.String("aspect_ratio", req.AspectRatio),
		slog.String("layout", req.Layout),
	)

	systemPrompt, userPrompt, negativePrompt, err := dr.prompt.BuildDesignSheet(&ports.DesignSheetPromptData{
		Descriptions: descriptions,
		Layout:       req.Layout,
		StyleMode:    req.StyleMode,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: build design sheet prompt: %w", ports.ErrGeneration, err)
	}

	record, err := renderImage(ctx, dr.generator, dr.writer, imageRenderRequest{
		Model:          req.Model,
		Prompt:         userPrompt,
		SystemPrompt:   systemPrompt,
		NegativePrompt: negativePrompt,
		AspectRatio:    aspectRatio,
		ImageSize:      imageSize,
		Seed:           designSeed(req.Seed),
		Images:         imageURIs,
		CacheControl:   dr.cacheControl,
		PathFor: func(mimeType string) (string, error) {
			return asset.DesignSheetPath(req.OutputDir, req.CharacterIDs, req.JobID, imagegen.ExtensionByMIMEType(mimeType))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("design sheet (%v): %w", req.CharacterIDs, err)
	}

	// Recording on the state is idempotent (a record for the same character is overwritten).
	now := time.Now().UTC()
	if state == nil {
		state = &comic.MangaState{
			Version:   comic.StateSchemaVersion,
			CreatedAt: now,
		}
	}
	for _, id := range req.CharacterIDs {
		state.SetDesignSheet(comic.DesignSheetRef{
			CharacterID: id,
			ImageURL:    record.ImageURL,
			UsedSeed:    record.UsedSeed,
		})
	}
	state.UpdatedAt = now

	return state, nil
}

// designSeed draws a new seed when none is specified (nil).
func designSeed(v *int64) *int64 {
	if v == nil {
		return newSeed()
	}
	return v
}

// collectCharacterURIs gathers the character information and returns the image URI
// slice and the descriptions. override applies only when ids has a single entry (not a
// combined design sheet).
//
// If a reference image exists at the same ratio as the sheet about to be drawn, it is
// used (the same ReferenceURLFor rule as panel.go / page.go). Drawing from a master at a
// different ratio puts compositional distortion into the sheet itself, which is the
// identity anchor of everything generated.
func (dr *DesignSheetRunner) collectCharacterURIs(ctx context.Context, ids []string, override ports.DesignOverride, aspectRatio string) ([]string, []string, error) {
	var uris []string
	var descriptions []string
	var missingIDs []string
	processedIDs := make(map[string]struct{})
	applyOverride := len(ids) == 1

	for _, id := range ids {
		if _, exists := processedIDs[id]; exists {
			continue
		}
		processedIDs[id] = struct{}{}

		char := dr.characters.GetCharacter(id)
		if char == nil {
			missingIDs = append(missingIDs, id)
			continue
		}

		referenceURL := char.ReferenceURLFor(aspectRatio)
		visualCues := char.VisualCues

		if applyOverride && strings.TrimSpace(override.ReferenceURL) != "" {
			referenceURL = override.ReferenceURL
		}
		if applyOverride && len(override.VisualCues) > 0 {
			visualCues = override.VisualCues
		}

		if referenceURL == "" {
			slog.WarnContext(ctx, "skipping character without a valid reference image", "id", id)
			continue
		}

		uris = append(uris, referenceURL)

		desc := char.Name
		if len(visualCues) > 0 {
			desc = fmt.Sprintf("%s (%s)", char.Name, strings.Join(visualCues, ", "))
		}
		descriptions = append(descriptions, desc)
	}

	if len(missingIDs) > 0 {
		return nil, nil, fmt.Errorf("%w: character ID %s", ports.ErrNotFound, strings.Join(missingIDs, ", "))
	}

	if len(uris) == 0 {
		return nil, nil, fmt.Errorf("%w: no character has a valid reference image (IDs: %s)", ports.ErrInvalidRequest, strings.Join(ids, ", "))
	}

	return uris, descriptions, nil
}
