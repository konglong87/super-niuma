package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInspectSettingsMatchesRuntimeAndIgnoresProjectOverride(t *testing.T) {
	global, workspace := t.TempDir(), t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", global)
	project := filepath.Join(workspace, ".niuma")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(global, "settings.json"):  `{"provider":"openai","model":"same-model","providerProtocol":"openai-responses","responses":{"store":false},"env":{"ANTHROPIC_API_KEY":"private"},"permissions":{"allow":["Read"]}}`,
		filepath.Join(project, "settings.json"): `{"model":"same-model","providerProtocol":"openai-chat-completions","permissions":{"allow":["Glob"]}}`,
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	loaded, sources := InspectSettings(workspace)
	if want := LoadSettings(workspace); !reflect.DeepEqual(loaded, want) {
		t.Fatalf("inspection differs from runtime: got=%+v want=%+v", loaded, want)
	}
	if sources["model"] != filepath.Join(global, "settings.json") {
		t.Fatalf("global source wrong: %v", sources)
	}
	if sources["responses.store"] != filepath.Join(global, "settings.json") {
		t.Fatal("project protocol switch affected global responses source")
	}
}

func TestValidateSettingsAllowsExtensibleModelsAndSupportedRoutes(t *testing.T) {
	for _, settings := range []Settings{
		{},
		{Provider: "custom", Model: "vendor/future-model", ProviderProtocol: ProviderProtocolOpenAIResponses, Responses: &ResponsesProviderSettings{StateMode: ResponsesStateModeStateless}},
		{Provider: "anthropic-compatible", ProviderProtocol: ProviderProtocolAnthropicMessages},
		{Provider: "custom", BaseURL: "https://example.test/v1", APIKey: "key", ProviderProtocol: ProviderProtocolOpenAIResponses},
	} {
		if issues := ValidateSettings(settings); len(issues) > 0 {
			t.Fatalf("valid settings rejected: %+v", issues)
		}
	}
}
