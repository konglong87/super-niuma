package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProviderProtocolKeepsLegacyKinds(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		protocol ProviderProtocol
	}{
		{name: "empty defaults to anthropic", protocol: ProviderProtocolAnthropicMessages},
		{name: "anthropic compatible", kind: "anthropic-compatible", protocol: ProviderProtocolAnthropicMessages},
		{name: "custom", kind: "custom", protocol: ProviderProtocolOpenAIChatCompletions},
		{name: "openai", kind: "openai", protocol: ProviderProtocolOpenAIChatCompletions},
		{name: "openai compatible", kind: "openai-compatible", protocol: ProviderProtocolOpenAIChatCompletions},
		{name: "chat completions", kind: "openai-chat-completions", protocol: ProviderProtocolOpenAIChatCompletions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := ResolveProviderProtocol(tt.kind, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Explicit || resolved.Protocol != tt.protocol {
				t.Fatalf("resolved = %+v, want legacy %q", resolved, tt.protocol)
			}
		})
	}
}

func TestResolveProviderProtocolResponsesIsExplicitAndStateless(t *testing.T) {
	resolved, err := ResolveProviderProtocol("custom", " OPENAI-RESPONSES ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Explicit || resolved.Protocol != ProviderProtocolOpenAIResponses {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved.StateMode != ResponsesStateModeStateless || resolved.Store == nil || *resolved.Store {
		t.Fatalf("responses settings = %+v", resolved.ResponsesProviderSettings)
	}
}

func TestResolveProviderProtocolRejectsUnsupportedResponsesState(t *testing.T) {
	store := true
	tests := []struct {
		name     string
		settings *ResponsesProviderSettings
		contains string
	}{
		{
			name:     "previous response id",
			settings: &ResponsesProviderSettings{StateMode: ResponsesStateModePreviousResponseID},
			contains: "not implemented",
		},
		{
			name:     "store true",
			settings: &ResponsesProviderSettings{Store: &store},
			contains: "store=true is not implemented",
		},
		{
			name:     "unknown state",
			settings: &ResponsesProviderSettings{StateMode: "remote-magic"},
			contains: "unsupported responses state mode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveProviderProtocol("custom", ProviderProtocolOpenAIResponses, tt.settings)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("error = %v, want %q", err, tt.contains)
			}
		})
	}
}

func TestResolveProviderProtocolRejectsConflicts(t *testing.T) {
	tests := []struct {
		kind     string
		protocol ProviderProtocol
	}{
		{kind: "anthropic", protocol: ProviderProtocolOpenAIResponses},
		{kind: "openai-chat-completions", protocol: ProviderProtocolOpenAIResponses},
		{kind: "custom", protocol: ProviderProtocolAnthropicMessages},
	}
	for _, tt := range tests {
		if _, err := ResolveProviderProtocol(tt.kind, tt.protocol, nil); err == nil || !strings.Contains(err.Error(), "incompatible") {
			t.Fatalf("ResolveProviderProtocol(%q, %q) error = %v", tt.kind, tt.protocol, err)
		}
	}
	if _, err := ResolveProviderProtocol("custom", "unknown-wire", nil); err == nil || !strings.Contains(err.Error(), "unsupported provider protocol") {
		t.Fatalf("unknown protocol error = %v", err)
	}
}

func TestLoadForCWDReadsPrimaryAndFallbackProtocols(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_ENV", "")
	isolateAmbientProviderEnv(t)
	mustWrite(t, filepath.Join(project, "config", "settings.json"), `{"provider":"custom","providerProtocol":"openai-responses","baseURL":"https://primary.example.test/v1","apiKey":"primary-key","responses":{"stateMode":"stateless","store":false},"fallback":{"providers":[{"name":"responses-fallback","type":"openai-compatible","protocol":"openai-responses","baseURL":"https://fallback.example.test/v1","apiKey":"fallback-key","model":"fallback-model","responses":{"stateMode":"stateless","store":false}}]}}`)

	cfg := LoadForCWD(project)
	if cfg.ProviderProtocol != ProviderProtocolOpenAIResponses || cfg.Responses == nil || cfg.Responses.Store == nil || *cfg.Responses.Store {
		t.Fatalf("primary protocol/settings = %q %+v", cfg.ProviderProtocol, cfg.Responses)
	}
	if cfg.BaseURL != "https://primary.example.test/v1" {
		t.Fatalf("primary base URL = %q", cfg.BaseURL)
	}
	if len(cfg.FallbackProviders) != 1 {
		t.Fatalf("fallback providers = %+v", cfg.FallbackProviders)
	}
	fallback := cfg.FallbackProviders[0]
	if fallback.Protocol != ProviderProtocolOpenAIResponses || fallback.Responses == nil || fallback.Responses.StateMode != ResponsesStateModeStateless {
		t.Fatalf("fallback protocol/settings = %q %+v", fallback.Protocol, fallback.Responses)
	}
	if fallback.BaseURL != "https://fallback.example.test/v1" {
		t.Fatalf("fallback base URL = %q", fallback.BaseURL)
	}
}

func TestSelectProviderCarriesProtocolWithoutMutatingFallback(t *testing.T) {
	store := false
	cfg := Config{FallbackProviders: []ProviderConfig{{
		Name:      "responses",
		Type:      "custom",
		Protocol:  ProviderProtocolOpenAIResponses,
		BaseURL:   "https://responses.example.test/v1",
		APIKey:    "key",
		Model:     "model",
		Responses: &ResponsesProviderSettings{StateMode: ResponsesStateModeStateless, Store: &store},
	}}}

	selected, err := cfg.SelectProvider("responses")
	if err != nil {
		t.Fatal(err)
	}
	if selected.ProviderProtocol != ProviderProtocolOpenAIResponses || selected.Responses == nil || selected.Responses.Store == nil || *selected.Responses.Store {
		t.Fatalf("selected protocol/settings = %q %+v", selected.ProviderProtocol, selected.Responses)
	}
	*selected.Responses.Store = true
	if *cfg.FallbackProviders[0].Responses.Store {
		t.Fatal("SelectProvider aliased fallback responses settings")
	}
}

func TestMergeSettingsMergesResponsesFields(t *testing.T) {
	store := false
	merged := MergeSettings(
		Settings{ProviderProtocol: ProviderProtocolOpenAIResponses, Responses: &ResponsesProviderSettings{StateMode: ResponsesStateModeStateless}},
		Settings{Responses: &ResponsesProviderSettings{Store: &store}},
	)
	if merged.ProviderProtocol != ProviderProtocolOpenAIResponses || merged.Responses == nil || merged.Responses.StateMode != ResponsesStateModeStateless || merged.Responses.Store == nil || *merged.Responses.Store {
		t.Fatalf("merged settings = %+v", merged)
	}
}
