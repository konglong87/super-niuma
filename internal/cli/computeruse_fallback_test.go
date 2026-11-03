package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

const (
	computerFallbackName  = "vision-backup"
	computerFallbackModel = "backup-vision-model"
	computerFallbackReply = "approved fallback received the observation"
)

func TestConstrainComputerUseFallbacks(t *testing.T) {
	named := config.ProviderConfig{Name: computerFallbackName, Type: "anthropic", BaseURL: "http://backup.invalid", APIKey: "test-only"}
	override := named
	override.Model = computerFallbackModel
	unnamed := named
	unnamed.Name = ""
	second := unnamed
	second.Name = "fallback-2"
	third := unnamed
	third.Name = "fallback-3"
	route := func(provider, model string) config.ComputerUseImageInputRoute {
		return config.ComputerUseImageInputRoute{Provider: provider, Model: model}
	}
	for _, test := range []struct {
		name       string
		disabled   bool
		noSettings bool
		fallbacks  []config.ProviderConfig
		routes     []config.ComputerUseImageInputRoute
		want       []config.ProviderConfig
	}{
		{name: "disabled preserves undeclared and unnamed fallbacks", disabled: true, fallbacks: []config.ProviderConfig{named, unnamed}, want: []config.ProviderConfig{named, unnamed}},
		{name: "disabled without settings", disabled: true, noSettings: true, fallbacks: []config.ProviderConfig{unnamed}, want: []config.ProviderConfig{unnamed}},
		{name: "no fallbacks"},
		{name: "nil computer settings fail closed", noSettings: true, fallbacks: []config.ProviderConfig{named}},
		{name: "undeclared route fails closed", fallbacks: []config.ProviderConfig{named}},
		{name: "primary declaration cannot authorize fallback", fallbacks: []config.ProviderConfig{unnamed}, routes: []config.ComputerUseImageInputRoute{route("primary", computerRuntimeModel)}},
		{name: "protocol is not provider identity", fallbacks: []config.ProviderConfig{named}, routes: []config.ComputerUseImageInputRoute{route(named.Type, computerRuntimeModel)}},
		{name: "wrong model fails closed", fallbacks: []config.ProviderConfig{named}, routes: []config.ComputerUseImageInputRoute{route(computerFallbackName, computerFallbackModel)}},
		{name: "provider match is case sensitive", fallbacks: []config.ProviderConfig{named}, routes: []config.ComputerUseImageInputRoute{route(strings.ToUpper(computerFallbackName), computerRuntimeModel)}},
		{name: "wildcard does not authorize route", fallbacks: []config.ProviderConfig{named}, routes: []config.ComputerUseImageInputRoute{route("*", "*")}},
		{name: "empty model inherits final query model", fallbacks: []config.ProviderConfig{named}, routes: []config.ComputerUseImageInputRoute{route(computerFallbackName, computerRuntimeModel)}, want: []config.ProviderConfig{named}},
		{name: "empty model does not inherit selected provider model", fallbacks: []config.ProviderConfig{named}, routes: []config.ComputerUseImageInputRoute{route(computerFallbackName, "selected-model")}},
		{name: "explicit model overrides final query model", fallbacks: []config.ProviderConfig{override}, routes: []config.ComputerUseImageInputRoute{route(computerFallbackName, computerFallbackModel)}, want: []config.ProviderConfig{override}},
		{name: "explicit model cannot use query model declaration", fallbacks: []config.ProviderConfig{override}, routes: []config.ComputerUseImageInputRoute{route(computerFallbackName, computerRuntimeModel)}},
		{name: "unnamed survivor retains original index", fallbacks: []config.ProviderConfig{named, unnamed}, routes: []config.ComputerUseImageInputRoute{route("fallback-2", computerRuntimeModel)}, want: []config.ProviderConfig{second}},
		{name: "filtered index cannot authorize undeclared endpoint", fallbacks: []config.ProviderConfig{named, unnamed}, routes: []config.ComputerUseImageInputRoute{route("fallback-1", computerRuntimeModel)}},
		{name: "mixed routes preserve order fields and original names", fallbacks: []config.ProviderConfig{unnamed, override, unnamed, named}, routes: []config.ComputerUseImageInputRoute{route(computerFallbackName, computerFallbackModel), route("fallback-3", computerRuntimeModel)}, want: []config.ProviderConfig{override, third}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := computerRuntimeConfig()
			cfg.APIKey, cfg.AuthToken, cfg.BaseURL = "primary-key", "primary-token", "http://primary.invalid"
			cfg.SelectedProvider, cfg.SelectedProviderModel = "selected-primary", "selected-model"
			cfg.Settings.Model = "settings-model"
			cfg.FallbackProviders = append([]config.ProviderConfig(nil), test.fallbacks...)
			// Deliberately share backing storage: filtering must not mutate either view.
			cfg.Settings.Fallback = &config.FallbackSettings{Providers: cfg.FallbackProviders}
			cfg.Settings.ComputerUse.ImageInputRoutes = test.routes
			if test.noSettings {
				cfg.Settings.ComputerUse = nil
			}
			before := computerFallbackConfigSnapshot(t, cfg)
			got := constrainComputerUseFallbacks(cfg, computerRuntimeModel, !test.disabled)
			if !bytes.Equal(before, computerFallbackConfigSnapshot(t, cfg)) {
				t.Fatal("filter mutated its input configuration, fallback backing array, or settings")
			}
			if test.disabled && !reflect.DeepEqual(got, cfg) {
				t.Fatal("disabled ComputerUse must return the configuration unchanged")
			}
			if len(got.FallbackProviders) != len(test.want) {
				t.Fatalf("fallbacks=%+v, want %+v", got.FallbackProviders, test.want)
			}
			for i, want := range test.want {
				if !reflect.DeepEqual(got.FallbackProviders[i], want) {
					t.Errorf("fallback[%d]=%+v, want %+v", i, got.FallbackProviders[i], want)
				}
			}
			rest := got
			rest.FallbackProviders = cfg.FallbackProviders
			if !reflect.DeepEqual(rest, cfg) {
				t.Fatal("filter changed primary or another non-fallback configuration field")
			}
			if again := constrainComputerUseFallbacks(got, computerRuntimeModel, !test.disabled); !reflect.DeepEqual(again, got) {
				t.Fatal("filter is not idempotent; retained fallback identity must remain stable")
			}
			if !test.disabled && len(got.FallbackProviders) > 0 {
				got.FallbackProviders[0].Name = "mutated-copy"
				if !bytes.Equal(before, computerFallbackConfigSnapshot(t, cfg)) {
					t.Fatal("retained fallback slice aliases the original configuration/settings")
				}
			}
		})
	}
}

