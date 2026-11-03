package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolvePath(cwd, path string) (string, error) {
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(filepath.Join(cwd, path))
}

func EnsureWritablePath(cwd string, writableRoots []string, target string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	roots := append([]string{cwd}, writableRoots...)
	for _, root := range roots {
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if !pathWithin(target, absRoot) {
			continue
		}
		if realPathWithin(target, absRoot) {
			return nil
		}
	}
	return fmt.Errorf("write path %s is outside the current workspace and configured additional directories", target)
}

func EnsureWritablePathWithSandbox(cwd string, writableRoots []string, target string, sandbox SandboxConfig) error {
	roots := append([]string(nil), writableRoots...)
	roots = append(roots, sandbox.FilesystemAllowWrite...)
	if err := EnsureWritablePath(cwd, roots, target); err != nil {
		return err
	}
	// Default write protection is independent of the sandbox toggle: the sandbox
	// is off by default, so gating these paths on it left settings files and git
	// hooks writable in the default configuration — a self-privilege-escalation
	// and persistence path for any tool call.
	if deny, ok := matchDenyWritePath(cwd, target, defaultDenyWritePaths()); ok {
		return fmt.Errorf("write path %s is denied by default write protection rule %s", target, deny)
	}
	if sandbox.Enabled {
		if deny, ok := matchDenyWritePath(cwd, target, sandboxOnlyDenyWritePaths()); ok {
			return fmt.Errorf("write path %s is denied by sandbox default denyWrite rule %s", target, deny)
		}
	}
	if deny, ok := matchDenyWritePath(cwd, target, sandbox.FilesystemDenyWrite); ok {
		return fmt.Errorf("write path %s is denied by sandbox filesystem.denyWrite rule %s", target, deny)
	}
	return nil
}

// defaultDenyWritePaths are never writable through a tool call. .git/config is
// included because it can point core.hooksPath elsewhere, which would make
// protecting .git/hooks alone pointless.
func defaultDenyWritePaths() []string {
	return []string{
		".niuma/settings.json",
		".niuma/settings.local.json",
		".niuma-legacy/settings.json",
		".niuma-legacy/settings.local.json",
		".claude/settings.json",
		".claude/settings.local.json",
		".git/hooks",
		".git/config",
	}
}

// sandboxOnlyDenyWritePaths stay writable when the sandbox is off so skill
// authoring keeps working in the default configuration.
func sandboxOnlyDenyWritePaths() []string {
	return []string{".claude/skills"}
}

// DefaultDenyWritePaths and SandboxOnlyDenyWritePaths expose the two lists above
// so the OS sandbox profile builders deny exactly the same targets instead of
// keeping a second copy. internal/sandbox used to hardcode its own list and it
// had already drifted: after the product rename it still named only .claude/, so
// .niuma/settings.json and .niuma-legacy/settings.json were bound writable
// inside the sandbox while the tool layer denied them. Keeping two pattern sets
// in sync failed the same way for the destructive-shell rules — see the note on
// shellRules in internal/permissions/shellrisk.go.
//
// Paths are workspace-relative patterns; callers resolve them against cwd.
func DefaultDenyWritePaths() []string { return defaultDenyWritePaths() }

func SandboxOnlyDenyWritePaths() []string { return sandboxOnlyDenyWritePaths() }

func matchDenyWritePath(cwd, target string, denyWrite []string) (string, bool) {
	absTarget, _ := filepath.Abs(target)
	for _, deny := range denyWrite {
		denyPath, err := ResolvePath(cwd, deny)
		if err != nil {
			continue
		}
		if pathWithin(absTarget, denyPath) || realPathWithin(target, denyPath) {
			return deny, true
		}
	}
	return "", false
}

func realPathWithin(target, root string) bool {
	realTarget, err := realWritePath(target)
	if err != nil {
		return false
	}
	// A declared writable root may not exist yet (for example, first project
	// memory creation). Resolve its nearest existing ancestor exactly as for
	// the target; falling back to the lexical root mismatches symlink parents.
	// Resolution errors still fail closed, and the caller's lexical containment
	// check plus this real-path check continue to reject escaping child links.
	realRoot, err := realWritePath(root)
	if err != nil {
		return false
	}
	return pathWithin(realTarget, realRoot)
}

func realWritePath(target string) (string, error) {
	target, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	var missing []string
	current := target
	for {
		if _, err := os.Lstat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return filepath.Abs(real)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return target, nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathWithin(target, root string) bool {
	target = filepath.Clean(target)
	root = filepath.Clean(root)
	if target == root {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
