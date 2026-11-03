package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/permissions"
)

func TestResolvePath(t *testing.T) {
	tmp := t.TempDir()
	got, err := ResolvePath(tmp, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "a", "b.txt")
	if got != want {
		t.Fatalf("ResolvePath() = %q, want %q", got, want)
	}
}

func TestEnsureWritablePath(t *testing.T) {
	tmp := t.TempDir()
	other := t.TempDir()
	if err := EnsureWritablePath(tmp, nil, filepath.Join(tmp, "a.txt")); err != nil {
		t.Fatalf("workspace path denied: %v", err)
	}
	if err := EnsureWritablePath(tmp, nil, filepath.Join(other, "a.txt")); err == nil {
		t.Fatal("outside path allowed, want denied")
	}
	if err := EnsureWritablePath(tmp, []string{other}, filepath.Join(other, "a.txt")); err != nil {
		t.Fatalf("additional root path denied: %v", err)
	}
}

func TestEnsureWritablePathWithSandboxPolicy(t *testing.T) {
	tmp := t.TempDir()
	other := t.TempDir()
	if err := EnsureWritablePathWithSandbox(tmp, nil, filepath.Join(other, "allowed.txt"), SandboxConfig{FilesystemAllowWrite: []string{other}}); err != nil {
		t.Fatalf("sandbox allowWrite path denied: %v", err)
	}
	if err := EnsureWritablePathWithSandbox(tmp, nil, filepath.Join(tmp, ".git", "config"), SandboxConfig{Enabled: true}); err == nil {
		t.Fatal("default sensitive sandbox denyWrite path allowed")
	}
	if err := EnsureWritablePathWithSandbox(tmp, nil, filepath.Join(tmp, "private", "secret.txt"), SandboxConfig{FilesystemDenyWrite: []string{"private"}}); err == nil {
		t.Fatal("explicit sandbox denyWrite path allowed")
	}
}

func TestRegistryFilterPolicyAppliesAllowAndDeny(t *testing.T) {
	registry := NewRegistry(namedTool{name: "Read"}, namedTool{name: "Bash"}, namedTool{name: "TaskOutput"})
	filtered := registry.FilterPolicy([]string{"Read", "Bash", "TaskOutput"}, []string{"Bash", "TaskOutput"})
	var names []string
	for _, tool := range filtered.List() {
		names = append(names, tool.Name())
	}
	if len(names) != 1 || names[0] != "Read" {
		t.Fatalf("filtered tools = %+v", names)
	}
}

func TestRegistryCloneDoesNotPolluteParent(t *testing.T) {
	parent := NewRegistry(namedTool{name: "Read"})
	child := parent.Clone()
	child.Register(namedTool{name: "mcp__local__lookup"})
	if _, ok := parent.Get("mcp__local__lookup"); ok {
		t.Fatal("child registration polluted parent registry")
	}
	if _, ok := child.Get("Read"); !ok {
		t.Fatal("clone did not preserve existing tool")
	}
}

func TestEffectiveResultLimitUsesLowerToolLimit(t *testing.T) {
	tool := limitedTool{namedTool: namedTool{name: "Search"}, limit: 20_000}
	if got := EffectiveResultLimit(tool, 200_000); got != 20_000 {
		t.Fatalf("EffectiveResultLimit() = %d, want 20000", got)
	}
	if got := EffectiveResultLimit(tool, 10_000); got != 10_000 {
		t.Fatalf("EffectiveResultLimit() = %d, want configured lower limit", got)
	}
	if got := EffectiveResultLimit(namedTool{name: "Plain"}, 200_000); got != 200_000 {
		t.Fatalf("EffectiveResultLimit() = %d, want configured limit", got)
	}
}

func TestEffectiveResultLimitSkipsBudgetForBudgetSkippingTools(t *testing.T) {
	tool := budgetSkippingTool{namedTool{name: "Read"}}
	if got := EffectiveResultLimit(tool, 200_000); got != 0 {
		t.Fatalf("EffectiveResultLimit() = %d, want no individual result limit", got)
	}
}

