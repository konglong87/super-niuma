package config

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/konglong87/super-niuma/internal/product"
)

func TestExplicitSettingsMergesUserProjectAndLocal(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "model": "user-model",
	  "modelOptions": ["global-a", "shared-model"],
	  "env": {"ANTHROPIC_API_KEY": "from-user", "A": "1"},
	  "permissions": {"allow": ["Read"], "ask": ["Bash"], "defaultMode": "ask", "source": "user", "additionalDirectories": ["/shared"]},
	  "mcpServers": {"user": {"command": "user"}}
	}`)
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "model": "project-model",
	  "modelOptions": ["project-a", "shared-model"],
	  "env": {"A": "2"},
	  "permissions": {"deny": ["Bash"], "preference": "project"},
	  "mcpServers": {"project": {"command": "project"}}
	}`)
	mustWrite(t, filepath.Join(project, ".claude", "settings.local.json"), `{
	  "model": "local-model",
	  "env": {"B": "3"}
	}`)

	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(home, ".niuma", "settings.json"),
		filepath.Join(project, ".claude", "settings.json"),
		filepath.Join(project, ".claude", "settings.local.json"),
	)
	if loaded.Model != "local-model" {
		t.Fatalf("model = %q, want local-model", loaded.Model)
	}
	if got := strings.Join(loaded.ModelOptions, ","); got != "global-a,shared-model,project-a" {
		t.Fatalf("modelOptions = %q", got)
	}
	if loaded.Env["ANTHROPIC_API_KEY"] != "from-user" || loaded.Env["A"] != "2" || loaded.Env["B"] != "3" {
		t.Fatalf("env = %+v", loaded.Env)
	}
	if len(loaded.Permissions.Allow) != 1 || loaded.Permissions.Allow[0] != "Read" {
		t.Fatalf("allow = %+v", loaded.Permissions.Allow)
	}
	if len(loaded.Permissions.Deny) != 1 || loaded.Permissions.Deny[0] != "Bash" {
		t.Fatalf("deny = %+v", loaded.Permissions.Deny)
	}
	if strings.Join(loaded.Permissions.Ask, ",") != "Bash" || loaded.Permissions.Source != "user" || loaded.Permissions.Preference != "project" {
		t.Fatalf("permission metadata = %+v", loaded.Permissions)
	}
	if strings.Join(loaded.AdditionalDirectories, ",") != "/shared" || strings.Join(loaded.Permissions.AdditionalDirectories, ",") != "/shared" {
		t.Fatalf("additional directories = %+v permissions=%+v", loaded.AdditionalDirectories, loaded.Permissions)
	}
	if len(loaded.MCPServers) != 2 {
		t.Fatalf("mcp servers = %+v", loaded.MCPServers)
	}
	if len(loaded.Sources) != 3 {
		t.Fatalf("sources = %+v", loaded.Sources)
	}
	if loaded.Permissions.RuleSources["allow:Read"] != "user" || loaded.Permissions.RuleSources["deny:Bash"] != "project" || loaded.Permissions.RuleSources["ask:Bash"] != "user" || loaded.Permissions.RuleSources["alwaysAsk:Bash"] != "user" || loaded.Permissions.RuleSources["defaultMode"] != "user" {
		t.Fatalf("permission rule sources = %+v", loaded.Permissions.RuleSources)
	}
}

func TestResolveImageGenerationDefaultsToDisabled(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	resolved, err := ResolveImageGeneration(cwd)
	if err != nil {
		t.Fatalf("ResolveImageGeneration() error = %v", err)
	}
	if resolved.Enabled {
		t.Fatal("image generation should be disabled by default")
	}
	if resolved.MaxImages != 1 || resolved.Quality != "auto" || resolved.Size != "auto" || resolved.OutputFormat != "png" || resolved.Background != "auto" {
		t.Fatalf("unexpected defaults: %+v", resolved)
	}
	if resolved.AsyncChannelEnabled || len(resolved.AsyncChannelAccountKeys) != 0 {
		t.Fatalf("unexpected async channel defaults: %+v", resolved)
	}
	if resolved.PreviewInContext {
		t.Fatalf("image artifact preview should be disabled by default: %+v", resolved)
	}
	worker := resolved.Worker
	if worker.PollIntervalMS != 500 || worker.MaxConcurrent != 2 || worker.MaxAttempts != 3 || worker.HeartbeatSeconds != 15 || worker.LeaseSeconds != 240 || worker.FinalizeTimeoutSeconds != 10 || worker.MaxQueuedPerTenant != 100 || worker.MaxQueuedPerUser != 20 {
		t.Fatalf("unexpected worker defaults: %+v", worker)
	}
}

func TestResolvedImageGenerationAsyncChannelAccountEligibility(t *testing.T) {
	tests := []struct {
		name       string
		resolved   ResolvedImageGeneration
		accountKey string
		want       bool
	}{
		{name: "disabled stays synchronous", resolved: ResolvedImageGeneration{AsyncChannelEnabled: false}, accountKey: "primary", want: false},
		{name: "enabled empty list allows every configured account", resolved: ResolvedImageGeneration{AsyncChannelEnabled: true}, accountKey: "primary", want: true},
		{name: "enabled exact account is eligible", resolved: ResolvedImageGeneration{AsyncChannelEnabled: true, AsyncChannelAccountKeys: []string{"primary", "canary"}}, accountKey: "canary", want: true},
		{name: "enabled unlisted account stays synchronous", resolved: ResolvedImageGeneration{AsyncChannelEnabled: true, AsyncChannelAccountKeys: []string{"canary"}}, accountKey: "primary", want: false},
		{name: "matching is case sensitive", resolved: ResolvedImageGeneration{AsyncChannelEnabled: true, AsyncChannelAccountKeys: []string{"Canary"}}, accountKey: "canary", want: false},
		{name: "empty account key is never eligible", resolved: ResolvedImageGeneration{AsyncChannelEnabled: true}, accountKey: " ", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.resolved.AsyncChannelEnabledForAccount(test.accountKey); got != test.want {
				t.Fatalf("AsyncChannelEnabledForAccount(%q) = %v, want %v", test.accountKey, got, test.want)
			}
		})
	}
}

