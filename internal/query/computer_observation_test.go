package query

import (
	"context"
	"io"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
)

func TestPersistComputerObservationStoresScopedAssetReference(t *testing.T) {
	blobStore := imagegen.NewMemoryBlobStore()
	assetStore := media.NewMemoryStore()
	session := New(nil, nil, Options{TenantID: 7, UserID: 11, TenantSessionID: 13, ImageBlobStore: blobStore, MediaAssetStore: assetStore})
	ref := session.persistComputerObservation(context.Background(), `{"observation":{"id":"observation-1"}}`, []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: blockTypeImage, Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: computerPrivacyPNG}}}}})
	if ref == nil || ref.ObservationID != "observation-1" || ref.AssetID == "" {
		t.Fatalf("reference = %+v", ref)
	}
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	asset, err := assetStore.Get(context.Background(), policy, ref.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := blobStore.Open(context.Background(), policy, asset.Original.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || asset.MediaType != "image/png" || asset.SessionID != 13 {
		t.Fatalf("asset=%+v data=%d", asset, len(data))
	}
	if _, err := assetStore.Get(context.Background(), media.AccessPolicy{TenantID: 7, UserID: 12, SessionID: 13}, ref.AssetID); err != media.ErrForbidden {
		t.Fatalf("cross-user read err=%v, want %v", err, media.ErrForbidden)
	}
}
