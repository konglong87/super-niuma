package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/media"
)

func TestMemoryBlobStorePersistsSHA256AndEnforcesScope(t *testing.T) {
	store := NewMemoryBlobStore()
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	data := []byte("generated image")
	blob, err := store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: "tenant-7/user-11/session-13/asset-a.png", MediaType: "image/png", Name: "a.png", Data: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(data)
	if blob.SHA256 != hex.EncodeToString(wantHash[:]) || blob.SizeBytes != int64(len(data)) {
		t.Fatalf("blob = %+v", blob)
	}
	reader, err := store.Open(context.Background(), policy, blob.Key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err != nil || closeErr != nil || !bytes.Equal(got, data) {
		t.Fatalf("read = %q err=%v close=%v", got, err, closeErr)
	}
	if _, err := store.Open(context.Background(), media.AccessPolicy{TenantID: 8, UserID: 11, SessionID: 13}, blob.Key); !errors.Is(err, ErrBlobForbidden) {
		t.Fatalf("cross tenant err = %v", err)
	}
	if _, err := store.Open(context.Background(), media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 99}, blob.Key); !errors.Is(err, ErrBlobForbidden) {
		t.Fatalf("cross session err = %v", err)
	}
}

func TestFilesystemBlobStoreWritesAtomicallyAndDeletesOrphans(t *testing.T) {
	requireSecureFilesystemBlobStore(t)
	root := t.TempDir()
	store, err := NewFilesystemBlobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	policy := media.AccessPolicy{TenantID: 1, UserID: 2, SessionID: 3}
	key := "tenant-1/user-2/session-3/asset-a.png"
	if _, err := store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, MediaType: "image/png", Data: bytes.NewReader([]byte("first"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, MediaType: "image/png", Data: bytes.NewReader([]byte("second"))}); err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), policy, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(got) != "second" {
		t.Fatalf("atomic replacement read %q", got)
	}
	orphan := filepath.Join(root, "tenant-1", "user-2", "session-3", "asset-orphan.png")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), policy, "tenant-1/user-2/session-3/asset-orphan.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan still exists: %v", err)
	}
	if err := store.Delete(context.Background(), policy, key); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), policy, key); !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestFilesystemBlobStoreUsesSecurePlatformBackend(t *testing.T) {
	requireSecureFilesystemBlobStore(t)
}

func TestBlobStoreRejectsUnsafeKeysAndMissingScope(t *testing.T) {
	store := NewMemoryBlobStore()
	for _, req := range []PutBlobRequest{{Key: "../../escape", Data: bytes.NewReader([]byte("x")), Policy: media.AccessPolicy{TenantID: 1, UserID: 2, SessionID: 3}}, {Key: "asset", Data: bytes.NewReader([]byte("x")), Policy: media.AccessPolicy{TenantID: 0, UserID: 2, SessionID: 3}}} {
		if _, err := store.Put(context.Background(), req); !errors.Is(err, ErrInvalidBlob) {
			t.Fatalf("put %q err = %v", req.Key, err)
		}
	}
}

func TestBlobStoresRejectNonCanonicalKeysForEveryOperation(t *testing.T) {
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	keys := []string{
		"tenant-7/user-11/session-13/../../victim",
		"tenant-7/user-11/session-13/./victim",
		"tenant-7/user-11/session-13//victim",
		"/tmp/victim",
		"tenant-7\\user-11\\session-13\\victim",
		"tenant-7/user-11/session-13/victim\x00.png",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			for _, store := range []BlobStore{NewMemoryBlobStore(), newTestFilesystemBlobStore(t)} {
				if _, err := store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, Data: bytes.NewReader([]byte("x"))}); !errors.Is(err, ErrInvalidBlob) {
					t.Fatalf("put key=%q err=%v", key, err)
				}
				if _, err := store.Open(context.Background(), policy, key); !errors.Is(err, ErrInvalidBlob) {
					t.Fatalf("open key=%q err=%v", key, err)
				}
				if err := store.Delete(context.Background(), policy, key); !errors.Is(err, ErrInvalidBlob) {
					t.Fatalf("delete key=%q err=%v", key, err)
				}
			}
		})
	}
}