func TestResolveImageGenerationResolvesWorkerOverrides(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"providers": [{"name": "jiuan", "baseURL": "https://jiuan.example/v1", "apiKey": "secret-key"}]},
  "imageGeneration": {
    "enabled": true,
    "provider": "jiuan",
	"previewInContext": true,
    "timeoutSeconds": 200,
    "asyncChannelEnabled": true,
    "asyncChannelAccountKeys": ["primary", "canary"],
    "worker": {
      "pollIntervalMs": 750,
      "maxConcurrent": 4,
      "maxAttempts": 5,
      "heartbeatSeconds": 20,
      "leaseSeconds": 280,
      "finalizeTimeoutSeconds": 12,
      "maxQueuedPerTenant": 120,
      "maxQueuedPerUser": 24
    }
  }
}`)

	resolved, err := ResolveImageGeneration(cwd)
	if err != nil {
		t.Fatalf("ResolveImageGeneration() error = %v", err)
	}
	if !resolved.PreviewInContext || !resolved.AsyncChannelEnabled || strings.Join(resolved.AsyncChannelAccountKeys, ",") != "primary,canary" {
		t.Fatalf("async channel config = %+v", resolved)
	}
	want := ResolvedImageGenerationWorker{PollIntervalMS: 750, MaxConcurrent: 4, MaxAttempts: 5, HeartbeatSeconds: 20, LeaseSeconds: 280, FinalizeTimeoutSeconds: 12, MaxQueuedPerTenant: 120, MaxQueuedPerUser: 24}
	if resolved.Worker != want {
		t.Fatalf("worker = %+v, want %+v", resolved.Worker, want)
	}
}

func TestResolveImageGenerationRejectsInvalidWorkerSettings(t *testing.T) {
	tests := []struct {
		name   string
		worker string
		want   string
	}{
		{name: "poll interval", worker: `"pollIntervalMs":-1`, want: "pollIntervalMs"},
		{name: "concurrency", worker: `"maxConcurrent":-1`, want: "maxConcurrent"},
		{name: "attempts", worker: `"maxAttempts":-1`, want: "maxAttempts"},
		{name: "heartbeat", worker: `"heartbeatSeconds":-1`, want: "heartbeatSeconds"},
		{name: "lease", worker: `"leaseSeconds":-1`, want: "leaseSeconds"},
		{name: "finalize timeout", worker: `"finalizeTimeoutSeconds":-1`, want: "finalizeTimeoutSeconds"},
		{name: "tenant queue", worker: `"maxQueuedPerTenant":-1`, want: "maxQueuedPerTenant"},
		{name: "user queue", worker: `"maxQueuedPerUser":-1`, want: "maxQueuedPerUser"},
		{name: "heartbeat lease ordering", worker: `"heartbeatSeconds":30,"leaseSeconds":30`, want: "heartbeatSeconds must be less than leaseSeconds"},
		{name: "attempt lease ordering", worker: `"heartbeatSeconds":15,"leaseSeconds":179`, want: "leaseSeconds must be at least timeoutSeconds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			cwd := t.TempDir()
			t.Setenv("HOME", home)
			mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"providers": [{"name": "jiuan", "baseURL": "https://jiuan.example/v1", "apiKey": "secret-key"}]},
  "imageGeneration": {"enabled":true,"provider":"jiuan","worker":{`+test.worker+`}}
}`)
			_, err := ResolveImageGeneration(cwd)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveImageGeneration() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestExplicitSettingsMergesImageWorkerAndCanaryAccountSettings(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	"imageGeneration": {"previewInContext":true,"asyncChannelEnabled":true,"asyncChannelAccountKeys":["global"],"worker":{"maxConcurrent":4,"maxAttempts":5}}
}`)
	mustWrite(t, filepath.Join(project, ".niuma-legacy", "settings.json"), `{
	"imageGeneration": {"previewInContext":false,"asyncChannelAccountKeys":[],"worker":{"maxConcurrent":8}}
}`)

	image := loadExplicitSettingsForTest(t, project,
		filepath.Join(home, ".niuma", "settings.json"),
		filepath.Join(project, ".niuma-legacy", "settings.json"),
	).Settings.ImageGeneration
	if image == nil || image.AsyncChannelEnabled == nil || !*image.AsyncChannelEnabled {
		t.Fatalf("merged image settings = %+v", image)
	}
	if image.AsyncChannelAccountKeys == nil || len(image.AsyncChannelAccountKeys) != 0 {
		t.Fatalf("explicit empty account list must clear inherited canary list: %+v", image.AsyncChannelAccountKeys)
	}
	if image.PreviewInContext == nil || *image.PreviewInContext {
		t.Fatalf("explicit false preview must override inherited true: %+v", image.PreviewInContext)
	}
	if image.Worker == nil || image.Worker.MaxConcurrent != 8 || image.Worker.MaxAttempts != 5 {
		t.Fatalf("merged worker settings = %+v", image.Worker)
	}
}

func TestResolveImageGenerationResolvesNamedProviderCredentials(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"providers": [{"name": "jiuan", "type": "openai-compatible", "baseURL": "https://jiuan.example/v1", "apiKey": "secret-key", "model": "chat-model"}]},
  "imageGeneration": {"enabled": true, "provider": "jiuan"}
}`)

	resolved, err := ResolveImageGeneration(cwd)
	if err != nil {
		t.Fatalf("ResolveImageGeneration() error = %v", err)
	}
	if !resolved.Enabled || resolved.Provider != "jiuan" || resolved.BaseURL != "https://jiuan.example/v1" || resolved.APIKey != "secret-key" {
		t.Fatalf("provider resolution = %+v", resolved)
	}
	if resolved.Model != "gpt-image-2" {
		t.Fatalf("model = %q, want gpt-image-2", resolved.Model)
	}
}

func TestResolveImageGenerationCatalogNormalizesAgnesCapabilities(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"providers": [{"name": "agnes", "baseURL": "https://apihub.agnes-ai.com/v1", "apiKey": "secret-key", "imageProtocol": "agnes-images"}]},
  "imageGeneration": {
    "enabled": true,
    "defaultProvider": "agnes",
    "defaultModel": "agnes-image-2.5-flash",
    "catalog": [{
      "provider": "agnes",
      "model": "agnes-image-2.5-flash",
      "label": "Agnes Image 2.5 Flash",
      "operations": ["generate", "edit", "compose"],
      "resolutions": ["1K", "2K", "3K", "4K"],
      "aspectRatios": ["1:1", "16:9"],
      "outputFormats": ["png"],
      "responseModes": ["b64_json", "url"],
      "supportsMask": false,
      "maxInputImages": 4
    }]
  }
}`)

	resolved, err := ResolveImageGeneration(cwd)
	if err != nil {
		t.Fatalf("ResolveImageGeneration() error = %v", err)
	}
	if resolved.Provider != "agnes" || resolved.Model != "agnes-image-2.5-flash" {
		t.Fatalf("defaults = %q/%q", resolved.Provider, resolved.Model)
	}
	if len(resolved.Catalog) != 1 || resolved.Catalog[0].ImageProtocol != "agnes-images" {
		t.Fatalf("catalog = %+v", resolved.Catalog)
	}
	capability := resolved.Catalog[0].Capability
	if len(capability.Resolutions) != 4 || capability.Resolutions[1] != "2K" || capability.AspectRatios[1] != "16:9" || capability.SupportsMask {
		t.Fatalf("capability = %+v", capability)
	}
}

func TestResolveImageGenerationCatalogRejectsDuplicateProviderModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"providers": [{"name": "agnes", "baseURL": "https://apihub.agnes-ai.com/v1", "apiKey": "secret-key"}]},
  "imageGeneration": {"enabled": true, "defaultProvider": "agnes", "defaultModel": "m", "catalog": [{"provider":"agnes","model":"m"},{"provider":"agnes","model":"m"}]}
}`)
	if _, err := ResolveImageGeneration(t.TempDir()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate catalog error, got %v", err)
	}
}

func TestResolveImageGenerationRejectsInvalidValues(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"providers": [{"name": "jiuan", "baseURL": "https://jiuan.example/v1", "apiKey": "secret-key"}]},
  "imageGeneration": {"enabled": true, "provider": "jiuan", "quality": "ultra"}
}`)

	if _, err := ResolveImageGeneration(cwd); err == nil || !strings.Contains(err.Error(), "quality") {
		t.Fatalf("expected quality validation error, got %v", err)
	}
}

func TestResolveImageGenerationRejectsMissingCredentials(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"providers": [{"name": "jiuan", "baseURL": "https://jiuan.example/v1"}]},
  "imageGeneration": {"enabled": true, "provider": "jiuan"}
}`)

	if _, err := ResolveImageGeneration(cwd); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("expected credential error, got %v", err)
	}
}

func TestResolveImageGenerationDoesNotUseClaudeCodeAuthTokenFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "claude-auth")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{"imageGeneration":{"enabled":true,"provider":"jiuan"},"fallback":{"providers":[{"name":"jiuan","baseURL":"https://jiuan.example/v1"}]}}`)
	if _, err := ResolveImageGeneration(t.TempDir()); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("ResolveImageGeneration() error = %v, want missing credential", err)
	}
}

func TestExplicitSettingsMergesSubagentModelTiers(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "subagentModelTiers": {"haiku": "user-fast", "sonnet": "user-mid"}
	}`)
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "subagentModelTiers": {"haiku": "project-fast"}
	}`)

	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(home, ".niuma", "settings.json"),
		filepath.Join(project, ".claude", "settings.json"),
	)
	if loaded.SubagentModelTiers["haiku"] != "project-fast" {
		t.Fatalf("haiku = %q, want project-fast (project overrides user)", loaded.SubagentModelTiers["haiku"])
	}
	if loaded.SubagentModelTiers["sonnet"] != "user-mid" {
		t.Fatalf("sonnet = %q, want user-mid (inherited from user)", loaded.SubagentModelTiers["sonnet"])
	}
	if got := SubagentModelTiers(project)["haiku"]; got != "user-fast" {
		t.Fatalf("SubagentModelTiers accessor haiku = %q, want user-fast (default global only)", got)
	}
}

func TestExplicitSettingsMergesModelPricing(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "modelPricing": {"glm-5.1": {"input": 1.0, "output": 2.0}, "gpt-5.5": {"input": 3, "output": 6}}
	}`)
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "modelPricing": {"glm-5.1": {"input": 1.5, "output": 2.5}}
	}`)

	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(home, ".niuma", "settings.json"),
		filepath.Join(project, ".claude", "settings.json"),
	)
	if got := loaded.ModelPricing["glm-5.1"].Input; got != 1.5 {
		t.Fatalf("glm-5.1 input = %v, want 1.5 (project overrides user)", got)
	}
	if got := loaded.ModelPricing["gpt-5.5"].Output; got != 6 {
		t.Fatalf("gpt-5.5 output = %v, want 6 (inherited from user)", got)
	}
	if got := ModelPricing(project)["glm-5.1"].Output; got != 2.0 {
		t.Fatalf("ModelPricing accessor glm-5.1 output = %v, want 2 (default global only)", got)
	}
}

func TestLoadSettingsIgnoresLegacyClaudeGlobalSettings(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	legacyPath := filepath.Join(home, ".claude", "settings.json")
	goClaudePath := filepath.Join(home, ".niuma", "settings.json")
	mustWrite(t, legacyPath, `{
	  "model": "legacy-model",
	  "env": {"ANTHROPIC_API_KEY": "legacy-key", "A": "legacy"},
	  "permissions": {"allow": ["Read"]}
	}`)
	mustWrite(t, goClaudePath, `{
	  "model": "niuma-legacy-model",
	  "env": {"ANTHROPIC_API_KEY": "niuma-legacy-key", "B": "niuma-legacy"},
	  "permissions": {"deny": ["Bash"]}
	}`)

	loaded := LoadSettings(project)
	if loaded.Model != "niuma-legacy-model" {
		t.Fatalf("model = %q, want niuma-legacy-model", loaded.Model)
	}
	if loaded.Env["ANTHROPIC_API_KEY"] != "niuma-legacy-key" || loaded.Env["A"] != "" || loaded.Env["B"] != "niuma-legacy" {
		t.Fatalf("env = %+v", loaded.Env)
	}
	if got := strings.Join(loaded.Sources, ","); got != goClaudePath {
		t.Fatalf("sources = %q, want only niuma-legacy", got)
	}
	if strings.Join(loaded.Permissions.Allow, ",") != "" || strings.Join(loaded.Permissions.Deny, ",") != "Bash" {
		t.Fatalf("permissions = %+v", loaded.Permissions)
	}
	if strings.Contains(strings.Join(loaded.Sources, ","), legacyPath) {
		t.Fatalf("legacy global settings should not be loaded: %+v", loaded.Sources)
	}
}

func TestLoadSettingsIgnoresBothLegacyProjectDirectories(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "model": "legacy-project-model",
	  "identity": {"guidanceFilename": "legacy.md"},
	  "env": {"A": "legacy"}
	}`)
	mustWrite(t, filepath.Join(project, ".niuma-legacy", "settings.json"), `{
	  "model": "go-project-model",
	  "identity": {"guidanceFilename": "agentx.md"},
	  "env": {"A": "go"}
	}`)

	loaded := LoadSettings(project)
	if loaded.Model != "" || loaded.Identity != nil || len(loaded.Env) != 0 || len(loaded.Sources) != 0 {
		t.Fatalf("project settings were implicitly loaded: %+v", loaded)
	}
}

