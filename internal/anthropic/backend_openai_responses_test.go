package anthropic

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

	"github.com/konglong87/super-niuma/internal/config"
	providerstate "github.com/konglong87/super-niuma/internal/provider"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/openai/openai-go/v3/responses"
)

func TestLegacyCustomProviderKeepsChatCompletionsDispatch(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		writeOpenAITextStream(w, "legacy-ok")
	}))
	defer server.Close()

	client := NewClient(config.Config{Provider: "custom", APIKey: "key", BaseURL: server.URL + "/v1"})
	if client.providers[0].source != protocolSourceLegacyDerived || client.providers[0].backend != nil {
		t.Fatalf("legacy provider unexpectedly entered backend registry: %+v", client.providers[0])
	}
	res, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model: "legacy-model", Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/chat/completions" || res.Message.Content[0].Text != "legacy-ok" {
		t.Fatalf("path=%q result=%+v", path, res)
	}
}

func TestExplicitChatProtocolKeepsExistingChatBackend(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		writeOpenAITextStream(w, "chat-ok")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "openai-compatible", ProviderProtocol: config.ProviderProtocolOpenAIChatCompletions,
		APIKey: "key", BaseURL: server.URL + "/v1",
	})
	if client.providers[0].source != protocolSourceExplicit || client.providers[0].backend != nil {
		t.Fatalf("explicit chat provider should use the existing implementation: %+v", client.providers[0])
	}
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model: "chat-model", Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/chat/completions" {
		t.Fatalf("path = %q", path)
	}
}

