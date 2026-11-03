package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
)

const (
	computerRuntimeModel       = "vision-model"
	computerRuntimeHostSession = "approved-host-session"
	computerRuntimeObservation = "fresh-observation"
)

var computerRuntimeOwner = cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 13}

type runtimeComputerBridge struct {
	computerUseWiringService
	sessionID        string
	lookupErr        error
	owners           []cu.SessionOwner
	observed         []cu.SessionOwner
	imageOwners      []cu.SessionOwner
	observedSession  string
	imageSession     string
	imageObservation string
	image            []byte
}

var _ desktopComputerBridge = (*runtimeComputerBridge)(nil)

func (b *runtimeComputerBridge) Lookup(_ context.Context, owner cu.SessionOwner) (string, error) {
	b.owners = append(b.owners, owner)
	return b.sessionID, b.lookupErr
}
func (b *runtimeComputerBridge) Observe(_ context.Context, owner cu.SessionOwner, req cu.ObserveRequest) (cu.Observation, error) {
	b.observed = append(b.observed, owner)
	b.observedSession = req.SessionID
	return cu.Observation{ID: computerRuntimeObservation}, nil
}
func (b *runtimeComputerBridge) ObservationImage(_ context.Context, owner cu.SessionOwner, sessionID, observationID string) ([]byte, string, error) {
	b.imageOwners = append(b.imageOwners, owner)
	b.imageSession, b.imageObservation = sessionID, observationID
	return b.image, "image/png", nil
}

func computerRuntimeConfig() config.Config {
	return config.Config{Provider: "anthropic", Settings: config.Settings{ComputerUse: &config.ComputerUseSettings{
		ImageInputRoutes: []config.ComputerUseImageInputRoute{{Provider: "primary", Model: computerRuntimeModel}},
	}}}
}

func computerRuntimeOptions(bridge desktopComputerBridge) options {
	return options{desktopComputerBridge: bridge, tenantID: computerRuntimeOwner.TenantID,
		tenantUserID: computerRuntimeOwner.UserID, tenantSessionID: computerRuntimeOwner.SessionID}
}

func TestDesktopComputerUseRuntimeGates(t *testing.T) {
	for _, test := range []struct {
		name         string
		mutate       func(*options, *config.Config, *runtimeComputerBridge)
		want         bool
		wantGuidance bool
		lookups      int
	}{
		{name: "approved", want: true, lookups: 1},
		{name: "no bridge", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.desktopComputerBridge = nil }},
		{name: "bare", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) {
			o.runtimeProfile = runtimeprofile.ProfileBare
		}},
		{name: "tools disabled", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.disableTools = true }},
		{name: "missing tenant", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.tenantID = 0 }},
		{name: "missing user", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.tenantUserID = 0 }},
		{name: "missing conversation", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.tenantSessionID = 0 }},
		{name: "negative tenant cast", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.tenantID = math.MaxUint64 }},
		{name: "negative user cast", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.tenantUserID = math.MaxUint64 }},
		{name: "negative conversation cast", mutate: func(o *options, _ *config.Config, _ *runtimeComputerBridge) { o.tenantSessionID = math.MaxUint64 }},
		{name: "undeclared images", wantGuidance: true, mutate: func(_ *options, c *config.Config, _ *runtimeComputerBridge) { c.Settings.ComputerUse = nil }},
		{name: "lookup denied", lookups: 1, mutate: func(_ *options, _ *config.Config, b *runtimeComputerBridge) {
			b.lookupErr = errors.New("sensitive host error")
		}},
		{name: "empty lookup", lookups: 1, mutate: func(_ *options, _ *config.Config, b *runtimeComputerBridge) { b.sessionID = "" }},
		{name: "blank lookup", lookups: 1, mutate: func(_ *options, _ *config.Config, b *runtimeComputerBridge) { b.sessionID = " \n" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := &runtimeComputerBridge{sessionID: computerRuntimeHostSession}
			opts, cfg := computerRuntimeOptions(bridge), computerRuntimeConfig()
			// Stale/manual flags must not bypass the production bridge lookup.
			opts.computerUseProfile, opts.computerUseImageSupported, opts.computerUseService = true, true, computerUseWiringService{}
			if test.mutate != nil {
				test.mutate(&opts, &cfg, bridge)
			}
			guidance, cleanup := configureDesktopComputerUse(context.Background(), cfg, computerRuntimeModel, &opts)
			defer cleanup()
			if opts.computerUseProfile != test.want || opts.computerUseImageSupported != test.want || (opts.computerUseService != nil) != test.want || (guidance != "") != (test.want || test.wantGuidance) {
				t.Fatalf("gate mismatch: profile=%v image=%v service=%T guidance=%q", opts.computerUseProfile, opts.computerUseImageSupported, opts.computerUseService, guidance)
			}
			if len(bridge.owners) != test.lookups {
				t.Fatalf("lookup count=%d, want %d", len(bridge.owners), test.lookups)
			}
			if test.lookups > 0 && bridge.owners[0] != computerRuntimeOwner {
				t.Fatalf("owner changed: %+v", bridge.owners)
			}
			if test.want {
				tracked, ok := opts.computerUseService.(*trackedDesktopComputerService)
				if !ok || tracked.desktopComputerBridge != bridge {
					t.Fatal("supplied bridge was not wrapped for session tracking")
				}
				for _, want := range []string{"first call", "observe", "fresh observation", "Never retry", "Stop", "untrusted data"} {
					if !strings.Contains(guidance, want) {
						t.Errorf("guidance missing %q", want)
					}
				}
			}
		})
	}
}

