package runtimecompose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/promptdump"
)

func anthropicSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	lines := []string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_offline","type":"message","role":"assistant","model":"offline-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}}`, ``,
		`event: content_block_start`, `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`, ``,
		`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"INTERNAL_THINKING_DO_NOT_PERSIST"}}`, ``,
		`event: content_block_delta`, `data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"INTERNAL_SIGNATURE_DO_NOT_PERSIST"}}`, ``,
		`event: content_block_stop`, `data: {"type":"content_block_stop","index":0}`, ``,
		`event: content_block_start`, `data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`, ``,
		`event: content_block_delta`, fmt.Sprintf(`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":%q}}`, text), ``,
		`event: content_block_stop`, `data: {"type":"content_block_stop","index":1}`, ``,
		`event: message_delta`, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":7}}`, ``,
		`event: message_stop`, `data: {"type":"message_stop"}`, ``,
	}
	fmt.Fprintln(w, strings.Join(lines, "\n"))
}
func chatSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, `data: {"id":"chat_offline","object":"chat.completion.chunk","created":1,"model":"offline-model","choices":[{"index":0,"delta":{"role":"assistant"}}]}`+"\n")
	fmt.Fprintf(w, "data: {\"id\":\"chat_offline\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"offline-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", text)
	fmt.Fprint(w, `data: {"id":"chat_offline","object":"chat.completion.chunk","created":1,"model":"offline-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
	fmt.Fprint(w, `data: {"id":"chat_offline","object":"chat.completion.chunk","created":1,"model":"offline-model","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":3}}}`+"\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}
func responsesSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":%q}\n\n", text)
	fmt.Fprint(w, `data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp_1","status":"completed","model":"offline-model","output":[],"usage":{"input_tokens":11,"input_tokens_details":{"cached_tokens":3},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":18}}}`+"\n\n")
}

func TestRealClientOfflineProtocolsAndActualUsage(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	for _, tt := range []struct {
		name, kind, path string
		protocol         config.ProviderProtocol
		write            func(http.ResponseWriter, string)
		usage            workflow.Usage
	}{
		{"anthropic", "anthropic", "/v1/messages", config.ProviderProtocolAnthropicMessages, anthropicSSE, workflow.Usage{InputTokens: 11, OutputTokens: 7, CacheReadTokens: 3, CacheWriteTokens: 2}},
		{"chat", "openai", "/v1/chat/completions", config.ProviderProtocolOpenAIChatCompletions, chatSSE, workflow.Usage{InputTokens: 8, OutputTokens: 7, CacheReadTokens: 3}},
		{"responses", "openai", "/v1/responses", config.ProviderProtocolOpenAIResponses, responsesSSE, workflow.Usage{InputTokens: 8, OutputTokens: 7, CacheReadTokens: 3}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Provider = tt.kind
			cfg.ProviderProtocol = tt.protocol
			cfg.SelectedProvider = "offline-service"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != tt.path {
					t.Errorf("path=%s want=%s", r.URL.Path, tt.path)
				}
				auth := r.Header.Get("Authorization")
				if tt.kind == "anthropic" {
					auth = r.Header.Get("x-api-key")
				}
				if !strings.Contains(auth, cfg.APIKey) {
					t.Error("configured authentication missing")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request["model"] != "offline-model" {
					t.Errorf("model=%v", request["model"])
				}
				if tools, ok := request["tools"].([]any); ok && len(tools) != 0 {
					t.Errorf("tools sent=%v", tools)
				}
				tt.write(w, "offline answer "+cfg.APIKey)
			}))
			defer server.Close()
			cfg.BaseURL = server.URL + "/v1"
			// Anthropic SDK adds /v1 itself when base URL already ends in /v1.
			if tt.kind == "anthropic" {
				cfg.BaseURL = server.URL
			}
			e := NewTextExecutor(resolved(cfg))
			var events []workflow.Event
			got, err := e.Execute(context.Background(), testInput(), func(event workflow.Event) { events = append(events, event) })
			if err != nil {
				t.Fatalf("real offline client result=%+v err=%v", got, err)
			}
			if got.Status != workflow.Completed || !got.UsageKnown || got.Provider != "offline-service" || got.Model != "offline-model" || got.Usage != tt.usage || calls.Load() != 1 {
				t.Fatalf("actual result=%+v calls=%d", got, calls.Load())
			}
			raw, _ := json.Marshal(struct {
				Result workflow.Result
				Events []workflow.Event
			}{got, events})
			for _, s := range []string{cfg.APIKey, "INTERNAL_THINKING_DO_NOT_PERSIST", "INTERNAL_SIGNATURE_DO_NOT_PERSIST"} {
				if strings.Contains(string(raw), s) {
					t.Fatalf("leaked=%s", s)
				}
			}
		})
	}
}

func TestRealClientOfflineFallbackReportsActualRouteAndModel(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"temporarily overloaded"}}`)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "fallback-model" {
			t.Errorf("fallback model=%v", body["model"])
		}
		anthropicSSE(w, "fallback success fallback-real-private-key")
	}))
	defer fallback.Close()
	cfg := testConfig()
	cfg.BaseURL = primary.URL
	cfg.SelectedProvider = "primary-service"
	cfg.FallbackProviders = []config.ProviderConfig{{Name: "fallback-service", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-real-private-key", Model: "fallback-model"}}
	got, err := NewTextExecutor(resolved(cfg)).Execute(context.Background(), testInput(), nil)
	if err != nil || got.Status != workflow.Completed || got.Provider != "fallback-service" || got.Model != "fallback-model" || strings.Contains(got.Output, "fallback-real-private-key") || primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatalf("result=%+v err=%v calls=%d/%d", got, err, primaryCalls.Load(), fallbackCalls.Load())
	}
}

func TestRealClientCallerCancelDoesNotFallback(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	started := make(chan struct{})
	release := make(chan struct{})
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer primary.Close()
	defer close(release)
	var fallbackCalls atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fallbackCalls.Add(1); anthropicSSE(w, "must not run") }))
	defer fallback.Close()
	cfg := testConfig()
	cfg.BaseURL = primary.URL
	cfg.FallbackProviders = []config.ProviderConfig{{Name: "fallback-service", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-key"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := NewTextExecutor(resolved(cfg)).Execute(ctx, testInput(), nil); done <- err }()
	select {
	case <-started:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("loopback request never started")
	}
	select {
	case err := <-done:
		requireCode(t, err, CodeCancelled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not finish promptly")
	}
	if fallbackCalls.Load() != 0 {
		t.Fatalf("fallback called after cancellation: %d", fallbackCalls.Load())
	}
}
