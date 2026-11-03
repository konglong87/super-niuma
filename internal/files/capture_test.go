package files

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func changeFor(changes []Change, path string) *Change {
	for i := range changes {
		if changes[i].Path == path {
			return &changes[i]
		}
	}
	return nil
}

func TestCaptureDiffDetectsCreateModifyDelete(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keep.txt")
	modify := filepath.Join(root, "modify.txt")
	del := filepath.Join(root, "delete.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modify, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(del, []byte("gone"), 0644); err != nil {
		t.Fatal(err)
	}

	capture, err := NewCapture(CaptureConfig{Roots: []string{root}, SnapshotDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modify, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(del); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	changes, skipped, err := capture.Changes()
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped: %v", skipped)
	}
	if changeFor(changes, keep) != nil {
		t.Errorf("unchanged file should not appear")
	}
	if c := changeFor(changes, modify); c == nil || c.Before != "v1" || c.After != "v2" {
		t.Errorf("modify change wrong: %+v", c)
	}
	if c := changeFor(changes, del); c == nil || !c.BeforeExists || c.AfterExists {
		t.Errorf("delete change wrong: %+v", c)
	}
	if c := changeFor(changes, filepath.Join(root, "created.txt")); c == nil || c.BeforeExists || !c.AfterExists {
		t.Errorf("create change wrong: %+v", c)
	}
}

func TestCaptureDiffDetectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("t"), 0644); err != nil {
		t.Fatal(err)
	}
	capture, err := NewCapture(CaptureConfig{Roots: []string{root}, SnapshotDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink("target.txt", link); err != nil {
		t.Fatal(err)
	}
	changes, _, err := capture.Changes()
	if err != nil {
		t.Fatal(err)
	}
	c := changeFor(changes, link)
	if c == nil || !c.AfterIsSymlink || c.AfterLinkTarget != "target.txt" {
		t.Fatalf("symlink change wrong: %+v", c)
	}
}

func TestCaptureBudgetReportsSkippedInsteadOfEmptyBefore(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big.txt")
	if err := os.WriteFile(big, []byte("0123456789abcdef"), 0644); err != nil {
		t.Fatal(err)
	}
	// Budget smaller than the file forces the before-content to go uncaptured.
	capture, err := NewCapture(CaptureConfig{
		Roots:          []string{root},
		MaxInlineBytes: 4,
		MaxTotalBytes:  4,
		SnapshotDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, []byte("mutated-content-here"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, skipped, err := capture.Changes()
	if err != nil {
		t.Fatal(err)
	}
	if changeFor(changes, big) != nil {
		t.Errorf("uncaptured file must not emit a destructive change: %+v", changes)
	}
	if len(skipped) != 1 || skipped[0] != big {
		t.Errorf("expected big.txt reported skipped, got %v", skipped)
	}
}

func TestCaptureExternalizesLargeBeforeContent(t *testing.T) {
	root := t.TempDir()
	snapDir := t.TempDir()
	target := filepath.Join(root, "big.txt")
	// Before-content well above the externalize threshold but below the inline
	// capture cap, so it is captured in memory then externalized on diff.
	before := strings.Repeat("A", 32*1024)
	if err := os.WriteFile(target, []byte(before), 0644); err != nil {
		t.Fatal(err)
	}
	capture, err := NewCapture(CaptureConfig{Roots: []string{root}, SnapshotDir: snapDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("small-after"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, _, err := capture.Changes()
	if err != nil {
		t.Fatal(err)
	}
	c := changeFor(changes, target)
	if c == nil {
		t.Fatalf("no change captured")
	}
	if c.Before != "" {
		t.Errorf("large before-content should be externalized, not inlined (%d bytes inline)", len(c.Before))
	}
	if c.BeforeSnapshotPath == "" || !strings.Contains(c.BeforeSnapshotPath, contentAddressedPrefix) {
		t.Errorf("expected content-addressed before-snapshot, got %q", c.BeforeSnapshotPath)
	}
	data, err := os.ReadFile(c.BeforeSnapshotPath)
	if err != nil || string(data) != before {
		t.Fatalf("externalized snapshot content mismatch: err=%v", err)
	}
}

func TestCaptureIgnoresGitDirectory(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(gitDir, "config")
	if err := os.WriteFile(cfg, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	capture, err := NewCapture(CaptureConfig{Roots: []string{root}, SnapshotDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("v2-modified"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, _, err := capture.Changes()
	if err != nil {
		t.Fatal(err)
	}
	if changeFor(changes, cfg) != nil {
		t.Fatalf(".git contents must not be tracked: %+v", changes)
	}
}

func TestCaptureReclaimsUnreferencedSnapshots(t *testing.T) {
	root := t.TempDir()
	snapDir := t.TempDir()
	stable := filepath.Join(root, "stable.bin")
	changed := filepath.Join(root, "changed.bin")
	// Both large enough to be snapshotted rather than inlined.
	payload := make([]byte, 2048)
	for i := range payload {
		payload[i] = byte(i)
	}
	if err := os.WriteFile(stable, payload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changed, payload, 0644); err != nil {
		t.Fatal(err)
	}
	capture, err := NewCapture(CaptureConfig{
		Roots:          []string{root},
		MaxInlineBytes: 16,
		SnapshotDir:    snapDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	mutated := append(append([]byte(nil), payload...), 'X')
	if err := os.WriteFile(changed, mutated, 0644); err != nil {
		t.Fatal(err)
	}
	changes, _, err := capture.Changes()
	if err != nil {
		t.Fatal(err)
	}
	c := changeFor(changes, changed)
	if c == nil || c.BeforeSnapshotPath == "" {
		t.Fatalf("changed file should carry a before-snapshot: %+v", c)
	}
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the referenced snapshot to survive, found %d", len(entries))
	}
}
