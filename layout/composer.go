package layout

import (
	"context"
	"fmt"
	"sync"

	imagePorts "github.com/shouni/gemini-image-kit/ports"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/shouni/go-comic-kit/ports"
)

// ComicComposer は、キャラクター・パネル等の参照アセットの事前アップロードと
// アップロード済み URI の解決を担います。
//
// Gemini API モードでは File API へアップロードした URI を、Vertex AI モードでは
// GCS URI の直接参照（アップロード省略）を提供します。同一 URL の二重アップロードは
// singleflight で防止します。
type ComicComposer struct {
	AssetManager    imagePorts.AssetManager
	BackendProvider imagePorts.Backend
	CharactersMap   *ports.Characters
	resourceMap     resourceMap
	mu              sync.RWMutex
	uploadGroup     singleflight.Group
}

type resourceMap struct {
	character map[string]string // ReferenceURL -> FileAPIURI
	panel     map[string]string // ReferenceURL -> FileAPIURI
}

// NewComicComposer は ComicComposer の新しいインスタンスを初期化済みの状態で生成します。
func NewComicComposer(
	assetMgr imagePorts.AssetManager,
	backend imagePorts.Backend,
	cm *ports.Characters,
) (*ComicComposer, error) {
	if assetMgr == nil {
		return nil, fmt.Errorf("assetMgr is required")
	}
	if backend == nil {
		return nil, fmt.Errorf("backend is required")
	}

	return &ComicComposer{
		AssetManager:    assetMgr,
		BackendProvider: backend,
		CharactersMap:   cm,
		resourceMap: resourceMap{
			character: make(map[string]string),
			panel:     make(map[string]string),
		},
	}, nil
}

// GetCharacterResourceURI はキャラクターの既定参照画像（ReferenceURL）の画像URIを取得します。
// アスペクト比別の参照画像（ReferenceURLs）を取得するには GetCharacterResourceURIFor を使って
// ください。
func (mc *ComicComposer) GetCharacterResourceURI(charID string) string {
	char := mc.CharactersMap.GetCharacterWithDefault(charID)
	if char == nil {
		return ""
	}
	return mc.getReferenceResourceURI(char.ReferenceURL)
}

// GetCharacterResourceURIFor は、指定したキャラクターの aspectRatio に一致する参照画像
// （ReferenceURLs）があればその画像URIを、無ければ既定の ReferenceURL の画像URIを取得します。
func (mc *ComicComposer) GetCharacterResourceURIFor(charID, aspectRatio string) string {
	char := mc.CharactersMap.GetCharacterWithDefault(charID)
	if char == nil {
		return ""
	}
	return mc.getReferenceResourceURI(char.ReferenceURLFor(aspectRatio))
}

func (mc *ComicComposer) getReferenceResourceURI(referenceURL string) string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.resourceMap.character[referenceURL]
}

// GetPanelResourceURI はパネルの画像URIを取得します。
func (mc *ComicComposer) GetPanelResourceURI(referenceURL string) string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.resourceMap.panel[referenceURL]
}

// PrepareCharacterResources は state に登場する全キャラクター（参照画像添付対象、
// ProminenceBackground を除く）の参照画像を事前アップロードします。
// 各キャラクターの ReferenceURL（既定のフォールバック）と ReferenceURLs（アスペクト比別）の
// 両方に含まれる参照画像URLをすべて対象にします。
func (mc *ComicComposer) PrepareCharacterResources(ctx context.Context, state *ports.MangaState) error {
	targets := make(map[string]string)
	addCharacterURLs := func(char *ports.Character) {
		if char == nil {
			return
		}
		if char.ReferenceURL != "" {
			targets[char.ReferenceURL] = char.ReferenceURL
		}
		for _, url := range char.ReferenceURLs {
			if url != "" {
				targets[url] = url
			}
		}
	}

	// デフォルトキャラクターをアップロード対象に追加
	addCharacterURLs(mc.CharactersMap.GetDefault())

	// state に登場するキャラクターをアップロード対象に追加
	for _, id := range state.UniqueReferencedCharacterIDs() {
		addCharacterURLs(mc.CharactersMap.GetCharacterWithDefault(id))
	}

	return mc.prepareResources(ctx, targets, mc.getOrUploadAsset, "character")
}

