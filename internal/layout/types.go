// Package layout は、パネル・ページ単位での漫画画像の合成・レイアウト生成と、
// 参照アセットの事前アップロード管理を提供します。
package layout

import (
	"slices"
)

const (
	// DefaultAspectRatio はパネル・ページ・デザインシートの既定のアスペクト比です。
	// 縦長なのは、コマが縦長のページへ積まれるためです。横長で生成すると合成時に
	// 上下が切られるか、余白で埋めることになります。
	//
	// パネル・ページ・シートで別々の定数を持っていたのをやめました。3つが揃って
	// いないと参照画像によるブレ抑制が黙って無効になるので、揃っていないほうが
	// 異常だからです。1回だけ別比率のシートが要る場合は
	// DesignSheetRequest.AspectRatio で個別に指定します。
	DefaultAspectRatio = "3:4"

	// ImageSize1K は標準的な解像度の設定（1024x1024相当）です。
	ImageSize1K = "1K"
	// ImageSize2K は高解像度の設定（2048x2048相当）です。
	ImageSize2K = "2K"
)

// aspectRatios は受け付けるアスペクト比です。
// キャラクターの参照画像（go-character-kit の ReferenceURLs）を、実際にその画像を使う先
// （go-veo-orchestrator のキーフレーム、ap-comp のカバーアート等）と同じアスペクト比で
// 用意できるようにするための選択肢で、ap-comp の coverArtAspectRatios を含みます。
var aspectRatios = []string{"1:1", "3:4", "9:16", "16:9"}

// AspectRatios は受け付けるアスペクト比の一覧を返します。
func AspectRatios() []string { return slices.Clone(aspectRatios) }

// IsAspectRatio は、value がサポート対象のアスペクト比かどうかを判定します。
func IsAspectRatio(value string) bool {
	return slices.Contains(aspectRatios, value)
}
