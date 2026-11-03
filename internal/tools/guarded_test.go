package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/permissions"
)

type noopTool struct{}

func (noopTool) Name() string                 { return "Write" }
func (noopTool) Description() string          { return "" }
func (noopTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (noopTool) Run(context.Context, json.RawMessage, Context) Result {
	return Result{Content: "ran"}
}

type readOnlyNoopTool struct{ noopTool }

func (readOnlyNoopTool) Name() string { return "Read" }
func (readOnlyNoopTool) ExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{Concurrency: ConcurrencyReadOnly}
}

func TestGuardDeniesTool(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{Deny: []string{"Write"}})
	res := Guard(noopTool{}, policy).Run(context.Background(), nil, Context{})
	if !res.IsError {
		t.Fatalf("guard result = %+v, want error", res)
	}
}

func TestGuardAuditsPermissionDecision(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{Allow: []string{"Write:file.txt"}, DefaultMode: "deny"})
	var audits []PermissionAudit
	input := json.RawMessage(`{"file_path":"file.txt"}`)
	res := Guard(noopTool{}, policy).Run(context.Background(), input, Context{
		PermissionAudit: func(audit PermissionAudit) {
			audits = append(audits, audit)
		},
	})
	if res.IsError || len(audits) != 1 {
		t.Fatalf("res=%+v audits=%+v", res, audits)
	}
	if !audits[0].Allowed || audits[0].Rule != "Write:file.txt" || audits[0].Request != "file.txt" {
		t.Fatalf("audit = %+v", audits[0])
	}
}

func TestGuardUsesPermissionPrompt(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{DefaultMode: "ask"})
	res := Guard(noopTool{}, policy).Run(context.Background(), json.RawMessage(`{"file_path":"a.txt"}`), Context{
		PermissionPrompt: func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
			return PermissionPromptResponse{Allowed: true, Reason: "ok"}
		},
	})
	if res.IsError || res.Content != "ran" {
		t.Fatalf("result = %+v", res)
	}
}

func TestGuardAppliesSessionPermissionAllow(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{DefaultMode: "deny"})
	res := Guard(noopTool{}, policy).Run(context.Background(), json.RawMessage(`{"file_path":"a.txt"}`), Context{
		SessionAllow: []string{"Write:a.txt"},
	})
	if res.IsError || res.Content != "ran" {
		t.Fatalf("result = %+v", res)
	}
}

func TestGuardRuntimePermissionAllowBypassesPrompt(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{DefaultMode: "ask"})
	prompted := false
	res := Guard(noopTool{}, policy).Run(context.Background(), json.RawMessage(`{"file_path":"a.txt"}`), Context{
		RuntimePermissionMode: func() string { return "allow" },
		PermissionPrompt: func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
			prompted = true
			return PermissionPromptResponse{Allowed: false, Reason: "should not ask"}
		},
	})
	if res.IsError || res.Content != "ran" || prompted {
		t.Fatalf("res=%+v prompted=%v", res, prompted)
	}
}

func TestGuardPersistsPromptDecision(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{DefaultMode: "ask"})
	var updates []PermissionUpdate
	res := Guard(noopTool{}, policy).Run(context.Background(), json.RawMessage(`{"file_path":"a.txt"}`), Context{
		PermissionPrompt: func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
			return PermissionPromptResponse{Allowed: true, Destination: "session"}
		},
		PermissionUpdate: func(update PermissionUpdate) error {
			updates = append(updates, update)
			return nil
		},
	})
	if res.IsError || len(updates) != 1 || updates[0].Decision != "allow" || updates[0].Rule != "Write:a.txt" || updates[0].Destination != "session" {
		t.Fatalf("res=%+v updates=%+v", res, updates)
	}
}