func TestComputerUseGuidanceIsBoundedAndForbidsAlternateGUIRoutes(t *testing.T) {
	for _, want := range []string{
		"use ComputerUse immediately",
		"Bash",
		"osascript",
		"System Events",
		"screencapture",
		"open",
		"AgentGet",
		"AgentCreate",
		"deep accessibility or UI-tree inspection",
		"first call must be action=launch_app with its registered target_id",
		"at most 8 turns and 6 input actions",
	} {
		if !strings.Contains(computerUseSystemGuidance, want) {
			t.Errorf("ComputerUse guidance missing %q", want)
		}
	}
}

func TestDesktopComputerUseUnavailableRouteReportsCapabilityWithoutEnablingIt(t *testing.T) {
	bridge := &runtimeComputerBridge{sessionID: computerRuntimeHostSession}
	opts := computerRuntimeOptions(bridge)
	guidance, cleanup := configureDesktopComputerUse(context.Background(), config.Config{}, computerRuntimeModel, &opts)
	defer cleanup()
	if !strings.Contains(guidance, "ComputerUse is unavailable") || !strings.Contains(guidance, "Do not improvise GUI automation") {
		t.Fatalf("unavailable guidance=%q", guidance)
	}
	if opts.computerUseProfile || opts.computerUseService != nil || opts.computerUseImageSupported {
		t.Fatalf("unavailable route enabled ComputerUse: profile=%v service=%T image=%v", opts.computerUseProfile, opts.computerUseService, opts.computerUseImageSupported)
	}
}

func TestDesktopComputerUseCanceledLookupCannotEnable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bridge := &runtimeComputerBridge{sessionID: computerRuntimeHostSession}
	opts := computerRuntimeOptions(bridge)
	got, cleanup := configureDesktopComputerUse(ctx, computerRuntimeConfig(), computerRuntimeModel, &opts)
	defer cleanup()
	if got != "" || opts.computerUseService != nil {
		t.Fatalf("canceled lookup enabled capability: %q", got)
	}
}

