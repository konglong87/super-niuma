package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

const (
	responsesLiveEndpointEnv = "NIUMA_RESPONSES_LIVE_ENDPOINT"
	responsesLiveAPIKeyEnv   = "NIUMA_RESPONSES_LIVE_API_KEY"
	responsesLiveModelEnv    = "NIUMA_RESPONSES_LIVE_MODEL"
)

func TestOpenAIResponsesLiveContract(t *testing.T) {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv(responsesLiveEndpointEnv)), "/")
	apiKey := strings.TrimSpace(os.Getenv(responsesLiveAPIKeyEnv))
	model := strings.TrimSpace(os.Getenv(responsesLiveModelEnv))
	if endpoint == "" || apiKey == "" || model == "" {
		t.Skipf("set %s, %s, and %s to run the live Responses contract", responsesLiveEndpointEnv, responsesLiveAPIKeyEnv, responsesLiveModelEnv)
	}

	client := newResponsesLiveClient(endpoint, apiKey)
	baseMessages := []MessageParam{{
		Role: "user",
		Content: []ContentBlock{{
			Type: "text",
			Text: "Reply with exactly LIVE_OK and no other text.",
		}},
	}}

	t.Run("text_reasoning_and_usage", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var streamedText, streamedThinking strings.Builder
		result, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024, Messages: baseMessages,
			Thinking: &ThinkingConfig{Type: "enabled", Effort: "low"},
		}, StreamCallbacks{
			OnText:     func(delta string) error { _, _ = streamedText.WriteString(delta); return nil },
			OnThinking: func(delta string) error { _, _ = streamedThinking.WriteString(delta); return nil },
		})
		if err != nil {
			t.Fatalf("live text request failed: %v", err)
		}
		if result == nil || strings.TrimSpace(streamedText.String()) == "" {
			t.Fatalf("live text response was empty: result_present=%t", result != nil)
		}
		if result.Usage.InputTokens <= 0 || result.Usage.OutputTokens <= 0 {
			t.Fatalf("live usage missing: input=%d output=%d", result.Usage.InputTokens, result.Usage.OutputTokens)
		}
		t.Logf("text=true reasoning_summary=%t opaque_continuation=%t usage_input=%d usage_output=%d usage_reasoning=%d",
			streamedThinking.Len() > 0, hasContinuation(result.Message.Content), result.Usage.InputTokens,
			result.Usage.OutputTokens, result.Usage.ReasoningOutputTokens)
	})

	t.Run("function_call_and_stateless_continuation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		messages := []MessageParam{{Role: "user", Content: []ContentBlock{{
			Type: "text", Text: "Call live_echo exactly once with value set to contract-ok. Do not answer before calling the tool.",
		}}}}
		tool := ToolDefinition{
			Name: "live_echo", Description: "Return the supplied value.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		}
		first, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024, Messages: messages, Tools: []ToolDefinition{tool},
			Thinking: &ThinkingConfig{Type: "enabled", Effort: "low"},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live function request failed: %v", err)
		}
		calls := toolCalls(first)
		if len(calls) != 1 || calls[0].Name != tool.Name || calls[0].ID == "" {
			if len(calls) == 2 {
				t.Logf("duplicate_diagnostic same_name=%t same_id=%t same_input=%t id_lengths=%d/%d input_lengths=%d/%d",
					calls[0].Name == calls[1].Name, calls[0].ID == calls[1].ID, string(calls[0].Input) == string(calls[1].Input),
					len(calls[0].ID), len(calls[1].ID), len(calls[0].Input), len(calls[1].Input))
			}
			t.Fatalf("expected one live_echo call: count=%d", len(calls))
		}
		messages = append(messages,
			MessageParam{Role: "assistant", Content: first.Message.Content},
			MessageParam{Role: "user", Content: []ContentBlock{{
				Type: "tool_result", ToolUseID: calls[0].ID, Content: `{"ok":true,"value":"contract-ok"}`,
			}}},
		)
		second, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024, Messages: messages, Tools: []ToolDefinition{tool},
			Thinking: &ThinkingConfig{Type: "enabled", Effort: "low"},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live function continuation failed: %v", err)
		}
		if strings.TrimSpace(visibleText(second.Message.Content)) == "" {
			t.Fatal("live function continuation returned no visible text")
		}
		t.Logf("function_call=true continuation_replayed=%t usage_input=%d usage_output=%d",
			hasContinuation(first.Message.Content), second.Usage.InputTokens, second.Usage.OutputTokens)
	})

	t.Run("parallel_function_calls", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		tools := []ToolDefinition{
			{Name: "live_alpha", Description: "Record the alpha value.", InputSchema: liveValueSchema()},
			{Name: "live_beta", Description: "Record the beta value.", InputSchema: liveValueSchema()},
		}
		result, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024,
			Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{
				Type: "text", Text: "Call both live_alpha with value alpha and live_beta with value beta in this turn. Do not answer without both calls.",
			}}}},
			Tools: tools, Thinking: &ThinkingConfig{Type: "enabled", Effort: "low"},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live parallel function request failed: %v", err)
		}
		calls := toolCalls(result)
		seen := make(map[string]bool, len(calls))
		for _, call := range calls {
			seen[call.Name] = true
		}
		if len(calls) != 2 || !seen["live_alpha"] || !seen["live_beta"] {
			t.Fatalf("expected both live function calls: count=%d alpha=%t beta=%t", len(calls), seen["live_alpha"], seen["live_beta"])
		}
		t.Logf("parallel_function_calls=true count=%d", len(calls))
	})

	t.Run("parallel_function_call_continuation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		tools := []ToolDefinition{
			{Name: "live_read", Description: "Read the requested test resource.", InputSchema: liveValueSchema()},
			{Name: "live_todo", Description: "Record the requested test task.", InputSchema: liveValueSchema()},
		}
		messages := []MessageParam{{Role: "user", Content: []ContentBlock{{
			Type: "text", Text: "Call both live_read with value rules and live_todo with value task in this turn. Do not answer without both calls.",
		}}}}
		first, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024, Messages: messages, Tools: tools,
			Thinking: &ThinkingConfig{Type: "enabled", Effort: "low"},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live parallel continuation setup failed: %v", err)
		}
		calls := toolCalls(first)
		if len(calls) != 2 {
			t.Fatalf("expected two setup calls: count=%d", len(calls))
		}
		messages = append(messages, MessageParam{Role: "assistant", Content: first.Message.Content})
		resultByName := map[string]string{
			"live_read": strings.Repeat("rule line\n", 700),
			"live_todo": "tool execution failed: write path is outside the configured workspace",
		}
		for _, call := range calls {
			messages = append(messages, MessageParam{Role: "user", Content: []ContentBlock{{
				Type: "tool_result", ToolUseID: call.ID, Content: resultByName[call.Name], IsError: call.Name == "live_todo",
			}}})
		}
		second, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024, Messages: messages, Tools: tools,
			Thinking: &ThinkingConfig{Type: "enabled", Effort: "low"},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live parallel function continuation failed: %v", err)
		}
		if strings.TrimSpace(visibleText(second.Message.Content)) == "" {
			t.Fatal("live parallel function continuation returned no visible text")
		}
		t.Logf("parallel_function_continuation=true usage_input=%d usage_output=%d",
			second.Usage.InputTokens, second.Usage.OutputTokens)
	})

	t.Run("consecutive_user_messages", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024,
			Messages: []MessageParam{
				{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Conversation context: the user greeted you."}}},
				{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Runtime context: answer without tools."}}},
				{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Reply with exactly CONSECUTIVE_OK and no other text."}}},
			},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live consecutive user messages failed: %v", err)
		}
		if !strings.Contains(visibleText(result.Message.Content), "CONSECUTIVE_OK") {
			t.Fatalf("live consecutive user response mismatch: %q", visibleText(result.Message.Content))
		}
	})

	t.Run("assistant_text_history", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024,
			Messages: []MessageParam{
				{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Remember the marker ALPHA."}}},
				{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "I will remember ALPHA."}}},
				{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Reply with exactly ASSISTANT_HISTORY_OK and no other text."}}},
			},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live assistant text history failed: %v", err)
		}
		if !strings.Contains(visibleText(result.Message.Content), "ASSISTANT_HISTORY_OK") {
			t.Fatalf("live assistant history response mismatch: %q", visibleText(result.Message.Content))
		}
	})

	t.Run("structured_output", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 1024,
			Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{
				Type: "text", Text: "Return the requested contract status.",
			}}}},
			ResponseFormat: &ResponseFormat{Type: "json_schema", JSONSchema: &ResponseFormatSchema{
				Name: "live_contract", Strict: true,
				Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"},"protocol":{"type":"string"}},"required":["ok","protocol"],"additionalProperties":false}`),
			}},
		}, StreamCallbacks{})
		if err != nil {
			t.Fatalf("live structured output request failed: %v", err)
		}
		var payload struct {
			OK       bool   `json:"ok"`
			Protocol string `json:"protocol"`
		}
		if err := json.Unmarshal([]byte(visibleText(result.Message.Content)), &payload); err != nil {
			t.Fatalf("live structured output was not valid JSON: %v", err)
		}
		if !payload.OK || payload.Protocol == "" {
			t.Fatalf("live structured output failed schema intent: ok=%t protocol_present=%t", payload.OK, payload.Protocol != "")
		}
		t.Logf("structured_output=true usage_input=%d usage_output=%d", result.Usage.InputTokens, result.Usage.OutputTokens)
	})

	t.Run("invalid_model", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		result, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model + "-invalid-live-contract", MaxTokens: 64, Messages: baseMessages,
		}, StreamCallbacks{})
		if err != nil {
			t.Log("invalid_model_rejected=true")
			return
		}
		if result == nil || strings.TrimSpace(visibleText(result.Message.Content)) == "" {
			t.Fatal("invalid model was accepted but returned no usable response")
		}
		// Compatible gateways may intentionally route arbitrary model aliases.
		// This is a provider capability observation, not a protocol failure.
		t.Log("invalid_model_rejected=false")
	})

	t.Run("invalid_auth", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		badClient := newResponsesLiveClient(endpoint, "invalid-live-contract-key")
		_, err := badClient.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 64, Messages: baseMessages,
		}, StreamCallbacks{})
		if err == nil {
			t.Fatal("invalid auth unexpectedly succeeded")
		}
		t.Log("invalid_auth_rejected=true")
	})

	t.Run("caller_cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.StreamMessages(ctx, MessagesRequest{
			Model: model, MaxTokens: 64, Messages: baseMessages,
		}, StreamCallbacks{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request error = %v", err)
		}
		t.Log("caller_cancellation=true")
	})
}

func newResponsesLiveClient(endpoint, apiKey string) *Client {
	store := false
	return NewClient(config.Config{
		Provider:         "custom",
		ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		BaseURL:          endpoint,
		APIKey:           apiKey,
		Responses: &config.ResponsesProviderSettings{
			StateMode: config.ResponsesStateModeStateless,
			Store:     &store,
		},
	})
}

func liveValueSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
}

func toolCalls(result *StreamResult) []ContentBlock {
	if result == nil {
		return nil
	}
	var calls []ContentBlock
	for _, block := range result.Message.Content {
		if block.Type == "tool_use" {
			calls = append(calls, block)
		}
	}
	return calls
}

func visibleText(content []ContentBlock) string {
	var text strings.Builder
	for _, block := range content {
		if block.Type == "text" {
			_, _ = text.WriteString(block.Text)
		}
	}
	return text.String()
}

func hasContinuation(content []ContentBlock) bool {
	for _, block := range content {
		if block.Type == responsesContinuationBlockType && block.Continuation != nil {
			return true
		}
	}
	return false
}