func TestGuardSkipPermissionsBypassesPrompt(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{Allow: []string{"*"}, DefaultMode: "allow", Bypass: true})
	prompted := false
	res := Guard(bashLikeTool{}, policy).Run(context.Background(), json.RawMessage(`{"command":"rm -rf /tmp/example"}`), Context{
		PermissionPrompt: func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
			prompted = true
			return PermissionPromptResponse{Allowed: false, Reason: "should not ask"}
		},
	})
	if prompted || res.IsError || res.Content != "ran" {
		t.Fatalf("prompted=%v res=%+v", prompted, res)
	}
}

type bashLikeTool struct{}

func (bashLikeTool) Name() string                 { return "Bash" }
func (bashLikeTool) Description() string          { return "bash" }
func (bashLikeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (bashLikeTool) Run(context.Context, json.RawMessage, Context) Result {
	return Result{Content: "ran"}
}

func TestGuardHonorsActiveSkillAllowedTools(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{})
	res := Guard(noopTool{}, policy).Run(context.Background(), json.RawMessage(`{"file_path":"a.txt"}`), Context{
		ActiveSkill: &SkillRuntime{Name: "read-only", AllowedTools: []string{"Read"}},
	})
	if !res.IsError || !strings.Contains(res.Content, "not allowed by active skill") {
		t.Fatalf("result = %+v", res)
	}
}

func TestGuardParallelSafeRequiresReadOnlyAndPreapprovedPermission(t *testing.T) {
	input := json.RawMessage(`{"file_path":"a.txt"}`)
	tests := []struct {
		name    string
		tool    Tool
		policy  permissions.Policy
		context Context
		want    bool
	}{
		{name: "read only allow", tool: readOnlyNoopTool{}, policy: permissions.FromSettings(config.PermissionSettings{Allow: []string{"Read:a.txt"}, DefaultMode: "deny"}), want: true},
		{name: "read only ask", tool: readOnlyNoopTool{}, policy: permissions.FromSettings(config.PermissionSettings{Ask: []string{"Read"}, DefaultMode: "ask"}), want: false},
		{name: "read only deny", tool: readOnlyNoopTool{}, policy: permissions.FromSettings(config.PermissionSettings{Deny: []string{"Read"}}), want: false},
		{name: "active skill deny", tool: readOnlyNoopTool{}, policy: permissions.FromSettings(config.PermissionSettings{Allow: []string{"Read:a.txt"}}), context: Context{ActiveSkill: &SkillRuntime{Name: "write-only", AllowedTools: []string{"Write"}}}, want: false},
		{name: "serial tool", tool: noopTool{}, policy: permissions.FromSettings(config.PermissionSettings{Allow: []string{"Write:a.txt"}}), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry(Guard(tt.tool, tt.policy))
			if got := registry.ParallelSafe(tt.tool.Name(), input, tt.context); got != tt.want {
				t.Fatalf("ParallelSafe() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGuardParallelSafetyDoesNotMutateSharedPolicy(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{
		Allow:       []string{"Read:*"},
		DefaultMode: "deny",
	})
	first := guardedTool{inner: readOnlyNoopTool{}, policy: policy}
	second := guardedTool{inner: readOnlyNoopTool{}, policy: policy}
	toolContext := Context{
		SessionAllow:          []string{"Read:a.txt"},
		RuntimePermissionMode: func() string { return "allow" },
	}
	input := json.RawMessage(`{"file_path":"a.txt"}`)

	const calls = 64
	var wait sync.WaitGroup
	for i := 0; i < calls; i++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			if !first.ParallelSafe(input, toolContext) {
				t.Error("first guarded read unexpectedly unsafe")
			}
		}()
		go func() {
			defer wait.Done()
			if !second.ParallelSafe(input, toolContext) {
				t.Error("second guarded read unexpectedly unsafe")
			}
		}()
	}
	wait.Wait()
	if _, mutated := policy.RuleSources["defaultMode"]; mutated {
		t.Fatalf("shared policy RuleSources was mutated: %+v", policy.RuleSources)
	}
}
