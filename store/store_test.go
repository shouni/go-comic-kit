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

// errReader は Open を必ず失敗させる ContentReader です。
type errReader struct{ err error }

func (r *errReader) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, r.err
}

// TestLoadClassifiesFailures は、Load の失敗が ports の番兵で分類されることを
// 確認します。state が「まだ無い」「壊れている」「読めなかった」は消費側の応答
// （404 / 400 / 502）が変わるところなので、メッセージではなく errors.Is で
// 見分けられなければなりません。
func TestLoadClassifiesFailures(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		reader ports.ContentReader
		want   error
	}{
		"missing": {
			// remoteio はローカル・GCS・S3 のいずれでも不存在を os.ErrNotExist で包む。
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
			// json/v2 の既定は重複拒否。v1 は後勝ちで黙って読んでいた。
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

// TestLoadIfExistsOnlyTreatsNotFoundAsAbsent は、「無い」と言えるのが ErrNotFound の
// ときだけであることを確認します。読み取り失敗や壊れた state まで「無い」に化けると、
// 呼び出し側が新規に生成した状態で既存の記録を上書きします。
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

// TestSaveClassifiesWriteFailure は、保存の失敗が再試行の価値がある分類
// （ErrGeneration）で返ることを確認します。
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
