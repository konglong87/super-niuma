package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

// shortenOpenAIRetryBackoff 把瞬时故障退避压到毫秒级，否则重试测试要真等几秒。
func shortenOpenAIRetryBackoff(t *testing.T) {
	t.Helper()
	backoff := openAIStreamCreateBackoff
	maxBackoff := openAIStreamCreateMaxBackoff
	openAIStreamCreateBackoff = time.Millisecond
	openAIStreamCreateMaxBackoff = 5 * time.Millisecond
	t.Cleanup(func() {
		openAIStreamCreateBackoff = backoff
		openAIStreamCreateMaxBackoff = maxBackoff
	})
}

func openAICompatibleClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClient(config.Config{
		Provider: "openai-compatible",
		APIKey:   "key",
		BaseURL:  baseURL,
	})
}

func streamOpenAIProbe(t *testing.T, client *Client) (string, error) {
	t.Helper()
	var text string
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "gpt-test",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	return text, err
}

// TestOpenAIStreamCreateRetriesServerError 锁住 AUDIT-P1-08：建流阶段的 5xx 以前完全
// 不重试，单 provider 配置下一次抖动就直接把整轮对话打挂。
func TestOpenAIStreamCreateRetriesServerError(t *testing.T) {
	shortenOpenAIRetryBackoff(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			http.Error(w, `{"error":{"message":"upstream unavailable","type":"server_error"}}`, http.StatusServiceUnavailable)
			return
		}
		writeOpenAITextStream(w, "recovered")
	}))
	defer server.Close()

	text, err := streamOpenAIProbe(t, openAICompatibleClient(t, server.URL))
	if err != nil {
		t.Fatalf("expected 5xx to be retried, got error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (initial + 2 retries)", attempts)
	}
	if text != "recovered" {
		t.Fatalf("text = %q, want recovered", text)
	}
}

// TestOpenAIStreamCreateRetriesNetworkError 覆盖「没有 HTTP 状态码」的网络抖动：
// 服务端在写响应头之前就掐掉连接。
func TestOpenAIStreamCreateRetriesNetworkError(t *testing.T) {
	shortenOpenAIRetryBackoff(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, err := hijacker.Hijack()
				if err == nil {
					_ = conn.Close()
					return
				}
			}
		}
		writeOpenAITextStream(w, "recovered")
	}))
	defer server.Close()

	text, err := streamOpenAIProbe(t, openAICompatibleClient(t, server.URL))
	if err != nil {
		t.Fatalf("expected network error to be retried, got error: %v", err)
	}
	if attempts != 2 || text != "recovered" {
		t.Fatalf("attempts = %d text = %q, want 2 / recovered", attempts, text)
	}
}

// TestOpenAIStreamCreateHonorsRetryAfterHeader 锁住 P1-08 的第二半：退避时长以前只会
// 取固定 65 秒或从错误文案里正则抠数字，完全不读 Retry-After 响应头。这里服务端明确
// 要求等 1 秒，重试必须在这个量级内发生，而不是傻等 65 秒。
func TestOpenAIStreamCreateHonorsRetryAfterHeader(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":{"message":"slow down","type":"rate_limit_error"}}`, http.StatusTooManyRequests)
			return
		}
		writeOpenAITextStream(w, "recovered")
	}))
	defer server.Close()

	start := time.Now()
	text, err := streamOpenAIProbe(t, openAICompatibleClient(t, server.URL))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected 429 with Retry-After to be retried, got error: %v", err)
	}
	if attempts != 2 || text != "recovered" {
		t.Fatalf("attempts = %d text = %q, want 2 / recovered", attempts, text)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("elapsed = %s, want >= ~1s from the Retry-After header", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("elapsed = %s, want the 1s header to win over the %s default", elapsed, defaultOpenAIStreamCreateRateLimitDelay)
	}
}

