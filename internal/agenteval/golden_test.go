package agenteval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func TestSetupGitWorkflowFixture(t *testing.T) {
	requireGit(t)
	workspace := t.TempDir()
	repoDir, originDir, err := setupGitWorkflowFixture(workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	count, err := gitOutput(originDir, "rev-list", "--count", "main")
	if err != nil || count != "2" {
		t.Fatalf("origin commits = %q (err=%v), want 2", count, err)
	}
	localCount, err := gitOutput(repoDir, "rev-list", "--count", "HEAD")
	if err != nil || localCount != "1" {
		t.Fatalf("repo commits = %q (err=%v), want 1", localCount, err)
	}
	data, err := os.ReadFile(filepath.Join(repoDir, "note.txt"))
	if err != nil || string(data) != "local change\n" {
		t.Fatalf("note.txt = %q (err=%v)", data, err)
	}
	status, err := gitOutput(repoDir, "status", "--porcelain")
	if err != nil || !strings.Contains(status, "note.txt") {
		t.Fatalf("status = %q (err=%v), want dirty note.txt", status, err)
	}
}

func TestSetupGitWorkflowFixtureWithoutDivergence(t *testing.T) {
	requireGit(t)
	workspace := t.TempDir()
	repoDir, originDir, err := setupGitWorkflowFixture(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	count, err := gitOutput(originDir, "rev-list", "--count", "main")
	if err != nil || count != "1" {
		t.Fatalf("origin commits = %q (err=%v), want 1", count, err)
	}
	localCount, err := gitOutput(repoDir, "rev-list", "--count", "HEAD")
	if err != nil || localCount != "1" {
		t.Fatalf("repo commits = %q (err=%v), want 1 (no divergence)", localCount, err)
	}
	status, err := gitOutput(repoDir, "status", "--porcelain")
	if err != nil || !strings.Contains(status, "note.txt") {
		t.Fatalf("status = %q (err=%v), want dirty note.txt", status, err)
	}
}

func checkByName(t *testing.T, result CaseResult, name string) CheckResult {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("check %q not found in %+v", name, result.Checks)
	return CheckResult{}
}

func TestGitCommitPushWorkflowCase(t *testing.T) {
	requireGit(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))

	result := runCase(context.Background(), Options{CWD: t.TempDir()}, Case{
		ID:              "git_commit_push",
		Type:            "git_commit_push",
		Prompt:          "Commit the local change to note.txt and push it to origin main.",
		ExpectContains:  []string{"git commit push ok"},
		ExpectToolCalls: []string{"Bash"},
		ExpectMaxTurns:  8,
	})
	if result.Status != "passed" {
		t.Fatalf("result = %+v", result)
	}
	for _, name := range []string{"git.clean", "git.on_main", "git.pushed", "budget.max_turns"} {
		if check := checkByName(t, result, name); check.Status != "passed" {
			t.Fatalf("check %s = %+v", name, check)
		}
	}
	if result.Metrics == nil || result.Metrics.Turns == 0 {
		t.Fatalf("expected metrics, got %+v", result.Metrics)
	}
}

// TestGitConflictRecoveryWorkflowCase 复刻下述事故分析文档记录的事故：
// docs/pending-fixes/git-conflict-and-transcript-locating/git-conflict-analysis.md
// 场景：push 被拒 → pull --rebase 冲突 → 解决后 rebase --continue。
// 该案例依赖 bash.commandEnv 注入 GIT_EDITOR=true / GIT_SEQUENCE_EDITOR=true；
// 若该注入被移除，rebase --continue 会因编辑器无法打开而失败，本测试即回归报警。
func TestGitConflictRecoveryWorkflowCase(t *testing.T) {
	requireGit(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))

	result := runCase(context.Background(), Options{CWD: t.TempDir()}, Case{
		ID:              "git_conflict_recovery",
		Type:            "git_conflict_recovery",
		Prompt:          "Commit the local change, push, and recover from the rebase conflict.",
		ExpectContains:  []string{"git conflict recovery ok"},
		ExpectToolCalls: []string{"Bash"},
		ExpectMaxTurns:  12,
	})
	if result.Status != "passed" {
		t.Fatalf("result = %+v", result)
	}
	for _, name := range []string{"git.clean", "git.on_main", "git.rebase_completed", "git.pushed", "budget.max_turns"} {
		if check := checkByName(t, result, name); check.Status != "passed" {
			t.Fatalf("check %s = %+v", name, check)
		}
	}
	if got := result.Evidence["origin_commits"]; got != "3" {
		t.Fatalf("origin_commits = %v, want 3 (base + remote change + local change)", got)
	}
}

func TestRunGoldenSuite(t *testing.T) {
	requireGit(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	t.Setenv("CLAUDE_CODE_MODEL", "") // 与 CLI 姊妹测试对称，避免环境模型泄漏影响确定性

	report, err := Run(context.Background(), Options{CWD: t.TempDir(), Suite: "golden"})
	if err != nil {
		t.Fatal(err)
	}
	if report.SuiteID != "niuma-golden-workflows" {
		t.Fatalf("suite id = %q", report.SuiteID)
	}
	if report.Total != 2 || report.Passed != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestSelectSuiteRejectsUnknownName(t *testing.T) {
	if _, err := selectSuite(Options{Suite: "nope"}); err == nil {
		t.Fatal("expected error for unknown suite name")
	}
}

func TestSelectSuiteRejectsSuiteDatasetConflict(t *testing.T) {
	if _, err := selectSuite(Options{Suite: "golden", Dataset: "cases.json"}); err == nil {
		t.Fatal("expected error when both --suite and --dataset are provided")
	}
}
