// Package store は、MangaState（状態ドキュメント）の永続化を提供します。
// state は GCS またはローカルの comic_state.json として保存され、これが作品の
// 唯一の真実源になります。履歴一覧はアプリ側がこのファイル群を列挙して実現します。
//
// 入出力には encoding/json/v2 を使います。v1 と違って重複したメンバー名を既定で
// 拒否するためで、唯一の真実源のロード口としては「壊れた state を黙って後勝ちで
// 読む」より「壊れていると言う」ほうが正しいからです。
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

// stateIndent は保存する JSON のインデントです（人が差分を読むため整形して書きます）。
const stateIndent = "  "

// Load は指定パス（GCS URI またはローカルパス）から MangaState を読み込みます。
//
// 失敗は ports の番兵エラーで分類して返します。state が「まだ無い」のか「壊れている」のか
// 「読めなかった」のかは呼び出し側の応答（404 / 400 / 502）が変わるところなので、
// メッセージではなく errors.Is で見分けられる必要があります。
func Load(ctx context.Context, reader ports.ContentReader, statePath string) (*comic.MangaState, error) {
	rc, err := reader.Open(ctx, statePath)
	if err != nil {
		// remoteio はローカル・GCS・S3 のいずれでも不存在を os.ErrNotExist で包みます。
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: state ファイル (%s)", ports.ErrNotFound, statePath)
		}
		return nil, fmt.Errorf("%w: state ファイルのオープンに失敗しました (%s): %w",
			ports.ErrGeneration, statePath, err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "state ファイルのクローズに失敗しました", "error", closeErr)
		}
	}()

	state := &comic.MangaState{}
	if err := json.UnmarshalRead(rc, state); err != nil {
		return nil, fmt.Errorf("%w: state JSON のパースに失敗しました (%s): %w",
			ports.ErrInvalidRequest, statePath, err)
	}
	if state.Version > comic.StateSchemaVersion {
		return nil, fmt.Errorf("%w: state スキーマバージョン %d は未対応です（このライブラリの対応バージョン: %d）",
			ports.ErrInvalidRequest, state.Version, comic.StateSchemaVersion)
	}
	return state, nil
}

// Save は MangaState を outputDir 配下の comic_state.json として保存し、保存先パスを返します。
// 同名ファイルは上書きされます（state は唯一の真実源であり、常に最新を保持します）。
func Save(ctx context.Context, writer remoteio.Writer, state *comic.MangaState, outputDir string) (string, error) {
	if state == nil {
		return "", fmt.Errorf("%w: state が nil です", ports.ErrInvalidRequest)
	}

	statePath, err := asset.StatePath(outputDir)
	if err != nil {
		return "", fmt.Errorf("%w: state 保存パスの生成に失敗しました: %w", ports.ErrInvalidRequest, err)
	}

	// パス生成は呼び出し側の引数（outputDir）が原因なので再試行しても直らず ErrInvalidRequest、
	// 保存そのものは一時的な失敗が多いので ErrGeneration。renderImage と同じ分け方です。
	// FormatNilSliceAsNull は v1 の出力（nil スライスは null）に揃えるためのものです。
	// v2 の既定は [] で、消費側にとってはそちらが扱いやすいのですが、既に保存済みの
	// state と書式が変わるのを json/v2 への移行のついでに起こしたくないので固定します。
	var buf bytes.Buffer
	if err := json.MarshalWrite(&buf, state,
		jsontext.WithIndent(stateIndent),
		json.FormatNilSliceAsNull(true),
	); err != nil {
		return "", fmt.Errorf("%w: state の JSON 変換に失敗しました: %w", ports.ErrInvalidRequest, err)
	}

	if err := writer.Write(ctx, statePath, bytes.NewReader(buf.Bytes()),
		remoteio.WithContentType("application/json"),
	); err != nil {
		return "", fmt.Errorf("%w: state の保存に失敗しました (path: %s): %w",
			ports.ErrGeneration, statePath, err)
	}

	return statePath, nil
}