func TestComputerUseEffectiveImageRoutes(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*config.Config)
		want   bool
	}{
		{name: "primary", want: true},
		{name: "undeclared named primary", mutate: func(c *config.Config) { c.SelectedProvider = "other" }},
		{name: "protocol is not assertion", mutate: func(c *config.Config) {
			c.Settings.ComputerUse = nil
			c.ProviderProtocol = config.ProviderProtocolOpenAIResponses
		}},
		{name: "model name is not assertion", mutate: func(c *config.Config) { c.Settings.ComputerUse.ImageInputRoutes[0].Model = "gpt-4o" }},
		{name: "selected uses name not type", mutate: func(c *config.Config) { c.SelectedProvider = "named" }},
		{name: "selected exact name and final model", want: true, mutate: func(c *config.Config) {
			c.SelectedProvider = "named"
			c.SelectedProviderModel = "stale-model"
			c.Settings.Model = "also-stale"
			c.Settings.ComputerUse.ImageInputRoutes[0].Provider = "named"
		}},
		{name: "undeclared fallback", want: true, mutate: func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Name: "fallback", Type: "anthropic"}}
		}},
		{name: "fallback inherits query model", want: true, mutate: func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Name: "fallback", Type: "openai", Model: "  "}}
			c.Settings.ComputerUse.ImageInputRoutes = append(c.Settings.ComputerUse.ImageInputRoutes, config.ComputerUseImageInputRoute{Provider: "fallback", Model: computerRuntimeModel})
		}},
		{name: "fallback model overrides query", want: true, mutate: func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Name: "fallback", Type: "openai", Model: "other"}}
			c.Settings.ComputerUse.ImageInputRoutes = append(c.Settings.ComputerUse.ImageInputRoutes, config.ComputerUseImageInputRoute{Provider: "fallback", Model: computerRuntimeModel})
		}},
		{name: "fallback override declared", want: true, mutate: func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Name: "fallback", Type: "openai", Model: " other "}}
			c.Settings.ComputerUse.ImageInputRoutes = append(c.Settings.ComputerUse.ImageInputRoutes, config.ComputerUseImageInputRoute{Provider: "fallback", Model: "other"})
		}},
		{name: "unnamed fallback cannot alias primary", want: true, mutate: func(c *config.Config) { c.FallbackProviders = []config.ProviderConfig{{Type: "anthropic"}} }},
		{name: "unnamed fallback uses runtime name", want: true, mutate: func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Type: "anthropic"}}
			c.Settings.ComputerUse.ImageInputRoutes = append(c.Settings.ComputerUse.ImageInputRoutes, config.ComputerUseImageInputRoute{Provider: "fallback-1", Model: computerRuntimeModel})
		}},
		{name: "two unnamed endpoints require separate declarations", want: true, mutate: func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Type: "anthropic"}, {Type: "anthropic"}}
			c.Settings.ComputerUse.ImageInputRoutes = append(c.Settings.ComputerUse.ImageInputRoutes, config.ComputerUseImageInputRoute{Provider: "fallback-1", Model: computerRuntimeModel})
		}},
		{name: "every fallback checked", want: true, mutate: func(c *config.Config) {
			c.FallbackProviders = []config.ProviderConfig{{Type: "anthropic"}, {Type: "openai"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := computerRuntimeConfig()
			if test.mutate != nil {
				test.mutate(&cfg)
			}
			before, _ := json.Marshal(cfg)
			if got := computerUseRoutesSupportImages(cfg, computerRuntimeModel); got != test.want {
				t.Fatalf("supported=%v, want %v", got, test.want)
			}
			after, _ := json.Marshal(cfg)
			if !bytes.Equal(before, after) {
				t.Fatal("route gate changed configuration/fallback policy")
			}
		})
	}
}

