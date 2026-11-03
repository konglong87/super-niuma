package tools

import (
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

// ShouldSkipSearchDir filters generated or dependency directories from recursive
// discovery tools while still allowing direct reads when the user names a path.
func ShouldSkipSearchDir(root, path string) bool {
	name := filepath.Base(path)
	switch name {
	case ".git", "node_modules", "vendor", ".idea":
		return true
	}
	if strings.HasPrefix(name, ".cache") {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	ownedWorktrees := filepath.ToSlash(filepath.Join(config.CurrentIdentity(root).ConfigDirName, "worktrees"))
	return rel == ".claude/worktrees" ||
		strings.HasPrefix(rel, ".claude/worktrees/") ||
		rel == ownedWorktrees ||
		strings.HasPrefix(rel, ownedWorktrees+"/")
}
