package runtimecompose

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/promptdump"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	providerErrorSentinel = "provider-error-body-must-not-be-logged"
	loggingTestTimeout    = 10 * time.Second
	normalLogMessage      = "unrelated request is still logging"
)

func TestRealClientRetryAndFallbackErrorsNeverReachGlobalLogger(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	for _, mode := range []string{"retry", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			core, capture := observer.New(zapcore.DebugLevel)
			logger := zap.New(core)
			restore := observability.SetDefaultZapLogger(logger)
			defer restore()
			cfg := testConfig()
			if mode == "retry" {
				cfg.Provider = "openai"
				cfg.ProviderProtocol = config.ProviderProtocolOpenAIChatCompletions
			}
			var primaryCalls, fallbackCalls atomic.Int32
			firstRequest := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := primaryCalls.Add(1)
				if mode == "retry" && call > 1 {
					chatSSE(w, "safe retry result")
					return
				}
				if call == 1 {
					close(firstRequest)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				status, errorType := http.StatusServiceUnavailable, "server_error"
				if mode == "fallback" {
					status, errorType = http.StatusBadRequest, "overloaded_error"
				}
				w.WriteHeader(status)
				// Include both configured credentials and an unknown body sentinel. The
				// original Client logger sees this error before our guardedStreamer does.
				_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]string{"type": errorType, "message": providerErrorSentinel + " " + cfg.APIKey + " " + cfg.AuthToken}})
			}))
			defer func() { unblock(); primary.Close() }()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fallbackCalls.Add(1)
				anthropicSSE(w, "safe fallback result")
			}))
			defer fallback.Close()
			cfg.BaseURL = primary.URL
			if mode == "retry" {
				cfg.BaseURL += "/v1"
			} else {
				cfg.FallbackProviders = []config.ProviderConfig{{Name: "offline-fallback", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-test-credential", Model: "fallback-model"}}
			}
			parent, cancel := context.WithTimeout(context.Background(), loggingTestTimeout)
			defer cancel()
			type execution struct {
				result workflow.Result
				err    error
			}
			done := make(chan execution, 1)
			observability.Info(parent, nil, "normal", "before", normalLogMessage)
			go func() {
				result, err := NewTextExecutor(resolved(cfg)).Execute(parent, testInput(), nil)
				done <- execution{result, err}
			}()
			select {
			case <-firstRequest:
				observability.Info(parent, nil, "normal", "concurrent", normalLogMessage)
				unblock()
			case <-parent.Done():
				t.Fatal("loopback provider was not reached")
			}
			var got execution
			select {
			case got = <-done:
			case <-parent.Done():
				t.Fatal("offline execution did not finish")
			}
			if got.err != nil || got.result.Status != workflow.Completed {
				t.Fatalf("result=%+v err=%v", got.result, got.err)
			}
			if mode == "retry" && (primaryCalls.Load() != 2 || fallbackCalls.Load() != 0) {
				t.Fatalf("retry path was not exercised: primary=%d fallback=%d", primaryCalls.Load(), fallbackCalls.Load())
			}
			if mode == "fallback" && (primaryCalls.Load() != 1 || fallbackCalls.Load() != 1) {
				t.Fatalf("fallback path was not exercised: primary=%d fallback=%d", primaryCalls.Load(), fallbackCalls.Load())
			}
			observability.Info(parent, nil, "normal", "after", normalLogMessage)
			if observability.DefaultZapLogger() != logger {
				t.Fatal("workflow replaced the process-wide logger")
			}
			for _, entry := range capture.All() {
				raw, _ := json.Marshal(struct {
					Message string
					Fields  map[string]any
				}{entry.Message, entry.ContextMap()})
				for _, secret := range []string{cfg.APIKey, cfg.AuthToken, providerErrorSentinel} {
					if strings.Contains(string(raw), secret) {
						t.Fatalf("provider credential or body reached global logger: %s", secret)
					}
				}
			}
			if capture.Len() != 3 || capture.FilterMessage(normalLogMessage).Len() != 3 {
				t.Fatalf("workflow logging leaked or another context was silenced: %+v", capture.All())
			}
		})
	}
}

func TestTextExecutorAlsoSuppressesExplicitSlogInStreamer(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e := fakeExecutor(testConfig(), func(ctx context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
		observability.Error(ctx, logger, "provider", "stream", providerErrorSentinel, "error", fmt.Errorf("credential %s", testConfig().APIKey))
		return response("safe result"), nil
	})
	if _, err := e.Execute(context.Background(), testInput(), nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("workflow reached explicit slog logger: %s", out.String())
	}
	observability.Info(context.Background(), logger, "normal", "after", normalLogMessage)
	if !strings.Contains(out.String(), normalLogMessage) {
		t.Fatal("workflow muted an unrelated explicit slog logger")
	}
}
