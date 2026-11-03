package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestSkillTool(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(project, ".claude", "skills", "demo")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Demo\n\nInstructions"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"name": "demo"})
	res := New().Run(context.Background(), input, tools.Context{CWD: project})
	if res.IsError || !strings.Contains(res.Content, "Skill demo loaded") {
		t.Fatalf("result = %+v", res)
	}
	if len(res.ContextMessages) != 1 || !strings.Contains(res.ContextMessages[0].Content[0].Text, "Instructions") {
		t.Fatalf("context messages = %+v", res.ContextMessages)
	}
}

func TestSkillToolDescriptionRequiresMatchedSkillFirst(t *testing.T) {
	desc := New().Description()
	for _, want := range []string{"matches the user's request", "before taking task-specific actions"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}

func TestSkillToolClaudeCompatibleSchemaAndInput(t *testing.T) {
	t.Setenv("NIUMA_PROMPT_PROFILE", "claude-compatible")
	desc := New().Description()
	for _, want := range []string{"slash command", "BLOCKING REQUIREMENT", "NEVER mention a skill without actually calling this tool"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("compatible description missing %q:\n%s", want, desc)
		}
	}
	schema := string(New().InputSchema())
	for _, want := range []string{`"$schema"`, `"skill"`, `"args"`, `"required": ["skill"]`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("compatible schema missing %q:\n%s", want, schema)
		}
	}
	for _, notWant := range []string{`"name"`, `"prompt"`} {
		if strings.Contains(schema, notWant) {
			t.Fatalf("compatible schema leaked %q:\n%s", notWant, schema)
		}
	}

	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(project, ".claude", "skills", "demo")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Demo\n\nCompatible instructions"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"skill": "demo", "args": "ignored for inline skills"})
	res := New().Run(context.Background(), input, tools.Context{CWD: project})
	if res.IsError || res.Content != "Launching skill: demo" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.ContextMessages) != 1 || !strings.Contains(res.ContextMessages[0].Content[0].Text, "Compatible instructions") {
		t.Fatalf("context messages = %+v", res.ContextMessages)
	}
}

type forkStreamer struct {
	system string
	model  string
	prompt string
}

func (f *forkStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.system = req.System
	f.model = req.Model
	f.prompt = req.Messages[0].Content[0].Text
	_ = cb.OnText("forked")
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "forked"}}}}, nil
}

func TestSkillToolRunsForkedSkill(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(project, ".claude", "skills", "forked")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nmodel: custom-model\ncontext: fork\n---\n# Forked\n\nInstructions"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &forkStreamer{}
	input, _ := json.Marshal(map[string]string{"name": "forked", "prompt": "do skill"})
	res := New(streamer, "base-model").Run(context.Background(), input, tools.Context{CWD: project})
	if res.IsError || res.Content != "forked" {
		t.Fatalf("result = %+v", res)
	}
	if streamer.model != "custom-model" || streamer.prompt != "do skill" || !strings.Contains(streamer.system, "Instructions") {
		t.Fatalf("model=%q prompt=%q system=%q", streamer.model, streamer.prompt, streamer.system)
	}
}
