package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func TestSystemBlocksJoinForOpenAIAndCacheForAnthropic(t *testing.T) {
	req := MessagesRequest{
		Model:     "model",
		MaxTokens: 100,
		System:    "fallback",
		SystemBlocks: []SystemBlock{
			{Text: "static", Source: "static_prompt", CacheControl: &CacheControl{Type: "ephemeral", TTL: "1h", Scope: "global"}},
			{Text: "dynamic", Source: "dynamic_prompt", CacheControl: &CacheControl{Type: "ephemeral", TTL: "5m"}},
		},
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}
	encoded, err := json.Marshal(req.SystemBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "static_prompt") || strings.Contains(string(encoded), "dynamic_prompt") {
		t.Fatalf("diagnostic source leaked into JSON: %s", encoded)
	}
	openAIReq, err := openAIChatCompletionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(openAIReq.Messages) == 0 || openAIReq.Messages[0].Content != "static\n\ndynamic" {
		t.Fatalf("openai system = %+v", openAIReq.Messages)
	}
	params, err := sdkMessageParams(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(params.System) != 2 {
		t.Fatalf("system blocks = %+v", params.System)
	}
	if params.System[0].CacheControl.Type != "ephemeral" || params.System[0].CacheControl.TTL != "1h" {
		t.Fatalf("static cache = %+v", params.System[0].CacheControl)
	}
	data, err := json.Marshal(params.System[0].CacheControl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"scope":"global"`) {
		t.Fatalf("static cache missing scope: %s", data)
	}
	if params.System[1].CacheControl.Type != "ephemeral" || params.System[1].CacheControl.TTL != "5m" {
		t.Fatalf("dynamic cache = %+v", params.System[1].CacheControl)
	}
}

func TestOpenAIChatCompletionRequestMapsResponseFormatJSONSchema(t *testing.T) {
	req := MessagesRequest{
		Model:     "model",
		MaxTokens: 100,
		Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
		ResponseFormat: &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &ResponseFormatSchema{
				Name:   "teach_decision_v1",
				Strict: true,
				Schema: json.RawMessage(`{"type":"object","properties":{"skill_version":{"type":"integer"}}}`),
			},
		},
	}
	openAIReq, err := openAIChatCompletionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if openAIReq.ResponseFormat == nil || openAIReq.ResponseFormat.Type != openai.ChatCompletionResponseFormatTypeJSONSchema {
		t.Fatalf("response format = %+v", openAIReq.ResponseFormat)
	}
	if openAIReq.ResponseFormat.JSONSchema == nil || openAIReq.ResponseFormat.JSONSchema.Name != "teach_decision_v1" || !openAIReq.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("json schema = %+v", openAIReq.ResponseFormat.JSONSchema)
	}
	data, err := openAIReq.ResponseFormat.JSONSchema.Schema.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"skill_version"`) || !strings.Contains(string(data), `"integer"`) {
		t.Fatalf("schema = %s", data)
	}
}

func TestMessageContentCacheControlMapsToAnthropicSDK(t *testing.T) {
	req := MessagesRequest{
		Model:     "model",
		MaxTokens: 100,
		Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi", CacheControl: &CacheControl{Type: "ephemeral", TTL: "1h"}}}}},
	}
	params, err := sdkMessageParams(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(params.Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"cache_control"`) || !strings.Contains(string(data), `"ttl":"1h"`) {
		t.Fatalf("message cache control missing: %s", data)
	}
}

func TestThinkingConfigMapsToAnthropicSDK(t *testing.T) {
	req := MessagesRequest{
		Model:     "model",
		MaxTokens: 5000,
		Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
		Thinking:  ThinkingConfigFromEffort("high", 5000),
	}
	params, err := sdkMessageParams(req)
	if err != nil {
		t.Fatal(err)
	}
	if params.Thinking.OfEnabled == nil || params.Thinking.OfEnabled.BudgetTokens != 4096 {
		t.Fatalf("thinking = %+v", params.Thinking)
	}
}

func TestImageContentMapsToOpenAIAndAnthropicSDK(t *testing.T) {
	req := MessagesRequest{
		Model:     "model",
		MaxTokens: 100,
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "describe"},
			{Type: "image", Source: &ContentSource{Type: "base64", MediaType: "image/png", Data: "cG5n"}},
		}}},
	}
	openAIReq, err := openAIChatCompletionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(openAIReq.Messages) != 2 {
		t.Fatalf("openai messages = %+v", openAIReq.Messages)
	}
	imageMsg := openAIReq.Messages[1]
	if len(imageMsg.MultiContent) != 1 || imageMsg.MultiContent[0].ImageURL == nil || !strings.HasPrefix(imageMsg.MultiContent[0].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("openai image message = %+v", imageMsg)
	}
	params, err := sdkMessageParams(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(params.Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"image"`) || !strings.Contains(string(data), `"media_type":"image/png"`) || !strings.Contains(string(data), `"data":"cG5n"`) {
		t.Fatalf("anthropic image message = %s", data)
	}
}

func TestStreamMessagesFallsBackToNextProvider(t *testing.T) {
	primaryAttempts := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts++
		http.Error(w, "primary unavailable", http.StatusServiceUnavailable)
	}))
	defer primary.Close()

	var fallbackModel string
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		fallbackModel = body.Model
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		APIKey:  "primary-key",
		BaseURL: primary.URL,
		FallbackProviders: []config.ProviderConfig{{
			Name:    "provider1",
			Type:    "anthropic",
			BaseURL: fallback.URL,
			APIKey:  "fallback-key",
			Model:   "fallback-model",
		}},
	})
	var text string
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "primary-model",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if primaryAttempts == 0 {
		t.Fatal("primary provider was not attempted")
	}
	if text != "fallback-ok" || len(res.Message.Content) != 1 {
		t.Fatalf("text=%q res=%+v", text, res)
	}
	if fallbackModel != "fallback-model" {
		t.Fatalf("fallback model = %q, want fallback-model", fallbackModel)
	}
}

