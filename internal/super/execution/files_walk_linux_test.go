//go:build linux

package execution

import (
	"os"
	"path/filepath"
	"testing"
)

// An ancestor the worker may pass through but not list, as /data on Android.
func TestVerifyRootThroughSearchOnlyAncestor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	root, err := os.OpenRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if !verifyRoot(root, workspace) {
		t.Fatal("search-only ancestor refused")
	}
}

func TestVerifyRootStillRefusesSymlinkedAncestor(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	workspace := filepath.Join(real, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if verifyRoot(root, filepath.Join(base, "link", "workspace")) {
		t.Fatal("symlinked ancestor accepted")
	}
}
