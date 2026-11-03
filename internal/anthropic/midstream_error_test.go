package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

// 这一组测试盯的是 AUDIT-P1-10：仓里原来的两个「SSE 健壮性测试」验证的是 parseSSE，
// 而生产 Anthropic 路径走 SDK 的 Messages.NewStreaming、OpenAI 路径走 go-openai，
// mid-stream error event 在生产路径上零覆盖。

// TestAnthropicMidStreamErrorEventSurfacesWithPartialText 覆盖生产 Anthropic 路径：
// 流中途来 error 事件时，错误必须冒出来，并且已经吐给用户的文本要保留在 partial 里。
func TestAnthropicMidStreamErrorEventSurfacesWithPartialText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAnthropicTextThenErrorEventStream(w, "partial")
	}))
	defer server.Close()

	client := NewClient(config.Config{APIKey: "key", BaseURL: server.URL})
	var text string
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err == nil {
		t.Fatal("expected the mid-stream error event to surface as an error")
	}
	if text != "partial" {
		t.Fatalf("streamed text = %q, want partial", text)
	}
	if !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("error = %v, want it to mention the overloaded_error from the stream", err)
	}
	var partial *PartialStreamError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %T (%v), want a *PartialStreamError so the caller keeps the partial turn", err, err)
	}
	if partial.Partial == nil || len(partial.Partial.Message.Content) == 0 {
		t.Fatalf("partial = %+v, want the already-streamed text preserved", partial.Partial)
	}
	if partial.Partial.Message.Content[0].Text != "partial" {
		t.Fatalf("partial content = %+v", partial.Partial.Message.Content)
	}
}

// TestAnthropicMidStreamErrorEventBeforeAnyTextFallsBack 覆盖同一个事件的另一半语义：
// 还没吐任何文本就 overloaded 时，应该切到 fallback provider 而不是把错误返给用户。
func TestAnthropicMidStreamErrorEventBeforeAnyTextFallsBack(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAnthropicErrorEventStream(w)
	}))
	defer primary.Close()
	fallbackAttempts := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackAttempts++
		writeAnthropicTextStream(w, "recovered")
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		APIKey:  "key",
		BaseURL: primary.URL,
		FallbackProviders: []config.ProviderConfig{{
			Name:    "fallback",
			Type:    "anthropic",
			BaseURL: fallback.URL,
			APIKey:  "key",
		}},
	})
	var text string
	if _, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if fallbackAttempts != 1 || text != "recovered" {
		t.Fatalf("fallbackAttempts = %d text = %q, want 1 / recovered", fallbackAttempts, text)
	}
}

// TestOpenAIMidStreamErrorInsideChunkSurfaces 覆盖 P1-10 点出的静默失败：很多
// OpenAI-compatible 网关把错误塞在一个正常形状的 chunk 里（error 不是首个 key，所以
// go-openai 的 `data: {"error":` 前缀检测抓不到），而读循环只看 Choices/Usage，
// 结果是静默产出一个空响应。
func TestOpenAIMidStreamErrorInsideChunkSurfaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
			``,
			`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[],"error":{"message":"upstream model exploded","type":"server_error","code":"internal_error"}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")))
	}))
	defer server.Close()

	var text string
	_, err := openAICompatibleClient(t, server.URL).StreamMessages(context.Background(), MessagesRequest{
		Model:    "gpt-test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err == nil {
		t.Fatal("expected the chunk-embedded error to surface instead of a silent empty response")
	}
	if !strings.Contains(err.Error(), "upstream model exploded") {
		t.Fatalf("error = %v, want it to carry the gateway's message", err)
	}
	if text != "partial" {
		t.Fatalf("streamed text = %q, want partial", text)
	}
	var partial *PartialStreamError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %T (%v), want a *PartialStreamError", err, err)
	}
	if partial.Partial == nil || len(partial.Partial.Message.Content) == 0 || partial.Partial.Message.Content[0].Text != "partial" {
		t.Fatalf("partial = %+v, want the already-streamed text preserved", partial.Partial)
	}
}

// TestOpenAIMidStreamStandaloneErrorDataSurfaces 锁住相邻的那半边行为：错误单独作为
// 一行 `data: {"error":...}` 下发时 go-openai 自己认得，别在改读循环时把它弄丢。
func TestOpenAIMidStreamStandaloneErrorDataSurfaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
			``,
			`data: {"error":{"message":"quota drained","type":"insufficient_quota"}}`,
			``,
		}, "\n")))
	}))
	defer server.Close()

	var text string
	_, err := openAICompatibleClient(t, server.URL).StreamMessages(context.Background(), MessagesRequest{
		Model:    "gpt-test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	if err == nil {
		t.Fatal("expected the standalone error data line to surface as an error")
	}
	if !strings.Contains(err.Error(), "quota drained") {
		t.Fatalf("error = %v, want it to carry the gateway's message", err)
	}
	if text != "partial" {
		t.Fatalf("streamed text = %q, want partial", text)
	}
}

// writeAnthropicTextThenErrorEventStream 先正常吐一段文本，再下发一个真实的 SSE
// error 事件（不是截断的 JSON —— 那条路径已经有测试了）。
func writeAnthropicTextThenErrorEventStream(w http.ResponseWriter, text string) {
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
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		``,
		// SSE 事件靠空行分发，最后一个事件也必须自带终止空行，否则解码器根本看不到它。
		``,
	}, "\n")))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeAnthropicErrorEventStream(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/event-stream")
	_, _ = w.Write([]byte(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
		``,
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		``,
		// SSE 事件靠空行分发，最后一个事件也必须自带终止空行，否则解码器根本看不到它。
		``,
	}, "\n")))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
