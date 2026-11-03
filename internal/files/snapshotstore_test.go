package files

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func countBlobs(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	// Blobs are sharded into per-prefix subdirectories, so count recursively.
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), contentAddressedPrefix) {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSnapshotStoreDedupsIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("dedup-me\n", 100)
	var firstPath string
	// 1000 identical stores must collapse to a single content-addressed blob so
	// repeated edits do not grow the store linearly.
	for i := 0; i < 1000; i++ {
		path, err := StoreSnapshotContentAddressed(dir, strings.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		if firstPath == "" {
			firstPath = path
		} else if path != firstPath {
			t.Fatalf("identical content produced different paths: %q vs %q", firstPath, path)
		}
	}
	if n := countBlobs(t, dir); n != 1 {
		t.Fatalf("expected 1 deduped blob, found %d", n)
	}
	// Distinct content is stored separately.
	if _, err := StoreSnapshotContentAddressed(dir, strings.NewReader("different")); err != nil {
		t.Fatal(err)
	}
	if n := countBlobs(t, dir); n != 2 {
		t.Fatalf("expected 2 blobs after distinct content, found %d", n)
	}
}

func TestGCSnapshotsKeepsReachableRemovesOrphans(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "legacy"))
	dir, err := SnapshotDir()
	if err != nil {
		t.Fatal(err)
	}
	keep, err := StoreSnapshotContentAddressed(dir, strings.NewReader("keep-me"))
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := StoreSnapshotContentAddressed(dir, strings.NewReader("orphan"))
	if err != nil {
		t.Fatal(err)
	}
	removed, freed, err := GCSnapshots(map[string]bool{keep: true})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || freed <= 0 {
		t.Fatalf("removed=%d freed=%d, want 1 orphan removed", removed, freed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("reachable blob must survive GC: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan blob must be removed, err=%v", err)
	}
}

func TestExternalizeContent(t *testing.T) {
	dir := t.TempDir()

	// Empty content is never externalized: no blob, no hash.
	path, hash, err := ExternalizeContent(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || hash != "" {
		t.Fatalf("empty content must not externalize, got path=%q hash=%q", path, hash)
	}
	if n := countBlobs(t, dir); n != 0 {
		t.Fatalf("empty content must create no blob, found %d", n)
	}

	// Non-empty content yields a content-addressed blob and its hash, and the
	// blob body round-trips.
	content := "fixture_value=abcdef0123456789\n"
	path, hash, err = ExternalizeContent(dir, content)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || hash == "" {
		t.Fatalf("non-empty content must externalize, got path=%q hash=%q", path, hash)
	}
	if got := SnapshotHashFromPath(path); got != hash {
		t.Fatalf("hash mismatch: path-derived %q vs returned %q", got, hash)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("blob body mismatch: got %q want %q", data, content)
	}

	// Identical content dedups to the same blob.
	path2, _, err := ExternalizeContent(dir, content)
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path {
		t.Fatalf("identical content must dedup: %q vs %q", path2, path)
	}
	if n := countBlobs(t, dir); n != 1 {
		t.Fatalf("expected 1 deduped blob, found %d", n)
	}
}

func TestStoreSnapshotShardsByPrefix(t *testing.T) {
	dir := t.TempDir()
	content := "shard-me\n"
	sum := sha256.Sum256([]byte(content))
	hexsum := hex.EncodeToString(sum[:])

	path, err := StoreSnapshotContentAddressed(dir, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, hexsum[:2], contentAddressedPrefix+hexsum+".bin")
	if path != want {
		t.Fatalf("blob must land in shard subdir:\n got %q\nwant %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sharded blob must exist: %v", err)
	}
}

func TestStoreSnapshotReusesLegacyFlatBlob(t *testing.T) {
	dir := t.TempDir()
	content := "legacy-flat\n"
	sum := sha256.Sum256([]byte(content))
	hexsum := hex.EncodeToString(sum[:])

	// Simulate a pre-sharding flat blob already on disk.
	flat := filepath.Join(dir, contentAddressedPrefix+hexsum+".bin")
	if err := os.WriteFile(flat, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	// Storing the same content must reuse the flat blob, not write a sharded dup.
	path, err := StoreSnapshotContentAddressed(dir, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if path != flat {
		t.Fatalf("must reuse legacy flat blob: got %q want %q", path, flat)
	}
	if n := countBlobs(t, dir); n != 1 {
		t.Fatalf("must not duplicate content across layers, found %d blobs", n)
	}
}

func TestGCSnapshotsWalksBothLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	dir, err := SnapshotDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	// A sharded blob to keep, a sharded blob to reclaim...
	keep, err := StoreSnapshotContentAddressed(dir, strings.NewReader("keep-sharded"))
	if err != nil {
		t.Fatal(err)
	}
	shardedOrphan, err := StoreSnapshotContentAddressed(dir, strings.NewReader("orphan-sharded"))
	if err != nil {
		t.Fatal(err)
	}
	// ...and a pre-sharding flat orphan in the top layer.
	flatOrphan := filepath.Join(dir, contentAddressedPrefix+"deadbeef.bin")
	if err := os.WriteFile(flatOrphan, []byte("orphan-flat"), 0600); err != nil {
		t.Fatal(err)
	}

	removed, freed, err := GCSnapshots(map[string]bool{keep: true})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 || freed <= 0 {
		t.Fatalf("removed=%d freed=%d, want 2 orphans (flat + sharded) removed", removed, freed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("reachable sharded blob must survive GC: %v", err)
	}
	if _, err := os.Stat(shardedOrphan); !os.IsNotExist(err) {
		t.Fatalf("sharded orphan must be removed, err=%v", err)
	}
	if _, err := os.Stat(flatOrphan); !os.IsNotExist(err) {
		t.Fatalf("flat orphan must be removed, err=%v", err)
	}
}

func TestSnapshotHashFromPath(t *testing.T) {
	if got := SnapshotHashFromPath("/x/y/" + contentAddressedPrefix + "deadbeef.bin"); got != "deadbeef" {
		t.Fatalf("content-addressed path: got %q want deadbeef", got)
	}
	if got := SnapshotHashFromPath("/tmp/file-snapshot-123.bin"); got != "" {
		t.Fatalf("non-content-addressed path must return empty, got %q", got)
	}
	if got := SnapshotHashFromPath(""); got != "" {
		t.Fatalf("empty path must return empty, got %q", got)
	}
}

func BenchmarkSnapshotStoreRepeatedIdenticalContent(b *testing.B) {
	dir := b.TempDir()
	content := strings.Repeat("x", 4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := StoreSnapshotContentAddressed(dir, strings.NewReader(content)); err != nil {
			b.Fatal(err)
		}
	}
}