func TestExplicitSettingsMergesInitDetailLevel(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "init": {"detailLevel": "minimal"}
	}`)
	mustWrite(t, filepath.Join(project, ".niuma-legacy", "settings.json"), `{
	  "init": {"detailLevel": "detailed"}
	}`)

	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(home, ".niuma", "settings.json"),
		filepath.Join(project, ".niuma-legacy", "settings.json"),
	)
	if loaded.Init == nil || loaded.Init.DetailLevel != "detailed" {
		t.Fatalf("init settings = %+v", loaded.Init)
	}
	if got := InitDetailLevel(loaded.Settings); got != "detailed" {
		t.Fatalf("InitDetailLevel = %q, want detailed", got)
	}

	loaded.Init.DetailLevel = "invalid"
	if got := InitDetailLevel(loaded.Settings); got != "balanced" {
		t.Fatalf("invalid InitDetailLevel = %q, want balanced", got)
	}
	t.Setenv("NIUMA_INIT_DETAIL_LEVEL", "minimal")
	if got := InitDetailLevel(loaded.Settings); got != "minimal" {
		t.Fatalf("env InitDetailLevel = %q, want minimal", got)
	}
}

func TestProjectSettingsPathUsesGolangCCDirectory(t *testing.T) {
	project := t.TempDir()
	path := ProjectSettingsPath(project, false)
	want := filepath.Join(project, ".niuma", "settings.json")
	if path != want {
		t.Fatalf("ProjectSettingsPath = %q, want %q", path, want)
	}
	local := ProjectSettingsPath(project, true)
	wantLocal := filepath.Join(project, ".niuma", "settings.local.json")
	if local != wantLocal {
		t.Fatalf("ProjectSettingsPath local = %q, want %q", local, wantLocal)
	}
}

func TestProjectSettingsPathIgnoresLegacyIdentityConfigDirForWrites(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "identity": {"configDirName": ".claude"}
	}`)

	path := ProjectSettingsPath(project, false)
	want := filepath.Join(project, ".niuma", "settings.json")
	if path != want {
		t.Fatalf("ProjectSettingsPath = %q, want %q", path, want)
	}
}

func TestEnsureProjectSettingsMaterializedCopiesLegacySettingsToGolangCC(t *testing.T) {
	project := t.TempDir()
	legacyProject := filepath.Join(project, ".claude", "settings.json")
	legacyLocal := filepath.Join(project, ".claude", "settings.local.json")
	mustWrite(t, legacyProject, `{
	  "model": "legacy-model",
	  "identity": {"configDirName": ".claude"},
	  "env": {"A": "legacy"}
	}`)
	mustWrite(t, legacyLocal, `{"env": {"B": "local"}}`)

	if err := EnsureProjectSettingsMaterialized(project); err != nil {
		t.Fatal(err)
	}
	goProject := filepath.Join(project, ".niuma", "settings.json")
	goLocal := filepath.Join(project, ".niuma", "settings.local.json")
	loaded := LoadSettingsFile(goProject)
	if loaded.Model != "legacy-model" || loaded.Env["A"] != "legacy" {
		t.Fatalf("materialized project settings = %+v", loaded)
	}
	if loaded.Identity == nil || loaded.Identity.ConfigDirName != ".niuma" {
		t.Fatalf("materialized identity = %+v", loaded.Identity)
	}
	loadedLocal := LoadSettingsFile(goLocal)
	if loadedLocal.Env["B"] != "local" {
		t.Fatalf("materialized local settings = %+v", loadedLocal)
	}
	if _, err := os.Stat(legacyProject); err != nil {
		t.Fatalf("legacy project should remain readable: %v", err)
	}
	if _, err := os.Stat(legacyLocal); err != nil {
		t.Fatalf("legacy local should remain readable: %v", err)
	}
}

func TestEnsureProjectSettingsMaterializedMergesPreviousProductSettings(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "model": "claude-model",
	  "env": {"CLAUDE_ONLY": "1", "SHARED": "claude"}
	}`)
	mustWrite(t, filepath.Join(project, ".niuma-legacy", "settings.json"), `{
	  "provider": "legacy-provider",
	  "identity": {"configDirName": ".niuma-legacy"},
	  "env": {"LEGACY_ONLY": "1", "SHARED": "legacy"}
	}`)

	if err := EnsureProjectSettingsMaterialized(project); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, ".niuma", "settings.json")
	loaded := LoadSettingsFile(path)
	if loaded.Model != "claude-model" || loaded.Provider != "legacy-provider" {
		t.Fatalf("materialized settings = %+v", loaded)
	}
	if loaded.Env["CLAUDE_ONLY"] != "1" || loaded.Env["LEGACY_ONLY"] != "1" || loaded.Env["SHARED"] != "legacy" {
		t.Fatalf("materialized env = %+v", loaded.Env)
	}
	if loaded.Identity == nil || loaded.Identity.ConfigDirName != ".niuma" {
		t.Fatalf("materialized identity = %+v", loaded.Identity)
	}
}

func TestProjectSettingsPathMigratesPreviousDefaultDirectory(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma-legacy", "settings.json"), `{
	  "identity": {"configDirName": ".niuma-legacy"}
	}`)

	want := filepath.Join(project, ".niuma", "settings.json")
	if got := ProjectSettingsPath(project, false); got != want {
		t.Fatalf("ProjectSettingsPath = %q, want %q", got, want)
	}
}

func TestProjectSettingsPathUsesConfiguredConfigDirectory(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "identity": {"configDirName": ".agentx"}
	}`)

	path := ProjectSettingsPath(project, false)
	want := filepath.Join(project, ".agentx", "settings.json")
	if path != want {
		t.Fatalf("ProjectSettingsPath = %q, want %q", path, want)
	}
}

func TestLoadSettingsIgnoresConfiguredProjectDirectory(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "identity": {"configDirName": ".agentx"}
	}`)
	mustWrite(t, filepath.Join(project, ".niuma-legacy", "settings.json"), `{"model": "default-project-model"}`)
	mustWrite(t, filepath.Join(project, ".agentx", "settings.json"), `{"model": "agentx-project-model"}`)

	loaded := LoadSettings(project)
	if loaded.Model != "" || len(loaded.Sources) != 1 || loaded.Sources[0] != filepath.Join(home, ".niuma", "settings.json") {
		t.Fatalf("custom project directory was implicitly loaded: %+v", loaded)
	}
}

func TestExplicitSettingsSupportsSnakeCaseModelOptionsInYAML(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")

	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "modelOptions": ["global-model", "shared-model"]
	}`)
	mustWrite(t, filepath.Join(project, "config", "config.local.yaml"), `
model_options:
  - local-model
  - shared-model
`)

	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(home, ".niuma", "settings.json"),
		filepath.Join(project, "config", "config.local.yaml"),
	)
	if got := strings.Join(loaded.ModelOptions, ","); got != "global-model,shared-model,local-model" {
		t.Fatalf("modelOptions = %q", got)
	}
}

