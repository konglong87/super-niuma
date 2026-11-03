package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

// twoProviderClient 起一个必挂的 primary 和一个正常的 fallback，返回 client 与 primary 的
// 计数器。每个用例都做两次 StreamMessages —— 熔断冷却只有跨轮才看得出来。
func twoProviderClient(t *testing.T) (*Client, *atomic.Int32) {
	t.Helper()
	var primaryAttempts atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryAttempts.Add(1)
		http.Error(w, "primary unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(primary.Close)

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAnthropicTextStream(w, "fallback-ok")
	}))
	t.Cleanup(fallback.Close)

	client := NewClient(config.Config{
		SelectedProvider: "primary",
		APIKey:           "primary-key",
		BaseURL:          primary.URL,
		FallbackProviders: []config.ProviderConfig{{
			Name:    "provider1",
			Type:    "anthropic",
			BaseURL: fallback.URL,
			APIKey:  "fallback-key",
		}},
	})
	return client, &primaryAttempts
}

func streamOnce(t *testing.T, client *Client) (string, error) {
	t.Helper()
	var text string
	_, err := client.StreamMessages(context.Background(), MessagesRequest{
		Model:    "primary-model",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{OnText: func(delta string) error {
		text += delta
		return nil
	}})
	return text, err
}

// 这是 AUDIT-P1-08 原文列的那条：fallback 之后下一轮仍然从 primary 重试。
func TestFailedProviderIsSkippedOnTheNextTurn(t *testing.T) {
	client, primaryAttempts := twoProviderClient(t)

	if text, err := streamOnce(t, client); err != nil || text != "fallback-ok" {
		t.Fatalf("first turn: text=%q err=%v", text, err)
	}
	afterFirst := primaryAttempts.Load()
	if afterFirst == 0 {
		t.Fatal("primary was never attempted on the first turn")
	}

	if text, err := streamOnce(t, client); err != nil || text != "fallback-ok" {
		t.Fatalf("second turn: text=%q err=%v", text, err)
	}
	if got := primaryAttempts.Load(); got != afterFirst {
		t.Fatalf("primary attempts grew from %d to %d; the cooled-down provider was retried", afterFirst, got)
	}
}

func TestProviderLeavesCooldownWhenItExpires(t *testing.T) {
	client, primaryAttempts := twoProviderClient(t)
	now := time.Now()
	client.breaker.now = func() time.Time { return now }

	if _, err := streamOnce(t, client); err != nil {
		t.Fatal(err)
	}
	afterFirst := primaryAttempts.Load()

	// 冷却窗口内：跳过。
	now = now.Add(providerCooldown - time.Second)
	if _, err := streamOnce(t, client); err != nil {
		t.Fatal(err)
	}
	if got := primaryAttempts.Load(); got != afterFirst {
		t.Fatalf("primary attempts grew from %d to %d inside the cooldown window", afterFirst, got)
	}

	// 窗口过了：重新试。
	now = now.Add(2 * time.Second)
	if _, err := streamOnce(t, client); err != nil {
		t.Fatal(err)
	}
	if got := primaryAttempts.Load(); got <= afterFirst {
		t.Fatalf("primary attempts stayed at %d after the cooldown expired; want a fresh attempt", got)
	}
}

func TestSuccessfulProviderIsNotCooledDown(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writeAnthropicTextStream(w, "ok")
	}))
	defer server.Close()

	client := NewClient(config.Config{APIKey: "key", BaseURL: server.URL})
	for turn := 1; turn <= 3; turn++ {
		if _, err := streamOnce(t, client); err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// 反方向守卫：只有一个 provider 时冷却不能把它锁死，否则一次抖动就等于整段会话不可用。
func TestSoleProviderIsStillTriedWhileCoolingDown(t *testing.T) {
	// SDK 自己带 WithMaxRetries(2)，所以第一轮里 server 会被打三次；用一个显式开关
	// 划分两轮，而不是数调用次数。
	recovered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !recovered {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		writeAnthropicTextStream(w, "recovered")
	}))
	defer server.Close()

	client := NewClient(config.Config{APIKey: "key", BaseURL: server.URL})
	if _, err := streamOnce(t, client); err == nil {
		t.Fatal("first turn should have failed")
	}
	recovered = true
	text, err := streamOnce(t, client)
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if text != "recovered" {
		t.Fatalf("text = %q, want recovered", text)
	}
}

