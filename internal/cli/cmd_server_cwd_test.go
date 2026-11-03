package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AUDIT-P1-27：请求体里的 cwd 直接决定服务端工具的执行目录，此前没有 allowlist、
// 也没有 workspace 根约束。这条锁的是它不能再指到 workspace 之外。
func TestRequestCWDOutsideWorkspaceIsRejected(t *testing.T) {
	workspace := t.TempDir()
	roots, err := resolveAllowedCWDRoots(workspace)
	if err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	for _, requested := range []string{outside, "/etc", filepath.Join(workspace, "..")} {
		if _, err := resolveRequestCWD(requested, roots); err == nil {
			t.Fatalf("cwd %q outside the workspace was accepted", requested)
		}
	}

	// 路径穿越也要拦住，而不是被字符串前缀匹配放过去。
	traversal := filepath.Join(workspace, "sub", "..", "..", "escaped")
	if _, err := resolveRequestCWD(traversal, roots); err == nil {
		t.Fatalf("traversal cwd %q was accepted", traversal)
	}
}

// 反方向断言：workspace 自身和它的子目录必须照常可用 —— 这是 WebUI / mobile /
// agent task 每次请求都会带上的正常值。
func TestRequestCWDInsideWorkspaceIsAccepted(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "pkg", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	roots, err := resolveAllowedCWDRoots(workspace)
	if err != nil {
		t.Fatal(err)
	}

	for _, requested := range []string{workspace, nested, filepath.Join(workspace, "pkg", ".", "sub")} {
		resolved, err := resolveRequestCWD(requested, roots)
		if err != nil {
			t.Fatalf("cwd %q inside the workspace was rejected: %v", requested, err)
		}
		if resolved == "" {
			t.Fatalf("cwd %q resolved to an empty path", requested)
		}
	}
}

// workspace 内一个指向外部的软链不能把整棵允许子树撑开。
func TestRequestCWDRejectsSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	roots, err := resolveAllowedCWDRoots(workspace)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := resolveRequestCWD(link, roots); err == nil {
		t.Fatalf("a symlink out of the workspace (%s -> %s) was accepted", link, outside)
	}
}

// 一台机器托管多个仓库时，用环境变量显式列出根目录。
func TestAllowedCWDRootsHonorsEnvOverride(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	t.Setenv(serverAllowedCWDRootsEnv, strings.Join([]string{workspace, extra}, string(os.PathListSeparator)))

	roots, err := resolveAllowedCWDRoots(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("roots=%v, want both the workspace and the extra root", roots)
	}
	if _, err := resolveRequestCWD(extra, roots); err != nil {
		t.Fatalf("the explicitly allowed extra root was rejected: %v", err)
	}
	if _, err := resolveRequestCWD(t.TempDir(), roots); err == nil {
		t.Fatal("a directory outside both roots was accepted")
	}
}

// workspace 为空且没配环境变量时无从约束。保持原行为，而不是假装拦住了。
func TestRequestCWDWithNoRootsIsUnconstrained(t *testing.T) {
	roots, err := resolveAllowedCWDRoots("")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Fatalf("roots=%v, want none", roots)
	}
	if _, err := resolveRequestCWD("/etc", roots); err != nil {
		t.Fatalf("with no roots configured the cwd must pass through: %v", err)
	}
}

func TestDesktopLocalRequestCWDAllowsAnyExistingDirectory(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	validate, err := newServerRequestCWDValidator(workspace, true)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := validate(filepath.Join(outside, "."))
	if err != nil {
		t.Fatalf("desktop-local validator rejected an existing directory outside the startup workspace: %v", err)
	}
	if resolved != outside {
		t.Fatalf("resolved cwd = %q, want %q", resolved, outside)
	}

	if _, err := validate(filepath.Join(outside, "missing")); err == nil {
		t.Fatal("desktop-local validator accepted a missing directory")
	}
}