// TestOpenAIStreamCreateDoesNotRetryClientError 守住边界：明确的 4xx 重试不会变好，
// 重试只会白等并推迟 provider fallback。
func TestOpenAIStreamCreateDoesNotRetryClientError(t *testing.T) {
	shortenOpenAIRetryBackoff(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, `{"error":{"message":"bad request","type":"invalid_request_error"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	if _, err := streamOpenAIProbe(t, openAICompatibleClient(t, server.URL)); err == nil {
		t.Fatal("expected a 400 to surface as an error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (a 400 must not be retried)", attempts)
	}
}

// TestOpenAIStreamCreateDoesNotRetryAfterProviderTimeout 守住和 AUDIT-P0-07 分段超时的
// 边界：守卫主动取消请求说明网关卡住了，该做的是尽快切 provider，不是原地再等一轮。
func TestOpenAIStreamCreateDoesNotRetryAfterProviderTimeout(t *testing.T) {
	shortenOpenAIRetryBackoff(t)
	restore := providerTimeouts
	providerTimeouts = providerHTTPTimeouts{responseHeader: 30 * time.Millisecond}
	t.Cleanup(func() { providerTimeouts = restore })

	// handler 会一直卡住直到测试收尾，读计数时它仍在运行，所以必须用原子计数。
	var attempts atomic.Int32
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		select {
		case <-done:
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer func() {
		close(done)
		server.Close()
	}()

	if _, err := streamOpenAIProbe(t, openAICompatibleClient(t, server.URL)); err == nil {
		t.Fatal("expected the segmented-timeout guard to surface an error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (a provider timeout must not be retried in place)", got)
	}
}

func TestRetryAfterFromHeader(t *testing.T) {
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{name: "absent", header: http.Header{}, want: 0},
		{name: "seconds", header: http.Header{"Retry-After": []string{"7"}}, want: 7 * time.Second},
		{name: "zero seconds", header: http.Header{"Retry-After": []string{"0"}}, want: 0},
		{name: "garbage", header: http.Header{"Retry-After": []string{"soon"}}, want: 0},
		{
			name:   "http date",
			header: http.Header{"Retry-After": []string{"Sun, 26 Jul 2026 10:00:30 GMT"}},
			want:   30 * time.Second,
		},
		{
			name:   "past http date",
			header: http.Header{"Retry-After": []string{"Sun, 26 Jul 2026 09:59:00 GMT"}},
			want:   0,
		},
		{
			// OpenAI 实际下发的是毫秒级的 retry-after-ms，优先级高于秒级的 Retry-After。
			name:   "retry after ms wins",
			header: http.Header{"Retry-After-Ms": []string{"250"}, "Retry-After": []string{"9"}},
			want:   250 * time.Millisecond,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfterFromHeader(tc.header, now); got != tc.want {
				t.Fatalf("retryAfterFromHeader = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestResponsesNonJSONStreamFailureRetainsOneRetry(t *testing.T) {
	restoreBackoff := responsesMidstreamBackoff
	responsesMidstreamBackoff = time.Millisecond
	t.Cleanup(func() { responsesMidstreamBackoff = restoreBackoff })

	backend := &scriptedResponsesRetryBackend{firstErr: &openAIResponsesStreamError{
		err:         io.ErrClosedPipe,
		failureKind: responsesStreamFailureKind,
		retryable:   true,
	}}
	client := NewClient(config.Config{
		Provider: "custom", ProviderProtocol: config.ProviderProtocolOpenAIResponses,
		APIKey: "key", BaseURL: "http://responses.invalid",
	})
	client.providers[0].backend = backend
	result, err := client.StreamMessages(context.Background(), basicResponsesRequest(), StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if backend.attempts != 2 {
		t.Fatalf("orchestration attempts = %d, want 2", backend.attempts)
	}
	if got := visibleText(result.Message.Content); got != "recovered" {
		t.Fatalf("result text = %q, want recovered", got)
	}
}

func TestResponsesMidstreamBackoffIsExponentialAndCapped(t *testing.T) {
	restoreBase, restoreMax := responsesMidstreamBackoff, responsesMidstreamMaxBackoff
	responsesMidstreamBackoff, responsesMidstreamMaxBackoff = 300*time.Millisecond, 5*time.Second
	t.Cleanup(func() {
		responsesMidstreamBackoff, responsesMidstreamMaxBackoff = restoreBase, restoreMax
	})
	want := []time.Duration{300 * time.Millisecond, 600 * time.Millisecond, 1200 * time.Millisecond, 2400 * time.Millisecond, 4800 * time.Millisecond, 5 * time.Second}
	for attempt, expected := range want {
		if got := responsesMidstreamBackoffDelay(attempt); got != expected {
			t.Fatalf("attempt %d delay = %s, want %s", attempt+1, got, expected)
		}
	}
}

type scriptedResponsesRetryBackend struct {
	attempts int
	firstErr error
}

func (*scriptedResponsesRetryBackend) Protocol() config.ProviderProtocol {
	return config.ProviderProtocolOpenAIResponses
}

func (*scriptedResponsesRetryBackend) Capabilities() providerCapabilities {
	return providerCapabilities{}
}

func (b *scriptedResponsesRetryBackend) StreamMessages(context.Context, MessagesRequest, StreamCallbacks) (*StreamResult, error) {
	b.attempts++
	if b.attempts == 1 {
		return nil, b.firstErr
	}
	return &StreamResult{Message: MessageParam{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "recovered"}}}}, nil
}
