package worktree

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestWorktreeList(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	tmp := t.TempDir()
	run(t, tmp, "git", "init")
	run(t, tmp, "git", "config", "user.email", "test@example.com")
	run(t, tmp, "git", "config", "user.name", "Test")
	run(t, tmp, "git", "commit", "--allow-empty", "-m", "init")

	res := New().Run(context.Background(), json.RawMessage(`{"action":"list"}`), tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "worktree ") {
		t.Fatalf("list = %s", res.Content)
	}
}

func run(t *testing.T, cwd string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// initRepo creates a repository with one commit so `git worktree add` has a base.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	return dir
}

// TestWorktreeAddListRemove covers the add/remove actions, which AUDIT-P1-19
// flagged as completely untested.
func TestWorktreeAddListRemove(t *testing.T) {
	repo := initRepo(t)
	target := filepath.Join(t.TempDir(), "wt")
	tool := New()
	ctx := tools.Context{CWD: repo}

	add, _ := json.Marshal(map[string]any{"action": "add", "path": target, "branch": "feature/x"})
	if res := tool.Run(context.Background(), add, ctx); res.IsError {
		t.Fatalf("add = %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	list, _ := json.Marshal(map[string]any{"action": "list"})
	res := tool.Run(context.Background(), list, ctx)
	if res.IsError || !strings.Contains(res.Content, "feature/x") {
		t.Fatalf("list = %+v", res)
	}

	remove, _ := json.Marshal(map[string]any{"action": "remove", "path": target})
	if res := tool.Run(context.Background(), remove, ctx); res.IsError {
		t.Fatalf("remove = %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err == nil {
		t.Fatal("worktree still present after remove")
	}
}

func TestWorktreeAddUsesBaseAndRemoveForce(t *testing.T) {
	repo := initRepo(t)
	target := filepath.Join(t.TempDir(), "wt")
	tool := New()
	ctx := tools.Context{CWD: repo}

	add, _ := json.Marshal(map[string]any{"action": "add", "path": target, "branch": "feature/y", "base": "main"})
	if res := tool.Run(context.Background(), add, ctx); res.IsError {
		t.Fatalf("add with base = %s", res.Content)
	}
	// A dirty worktree needs --force to come out.
	if err := os.WriteFile(filepath.Join(target, "dirty.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	remove, _ := json.Marshal(map[string]any{"action": "remove", "path": target, "force": true})
	if res := tool.Run(context.Background(), remove, ctx); res.IsError {
		t.Fatalf("forced remove = %s", res.Content)
	}
}

func TestWorktreeRejectsBadInput(t *testing.T) {
	tool := New()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"malformed json", `{`, "unexpected end"},
		{"unknown action", `{"action":"teleport"}`, "unsupported Worktree action"},
		{"add without branch", `{"action":"add","path":"/tmp/x"}`, "path and branch are required"},
		{"add without path", `{"action":"add","branch":"b"}`, "path and branch are required"},
		{"remove without path", `{"action":"remove"}`, "path is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Run(context.Background(), json.RawMessage(tc.input), tools.Context{CWD: t.TempDir()})
			if !res.IsError || !strings.Contains(res.Content, tc.want) {
				t.Fatalf("res = %+v, want error containing %q", res, tc.want)
			}
		})
	}
}

func TestWorktreeSurfacesGitFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Not a repository: git must fail and the failure must reach the caller.
	input, _ := json.Marshal(map[string]any{"action": "list"})
	res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if !res.IsError {
		t.Fatalf("expected git failure to surface, got %+v", res)
	}
}
