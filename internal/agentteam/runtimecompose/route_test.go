package runtimecompose

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/promptdump"
	"github.com/konglong87/super-niuma/internal/query"
)

func routeTestConfig() config.Config {
	cfg := testConfig()
	cfg.SelectedProvider = "primary-service"
	cfg.BaseURL = "https://route-user:route-password@primary.example/v1?api_key=query-private-credential#private-fragment"
	cfg.FallbackProviders = []config.ProviderConfig{
		{Name: "backup-a", Type: "anthropic", Model: "backup-a-model", BaseURL: "https://backup-user:backup-password@backup-a.example/v1?token=fallback-query-secret", APIKey: "backup-a-private-key", AuthToken: "backup-a-private-auth"},
		{Name: "backup-b", Type: "anthropic", Model: "backup-b-model", BaseURL: "https://backup-b.example/v1", APIKey: "backup-b-private-key"},
	}
	return cfg
}

func TestPreflightRouteHashRoundTripAndPublicOnly(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	cfg := routeTestConfig()
	e := NewTextExecutor(resolved(cfg))
	agent, err := e.Preflight(context.Background(), testInput().Agent)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := hex.DecodeString(agent.RouteHash)
	if err != nil || len(digest) != 32 {
		t.Fatalf("missing SHA-256 route hash: %q err=%v", agent.RouteHash, err)
	}
	again, err := e.Preflight(context.Background(), agent)
	if err != nil || again.RouteHash != agent.RouteHash {
		t.Fatalf("route fence is not stable: agent=%+v err=%v", again, err)
	}
	// Deliberately change every credential container, URL userinfo, query and
	// fragment. None may contribute even indirectly to the persisted route hash.
	cfg.APIKey = "rotated-primary-key"
	cfg.AuthToken = "rotated-primary-auth"
	cfg.Settings.APIKey = "rotated-settings-key"
	cfg.Settings.AuthToken = "rotated-settings-auth"
	cfg.Settings.Fallback = &config.FallbackSettings{Providers: []config.ProviderConfig{{APIKey: "rotated-settings-backup-key", AuthToken: "rotated-settings-backup-auth"}}}
	cfg.BaseURL = "https://another-user:rotated-password@primary.example/v1?entirely=different&auth_token=rotated-query#changed-fragment"
	cfg.FallbackProviders[0].APIKey = "rotated-backup-key"
	cfg.FallbackProviders[0].AuthToken = "rotated-backup-auth"
	cfg.FallbackProviders[0].BaseURL = "https://new-backup-user:new-backup-password@backup-a.example/v1?new_query=new-secret#fragment"
	cfg.FallbackProviders[1].APIKey = "rotated-second-backup-key"
	rotated, err := NewTextExecutor(resolved(cfg)).Preflight(context.Background(), agent)
	if err != nil || rotated.RouteHash != agent.RouteHash {
		t.Fatalf("credential rotation changed public route hash: agent=%+v err=%v", rotated, err)
	}
	// A default-model edit cannot replace the immutable effective agent model.
	cfg.Settings.Model = "changed-global-default"
	retained, err := NewTextExecutor(resolved(cfg)).Preflight(context.Background(), agent)
	if err != nil || retained.Model != agent.Model || retained.RouteHash != agent.RouteHash {
		t.Fatalf("global default replaced pinned attempt route: %+v err=%v", retained, err)
	}
}

