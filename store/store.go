// Package store は、MangaState（状態ドキュメント）の永続化を提供します。
// state は GCS またはローカルの comic_state.json として保存され、これが作品の
// 唯一の真実源になります。履歴一覧はアプリ側がこのファイル群を列挙して実現します。
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-comic-kit/asset"
	"github.com/shouni/go-comic-kit/ports"
)

// Load は指定パス（GCS URI またはローカルパス）から MangaState を読み込みます。
func Load(ctx context.Context, reader ports.ContentReader, statePath string) (*ports.MangaState, error) {
	rc, err := reader.Open(ctx, statePath)
	if err != nil {
		return nil, fmt.Errorf("state ファイルのオープンに失敗しました (%s): %w", statePath, err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "state ファイルのクローズに失敗しました", "error", closeErr)
		}
	}()

	state := &ports.MangaState{}
	if err := json.NewDecoder(rc).Decode(state); err != nil {
		return nil, fmt.Errorf("state JSON のパースに失敗しました (%s): %w", statePath, err)
	}
	if state.Version > ports.StateSchemaVersion {
		return nil, fmt.Errorf("state スキーマバージョン %d は未対応です（このライブラリの対応バージョン: %d）",
			state.Version, ports.StateSchemaVersion)
	}
	return state, nil
}

// Save は MangaState を outputDir 配下の comic_state.json として保存し、保存先パスを返します。
// 同名ファイルは上書きされます（state は唯一の真実源であり、常に最新を保持します）。
func Save(ctx context.Context, writer remoteio.Writer, state *ports.MangaState, outputDir string) (string, error) {
	if state == nil {
		return "", fmt.Errorf("state が nil です")
	}

	statePath, err := asset.ResolveOutputPath(outputDir, asset.DefaultStateJSON)
	if err != nil {
		return "", fmt.Errorf("state 保存パスの生成に失敗しました: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("state の JSON 変換に失敗しました: %w", err)
	}

	if err := writer.Write(ctx, statePath, bytes.NewReader(data),
		remoteio.WithContentType("application/json"),
	); err != nil {
		return "", fmt.Errorf("state の保存に失敗しました (path: %s): %w", statePath, err)
	}

	return statePath, nil
}