func TestOpenAIResponsesBackendMapsRequestAndTypedStream(t *testing.T) {
	var path, auth string
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeOpenAIResponsesStream(w)
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "responses-key", BaseURL: server.URL + "/v1",
	})
	provider := client.providers[0]
	if provider.protocol != config.ProviderProtocolOpenAIResponses || provider.backend == nil {
		t.Fatalf("Responses backend was not selected: %+v", provider)
	}
	if provider.backend.Capabilities()[capabilityPreviousResponseID] {
		t.Fatal("previous-response-id must remain disabled in phase one")
	}

	var thinking, text string
	result, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "gpt-responses-test",
		MaxTokens: 2048,
		System:    "fallback system",
		SystemBlocks: []SystemBlock{
			{Type: "text", Text: "static system"},
			{Type: "text", Text: "dynamic system"},
		},
		Messages: []MessageParam{
			{Role: "user", Content: []ContentBlock{
				{Type: "text", Text: "inspect"},
				{Type: "image", Source: &ContentSource{Type: "base64", MediaType: "image/png", Data: "aW1hZ2U="}},
			}},
			{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ID: "call_previous", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`)}}},
			{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "call_previous", Content: "file contents"}}},
		},
		Tools:    []ToolDefinition{{Name: "Read", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}`)}},
		Thinking: &ThinkingConfig{Type: "enabled", Effort: "high"},
		ResponseFormat: &ResponseFormat{Type: "json_schema", JSONSchema: &ResponseFormatSchema{
			Name: "answer", Description: "answer schema", Strict: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
		}},
	}, StreamCallbacks{
		OnThinking: func(delta string) error { thinking += delta; return nil },
		OnText:     func(delta string) error { text += delta; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	if path != "/v1/responses" || auth != "Bearer responses-key" {
		t.Fatalf("path=%q auth=%q", path, auth)
	}
	if request["store"] != false {
		t.Fatalf("store = %#v, want false", request["store"])
	}
	include, ok := request["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", request["include"])
	}
	if _, ok := request["previous_response_id"]; ok {
		t.Fatalf("phase one request sent previous_response_id: %#v", request)
	}
	if request["instructions"] != "static system\n\ndynamic system" {
		t.Fatalf("instructions = %#v", request["instructions"])
	}
	if request["model"] != "gpt-responses-test" || request["max_output_tokens"] != float64(2048) {
		t.Fatalf("model/max_output_tokens = %#v/%#v", request["model"], request["max_output_tokens"])
	}
	assertResponsesRequestShape(t, request)

	if thinking != "brief reasoning" || text != `{"ok":true}` {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
	if result.StopReason != "tool_use" || len(result.Message.Content) != 4 {
		t.Fatalf("result = %+v", result)
	}
	continuation := result.Message.Content[0]
	if continuation.Type != responsesContinuationBlockType || continuation.Continuation == nil || continuation.Continuation.OpaqueItems[0].EncryptedContent != "encrypted-reasoning" {
		t.Fatalf("continuation = %+v", continuation)
	}
	tool := result.Message.Content[3]
	if tool.Type != "tool_use" || tool.ID != "call_42" || tool.Name != "Read" || string(tool.Input) != `{"file_path":"go.mod"}` {
		t.Fatalf("tool block = %+v", tool)
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 8 || result.Usage.CacheReadInputTokens != 4 || result.Usage.ReasoningOutputTokens != 3 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}

func TestOpenAIResponsesRequestReplaysOpaqueReasoning(t *testing.T) {
	continuation := providerContinuationForTest()
	params, err := openAIResponsesRequest(MessagesRequest{
		Model: "gpt-test", MaxTokens: 100,
		Messages: []MessageParam{
			{Role: "assistant", Content: []ContentBlock{{Type: responsesContinuationBlockType, Continuation: &continuation}, {Type: "tool_use", ID: "call_1", Name: "Read", Input: json.RawMessage(`{}`)}}},
			{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "call_1", Content: "ok"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"reasoning"`, `"id":"rs_1"`, `"encrypted_content":"encrypted-reasoning"`, `"type":"function_call_output"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("request missing %s: %s", want, data)
		}
	}
}

func TestOpenAIResponsesRequestEncodesAssistantTextAsOutputMessage(t *testing.T) {
	params, err := openAIResponsesRequest(MessagesRequest{
		Model: "gpt-test", MaxTokens: 100,
		Messages: []MessageParam{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "assistant", Content: []ContentBlock{{Type: "text", ID: "msg_previous", Text: "previous answer"}}},
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "continue"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, want := range []string{`"id":"msg_previous"`, `"role":"assistant"`, `"status":"completed"`, `"type":"output_text"`, `"text":"previous answer"`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("request missing %s: %s", want, encoded)
		}
	}
	if strings.Contains(encoded, `"role":"assistant","content":[{"text":"previous answer","type":"input_text"`) {
		t.Fatalf("assistant history was encoded as input_text: %s", encoded)
	}
}

func TestOpenAIResponsesRequestGeneratesReplayIDForLegacyAssistantText(t *testing.T) {
	params, err := openAIResponsesRequest(MessagesRequest{
		Model: "gpt-test", MaxTokens: 100,
		Messages: []MessageParam{{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "legacy answer"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"id":"`+responsesReplayMessageIDPrefix+`0_0"`) {
		t.Fatalf("request missing generated replay id: %s", data)
	}
}

func TestOpenAIResponsesStreamPreservesOutputMessageIDForReplay(t *testing.T) {
	state := newOpenAIResponsesStreamState()
	_, err := state.consume(responses.ResponseStreamEventUnion{
		Type: responsesEventOutputItemAdded,
		Item: responses.ResponseOutputItemUnion{Type: "message", ID: "msg_streamed"},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.consume(responses.ResponseStreamEventUnion{
		Type: responsesEventOutputTextDelta, Delta: "answer",
	}, StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	result := state.result()
	if len(result.Message.Content) != 1 || result.Message.Content[0].Type != "text" || result.Message.Content[0].ID != "msg_streamed" {
		t.Fatalf("streamed text block = %+v", result.Message.Content)
	}
}

func TestOpenAIResponsesDeduplicatesToolCallsByCallID(t *testing.T) {
	first := &openAIResponsesToolAccumulator{itemID: "item_1", callID: "call_1", name: "Read"}
	first.arguments.WriteString(`{}`)
	second := &openAIResponsesToolAccumulator{itemID: "item_alias", callID: "call_1", name: "Read"}
	second.arguments.WriteString(`{"file_path":"README.md"}`)
	state := &openAIResponsesStreamState{
		tools:     map[string]*openAIResponsesToolAccumulator{"item_1": first, "item_alias": second},
		toolOrder: []string{"item_1", "item_alias"},
	}

	result := state.result()
	if len(result.Message.Content) != 1 {
		t.Fatalf("tool blocks = %d, want one block per call_id", len(result.Message.Content))
	}
	tool := result.Message.Content[0]
	if tool.Type != "tool_use" || tool.ID != "call_1" || string(tool.Input) != `{"file_path":"README.md"}` {
		t.Fatalf("deduplicated tool = %+v", tool)
	}
}

func providerContinuationForTest() providerstate.Continuation {
	return providerstate.Continuation{
		Version: providerstate.ContinuationVersion, Protocol: config.ProviderProtocolOpenAIResponses,
		Provider: "primary", EndpointID: providerstate.EndpointID("https://example.com/v1"), Model: "gpt-test",
		OpaqueItems: []providerstate.OpaqueItem{{Type: "reasoning", ID: "rs_1", EncryptedContent: "encrypted-reasoning"}},
	}
}

func assertResponsesRequestShape(t *testing.T, request map[string]any) {
	t.Helper()
	reasoning, ok := request["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", request["reasoning"])
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", request["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "Read" || tool["description"] != "Read a file" {
		t.Fatalf("tool = %#v", tool)
	}
	textConfig, ok := request["text"].(map[string]any)
	if !ok {
		t.Fatalf("text config = %#v", request["text"])
	}
	format := textConfig["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "answer" || format["strict"] != true {
		t.Fatalf("format = %#v", format)
	}
	items, ok := request["input"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("input = %#v", request["input"])
	}
	encoded, _ := json.Marshal(items)
	for _, want := range []string{`"type":"input_image"`, `"type":"function_call"`, `"type":"function_call_output"`, `"call_id":"call_previous"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("input missing %s: %s", want, encoded)
		}
	}
}

func TestOpenAIResponsesCanFallbackBeforeAnyDelta(t *testing.T) {
	primaryCalls := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"temporarily unavailable","type":"server_error","code":"server_error"}}`))
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "key", BaseURL: primary.URL + "/v1",
		FallbackProviders: []config.ProviderConfig{{
			Name: "anthropic-backup", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-key",
		}},
	})
	result, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model: "test", Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if primaryCalls == 0 || len(result.Message.Content) != 1 || result.Message.Content[0].Text != "fallback-ok" {
		t.Fatalf("primaryCalls=%d result=%+v", primaryCalls, result)
	}
}

func TestOpenAIResponsesProviderTimeoutFallsBack(t *testing.T) {
	previousTimeouts := providerTimeouts
	providerTimeouts = providerHTTPTimeouts{responseHeader: 25 * time.Millisecond}
	t.Cleanup(func() { providerTimeouts = previousTimeouts })

	primary := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(150 * time.Millisecond)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAnthropicTextStream(w, "responses-timeout-fallback")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "primary-key", BaseURL: primary.URL + "/v1",
		FallbackProviders: []config.ProviderConfig{{Name: "backup", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-key"}},
	})
	result, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
	if err != nil {
		t.Fatalf("timeout should fail over to the next provider: %v", err)
	}
	if result == nil || len(result.Message.Content) != 1 || result.Message.Content[0].Text != "responses-timeout-fallback" {
		t.Fatalf("result = %+v, want fallback response", result)
	}
}

func TestOpenAIResponsesBackendCanBeFallbackProvider(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"responses-fallback\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"model\":\"fallback-model\",\"output\":[],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1,\"output_tokens_details\":{\"reasoning_tokens\":0},\"total_tokens\":2}}}\n\n")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "unsupported", APIKey: "primary-key",
		FallbackProviders: []config.ProviderConfig{{
			Name: "responses-backup", Type: "custom", Protocol: config.ProviderProtocolOpenAIResponses,
			BaseURL: server.URL + "/v1", APIKey: "fallback-key", Model: "fallback-model",
		}},
	})
	result, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/responses" || len(result.Message.Content) != 1 || result.Message.Content[0].Text != "responses-fallback" {
		t.Fatalf("path=%q result=%+v", path, result)
	}
}

func TestOpenAIResponsesSSEFailureFallbackAndMidstreamBoundary(t *testing.T) {
	t.Run("before delta", func(t *testing.T) {
		primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"sequence_number\":1,\"code\":\"server_error\",\"message\":\"temporarily unavailable\"}\n\n")
		}))
		defer primary.Close()
		fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeAnthropicTextStream(w, "fallback-ok")
		}))
		defer fallback.Close()
		client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
		result, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
		if err != nil || result == nil || len(result.Message.Content) == 0 || result.Message.Content[0].Text != "fallback-ok" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("after delta", func(t *testing.T) {
		primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\"}\n\ndata: {\"type\":\"error\",\"sequence_number\":2,\"code\":\"server_error\",\"message\":\"temporarily unavailable\"}\n\n")
		}))
		defer primary.Close()
		fallbackCalls := 0
		fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackCalls++
			writeAnthropicTextStream(w, "must-not-run")
		}))
		defer fallback.Close()
		client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
		var text string
		result, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
			OnText: func(delta string) error { text += delta; return nil },
		})
		if err == nil || result != nil || text != "partial" || fallbackCalls != 0 {
			t.Fatalf("result=%+v err=%v text=%q fallbackCalls=%d", result, err, text, fallbackCalls)
		}
		var partial *PartialStreamError
		if !errors.As(err, &partial) || partial.Partial.Message.Content[0].Text != "partial" {
			t.Fatalf("partial error = %#v", err)
		}
	})
}

func TestOpenAIResponsesTruncatedStreamRetriesBeforeVisibleOutput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeOpenAIResponsesTruncatedStream(w, false)
			return
		}
		writeOpenAIResponsesTextStream(w, "recovered")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider:         "custom",
		ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey:           "key",
		BaseURL:          server.URL + "/v1",
		SelectedProvider: "responses",
	})
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	result, err := client.StreamMessages(ctx, basicResponsesRequest(), StreamCallbacks{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 2 || result == nil || len(result.Message.Content) != 1 || result.Message.Content[0].Text != "recovered" {
		t.Fatalf("calls=%d result=%+v", calls.Load(), result)
	}
	foundRetryAttempt := false
	for _, event := range sink.Events() {
		if event.Properties["retry_attempt"] == 1 {
			foundRetryAttempt = true
			break
		}
	}
	if !foundRetryAttempt {
		t.Fatalf("retry attempt was not recorded in telemetry: %+v", sink.Events())
	}
}

func TestOpenAIResponsesReasoningOnlyFailureRetriesSixTimesThenSucceeds(t *testing.T) {
	const wantedJSONRetries = 6
	restoreBackoff := responsesMidstreamBackoff
	responsesMidstreamBackoff = time.Millisecond
	t.Cleanup(func() { responsesMidstreamBackoff = restoreBackoff })

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := attempts.Add(1)
		w.Header().Set("X-Request-ID", fmt.Sprintf("req-%d", attempt))
		if attempt <= wantedJSONRetries {
			writeOpenAIResponsesTruncatedReasoningStream(w, fmt.Sprintf("discard-%d", attempt))
			return
		}
		writeOpenAIResponsesReasoningTextStream(w, "kept-reasoning", "recovered")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "key", BaseURL: server.URL, SelectedProvider: "responses",
	})
	var reasoning, text strings.Builder
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	result, err := client.StreamMessages(ctx, basicResponsesRequest(), StreamCallbacks{
		OnThinking: func(delta string) error { reasoning.WriteString(delta); return nil },
		OnText:     func(delta string) error { text.WriteString(delta); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := attempts.Load(), int32(wantedJSONRetries+1); got != want {
		t.Fatalf("orchestration attempts = %d, want %d", got, want)
	}
	if got := reasoning.String(); got != "kept-reasoning" {
		t.Fatalf("reasoning callbacks = %q, want only successful-attempt reasoning", got)
	}
	if got := text.String(); got != "recovered" {
		t.Fatalf("text callbacks = %q, want recovered", got)
	}
	if got := visibleText(result.Message.Content); got != "recovered" {
		t.Fatalf("result text = %q, want recovered", got)
	}
	assertResponsesRetryOutcome(t, sink, "recovered")
	requestIDs := make(map[string]bool)
	failedReadRequestIDs := make(map[string]bool)
	firstEventRequestIDs := make(map[string]bool)
	firstDeltaRequestIDs := make(map[string]bool)
	successPhaseRequestIDs := make(map[string]string)
	var outcomeLastFailedRequestID string
	for _, event := range sink.Events() {
		if event.Name == telemetry.EventModelPhase+"stream.create"+telemetry.SpanFinishedSuffix {
			if requestID, _ := event.Properties["provider_request_id"].(string); requestID != "" {
				requestIDs[requestID] = true
			}
		}
		if event.Name == telemetry.EventModelPhase+"stream.read"+telemetry.SpanFinishedSuffix && event.Status == telemetry.StatusError {
			if requestID, _ := event.Properties["provider_request_id"].(string); requestID != "" {
				failedReadRequestIDs[requestID] = true
			}
			if event.Properties["http_status"] != http.StatusOK || event.Properties["response_mime"] != "text/event-stream" {
				t.Fatalf("failed stream.read response metadata = %+v", event.Properties)
			}
		}
		if event.Properties["retry_outcome"] == responsesRetryOutcomeRecovered {
			outcomeLastFailedRequestID, _ = event.Properties["last_failed_provider_request_id"].(string)
			if _, ok := event.Properties["provider_request_id"]; ok {
				t.Fatalf("recovered outcome must not report failed request as provider_request_id: %+v", event.Properties)
			}
		}
		if event.Name == telemetry.EventModelPhase+"stream.first_event"+telemetry.SpanFinishedSuffix {
			if requestID, _ := event.Properties["provider_request_id"].(string); requestID != "" {
				firstEventRequestIDs[requestID] = true
			}
		}
		if event.Name == telemetry.EventModelPhase+"stream.first_delta"+telemetry.SpanFinishedSuffix {
			if requestID, _ := event.Properties["provider_request_id"].(string); requestID != "" {
				firstDeltaRequestIDs[requestID] = true
			}
		}
		if event.Status == telemetry.StatusOK {
			for _, phase := range []string{"stream.first_event", "stream.first_delta", "stream.read"} {
				if event.Name == telemetry.EventModelPhase+phase+telemetry.SpanFinishedSuffix {
					successPhaseRequestIDs[phase], _ = event.Properties["provider_request_id"].(string)
				}
			}
		}
	}
	for attempt := 1; attempt <= wantedJSONRetries+1; attempt++ {
		requestID := fmt.Sprintf("req-%d", attempt)
		if !requestIDs[requestID] {
			t.Fatalf("missing provider request ID for attempt %d: %+v", attempt, requestIDs)
		}
		if !firstEventRequestIDs[requestID] || !firstDeltaRequestIDs[requestID] {
			t.Fatalf("attempt %d phase correlation missing: first_event=%+v first_delta=%+v", attempt, firstEventRequestIDs, firstDeltaRequestIDs)
		}
	}
	for attempt := 1; attempt <= wantedJSONRetries; attempt++ {
		if !failedReadRequestIDs[fmt.Sprintf("req-%d", attempt)] {
			t.Fatalf("missing failed stream.read request ID for attempt %d: %+v", attempt, failedReadRequestIDs)
		}
	}
	if outcomeLastFailedRequestID != "req-6" {
		t.Fatalf("recovered outcome last failed request ID = %q, want req-6", outcomeLastFailedRequestID)
	}
	for _, phase := range []string{"stream.first_event", "stream.first_delta", "stream.read"} {
		if got := successPhaseRequestIDs[phase]; got != "req-7" {
			t.Fatalf("successful %s request ID = %q, want req-7; phases=%+v", phase, got, successPhaseRequestIDs)
		}
	}
}

func TestOpenAIResponsesForwardsReasoningAfterTextCommitInOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeOpenAIResponsesReasoningTextReasoningStream(w, "before", "answer", "after")
	}))
	defer server.Close()
	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "key", BaseURL: server.URL,
	})
	var callbacks []string
	_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
		OnThinking: func(delta string) error { callbacks = append(callbacks, "thinking:"+delta); return nil },
		OnText:     func(delta string) error { callbacks = append(callbacks, "text:"+delta); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(callbacks, ","); got != "thinking:before,text:answer,thinking:after" {
		t.Fatalf("callback order = %q", got)
	}
}

func TestOpenAIResponsesReasoningAfterTextCallbackErrorPropagatesWithoutFallback(t *testing.T) {
	sentinel := &responsesCallbackTestError{message: "late reasoning callback failed"}
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesReasoningTextReasoningStream(w, "before", "answer", "after")
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "must-not-run")
	}))
	defer fallback.Close()
	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	thinkingCalls := 0
	_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
		OnThinking: func(string) error {
			thinkingCalls++
			if thinkingCalls == 2 {
				return sentinel
			}
			return nil
		},
	})
	var typed *responsesCallbackTestError
	if !errors.Is(err, sentinel) || !errors.As(err, &typed) || typed != sentinel {
		t.Fatalf("err = %v, want late callback error identity", err)
	}
	if thinkingCalls != 2 || primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("thinking=%d primary=%d fallback=%d", thinkingCalls, primaryCalls.Load(), fallbackCalls.Load())
	}
}

func TestOpenAIResponsesTerminalProviderErrorReadCarriesResponseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-ID", "req-terminal")
		_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.failed","sequence_number":1,"response":{"id":"resp_1","status":"failed","model":"test","error":{"code":"server_error","message":"upstream failed"},"output":[]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "key", BaseURL: server.URL,
	})
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	_, err := client.StreamMessages(ctx, basicResponsesRequest(), StreamCallbacks{})
	if err == nil {
		t.Fatal("expected terminal provider error")
	}
	found := make(map[string]bool)
	for _, event := range sink.Events() {
		phase := ""
		switch event.Name {
		case telemetry.EventModelPhase + "stream.first_event" + telemetry.SpanFinishedSuffix:
			phase = "stream.first_event"
		case telemetry.EventModelPhase + "stream.read" + telemetry.SpanFinishedSuffix:
			if event.Status != telemetry.StatusError {
				continue
			}
			phase = "stream.read"
		default:
			continue
		}
		if event.Properties["provider_request_id"] != "req-terminal" || event.Properties["http_status"] != http.StatusOK || event.Properties["response_mime"] != "text/event-stream" {
			t.Fatalf("terminal %s metadata = %+v", phase, event.Properties)
		}
		found[phase] = true
	}
	if !found["stream.first_event"] || !found["stream.read"] {
		t.Fatalf("terminal stream phase metadata missing: found=%+v events=%+v", found, sink.Events())
	}
}

func TestOpenAIResponsesZeroOutputTerminalFailureFallsBackWithoutInPlaceRetry(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesFailedStream(w)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()
	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	result, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatalf("primary=%d fallback=%d, want 1/1", primaryCalls.Load(), fallbackCalls.Load())
	}
	if got := visibleText(result.Message.Content); got != "fallback-ok" {
		t.Fatalf("result text = %q", got)
	}
}

func TestOpenAIResponsesNoDeltaJSONFailureRetainsOneRetryThenFallsBack(t *testing.T) {
	restoreBackoff := responsesMidstreamBackoff
	responsesMidstreamBackoff = time.Millisecond
	t.Cleanup(func() { responsesMidstreamBackoff = restoreBackoff })

	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesTruncatedStream(w, false)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	result, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if primaryCalls.Load() != 2 || fallbackCalls.Load() != 1 {
		t.Fatalf("primary=%d fallback=%d, want primary=2 fallback=1", primaryCalls.Load(), fallbackCalls.Load())
	}
	if got := visibleText(result.Message.Content); got != "fallback-ok" {
		t.Fatalf("result text = %q", got)
	}
}

func TestOpenAIResponsesTruncatedStreamAfterTextDoesNotReplayAndCoolsProvider(t *testing.T) {
	var fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeOpenAIResponsesTruncatedStream(w, true)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "must-not-run")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		Provider:          "custom",
		ProviderProtocol:  config.ProviderProtocolOpenAIResponses,
		APIKey:            "key",
		BaseURL:           primary.URL + "/v1",
		FallbackProviders: []config.ProviderConfig{{Name: "fallback", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-key"}},
	})
	var text string
	_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
		OnText: func(delta string) error { text += delta; return nil },
	})
	if err == nil || text != "partial" || fallbackCalls.Load() != 0 {
		t.Fatalf("err=%v text=%q fallbackCalls=%d", err, text, fallbackCalls.Load())
	}
	var streamErr *openAIResponsesStreamError
	if !errors.As(err, &streamErr) || streamErr.failureKind != responsesJSONFailureKind || streamErr.events != 2 || streamErr.textDeltas != 1 {
		t.Fatalf("stream error metadata = %#v", streamErr)
	}
	if cooling := client.breaker.cooling(client.providers); len(cooling) == 0 {
		t.Fatal("provider was not cooled down after a mid-stream protocol failure")
	}
}

func TestOpenAIResponsesReasoningOnlyFailureExhaustsThenFallsBackAndCoolsProvider(t *testing.T) {
	restoreBackoff, restoreMax := responsesMidstreamBackoff, responsesMidstreamMaxBackoff
	responsesMidstreamBackoff, responsesMidstreamMaxBackoff = time.Millisecond, time.Millisecond
	t.Cleanup(func() {
		responsesMidstreamBackoff, responsesMidstreamMaxBackoff = restoreBackoff, restoreMax
	})

	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := primaryCalls.Add(1)
		w.Header().Set("X-Request-ID", fmt.Sprintf("req-exhaust-%d", attempt))
		writeOpenAIResponsesTruncatedReasoningStream(w, "discarded")
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	defer fallback.Close()

	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	var reasoning strings.Builder
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	result, err := client.StreamMessages(ctx, basicResponsesRequest(), StreamCallbacks{
		OnThinking: func(delta string) error { reasoning.WriteString(delta); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := primaryCalls.Load(); got != responsesJSONMidstreamRetries+1 {
		t.Fatalf("primary calls = %d, want %d", got, responsesJSONMidstreamRetries+1)
	}
	if fallbackCalls.Load() != 1 || visibleText(result.Message.Content) != "fallback-ok" {
		t.Fatalf("fallback calls=%d result=%+v", fallbackCalls.Load(), result)
	}
	if reasoning.Len() != 0 {
		t.Fatalf("failed reasoning leaked to callback: %q", reasoning.String())
	}
	if _, ok := client.breaker.cooling(client.providers)[client.providers[0].name]; !ok {
		t.Fatal("exhausted Responses provider did not enter cooldown")
	}
	assertResponsesRetryOutcome(t, sink, "exhausted")
	assertResponsesRetryOutcomeLastFailedRequestID(t, sink, responsesRetryOutcomeExhausted, "req-exhaust-7")
}

func TestOpenAIResponsesTextFailureWithNilCallbacksDoesNotRetryOrFallback(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesTruncatedStream(w, true)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "must-not-run")
	}))
	defer fallback.Close()

	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	_, err := client.StreamMessages(ctx, basicResponsesRequest(), StreamCallbacks{})
	if err == nil || primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("err=%v primary=%d fallback=%d", err, primaryCalls.Load(), fallbackCalls.Load())
	}
	assertResponsesRetryOutcome(t, sink, "committed_output")
}

func TestOpenAIResponsesToolSemanticsDoNotReplayOrFallback(t *testing.T) {
	cases := []struct {
		name  string
		write func(http.ResponseWriter)
	}{
		{name: "function call item only", write: writeOpenAIResponsesTruncatedToolItemStream},
		{name: "arguments delta only", write: writeOpenAIResponsesTruncatedToolArgumentsStream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls, fallbackCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				tc.write(w)
			}))
			defer server.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fallbackCalls.Add(1)
				writeAnthropicTextStream(w, "must-not-run")
			}))
			defer fallback.Close()

			client := responsesClientWithAnthropicFallback(server.URL, fallback.URL)
			_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
			if err == nil || calls.Load() != 1 || fallbackCalls.Load() != 0 {
				t.Fatalf("err=%v calls=%d fallback=%d; tool semantics must not be replayed or fall back", err, calls.Load(), fallbackCalls.Load())
			}
		})
	}
}

func TestOpenAIResponsesToolItemThenTerminalFailureDoesNotRetryOrFallback(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesToolItemFailedStream(w)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "must-not-run")
	}))
	defer fallback.Close()
	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
	if err == nil || primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("err=%v primary=%d fallback=%d", err, primaryCalls.Load(), fallbackCalls.Load())
	}
	var streamErr *openAIResponsesStreamError
	var eventErr *openAIResponsesEventError
	if !errors.As(err, &streamErr) || !errors.As(err, &eventErr) || streamErr.toolCalls != 1 || !eventErr.retryable {
		t.Fatalf("terminal error chain/state = %#v", err)
	}
}

func TestOpenAIResponsesToolCommitFlushesBufferedReasoningBeforeError(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesReasoningToolMalformedStream(w)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "must-not-run")
	}))
	defer fallback.Close()
	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	var reasoning strings.Builder
	_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
		OnThinking: func(delta string) error { reasoning.WriteString(delta); return nil },
	})
	if err == nil || primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("err=%v primary=%d fallback=%d", err, primaryCalls.Load(), fallbackCalls.Load())
	}
	if got := reasoning.String(); got != "tool reasoning" {
		t.Fatalf("reasoning callback = %q, want once", got)
	}
}

func TestOpenAIResponsesToolCommitReasoningCallbackErrorReplacesProviderError(t *testing.T) {
	sentinel := &responsesCallbackTestError{message: "tool-commit reasoning callback failed"}
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesReasoningToolMalformedStream(w)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "must-not-run")
	}))
	defer fallback.Close()
	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
		OnThinking: func(string) error { return sentinel },
	})
	var typed *responsesCallbackTestError
	if !errors.Is(err, sentinel) || !errors.As(err, &typed) || typed != sentinel {
		t.Fatalf("err = %v, want callback error identity", err)
	}
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("primary=%d fallback=%d", primaryCalls.Load(), fallbackCalls.Load())
	}
	if _, ok := client.breaker.cooling(client.providers)[client.providers[0].name]; !ok {
		t.Fatal("retryable provider failure was not recorded before callback error replaced it")
	}
}

func TestOpenAIResponsesCancellationDuringBackoffStopsRetry(t *testing.T) {
	restoreBackoff := responsesMidstreamBackoff
	responsesMidstreamBackoff = time.Minute
	t.Cleanup(func() { responsesMidstreamBackoff = restoreBackoff })

	var calls atomic.Int32
	firstRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(firstRequest)
		}
		writeOpenAIResponsesTruncatedReasoningStream(w, "discarded")
	}))
	defer server.Close()
	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "key", BaseURL: server.URL,
	})
	ctx, cancel := context.WithCancel(context.Background())
	sink := telemetry.NewMemorySink()
	ctx = telemetry.WithEmitter(ctx, telemetry.NewEmitter(sink))
	errCh := make(chan error, 1)
	go func() {
		_, err := client.StreamMessages(ctx, basicResponsesRequest(), StreamCallbacks{})
		errCh <- err
	}()
	<-firstRequest
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled identity", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt Responses retry backoff")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	assertResponsesRetryOutcome(t, sink, "cancelled")
}

func TestOpenAIResponsesCallbackErrorDoesNotRetryOrFallback(t *testing.T) {
	sentinel := &responsesCallbackTestError{message: "thinking callback failed"}
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		writeOpenAIResponsesReasoningTextStream(w, "thinking", "answer")
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writeAnthropicTextStream(w, "must-not-run")
	}))
	defer fallback.Close()

	client := responsesClientWithAnthropicFallback(primary.URL, fallback.URL)
	_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
		OnThinking: func(string) error { return sentinel },
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want callback error identity", err)
	}
	var typed *responsesCallbackTestError
	if !errors.As(err, &typed) || typed != sentinel {
		t.Fatalf("err = %v, want callback error type and pointer identity", err)
	}
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("primary=%d fallback=%d", primaryCalls.Load(), fallbackCalls.Load())
	}
}

type responsesCallbackTestError struct {
	message string
}

func (e *responsesCallbackTestError) Error() string { return e.message }

func TestResponsesRetryBufferDoesNotChangeOtherProtocolCallbackOrder(t *testing.T) {
	t.Run("Anthropic Messages", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeAnthropicThinkingTextStream(w, "anthropic-think", "anthropic-text")
		}))
		defer server.Close()
		client := NewClient(config.Config{Provider: "anthropic", APIKey: "key", BaseURL: server.URL})
		var order []string
		_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
			OnThinking: func(delta string) error { order = append(order, "thinking:"+delta); return nil },
			OnText:     func(delta string) error { order = append(order, "text:"+delta); return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(order, ","); got != "thinking:anthropic-think,text:anthropic-text" {
			t.Fatalf("callback order = %q", got)
		}
	})

	t.Run("OpenAI Chat Completions", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"chat-think\"}}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"chat-text\"}}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}))
		defer server.Close()
		client := NewClient(config.Config{Provider: "custom", APIKey: "key", BaseURL: server.URL})
		var order []string
		_, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{
			OnThinking: func(delta string) error { order = append(order, "thinking:"+delta); return nil },
			OnText:     func(delta string) error { order = append(order, "text:"+delta); return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(order, ","); got != "thinking:chat-think,text:chat-text" {
			t.Fatalf("callback order = %q", got)
		}
	})
}

func responsesClientWithAnthropicFallback(primaryURL, fallbackURL string) *Client {
	return NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "key", BaseURL: primaryURL + "/v1",
		FallbackProviders: []config.ProviderConfig{{
			Name: "anthropic-backup", Type: "anthropic", BaseURL: fallbackURL, APIKey: "fallback-key",
		}},
	})
}

func assertResponsesRetryOutcome(t *testing.T, sink *telemetry.MemorySink, want string) {
	t.Helper()
	for _, event := range sink.Events() {
		if event.Properties["retry_outcome"] != want {
			continue
		}
		reason, hasReason := event.Properties["retry_reason"]
		if want == responsesRetryOutcomeCommittedOutput && hasReason {
			t.Fatalf("committed-output retry outcome must not claim reasoning-only retry: %+v", event.Properties)
		}
		if want != responsesRetryOutcomeCommittedOutput && reason != responsesRetryReasonReasoningOnlyDecode {
			t.Fatalf("retry outcome properties = %+v", event.Properties)
		}
		for _, forbidden := range []string{"reasoning", "tool_arguments", "api_key", "authorization"} {
			if _, ok := event.Properties[forbidden]; ok {
				t.Fatalf("retry telemetry leaked %q: %+v", forbidden, event.Properties)
			}
		}
		return
	}
	t.Fatalf("retry outcome %q not found in events: %+v", want, sink.Events())
}

func assertResponsesRetryOutcomeLastFailedRequestID(t *testing.T, sink *telemetry.MemorySink, outcome, want string) {
	t.Helper()
	for _, event := range sink.Events() {
		if event.Properties["retry_outcome"] == outcome {
			if got := event.Properties["last_failed_provider_request_id"]; got != want {
				t.Fatalf("outcome %q last_failed_provider_request_id = %#v, want %q; properties=%+v", outcome, got, want, event.Properties)
			}
			if _, ok := event.Properties["provider_request_id"]; ok {
				t.Fatalf("outcome %q must not use provider_request_id for failed request: %+v", outcome, event.Properties)
			}
			if event.Properties["http_status"] != http.StatusOK || event.Properties["response_mime"] != "text/event-stream" {
				t.Fatalf("outcome %q response metadata = %+v", outcome, event.Properties)
			}
			return
		}
	}
	t.Fatalf("retry outcome %q not found", outcome)
}

func basicResponsesRequest() MessagesRequest {
	return MessagesRequest{
		Model: "test", Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}
}

func writeOpenAIResponsesStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	events := []string{
		`{"type":"response.reasoning_summary_text.delta","sequence_number":1,"item_id":"reason_1","output_index":0,"summary_index":0,"delta":"brief reasoning"}`,
		`{"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"{\"ok\":true}"}`,
		`{"type":"response.output_item.added","sequence_number":3,"output_index":2,"item":{"id":"fc_42","type":"function_call","call_id":"call_42","name":"Read","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":4,"item_id":"call_42","output_index":2,"delta":"{\"file_path\":"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":5,"item_id":"call_42","output_index":2,"name":"Read","arguments":"{\"file_path\":\"go.mod\"}"}`,
		`{"type":"response.completed","sequence_number":6,"response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-responses-test","output":[{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"encrypted-reasoning","status":"completed"},{"id":"fc_42","type":"function_call","call_id":"call_42","name":"Read","arguments":"{\"file_path\":\"go.mod\"}","status":"completed"}],"usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":4},"output_tokens":8,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":20},"service_tier":"default"}}`,
	}
	for _, event := range events {
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", event)
	}
}

func writeOpenAIResponsesTextStream(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":%q}\n\n", text)
	_, _ = fmt.Fprint(w, `data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp_1","status":"completed","model":"test","output":[],"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`+"\n\n")
}

func writeOpenAIResponsesTruncatedStream(w http.ResponseWriter, withText bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
	if withText {
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"partial"}`+"\n\n")
	}
	_, _ = fmt.Fprint(w, `data: {"type":`+"\n\n")
}

func writeOpenAIResponsesTruncatedReasoningStream(w http.ResponseWriter, reasoning string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"sequence_number\":1,\"item_id\":\"reason_1\",\"output_index\":0,\"summary_index\":0,\"delta\":%q}\n\n", reasoning)
	_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_summary_text.delta","sequence_number":2,"delta":"truncated"`+"\n\n")
}

func writeOpenAIResponsesReasoningTextStream(w http.ResponseWriter, reasoning, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_success","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"sequence_number\":1,\"item_id\":\"reason_1\",\"output_index\":0,\"summary_index\":0,\"delta\":%q}\n\n", reasoning)
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":2,\"item_id\":\"msg_1\",\"output_index\":1,\"content_index\":0,\"delta\":%q}\n\n", text)
	_, _ = fmt.Fprint(w, `data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_success","status":"completed","model":"test","output":[],"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`+"\n\n")
}

func writeOpenAIResponsesReasoningTextReasoningStream(w http.ResponseWriter, before, text, after string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_success","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"sequence_number\":1,\"item_id\":\"reason_1\",\"output_index\":0,\"summary_index\":0,\"delta\":%q}\n\n", before)
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":2,\"item_id\":\"msg_1\",\"output_index\":1,\"content_index\":0,\"delta\":%q}\n\n", text)
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"sequence_number\":3,\"item_id\":\"reason_1\",\"output_index\":0,\"summary_index\":0,\"delta\":%q}\n\n", after)
	_, _ = fmt.Fprint(w, `data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_success","status":"completed","model":"test","output":[],"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`+"\n\n")
}

func writeOpenAIResponsesTruncatedToolItemStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Read","arguments":"","status":"in_progress"}}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":`+"\n\n")
}

func writeOpenAIResponsesTruncatedToolArgumentsStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":1,\"item_id\":\"fc_1\",\"output_index\":0,\"delta\":%q}\n\n", `{"file_path":"`)
	_, _ = fmt.Fprint(w, `data: {"type":`+"\n\n")
}

func writeOpenAIResponsesToolItemFailedStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Read","arguments":"","status":"in_progress"}}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":"response.failed","sequence_number":2,"response":{"id":"resp_1","status":"failed","model":"test","error":{"code":"server_error","message":"upstream failed"},"output":[]}}`+"\n\n")
}

func writeOpenAIResponsesFailedStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":"response.failed","sequence_number":1,"response":{"id":"resp_1","status":"failed","model":"test","error":{"code":"server_error","message":"upstream failed"},"output":[]}}`+"\n\n")
}

func writeOpenAIResponsesReasoningToolMalformedStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"in_progress","model":"test"}}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_summary_text.delta","sequence_number":1,"item_id":"reason_1","output_index":0,"summary_index":0,"delta":"tool reasoning"}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"Read","arguments":"","status":"in_progress"}}`+"\n\n")
	_, _ = fmt.Fprint(w, `data: {"type":`+"\n\n")
}

func writeAnthropicThinkingTextStream(w http.ResponseWriter, thinking, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}",
		fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":%q}}", thinking),
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
		fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}", text),
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}",
	}
	_, _ = fmt.Fprint(w, strings.Join(events, "\n\n")+"\n\n")
}
