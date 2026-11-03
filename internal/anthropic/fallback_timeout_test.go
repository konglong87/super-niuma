package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

// stalledGatewayError 复现「网关收下请求后不返回响应头」，返回守卫真正产出的错误。
func stalledGatewayError(t *testing.T, callerCtx context.Context) error {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := newHTTPTraceClientWithTimeouts(nil, providerHTTPTimeouts{
		responseHeader: 150 * time.Millisecond,
		streamIdle:     10 * time.Second,
		responseBody:   10 * time.Second,
	})
	req, err := http.NewRequestWithContext(callerCtx, http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected the response-header guard to fire")
	}
	return err
}

// 守卫是靠 cancel 派生 context 实现超时的，所以它的错误链里必然含 context.Canceled。
// 这一点不能因为加了标记就消失：上层还有代码靠它判断。
func TestProviderTimeoutErrorKeepsUnderlyingCancellation(t *testing.T) {
	err := stalledGatewayError(t, context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the underlying context.Canceled to stay visible", err)
	}
	if !strings.Contains(err.Error(), "provider sent no response headers after 150ms") {
		t.Fatalf("err = %v, want the response-header phase and timeout in the message", err)
	}
}

// provider 卡死必须能切到备用 provider。守卫用 cancel 实现超时，若只按
// context.Canceled 判断就会误判成用户取消，把整条 fallback 链跳过。
func TestStalledProviderStillAllowsFallback(t *testing.T) {
	callerCtx := context.Background() // 调用方 context 健康，没人取消
	err := stalledGatewayError(t, callerCtx)
	if !canFallbackAfterError(callerCtx, err) {
		t.Fatalf("err = %v, want fallback allowed so a stalled gateway fails over", err)
	}
}

// 端到端：主 provider 收下请求后不返回响应头，请求必须由备用 provider 接住 ——
// 这才是用户真正感知到的行为（网关卡死时自动切走，而不是把错误报到脸上）。
func TestStalledProviderFailsOverToNextProvider(t *testing.T) {
	defer func(saved providerHTTPTimeouts) { providerTimeouts = saved }(providerTimeouts)
	providerTimeouts = providerHTTPTimeouts{
		responseHeader: 200 * time.Millisecond,
		streamIdle:     5 * time.Second,
		responseBody:   5 * time.Second,
	}

	release := make(chan struct{})
	primary := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		primary.Close()
	})
	fallbackAttempts := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	if err != nil {
		t.Fatalf("stalled primary should have failed over, got error: %v", err)
	}
	if fallbackAttempts == 0 {
		t.Fatal("fallback provider was never tried")
	}
	if text != "fallback-ok" {
		t.Fatalf("text = %q, want the fallback provider's answer", text)
	}
}

// 反面：用户按 ESC 取消时不能触发 fallback，否则一次取消会把所有备用 provider 都打一遍。
func TestCallerCancellationBlocksFallback(t *testing.T) {
	callerCtx, cancel := context.WithCancel(context.Background())
	err := stalledGatewayError(t, callerCtx)
	cancel()
	if canFallbackAfterError(callerCtx, err) {
		t.Fatalf("err = %v, want fallback declined once the caller cancelled", err)
	}
}
