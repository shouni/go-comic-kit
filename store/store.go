// Package store persists the MangaState (state document).
// The state is saved as comic_state.json on GCS or locally, and it is the work's single
// source of truth. History listings are built by the app enumerating these files.
//
// It reads and writes with encoding/json/v2, because unlike v1 it rejects duplicate
// member names by default; for the loader of the single source of truth, "say it is
// broken" is more correct than "silently read a broken state last-key-wins".
package store

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/ports"
)

// stateIndent is the indentation of the saved JSON (formatted so people can read diffs).
const stateIndent = "  "

// Load reads a MangaState from the given path (a GCS URI or a local path).
//
// Failures are classified with the ports sentinel errors. Whether the state is "not
// there yet", "broken" or "could not be read" changes the caller's response
// (404 / 400 / 502), so it has to be distinguishable with errors.Is, not by message.
func Load(ctx context.Context, reader ports.ContentReader, statePath string) (*comic.MangaState, error) {
	rc, err := reader.Open(ctx, statePath)
	if err != nil {
		// remoteio wraps not-found as os.ErrNotExist on local, GCS and S3 alike.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: state file (%s)", ports.ErrNotFound, statePath)
		}
		return nil, fmt.Errorf("%w: open state file (%s): %w",
			ports.ErrGeneration, statePath, err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "failed to close state file", "error", closeErr)
		}
	}()

	state := &comic.MangaState{}
	if err := json.UnmarshalRead(rc, state); err != nil {
		return nil, fmt.Errorf("%w: parse state JSON (%s): %w",
			ports.ErrInvalidRequest, statePath, err)
	}
	if state.Version > comic.StateSchemaVersion {
		return nil, fmt.Errorf("%w: state schema version %d is not supported (this library supports up to %d)",
			ports.ErrInvalidRequest, state.Version, comic.StateSchemaVersion)
	}
	return state, nil
}

// LoadIfExists is like Load, but returns (nil, nil) instead of an error when there is no
// state.
//
// It is the entry point for the "load if present, otherwise start fresh" path. If the
// caller makes that decision with err != nil, a transient read failure (ErrGeneration)
// or a broken state (ErrInvalidRequest) turns into "not there yet", and a freshly
// generated state overwrites the existing record. Only ErrNotFound means absent.
func LoadIfExists(ctx context.Context, reader ports.ContentReader, statePath string) (*comic.MangaState, error) {
	state, err := Load(ctx, reader, statePath)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	return state, err
}

// Save saves the MangaState as comic_state.json under outputDir and returns the path.
// An existing file of the same name is overwritten (the state is the single source of
// truth and always holds the latest).
func Save(ctx context.Context, writer remoteio.Writer, state *comic.MangaState, outputDir string) (string, error) {
	if state == nil {
		return "", fmt.Errorf("%w: state is nil", ports.ErrInvalidRequest)
	}

	statePath, err := asset.StatePath(outputDir)
	if err != nil {
		return "", fmt.Errorf("%w: build state path: %w", ports.ErrInvalidRequest, err)
	}

	// Building the path fails on the caller's argument (outputDir), which a retry does not
	// fix, so it is ErrInvalidRequest; the save itself usually fails transiently, so it is
	// ErrGeneration. The same split as renderImage.
	// FormatNilSliceAsNull matches v1's output (a nil slice is null). v2 defaults to [],
	// which is easier for consumers, but changing the format from already saved states as
	// a side effect of moving to json/v2 is not wanted, so it is pinned.
	var buf bytes.Buffer
	if err := json.MarshalWrite(&buf, state,
		jsontext.WithIndent(stateIndent),
		json.FormatNilSliceAsNull(true),
	); err != nil {
		return "", fmt.Errorf("%w: marshal state JSON: %w", ports.ErrInvalidRequest, err)
	}

	if err := writer.Write(ctx, statePath, bytes.NewReader(buf.Bytes()),
		remoteio.WithContentType("application/json"),
	); err != nil {
		return "", fmt.Errorf("%w: save state (path: %s): %w",
			ports.ErrGeneration, statePath, err)
	}

	return statePath, nil
}
