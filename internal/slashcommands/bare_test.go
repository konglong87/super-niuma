package slashcommands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBareSlashCommandsUseAllowlistAndExplicitSkills(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_BUNDLED_SKILLS_PATHS", "")
	t.Setenv("NIUMA_BUNDLED_SKILLS_PATHS", "")
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy.md"), []byte("Deploy $ARGUMENTS"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands, err := ListWithOptions(t.TempDir(), "", DiscoveryOptions{Bare: true, ExplicitRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, command := range commands {
		seen[command.Name] = true
	}
	if !seen["help"] || !seen["deploy"] || seen["plugins"] || seen["recap"] {
		t.Fatalf("commands = %+v", commands)
	}
	prompt, ok, err := ResolvePromptWithOptions(context.Background(), t.TempDir(), "/deploy prod", DiscoveryOptions{Bare: true, ExplicitRoots: []string{root}})
	if err != nil || !ok || !strings.Contains(prompt, "Deploy prod") {
		t.Fatalf("prompt=%q ok=%v err=%v", prompt, ok, err)
	}
}
