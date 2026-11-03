package tools

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestEnvironmentWithSkillRuntimeAddsStableDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "skill")
	base := []string{"BASE=1"}
	env := EnvironmentWithSkillRuntime(base, &SkillRuntime{
		Directory:        directory,
		FilesystemBacked: true,
	})
	if !slices.Contains(env, GolangCCSkillDirEnv+"="+directory) {
		t.Fatalf("environment missing %s: %v", GolangCCSkillDirEnv, env)
	}
	if !slices.Contains(env, ClaudeSkillDirEnv+"="+directory) {
		t.Fatalf("environment missing %s: %v", ClaudeSkillDirEnv, env)
	}
	if len(base) != 1 {
		t.Fatalf("base environment mutated: %v", base)
	}
}

func TestEnvironmentWithSkillRuntimeRejectsInlineAndRelativeDirectories(t *testing.T) {
	tests := []*SkillRuntime{
		nil,
		{Directory: "/tmp/inline", FilesystemBacked: false},
		{Directory: "relative/skill", FilesystemBacked: true},
	}
	for _, active := range tests {
		if got := EnvironmentWithSkillRuntime([]string{"BASE=1"}, active); !slices.Equal(got, []string{"BASE=1"}) {
			t.Fatalf("EnvironmentWithSkillRuntime(%+v) = %v", active, got)
		}
	}
}
