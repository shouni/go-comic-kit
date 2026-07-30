package ports

import "errors"

// 本ファイルは、操作が返すエラーを呼び出し側が分類するための番兵エラーを定義します。
// 各操作は状況に応じた説明的なメッセージにこれらを %w で包んで返すため、消費側は
// errors.Is で分類だけを見て応答（HTTP ステータス等）を決められます。
//
//	switch {
//	case errors.Is(err, ports.ErrNotFound):       // 404
//	case errors.Is(err, ports.ErrInvalidRequest): // 400
//	case errors.Is(err, ports.ErrGeneration):     // 502（再試行の余地あり）
//	}
var (
	// ErrNotFound は、指定された章・パネル・ページが state に存在しないことを表します。
	// 呼び出し側の指定ミスであり、同じ引数で再試行しても成功しません。
	ErrNotFound = errors.New("対象が見つかりません")

	// ErrInvalidRequest は、リクエストの内容が不正であることを表します
	// （必須項目の欠落、編集対象の画像が未生成、参照画像を持つキャラクターが皆無など）。
	// こちらも同じ引数での再試行では解決しません。
	ErrInvalidRequest = errors.New("リクエストが不正です")

	// ErrGeneration は、AI 呼び出しそのものか、その応答の解釈に失敗したことを表します。
	// 一時的な失敗であることが多く、再試行の価値があります。
	ErrGeneration = errors.New("生成に失敗しました")
)