func TestConfiguredModelsDerivesFromProviders(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")

	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"model":"glm-5.1","provider":"custom","fallback":{"enabled":true,"providers":[{"name":"a","type":"custom","model":"deepseek-v4-pro"},{"name":"b","type":"custom","model":"kimi-k2.6"},{"name":"dup","type":"custom","model":"glm-5.1"}]}}`)

	got := ConfiguredModels(project)
	if want := "glm-5.1,deepseek-v4-pro,kimi-k2.6"; strings.Join(got, ",") != want {
		t.Fatalf("ConfiguredModels = %q, want %q", strings.Join(got, ","), want)
	}
}

func TestAvailableModelsMergesConfiguredCandidates(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"model":"primary-model","fallback":{"enabled":true,"providers":[{"name":"first","type":"custom","model":"provider-model"},{"name":"duplicate","type":"custom","model":"picker-model"}]},"modelOptions":["picker-model","primary-model"]}`)

	got := AvailableModels(project)
	want := "picker-model,primary-model,provider-model"
	if strings.Join(got, ",") != want {
		t.Fatalf("AvailableModels = %q, want %q", strings.Join(got, ","), want)
	}
}

func TestAvailableModelsFallsBackToKnownModels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_MODEL", "")
	got := AvailableModels(t.TempDir())
	if len(got) != 0 {
		t.Fatalf("AvailableModels = %q, want no implicit Anthropic catalog", strings.Join(got, ","))
	}
}

func TestGlobalSettingsPathUsesGolangCCDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := GlobalSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".niuma", "settings.json")
	if path != want {
		t.Fatalf("GlobalSettingsPath = %q, want %q", path, want)
	}

	settings := Settings{Provider: "custom", BaseURL: "https://example.test/v1", APIKey: "from-niuma"}
	if err := SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded := LoadGlobalSettings()
	if loaded.APIKey != "from-niuma" {
		t.Fatalf("loaded api key = %q", loaded.APIKey)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy settings should not be written, err=%v", err)
	}
}

func TestSaveGlobalSettingsPreservesUnknownFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := GlobalSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, `{"model":"old","future":{"nested":{"keep":true}},"mcpServers":{"future":{"command":"x","unknown":7}}}`)

	next := LoadGlobalSettings()
	next.Model = "new"
	if err := SaveGlobalSettings(next); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["model"] != "new" {
		t.Fatalf("model = %v, want new", stored["model"])
	}
	if stored["future"].(map[string]any)["nested"].(map[string]any)["keep"] != true {
		t.Fatalf("unknown nested field was lost: %v", stored["future"])
	}
	futureServer := stored["mcpServers"].(map[string]any)["future"].(map[string]any)
	if futureServer["unknown"] != float64(7) {
		t.Fatalf("unknown map entry field was lost: %v", futureServer)
	}
}

func TestLoadSettingsReadsConfiguredGlobalConfigRoot(t *testing.T) {
	home := t.TempDir()
	configRoot := filepath.Join(t.TempDir(), "owned-config")
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", configRoot)

	mustWrite(t, filepath.Join(home, ".niuma-legacy", "settings.json"), `{"model": "home-model"}`)
	mustWrite(t, filepath.Join(configRoot, "settings.json"), `{"model": "configured-root-model"}`)

	loaded := LoadSettings(project)
	if loaded.Model != "configured-root-model" {
		t.Fatalf("model = %q, want configured-root-model sources=%+v", loaded.Model, loaded.Sources)
	}
	if got := strings.Join(loaded.Sources, ","); strings.Contains(got, filepath.Join(home, ".niuma-legacy")) || !strings.Contains(got, filepath.Join(configRoot, "settings.json")) {
		t.Fatalf("sources = %+v", loaded.Sources)
	}
}

func TestLoadForCWDUsesCanonicalSettingsAndIgnoresProviderEnv(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "provider": "custom",
	  "baseURL": "https://example.test/",
	  "apiKey": "from-settings"
	}`)
	cfg := LoadForCWD(project)
	if cfg.APIKey != "from-settings" {
		t.Fatalf("APIKey = %q, want canonical settings value", cfg.APIKey)
	}
	if cfg.BaseURL != "https://example.test" {
		t.Fatalf("BaseURL = %q, want trimmed canonical settings URL", cfg.BaseURL)
	}
}

func TestExplicitYAMLParsesContextLength(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
model: gpt-test
context_length: 251000
`)
	settings := LoadSettingsFile(filepath.Join(project, "config", "config.yaml"))
	if settings.ContextLength != 251000 {
		t.Fatalf("ContextLength = %d, want 251000", settings.ContextLength)
	}
}

