package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/promptmode"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
)

// Registering BashOutput / KillShell is a single line each in
// coreRuntimeTools; deleting either leaves every other test green while the
// model loses the only way to poll and to stop a background command
// (AUDIT-P1-13). This asserts they reach the tool list the model is actually
// offered, through the real session-construction path.
func TestNewQuerySessionOffersBackgroundShellTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	session, cleanup, err := newQuerySession(context.Background(), options{
		cwd:           t.TempDir(),
		model:         "test-model",
		maxTurns:      1,
		noPersistence: true,
		promptMode:    promptmode.Code.String(),
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	offered := map[string]string{}
	for _, def := range session.ToolDefinitions() {
		offered[def.Name] = def.Description
	}
	for _, name := range []string{"BashOutput", "KillShell"} {
		description, ok := offered[name]
		if !ok {
			t.Fatalf("%s is not offered to the model; a background command can neither be polled incrementally nor killed", name)
		}
		if strings.TrimSpace(description) == "" {
			t.Fatalf("%s is offered with an empty description", name)
		}
	}
}

// coreRuntimeTools is the list newQuerySession hands to tools.GuardAll, so
// both new tools go through the permission chain: a settings deny rule must
// stop them before they reach the background store.
func TestBackgroundShellToolsGoThroughPermissionChain(t *testing.T) {
	policy := permissions.FromSettings(config.PermissionSettings{Deny: []string{"BashOutput", "KillShell"}})
	registry := tools.NewRegistry(tools.GuardAll(policy, coreRuntimeTools(config.Settings{}, nil, "")...)...)
	for name, input := range map[string]string{
		"BashOutput": `{"bash_id":"bg_denied"}`,
		"KillShell":  `{"shell_id":"bg_denied"}`,
	} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s is missing from coreRuntimeTools", name)
		}
		res := tool.Run(context.Background(), json.RawMessage(input), tools.Context{CWD: t.TempDir()})
		if !res.IsError || !strings.Contains(res.Content, "denied by settings") {
			t.Fatalf("%s bypassed the permission chain: %+v", name, res)
		}
		// Result-size truncation only applies to tools that declare a limit
		// (AUDIT-P1-19); the guard wrapper must forward that declaration.
		if got := tools.EffectiveResultLimit(tool, 0); got != toolresult.DefaultLimit {
			t.Fatalf("EffectiveResultLimit(%s) = %d, want %d", name, got, toolresult.DefaultLimit)
		}
	}
}
