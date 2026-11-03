package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExplicitConfigPreflightAndRoutePin(t *testing.T) {
	for _, tc := range []struct {
		name            string
		modify          func(*config.Config)
		provider, model string
		want            error
	}{
		{"valid", func(*config.Config) {}, "fixture", "fixture-model", nil},
		{"missing_key", func(c *config.Config) { c.APIKey = "" }, "fixture", "fixture-model", ErrUnavailable},
		{"no_model", func(*config.Config) {}, "fixture", "", ErrUnavailable},
		{"unsupported_protocol", func(c *config.Config) { c.ProviderProtocol = "unknown" }, "fixture", "fixture-model", ErrUnavailable},
		{"remote_plain_http", func(c *config.Config) { c.BaseURL = "http://model.invalid/v1" }, "fixture", "fixture-model", ErrUnavailable},
		{"explicit_local_http", func(c *config.Config) { c.BaseURL = "http://127.0.0.1:12345/v1" }, "fixture", "fixture-model", nil},
		{"url_credentials", func(c *config.Config) { c.BaseURL = "https://secret:secret@model.invalid" }, "fixture", "fixture-model", ErrUnavailable},
		{"url_query", func(c *config.Config) { c.BaseURL = "https://model.invalid?key=secret" }, "fixture", "fixture-model", ErrUnavailable},
		{"header_injection", func(c *config.Config) { c.APIKey = "key\r\nInjected:x" }, "fixture", "fixture-model", ErrUnavailable},
		{"route_mismatch", func(*config.Config) {}, "not-bound", "fixture-model", ErrUnavailable},
		{"model_mismatch", func(c *config.Config) { c.SelectedProviderModel = "pinned" }, "fixture", "fixture-model", ErrUnavailable},
		{"configuration_error", func(c *config.Config) { c.ConfigurationError = "secret" }, "fixture", "fixture-model", ErrUnavailable},
		{"responses_store", func(c *config.Config) {
			yes := true
			c.Provider = "openai"
			c.ProviderProtocol = config.ProviderProtocolOpenAIResponses
			c.Responses = &config.ResponsesProviderSettings{Store: &yes}
		}, "fixture", "fixture-model", ErrUnavailable},
		{"responses_stateful", func(c *config.Config) {
			c.Provider = "openai"
			c.ProviderProtocol = config.ProviderProtocolOpenAIResponses
			c.Responses = &config.ResponsesProviderSettings{StateMode: config.ResponsesStateModePreviousResponseID}
		}, "fixture", "fixture-model", ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fixtureConfig()
			tc.modify(&cfg)
			r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
			if err := r.Preflight(context.Background(), tc.provider, tc.model); err != tc.want {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
	r := New(func(context.Context) (config.Config, error) {
		return config.Config{}, errors.New("secret resolver failure")
	}, nil)
	if err := r.Preflight(context.Background(), "", "model"); err != ErrUnavailable {
		t.Fatal(err)
	}
	var nilRunner *Runner
	if err := nilRunner.Preflight(context.Background(), "", "model"); err != ErrUnavailable {
		t.Fatal(err)
	}
}

func TestNamedRouteSelectionDoesNotFallback(t *testing.T) {
	cfg := fixtureConfig()
	cfg.FallbackProviders = []config.ProviderConfig{{Name: "chosen", Type: "openai", BaseURL: "https://chosen.invalid/v1", APIKey: "chosen-key", Model: "chosen-model"}, {Name: "other", Type: "anthropic", BaseURL: "https://other.invalid", APIKey: "other-key"}}
	r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
	got, err := r.configuration(context.Background(), "chosen", "chosen-model")
	if err != nil || got.SelectedProvider != "chosen" || got.APIKey != "chosen-key" || len(got.FallbackProviders) != 0 {
		t.Fatal("route not pinned", err)
	}
	if len(cfg.FallbackProviders) != 2 {
		t.Fatal("resolver snapshot mutated")
	}
	if _, err := r.configuration(context.Background(), "chosen", "other-model"); err != ErrUnavailable {
		t.Fatal(err)
	}
}

func TestSingleHTTPPostNoRedirectRetryOrFallback(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusTemporaryRedirect, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls, redirects atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1); w.WriteHeader(200) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", target.URL)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				io.WriteString(w, "secret provider error body")
			}))
			defer server.Close()
			cfg := fixtureConfig()
			cfg.BaseURL = server.URL
			cfg.FallbackProviders = []config.ProviderConfig{{Name: "other", Type: "anthropic", BaseURL: target.URL, APIKey: "secret"}}
			req := fixtureRequest(t)
			r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
			got, err := r.Execute(context.Background(), req)
			if err != ErrFailed || got.Text != "" || calls.Load() != 1 || redirects.Load() != 0 {
				t.Fatalf("result=%+v err=%v requests=%d redirects=%d", got, err, calls.Load(), redirects.Load())
			}
			b := req.Budget.(*fakeBudget)
			if len(b.reserved) != 1 || len(b.settled) != 1 || b.settled[0].known {
				t.Fatal("failed request refunded/retried")
			}
		})
	}
}