func TestExplicitYAMLParsesUpdateSettings(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
update:
  enabled: true
  checkOnStartup: true
  checkOnly: true
  autoPull: true
  skipWhenDirty: true
  strategy: git-ff-only
  repoDir: /opt/niuma-legacy
  customCommand: make update
  versionSourceURL: https://updates.example.com/niuma-legacy/version
  scheduleInterval: daily
  timeoutSeconds: 12
`)
	settings := LoadSettingsFile(filepath.Join(project, "config", "config.yaml"))
	if settings.Update == nil {
		t.Fatal("update settings not loaded")
	}
	if settings.Update.Enabled == nil || !*settings.Update.Enabled {
		t.Fatalf("enabled = %#v", settings.Update.Enabled)
	}
	if settings.Update.RepoDir != "/opt/niuma-legacy" || settings.Update.TimeoutSeconds != 12 {
		t.Fatalf("update settings = %+v", settings.Update)
	}
	if settings.Update.CheckOnly == nil || !*settings.Update.CheckOnly || settings.Update.CustomCommand != "make update" || settings.Update.VersionSourceURL == "" || settings.Update.ScheduleInterval != "daily" {
		t.Fatalf("extended update settings = %+v", settings.Update)
	}
}

func TestExplicitSettingsMergesAutoCompactConfig(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
autoCompact:
  enabled: true
  defaultThresholdRatio: 0.75
  preserveRecentRounds: 6
  maxSummaryTokens: 8000
  modelContext:
    gpt-5.5: 200000
  modelThresholdRatio:
    gpt-5.5: 0.5
`)
	mustWrite(t, filepath.Join(project, "config", "config.local.yaml"), `
autoCompact:
  preserveRecentRounds: 8
  modelThresholdRatio:
    gpt-5.5: 0.6
    claude-sonnet-4-6: 0.7
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, "config", "config.yaml"),
		filepath.Join(project, "config", "config.local.yaml"),
	)
	if loaded.AutoCompact == nil {
		t.Fatal("autoCompact settings not loaded")
	}
	if loaded.AutoCompact.Enabled == nil || !*loaded.AutoCompact.Enabled {
		t.Fatalf("enabled = %#v", loaded.AutoCompact.Enabled)
	}
	if loaded.AutoCompact.PreserveRecentRounds == nil || *loaded.AutoCompact.PreserveRecentRounds != 8 {
		t.Fatalf("preserveRecentRounds = %#v, want 8", loaded.AutoCompact.PreserveRecentRounds)
	}
	if loaded.AutoCompact.ModelContext["gpt-5.5"] != 200000 {
		t.Fatalf("modelContext = %+v", loaded.AutoCompact.ModelContext)
	}
	if loaded.AutoCompact.ModelThresholdRatio["gpt-5.5"] != 0.6 || loaded.AutoCompact.ModelThresholdRatio["claude-sonnet-4-6"] != 0.7 {
		t.Fatalf("modelThresholdRatio = %+v", loaded.AutoCompact.ModelThresholdRatio)
	}
}

func TestExplicitSettingsAutoCompactAllowsExplicitZeroOverrides(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
autoCompact:
  enabled: true
  summaryModel: compact-model
  cooldownTurns: 2
`)
	mustWrite(t, filepath.Join(project, "config", "config.local.yaml"), `
autoCompact:
  summaryModel: ""
  cooldownTurns: 0
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, "config", "config.yaml"),
		filepath.Join(project, "config", "config.local.yaml"),
	)
	if loaded.AutoCompact == nil {
		t.Fatal("autoCompact settings not loaded")
	}
	if loaded.AutoCompact.SummaryModel == nil || *loaded.AutoCompact.SummaryModel != "" {
		t.Fatalf("summaryModel = %#v, want explicit empty string", loaded.AutoCompact.SummaryModel)
	}
	if loaded.AutoCompact.CooldownTurns == nil || *loaded.AutoCompact.CooldownTurns != 0 {
		t.Fatalf("cooldownTurns = %#v, want explicit zero", loaded.AutoCompact.CooldownTurns)
	}
}

func TestExplicitSettingsMergesRecapConfig(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
recap:
  enabled: true
  mode: manual
  model: recap-small
  recentMessageWindow: 30
  maxTokens: 512
  awayDelaySeconds: 120
  includeCompactSummary: true
`)
	mustWrite(t, filepath.Join(project, "config", "config.local.yaml"), `
recap:
  mode: post_turn
  recentMessageWindow: 12
  awayDelaySeconds: 5
  includeCompactSummary: false
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, "config", "config.yaml"),
		filepath.Join(project, "config", "config.local.yaml"),
	)
	if loaded.Recap == nil {
		t.Fatal("recap settings not loaded")
	}
	if loaded.Recap.Enabled == nil || !*loaded.Recap.Enabled {
		t.Fatalf("enabled = %#v", loaded.Recap.Enabled)
	}
	if loaded.Recap.Mode != "post_turn" || loaded.Recap.Model != "recap-small" {
		t.Fatalf("recap settings = %+v", loaded.Recap)
	}
	if loaded.Recap.RecentMessageWindow == nil || *loaded.Recap.RecentMessageWindow != 12 {
		t.Fatalf("recentMessageWindow = %#v", loaded.Recap.RecentMessageWindow)
	}
	if loaded.Recap.AwayDelaySeconds == nil || *loaded.Recap.AwayDelaySeconds != 5 {
		t.Fatalf("awayDelaySeconds = %#v", loaded.Recap.AwayDelaySeconds)
	}
	if loaded.Recap.IncludeCompactSummary == nil || *loaded.Recap.IncludeCompactSummary {
		t.Fatalf("includeCompactSummary = %#v", loaded.Recap.IncludeCompactSummary)
	}
}

func TestExplicitSettingsMergesTUIConfig(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
tui:
  resumeHistoryLimit: 6
  showThinking: true
  thinkingMode: full
webAgentUI:
  showThinking: true
`)
	mustWrite(t, filepath.Join(project, "config", "config.local.yaml"), `
tui:
  resumeHistoryLimit: 2
  showThinking: false
  thinkingMode: summary
webAgentUI:
  showThinking: false
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, "config", "config.yaml"),
		filepath.Join(project, "config", "config.local.yaml"),
	)
	if loaded.TUI == nil || loaded.TUI.ResumeHistoryLimit == nil || *loaded.TUI.ResumeHistoryLimit != 2 {
		t.Fatalf("tui settings = %+v", loaded.TUI)
	}
	if loaded.TUI.ShowThinking == nil || *loaded.TUI.ShowThinking {
		t.Fatalf("tui showThinking = %#v", loaded.TUI.ShowThinking)
	}
	if loaded.TUI.ThinkingMode != TUIThinkingModeSummary {
		t.Fatalf("tui thinkingMode = %q", loaded.TUI.ThinkingMode)
	}
	if loaded.WebAgentUI == nil || loaded.WebAgentUI.ShowThinking == nil || *loaded.WebAgentUI.ShowThinking {
		t.Fatalf("webAgentUI settings = %+v", loaded.WebAgentUI)
	}
}

func TestShowThinkingDefaultsAndExplicitOptOut(t *testing.T) {
	if !TUIShowThinking(Settings{}) || !WebAgentUIShowThinking(Settings{}) {
		t.Fatal("showThinking must default to true")
	}
	show := false
	settings := Settings{
		TUI:        &TUISettings{ShowThinking: &show},
		WebAgentUI: &WebAgentUISettings{ShowThinking: &show},
	}
	if TUIShowThinking(settings) || WebAgentUIShowThinking(settings) {
		t.Fatal("explicit showThinking=false must be preserved")
	}
}

func TestTUIThinkingModePrefersModeAndFallsBackToLegacyShowThinking(t *testing.T) {
	if got := ResolveTUIThinkingMode(Settings{}); got != TUIThinkingModeFull {
		t.Fatalf("default mode = %q, want %q", got, TUIThinkingModeFull)
	}
	show := false
	if got := ResolveTUIThinkingMode(Settings{TUI: &TUISettings{ShowThinking: &show}}); got != TUIThinkingModeHidden {
		t.Fatalf("legacy hidden mode = %q, want %q", got, TUIThinkingModeHidden)
	}
	show = true
	settings := Settings{TUI: &TUISettings{ShowThinking: &show, ThinkingMode: TUIThinkingModeSummary}}
	if got := ResolveTUIThinkingMode(settings); got != TUIThinkingModeSummary {
		t.Fatalf("explicit mode = %q, want %q", got, TUIThinkingModeSummary)
	}
}

func TestNormalizeTUIThinkingModeRejectsUnknownValue(t *testing.T) {
	for _, mode := range []string{TUIThinkingModeFull, TUIThinkingModeSummary, TUIThinkingModeHidden} {
		got, err := NormalizeTUIThinkingMode(mode)
		if err != nil || got != mode {
			t.Fatalf("NormalizeTUIThinkingMode(%q) = %q, %v", mode, got, err)
		}
	}
	if _, err := NormalizeTUIThinkingMode("collapsed-ish"); err == nil {
		t.Fatal("unknown thinking mode was accepted")
	}
}

func TestMultimodalModelForUsesConfiguredModalityModels(t *testing.T) {
	t.Setenv("IMAGE_MODEL", "vision-env-model")
	settings := Settings{Multimodal: &MultimodalSettings{
		DefaultModel: "default-mm",
		Models: map[string]string{
			"image": "vision-configured-model",
			"audio": "audio-model",
		},
	}}
	if got := MultimodalModelFor(settings, "primary", "image/png"); got != "vision-configured-model" {
		t.Fatalf("image model = %q", got)
	}
	if got := MultimodalModelFor(settings, "primary", "voice"); got != "audio-model" {
		t.Fatalf("audio model = %q", got)
	}
	if got := MultimodalModelFor(settings, "primary", "video"); got != "default-mm" {
		t.Fatalf("video model = %q", got)
	}
}

func TestMultimodalModelForFallsBackToPrimaryWhenDisabled(t *testing.T) {
	disabled := false
	settings := Settings{Multimodal: &MultimodalSettings{
		Enabled:      &disabled,
		DefaultModel: "default-mm",
		Models:       map[string]string{"image": "vision-model"},
	}}
	if got := MultimodalModelFor(settings, "primary", "image"); got != "primary" {
		t.Fatalf("disabled multimodal model = %q", got)
	}
}

func TestExplicitSettingsMergesMultimodalConfig(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
model: primary
multimodal:
  enabled: true
  defaultModel: omni-default
  models:
    image: vision-a
    audio: audio-a
`)
	mustWrite(t, filepath.Join(project, "config", "config.local.yaml"), `
multimodal:
  models:
    image: vision-local
    video: video-local
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, "config", "config.yaml"),
		filepath.Join(project, "config", "config.local.yaml"),
	)
	if loaded.Multimodal == nil {
		t.Fatal("multimodal settings not loaded")
	}
	if got := MultimodalModelFor(loaded.Settings, loaded.Model, "image"); got != "vision-local" {
		t.Fatalf("merged image model = %q", got)
	}
	if got := MultimodalModelFor(loaded.Settings, loaded.Model, "audio"); got != "audio-a" {
		t.Fatalf("merged audio model = %q", got)
	}
	if got := MultimodalModelFor(loaded.Settings, loaded.Model, "video"); got != "video-local" {
		t.Fatalf("merged video model = %q", got)
	}
}

func TestLoadForCWDSupportsAuthTokenAliases(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "from-env")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "provider": "anthropic",
	  "baseURL": "https://api.example.test",
	  "authToken": "from-settings"
	}`)
	cfg := LoadForCWD(project)
	if cfg.AuthToken != "from-settings" {
		t.Fatalf("AuthToken = %q, want canonical settings value", cfg.AuthToken)
	}

	cfg = LoadForCWD(project)
	if cfg.AuthToken != "from-settings" {
		t.Fatalf("AuthToken = %q after env removal, want canonical settings value", cfg.AuthToken)
	}
}

func TestLoadSettingsParsesSandbox(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "sandbox": {
	    "enabled": true,
	    "failIfUnavailable": true,
	    "allowUnsandboxedCommands": false,
	    "enabledPlatforms": ["macos"],
	    "excludedCommands": ["docker:*"],
	    "filesystem": {
	      "allowWrite": ["./tmp"],
	      "denyWrite": [".claude/skills"],
	      "denyRead": ["~/.ssh"]
	    },
	    "network": {
	      "disabled": true,
	      "allowDomains": ["docs.example.com"],
	      "denyDomains": ["blocked.example.com"],
	      "proxy": {
	        "url": "http://127.0.0.1:8080",
	        "mode": "required",
	        "required": true
	      },
	      "mitm": {
	        "caFile": "./certs/dev-ca.pem",
	        "required": true
	      }
	    }
	  }
	}`)
	loaded := LoadSettings(project)
	if loaded.Sandbox == nil || loaded.Sandbox.Enabled == nil || !*loaded.Sandbox.Enabled {
		t.Fatalf("sandbox enabled = %#v", loaded.Sandbox)
	}
	if loaded.Sandbox.AllowUnsandboxedCommands == nil || *loaded.Sandbox.AllowUnsandboxedCommands {
		t.Fatalf("allowUnsandboxedCommands = %#v", loaded.Sandbox.AllowUnsandboxedCommands)
	}
	if strings.Join(loaded.Sandbox.EnabledPlatforms, ",") != "macos" || strings.Join(loaded.Sandbox.Filesystem.AllowWrite, ",") != "./tmp" {
		t.Fatalf("sandbox = %+v", loaded.Sandbox)
	}
	if loaded.Sandbox.Network.Disabled == nil || !*loaded.Sandbox.Network.Disabled || strings.Join(loaded.Sandbox.Network.AllowDomains, ",") != "docs.example.com" || strings.Join(loaded.Sandbox.Network.DenyDomains, ",") != "blocked.example.com" {
		t.Fatalf("sandbox network = %+v", loaded.Sandbox.Network)
	}
	if loaded.Sandbox.Network.Proxy.URL != "http://127.0.0.1:8080" || loaded.Sandbox.Network.Proxy.Mode != "required" || loaded.Sandbox.Network.Proxy.Required == nil || !*loaded.Sandbox.Network.Proxy.Required {
		t.Fatalf("sandbox network proxy = %+v", loaded.Sandbox.Network.Proxy)
	}
	if loaded.Sandbox.Network.MITM.CAFile != "./certs/dev-ca.pem" || loaded.Sandbox.Network.MITM.Required == nil || !*loaded.Sandbox.Network.MITM.Required {
		t.Fatalf("sandbox network mitm = %+v", loaded.Sandbox.Network.MITM)
	}
}

func TestLoadSettingsParsesWebSearchEndpoint(t *testing.T) {
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `webSearch:
  endpoint: https://cn.bing.com/search
