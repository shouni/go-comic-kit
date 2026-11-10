package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/shouni/go-comic-kit/comic"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/ports"
)

type memWriter struct {
	lastPath string
	data     []byte
}

func (w *memWriter) Write(_ context.Context, path string, r io.Reader, _ ...remoteio.WriteOption) error {
	w.lastPath = path
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	w.data = data
	return nil
}

type memReader struct {
	data []byte
}

func (r *memReader) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.data)), nil
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	original := &comic.MangaState{
		Version:   comic.StateSchemaVersion,
		ID:        "job-001",
		Title:     "夜明けのデプロイ",
		Chapters:  []comic.Chapter{{ID: "ch01", Title: "導入", Summary: "つかみ"}},
		Panels:    []comic.Panel{{ID: "ch01-p01", ChapterID: "ch01", Page: 1}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	writer := &memWriter{}
	path, err := Save(context.Background(), writer, original, "gs://bucket/works/job-001")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if !strings.HasSuffix(path, "comic_state.json") || writer.lastPath != path {
		t.Errorf("path = %q, want comic_state.json under output dir", path)
	}

	restored, err := Load(context.Background(), &memReader{data: writer.data}, path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if diff := cmp.Diff(original, restored); diff != "" {
		t.Errorf("round trip mismatch (-original +restored):\n%s", diff)
	}
}

func TestLoadRejectsNewerSchemaVersion(t *testing.T) {
	t.Parallel()

	reader := &memReader{data: []byte(`{"version": 999, "id": "x", "panels": []}`)}
	if _, err := Load(context.Background(), reader, "comic_state.json"); err == nil {
		t.Error("Load with newer schema version succeeded, want error")
	}
}

func TestSaveNilStateFails(t *testing.T) {
	t.Parallel()

	if _, err := Save(context.Background(), &memWriter{}, nil, "out"); err == nil {
		t.Error("Save(nil) succeeded, want error")
	}
}

// errReader is a ContentReader whose Open always fails.
type errReader struct{ err error }

func (r *errReader) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, r.err
}

// TestLoadClassifiesFailures checks that Load's failures are classified with the ports
// sentinels. "Not there yet", "broken" and "could not be read" change the consumer's
// response (404 / 400 / 502), so they must be distinguishable with errors.Is, not by
// message.
func TestLoadClassifiesFailures(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		reader ports.ContentReader
		want   error
	}{
		"missing": {
			// remoteio wraps not-found as os.ErrNotExist on local, GCS and S3 alike.
			reader: &errReader{err: fmt.Errorf("open failed: %w", fs.ErrNotExist)},
			want:   ports.ErrNotFound,
		},
		"unreachable": {
			reader: &errReader{err: errors.New("connection reset")},
			want:   ports.ErrGeneration,
		},
		"broken json": {
			reader: &memReader{data: []byte(`{"version": 1, "id":`)},
			want:   ports.ErrInvalidRequest,
		},
		"duplicate member": {
			// json/v2 rejects duplicates by default. v1 silently read them last-key-wins.
			reader: &memReader{data: []byte(`{"id":"a","id":"b"}`)},
			want:   ports.ErrInvalidRequest,
		},
		"newer schema": {
			reader: &memReader{data: []byte(`{"version": 99}`)},
			want:   ports.ErrInvalidRequest,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(t.Context(), tc.reader, "gs://bucket/works/x/comic_state.json")
			if !errors.Is(err, tc.want) {
				t.Errorf("Load() error = %v, want errors.Is(..., %v)", err, tc.want)
			}
		})
	}
}

// TestLoadIfExistsOnlyTreatsNotFoundAsAbsent checks that only ErrNotFound means
// "absent". If a read failure or a broken state turned into "absent", the caller would
// overwrite the existing record with a freshly generated state.
func TestLoadIfExistsOnlyTreatsNotFoundAsAbsent(t *testing.T) {
	t.Parallel()

	t.Run("missing is nil, nil", func(t *testing.T) {
		t.Parallel()
		state, err := LoadIfExists(t.Context(), &errReader{err: fmt.Errorf("open failed: %w", fs.ErrNotExist)}, "gs://b/x/comic_state.json")
		if err != nil || state != nil {
			t.Errorf("LoadIfExists() = (%v, %v), want (nil, nil)", state, err)
		}
	})

	t.Run("present is loaded", func(t *testing.T) {
		t.Parallel()
		state, err := LoadIfExists(t.Context(), &memReader{data: []byte(`{"version": 1, "id": "w1"}`)}, "gs://b/x/comic_state.json")
		if err != nil || state == nil || state.ID != "w1" {
			t.Errorf("LoadIfExists() = (%+v, %v), want the stored state", state, err)
		}
	})

	for name, tc := range map[string]struct {
		reader ports.ContentReader
		want   error
	}{
		"unreachable stays an error": {&errReader{err: errors.New("connection reset")}, ports.ErrGeneration},
		"broken json stays an error": {&memReader{data: []byte(`{"version": 1, "id":`)}, ports.ErrInvalidRequest},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state, err := LoadIfExists(t.Context(), tc.reader, "gs://b/x/comic_state.json")
			if !errors.Is(err, tc.want) || state != nil {
				t.Errorf("LoadIfExists() = (%v, %v), want (nil, errors.Is(..., %v))", state, err, tc.want)
			}
		})
	}
}

// TestSaveClassifiesWriteFailure checks that a save failure comes back in the class worth
// retrying (ErrGeneration).
func TestSaveClassifiesWriteFailure(t *testing.T) {
	t.Parallel()

	writer := &failingWriter{err: errors.New("503 backend error")}
	_, err := Save(t.Context(), writer, &comic.MangaState{Version: comic.StateSchemaVersion}, "gs://bucket/out")
	if !errors.Is(err, ports.ErrGeneration) {
		t.Errorf("Save() error = %v, want errors.Is(..., ports.ErrGeneration)", err)
	}
}

type failingWriter struct{ err error }

func (w *failingWriter) Write(_ context.Context, _ string, _ io.Reader, _ ...remoteio.WriteOption) error {
	return w.err
}