func TestStreamMessagesUsesOpenAICompatibleProvider(t *testing.T) {
	var requestedPath string
	var requestedAuth string
	var requestedBody struct {
		Model               string `json:"model"`
		MaxCompletionTokens int    `json:"max_completion_tokens"`
		Messages            []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		requestedAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&requestedBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeOpenAITextStream(w, "openai-ok")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "custom",
		APIKey:   "openai-key",
		BaseURL:  server.URL + "/v1",
	})
	var text string
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "gpt-test",
		MaxTokens: 123,
		System:    "system prompt",
		Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if requestedPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", requestedPath)
	}
	if requestedAuth != "Bearer openai-key" {
		t.Fatalf("Authorization = %q", requestedAuth)
	}
	if requestedBody.Model != "gpt-test" || requestedBody.MaxCompletionTokens != 123 || !requestedBody.Stream || !requestedBody.StreamOptions.IncludeUsage {
		t.Fatalf("request body = %+v", requestedBody)
	}
	if len(requestedBody.Messages) != 2 || requestedBody.Messages[0].Role != "system" || requestedBody.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v", requestedBody.Messages)
	}
	if text != "openai-ok" || len(res.Message.Content) != 1 || res.Message.Content[0].Text != "openai-ok" {
		t.Fatalf("text=%q res=%+v", text, res)
	}
	if res.StopReason != "end_turn" || res.Usage.InputTokens != 2 || res.Usage.OutputTokens != 3 || res.Usage.CacheReadInputTokens != 1 {
		t.Fatalf("stop/usage = %q %+v", res.StopReason, res.Usage)
	}
	if !res.Usage.InputTokensIncludeCacheRead {
		t.Fatalf("openai-compatible prompt_tokens should retain provider cache-inclusive semantics: %+v", res.Usage)
	}
}

