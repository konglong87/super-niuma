package channel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspaceAllowsExistingDirectoryUnderRoot(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "project")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveWorkspace(child, []string{root})
	want, resolveErr := filepath.EvalSymlinks(child)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || got != want {
		t.Fatalf("workspace=%q err=%v", got, err)
	}
}

func TestResolveWorkspaceRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{filepath.Join(root, ".."), link} {
		if got, err := ResolveWorkspace(candidate, []string{root}); err == nil {
			t.Fatalf("workspace=%q accepted as %q", candidate, got)
		}
	}
}

func TestResolveWorkspaceFailsClosedWithoutRoots(t *testing.T) {
	if _, err := ResolveWorkspace(t.TempDir(), nil); err == nil {
		t.Fatal("workspace accepted without configured roots")
	}
}
