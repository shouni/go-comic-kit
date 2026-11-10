package operations

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

// ImageRequest is the input of one image generation.
//
// It does not use imagegen.Request as is because Images holds something different.
// imagegen takes resolved references (a gs:// URI or fetched bytes), while this layer
// holds unresolved URLs, which need a fetch for http(s). Resolution is taken on by the
// workflow layer's resolvingImageGenerator.
//
// Generation parameters are held by embedding gemini.GenerateOptions. Copying them would
// need two places kept in sync every time gemini gains a field — the same reason
// imagegen.Request itself embeds it. Go 1.27 allows a promoted field as a composite
// literal key, so callers can write Model: / AspectRatio: flat.
type ImageRequest struct {
	// Model is the model name used for generation.
	Model string
	// Prompt is the generation instruction; NegativePrompt is what should not appear.
	Prompt         string
	NegativePrompt string
	// Images are the reference image URLs. gs:// and http(s):// can be mixed.
	// The order is kept, because it affects how the model reads them.
	Images []string

	gemini.GenerateOptions
}

// ImageResponse is the result of one image generation (an alias of imagegen.Response).
type ImageResponse = imagegen.Response

// ImageGenerator is the dependency interface that generates an image with zero or more
// reference images attached. All three operations — design sheet, panel and page — go
// through it.
//
// Single and fused generation are the same call; the number of references is told apart
// only by the length of ImageRequest.Images. It does not use imagegen.Generator as is
// because the reference images are unresolved URLs, and so that test fakes stay within
// this package.
type ImageGenerator interface {
	Generate(ctx context.Context, req ImageRequest) (*ImageResponse, error)
}

// writeGeneratedImage writes the generated image data to the given path with
// Content-Type and Cache-Control. An empty cacheControl uses the default.
func writeGeneratedImage(ctx context.Context, writer remoteio.Writer, path string, resp *ImageResponse, cacheControl string) error {
	if cacheControl == "" {
		cacheControl = ports.DefaultCacheControl
	}
	return writer.Write(ctx, path, bytes.NewReader(resp.Data),
		remoteio.WithContentType(resp.MIMEType),
		remoteio.WithCacheControl(cacheControl),
	)
}

// imageRenderRequest is an image generation input plus how to save the result.
type imageRenderRequest struct {
	ImageRequest
	// CacheControl is the Cache-Control set when saving (empty means ports.DefaultCacheControl).
	CacheControl string
	// PathFor decides the save path from the generated MIME type.
	// The extension depends on the MIME type, so it can only be decided after generation.
	PathFor func(mimeType string) (string, error)
}

// renderImage runs the flow shared by panel generation, page composition and design
// sheet generation: generate → save → build the generation record. Callers are left with
// only assembling the input and deciding where to save.
//
// Every failure carries a ports sentinel error. Saving and path building are not AI
// calls, but a bare fmt.Errorf would be invisible to the caller's errors.Is
// classification (see ports/errors.go). A path-building failure is caused by the
// arguments (OutputDir and so on) and a retry does not fix it, so it is
// ErrInvalidRequest; a save failure is usually transient, so it is ErrGeneration.
func renderImage(ctx context.Context, generator ImageGenerator, writer remoteio.Writer, req imageRenderRequest) (*comic.GenerationRecord, error) {
	resp, err := generator.Generate(ctx, req.ImageRequest)
	if err != nil {
		return nil, fmt.Errorf("%w: generate image: %w", ports.ErrGeneration, err)
	}

	finalPath, err := req.PathFor(resp.MIMEType)
	if err != nil {
		return nil, fmt.Errorf("%w: build image path: %w", ports.ErrInvalidRequest, err)
	}
	if err := writeGeneratedImage(ctx, writer, finalPath, resp, req.CacheControl); err != nil {
		return nil, fmt.Errorf("%w: save image (path: %s): %w", ports.ErrGeneration, finalPath, err)
	}

	return &comic.GenerationRecord{
		ImageURL:       finalPath,
		UsedSeed:       resp.UsedSeed,
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		Model:          req.Model,
		GeneratedAt:    time.Now().UTC(),
	}, nil
}
