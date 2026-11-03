package files

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

// Content-addressed snapshot store. Blobs referenced by file_change events are
// named by their sha256 content hash, so identical content is stored exactly
// once (dedup). Lifecycle is governed by transcript reachability: GCSnapshots
// removes only blobs that no live transcript references, so an object still
// usable for rewind is never reclaimed.
//
// Blobs are sharded into per-prefix subdirectories (dir/<hex[:2]>/sha256-<hex>.bin,
// like a git object store) so the store stays fast to enumerate as the blob count
// grows. Pre-sharding flat blobs (dir/sha256-<hex>.bin) are still honored: writes
// reuse an existing flat blob instead of duplicating it, reads use the full path
// stored in the transcript, and GC walks both layers.

const contentAddressedPrefix = "sha256-"

// SnapshotDir returns the global snapshot directory for the active identity.
func SnapshotDir() (string, error) {
	return config.CurrentIdentity("").GlobalStatePath("snapshots")
}

// StoreSnapshotContentAddressed streams src into the snapshot store under a
// content-addressed name and returns the blob path. Identical content reuses
// the existing blob instead of writing a duplicate.
func StoreSnapshotContentAddressed(dir string, src io.Reader) (string, error) {
	if strings.TrimSpace(dir) == "" {
		d, err := SnapshotDir()
		if err != nil {
			return "", err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".blob-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), src); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	hexsum := hex.EncodeToString(h.Sum(nil))
	name := contentAddressedPrefix + hexsum + ".bin"
	// Reuse a pre-sharding flat blob if one already holds this content, so the
	// migration to sharding never duplicates an object that is still in use.
	if legacy := filepath.Join(dir, name); statExists(legacy) {
		return legacy, nil
	}
	shardDir := filepath.Join(dir, hexsum[:2])
	if err := os.MkdirAll(shardDir, 0700); err != nil {
		return "", err
	}
	final := filepath.Join(shardDir, name)
	if statExists(final) {
		// Dedup: identical content is already stored; drop the temp copy.
		return final, nil
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return "", err
	}
	cleanup = false
	return final, nil
}

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ExternalizeContent stores content in the content-addressed snapshot store and
// returns the blob path together with the content's sha256 hex. Empty content is
// never externalized: it carries no leak risk and needs no blob, so the returned
// path and hash are both "" and the caller keeps it inline. Non-empty content
// always yields both a blob path and a hash. dir "" resolves to SnapshotDir().
func ExternalizeContent(dir, content string) (blobPath, sha256hex string, err error) {
	if content == "" {
		return "", "", nil
	}
	path, err := StoreSnapshotContentAddressed(dir, strings.NewReader(content))
	if err != nil {
		return "", "", err
	}
	return path, SnapshotHashFromPath(path), nil
}

// SHA256Hex returns the hex-encoded sha256 of s. It is used to record an audit
// hash for content that is intentionally not stored as a blob (e.g. a turn-level
// superseded file_change), so the transcript still records what changed without
// carrying or duplicating the body.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SnapshotHashFromPath extracts the sha256 hex from a content-addressed blob
// path named "<...>/sha256-<hex>.bin". It returns "" when p is not a
// content-addressed blob path, so callers can distinguish a legacy or temp path.
func SnapshotHashFromPath(p string) string {
	base := filepath.Base(strings.TrimSpace(p))
	if !strings.HasPrefix(base, contentAddressedPrefix) {
		return ""
	}
	base = strings.TrimPrefix(base, contentAddressedPrefix)
	return strings.TrimSuffix(base, ".bin")
}

// BlobInfo describes one content-addressed blob on disk.
type BlobInfo struct {
	Path string
	Size int64
}

// ListBlobs returns every content-addressed blob currently in the snapshot store
// across both the flat and sharded layers. Lifecycle GC uses it to decide what to
// keep or reclaim independently of transcript content.
func ListBlobs() ([]BlobInfo, error) {
	dir, err := SnapshotDir()
	if err != nil {
		return nil, err
	}
	var out []BlobInfo
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), contentAddressedPrefix) {
			return nil
		}
		size := int64(0)
		if info, e := d.Info(); e == nil {
			size = info.Size()
		}
		out = append(out, BlobInfo{Path: path, Size: size})
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return nil, walkErr
	}
	return out, nil
}

// GCSnapshots removes snapshot blobs whose absolute path is not in reachable,
// the set of blob paths still referenced by live transcripts. Reachable blobs
// are always kept, so objects still usable for rewind survive. It walks both the
// flat layer (pre-sharding blobs) and the per-prefix shard subdirectories, and
// only ever removes content-addressed blobs. It returns the number of blobs
// removed and the bytes freed.
func GCSnapshots(reachable map[string]bool) (removed int, freed int64, err error) {
	dir, err := SnapshotDir()
	if err != nil {
		return 0, 0, err
	}
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		// Never touch in-progress temp files from a concurrent capture/write.
		if strings.HasPrefix(name, ".blob-") || strings.HasPrefix(name, ".tmp-") {
			return nil
		}
		// Only manage content-addressed blobs; leave anything else untouched.
		if !strings.HasPrefix(name, contentAddressedPrefix) {
			return nil
		}
		if reachable[path] {
			return nil
		}
		size := int64(0)
		if info, statErr := d.Info(); statErr == nil {
			size = info.Size()
		}
		if rmErr := os.Remove(path); rmErr == nil {
			removed++
			freed += size
		}
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return removed, freed, walkErr
	}
	return removed, freed, nil
}