func TestFilesystemBlobStoreTraversalCannotTouchVictimOutsideRoot(t *testing.T) {
	root := t.TempDir()
	store, err := NewFilesystemBlobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(filepath.Dir(root), "imagegen-victim")
	if err := os.WriteFile(victim, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(victim) })
	key := "tenant-7/user-11/session-13/../../../../" + filepath.Base(victim)
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	if _, err := store.Open(context.Background(), policy, key); !errors.Is(err, ErrInvalidBlob) {
		t.Fatalf("open err=%v", err)
	}
	if err := store.Delete(context.Background(), policy, key); !errors.Is(err, ErrInvalidBlob) {
		t.Fatalf("delete err=%v", err)
	}
	got, err := os.ReadFile(victim)
	if err != nil || string(got) != "protected" {
		t.Fatalf("victim=%q err=%v", got, err)
	}
}

func TestFilesystemBlobStoreRejectsIntermediateSymlinkForEveryOperation(t *testing.T) {
	requireSecureFilesystemBlobStore(t)
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	key := "tenant-7/user-11/session-13/asset.png"
	for _, operation := range []string{"put", "open", "delete"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			store, err := NewFilesystemBlobStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "tenant-7"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "tenant-7", "user-11")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			victim := filepath.Join(outside, "session-13", "asset.png")
			if err := os.MkdirAll(filepath.Dir(victim), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(victim, []byte("protected"), 0o600); err != nil {
				t.Fatal(err)
			}

			switch operation {
			case "put":
				_, err = store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, Data: bytes.NewReader([]byte("replacement"))})
			case "open":
				_, err = store.Open(context.Background(), policy, key)
			case "delete":
				err = store.Delete(context.Background(), policy, key)
			}
			if !errors.Is(err, ErrBlobForbidden) {
				t.Fatalf("%s err=%v", operation, err)
			}
			got, readErr := os.ReadFile(victim)
			if readErr != nil || string(got) != "protected" {
				t.Fatalf("victim=%q err=%v", got, readErr)
			}
		})
	}
}

func TestFilesystemBlobStoreRejectsFinalSymlink(t *testing.T) {
	requireSecureFilesystemBlobStore(t)
	root := t.TempDir()
	outside := t.TempDir()
	store, err := NewFilesystemBlobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	key := "tenant-7/user-11/session-13/asset.png"
	link := filepath.Join(root, "tenant-7", "user-11", "session-13", "asset.png")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim.png")
	if err := os.WriteFile(victim, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, operation := range []string{"put", "open", "delete"} {
		t.Run(operation, func(t *testing.T) {
			var err error
			switch operation {
			case "put":
				_, err = store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, Data: bytes.NewReader([]byte("replacement"))})
			case "open":
				_, err = store.Open(context.Background(), policy, key)
			case "delete":
				err = store.Delete(context.Background(), policy, key)
			}
			if !errors.Is(err, ErrBlobForbidden) {
				t.Fatalf("%s err=%v", operation, err)
			}
			got, readErr := os.ReadFile(victim)
			if readErr != nil || string(got) != "protected" {
				t.Fatalf("victim=%q err=%v", got, readErr)
			}
		})
	}
}