`)
	loaded := LoadSettingsFile(filepath.Join(project, "config", "config.yaml"))
	if loaded.WebSearch == nil || loaded.WebSearch.Endpoint != "https://cn.bing.com/search" {
		t.Fatalf("webSearch = %+v", loaded.WebSearch)
	}
}

func TestExplicitSettingsMergesProjectYAMLConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")

	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "model": "claude-json-model",
	  "env": {"ANTHROPIC_BASE_URL": "https://json.example.test"},
	  "permissions": {"allow": ["Read"]}
	}`)
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
model: claude-yaml-model
outputStyle: Focused
language: Chinese
env:
  ANTHROPIC_BASE_URL: https://yaml.example.test/
  ANTHROPIC_API_KEY: from-yaml
permissions:
  deny:
    - Bash
mcpServers:
  yaml:
    command: echo
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, ".claude", "settings.json"),
		filepath.Join(project, "config", "config.yaml"),
	)
	if loaded.Model != "claude-yaml-model" {
		t.Fatalf("model = %q", loaded.Model)
	}
	if loaded.OutputStyle != "Focused" || loaded.Language != "Chinese" {
		t.Fatalf("prompt settings = outputStyle:%q language:%q", loaded.OutputStyle, loaded.Language)
	}
	if loaded.Env["ANTHROPIC_BASE_URL"] != "https://yaml.example.test/" || loaded.Env["ANTHROPIC_API_KEY"] != "from-yaml" {
		t.Fatalf("env = %+v", loaded.Env)
	}
	if strings.Join(loaded.Permissions.Allow, ",") != "Read" || strings.Join(loaded.Permissions.Deny, ",") != "Bash" {
		t.Fatalf("permissions = %+v", loaded.Permissions)
	}
	if _, ok := loaded.MCPServers["yaml"]; !ok {
		t.Fatalf("mcp servers = %+v", loaded.MCPServers)
	}
	if got := strings.Join(loaded.Sources, ","); !strings.Contains(got, filepath.Join("config", "config.yaml")) {
		t.Fatalf("sources = %+v", loaded.Sources)
	}
}

func TestExplicitSettingsMergesEnvironmentYAMLConfig(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_ENV", "dev")
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
model: base-model
env:
  ANTHROPIC_BASE_URL: https://base.example.test
`)
	mustWrite(t, filepath.Join(project, "config", "config.dev.yaml"), `
model: dev-model
env:
  ANTHROPIC_BASE_URL: https://dev.example.test
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, "config", "config.yaml"),
		filepath.Join(project, "config", "config.dev.yaml"),
	)
	if loaded.Model != "dev-model" || loaded.Env["ANTHROPIC_BASE_URL"] != "https://dev.example.test" {
		t.Fatalf("loaded = %+v", loaded.Settings)
	}
}

func TestExplicitSettingsMergesLocalYAMLConfigLast(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_ENV", "dev")
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
model: base-model
env:
  ANTHROPIC_BASE_URL: https://base.example.test
`)
	mustWrite(t, filepath.Join(project, "config", "config.dev.yaml"), `
model: dev-model
env:
  ANTHROPIC_BASE_URL: https://dev.example.test
`)
	mustWrite(t, filepath.Join(project, "config", "config.local.yaml"), `
model: local-model
env:
  ANTHROPIC_BASE_URL: https://local.example.test
`)
	loaded := loadExplicitSettingsForTest(t, project,
		filepath.Join(project, "config", "config.yaml"),
		filepath.Join(project, "config", "config.dev.yaml"),
		filepath.Join(project, "config", "config.local.yaml"),
	)
	if loaded.Model != "local-model" || loaded.Env["ANTHROPIC_BASE_URL"] != "https://local.example.test" {
		t.Fatalf("loaded = %+v", loaded.Settings)
	}
	if got := strings.Join(loaded.Sources, ","); !strings.Contains(got, "config.local.yaml") {
		t.Fatalf("sources = %+v", loaded.Sources)
	}
}

func TestLoadForCWDReadsPrimaryProviderType(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_ENV", "")
	isolateAmbientProviderEnv(t)
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"provider":"custom","env":{"ANTHROPIC_BASE_URL":"https://openai-compatible.example.test/v1","ANTHROPIC_API_KEY":"key"}}`)

	cfg := LoadForCWD(project)
	if cfg.Provider != "custom" {
		t.Fatalf("Provider = %q, want custom", cfg.Provider)
	}

	t.Setenv("NIUMA_PROVIDER", "anthropic-compatible")
	cfg = LoadForCWD(project)
	if cfg.Provider != "custom" {
		t.Fatalf("Provider = %q, want canonical settings value", cfg.Provider)
	}
}

func TestResolveModelPriority(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "global"))
	mustWrite(t, filepath.Join(project, "global", "settings.json"), `{"model":"global-model"}`)
	t.Setenv("CLAUDE_CODE_MODEL", "env-model")
	t.Setenv("NIUMA_ENV", "")
	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{"model": "claude-json-model"}`)
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `model: yaml-model`)

	if got := ResolveModel(project, "flag-model"); got != "flag-model" {
		t.Fatalf("explicit model = %q", got)
	}
	if got := ResolveModel(project, ""); got != "global-model" {
		t.Fatalf("process model environment must not override global settings: %q", got)
	}
	if err := os.Remove(filepath.Join(project, "config", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if got := ResolveModel(project, ""); got != "global-model" {
		t.Fatalf("global model = %q", got)
	}
	t.Setenv("CLAUDE_CODE_MODEL", "")
	if got := ResolveModel(project, ""); got != "global-model" {
		t.Fatalf("settings model = %q", got)
	}
}

// With no explicit/project/env/top-level model but a configured provider, the
// resolved default should be that provider's model, not the built-in Anthropic
// model (provider neutrality P1③).
func TestResolveModelDoesNotFallBackToConfiguredProviderModel(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_MODEL", "")
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "fallback": {"enabled": true, "providers": [
	    {"name": "glm", "type": "custom", "baseURL": "https://x", "apiKey": "y", "model": "glm-5.1"}
	  ]}
	}`)
	if got := ResolveModel(project, ""); got != "" {
		t.Fatalf("ResolveModel = %q, want no implicit provider model", got)
	}
}

func TestLoadForCWDBuildsFallbackProviders(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_ENV", "")
	isolateAmbientProviderEnv(t)
	t.Setenv("BACKUP_KEY", "expanded-key")
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"provider":"anthropic","baseURL":"https://primary.example.test","apiKey":"primary-key","fallback":{"enabled":true,"providers":[{"name":"provider1","type":"anthropic","baseURL":"https://provider1.example.test/","apiKey":"${BACKUP_KEY}","model":"fallback-model"},{"name":"provider2","type":"anthropic-compatible","baseURL":"https://provider2.example.test"}]}}`)

	cfg := LoadForCWD(project)
	if len(cfg.FallbackProviders) != 2 {
		t.Fatalf("fallback providers = %+v", cfg.FallbackProviders)
	}
	first := cfg.FallbackProviders[0]
	if first.Name != "provider1" || first.BaseURL != "https://provider1.example.test" || first.APIKey != "${BACKUP_KEY}" || first.Model != "fallback-model" {
		t.Fatalf("first provider = %+v", first)
	}
	second := cfg.FallbackProviders[1]
	if second.Type != "anthropic-compatible" || second.APIKey != "primary-key" {
		t.Fatalf("second provider = %+v", second)
	}
}

func TestFallbackProviderExplicitUnsetEnvDoesNotInheritPrimaryAuth(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_ENV", "")
	isolateAmbientProviderEnv(t)
	t.Setenv("MISSING_BACKUP_KEY", "")
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"provider":"anthropic","baseURL":"https://primary.example.test","apiKey":"primary-key","fallback":{"enabled":true,"providers":[{"name":"provider1","baseURL":"https://provider1.example.test","apiKey":"${MISSING_BACKUP_KEY}"},{"name":"provider2","baseURL":"https://provider2.example.test"}]}}`)

	cfg := LoadForCWD(project)
	if len(cfg.FallbackProviders) != 2 {
		t.Fatalf("fallback providers = %+v", cfg.FallbackProviders)
	}
	if cfg.FallbackProviders[0].APIKey != "${MISSING_BACKUP_KEY}" {
		t.Fatalf("explicit environment reference was changed: %+v", cfg.FallbackProviders[0])
	}
	if cfg.FallbackProviders[1].APIKey != "primary-key" {
		t.Fatalf("omitted provider key did not inherit primary auth: %+v", cfg.FallbackProviders[1])
	}
}

