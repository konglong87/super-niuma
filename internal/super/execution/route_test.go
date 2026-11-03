package execution

import (
	"context"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestRouteFingerprintPublicFieldsAndSecretRotation(t *testing.T) {
	cfg := fixtureConfig()
	r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
	baseline, err := r.RouteFingerprint(context.Background(), "fixture", "fixture-model")
	if err != nil || len(baseline) != 64 {
		t.Fatalf("fingerprint=%q err=%v", baseline, err)
	}
	for _, change := range []func(*config.Config){
		func(c *config.Config) { c.APIKey = "new-secret"; c.AuthToken = "rotated-secret" },
		func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Name: "not-selected", Type: "anthropic", BaseURL: "https://unused.invalid", APIKey: "unused-secret"}}
		},
		func(c *config.Config) { c.BaseURL += "/" },
		func(c *config.Config) { c.ProviderProtocol = config.ProviderProtocolAnthropicMessages },
	} {
		cfg = fixtureConfig()
		change(&cfg)
		got, err := r.RouteFingerprint(context.Background(), "fixture", "fixture-model")
		if err != nil || got != baseline {
			t.Fatalf("non-route change invalidated grant: %q %v", got, err)
		}
	}
	for _, change := range []func(*config.Config){
		func(c *config.Config) { c.BaseURL = "https://new-endpoint.invalid" },
		func(c *config.Config) { c.BaseURL = "https://model.invalid/other" },
		func(c *config.Config) {
			c.Provider = "openai"
			c.ProviderProtocol = config.ProviderProtocolOpenAIChatCompletions
		},
		func(c *config.Config) {
			c.Provider = "openai"
			c.ProviderProtocol = config.ProviderProtocolOpenAIResponses
		},
	} {
		cfg = fixtureConfig()
		change(&cfg)
		got, err := r.RouteFingerprint(context.Background(), "fixture", "fixture-model")
		if err != nil || got == baseline {
			t.Fatalf("route drift undetected: %q %v", got, err)
		}
	}
	cfg = fixtureConfig()
	changedModel, err := r.RouteFingerprint(context.Background(), "fixture", "different-model")
	if err != nil || changedModel == baseline {
		t.Fatal("model drift undetected", err)
	}
}

func TestRouteFingerprintUsesOnlySelectedRouteAndEffectiveResponsesSettings(t *testing.T) {
	cfg := fixtureConfig()
	cfg.FallbackProviders = []config.ProviderConfig{{Name: "selected", Type: "openai", Protocol: config.ProviderProtocolOpenAIResponses, BaseURL: "https://selected.invalid/v1", APIKey: "secret", Model: "pinned"}}
	r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
	first, err := r.RouteFingerprint(context.Background(), "selected", "pinned")
	if err != nil {
		t.Fatal(err)
	}
	no := false
	cfg.FallbackProviders[0].Responses = &config.ResponsesProviderSettings{Store: &no, StateMode: config.ResponsesStateModeStateless}
	cfg.APIKey = "changed-primary-key"
	cfg.BaseURL = "https://changed-primary.invalid"
	same, err := r.RouteFingerprint(context.Background(), "selected", "pinned")
	if err != nil || same != first {
		t.Fatal("effective selected route changed unexpectedly", err)
	}
	cfg.FallbackProviders[0].BaseURL = "https://new-selected.invalid/v1"
	different, err := r.RouteFingerprint(context.Background(), "selected", "pinned")
	if err != nil || different == first {
		t.Fatal("selected route drift ignored", err)
	}
	yes := true
	cfg.FallbackProviders[0].Responses.Store = &yes
	if _, err := r.RouteFingerprint(context.Background(), "selected", "pinned"); err != ErrUnavailable {
		t.Fatal("unsupported server state fingerprint accepted", err)
	}
}