// 全员冷却时同样不能一次都不试就报错。
func TestAllProvidersCoolingDownAreStillTried(t *testing.T) {
	var primaryAttempts, fallbackAttempts atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryAttempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackAttempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		APIKey:            "primary-key",
		BaseURL:           primary.URL,
		FallbackProviders: []config.ProviderConfig{{Name: "provider1", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-key"}},
	})
	if _, err := streamOnce(t, client); err == nil {
		t.Fatal("first turn should have failed")
	}
	if _, err := streamOnce(t, client); err == nil {
		t.Fatal("second turn should have failed")
	}
	if primaryAttempts.Load() < 2 || fallbackAttempts.Load() < 2 {
		t.Fatalf("primary=%d fallback=%d; both should be retried when every provider is cooling",
			primaryAttempts.Load(), fallbackAttempts.Load())
	}
}

// 调用方取消不是 provider 健康度的信号，不能把 provider 打进冷却。
func TestCallerCancellationDoesNotCoolDownTheProvider(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writeAnthropicTextStream(w, "ok")
	}))
	defer server.Close()

	client := NewClient(config.Config{
		APIKey:            "primary-key",
		BaseURL:           server.URL,
		FallbackProviders: []config.ProviderConfig{{Name: "provider1", Type: "anthropic", BaseURL: server.URL, APIKey: "fallback-key"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.StreamMessages(ctx, MessagesRequest{
		Model:    "primary-model",
		Messages: []MessageParam{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	}, StreamCallbacks{}); err == nil {
		t.Fatal("cancelled request should fail")
	}
	if cooling := client.breaker.cooling(client.providers); len(cooling) != 0 {
		t.Fatalf("cancellation put providers in cooldown: %v", cooling)
	}
}

func TestProviderCooldownDisabledByZeroDuration(t *testing.T) {
	saved := providerCooldown
	defer func() { providerCooldown = saved }()
	providerCooldown = 0

	client, primaryAttempts := twoProviderClient(t)
	if _, err := streamOnce(t, client); err != nil {
		t.Fatal(err)
	}
	afterFirst := primaryAttempts.Load()
	if _, err := streamOnce(t, client); err != nil {
		t.Fatal(err)
	}
	if got := primaryAttempts.Load(); got <= afterFirst {
		t.Fatalf("primary attempts stayed at %d with cooldown disabled", got)
	}
}

// Client 被 batch 子代理并发共享（Task 默认 4 路、最多 16 路），所以熔断状态必须加锁。
func TestProviderBreakerIsConcurrencySafe(t *testing.T) {
	client, _ := twoProviderClient(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := streamOnce(t, client); err != nil {
				t.Errorf("stream: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestCooldownSkipIsReportedInTheAggregatedFailure(t *testing.T) {
	var primaryAttempts atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryAttempts.Add(1)
		http.Error(w, "primary unavailable", http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	var fallbackCalls atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fallbackCalls.Add(1) == 1 {
			writeAnthropicTextStream(w, "ok")
			return
		}
		http.Error(w, "fallback unavailable", http.StatusServiceUnavailable)
	}))
	defer fallback.Close()

	client := NewClient(config.Config{
		SelectedProvider:  "primary",
		APIKey:            "primary-key",
		BaseURL:           primary.URL,
		FallbackProviders: []config.ProviderConfig{{Name: "provider1", Type: "anthropic", BaseURL: fallback.URL, APIKey: "fallback-key"}},
	})
	// 第一轮：primary 挂、fallback 成功 → 只有 primary 进冷却。第二轮 fallback 也挂，
	// 于是失败列表里一条是真失败、一条是冷却跳过 —— 后者必须在文案里说清楚，不然
	// 用户看到的是"只试了 fallback"却不知道 primary 为什么没试。
	if _, err := streamOnce(t, client); err != nil {
		t.Fatalf("first turn should succeed via fallback: %v", err)
	}
	afterFirst := primaryAttempts.Load()

	_, err := streamOnce(t, client)
	if err == nil {
		t.Fatal("second turn should fail")
	}
	if got := primaryAttempts.Load(); got != afterFirst {
		t.Fatalf("primary attempts grew from %d to %d; it should have been skipped", afterFirst, got)
	}
	if !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("aggregated failure does not mention the cooldown skip: %v", err)
	}
}
