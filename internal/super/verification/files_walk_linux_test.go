//go:build linux

package verification

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// An ancestor the verifier may pass through but not list, as /data on Android.
func TestOpenWorkspaceThroughSearchOnlyAncestor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	base := canonicalTempDir(t)
	gate := filepath.Join(base, "gate")
	workspace := filepath.Join(gate, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(gate, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(gate, 0o700) })
	if _, err := os.ReadDir(gate); err == nil {
		t.Fatal("fixture ancestor is still readable")
	}
	root, err := openWorkspace(context.Background(), workspace)
	if err != nil {
		t.Fatalf("search-only ancestor refused: %v", err)
	}
	root.Close()
}

func TestOpenWorkspaceRejectsSymlinkAndFileAncestors(t *testing.T) {
	base := canonicalTempDir(t)
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "workspace"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, code := range map[string]string{
		filepath.Join(base, "link", "workspace"): CodeSymlink,
		filepath.Join(base, "file", "workspace"): CodeNotRegular,
		filepath.Join(base, "missing", "dir"):    CodeMissing,
	} {
		_, err := openWorkspace(context.Background(), path)
		if !errors.Is(err, ErrInvalidWorkspace) || !strings.HasSuffix(err.Error(), code) {
			t.Fatalf("%s: got %v, want %s", path, err, code)
		}
	}
}

func TestOpenWorkspaceHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openWorkspace(ctx, canonicalTempDir(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled walk returned %v", err)
	}
}
