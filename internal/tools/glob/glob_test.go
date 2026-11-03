package glob

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestGlobTool(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "a", "main.go"), "")
	mustWrite(t, filepath.Join(tmp, "a", "readme.md"), "")
	mustWrite(t, filepath.Join(tmp, "b", "main.go"), "")

	input, _ := json.Marshal(map[string]string{"pattern": "**/*.go"})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, filepath.Join(tmp, "a", "main.go")) ||
		!strings.Contains(res.Content, filepath.Join(tmp, "b", "main.go")) ||
		strings.Contains(res.Content, "readme.md") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestGlobToolLimitAndModifiedOrder(t *testing.T) {
	tmp := t.TempDir()
	oldPath := filepath.Join(tmp, "old.go")
	newPath := filepath.Join(tmp, "new.go")
	mustWrite(t, oldPath, "")
	mustWrite(t, newPath, "")
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"pattern": "*.go", "limit": 1})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if strings.TrimSpace(res.Content) != newPath {
		t.Fatalf("content = %q, want %s", res.Content, newPath)
	}
}

func TestGlobSkipsManagedAgentWorktrees(t *testing.T) {
	tmp := t.TempDir()
	realPath := filepath.Join(tmp, "internal", "query.go")
	stalePath := filepath.Join(tmp, ".claude", "worktrees", "agent-old", "internal", "query.go")
	mustWrite(t, realPath, "")
	mustWrite(t, stalePath, "")

	input, _ := json.Marshal(map[string]string{"pattern": "**/*.go"})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, realPath) {
		t.Fatalf("content missing real path: %q", res.Content)
	}
	if strings.Contains(res.Content, stalePath) || strings.Contains(res.Content, ".claude/worktrees") {
		t.Fatalf("content includes managed worktree path: %q", res.Content)
	}
}

func TestGlobDescriptionEncouragesPathVerification(t *testing.T) {
	desc := New().Description()
	for _, want := range []string{
		"verify uncertain paths before Read",
		"Prefer narrow patterns",
		"limit parameter",
		"searching file CONTENTS",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
