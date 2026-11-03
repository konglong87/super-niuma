package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestParseArgsBareRuntimeProfile(t *testing.T) {
	opts, rest, err := parseArgs([]string{"--bare", "--tools", "Read,Task", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.runtimeProfile.IsBare() || !opts.toolsSpecified || strings.Join(opts.enabledTools, ",") != "Read,Task" || len(rest) != 0 {
		t.Fatalf("opts=%+v rest=%v", opts, rest)
	}
}

func TestValidateBareRuntimeProfileCombinations(t *testing.T) {
	bare := options{runtimeProfile: runtimeprofile.ProfileBare}
	if err := validateRuntimeProfileCombination(bare, nil, false, commandSpec{}); err != nil {
		t.Fatal(err)
	}
	chat := bare
	chat.promptMode = "chat"
	if err := validateRuntimeProfileCombination(chat, nil, false, commandSpec{}); err == nil || !strings.Contains(err.Error(), "prompt-mode code") {
		t.Fatalf("chat error = %v", err)
	}
	review, ok := lookupCommand("review")
	if !ok {
		t.Fatal("review command missing")
	}
	if err := validateRuntimeProfileCombination(bare, []string{"review"}, true, review); err == nil || !strings.Contains(err.Error(), "query sessions") {
		t.Fatalf("review error = %v", err)
	}
}

func TestPrepareBareRuntimeSettingsKeepsOnlyExplicitContributors(t *testing.T) {
	settings := config.Settings{
		Hooks:                 map[string][]config.HookCommand{hooks.SessionStart: {{Command: "inherited-hook"}}},
		MCPServers:            map[string]config.MCPServerConfig{"inherited": {Command: "inherited-mcp"}},
		AdditionalDirectories: []string{"inherited-dir"},
	}
	opts := options{
		runtimeProfile: runtimeprofile.ProfileBare,
		settingsInputs: []string{`{"hooks":{"SessionStart":[{"command":"explicit-hook"}]},"mcpServers":{"explicit":{"command":"explicit-mcp"}},"additionalDirectories":["explicit-dir"]}`},
	}
	if err := prepareRuntimeSettings(&settings, opts); err != nil {
		t.Fatal(err)
	}
	if len(settings.Hooks) != 0 || len(settings.MCPServers) != 1 || settings.MCPServers["explicit"].Command != "explicit-mcp" || strings.Join(settings.AdditionalDirectories, ",") != "explicit-dir" {
		t.Fatalf("settings = %+v", settings)
	}
}

func TestBareRuntimeToolsAreMinimalAndToolsFlagOnlyNarrows(t *testing.T) {
	all := bareRuntimeTools()
	if len(all) != 3 || all[0].Name() != runtimeprofile.BareToolRead || all[1].Name() != runtimeprofile.BareToolEdit || all[2].Name() != runtimeprofile.BareToolBash {
		t.Fatalf("bare tools = %v", toolNames(all))
	}
	filtered := filterRuntimeTools(all, options{toolsSpecified: true, enabledTools: []string{"Task", "Read"}})
	if len(filtered) != 1 || filtered[0].Name() != "Read" {
		t.Fatalf("filtered tools = %v", toolNames(filtered))
	}
}

func TestBareDisablesInteractiveSlashOutsideAllowlist(t *testing.T) {
	handled, _, err := handleInteractiveSlash(context.Background(), options{runtimeProfile: runtimeprofile.ProfileBare}, "/plugins", nil, nil, nil)
	if !handled || err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}

func TestBareHelpVersionAndSubcommandValidation(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--bare", "--help"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--bare") {
		t.Fatalf("help = %q", out.String())
	}
	out.Reset()
	if err := Run(context.Background(), []string{"--bare", "--version"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "niuma") {
		t.Fatalf("version = %q", out.String())
	}
	if err := Run(context.Background(), []string{"--bare", "review"}, &out, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "query sessions") {
		t.Fatalf("review error = %v", err)
	}
}

func TestBareTUIEnrichmentIsDisabledWithoutChangingDefault(t *testing.T) {
	bare := tuiEnrichmentForOptions(options{runtimeProfile: runtimeprofile.ProfileBare, cwd: t.TempDir()}, nil)
	if bare.runAwayRecap != nil || bare.runNextSteps != nil || bare.runPostTurnRecap != nil || bare.awayRecapDelay != 0 || bare.watchBackground != nil {
		t.Fatalf("bare enrichment = %+v", bare)
	}
	defaults := tuiEnrichmentForOptions(options{cwd: t.TempDir()}, nil)
	if defaults.runNextSteps == nil || defaults.runPostTurnRecap == nil || defaults.awayRecapDelay <= 0 || defaults.watchBackground == nil {
		t.Fatalf("default enrichment = %+v", defaults)
	}
}

func TestBareCLIRequestHasMinimalToolsAndNoImplicitContributors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_BUNDLED_SKILLS_PATHS", "")
	t.Setenv("NIUMA_BUNDLED_SKILLS_PATHS", "")
	t.Setenv("CLAUDE_CODE_SIMPLE", "")
	t.Setenv("NIUMA_SIMPLE", "")
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("IMPLICIT_MEMORY_SENTINEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	hookSentinel := filepath.Join(cwd, "hook-ran")
	settingsJSON, err := json.Marshal(config.Settings{Hooks: map[string][]config.HookCommand{
		hooks.SessionStart: {{Command: "touch " + hookSentinel}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	requestCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requestCh <- body
		writeAnthropicTextStream(t, w, "ok")
	}))
	defer server.Close()
	configureTestProvider(t, server.URL, "test-key")
	var stdout bytes.Buffer
	err = Run(context.Background(), []string{
		"--bare", "--cwd", cwd, "--settings", string(settingsJSON),
		"--no-session-persistence", "-p", "hello",
	}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON := <-requestCh
	var request struct {
		System json.RawMessage `json:"system"`
		Tools  []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "Read,Edit,Bash" {
		t.Fatalf("tools = %v", names)
	}
	requestText := string(requestJSON)
	for _, forbidden := range []string{"IMPLICIT_MEMORY_SENTINEL", "Git Snapshot", "skills_catalog"} {
		if strings.Contains(requestText, forbidden) {
			t.Fatalf("request contains implicit contributor %q: %s", forbidden, requestText)
		}
	}
	if _, err := os.Stat(hookSentinel); !os.IsNotExist(err) {
		t.Fatalf("hook sentinel exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(config.ProjectSettingsPath(cwd, false)); !os.IsNotExist(err) {
		t.Fatalf("bare run materialized project settings: %v", err)
	}

	defaultCWD := t.TempDir()
	if err := Run(context.Background(), []string{
		"--cwd", defaultCWD, "--no-session-persistence", "-p", "hello",
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	defaultRequestJSON := <-requestCh
	var defaultRequest struct {
		System json.RawMessage `json:"system"`
		Tools  []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(defaultRequestJSON, &defaultRequest); err != nil {
		t.Fatal(err)
	}
	if len(request.System) >= len(defaultRequest.System) || len(defaultRequest.Tools) <= len(request.Tools) {
		t.Fatalf("bare/default request sizes: system=%d/%d tools=%d/%d", len(request.System), len(defaultRequest.System), len(request.Tools), len(defaultRequest.Tools))
	}
}

func toolNames(items []tools.Tool) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Name())
	}
	return out
}
