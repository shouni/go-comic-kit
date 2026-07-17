package store

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

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
	original := &ports.MangaState{
		Version:   ports.StateSchemaVersion,
		ID:        "job-001",
		Title:     "夜明けのデプロイ",
		Chapters:  []ports.Chapter{{ID: "ch01", Title: "導入", Summary: "つかみ"}},
		Panels:    []ports.Panel{{ID: "ch01-p01", ChapterID: "ch01", Page: 1}},
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
	if !reflect.DeepEqual(original, restored) {
		t.Errorf("round trip mismatch:\noriginal: %+v\nrestored: %+v", original, restored)
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