func TestStreamMessagesDefaultsMaxTokensAboveThinkingBudget(t *testing.T) {
	t.Run("anthropic native", func(t *testing.T) {
		var requestedBody struct {
			MaxTokens int `json:"max_tokens"`
			Thinking  struct {
				BudgetTokens int `json:"budget_tokens"`
			} `json:"thinking"`
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&requestedBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			writeAnthropicTextStream(w, "ok")
		}))
		defer server.Close()

		client := NewClient(config.Config{APIKey: "primary-key", BaseURL: server.URL})
		_, err := client.StreamMessages(context.Background(), MessagesRequest{
			Model:    "claude-test",
			Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
			Thinking: &ThinkingConfig{Type: "enabled", Effort: "high", BudgetTokens: 4096},
		}, StreamCallbacks{OnText: func(string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		if requestedBody.MaxTokens != 8192 {
			t.Fatalf("max_tokens = %d, want 8192 (budget 4096 + 4096)", requestedBody.MaxTokens)
		}

		_, err = client.StreamMessages(context.Background(), MessagesRequest{
			Model:    "claude-test",
			Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
		}, StreamCallbacks{OnText: func(string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		if requestedBody.MaxTokens != 4096 {
			t.Fatalf("max_tokens without thinking = %d, want 4096", requestedBody.MaxTokens)
		}
	})

	t.Run("openai compatible", func(t *testing.T) {
		var requestedBody struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&requestedBody); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			writeOpenAITextStream(w, "ok")
		}))
		defer server.Close()

		client := NewClient(config.Config{
			Provider: "custom",
			APIKey:   "openai-key",
			BaseURL:  server.URL + "/v1",
		})
		_, err := client.StreamMessages(context.Background(), MessagesRequest{
			Model:    "gpt-test",
			Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
			Thinking: &ThinkingConfig{Type: "enabled", Effort: "high", BudgetTokens: 4096},
		}, StreamCallbacks{OnText: func(string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		if requestedBody.MaxCompletionTokens != 8192 {
			t.Fatalf("max_completion_tokens = %d, want 8192 (budget 4096 + 4096)", requestedBody.MaxCompletionTokens)
		}

		_, err = client.StreamMessages(context.Background(), MessagesRequest{
			Model:    "gpt-test",
			Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
		}, StreamCallbacks{OnText: func(string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		if requestedBody.MaxCompletionTokens != 4096 {
			t.Fatalf("max_completion_tokens without thinking = %d, want 4096", requestedBody.MaxCompletionTokens)
		}
	})
}

func TestStreamMessagesRetriesWithoutReasoningEffortAfterThinkingBudgetValidationError(t *testing.T) {
	var attempts int
	var reasoningEffort []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var body struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		reasoningEffort = append(reasoningEffort, body.ReasoningEffort)
		if body.ReasoningEffort != "" {
			http.Error(w, `{"error":{"message":"max_completion_tokens [2048] must be greater than thinking_budget [32768]","type":"invalid_request_error"}}`, http.StatusBadRequest)
			return
		}
		writeOpenAITextStream(w, "recovered")
	}))
	defer server.Close()

	client := NewClient(config.Config{Provider: "custom", APIKey: "key", BaseURL: server.URL + "/v1"})
	var text string
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "glm-5.1",
		MaxTokens: 2048,
		Thinking:  &ThinkingConfig{Type: "enabled", Effort: "medium", BudgetTokens: 2047},
		Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err != nil {
		t.Fatalf("expected reasoning downgrade to recover, got %v", err)
	}
	if attempts != 2 || len(reasoningEffort) != 2 || reasoningEffort[0] != "medium" || reasoningEffort[1] != "" {
		t.Fatalf("attempts=%d reasoning_effort=%v, want two attempts with medium then empty", attempts, reasoningEffort)
	}
	if text != "recovered" {
		t.Fatalf("text=%q, want recovered", text)
	}
}

func TestStreamMessagesRetriesOpenAICompatibleRateLimitAtStreamCreate(t *testing.T) {
	t.Setenv(openAIStreamCreateRateLimitRetryDelayEnv, "1")
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rpm exhausted","type":"rate_limit_reached","code":"rate_limit_reached"}}`))
			return
		}
		writeOpenAITextStream(w, "retry-ok")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "custom",
		APIKey:   "openai-key",
		BaseURL:  server.URL + "/v1",
	})
	var text string
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "deepseek-v4-flash",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if text != "retry-ok" || len(res.Message.Content) != 1 || res.Message.Content[0].Text != "retry-ok" {
		t.Fatalf("text=%q res=%+v", text, res)
	}
}