// PreparePanelResources は、生成済みパネル画像（Generation.ImageURL）を
// ページ合成の参照用に事前アップロードします。
func (mc *ComicComposer) PreparePanelResources(ctx context.Context, state *ports.MangaState) error {
	targets := make(map[string]string)

	for i := range state.Panels {
		gen := state.Panels[i].Generation
		if gen == nil || gen.ImageURL == "" {
			continue
		}
		targets[gen.ImageURL] = gen.ImageURL
	}

	return mc.prepareResources(ctx, targets, func(ctx context.Context, key, _ string) (string, error) {
		return mc.getOrUploadPanelAsset(ctx, key)
	}, "panel")
}

// getOrUploadAsset はキャラクター用アセットをキャッシュ制御しつつ取得またはアップロードします。
// キャラクターアセットの場合も検索キーは参照URLです（PrepareCharacterResources 参照）。
func (mc *ComicComposer) getOrUploadAsset(ctx context.Context, key, referenceURL string) (string, error) {
	return mc.getOrUploadResource(ctx, key, referenceURL, mc.resourceMap.character)
}

// getOrUploadPanelAsset はパネル用参照URLをキャッシュ制御しつつ取得またはアップロードします。
func (mc *ComicComposer) getOrUploadPanelAsset(ctx context.Context, referenceURL string) (string, error) {
	// パネルアセットの場合、検索キーとソースURLは同一です。
	return mc.getOrUploadResource(ctx, referenceURL, referenceURL, mc.resourceMap.panel)
}

// prepareResources は指定されたリソースを事前アップロードします。
func (mc *ComicComposer) prepareResources(
	ctx context.Context,
	targets map[string]string,
	upload func(context.Context, string, string) (string, error),
	resourceType string,
) error {
	eg, egCtx := errgroup.WithContext(ctx)

	for key, referenceURL := range targets {
		eg.Go(func() error {
			if _, err := upload(egCtx, key, referenceURL); err != nil {
				return fmt.Errorf("%s resource preparation failed for '%s': %w", resourceType, key, err)
			}
			return nil
		})
	}

	return eg.Wait()
}

// getOrUploadResource は二重チェックロッキングと singleflight を用いてアセットアップロードの共通ロジックを提供します。
func (mc *ComicComposer) getOrUploadResource(ctx context.Context, key, referenceURL string, resourceMap map[string]string) (string, error) {
	// Vertex AI モード時は Cloud Storage (gs://) を直接参照可能なため、
	// File API へのアップロード処理をバイパスし、転送コストを削減します。
	if mc.BackendProvider.IsVertexAI() && IsGCSURI(referenceURL) {
		mc.mu.RLock()
		_, ok := resourceMap[key]
		mc.mu.RUnlock()

		if !ok {
			mc.mu.Lock()
			// RUnlock と Lock の間に他のゴルーチンが書き込んでいる可能性があるため再確認する
			if _, ok := resourceMap[key]; !ok {
				resourceMap[key] = ""
			}
			mc.mu.Unlock()
		}
		return "", nil
	}

	// 最初のチェック: ロックを最小限にするための RLock
	mc.mu.RLock()
	uri, ok := resourceMap[key]
	mc.mu.RUnlock()
	if ok {
		return uri, nil
	}

	// 同一キーに対する同時リクエストを1つに集約（HTTP URL等の場合のみ）
	val, err, _ := mc.uploadGroup.Do(key, func() (interface{}, error) {
		mc.mu.RLock()
		existingURI, ok := resourceMap[key]
		mc.mu.RUnlock()
		if ok {
			return existingURI, nil
		}

		// ここで実際に File API (Google AI Studio) へアップロードされる
		uploadedURI, uploadErr := mc.AssetManager.UploadFile(ctx, referenceURL)
		if uploadErr != nil {
			return nil, uploadErr
		}

		mc.mu.Lock()
		resourceMap[key] = uploadedURI
		mc.mu.Unlock()
		return uploadedURI, nil
	})

	if err != nil {
		return "", err
	}

	return val.(string), nil
}
