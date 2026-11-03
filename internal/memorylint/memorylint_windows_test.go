//go:build windows

package memorylint

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeLocalTargetWindowsVolumeBeforeURIScheme(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file with space.md")
	destination := strings.ReplaceAll(target, " ", "%20") + "?raw=1#top"
	resolved, local, err := normalizeLocalTarget(dir, []byte(destination))
	if err != nil || !local || resolved != target {
		t.Fatalf("resolved=%q local=%v err=%v, want %q", resolved, local, err, target)
	}
}