func TestStreamMessagesFallsBackForOpenAIModelAccessError(t *testing.T) {
	primaryAttempts := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"This token has no access to model deepseek-v4-flash","type":"forbidden"}}`))
	}))
	defer primary.Close()

	nonMatchingAttempts := 0
	nonMatching := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonMatchingAttempts++
		writeOpenAITextStream(w, "wrong-fallback")
	}))
	defer nonMatching.Close()

	fallbackAttempts := 0
	var fallbackAuth string
	var fallbackModel string
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts++
		fallbackAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		fallbackModel = body.Model
		writeOpenAITextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		Provider: "custom",
		APIKey:   "primary-key",
		BaseURL:  primary.URL + "/v1",
		FallbackProviders: []config.ProviderConfig{{
			Name:    "gpt-5.5",
			Type:    "custom",
			BaseURL: nonMatching.URL + "/v1",
			APIKey:  "non-matching-key",
			Model:   "gpt-5.5",
		}, {
			Name:    "sensenova-deepseek",
			Type:    "custom",
			BaseURL: fallback.URL + "/v1",
			APIKey:  "fallback-key",
			Model:   "deepseek-v4-flash",
		}},
	})
	var text string
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "deepseek-v4-flash",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if primaryAttempts != 1 || fallbackAttempts != 1 {
		t.Fatalf("primary attempts=%d fallback attempts=%d", primaryAttempts, fallbackAttempts)
	}
	if nonMatchingAttempts != 0 {
		t.Fatalf("non-matching fallback attempts = %d, want 0", nonMatchingAttempts)
	}
	if fallbackAuth != "Bearer fallback-key" {
		t.Fatalf("fallback auth = %q", fallbackAuth)
	}
	if fallbackModel != "deepseek-v4-flash" {
		t.Fatalf("fallback model = %q", fallbackModel)
	}
	if text != "fallback-ok" || len(res.Message.Content) != 1 {
		t.Fatalf("text=%q res=%+v", text, res)
	}
}

func TestStreamMessagesDoesNotFallbackForOpenAIForbiddenPolicyError(t *testing.T) {
	primaryAttempts := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"policy denied this request","type":"forbidden"}}`))
	}))
	defer primary.Close()

	fallbackAttempts := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts++
		writeOpenAITextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		Provider: "custom",
		APIKey:   "primary-key",
		BaseURL:  primary.URL + "/v1",
		FallbackProviders: []config.ProviderConfig{{
			Name:    "provider1",
			Type:    "custom",
			BaseURL: fallback.URL + "/v1",
			APIKey:  "fallback-key",
		}},
	})
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err == nil {
		t.Fatal("expected forbidden error")
	}
	if primaryAttempts != 1 || fallbackAttempts != 0 {
		t.Fatalf("primary attempts=%d fallback attempts=%d", primaryAttempts, fallbackAttempts)
	}
}

func TestStreamMessagesOpenAICompatiblePartialTextError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeOpenAITextThenErrorStream(w, "openai-partial")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "custom",
		APIKey:   "openai-key",
		BaseURL:  server.URL + "/v1",
	})
	var text string
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "gpt-test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err == nil {
		t.Fatal("expected stream error")
	}
	var partialErr *PartialStreamError
	if !errors.As(err, &partialErr) || partialErr.Partial == nil {
		t.Fatalf("error = %T %v, want PartialStreamError", err, err)
	}
	if len(partialErr.Partial.Message.Content) != 1 || partialErr.Partial.Message.Content[0].Text != "openai-partial" {
		t.Fatalf("partial = %+v", partialErr.Partial)
	}
	if text != "openai-partial" {
		t.Fatalf("text = %q", text)
	}
}

func TestStreamMessagesEmitsModelPhaseTelemetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req-telemetry")
		writeOpenAITextStream(w, "ok")
	}))
	defer server.Close()

	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	client := NewClient(config.Config{
		Provider:         "openai-compatible",
		APIKey:           "key",
		BaseURL:          server.URL + "/v1",
		SelectedProvider: "selected-provider",
	})
	if _, err := client.StreamMessages(ctx, MessagesRequest{
		Model:           "gpt-test",
		Messages:        []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
		TenantSessionID: 42,
	}, StreamCallbacks{}); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	for _, want := range []string{
		"model.phase.request.build.finished",
		"model.phase.http.request_send.finished",
		"model.phase.http.write_request.finished",
		"model.phase.http.wait_first_response_byte.finished",
		"model.phase.http.stream_ready.finished",
		"model.phase.http_round_trip.finished",
		"model.phase.stream.create.finished",
		"model.phase.stream.first_event.finished",
		"model.phase.stream.first_delta.finished",
		"model.phase.stream.read.finished",
	} {
		if !telemetryEventsContain(events, want) {
			t.Fatalf("missing telemetry %q in %+v", want, events)
		}
	}
	create := telemetryEventByName(events, "model.phase.stream.create.finished")
	if create.SessionID != 42 {
		t.Fatalf("stream.create session id = %d, want 42", create.SessionID)
	}
	if create.Properties["provider_name"] != "selected-provider" || create.Properties["provider_role"] != "primary" || create.Properties["endpoint"] != server.URL+"/v1" {
		t.Fatalf("provider properties = %+v", create.Properties)
	}
	if create.Properties["provider_request_id"] != "req-telemetry" || create.Properties["http_status"] != 200 || create.Properties["response_mime"] != "text/event-stream" {
		t.Fatalf("response metadata = %+v", create.Properties)
	}
	for _, key := range []string{"http.request_send_ms", "http.write_request_ms", "http.wait_first_response_byte_ms", "http.stream_ready_ms", "http_round_trip_ms"} {
		if _, ok := create.Properties[key]; !ok {
			t.Fatalf("stream.create missing %s in properties %+v", key, create.Properties)
		}
	}
}

