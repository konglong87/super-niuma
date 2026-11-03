package goal

import (
	"path/filepath"
	"testing"
)

func TestDefaultRootIgnoresClaudeConfigDirForWrites(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(t.TempDir(), ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", legacy)

	want := filepath.Join(home, ".niuma")
	if got := DefaultRoot(); got != want {
		t.Fatalf("DefaultRoot = %q, want %q", got, want)
	}
}
