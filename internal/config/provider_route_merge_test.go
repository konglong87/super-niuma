package config

import "testing"

func TestProviderSwitchDropsInheritedProtocolAndCredentials(t *testing.T) {
	base := Settings{Provider: "custom", Model: "gpt-test", ProviderProtocol: ProviderProtocolOpenAIResponses, Responses: &ResponsesProviderSettings{StateMode: ResponsesStateModeStateless}, BaseURL: "https://old.example", APIKey: "old-secret", Env: map[string]string{"ANTHROPIC_API_KEY": "legacy-secret", "OTHER": "keep"}}
	got := MergeSettings(base, Settings{Provider: "anthropic", Model: "claude-test"})
	if _, err := ResolveProviderProtocol(got.Provider, got.ProviderProtocol, got.Responses); err != nil {
		t.Fatal(err)
	}
	if got.Model != "claude-test" || got.Responses != nil || got.BaseURL != "" || got.APIKey != "" || got.Env["ANTHROPIC_API_KEY"] != "legacy-secret" || got.Env["OTHER"] != "keep" {
		t.Fatalf("unexpected merged route: %+v", got)
	}
	if base.APIKey != "old-secret" || base.Env["ANTHROPIC_API_KEY"] != "legacy-secret" {
		t.Fatal("merge mutated source settings")
	}
}

func TestModelOnlyOverridePreservesRoute(t *testing.T) {
	got := MergeSettings(Settings{Provider: "custom", Model: "one", ProviderProtocol: ProviderProtocolOpenAIResponses}, Settings{Model: "two"})
	if got.Provider != "custom" || got.ProviderProtocol != ProviderProtocolOpenAIResponses || got.Model != "two" {
		t.Fatalf("unexpected route: %+v", got)
	}
}

func TestExplicitConflictingProviderRouteRemainsAnError(t *testing.T) {
	got := MergeSettings(Settings{Provider: "custom"}, Settings{Provider: "anthropic", ProviderProtocol: ProviderProtocolOpenAIResponses})
	if _, err := ResolveProviderProtocol(got.Provider, got.ProviderProtocol, got.Responses); err == nil {
		t.Fatal("explicit conflict was silently repaired")
	}
}