func computerFallbackConfigSnapshot(t *testing.T, cfg config.Config) []byte {
	t.Helper()
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// Exercise production construction and HTTP routing, not a pre-filtered client.
// Both providers use the same protocol: only the operator's route assertion may
// authorize sending a captured desktop image to the fallback.
func TestNewQuerySessionComputerUseFallbackAfterImage(t *testing.T) {
	for _, test := range []struct {
		name     string
		declared bool
		model    string
		sse      bool
	}{
		{name: "undeclared fallback fails closed"},
		{name: "declared fallback inherits final model", declared: true},
		{name: "declared fallback uses explicit model", declared: true, model: computerFallbackModel},
		{name: "SSE error undeclared fallback fails closed", sse: true},
		{name: "SSE error declared fallback succeeds", declared: true, sse: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
			var screenshot bytes.Buffer
			if err := png.Encode(&screenshot, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
				t.Fatal(err)
			}
			bridge := &cleanupComputerBridge{runtimeComputerBridge: runtimeComputerBridge{
				sessionID: computerRuntimeHostSession, image: screenshot.Bytes(),
			}, approved: true}
			var mu sync.Mutex
			var primaryRequests, fallbackRequests [][]byte
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read primary request: %v", err)
					http.Error(w, "read error", http.StatusBadRequest)
					return
				}
				mu.Lock()
				primaryRequests = append(primaryRequests, body)
				first := len(primaryRequests) == 1
				mu.Unlock()
				if first {
					writeComputerObserveStream(w)
					return
				}
				// A normal invalid_request_error never falls back, so it cannot
				// distinguish a working route guard from the unsafe old behavior.
				// overloaded_error permits fallback but HTTP 400 avoids SDK retries.
				const providerError = `{"type":"error","error":{"type":"overloaded_error","message":"primary rejected image request"}}`
				if test.sse {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: error\ndata: "+providerError+"\n\n")
					return
				}
				http.Error(w, providerError, http.StatusBadRequest)
			}))
			defer primary.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read fallback request: %v", err)
					http.Error(w, "read error", http.StatusBadRequest)
					return
				}
				mu.Lock()
				fallbackRequests = append(fallbackRequests, body)
				mu.Unlock()
				writeAnthropicTextStream(t, w, computerFallbackReply)
			}))
			defer fallback.Close()

			cfg := computerRuntimeConfig()
			cfg.Settings.Provider, cfg.Settings.BaseURL, cfg.Settings.APIKey = cfg.Provider, primary.URL, "test-only"
			cfg.Settings.Fallback = &config.FallbackSettings{Providers: []config.ProviderConfig{{
				Name: computerFallbackName, Type: cfg.Provider, BaseURL: fallback.URL, APIKey: "test-only", Model: test.model,
			}}}
			effectiveModel := test.model
			if effectiveModel == "" {
				effectiveModel = computerRuntimeModel
			}
			if test.declared {
				cfg.Settings.ComputerUse.ImageInputRoutes = append(cfg.Settings.ComputerUse.ImageInputRoutes,
					config.ComputerUseImageInputRoute{Provider: computerFallbackName, Model: effectiveModel})
			}
			if err := config.SaveGlobalSettings(cfg.Settings); err != nil {
				t.Fatal(err)
			}
			opts := computerRuntimeOptions(bridge)
			opts.cwd, opts.model, opts.maxTurns, opts.noPersistence = t.TempDir(), computerRuntimeModel, 3, true
			opts.toolsSpecified, opts.enabledTools, opts.permissionMode = true, []string{"ComputerUse"}, "allow"
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			session, cleanup, err := newQuerySession(ctx, opts, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if tools := session.ToolDefinitions(); len(tools) != 1 || tools[0].Name != "ComputerUse" {
				t.Fatalf("ComputerUse must remain enabled despite undeclared fallback: %+v", tools)
			}
			var output bytes.Buffer
			runErr := session.RunText(ctx, "Observe the approved desktop.", &output)
			cleanup()
			cleanup() // The fail-safe must be idempotent after success and failure.
			if bridge.stopCalls != 1 || bridge.stoppedOwner != computerRuntimeOwner || bridge.stoppedSession != computerRuntimeHostSession || bridge.approved {
				t.Errorf("cleanup did not revoke the bound session exactly once: calls=%d owner=%+v session=%q approved=%v", bridge.stopCalls, bridge.stoppedOwner, bridge.stoppedSession, bridge.approved)
			}
			if len(bridge.observed) != 1 || len(bridge.imageOwners) != 1 || bridge.imageObservation != computerRuntimeObservation {
				t.Fatalf("observe/image bridge was not used: observations=%v images=%v observationID=%q", bridge.observed, bridge.imageOwners, bridge.imageObservation)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(primaryRequests) != 2 {
				t.Fatalf("primary requests=%d, want observe then image (2); RunText error=%v", len(primaryRequests), runErr)
			}
			assertComputerFallbackImageRequest(t, primaryRequests[1], computerRuntimeModel, screenshot.Bytes())
			if !test.declared {
				if len(fallbackRequests) != 0 {
					t.Errorf("undeclared fallback received %d requests; desktop image must not leave the approved route", len(fallbackRequests))
				}
				if runErr == nil {
					t.Error("RunText succeeded after primary failure with no image-capable fallback")
				} else if !strings.Contains(runErr.Error(), "primary rejected image request") {
					t.Errorf("RunText did not preserve the primary failure: %v", runErr)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("declared fallback should succeed: %v", runErr)
			}
			if len(fallbackRequests) != 1 {
				t.Fatalf("declared fallback requests=%d, want 1", len(fallbackRequests))
			}
			assertComputerFallbackImageRequest(t, fallbackRequests[0], effectiveModel, screenshot.Bytes())
			if !strings.Contains(output.String(), computerFallbackReply) {
				t.Errorf("fallback completion missing from output: %q", output.String())
			}
		})
	}
}

func assertComputerFallbackImageRequest(t *testing.T, body []byte, model string, screenshot []byte) {
	t.Helper()
	var request struct {
		Model    string           `json:"model"`
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Model != model {
		t.Errorf("request model=%q, want %q", request.Model, model)
	}
	encoded := base64.StdEncoding.EncodeToString(screenshot)
	var hasImage func(any) bool
	hasImage = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			if v["type"] == "image" {
				source, ok := v["source"].(map[string]any)
				if ok && source["type"] == "base64" && source["media_type"] == "image/png" && source["data"] == encoded {
					return true
				}
			}
			for _, child := range v {
				if hasImage(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if hasImage(child) {
					return true
				}
			}
		}
		return false
	}
	for _, message := range request.Messages {
		if message["role"] == "user" && hasImage(message["content"]) {
			return
		}
	}
	t.Error("request has no user image block containing the actual observed PNG")
}