func TestEffectiveResultLimitCapsDeclaredLimitAtClaudeCodeDefault(t *testing.T) {
	tool := limitedTool{namedTool: namedTool{name: "LargeDefault"}, limit: 100_000}
	if got := EffectiveResultLimit(tool, 200_000); got != 50_000 {
		t.Fatalf("EffectiveResultLimit() = %d, want Claude Code default cap 50000", got)
	}
	if got := EffectiveResultLimit(tool, 32_000); got != 32_000 {
		t.Fatalf("EffectiveResultLimit() = %d, want configured lower limit", got)
	}
}

func TestGuardPreservesResultSizeLimiter(t *testing.T) {
	tool := limitedTool{namedTool: namedTool{name: "Search"}, limit: 20_000}
	guarded := Guard(tool, permissions.Policy{DefaultMode: "allow"})
	if got := EffectiveResultLimit(guarded, 200_000); got != 20_000 {
		t.Fatalf("EffectiveResultLimit(Guard(tool)) = %d, want 20000", got)
	}
}

func TestRegistryToolResultBudgetSkipNames(t *testing.T) {
	registry := NewRegistry(namedTool{name: "Bash"}, budgetSkippingTool{namedTool{name: "Read"}})
	got := registry.ToolResultBudgetSkipNames()
	if len(got) != 1 || !got["Read"] {
		t.Fatalf("skip names = %+v, want Read only", got)
	}

	guarded := NewRegistry(Guard(budgetSkippingTool{namedTool{name: "Read"}}, permissions.Policy{DefaultMode: "allow"}))
	got = guarded.ToolResultBudgetSkipNames()
	if len(got) != 1 || !got["Read"] {
		t.Fatalf("guarded skip names = %+v, want Read only", got)
	}
}

func TestGuardAppliesAgentPermissionModeWithoutBypassingSessionDeny(t *testing.T) {
	tool := Guard(namedTool{name: "Write"}, permissions.Policy{DefaultMode: "deny"})
	prompted := false
	res := tool.Run(context.Background(), json.RawMessage(`{}`), Context{
		AgentPolicy: &AgentPolicy{Name: "writer", PermissionMode: "ask"},
		PermissionPrompt: func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
			prompted = true
			return PermissionPromptResponse{Allowed: true, Destination: "once"}
		},
	})
	if res.IsError || !prompted {
		t.Fatalf("ask mode result=%+v prompted=%v", res, prompted)
	}

	res = tool.Run(context.Background(), json.RawMessage(`{}`), Context{
		SessionDeny: []string{"Write"},
		AgentPolicy: &AgentPolicy{Name: "writer", PermissionMode: "bypassPermissions"},
	})
	if !res.IsError {
		t.Fatalf("session deny was bypassed: %+v", res)
	}
}

type namedTool struct {
	name string
}

type limitedTool struct {
	namedTool
	limit int
}

type budgetSkippingTool struct {
	namedTool
}

func (l limitedTool) MaxResultSizeChars() int { return l.limit }

func (b budgetSkippingTool) SkipToolResultBudget() bool { return true }

func (n namedTool) Name() string {
	return n.name
}

func (n namedTool) Description() string {
	return n.name
}