func TestTransportNonReplayableAndNoEnvironmentDefaults(t *testing.T) {
	cfg := fixtureConfig()
	created, err := newHTTPStreamer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client := created.(*httpStreamer)
	transport := client.client.Transport.(*http.Transport)
	if transport.Proxy != nil || !transport.DisableKeepAlives || transport.ForceAttemptHTTP2 || transport.TLSNextProto == nil {
		t.Fatal("implicit proxy/retry path enabled")
	}
	calls := 0
	client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil || r.ProtoMajor != 1 || r.Header.Get("x-api-key") != cfg.APIKey || r.Header.Get("Accept") != "application/json" {
			t.Fatal("unexpected transport policy")
		}
		return nil, io.EOF
	})
	_, err = client.StreamMessages(context.Background(), anthropic.MessagesRequest{Model: "model", MaxTokens: 20, Messages: []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hi"}}}}}, anthropic.StreamCallbacks{})
	if err != ErrFailed || calls != 1 {
		t.Fatal("transport error retried", err, calls)
	}
}

func TestHTTPProtocolsThroughRealQueryWriteLoop(t *testing.T) {
	for _, protocol := range []config.ProviderProtocol{config.ProviderProtocolAnthropicMessages, config.ProviderProtocolOpenAIChatCompletions, config.ProviderProtocolOpenAIResponses} {
		t.Run(string(protocol), func(t *testing.T) {
			var calls atomic.Int32
			var bodies [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				bodies = append(bodies, body)
				var parsed map[string]any
				if err := json.Unmarshal(body, &parsed); err != nil {
					t.Error(err)
				}
				if parsed["stream"] != false {
					t.Error("implicit streaming")
				}
				w.Header().Set("Content-Type", "application/json")
				switch protocol {
				case config.ProviderProtocolAnthropicMessages:
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "secret-api-key" {
						t.Error("bad anthropic request")
					}
					if n == 1 {
						io.WriteString(w, `{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"super_write","input":{"path":"artifacts/real.txt","content":"actual tool loop"}}],"stop_reason":"tool_use","usage":{"input_tokens":25,"output_tokens":10}}`)
					} else {
						io.WriteString(w, `{"role":"assistant","content":[{"type":"text","text":"Artifact ready."}],"stop_reason":"end_turn","usage":{"input_tokens":30,"output_tokens":5}}`)
					}
				case config.ProviderProtocolOpenAIChatCompletions:
					if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret-api-key" || parsed["max_completion_tokens"] != float64(200) {
						t.Error("bad chat request")
					}
					if n == 1 {
						io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"super_write","arguments":"{\"path\":\"artifacts/real.txt\",\"content\":\"actual tool loop\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":25,"completion_tokens":10}}`)
					} else {
						io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Artifact ready."},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":5}}`)
					}
				case config.ProviderProtocolOpenAIResponses:
					if r.URL.Path != "/v1/responses" || parsed["store"] != false || parsed["previous_response_id"] != nil {
						t.Error("responses stateful call")
					}
					if n == 1 {
						io.WriteString(w, `{"status":"completed","output":[{"type":"function_call","call_id":"call-1","name":"super_write","arguments":"{\"path\":\"artifacts/real.txt\",\"content\":\"actual tool loop\"}"}],"usage":{"input_tokens":25,"output_tokens":10}}`)
					} else {
						io.WriteString(w, `{"status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Artifact ready."}]}],"usage":{"input_tokens":30,"output_tokens":5}}`)
					}
				}
			}))
			defer server.Close()
			cfg := fixtureConfig()
			cfg.ProviderProtocol = protocol
			cfg.BaseURL = server.URL + "/v1"
			if protocol != config.ProviderProtocolAnthropicMessages {
				cfg.Provider = "openai"
			}
			req := fixtureRequest(t)
			r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
			got, err := r.Execute(context.Background(), req)
			if err != nil || got.Text != "Artifact ready." || got.ToolCalls != 1 || calls.Load() != 2 || len(got.Writes) != 1 || got.Usage != (Usage{55, 15}) {
				t.Fatalf("result=%+v err=%v calls=%d", got, err, calls.Load())
			}
			data, err := os.ReadFile(filepath.Join(req.Workspace, "artifacts/real.txt"))
			if err != nil || string(data) != "actual tool loop" {
				t.Fatal("missing real tool write")
			}
			b := req.Budget.(*fakeBudget)
			if len(b.reserved) != 2 {
				t.Fatal("unreserved request")
			}
			for i, body := range bodies {
				if b.reserved[i].input < len(body)+framingReserve {
					t.Fatal("wire-byte reservation undercount")
				}
			}
		})
	}
}

