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

// capturedMessage 是从真实请求体里解出来的一条 message，用来断言回传给 provider 的
// content block 形状 —— 单测直接看 sdkMessageParam 的返回值看不出 JSON 序列化结果。
type capturedMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
		Data      string `json:"data"`
		ID        string `json:"id"`
		Name      string `json:"name"`
	} `json:"content"`
}

type capturedRequest struct {
	Messages []capturedMessage `json:"messages"`
}

// TestThinkingBlockRoundTripsSignatureToAnthropic 锁住 AUDIT-P1-07：多轮 tool use +
// thinking 时，上一轮的 thinking 块必须原样回传（Anthropic 要求 signature 不可丢），
// 且不能退化成空 text 块（会被 API 以 "text content blocks must be non-empty" 拒掉）。
func TestThinkingBlockRoundTripsSignatureToAnthropic(t *testing.T) {
	var secondRequest capturedRequest
	round := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		if round == 1 {
			writeAnthropicThinkingToolUseStream(w)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read second request body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &secondRequest); err != nil {
			t.Errorf("decode second request body: %v", err)
			return
		}
		writeAnthropicTextStream(w, "done")
	}))
	defer server.Close()

	client := NewClient(config.Config{APIKey: "key", BaseURL: server.URL})
	first, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "test",
		MaxTokens: 1024,
		Thinking:  &ThinkingConfig{Type: "enabled", BudgetTokens: 1024},
		Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(first.Message.Content) != 2 || first.Message.Content[0].Type != "thinking" {
		t.Fatalf("first turn content = %+v", first.Message.Content)
	}
	if first.Message.Content[0].Signature != "sig_abc" {
		t.Fatalf("first turn signature = %q, want sig_abc", first.Message.Content[0].Signature)
	}

	// 第二轮：把上一轮的 assistant 消息原样 append 回去，再补 tool_result —— 这正是
	// internal/query 的 agent loop 在做的事。
	_, err = client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "test",
		MaxTokens: 1024,
		Thinking:  &ThinkingConfig{Type: "enabled", BudgetTokens: 1024},
		Messages: []MessageParam{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
			first.Message,
			{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "toolu_1", Content: "file body"}}},
		},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}

	if len(secondRequest.Messages) != 3 {
		t.Fatalf("second request messages = %d, want 3", len(secondRequest.Messages))
	}
	assistant := secondRequest.Messages[1]
	if assistant.Role != "assistant" || len(assistant.Content) != 2 {
		t.Fatalf("assistant message = %+v", assistant)
	}
	thinking := assistant.Content[0]
	if thinking.Type != "thinking" {
		t.Fatalf("re-sent first block type = %q, want thinking (block = %+v)", thinking.Type, thinking)
	}
	if thinking.Signature != "sig_abc" {
		t.Fatalf("re-sent signature = %q, want sig_abc", thinking.Signature)
	}
	if thinking.Thinking != "let me think" {
		t.Fatalf("re-sent thinking = %q, want %q", thinking.Thinking, "let me think")
	}
	if assistant.Content[1].Type != "tool_use" || assistant.Content[1].ID != "toolu_1" {
		t.Fatalf("re-sent tool_use block = %+v", assistant.Content[1])
	}
	for _, message := range secondRequest.Messages {
		for _, block := range message.Content {
			if block.Type == "text" && block.Text == "" {
				t.Fatalf("re-sent an empty text block in %s message: %+v", message.Role, message.Content)
			}
		}
	}
}

// TestRedactedThinkingBlockRoundTripsToAnthropic 锁住 P1-07 的另一半：被安全系统
// 打码的 thinking 块只有 data 字段，同样必须原样回传，不能丢也不能变成空 text 块。
func TestRedactedThinkingBlockRoundTripsToAnthropic(t *testing.T) {
	var secondRequest capturedRequest
	round := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		if round == 1 {
			writeAnthropicRedactedThinkingStream(w)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read second request body: %v", err)
			return
		}
		if err := json.Unmarshal(body, &secondRequest); err != nil {
			t.Errorf("decode second request body: %v", err)
			return
		}
		writeAnthropicTextStream(w, "done")
	}))
	defer server.Close()

	client := NewClient(config.Config{APIKey: "key", BaseURL: server.URL})
	first, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "test",
		MaxTokens: 1024,
		Messages:  []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(first.Message.Content) != 1 || first.Message.Content[0].Type != "redacted_thinking" {
		t.Fatalf("first turn content = %+v, want one redacted_thinking block", first.Message.Content)
	}
	if first.Message.Content[0].Data != "redacted_payload" {
		t.Fatalf("redacted data = %q, want redacted_payload", first.Message.Content[0].Data)
	}

	_, err = client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "test",
		MaxTokens: 1024,
		Messages: []MessageParam{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
			first.Message,
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "continue"}}},
		},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if len(secondRequest.Messages) != 3 {
		t.Fatalf("second request messages = %d, want 3", len(secondRequest.Messages))
	}
	assistant := secondRequest.Messages[1]
	if len(assistant.Content) != 1 {
		t.Fatalf("assistant content = %+v, want one block", assistant.Content)
	}
	if assistant.Content[0].Type != "redacted_thinking" || assistant.Content[0].Data != "redacted_payload" {
		t.Fatalf("re-sent redacted block = %+v", assistant.Content[0])
	}
}

// TestUnsignedThinkingBlockIsDroppedNotSentAsEmptyText 覆盖 resume 场景：老会话日志
// 里可能存着没有 signature 的 thinking 块。没有 signature 的 thinking 块 Anthropic
// 一定会拒，因此正确处理是丢掉它，而不是回退成会被拒的空 text 块。
func TestUnsignedThinkingBlockIsDroppedNotSentAsEmptyText(t *testing.T) {
	var captured capturedRequest
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
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:     "test",
		MaxTokens: 1024,
		Messages: []MessageParam{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []ContentBlock{
				{Type: "thinking", Thinking: "unsigned reasoning"},
				{Type: "text", Text: "the answer"},
			}},
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "again"}}},
		},
	}, StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	assistant := captured.Messages[1]
	if len(assistant.Content) != 1 {
		t.Fatalf("assistant content = %+v, want the unsigned thinking block dropped", assistant.Content)
	}
	if assistant.Content[0].Type != "text" || assistant.Content[0].Text != "the answer" {
		t.Fatalf("assistant content = %+v", assistant.Content)
	}
}

func writeAnthropicThinkingToolUseStream(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_abc"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"README.md\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")))
}

func writeAnthropicRedactedThinkingStream(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"redacted_payload"}}`,
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
