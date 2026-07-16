// Package layout は、パネル・ページ単位での漫画画像の合成・レイアウト生成と、
// 参照アセットの事前アップロード管理を提供します。
package layout

import (
	"strings"
)

const (
	// DesignAspectRatio はキャラクターデザインシートの既定のアスペクト比です。
	// AspectRatio を指定せずに GenerateDesignSheet を呼んだ場合に使われます。
	DesignAspectRatio = "16:9"
	// PanelAspectRatio は単体パネル（1コマ）の推奨アスペクト比です。
	PanelAspectRatio = "16:9"
	// PageAspectRatio は統合ページ全体の推奨アスペクト比です。
	PageAspectRatio = "3:4"

	// ImageSize1K は標準的な解像度の設定（1024x1024相当）です。
	ImageSize1K = "1K"
	// ImageSize2K は高解像度の設定（2048x2048相当）です。
	ImageSize2K = "2K"
	// ImageSize4K は超高解像度の設定（4096x4096相当）です。
	ImageSize4K = "4K"
)

// IsGCSURI は、指定されたURIがGCS（Google Cloud Storage）のストレージURIであるかどうかを判定します。
func IsGCSURI(uri string) bool {
	const prefixGCS = "gs://"
	return strings.HasPrefix(uri, prefixGCS)
}

// designAspectRatios は GenerateDesignSheet が受け付けるデザインシートのアスペクト比です。
// キャラクターの参照画像（go-character-kit の ReferenceURLs）を、実際にその画像を使う先
// （go-veo-orchestrator のキーフレーム、ap-comp のカバーアート等）と同じアスペクト比で
// 用意できるようにするための選択肢で、ap-comp の coverArtAspectRatios と揃えています。
var designAspectRatios = []string{"1:1", "9:16", "16:9"}

// IsDesignAspectRatio は、value がデザインシート生成でサポート対象のアスペクト比かどうかを
// 判定します。
func IsDesignAspectRatio(value string) bool {
	for _, ratio := range designAspectRatios {
		if value == ratio {
			return true
		}
	}
	return false
}

// NormalizeDesignAspectRatio は、value がサポート対象でなければ DesignAspectRatio
// （既定値）にフォールバックします。
func NormalizeDesignAspectRatio(value string) string {
	if IsDesignAspectRatio(value) {
		return value
	}
	return DesignAspectRatio
}
