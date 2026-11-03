package media

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreIsIdempotentAndTenantScoped(t *testing.T) {
	store := NewMemoryStore()
	asset := Asset{AssetID: "asset-1", Kind: KindImage, MediaType: "image/png", TenantID: 7, UserID: 9, State: StateUploaded}
	if err := store.Put(context.Background(), asset); err != nil {
		t.Fatal(err)
	}
	asset.State = StateReady
	if err := store.Put(context.Background(), asset); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), AccessPolicy{TenantID: 7, UserID: 9}, "asset-1")
	if err != nil || got.State != StateReady {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := store.Get(context.Background(), AccessPolicy{TenantID: 8, UserID: 9}, "asset-1"); err == nil {
		t.Fatal("cross-tenant read should fail")
	}
}

func TestMemoryStoreDeletesExpiredAssets(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewMemoryStoreWithClock(func() time.Time { return now })
	asset := Asset{AssetID: "expired", TenantID: 7, UserID: 9, State: StateReady, ExpiresAt: now.Add(-time.Second)}
	if err := store.Put(context.Background(), asset); err != nil {
		t.Fatal(err)
	}
	if removed := store.DeleteExpired(context.Background(), now); removed != 1 {
		t.Fatalf("removed=%d", removed)
	}
}