func newTestFilesystemBlobStore(t *testing.T) *FilesystemBlobStore {
	t.Helper()
	store, err := NewFilesystemBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func requireSecureFilesystemBlobStore(t *testing.T) {
	t.Helper()
	if !filesystemBlobStoreSecureSupported() {
		t.Skip("requires the secure filesystem blob backend")
	}
}

func TestMemoryBlobInventoryListsOnlyRequestedTenant(t *testing.T) {
	store := NewMemoryBlobStore()
	for _, item := range []struct {
		policy media.AccessPolicy
		key    string
	}{
		{media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}, "tenant-7/user-11/session-13/asset-a.png"},
		{media.AccessPolicy{TenantID: 7, UserID: 12, SessionID: 14}, "tenant-7/user-12/session-14/asset-b.png"},
		{media.AccessPolicy{TenantID: 8, UserID: 11, SessionID: 13}, "tenant-8/user-11/session-13/asset-c.png"},
	} {
		if _, err := store.Put(context.Background(), PutBlobRequest{Policy: item.policy, Key: item.key, MediaType: "image/png", Data: bytes.NewReader([]byte(item.key))}); err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := store.ListTenantBlobs(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tenant-7/user-11/session-13/asset-a.png", "tenant-7/user-12/session-14/asset-b.png"}
	if len(blobs) != len(want) || (len(blobs) == len(want) && (blobs[0].Key != want[0] || blobs[1].Key != want[1])) {
		t.Fatalf("tenant blobs=%+v", blobs)
	}
	if _, err := store.ListTenantBlobs(context.Background(), 0); !errors.Is(err, ErrInvalidBlob) {
		t.Fatalf("missing tenant err=%v", err)
	}
}

func TestFilesystemBlobInventoryListsRegularTenantFilesOnly(t *testing.T) {
	requireSecureFilesystemBlobStore(t)
	root := t.TempDir()
	store, err := NewFilesystemBlobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	key := "tenant-7/user-11/session-13/asset-a.png"
	if _, err := store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, MediaType: "image/png", Data: bytes.NewReader([]byte("image"))}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tenant-7", ".blob-incomplete"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tenant-8", "user-11", "session-13"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tenant-8", "user-11", "session-13", "asset-b.png"), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	blobs, err := store.ListTenantBlobs(context.Background(), 7)
	if err != nil || len(blobs) != 1 || blobs[0].Key != key || blobs[0].CreatedAt.IsZero() {
		t.Fatalf("blobs=%+v err=%v", blobs, err)
	}
	empty, err := store.ListTenantBlobs(context.Background(), 99)
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing tenant blobs=%+v err=%v", empty, err)
	}
}

func TestFilesystemBlobInventorySkipsTemporaryDirectoriesAndNonRegularEntries(t *testing.T) {
	requireSecureFilesystemBlobStore(t)
	root, err := os.MkdirTemp("", "i")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	store, err := NewFilesystemBlobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	policy := media.AccessPolicy{TenantID: 1, UserID: 1, SessionID: 1}
	keys := []string{
		"tenant-1/user-1/session-1/b.png",
		"tenant-1/user-1/session-1/a.png",
	}
	for _, key := range keys {
		if _, err := store.Put(context.Background(), PutBlobRequest{Policy: policy, Key: key, MediaType: "image/png", Data: bytes.NewReader([]byte(key))}); err != nil {
			t.Fatal(err)
		}
	}
	tenantRoot := filepath.Join(root, "tenant-1")
	temporaryFile := filepath.Join(tenantRoot, ".blob-writing", "user-1", "session-1", "leaked.png")
	if err := os.MkdirAll(filepath.Dir(temporaryFile), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporaryFile, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tenantRoot, "user-1", "session-1", "link.png")
	if err := os.Symlink(filepath.Join(tenantRoot, "user-1", "session-1", "a.png"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	socketPath := filepath.Join(tenantRoot, "user-1", "session-1", "s")
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })

	blobs, err := store.ListTenantBlobs(context.Background(), policy.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tenant-1/user-1/session-1/a.png",
		"tenant-1/user-1/session-1/b.png",
	}
	if len(blobs) != len(want) {
		t.Fatalf("blobs=%+v", blobs)
	}
	for i, key := range want {
		if blobs[i].Key != key {
			t.Fatalf("blobs=%+v", blobs)
		}
	}
}
