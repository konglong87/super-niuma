//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package imagegen

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/konglong87/super-niuma/internal/media"
)

func TestFilesystemBlobStoreFailsClosedWithoutSecureBackend(t *testing.T) {
	store, err := NewFilesystemBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	key := "tenant-7/user-11/session-13/asset.png"
	if filesystemBlobStoreSecureSupported() {
		t.Fatal("unsupported platform reported a secure filesystem backend")
	}
	if _, err := store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, Data: bytes.NewReader([]byte("image"))}); !errors.Is(err, ErrFilesystemBlobStoreUnsupported) {
		t.Fatalf("put err=%v", err)
	}
	if _, err := store.Open(context.Background(), policy, key); !errors.Is(err, ErrFilesystemBlobStoreUnsupported) {
		t.Fatalf("open err=%v", err)
	}
	if err := store.Delete(context.Background(), policy, key); !errors.Is(err, ErrFilesystemBlobStoreUnsupported) {
		t.Fatalf("delete err=%v", err)
	}
	if _, err := store.ListTenantBlobs(context.Background(), policy.TenantID); !errors.Is(err, ErrFilesystemBlobStoreUnsupported) {
		t.Fatalf("list err=%v", err)
	}
}