func (n namedTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (n namedTool) Run(context.Context, json.RawMessage, Context) Result {
	return Result{Content: "ok"}
}

// AUDIT-P0-05: the default write protection must not depend on the sandbox
// toggle, which is off by default.
func TestEnsureWritablePathDefaultProtectionWithSandboxDisabled(t *testing.T) {
	tmp := t.TempDir()
	off := SandboxConfig{}
	for _, rel := range []string{
		".claude/settings.json",
		".claude/settings.local.json",
		".niuma-legacy/settings.json",
		".niuma-legacy/settings.local.json",
		".git/config",
		".git/hooks/pre-commit",
	} {
		target := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := EnsureWritablePathWithSandbox(tmp, nil, target, off); err == nil {
			t.Fatalf("%s writable with sandbox disabled", rel)
		}
	}
	if err := EnsureWritablePathWithSandbox(tmp, nil, filepath.Join(tmp, "main.go"), off); err != nil {
		t.Fatalf("ordinary workspace path denied: %v", err)
	}
	// Skill authoring stays possible while the sandbox is off, and is blocked
	// once it is on.
	skill := filepath.Join(tmp, ".claude", "skills", "demo", "SKILL.md")
	if err := EnsureWritablePathWithSandbox(tmp, nil, skill, off); err != nil {
		t.Fatalf("skill write denied with sandbox disabled: %v", err)
	}
	if err := EnsureWritablePathWithSandbox(tmp, nil, skill, SandboxConfig{Enabled: true}); err == nil {
		t.Fatal("skill write allowed with sandbox enabled")
	}
}

// AUDIT-P0-01: a runtime acceptEdits mode must not collapse into a full bypass.
func TestGuardRuntimeAcceptEditsDoesNotBypassBash(t *testing.T) {
	basePolicy := permissions.Policy{DefaultMode: "ask", Deny: []string{"Write(/etc/**)"}}
	acceptEdits := func() string { return "acceptEdits" }

	write := Guard(namedTool{name: "Write"}, basePolicy)
	res := write.Run(context.Background(), json.RawMessage(`{"file_path":"main.go"}`), Context{RuntimePermissionMode: acceptEdits})
	if res.IsError {
		t.Fatalf("acceptEdits blocked an ordinary file edit: %+v", res)
	}
	res = write.Run(context.Background(), json.RawMessage(`{"file_path":"/etc/apt/sources.list.d/x.list"}`), Context{RuntimePermissionMode: acceptEdits})
	if !res.IsError {
		t.Fatalf("acceptEdits bypassed a deny subtree rule: %+v", res)
	}

	bash := Guard(namedTool{name: "Bash"}, basePolicy)
	prompted := false
	res = bash.Run(context.Background(), json.RawMessage(`{"command":"rm -rf /"}`), Context{
		RuntimePermissionMode: acceptEdits,
		PermissionPrompt: func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
			prompted = true
			return PermissionPromptResponse{Allowed: false, Destination: "once", Reason: "denied by user"}
		},
	})
	if !prompted || !res.IsError {
		t.Fatalf("acceptEdits released Bash: prompted=%v result=%+v", prompted, res)
	}
}

// AUDIT-P0-01: --dangerously-skip-permissions (TODO-014) keeps skipping the
// classifier, and switching the runtime mode away from a permissive one revokes
// the session bypass.
func TestGuardBypassScope(t *testing.T) {
	bypassPolicy := permissions.Policy{DefaultMode: "allow", Allow: []string{"*"}, Bypass: true}
	bash := Guard(namedTool{name: "Bash"}, bypassPolicy)
	dangerous := json.RawMessage(`{"command":"rm -rf /"}`)

	failPrompt := func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
		t.Fatal("bypass mode prompted for permission")
		return PermissionPromptResponse{}
	}
	if res := bash.Run(context.Background(), dangerous, Context{PermissionPrompt: failPrompt}); res.IsError {
		t.Fatalf("bypass policy blocked a dangerous command: %+v", res)
	}
	if res := bash.Run(context.Background(), dangerous, Context{
		RuntimePermissionMode: func() string { return "bypassPermissions" },
		PermissionPrompt:      failPrompt,
	}); res.IsError {
		t.Fatalf("runtime bypassPermissions blocked a dangerous command: %+v", res)
	}
	// Session deny still wins over the bypass.
	if res := bash.Run(context.Background(), dangerous, Context{SessionDeny: []string{"Bash"}}); !res.IsError {
		t.Fatalf("session deny was bypassed: %+v", res)
	}
	// Narrowing the runtime mode revokes the session bypass.
	if res := bash.Run(context.Background(), dangerous, Context{
		RuntimePermissionMode: func() string { return "deny" },
	}); !res.IsError {
		t.Fatalf("deny mode still bypassed: %+v", res)
	}
}