func TestSanitizeEndpointRemovesCredentialsQueryAndFragment(t *testing.T) {
	got := sanitizeEndpoint("https://user:pass@example.test/v1/?token=secret#fragment")
	if got != "https://example.test/v1" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestStreamMessagesMapsOpenAIToolCalls(t *testing.T) {
	var requestedBody struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestedBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeOpenAIToolCallStream(w)
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "openai-compatible",
		APIKey:   "key",
		BaseURL:  server.URL + "/v1",
	})
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "gpt-test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "read"}}}},
		Tools: []ToolDefinition{{
			Name:        "Read",
			Description: "Read a file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}}}`),
		}},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(requestedBody.Tools) != 1 || requestedBody.Tools[0].Type != "function" || requestedBody.Tools[0].Function.Name != "Read" {
		t.Fatalf("tools = %+v", requestedBody.Tools)
	}
	if res.StopReason != "tool_use" || len(res.Message.Content) != 1 {
		t.Fatalf("result = %+v", res)
	}
	block := res.Message.Content[0]
	if block.Type != "tool_use" || block.ID != "call_1" || block.Name != "Read" || string(block.Input) != `{"file_path":"README.md"}` {
		t.Fatalf("tool block = %+v", block)
	}
}

func TestOpenAIStreamResultSplitsConcatenatedToolArgumentObjects(t *testing.T) {
	var args strings.Builder
	args.WriteString(`{"file_path":"/tmp/a.go"}`)
	args.WriteString(`{"file_path":"/tmp/b.go"}`)
	result := openAIStreamResult("", map[int]*openAIToolAccumulator{
		0: {id: "call_read", name: "Read", arguments: args},
	}, "tool_use", Usage{})

	if len(result.Message.Content) != 2 {
		t.Fatalf("content = %+v", result.Message.Content)
	}
	first := result.Message.Content[0]
	second := result.Message.Content[1]
	if first.ID != "call_read" || string(first.Input) != `{"file_path":"/tmp/a.go"}` {
		t.Fatalf("first block = %+v", first)
	}
	if second.ID != "call_read_part_2" || string(second.Input) != `{"file_path":"/tmp/b.go"}` {
		t.Fatalf("second block = %+v", second)
	}
}

func telemetryEventsContain(events []telemetry.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func telemetryEventByName(events []telemetry.Event, name string) telemetry.Event {
	for _, event := range events {
		if event.Name == name {
			return event
		}
	}
	return telemetry.Event{}
}

func TestStreamMessagesDoesNotFallbackAfterPartialText(t *testing.T) {
	callbackErr := errors.New("stop after partial")
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAnthropicTextStream(w, "partial")
	}))
	defer primary.Close()

	fallbackAttempts := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts++
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		APIKey:  "key",
		BaseURL: primary.URL,
		FallbackProviders: []config.ProviderConfig{{
			Name:    "provider1",
			BaseURL: fallback.URL,
			APIKey:  "key",
		}},
	})
	var text string
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return callbackErr
	}})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error = %v, want callback error", err)
	}
	if text != "partial" {
		t.Fatalf("text = %q, want partial", text)
	}
	if fallbackAttempts != 0 {
		t.Fatalf("fallback attempts = %d, want 0", fallbackAttempts)
	}
}

