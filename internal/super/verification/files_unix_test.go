//go:build unix

package verification

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFIFORejectedWithoutOpeningOrBlocking(t *testing.T) {
	root := workspace(t)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	expectFailedCheck(t, root, textPolicy("pipe", "ok"), CodeNotRegular)
}

func TestSnapshotBytesAreIndependentOfLaterPathReplacement(t *testing.T) {
	path := workspace(t)
	writeArtifact(t, path, "a", "original")
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data, err := readSnapshot(context.Background(), root, textPolicy("a", "original").Rules[0])
	if err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, path, "next", "replacement")
	if err := os.Rename(filepath.Join(path, "next"), filepath.Join(path, "a")); err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatal("snapshot changed after read")
	}
	expectFailedCheck(t, path, textPolicy("a", "original"), CodeTextMissing)
}