func TestMissingPartialAndZeroUsageKeepReservation(t *testing.T) {
	for _, usage := range []string{`{}`, `{"input_tokens":15}`, `{"output_tokens":5}`, `{"input_tokens":0,"output_tokens":0}`} {
		t.Run(usage, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"role":"assistant","content":[{"type":"text","text":"Done."}],"stop_reason":"end_turn","usage":%s}`, usage)
			}))
			defer server.Close()
			cfg := fixtureConfig()
			cfg.BaseURL = server.URL
			req := fixtureRequest(t)
			r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
			got, err := r.Execute(context.Background(), req)
			b := req.Budget.(*fakeBudget)
			if err != nil || got.UsageKnown || len(b.settled) != 1 || b.settled[0].known {
				t.Fatalf("got=%+v err=%v settle=%+v", got, err, b.settled)
			}
		})
	}
}

func TestProtocolRejectsTruncationServerToolsAndMalformedMessages(t *testing.T) {
	cases := []struct {
		protocol config.ProviderProtocol
		body     string
	}{
		{config.ProviderProtocolAnthropicMessages, `{"role":"assistant","content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens"}`},
		{config.ProviderProtocolAnthropicMessages, `{"role":"assistant","content":[{"type":"server_tool_use","id":"id","name":"web_search"}],"stop_reason":"tool_use"}`},
		{config.ProviderProtocolOpenAIChatCompletions, `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}]}`},
		{config.ProviderProtocolOpenAIResponses, `{"status":"incomplete","output":[]}`},
		{config.ProviderProtocolOpenAIResponses, `{"status":"completed","output":[{"type":"web_search_call"}]}`},
		{config.ProviderProtocolOpenAIResponses, `{"status":"completed","output":[{"type":"reasoning"}]}`},
		{config.ProviderProtocolAnthropicMessages, `{"role":"assistant","content":[{"type":"tool_use","id":"same","name":"super_write","input":{}},{"type":"tool_use","id":"same","name":"super_write","input":{}}],"stop_reason":"tool_use"}`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			out, err := decodeResponse(tc.protocol, []byte(tc.body))
			if err == nil {
				err = validateResponse(out)
			}
			if err == nil {
				t.Fatal("unsupported response accepted")
			}
		})
	}
	req := anthropic.MessagesRequest{Model: "model", MaxTokens: 1, Tools: []anthropic.ToolDefinition{{Name: "Agent"}}}
	if _, _, err := encodeRequest(config.ProviderProtocolAnthropicMessages, req); err == nil {
		t.Fatal("implicit tool accepted")
	}
	req.Tools = nil
	req.Messages = []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "image", Source: &anthropic.ContentSource{URL: "https://evil.invalid"}}}}}
	if _, _, err := encodeRequest(config.ProviderProtocolOpenAIChatCompletions, req); err == nil {
		t.Fatal("remote image accepted")
	}
}

func TestHTTPResponseByteLimitAndRawErrorPrivacy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
	}))
	defer server.Close()
	cfg := fixtureConfig()
	cfg.BaseURL = server.URL
	req := fixtureRequest(t)
	r := New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
	got, err := r.Execute(context.Background(), req)
	if err != ErrFailed || got.Text != "" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestAuthorityIsRecheckedAtHTTPBoundary(t *testing.T) {
	cfg := fixtureConfig()
	client, err := newHTTPStreamer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := client.(*httpStreamer)
	calls := 0
	httpClient.authorize = func(context.Context) error { return ErrAuthority }
	httpClient.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not send")
	})
	_, err = httpClient.StreamMessages(context.Background(), anthropic.MessagesRequest{
		Model: "model", MaxTokens: 10, Messages: []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "protected"}}}},
	}, anthropic.StreamCallbacks{})
	if err != ErrAuthority || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