func TestRouteDriftFailsBeforeClientInitialization(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	tests := []struct {
		name   string
		mutate func(*config.Config, *workflow.Agent)
	}{
		{"primary host", func(c *config.Config, a *workflow.Agent) { c.BaseURL = "https://different.example/v1" }},
		{"primary path", func(c *config.Config, a *workflow.Agent) { c.BaseURL = "https://primary.example/v2" }},
		{"primary scheme", func(c *config.Config, a *workflow.Agent) { c.BaseURL = "http://primary.example/v1" }},
		{"primary provider kind", func(c *config.Config, a *workflow.Agent) { c.Provider = "openai" }},
		{"primary route renamed", func(c *config.Config, a *workflow.Agent) { c.SelectedProvider = "renamed-service" }},
		{"primary protocol", func(c *config.Config, a *workflow.Agent) {
			c.ProviderProtocol = config.ProviderProtocolAnthropicMessages
		}},
		{"explicit agent model", func(c *config.Config, a *workflow.Agent) { a.Model = "different-model" }},
		{"selected model pin", func(c *config.Config, a *workflow.Agent) { c.SelectedProviderModel = "changed-named-model" }},
		{"fallback endpoint", func(c *config.Config, a *workflow.Agent) {
			c.FallbackProviders[0].BaseURL = "https://different-backup.example/v1"
		}},
		{"fallback path", func(c *config.Config, a *workflow.Agent) {
			c.FallbackProviders[0].BaseURL = "https://backup-a.example/v2"
		}},
		{"fallback provider", func(c *config.Config, a *workflow.Agent) { c.FallbackProviders[0].Type = "openai" }},
		{"fallback protocol", func(c *config.Config, a *workflow.Agent) {
			c.FallbackProviders[0].Protocol = config.ProviderProtocolAnthropicMessages
		}},
		{"fallback model", func(c *config.Config, a *workflow.Agent) { c.FallbackProviders[0].Model = "changed-backup-model" }},
		{"fallback name", func(c *config.Config, a *workflow.Agent) { c.FallbackProviders[0].Name = "changed-backup-name" }},
		{"fallback removal", func(c *config.Config, a *workflow.Agent) { c.FallbackProviders = c.FallbackProviders[:1] }},
		{"fallback addition", func(c *config.Config, a *workflow.Agent) {
			c.FallbackProviders = append(c.FallbackProviders, config.ProviderConfig{Type: "anthropic", BaseURL: "https://extra.example/v1", APIKey: "extra-backup-key"})
		}},
		{"fallback order", func(c *config.Config, a *workflow.Agent) {
			c.FallbackProviders[0], c.FallbackProviders[1] = c.FallbackProviders[1], c.FallbackProviders[0]
		}},
		{"supplied stale hash", func(c *config.Config, a *workflow.Agent) { a.RouteHash = "not-the-current-route" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := routeTestConfig()
			agent, err := NewTextExecutor(resolved(cfg)).Preflight(context.Background(), testInput().Agent)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(&cfg, &agent)
			e := NewTextExecutorWithStreamer(resolved(cfg), func(config.Config) query.MessageStreamer {
				t.Fatal("route drift initialized a model client")
				return nil
			})
			_, err = e.Preflight(context.Background(), agent)
			typed := requireCode(t, err, CodeConfig)
			if typed.Retryable || !strings.Contains(typed.Message, "route changed") || !strings.Contains(typed.Message, "start new run") {
				t.Fatalf("route drift error is not actionable: %+v", typed)
			}
			in := testInput()
			in.Agent = agent
			got, err := e.Execute(context.Background(), in, nil)
			requireCode(t, err, CodeConfig)
			if got.Status != workflow.Failed || got.Output != "" {
				t.Fatalf("route drift result=%+v", got)
			}
		})
	}
}

func TestCredentialRotationPassesExecuteRouteFence(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	cfg := testConfig()
	cfg.SelectedProvider = "primary-service"
	agent, err := NewTextExecutor(resolved(cfg)).Preflight(context.Background(), testInput().Agent)
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKey = "rotated-primary-credential"
	cfg.AuthToken = "rotated-primary-auth-token"
	e := fakeExecutor(cfg, func(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
		return response("credential rotation succeeded " + cfg.APIKey), nil
	})
	in := testInput()
	in.Agent = agent
	got, err := e.Execute(context.Background(), in, nil)
	if err != nil || got.Status != workflow.Completed || strings.Contains(got.Output, cfg.APIKey) {
		t.Fatalf("credential-only rotation did not pass safely: %+v err=%v", got, err)
	}
}

func TestPublicRouteHashDoesNotReadCredentialContainers(t *testing.T) {
	cfg := routeTestConfig()
	original, err := publicRouteHash(cfg, "effective-model")
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKey = ""
	cfg.AuthToken = ""
	cfg.Settings = config.Settings{}
	for i := range cfg.FallbackProviders {
		cfg.FallbackProviders[i].APIKey = ""
		cfg.FallbackProviders[i].AuthToken = ""
	}
	emptyCredentials, err := publicRouteHash(cfg, "effective-model")
	if err != nil || emptyCredentials != original {
		t.Fatalf("credentials contributed to public route hash: %q != %q err=%v", emptyCredentials, original, err)
	}
}

func TestRouteFenceNewRunAndMalformedURLSafety(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	cfg := routeTestConfig()
	agent, err := NewTextExecutor(resolved(cfg)).Preflight(context.Background(), testInput().Agent)
	if err != nil {
		t.Fatal(err)
	}
	cfg.BaseURL = "https://new-origin.example/v1"
	fresh := agent
	fresh.RouteHash = ""
	replacement, err := NewTextExecutor(resolved(cfg)).Preflight(context.Background(), fresh)
	if err != nil || replacement.RouteHash == "" || replacement.RouteHash == agent.RouteHash {
		t.Fatalf("new run did not bind new public route: %+v err=%v", replacement, err)
	}
	cfg.BaseURL = "https://private-user:private-userinfo@host.example/%zz?api_key=" + cfg.APIKey
	_, err = NewTextExecutor(resolved(cfg)).Preflight(context.Background(), fresh)
	requireCode(t, err, CodeConfig)
	for _, secret := range []string{cfg.APIKey, "private-userinfo", cfg.BaseURL} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("malformed route URL leaked credentials in error")
		}
	}
}