// Exercise production construction, actual model request, tool dispatch and
// screenshot attachment. No live host input or external provider is required.
func TestNewQuerySessionDesktopComputerUseObserve(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	project := t.TempDir()
	var screenshot bytes.Buffer
	if err := png.Encode(&screenshot, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	bridge := &runtimeComputerBridge{sessionID: computerRuntimeHostSession, image: screenshot.Bytes()}
	var requests []json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			writeComputerObserveStream(w)
		} else {
			writeAnthropicTextStream(t, w, "observation received")
		}
	}))
	defer provider.Close()
	cfg := computerRuntimeConfig()
	cfg.Settings.Provider, cfg.Settings.BaseURL, cfg.Settings.APIKey = cfg.Provider, provider.URL, "test-only"
	if err := config.SaveGlobalSettings(cfg.Settings); err != nil {
		t.Fatal(err)
	}
	opts := computerRuntimeOptions(bridge)
	opts.cwd, opts.model, opts.maxTurns, opts.noPersistence = project, computerRuntimeModel, 3, true
	opts.toolsSpecified, opts.enabledTools, opts.permissionMode = true, []string{"ComputerUse"}, "allow"
	session, cleanup, err := newQuerySession(context.Background(), opts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(session.ToolDefinitions()) != 1 || session.ToolDefinitions()[0].Name != "ComputerUse" {
		t.Fatalf("tools=%+v", session.ToolDefinitions())
	}
	if err := session.RunText(context.Background(), "Observe the approved desktop.", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d, want 2", len(requests))
	}
	var first struct {
		Model  string `json:"model"`
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(requests[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.Model != computerRuntimeModel {
		t.Fatalf("actual model=%q", first.Model)
	}
	var system string
	for _, block := range first.System {
		system += block.Text
	}
	for _, want := range []string{"first call", "fresh observation", "Never retry", "Stop"} {
		if !strings.Contains(system, want) {
			t.Errorf("system guidance missing %q", want)
		}
	}
	if !bytes.Contains(requests[1], []byte(base64.StdEncoding.EncodeToString(screenshot.Bytes()))) {
		t.Fatal("real screenshot image not attached to subsequent model request")
	}
	for _, owners := range [][]cu.SessionOwner{bridge.owners, bridge.observed, bridge.imageOwners} {
		if !reflect.DeepEqual(owners, []cu.SessionOwner{computerRuntimeOwner}) {
			t.Fatalf("identity not propagated: %+v", owners)
		}
	}
	if bridge.observedSession != computerRuntimeHostSession || bridge.imageSession != computerRuntimeHostSession || bridge.imageObservation != computerRuntimeObservation {
		t.Fatal("host session/observation binding lost")
	}
}

func writeComputerObserveStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_cu\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"vision-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"+
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_observe\",\"name\":\"ComputerUse\",\"input\":{}}}\n\n"+
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"action\\\":\\\"observe\\\",\\\"session_id\\\":\\\""+computerRuntimeHostSession+"\\\"}\"}}\n\n"+
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\n"+
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
}

func TestNewQuerySessionDesktopComputerUseFinalRoute(t *testing.T) {
	for _, test := range []struct {
		name, declaredModel               string
		fallback, declareFallback, denied bool
		want                              bool
	}{
		{name: "final agent model approved", declaredModel: computerRuntimeModel, want: true},
		{name: "selected model is not final", declaredModel: "selected-model"},
		{name: "fallback undeclared", declaredModel: computerRuntimeModel, fallback: true, want: true},
		{name: "all routes declared", declaredModel: computerRuntimeModel, fallback: true, declareFallback: true, want: true},
		{name: "lookup denied", declaredModel: computerRuntimeModel, denied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
			project := t.TempDir()
			mustWrite(t, filepath.Join(project, ".claude", "agents", "vision.md"), "---\nname: vision\ndescription: desktop agent\nmodel: "+computerRuntimeModel+"\n---\nUse approved tools.")
			settings := config.Settings{Provider: "anthropic", BaseURL: "http://unused.invalid", APIKey: "test-only", Fallback: &config.FallbackSettings{
				Providers: []config.ProviderConfig{{Name: "selected", Type: "anthropic", BaseURL: "http://unused.invalid", APIKey: "test-only", Model: "selected-model"}},
			}, ComputerUse: &config.ComputerUseSettings{ImageInputRoutes: []config.ComputerUseImageInputRoute{{Provider: "selected", Model: test.declaredModel}}}}
			if test.fallback {
				settings.Fallback.Providers = append(settings.Fallback.Providers, config.ProviderConfig{Name: "other", Type: "openai", BaseURL: "http://unused.invalid", APIKey: "test-only", Model: "other-model"})
			}
			if test.declareFallback {
				settings.ComputerUse.ImageInputRoutes = append(settings.ComputerUse.ImageInputRoutes, config.ComputerUseImageInputRoute{Provider: "other", Model: "other-model"})
			}
			if err := config.SaveGlobalSettings(settings); err != nil {
				t.Fatal(err)
			}
			bridge := &runtimeComputerBridge{sessionID: computerRuntimeHostSession}
			if test.denied {
				bridge.lookupErr = errors.New("not approved")
			}
			opts := computerRuntimeOptions(bridge)
			opts.cwd, opts.providerName, opts.agentName, opts.noPersistence = project, "selected", "vision", true
			opts.toolsSpecified, opts.enabledTools = true, []string{"ComputerUse"}
			session, cleanup, err := newQuerySession(context.Background(), opts, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			found := false
			for _, tool := range session.ToolDefinitions() {
				if tool.Name == "ComputerUse" {
					found = true
				}
			}
			if found != test.want {
				t.Fatalf("ComputerUse=%v, want %v", found, test.want)
			}
		})
	}
}

type coordinatedRuntimeComputerBridge struct {
	runtimeComputerBridge
	ensureCalls int
}

func (b *coordinatedRuntimeComputerBridge) EnsureComputerSession(_ context.Context, _ cu.SessionOwner) (string, error) {
	b.ensureCalls++
	return b.sessionID, nil
}

func TestDesktopComputerUseColdStartUsesLazyCoordinator(t *testing.T) {
	bridge := &coordinatedRuntimeComputerBridge{runtimeComputerBridge: runtimeComputerBridge{sessionID: computerRuntimeHostSession}}
	opts := computerRuntimeOptions(bridge)
	guidance, cleanup := configureDesktopComputerUse(context.Background(), computerRuntimeConfig(), computerRuntimeModel, &opts)
	defer cleanup()
	if guidance == "" || !opts.computerUseProfile || !opts.computerUseImageSupported {
		t.Fatalf("cold-start computer use was not enabled: guidance=%q profile=%v image=%v", guidance, opts.computerUseProfile, opts.computerUseImageSupported)
	}
	if len(bridge.owners) != 0 {
		t.Fatalf("cold-start setup performed an existing-session lookup: %d", len(bridge.owners))
	}
	tracked, ok := opts.computerUseService.(*trackedDesktopComputerService)
	if !ok {
		t.Fatalf("service type=%T, want tracked desktop service", opts.computerUseService)
	}
	id, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner)
	if err != nil || id != computerRuntimeHostSession {
		t.Fatalf("ensure id=%q err=%v", id, err)
	}
	if bridge.ensureCalls != 1 {
		t.Fatalf("ensure calls=%d, want 1", bridge.ensureCalls)
	}
	if tracked.session() != computerRuntimeHostSession {
		t.Fatalf("tracked session=%q, want %q", tracked.session(), computerRuntimeHostSession)
	}
	cleanup()
}

func TestDesktopComputerGuidanceUsesAtomicLaunchForClosedTarget(t *testing.T) {
	for _, want := range []string{"first call", "action=launch_app", "same turn", "without session_id", "Never retry"} {
		if !strings.Contains(computerUseSystemGuidance, want) {
			t.Errorf("ComputerUse guidance missing %q", want)
		}
	}
	for _, obsolete := range []string{"On the first call, use action=observe", "use a separate launch_app only", "prefer the first observe"} {
		if strings.Contains(computerUseSystemGuidance, obsolete) {
			t.Errorf("ComputerUse guidance conflicts with atomic launch: %q", obsolete)
		}
	}
}
