package agentworktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
)

type Info struct {
	Path       string `json:"worktree_path,omitempty"`
	Branch     string `json:"worktree_branch,omitempty"`
	HeadCommit string `json:"worktree_head_commit,omitempty"`
	GitRoot    string `json:"worktree_git_root,omitempty"`
	HookBased  bool   `json:"hook_based,omitempty"`
}

func NewSlug(prefix string) string {
	prefix = sanitizeSlug(prefix)
	if prefix == "" {
		prefix = "agent"
	}
	return prefix + "-" + newSlugSuffix()
}

func Create(ctx context.Context, cwd, slug string) (Info, error) {
	return CreateWithHooks(ctx, cwd, slug, hooks.Runner{})
}

func CreateWithHooks(ctx context.Context, cwd, slug string, hookRunner hooks.Runner) (Info, error) {
	slug = sanitizeSlug(slug)
	if slug == "" {
		return Info{}, fmt.Errorf("worktree slug is required")
	}
	if len(hookRunner.Hooks[hooks.WorktreeCreate]) > 0 {
		info, err := createWithWorktreeCreateHook(ctx, cwd, slug, hookRunner)
		if err != nil {
			return Info{}, err
		}
		return info, nil
	}
	gitRoot, err := gitOutput(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Info{}, fmt.Errorf("create agent worktree: not in a git repository: %w", err)
	}
	gitRoot = strings.TrimSpace(gitRoot)
	// 从这里往下都是对 gitRoot 元数据的读改写（HEAD、已存在检查、worktree add），
	// 必须整段串起来 —— 只锁 `worktree add` 那一行会留下 TOCTOU：两个调用都看到
	// "还没有这棵树"，然后一个成功一个报 already exists。
	defer lockGitRoot(gitRoot)()
	head, err := gitOutput(ctx, gitRoot, "rev-parse", "HEAD")
	if err != nil {
		return Info{}, fmt.Errorf("create agent worktree: resolve HEAD: %w", err)
	}
	info := Info{
		Path:       config.CurrentIdentity(gitRoot).ProjectStatePath(gitRoot, "worktrees", slug),
		Branch:     "worktree-" + slug,
		HeadCommit: strings.TrimSpace(head),
		GitRoot:    gitRoot,
	}
	if existingHead, err := gitOutput(ctx, info.Path, "rev-parse", "HEAD"); err == nil && strings.TrimSpace(existingHead) != "" {
		info.HeadCommit = strings.TrimSpace(existingHead)
		return info, nil
	}
	if err := os.MkdirAll(filepath.Dir(info.Path), 0700); err != nil {
		return Info{}, err
	}
	if _, err := gitOutput(ctx, gitRoot, "worktree", "add", "-B", info.Branch, info.Path, "HEAD"); err != nil {
		return Info{}, fmt.Errorf("create agent worktree: %w", err)
	}
	return info, nil
}

func HasChanges(ctx context.Context, info Info) bool {
	if strings.TrimSpace(info.Path) == "" || strings.TrimSpace(info.HeadCommit) == "" {
		return true
	}
	if status, err := gitOutput(ctx, info.Path, "status", "--porcelain"); err != nil || strings.TrimSpace(status) != "" {
		return true
	}
	count, err := gitOutput(ctx, info.Path, "rev-list", "--count", info.HeadCommit+"..HEAD")
	if err != nil {
		return true
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(count))
	return err != nil || parsed > 0
}

func CleanupIfUnchanged(ctx context.Context, info Info) (Info, error) {
	if strings.TrimSpace(info.Path) == "" {
		return Info{}, nil
	}
	if info.HookBased {
		return info, nil
	}
	if strings.TrimSpace(info.HeadCommit) == "" || strings.TrimSpace(info.GitRoot) == "" {
		return info, nil
	}
	if HasChanges(ctx, info) {
		return info, nil
	}
	if err := Remove(ctx, info); err != nil {
		return info, err
	}
	return Info{}, nil
}

func createWithWorktreeCreateHook(ctx context.Context, cwd, slug string, hookRunner hooks.Runner) (Info, error) {
	result, err := hookRunner.RunWithPayload(ctx, hooks.WorktreeCreate, cwd, hooks.Payload{Name: slug})
	if err != nil {
		return Info{}, fmt.Errorf("WorktreeCreate hook failed: %w", err)
	}
	path := strings.TrimSpace(result.HookSpecificOutput.WorktreePath)
	if strings.TrimSpace(result.HookSpecificOutput.HookEventName) != "" && !strings.EqualFold(result.HookSpecificOutput.HookEventName, hooks.WorktreeCreate) {
		path = ""
	}
	if path == "" {
		path = strings.TrimSpace(result.Message)
	}
	if path == "" {
		return Info{}, fmt.Errorf("WorktreeCreate hook failed: no successful output")
	}
	return Info{Path: filepath.Clean(path), HookBased: true}, nil
}

func Remove(ctx context.Context, info Info) error {
	if strings.TrimSpace(info.GitRoot) == "" || strings.TrimSpace(info.Path) == "" {
		return fmt.Errorf("worktree git root and path are required")
	}
	// remove 与 add 抢同一把 git 索引锁，所以走同一把互斥锁。
	defer lockGitRoot(strings.TrimSpace(info.GitRoot))()
	if _, err := gitOutput(ctx, info.GitRoot, "worktree", "remove", "--force", info.Path); err != nil {
		return err
	}
	if strings.TrimSpace(info.Branch) != "" {
		_, _ = gitOutput(ctx, info.GitRoot, "branch", "-D", info.Branch)
	}
	return nil
}

func gitOutput(ctx context.Context, cwd string, args ...string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("git command timed out: git %s\n%s", strings.Join(args, " "), text)
	}
	if err != nil {
		return text, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func sanitizeSlug(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r)
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
