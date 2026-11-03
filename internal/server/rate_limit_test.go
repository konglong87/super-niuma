package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/quota"
)

func postQuery(t *testing.T, handler http.Handler, remoteAddr string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"prompt":"hi"}`))
	req.RemoteAddr = remoteAddr
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func countingQueryHandler(t *testing.T, opts Options) (http.Handler, *int) {
	t.Helper()
	calls := 0
	handler := NewHandler(opts, func(context.Context, QueryRequest) (query.Result, error) {
		calls++
		return query.Result{Response: "ok"}, nil
	})
	return handler, &calls
}

// AUDIT-P1-27：reserveQueryQuota 在没有 tenant/user header 时整段跳过配额，于是
// 不带 X-Tenant-Key 的调用者此前不受任何限制。这条锁的是「没有配额」不再等于
// 「没有上限」。修复前这个循环会 200 到底。
func TestQueryWithoutTenantHeadersIsRateLimited(t *testing.T) {
	handler, calls := countingQueryHandler(t, Options{Workspace: "/tmp/work", QueryRateLimitPerMinute: 5})

	for i := 1; i <= 5; i++ {
		if status := postQuery(t, handler, "203.0.113.7:5000", nil); status != http.StatusOK {
			t.Fatalf("request %d: status=%d, want 200 while under the limit", i, status)
		}
	}
	if status := postQuery(t, handler, "203.0.113.7:5000", nil); status != http.StatusTooManyRequests {
		t.Fatalf("the 6th tenant-less request: status=%d, want 429", status)
	}
	if *calls != 5 {
		t.Fatalf("query function ran %d times; the rejected request must never reach the model", *calls)
	}
}

// 同样的兜底必须覆盖 /v1/chat/completions —— 两条路径共用 reserveQueryQuota，
// 审计条目点名的也是这两个。
func TestOpenAIChatWithoutTenantHeadersIsRateLimited(t *testing.T) {
	handler := NewHandler(Options{Workspace: "/tmp/work", QueryRateLimitPerMinute: 2}, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "ok"}, nil
	})

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	var last int
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.RemoteAddr = "203.0.113.8:5000"
		req.Header.Set("content-type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("the 3rd tenant-less chat completion: status=%d, want 429", last)
	}
}

// 兜底限流按 TCP 对端地址计数，**不看 X-Forwarded-For**。否则攻击者每次换一个
// 伪造的转发头就能拿回无限额度，这道门等于不存在。
func TestRateLimitKeyIgnoresForwardedForSpoofing(t *testing.T) {
	handler, calls := countingQueryHandler(t, Options{Workspace: "/tmp/work", QueryRateLimitPerMinute: 3})

	var last int
	for i := 0; i < 10; i++ {
		last = postQuery(t, handler, "203.0.113.9:5000", map[string]string{
			"X-Forwarded-For": fmt.Sprintf("10.0.0.%d", i),
		})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("rotating X-Forwarded-For escaped the limiter: last status=%d", last)
	}
	if *calls != 3 {
		t.Fatalf("query function ran %d times; the limit was 3", *calls)
	}
}

// 反方向断言之一：不同来源 IP 之间互不影响，一个刷接口的客户端不能把别人饿死。
func TestRateLimiterIsPerClient(t *testing.T) {
	handler, _ := countingQueryHandler(t, Options{Workspace: "/tmp/work", QueryRateLimitPerMinute: 2})

	for i := 0; i < 3; i++ {
		postQuery(t, handler, "203.0.113.10:5000", nil)
	}
	if status := postQuery(t, handler, "203.0.113.11:5000", nil); status != http.StatusOK {
		t.Fatalf("a second client was punished for the first one's traffic: status=%d", status)
	}
}

// 反方向断言之二：本机 127.0.0.1 自用的既有工作流不能被打断。默认额度（120/min）
// 远高于一个人类用户的实际速率。
func TestLocalWorkflowStaysUnderDefaultRateLimit(t *testing.T) {
	handler, calls := countingQueryHandler(t, Options{Workspace: "/tmp/work"})

	for i := 0; i < defaultQueryRateLimitPerMinute; i++ {
		if status := postQuery(t, handler, "127.0.0.1:5000", nil); status != http.StatusOK {
			t.Fatalf("local request %d was rejected under the default limit: status=%d", i+1, status)
		}
	}
	if *calls != defaultQueryRateLimitPerMinute {
		t.Fatalf("query ran %d times, want %d", *calls, defaultQueryRateLimitPerMinute)
	}
}

// 运维可以显式关掉兜底（负数），此时行为回到修复前 —— 但那是一个明确的选择。
func TestNegativeRateLimitDisablesTheFallback(t *testing.T) {
	handler, _ := countingQueryHandler(t, Options{Workspace: "/tmp/work", QueryRateLimitPerMinute: -1})

	for i := 0; i < 200; i++ {
		if status := postQuery(t, handler, "203.0.113.12:5000", nil); status != http.StatusOK {
			t.Fatalf("request %d rejected while the fallback is disabled: status=%d", i+1, status)
		}
	}
}

// 兜底限流的错误既要报出可读的原因，又必须仍然被识别成「配额超限」——
// isQuotaLimitError 靠它决定 fail-closed，writeQuotaHTTPError 靠它给 429。
func TestRateLimitErrorIsRecognisedAsQuotaLimit(t *testing.T) {
	err := newClientRateLimiter(1, time.Now).allow("client")
	if err != nil {
		t.Fatalf("first call rejected: %v", err)
	}
	err = newClientRateLimiter(0, time.Now).allow("client")
	if err != nil {
		t.Fatalf("a fresh limiter rejected the first call: %v", err)
	}

	limiter := newClientRateLimiter(1, time.Now)
	_ = limiter.allow("client")
	err = limiter.allow("client")
	if err == nil {
		t.Fatal("the over-limit call must fail")
	}
	if !isQuotaLimitError(err) {
		t.Fatalf("%v is not recognised as a quota limit; it would be treated as an infrastructure fault and fail open", err)
	}
	// 这条路径没有 tenant，所以不能照搬 quota.ErrRateLimited 的
	// 「tenant quota qps limit exceeded」—— 那句话只会把排查的人引向租户配置。
	if strings.Contains(err.Error(), quota.ErrRateLimited.Error()) {
		t.Fatalf("message leads with the tenant-quota wording on a tenant-less path: %v", err)
	}
}

// 窗口滚动后额度必须恢复，否则一个客户端被限一次就永远出不来。
func TestRateLimiterWindowResets(t *testing.T) {
	now := time.Date(2026, 7, 26, 10, 0, 30, 0, time.UTC)
	limiter := newClientRateLimiter(2, func() time.Time { return now })

	if err := limiter.allow("client"); err != nil {
		t.Fatalf("first call rejected: %v", err)
	}
	if err := limiter.allow("client"); err != nil {
		t.Fatalf("second call rejected: %v", err)
	}
	if err := limiter.allow("client"); err == nil {
		t.Fatal("third call in the same minute must be rejected")
	}

	now = now.Add(time.Minute)
	if err := limiter.allow("client"); err != nil {
		t.Fatalf("the next minute must start with a fresh budget: %v", err)
	}
}