func TestExplicitFallbackProviderUnsetEnvKeepsMatchingGlobalAuth(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("SENSENOVA_API_KEY", "")
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
	  "fallback": {
	    "enabled": true,
	    "providers": [{
	      "name": "sensenova-deepseek-v4-flash",
	      "type": "custom",
	      "baseURL": "https://token.sensenova.cn/v1",
	      "apiKey": "global-sensenova-key",
	      "model": "deepseek-v4-flash"
	    }]
	  }
	}`)
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
env:
  ANTHROPIC_API_KEY: primary-key
fallback:
  enabled: true
  providers:
    - name: glm-5.1
      baseURL: https://primary.example.test/v1
      apiKey: local-glm-key
      model: glm-5.1
    - name: sensenova-deepseek-v4-flash
      type: custom
      baseURL: https://token.sensenova.cn/v1
      apiKey: ${SENSENOVA_API_KEY}
      model: deepseek-v4-flash
`)

	explicit := loadExplicitSettingsForTest(t, project, filepath.Join(home, ".niuma", "settings.json"), filepath.Join(project, "config", "config.yaml"))
	cfg := LoadForCWD(project).WithRuntimeSettings(explicit.Settings)
	if len(cfg.FallbackProviders) != 2 {
		t.Fatalf("fallback providers = %+v", cfg.FallbackProviders)
	}
	if cfg.FallbackProviders[0].Name != "glm-5.1" || cfg.FallbackProviders[0].APIKey != "local-glm-key" {
		t.Fatalf("non-matching local provider changed: %+v", cfg.FallbackProviders[0])
	}
	got := cfg.FallbackProviders[1]
	if got.Name != "sensenova-deepseek-v4-flash" || got.APIKey != "${SENSENOVA_API_KEY}" || got.Model != "deepseek-v4-flash" {
		t.Fatalf("external credential reference was expanded or replaced: %+v", got)
	}
}

func TestExplicitFallbackSettingsCanBeDisabledByEnvironmentConfig(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_ENV", "prod")
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
fallback:
  enabled: true
  providers:
    - name: provider1
      baseURL: https://provider1.example.test
      apiKey: key
`)
	mustWrite(t, filepath.Join(project, "config", "config.prod.yaml"), `
fallback:
  enabled: false
`)

	explicit := loadExplicitSettingsForTest(t, project, filepath.Join(project, "config", "config.yaml"), filepath.Join(project, "config", "config.prod.yaml"))
	cfg := LoadForCWD(project).WithRuntimeSettings(explicit.Settings)
	if len(cfg.FallbackProviders) != 0 {
		t.Fatalf("fallback providers = %+v", cfg.FallbackProviders)
	}
	if cfg.Settings.Fallback == nil || cfg.Settings.Fallback.Enabled == nil || *cfg.Settings.Fallback.Enabled {
		t.Fatalf("settings fallback = %+v", cfg.Settings.Fallback)
	}
}

func TestLoadForCWDPreservesGlobalFallbackProvidersWhenProjectListIsEmpty(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	isolateAmbientProviderEnv(t)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"enabled": true, "providers": [{"name": "global-image", "type": "custom", "baseURL": "https://images.example/v1", "apiKey": "key", "model": "gpt-image-2"}]},
  "imageGeneration": {"enabled": true, "provider": "global-image"}
}`)
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
fallback:
  providers: []
`)
	cfg, err := ResolveImageGeneration(project)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Provider != "global-image" || cfg.BaseURL != "https://images.example/v1" {
		t.Fatalf("resolved image config = %+v", cfg)
	}
}

func TestResolveImageGenerationUsesGlobalProviderWhenChatFallbackIsDisabled(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	isolateAmbientProviderEnv(t)
	mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), `{
  "fallback": {"enabled": true, "providers": [{"name": "global-image", "type": "custom", "baseURL": "https://images.example/v1", "apiKey": "key", "model": "chat-model"}]},
  "imageGeneration": {"enabled": true, "provider": "global-image"}
}`)
	mustWrite(t, filepath.Join(project, "config", "config.yaml"), `
fallback:
  enabled: false
  providers: []
`)
	cfg, err := ResolveImageGeneration(project)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Provider != "global-image" || cfg.BaseURL != "https://images.example/v1" {
		t.Fatalf("resolved image config = %+v", cfg)
	}
}

func TestConfigWithRuntimeSettingsRebuildsFallbackProviders(t *testing.T) {
	enabled := true
	disabled := false
	cfg := Config{
		APIKey: "primary-key",
		FallbackProviders: []ProviderConfig{{
			Name:    "provider1",
			BaseURL: "https://provider1.example.test",
			APIKey:  "fallback-key",
		}},
		Settings: Settings{Fallback: &FallbackSettings{Enabled: &enabled, Providers: []ProviderConfig{{
			Name:    "provider1",
			BaseURL: "https://provider1.example.test",
			APIKey:  "fallback-key",
		}}}},
	}

	next := cfg.WithRuntimeSettings(Settings{Fallback: &FallbackSettings{Enabled: &disabled}})
	if len(next.FallbackProviders) != 0 {
		t.Fatalf("fallback providers = %+v", next.FallbackProviders)
	}
	if len(cfg.FallbackProviders) != 1 {
		t.Fatalf("original config mutated fallback providers = %+v", cfg.FallbackProviders)
	}
}

func TestConfigWithRuntimeSettingsRebuildsProviderFields(t *testing.T) {
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_BASE_URL", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER",
	} {
		t.Setenv(key, "")
	}
	store := false
	settings := Settings{
		Provider:         "custom",
		ProviderProtocol: ProviderProtocolOpenAIResponses,
		Responses:        &ResponsesProviderSettings{StateMode: ResponsesStateModeStateless, Store: &store},
		BaseURL:          "https://responses.example.test/v1/",
		APIKey:           "runtime-key",
		Fallback:         &FallbackSettings{Providers: []ProviderConfig{{Name: "inherited", Type: "custom"}}},
	}

	next := (Config{Provider: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "old-key"}).WithRuntimeSettings(settings)
	if next.Provider != "custom" || next.ProviderProtocol != ProviderProtocolOpenAIResponses {
		t.Fatalf("provider/protocol = %q/%q", next.Provider, next.ProviderProtocol)
	}
	if next.BaseURL != "https://responses.example.test/v1" || next.APIKey != "runtime-key" {
		t.Fatalf("baseURL/apiKey = %q/%q", next.BaseURL, next.APIKey)
	}
	if next.Responses == nil || next.Responses.Store == nil || *next.Responses.Store {
		t.Fatalf("responses = %+v", next.Responses)
	}
	if len(next.FallbackProviders) != 1 || next.FallbackProviders[0].APIKey != "runtime-key" {
		t.Fatalf("fallback providers = %+v", next.FallbackProviders)
	}
}

func TestConfigWithRuntimeSettingsIgnoresProviderEnvironment(t *testing.T) {
	t.Setenv("NIUMA_PROVIDER", "openai-compatible")
	t.Setenv("ANTHROPIC_BASE_URL", "https://environment.example.test/v1/")
	t.Setenv("ANTHROPIC_API_KEY", "environment-key")

	next := (Config{}).WithRuntimeSettings(Settings{
		Provider: "custom", BaseURL: "https://settings.example.test", APIKey: "settings-key",
		Env: map[string]string{
			"NIUMA_PROVIDER":     "anthropic",
			"ANTHROPIC_BASE_URL": "https://settings.example.test",
			"ANTHROPIC_API_KEY":  "settings-key",
		},
	})
	if next.Provider != "custom" || next.BaseURL != "https://settings.example.test" || next.APIKey != "settings-key" {
		t.Fatalf("environment overrode canonical route: provider=%q baseURL=%q apiKey=%q", next.Provider, next.BaseURL, next.APIKey)
	}
}

func TestConfigWithRuntimeSettingsPreservesExplicitProviderFieldsWithoutOverrides(t *testing.T) {
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_BASE_URL", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER",
	} {
		t.Setenv(key, "")
	}

	next := (Config{
		Provider: "openai-compatible", BaseURL: "https://existing.example.test/v1",
		APIKey: "existing-key", AuthToken: "existing-token",
	}).WithRuntimeSettings(Settings{})
	if next.Provider != "openai-compatible" || next.BaseURL != "https://existing.example.test/v1" ||
		next.APIKey != "existing-key" || next.AuthToken != "existing-token" {
		t.Fatalf("explicit provider fields changed: provider=%q baseURL=%q apiKey=%q authToken=%q",
			next.Provider, next.BaseURL, next.APIKey, next.AuthToken)
	}
}

func TestConfigWithRuntimeSettingsReportsMissingRoute(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "NIUMA_PROVIDER", "CLAUDE_CODE_PROVIDER"} {
		t.Setenv(key, "")
	}

	next := (Config{}).WithRuntimeSettings(Settings{Provider: "openai"})
	if next.ConfigurationError == "" || !strings.Contains(next.ConfigurationError, "baseURL") {
		t.Fatalf("missing route error = %q", next.ConfigurationError)
	}
}

func TestFeatureEnabledUsesGrowthBookTargeting(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"featureFlags":{"TOKEN_BUDGET":false},"growthbook":{"enabled":true,"features":{"TOKEN_BUDGET":{"enabled":true,"userTypes":["ant"],"querySources":["repl_*"]},"KAIROS_BRIEF":{"enabled":true,"modes":["repl_main_thread"]},"CACHED_MICROCOMPACT":{"enabled":true,"models":["claude-*"]}}}}`)
	ctx := FeatureContext{UserType: "ant", QuerySource: "repl_main_thread", Mode: "repl_main_thread", Model: "claude-sonnet-4-6"}
	if FeatureEnabled(project, "TOKEN_BUDGET", ctx) {
		t.Fatal("featureFlags should win over growthbook features for TOKEN_BUDGET")
	}
	if !FeatureEnabled(project, "KAIROS_BRIEF", ctx) {
		t.Fatal("growthbook mode-targeted KAIROS_BRIEF should be enabled")
	}
	if !FeatureEnabled(project, "CACHED_MICROCOMPACT", ctx) {
		t.Fatal("growthbook model-targeted CACHED_MICROCOMPACT should be enabled")
	}
	ctx.UserType = "external"
	if FeatureEnabled(project, "TOKEN_BUDGET", ctx) {
		t.Fatal("userType targeting should disable TOKEN_BUDGET for non-ant users")
	}
}

