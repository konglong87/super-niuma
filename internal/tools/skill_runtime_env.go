package tools

import (
	"path/filepath"
	"strings"
)

const (
	GolangCCSkillDirEnv = "NIUMA_SKILL_DIR"
	ClaudeSkillDirEnv   = "CLAUDE_SKILL_DIR"
)

// EnvironmentWithSkillRuntime adds stable Skill directory variables to a
// single child-process environment. It does not mutate the input slice and
// intentionally carries no shell-local state between tool calls.
func EnvironmentWithSkillRuntime(base []string, active *SkillRuntime) []string {
	env := append([]string(nil), base...)
	if active == nil || !active.FilesystemBacked {
		return env
	}
	directory := filepath.Clean(strings.TrimSpace(active.Directory))
	if directory == "." || !filepath.IsAbs(directory) {
		return env
	}
	return append(env,
		GolangCCSkillDirEnv+"="+directory,
		ClaudeSkillDirEnv+"="+directory,
	)
}
