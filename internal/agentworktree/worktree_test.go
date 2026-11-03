package agentworktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
)

func TestCreateAndRemoveGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	repo := initGitRepo(t)
	info, err := Create(context.Background(), repo, "agent-test")
	if err != nil {
		t.Fatal(err)
	}
	repoReal, _ := filepath.EvalSymlinks(repo)
	rootReal, _ := filepath.EvalSymlinks(info.GitRoot)
	if info.Path == "" || info.Branch != "worktree-agent-test" || info.HeadCommit == "" || rootReal != repoReal {
		t.Fatalf("info = %+v", info)
	}
	if _, err := os.Stat(filepath.Join(info.Path, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
	if HasChanges(context.Background(), info) {
		t.Fatalf("fresh worktree should be unchanged")
	}
	if err := os.WriteFile(filepath.Join(info.Path, "changed.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if !HasChanges(context.Background(), info) {
		t.Fatalf("dirty worktree should report changes")
	}
	kept, err := CleanupIfUnchanged(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Path == "" {
		t.Fatalf("dirty worktree should be kept")
	}
	if err := os.Remove(filepath.Join(info.Path, "changed.txt")); err != nil {
		t.Fatal(err)
	}
	kept, err = CleanupIfUnchanged(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Path != "" {
		t.Fatalf("unchanged worktree should be cleaned: %+v", kept)
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists or stat failed unexpectedly: %v", err)
	}
}

func TestCreateWithWorktreeCreateHookUsesStdoutPathWithoutGit(t *testing.T) {
	parent := t.TempDir()
	worktreePath := filepath.Join(t.TempDir(), "hook-worktree")
	runner := hooks.New(map[string][]config.HookCommand{
		hooks.WorktreeCreate: {{Command: "mkdir -p " + strconv.Quote(worktreePath) + " && printf %s " + strconv.Quote(worktreePath)}},
	})
	info, err := CreateWithHooks(context.Background(), parent, "agent-hook", runner)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != worktreePath || !info.HookBased || info.Branch != "" || info.HeadCommit != "" || info.GitRoot != "" {
		t.Fatalf("info = %+v", info)
	}
	kept, err := CleanupIfUnchanged(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Path != worktreePath || !kept.HookBased {
		t.Fatalf("hook worktree should be kept: %+v", kept)
	}
}

func TestCreateWithWorktreeCreateHookUsesHookSpecificOutput(t *testing.T) {
	parent := t.TempDir()
	worktreePath := filepath.Join(t.TempDir(), "json-worktree")
	payload := `{"hookSpecificOutput":{"hookEventName":"WorktreeCreate","worktreePath":` + strconv.Quote(worktreePath) + `}}`
	runner := hooks.New(map[string][]config.HookCommand{
		hooks.WorktreeCreate: {{Command: "mkdir -p " + strconv.Quote(worktreePath) + " && printf %s " + strconv.Quote(payload)}},
	})
	info, err := CreateWithHooks(context.Background(), parent, "agent-json-hook", runner)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != worktreePath || !info.HookBased {
		t.Fatalf("info = %+v", info)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