func TestFeatureEnabledInternalOverridesAndEnvPrecedence(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER_TYPE", "ant")
	t.Setenv("CLAUDE_INTERNAL_FC_OVERRIDES", `{"TOKEN_BUDGET": true, "KAIROS_BRIEF": false}`)
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"growthbook":{"features":{"TOKEN_BUDGET":false,"KAIROS_BRIEF":true,"CACHED_MICROCOMPACT":true}}}`)

	ctx := FeatureContext{UserType: "ant", QuerySource: "repl_main_thread", Mode: "repl_main_thread", Model: "claude-sonnet-4-6"}
	if !FeatureEnabled(project, "TOKEN_BUDGET", ctx) {
		t.Fatal("ant internal override should enable TOKEN_BUDGET")
	}
	if FeatureEnabled(project, "KAIROS_BRIEF", ctx) {
		t.Fatal("ant internal override should disable KAIROS_BRIEF")
	}
	t.Setenv("NIUMA_FEATURE_CACHED_MICROCOMPACT", "false")
	if FeatureEnabled(project, "CACHED_MICROCOMPACT", ctx) {
		t.Fatal("explicit env false should win over config")
	}
}

func TestFeatureEnabledUsesRemoteGrowthBookFeatures(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization header = %q", got)
		}
		_, _ = w.Write([]byte(`{"features":{"TOKEN_BUDGET":{"enabled":true,"userTypes":["ant"]},"KAIROS_BRIEF":false}}`))
	}))
	defer server.Close()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"growthbook":{"enabled":true,"url":"`+server.URL+`","refreshIntervalSeconds":3600,"headers":{"Authorization":"Bearer test-token"}}}`)

	ctx := FeatureContext{UserType: "ant", QuerySource: "repl_main_thread", Mode: "repl_main_thread", Model: "claude-sonnet-4-6"}
	if !FeatureEnabled(project, "TOKEN_BUDGET", ctx) {
		t.Fatal("remote growthbook feature should enable TOKEN_BUDGET")
	}
	if FeatureEnabled(project, "KAIROS_BRIEF", ctx) {
		t.Fatal("remote growthbook feature should disable KAIROS_BRIEF")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one cached fetch", requests)
	}
	if !FeatureEnabled(project, "TOKEN_BUDGET", ctx) || requests != 1 {
		t.Fatalf("cached feature fetch not reused, requests=%d", requests)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// isolateAmbientProviderEnv clears every environment variable the config loader
// reads from the process environment. Any test that writes provider settings to
// disk and then asserts on the loaded Config must call it: the loader prefers the
// ambient value over the file, so whatever the developer or CI happens to export
// silently overrides the fixture. A developer with ANTHROPIC_BASE_URL exported —
// the normal state when using a gateway — saw five tests in this package fail on
// a clean checkout.
//
// Clearing to "" is equivalent to unset here because every read is
// os.Getenv/product.Getenv followed by an emptiness check; none of these keys is
// read through LookupEnv, which would distinguish the two.
//
// The NIUMA_ names must be cleared under every legacy prefix too:
// product.Getenv walks the alias list and skips empty values, so clearing only
// the canonical spelling still lets NIUMA_PROVIDER and friends through.
func isolateAmbientProviderEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_AUTH_TOKEN",
		"CLAUDE_CODE_MODEL",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"CLAUDE_CODE_PROVIDER",
	} {
		t.Setenv(key, "")
	}
	prefixes := []string{
		product.EnvPrefix,
		product.LegacyEnvPrefix,
		product.LegacyShortEnvPrefix,
		product.LegacyCompactEnvPrefix,
	}
	for _, suffix := range []string{"PROVIDER", "INIT_DETAIL_LEVEL"} {
		for _, prefix := range prefixes {
			t.Setenv(prefix+suffix, "")
		}
	}
}

func TestConfigSelectProviderPromotesNamedFallback(t *testing.T) {
	cfg := Config{
		Provider: "custom",
		BaseURL:  "https://primary.example.test/v1",
		APIKey:   "primary-key",
		FallbackProviders: []ProviderConfig{
			{Name: "selected", Type: "custom", BaseURL: "https://selected.example.test/v1", APIKey: "selected-key", Model: "selected-model"},
			{Name: "other", Type: "custom", BaseURL: "https://other.example.test/v1", APIKey: "other-key", Model: "other-model"},
		},
	}

	selected, err := cfg.SelectProvider("selected")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Provider != "custom" || selected.BaseURL != "https://selected.example.test/v1" || selected.APIKey != "selected-key" {
		t.Fatalf("selected config = %+v", selected)
	}
	if selected.SelectedProvider != "selected" || selected.SelectedProviderModel != "selected-model" {
		t.Fatalf("selected provider metadata = %+v", selected)
	}
	if len(selected.FallbackProviders) != 1 || selected.FallbackProviders[0].Name != "other" {
		t.Fatalf("fallback providers = %+v", selected.FallbackProviders)
	}
}

func TestConfigSelectProviderRejectsUnknownName(t *testing.T) {
	cfg := Config{FallbackProviders: []ProviderConfig{{Name: "known"}}}
	_, err := cfg.SelectProvider("missing")
	if err == nil || !strings.Contains(err.Error(), "known") {
		t.Fatalf("error = %v, want available provider name", err)
	}
}

func TestMergeSettingsCarriesEffort(t *testing.T) {
	merged := mergeSettings(Settings{Effort: "low"}, Settings{Effort: "medium"})
	if merged.Effort != "medium" {
		t.Fatalf("override should win, got %q", merged.Effort)
	}
	merged = mergeSettings(Settings{Effort: "low"}, Settings{})
	if merged.Effort != "low" {
		t.Fatalf("empty override must not clear base, got %q", merged.Effort)
	}
}

func TestSettingsYAMLParsesEffort(t *testing.T) {
	var s Settings
	if err := yaml.Unmarshal([]byte("model: glm-5.1\neffort: medium\n"), &s); err != nil {
		t.Fatal(err)
	}
	if s.Effort != "medium" {
		t.Fatalf("effort = %q", s.Effort)
	}
}

// Explicit file merges retain parser/field coverage without re-enabling runtime
// discovery. Callers list the exact parser inputs and merge order.
func loadExplicitSettingsForTest(t *testing.T, cwd string, paths ...string) LoadedSettings {
	t.Helper()
	var loaded LoadedSettings
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		settings := LoadSettingsFile(path)
		annotatePermissionSources(&settings, permissionSourceForPath(cwd, path))
		loaded.Settings = MergeSettings(loaded.Settings, settings)
		loaded.Sources = append(loaded.Sources, path)
	}
	return loaded
}
