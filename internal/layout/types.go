// Package layout は、パネル・ページ単位での漫画画像の合成・レイアウト生成と、
// 参照アセットの事前アップロード管理を提供します。
package layout

import (
	"slices"
)

const (
	// DesignAspectRatio はキャラクターデザインシートの既定のアスペクト比です。
	// AspectRatio を指定せずに GenerateDesignSheet を呼んだ場合に使われます。
	DesignAspectRatio = "16:9"
	// PanelAspectRatio は単体パネル（1コマ）の推奨アスペクト比です。
	// ページ（PageAspectRatio）と揃えた縦長です。コマは縦長ページに積まれるため、
	// 横長で生成すると合成時に上下が切られるか、余白で埋めることになります。
	PanelAspectRatio = "3:4"
	// PageAspectRatio は統合ページ全体の推奨アスペクト比です。
	PageAspectRatio = "3:4"

	// ImageSize1K は標準的な解像度の設定（1024x1024相当）です。
	ImageSize1K = "1K"
	// ImageSize2K は高解像度の設定（2048x2048相当）です。
	ImageSize2K = "2K"
)

// designAspectRatios は GenerateDesignSheet が受け付けるデザインシートのアスペクト比です。
// キャラクターの参照画像（go-character-kit の ReferenceURLs）を、実際にその画像を使う先
// （go-veo-orchestrator のキーフレーム、ap-comp のカバーアート等）と同じアスペクト比で
// 用意できるようにするための選択肢で、ap-comp の coverArtAspectRatios を含みます。
//
// "3:4" はこのキット自身の消費先（PanelAspectRatio / PageAspectRatio）です。これが無いと、
// パネルとページの char.ReferenceURLFor は必ずアスペクト比なしの ReferenceURL へ落ち、
// 「生成対象と同じ比率の参照画像を使って細部のブレを抑える」仕組みが漫画側だけ働きません。
var designAspectRatios = []string{"1:1", "3:4", "9:16", "16:9"}

// IsDesignAspectRatio は、value がデザインシート生成でサポート対象のアスペクト比かどうかを
// 判定します。
func IsDesignAspectRatio(value string) bool {
	return slices.Contains(designAspectRatios, value)
}

// NormalizeDesignAspectRatio は、value がサポート対象でなければ DesignAspectRatio
// （既定値）にフォールバックします。
func NormalizeDesignAspectRatio(value string) string {
	if IsDesignAspectRatio(value) {
		return value
	}
	return DesignAspectRatio
}
