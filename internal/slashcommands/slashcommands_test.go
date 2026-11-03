package slashcommands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/goalcmd"
)

func TestListIncludesBuiltinsSkillsAndCustomCommands(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(project, ".claude", "commands"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "commands", "verify.md"), []byte("---\ndescription: Verify project\n---\nVerify $ARGUMENTS."), 0644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(project, ".claude", "skills", "office-hours")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Product brainstorm\n---\nBrainstorm."), 0644); err != nil {
		t.Fatal(err)
	}

	all, err := List(project, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, command := range all {
		names[command.Name] = command.Source
	}
	for _, command := range Builtins() {
		if names[command.Name] != "builtin" {
			t.Fatalf("builtin slash command %s source = %q, all=%+v", command.Name, names[command.Name], all)
		}
	}
	for name, source := range map[string]string{"verify": "custom", "office-hours": "project"} {
		if names[name] != source {
			t.Fatalf("command %s source = %q, all=%+v", name, names[name], all)
		}
	}

	filtered, err := List(project, "/off")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Name != "office-hours" {
		t.Fatalf("filtered commands = %+v", filtered)
	}
}

func TestGoalBuiltinUsesSharedBeginnerDescription(t *testing.T) {
	for _, command := range Builtins() {
		if command.Name != goalcmd.Name {
			continue
		}
		if command.Description != goalcmd.SlashDescription {
			t.Fatalf("goal description = %q, want %q", command.Description, goalcmd.SlashDescription)
		}
		if !strings.Contains(command.Description, "/goal help") {
			t.Fatalf("goal description lacks help path: %q", command.Description)
		}
		if !strings.Contains(command.Description, goalcmd.HelpDescriptionZH) {
			t.Fatalf("goal description lacks shared Chinese explanation: %q", command.Description)
		}
		return
	}
	t.Fatal("goal builtin not found")
}

func TestThinkingBuiltinIsDiscoverable(t *testing.T) {
	commands := Builtins()
	for _, command := range commands {
		if command.Name == "thinking" {
			if !strings.Contains(strings.ToLower(command.Description), "collapse") {
				t.Fatalf("thinking description = %q", command.Description)
			}
			return
		}
	}
	t.Fatal("thinking builtin is missing")
}

func TestResolvePromptInitMatchesGolden(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	prompt, ok, err := ResolvePrompt(context.Background(), project, "/init")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("/init was not resolved")
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "init_prompt.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if prompt != strings.TrimSuffix(string(golden), "\n") {
		t.Fatalf("prompt mismatch\n--- got ---\n%s\n--- want ---\n%s", prompt, golden)
	}
}

func TestResolvePromptInitUsesConfiguredGuidanceFilename(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".niuma-legacy"))
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(project, ".niuma-legacy"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".niuma-legacy", "settings.json"), []byte(`{
	  "identity": {
	    "productName": "agentx",
	    "guidanceFilename": "agentx.md"
	  }
	}`), 0644); err != nil {
		t.Fatal(err)
	}

	commands, err := List(project, "in")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) == 0 || commands[0].Name != "init" || !strings.Contains(commands[0].Description, "agentx.md") {
		t.Fatalf("commands = %+v", commands)
	}
	prompt, ok, err := ResolvePrompt(context.Background(), project, "/init")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(prompt, "`agentx.md`") || !strings.Contains(prompt, "guidance to agentx") {
		t.Fatalf("prompt did not use configured identity ok=%v:\n%s", ok, prompt)
	}
	if strings.Contains(prompt, "# niuma.md") {
		t.Fatalf("prompt used hard-coded guidance filename:\n%s", prompt)
	}
}

func TestResolvePromptInitUsesConfiguredDetailLevel(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".niuma-legacy"))
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(project, ".niuma-legacy"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".niuma-legacy", "settings.json"), []byte(`{
	  "init": {
	    "detailLevel": "minimal"
	  }
	}`), 0644); err != nil {
		t.Fatal(err)
	}

	prompt, ok, err := ResolvePrompt(context.Background(), project, "/init")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(prompt, "`minimal`") || strings.Contains(prompt, "`balanced`") {
		t.Fatalf("prompt did not use minimal detail level ok=%v:\n%s", ok, prompt)
	}

	if err := os.WriteFile(filepath.Join(project, ".niuma-legacy", "settings.json"), []byte(`{
	  "init": {
	    "detailLevel": "detailed"
	  }
	}`), 0644); err != nil {
		t.Fatal(err)
	}
	prompt, ok, err = ResolvePrompt(context.Background(), project, "/init")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(prompt, "`detailed`") || !strings.Contains(prompt, "stable public types") {
		t.Fatalf("prompt did not use detailed detail level ok=%v:\n%s", ok, prompt)
	}
}

func TestListSortsBuiltinsBeforeCustomCommands(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(project, ".claude", "commands"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "commands", "attach-local.md"), []byte("Attach local."), 0644); err != nil {
		t.Fatal(err)
	}

	commands, err := List(project, "attach")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) < 2 || commands[0].Name != "attach" || commands[0].Source != "builtin" {
		t.Fatalf("commands not sorted with builtin first: %+v", commands)
	}
}