func TestStreamMessagesReturnsAggregatedProviderFailures(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "primary down", http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fallback down", http.StatusBadGateway)
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		APIKey:  "key",
		BaseURL: primary.URL,
		FallbackProviders: []config.ProviderConfig{{
			Name:    "provider1",
			BaseURL: fallback.URL,
			APIKey:  "key",
		}},
	})
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "all providers failed") || !strings.Contains(msg, "primary") || !strings.Contains(msg, "provider1") {
		t.Fatalf("error = %q", msg)
	}
}

func TestStreamMessagesDoesNotFallbackForRequestErrors(t *testing.T) {
	primaryAttempts := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryAttempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad request"}}`))
	}))
	defer primary.Close()
	fallbackAttempts := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts++
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		APIKey:  "key",
		BaseURL: primary.URL,
		FallbackProviders: []config.ProviderConfig{{
			Name:    "provider1",
			BaseURL: fallback.URL,
			APIKey:  "key",
		}},
	})
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err == nil {
		t.Fatal("expected request error")
	}
	if primaryAttempts != 1 || fallbackAttempts != 0 {
		t.Fatalf("primary attempts=%d fallback attempts=%d", primaryAttempts, fallbackAttempts)
	}
}

func TestStreamMessagesDoesNotFallbackAfterStreamErrorWithPartialText(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAnthropicTextThenErrorStream(w, "partial")
	}))
	defer primary.Close()
	fallbackAttempts := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts++
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		APIKey:  "key",
		BaseURL: primary.URL,
		FallbackProviders: []config.ProviderConfig{{
			Name:    "provider1",
			BaseURL: fallback.URL,
			APIKey:  "key",
		}},
	})
	var text string
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err == nil {
		t.Fatal("expected stream error")
	}
	var partialErr *PartialStreamError
	if !errors.As(err, &partialErr) || partialErr.Partial == nil {
		t.Fatalf("error = %T %v, want PartialStreamError", err, err)
	}
	if len(partialErr.Partial.Message.Content) != 1 || partialErr.Partial.Message.Content[0].Text != "partial" {
		t.Fatalf("partial = %+v", partialErr.Partial)
	}
	if text != "partial" {
		t.Fatalf("text = %q, want partial", text)
	}
	if fallbackAttempts != 0 {
		t.Fatalf("fallback attempts = %d, want 0", fallbackAttempts)
	}
}

func TestStreamMessagesRetriesRetryableStatus(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")))
	}))
	defer server.Close()
	client := NewClient(config.Config{APIKey: "key", BaseURL: server.URL})
	var text string
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || text != "ok" || len(res.Message.Content) != 1 {
		t.Fatalf("attempts=%d text=%q res=%+v", attempts, text, res)
	}
}

func writeAnthropicTextStream(w http.ResponseWriter, text string) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")))
}

func writeOpenAITextStream(w http.ResponseWriter, text string) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"` + text + `"}}]}`,
		``,
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")))
}

func TestStreamMessagesOpenAIReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"choices":[{"delta":{"reasoning_content":"let me think"}}]}`,
			`{"choices":[{"delta":{"content":"answer"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":6}}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(config.Config{Provider: "custom", APIKey: "k", BaseURL: server.URL + "/v1"})
	var thinking, text string
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "glm-5.1",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{
		OnThinking: func(delta string) error { thinking += delta; return nil },
		OnText:     func(delta string) error { text += delta; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if thinking != "let me think" {
		t.Fatalf("thinking = %q", thinking)
	}
	if text != "answer" {
		t.Fatalf("text = %q", text)
	}
	if res.Usage.ReasoningOutputTokens != 6 {
		t.Fatalf("reasoning tokens = %d", res.Usage.ReasoningOutputTokens)
	}
	for _, block := range res.Message.Content {
		if strings.Contains(block.Text, "let me think") {
			t.Fatalf("reasoning must not leak into message content: %+v", res.Message.Content)
		}
	}
}

func writeOpenAITextThenErrorStream(w http.ResponseWriter, text string) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"` + text + `"}}]}`,
		``,
	}, "\n")))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if hijacker, ok := w.(http.Hijacker); ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
}

func writeOpenAIToolCallStream(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\""}}]}}]}`,
		``,
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")))
}

func writeAnthropicTextThenErrorStream(w http.ResponseWriter, text string) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta"`,
	}, "\n")))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if hijacker, ok := w.(http.Hijacker); ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
}
