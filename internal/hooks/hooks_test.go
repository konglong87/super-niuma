package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestRunner(t *testing.T) {
	tmp := t.TempDir()
	runner := New(map[string][]config.HookCommand{
		PreToolUse: {{Command: "printf $CLAUDE_TOOL_NAME > hook.out"}},
	})
	if err := runner.Run(context.Background(), PreToolUse, tmp, "Read"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "hook.out"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "Read" {
		t.Fatalf("hook output = %q", data)
	}
}

func TestRunnerJSONResult(t *testing.T) {
	tmp := t.TempDir()
	runner := New(map[string][]config.HookCommand{
		PreToolUse: {{Command: `printf '{"permissionDecision":"deny","permissionDecisionReason":"blocked","updatedInput":{"text":"new"},"additionalContext":"ctx","hookSpecificOutput":{"hookEventName":"WorktreeCreate","worktreePath":"/tmp/wt"}}'`}},
	})
	result, err := runner.RunWithPayload(context.Background(), PreToolUse, tmp, Payload{
		ToolName: "Echo",
		Input:    json.RawMessage(`{"text":"old"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PermissionDecision != "deny" || result.PermissionDecisionReason != "blocked" || !strings.Contains(string(result.UpdatedInput), "new") || result.AdditionalContext != "ctx" || result.HookSpecificOutput.WorktreePath != "/tmp/wt" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunnerSubagentStartPayloadFields(t *testing.T) {
	tmp := t.TempDir()
	runner := New(map[string][]config.HookCommand{
		SubagentStart: {{Command: `cat > subagent-start.json`}},
	})
	_, err := runner.RunWithPayload(context.Background(), SubagentStart, tmp, Payload{
		TaskID:               42,
		AgentID:              "agent-42",
		AgentType:            "reviewer",
		AgentName:            "reviewer",
		SessionID:            "sess",
		AgentTranscriptPath:  "/tmp/transcript.jsonl",
		LastAssistantMessage: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "subagent-start.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("payload = %s: %v", string(data), err)
	}
	if payload.Event != SubagentStart || payload.AgentID != "agent-42" || payload.AgentType != "reviewer" || payload.AgentTranscriptPath != "/tmp/transcript.jsonl" || payload.LastAssistantMessage != "hello" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunnerWorktreeCreatePayloadFields(t *testing.T) {
	tmp := t.TempDir()
	runner := New(map[string][]config.HookCommand{
		WorktreeCreate: {{Command: `cat > worktree-create.json; printf /tmp/hook-worktree`}},
	})
	result, err := runner.RunWithPayload(context.Background(), WorktreeCreate, tmp, Payload{Name: "agent-hook"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "/tmp/hook-worktree" {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "worktree-create.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("payload = %s: %v", string(data), err)
	}
	if payload.Event != WorktreeCreate || payload.Name != "agent-hook" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRunnerMatcherFiltersByToolAndInput(t *testing.T) {
	tmp := t.TempDir()
	runner := New(map[string][]config.HookCommand{
		PreToolUse: {
			{Matcher: "Bash:go test*", Command: "printf matched > hook.out"},
			{Matcher: "Write", Command: "printf wrong > hook.out"},
		},
	})
	result, err := runner.RunWithPayload(context.Background(), PreToolUse, tmp, Payload{
		ToolName: "Bash",
		Input:    json.RawMessage(`{"command":"go test ./..."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PermissionDecision != "" || result.Message != "" || len(result.UpdatedInput) != 0 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "hook.out"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "matched" {
		t.Fatalf("hook output = %q", string(data))
	}
}

func TestValidateRejectsInvalidMatcherSchema(t *testing.T) {
	err := Validate(map[string][]config.HookCommand{
		PreToolUse: {{Command: "printf ok", Matcher: "Bash", Tool: "Read"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot set matcher") {
		t.Fatalf("err = %v", err)
	}
}

// TestRunnerDoesNotLeakSecretsToHookCommands locks AUDIT-P1-16. Hooks can come from
// a project-level .claude/settings.json, so cloning a repo was enough to hand an
// attacker every credential in the parent environment.
func TestRunnerDoesNotLeakSecretsToHookCommands(t *testing.T) {
	tmp := t.TempDir()
	for _, key := range secretEnvFixture {
		t.Setenv(key, "leaked-"+key)
	}
	t.Setenv("GOPATH", "/keep/gopath")

	runner := New(map[string][]config.HookCommand{
		PreToolUse: {{Command: `printf '%s' "$ANTHROPIC_API_KEY|$GITHUB_TOKEN|$AWS_SECRET_ACCESS_KEY|$OPENAI_API_KEY|$MY_DB_PASSWORD|$GOPATH" > hook.out`}},
	})
	if err := runner.Run(context.Background(), PreToolUse, tmp, "Read"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "hook.out"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "|||||/keep/gopath" {
		t.Fatalf("hook saw %q; secrets must be stripped and GOPATH kept", got)
	}
}

var secretEnvFixture = []string{
	"ANTHROPIC_API_KEY",
	"GITHUB_TOKEN",
	"AWS_SECRET_ACCESS_KEY",
	"OPENAI_API_KEY",
	"MY_DB_PASSWORD",
}
