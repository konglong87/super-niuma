package gitcontext

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

const maxStatusChars = 2000

func Snapshot(ctx context.Context, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	if out, ok := git(ctx, cwd, "rev-parse", "--is-inside-work-tree"); !ok || strings.TrimSpace(out) != "true" {
		return ""
	}
	branch, _ := git(ctx, cwd, "branch", "--show-current")
	mainBranch, _ := git(ctx, cwd, "symbolic-ref", "refs/remotes/origin/HEAD", "--short")
	mainBranch = strings.TrimPrefix(strings.TrimSpace(mainBranch), "origin/")
	if mainBranch == "" {
		if out, ok := git(ctx, cwd, "rev-parse", "--abbrev-ref", "origin/main"); ok && strings.TrimSpace(out) != "" {
			mainBranch = "main"
		} else if out, ok := git(ctx, cwd, "rev-parse", "--abbrev-ref", "origin/master"); ok && strings.TrimSpace(out) != "" {
			mainBranch = "master"
		}
	}
	status, _ := git(ctx, cwd, "--no-optional-locks", "status", "--short")
	status = strings.TrimSpace(status)
	if len(status) > maxStatusChars {
		status = status[:maxStatusChars] + "\n... (truncated because it exceeds 2k characters; run git status for full output)"
	}
	log, _ := git(ctx, cwd, "--no-optional-locks", "log", "--oneline", "-n", "5")

	var parts []string
	parts = append(parts, "# Git Snapshot")
	parts = append(parts, "This is the git status at the start of the conversation. It is a snapshot and will not update automatically.")
	if strings.TrimSpace(branch) != "" {
		parts = append(parts, "Current branch: "+strings.TrimSpace(branch))
	}
	if strings.TrimSpace(mainBranch) != "" {
		parts = append(parts, "Main branch: "+strings.TrimSpace(mainBranch))
	}
	if status == "" {
		status = "(clean)"
	}
	parts = append(parts, "Status:\n"+status)
	if strings.TrimSpace(log) != "" {
		parts = append(parts, "Recent commits:\n"+strings.TrimSpace(log))
	}
	return strings.Join(parts, "\n\n")
}

func git(parent context.Context, cwd string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(stdout.String()), true
}
