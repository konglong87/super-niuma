package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

type capturedSystemRequest struct {
	System []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"system"`
}

func (c capturedSystemRequest) systemText() string {
	parts := make([]string, 0, len(c.System))
	for _, block := range c.System {
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n\n")
}

func captureAnthropicSystem(t *testing.T, req MessagesRequest) capturedSystemRequest {
	t.Helper()
	var captured capturedSystemRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		writeAnthropicTextStream(w, "ok")
	}))
	defer server.Close()

	client := NewClient(config.Config{APIKey: "key", BaseURL: server.URL})
	if _, err := client.StreamMessages(context.Background(), req, StreamCallbacks{}); err != nil {
		t.Fatal(err)
	}
	return captured
}

// TestAnthropicPathInjectsResponseFormatInstruction 锁住 AUDIT-P1-09：Anthropic 的
// Messages API 没有 response_format 字段，以前这个请求字段在 Anthropic 路径上被完全
// 静默丢弃 —— 只有 server 层有 prompt 兜底，CLI / 子代理路径什么都没有。唯一的落地
// 方式是把约束写进 system prompt，且必须在 provider 边界上做，才对所有调用方生效。
func TestAnthropicPathInjectsResponseFormatInstruction(t *testing.T) {
	captured := captureAnthropicSystem(t, MessagesRequest{
		Model:        "test",
		MaxTokens:    1024,
		SystemBlocks: []SystemBlock{{Type: "text", Text: "You are a helpful assistant."}},
		ResponseFormat: &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &ResponseFormatSchema{
				Name:   "result",
				Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
			},
		},
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	})

	system := captured.systemText()
	if !strings.Contains(system, "You are a helpful assistant.") {
		t.Fatalf("system lost the caller's own prompt: %q", system)
	}
	if !strings.Contains(system, responseFormatInstructionMarker) {
		t.Fatalf("system did not pick up the response_format instruction: %q", system)
	}
	if !strings.Contains(system, `"answer"`) {
		t.Fatalf("system did not carry the JSON schema: %q", system)
	}
}

func TestAnthropicPathInjectsJSONObjectResponseFormatInstruction(t *testing.T) {
	captured := captureAnthropicSystem(t, MessagesRequest{
		Model:          "test",
		MaxTokens:      1024,
		SystemBlocks:   []SystemBlock{{Type: "text", Text: "base"}},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
		Messages:       []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if !strings.Contains(captured.systemText(), responseFormatInstructionMarker) {
		t.Fatalf("system = %q, want the json_object instruction", captured.systemText())
	}
}

// TestAnthropicPathDoesNotDuplicateResponseFormatInstruction 守住边界：server 层和
// CLI 的 --json-schema 已经各自往 system prompt 里塞过同样的约束，provider 边界上再
// 塞一遍会让指令重复出现。
func TestAnthropicPathDoesNotDuplicateResponseFormatInstruction(t *testing.T) {
	captured := captureAnthropicSystem(t, MessagesRequest{
		Model:     "test",
		MaxTokens: 1024,
		SystemBlocks: []SystemBlock{
			{Type: "text", Text: "base"},
			{Type: "text", Text: "Respond with a single valid JSON object. " + responseFormatInstructionMarker},
		},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
		Messages:       []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if got := strings.Count(captured.systemText(), responseFormatInstructionMarker); got != 1 {
		t.Fatalf("instruction marker appears %d times, want 1: %q", got, captured.systemText())
	}
}

// TestAnthropicPathWithoutResponseFormatLeavesSystemAlone 确认没设 response_format 时
// 什么都不加，避免给每一次普通请求都塞无关指令。
func TestAnthropicPathWithoutResponseFormatLeavesSystemAlone(t *testing.T) {
	captured := captureAnthropicSystem(t, MessagesRequest{
		Model:        "test",
		MaxTokens:    1024,
		SystemBlocks: []SystemBlock{{Type: "text", Text: "base"}},
		Messages:     []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if captured.systemText() != "base" {
		t.Fatalf("system = %q, want just the caller's prompt", captured.systemText())
	}
}

// TestOpenAIRequestMapsThinkingToReasoningEffort 锁住 P1-09 的第三条：OpenAI 路径以前
// 完全不映射 thinking，调用方开了推理预算也是白开。
func TestOpenAIRequestMapsThinkingToReasoningEffort(t *testing.T) {
	cases := []struct {
		name     string
		thinking *ThinkingConfig
		want     string
	}{
		{name: "nil leaves it unset", thinking: nil, want: ""},
		{name: "disabled leaves it unset", thinking: &ThinkingConfig{Type: "disabled"}, want: ""},
		{name: "low", thinking: &ThinkingConfig{Type: "enabled", Effort: "low", BudgetTokens: 1024}, want: "low"},
		{name: "medium", thinking: &ThinkingConfig{Type: "enabled", Effort: "medium", BudgetTokens: 2048}, want: "medium"},
		{name: "high", thinking: &ThinkingConfig{Type: "enabled", Effort: "high", BudgetTokens: 4096}, want: "high"},
		{name: "max clamps to high", thinking: &ThinkingConfig{Type: "enabled", Effort: "max", BudgetTokens: 8192}, want: "high"},
		{
			// effort 是裸 token 数时按预算分档，和 thinkingBudgetTokens 的档位对齐。
			name:     "numeric effort falls back to the budget",
			thinking: &ThinkingConfig{Type: "enabled", Effort: "6000", BudgetTokens: 6000},
			want:     "high",
		},
		{
			name:     "small numeric budget maps low",
			thinking: &ThinkingConfig{Type: "enabled", Effort: "1024", BudgetTokens: 1024},
			want:     "low",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, err := openAIChatCompletionRequest(MessagesRequest{
				Model:     "gpt-test",
				MaxTokens: 1024,
				Thinking:  tc.thinking,
				Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if params.ReasoningEffort != tc.want {
				t.Fatalf("ReasoningEffort = %q, want %q", params.ReasoningEffort, tc.want)
			}
		})
	}
}

// TestOpenAIRequestSendsReasoningEffortOnTheWire 确认映射真的进了请求体，而不是只在
// 结构体上设了个字段。
func TestOpenAIRequestSendsReasoningEffortOnTheWire(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		writeOpenAITextStream(w, "ok")
	}))
	defer server.Close()

	if _, err := openAICompatibleClient(t, server.URL).StreamMessages(context.Background(), MessagesRequest{
		Model:    "gpt-test",
		Thinking: &ThinkingConfig{Type: "enabled", Effort: "high", BudgetTokens: 4096},
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{}); err != nil {
		t.Fatal(err)
	}
	if got := body["reasoning_effort"]; got != "high" {
		t.Fatalf("reasoning_effort = %v, want high (body = %v)", got, body)
	}
}
