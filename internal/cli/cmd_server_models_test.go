package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestServerModelCatalogIgnoresWorkspaceConfig(t *testing.T) {
	global := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", global)
	if err := os.WriteFile(filepath.Join(global, "settings.json"), []byte(`{"model":"gpt-global","fallback":{"enabled":true,"providers":[{"name":"glm-global","type":"custom","model":"glm-5.2","baseURL":"https://provider.example/v1"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "config", "config.yaml"), []byte("model: claude-sonnet-4-6\nprovider: anthropic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	models := serverModelCatalog(workspace)
	if containsString(models, "claude-sonnet-4-6") {
		t.Fatal("project YAML leaked into model catalog")
	}
	for _, want := range []string{"gpt-global", "glm-5.2"} {
		if !containsString(models, want) {
			t.Fatalf("models=%v, missing %q", models, want)
		}
	}
}

func TestResolveRuntimeProviderFallsBackToGlobalRegistry(t *testing.T) {
	global := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", global)
	if err := os.WriteFile(filepath.Join(global, "settings.json"), []byte(`{"fallback":{"enabled":true,"providers":[{"name":"glm-global","type":"custom","protocol":"openai-chat-completions","model":"glm-5.2","baseURL":"https://provider.example/v1","apiKey":"test-key"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "config", "config.yaml"), []byte("model: claude-sonnet-4-6\nprovider: anthropic\nfallback:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := options{cwd: workspace, providerName: "glm-global"}
	resolved, err := resolveRuntimeProviderConfig(&options)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SelectedProvider != "glm-global" || resolved.SelectedProviderModel != "glm-5.2" || resolved.BaseURL != "https://provider.example/v1" {
		t.Fatalf("resolved provider = %+v", resolved)
	}
}

func TestServerStartupSnapshotUsesCLINamedProviderAndModelOverrides(t *testing.T) {
	global, workspace := t.TempDir(), t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", global)
	for _, key := range []string{"NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER", "ANTHROPIC_BASE_URL"} {
		t.Setenv(key, "")
	}
	settings := `{"model":"file-default","provider":"anthropic","fallback":{"providers":[{"name":"selected","type":"custom","protocol":"openai-responses","model":"provider-default","baseURL":"https://selected.example/v1","apiKey":"never-in-snapshot"}]}}`
	if err := os.WriteFile(filepath.Join(global, "settings.json"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, model, want string
		explicit          bool
	}{
		{name: "provider default", model: "file-default", want: "provider-default"},
		{name: "explicit CLI model", model: "cli-override", want: "cli-override", explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := options{cwd: t.TempDir(), providerName: "selected", model: tc.model, modelExplicit: tc.explicit}
			snapshot, err := serverStartupSettingsSnapshot(base, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot["provider"] != "custom" || snapshot["model"] != tc.want || snapshot["baseURL"] != "https://selected.example/v1" || snapshot["providerProtocol"] != config.ProviderProtocolOpenAIResponses {
				t.Fatalf("wrong startup route: %+v", snapshot)
			}
			if _, exists := snapshot["apiKey"]; exists {
				t.Fatal("snapshot contains credentials")
			}
			if base.model != tc.model {
				t.Fatal("snapshot mutated caller runtime options")
			}
		})
	}
}

func TestServerStartupSnapshotUsesExplicitSettingsOverGlobalAndIgnoresWorkspace(t *testing.T) {
	global, workspace := t.TempDir(), t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", global)
	for _, key := range []string{"NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"} {
		t.Setenv(key, "")
	}
	projectDir := filepath.Join(workspace, ".niuma")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(projectDir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"model":"workspace-model"}`), 0600); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(global, "settings.json")
	if err := os.WriteFile(globalPath, []byte(`{"model":"global-model"}`), 0600); err != nil {
		t.Fatal(err)
	}
	base := options{cwd: t.TempDir(), settingsInputs: []string{`{"provider":"custom","providerProtocol":"openai-responses","baseURL":"https://cli-settings.example/v1"}`}}
	snapshot, err := serverStartupSettingsSnapshot(base, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot["provider"] != "custom" || snapshot["providerProtocol"] != config.ProviderProtocolOpenAIResponses || snapshot["baseURL"] != "https://cli-settings.example/v1" || snapshot["model"] != "global-model" {
		t.Fatalf("snapshot ignored runtime settings/workspace: %+v", snapshot)
	}
	if !containsString(snapshot["settingsSources"].([]string), globalPath) {
		t.Fatalf("global source missing: %v", snapshot["settingsSources"])
	}
}

func TestServerStartupSnapshotDoesNotInventRouteOnResolutionFailure(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	snapshot, err := serverStartupSettingsSnapshot(options{providerName: "missing"}, t.TempDir())
	if err == nil || snapshot != nil {
		t.Fatalf("invalid provider produced startup snapshot: %+v err=%v", snapshot, err)
	}
}

func TestRuntimeSettingsPreservesExplicitJSONAndFileOverrides(t *testing.T) {
	global, workspace := t.TempDir(), t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", global)
	mustWrite(t, filepath.Join(global, "settings.json"), `{"model":"global-model","effort":"low"}`)
	mustWrite(t, filepath.Join(workspace, ".niuma", "settings.json"), `{"effort":"off"}`)
	mustWrite(t, filepath.Join(workspace, "override.json"), `{"effort":"high"}`)
	mustWrite(t, filepath.Join(workspace, "override.yaml"), "effort: high\n")
	for _, input := range []string{`{"effort":"high"}`, "override.json"} {
		t.Run(input, func(t *testing.T) {
			opts, _, err := parseArgs([]string{"--cwd", workspace, "--settings", input, "-p", "test"})
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := resolveRuntimeProviderConfig(&opts)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Settings.Effort != "high" || cfg.Settings.Model != "global-model" {
				t.Fatalf("explicit settings not merged with global: %+v", cfg.Settings)
			}
			if got := config.LoadGlobalSettings().Effort; got != "low" {
				t.Fatalf("explicit override persisted: %q", got)
			}
		})
	}
	// The CLI has always accepted JSON only, even though the file parser also
	// supports YAML for explicit internal callers.
	opts, _, err := parseArgs([]string{"--cwd", workspace, "--settings", "override.yaml", "-p", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntimeProviderConfig(&opts); err == nil {
		t.Fatal("CLI unexpectedly accepted YAML settings")
	}
}
