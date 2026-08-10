package ports

import "github.com/shouni/go-comic-kit/internal/layout"

// 画像の解像度と比率の語彙です。Config に文字列で持たせる以上、利用側が
// マジックストリングを書かずに済むよう公開します（内部の layout パッケージは
// internal なのでアプリからは参照できません）。

const (
	// ImageSize1K は標準的な解像度の設定（1024x1024相当）です。
	ImageSize1K = layout.ImageSize1K
	// ImageSize2K は高解像度の設定（2048x2048相当）です。
	ImageSize2K = layout.ImageSize2K

	// DefaultAspectRatio はパネル・ページ・デザインシートの既定の比率です。
	// 縦長なのは、コマが縦長のページへ積まれるためです。横長で生成すると
	// 合成時に上下が切られるか、余白で埋めることになります。
	DefaultAspectRatio = layout.DefaultAspectRatio
)

// AspectRatios は Config.AspectRatio と DesignSheetRequest.AspectRatio が
// 受け付ける比率の一覧を返します。
func AspectRatios() []string { return layout.AspectRatios() }

// IsAspectRatio は、value が受け付ける比率かどうかを判定します。
func IsAspectRatio(value string) bool { return layout.IsAspectRatio(value) }

// IsImageSize は、value が受け付ける解像度かどうかを判定します。
func IsImageSize(value string) bool { return value == ImageSize1K || value == ImageSize2K }
