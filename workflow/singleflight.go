package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"

	"github.com/shouni/go-comic-kit/internal/operations"
	"github.com/shouni/go-comic-kit/internal/reference"
)

// This file holds the decorators that put a call guard over the kit's interfaces
// (operations.ImageGenerator / StructuredGenerator), and the code that builds keys from
// request contents. Call spacing, time limits and deduplication of concurrent calls
// themselves live in callguard, which shares one implementation with
// go-veo-orchestrator and genai-kit/lyria.
//
// resolvingImageGenerator, which resolves reference images, is a decorator of the same shape.

// singleflightImageGenerator is an ImageGenerator decorator that collapses concurrent
// identical image generation requests into one call.
// It protects the expensive image generation API from duplicate calls caused by Cloud
// Tasks' at-least-once delivery and MCP client retries. Only in-flight calls within the
// process are covered; durable deduplication is the job side's idempotency through the
// state's GenerationRecord.
type singleflightImageGenerator struct {
	inner operations.ImageGenerator
	guard *callguard.Guard
	group callguard.Group
}

var _ operations.ImageGenerator = (*singleflightImageGenerator)(nil)

// Generate collapses concurrent calls keyed by a hash of the request contents.
// The shared response is cloned for each caller.
func (g *singleflightImageGenerator) Generate(ctx context.Context, req operations.ImageRequest) (*operations.ImageResponse, error) {
	key := imageRequestKey(&req)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*operations.ImageResponse, error) {
		return g.inner.Generate(execCtx, req)
	})
	if err != nil {
		return nil, err
	}
	return cloneImageResponse(resp), nil
}

// singleflightStructuredGenerator is a StructuredGenerator decorator that collapses
// concurrent identical text generation requests into one call.
type singleflightStructuredGenerator struct {
	inner operations.StructuredGenerator
	guard *callguard.Guard
	group callguard.Group
}

var _ operations.StructuredGenerator = (*singleflightStructuredGenerator)(nil)

// Generate collapses concurrent calls keyed by a hash of the request contents.
func (g *singleflightStructuredGenerator) Generate(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	key := structuredRequestKey(modelName, prompt, attachments, &opts)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*gemini.Response, error) {
		return g.inner.Generate(execCtx, modelName, prompt, attachments, opts)
	})
	if err != nil {
		return nil, err
	}
	// NOTE: this returns a shallow copy, assuming the caller (operations) reads only Text.
	// If uses that modify gemini.Response's reference-type fields appear, switch to a deep copy.
	cloned := *resp
	return &cloned, nil
}

// imageRequestKey builds the singleflight key from an image generation request.
//
// Reference images are keyed by their unresolved URLs. Keyed by resolved bytes, calls for
// the same URL would each finish fetching before they could merge, defeating the
// deduplication.
func imageRequestKey(req *operations.ImageRequest) string {
	parts := []string{
		req.Model,
		req.Prompt,
		req.SystemPrompt,
		req.NegativePrompt,
		req.AspectRatio,
		req.ImageSize,
		callguard.SeedKey(req.Seed),
	}
	parts = append(parts, req.Images...)
	return callguard.Key("image", parts...)
}

// structuredRequestKey builds the singleflight key from a text generation request.
//
// An attachment is either a URI or bytes, so the key includes the URI as is, and a hash
// of the contents for bytes. Using only the length would give two different images of
// the same size the same key.
//
// Of GenerateOptions, only ResponseMIMEType and Seed go into the key. ResponseJSONSchema
// can be left out because the text generation callers (chapter.go / outline.go) pass the
// argument-free chapterScriptSchema / outlineSchema, fixed per call site. If the schema
// starts varying per call, add it to the key; otherwise concurrent calls with the same
// prompt and different schemas share one result. SystemPrompt is used only on the image
// path, where imageRequestKey includes it.
func structuredRequestKey(modelName string, prompt string, attachments []gemini.Attachment, opts *gemini.GenerateOptions) string {
	keyParts := []string{modelName, opts.ResponseMIMEType, callguard.SeedKey(opts.Seed), prompt}
	for _, attachment := range attachments {
		keyParts = append(keyParts, attachment.MIMEType, attachment.URI)
		if len(attachment.Data) > 0 {
			sum := sha256.Sum256(attachment.Data)
			keyParts = append(keyParts, hex.EncodeToString(sum[:]))
		}
	}
	return callguard.Key("structured", keyParts...)
}

// cloneImageResponse copies a response shared through singleflight so each caller can use it safely.
func cloneImageResponse(src *operations.ImageResponse) *operations.ImageResponse {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Data = append([]byte(nil), src.Data...)
	return &dst
}

// resolvingImageGenerator is a decorator that converts unresolved reference image URLs
// into sendable attachments before handing the request to image generation.
//
// The conversion lives here rather than in the operations layer because it involves I/O
// (the http(s) fetch). The operations layer is confined to prompt assembly and deciding
// where things are saved.
type resolvingImageGenerator struct {
	inner    imagegen.Generator
	resolver *reference.Resolver
}

var _ operations.ImageGenerator = (*resolvingImageGenerator)(nil)

// Generate resolves the reference images, then runs image generation.
func (g *resolvingImageGenerator) Generate(ctx context.Context, req operations.ImageRequest) (*operations.ImageResponse, error) {
	references, err := g.resolver.Resolve(ctx, req.Images)
	if err != nil {
		return nil, err
	}

	return g.inner.Generate(ctx, imagegen.Request{
		Model:           req.Model,
		Prompt:          req.Prompt,
		NegativePrompt:  req.NegativePrompt,
		References:      references,
		GenerateOptions: req.GenerateOptions,
	})
}
