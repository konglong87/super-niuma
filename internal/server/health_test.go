package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func getJSON(t *testing.T, handler http.Handler, path string, decorate func(*http.Request)) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if decorate != nil {
		decorate(req)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// 错误路径（401 等）走的是 http.Error 的纯文本，解不出 JSON 就交回空 map ——
	// 调用方本来就只在成功路径上看 payload。
	payload := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec.Code, payload
}

// AUDIT-P1-22：k8s probe 与 LB 健康检查不该持有业务凭证。/livez 与 /readyz 必须
// 在配了 token 的 server 上也能匿名访问。修复前只有一个需要 Bearer token 的 /health。
func TestProbesDoNotRequireAuthToken(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token", Workspace: "/tmp/work"}, nil)

	for _, path := range []string{"/livez", "/readyz"} {
		status, payload := getJSON(t, handler, path, nil)
		if status != http.StatusOK {
			t.Fatalf("%s without a token: status=%d payload=%v", path, status, payload)
		}
		if payload["status"] != "ok" {
			t.Fatalf("%s payload=%v, want status=ok", path, payload)
		}
	}

	// 对照：/health 仍然要 token，且仍然吐 workspace —— 行为一字未改。
	status, _ := getJSON(t, handler, "/health", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("/health without a token: status=%d, want 401", status)
	}
	status, payload := getJSON(t, handler, "/health", func(r *http.Request) {
		r.Header.Set("authorization", "Bearer token")
	})
	if status != http.StatusOK || payload["workspace"] != "/tmp/work" {
		t.Fatalf("/health with a token: status=%d payload=%v", status, payload)
	}
}

// AUDIT-P1-22：liveness 与 readiness 必须行为不同 —— 依赖挂了，进程还活着，
// 但不该再收流量。修复前 /health 对两者一视同仁，且根本不探依赖。
func TestReadinessFailsWhileLivenessStaysUpWhenDependencyIsDown(t *testing.T) {
	handler := NewHandler(Options{
		AuthToken: "token",
		ReadinessProbes: []ReadinessProbe{
			{Name: "mysql", Check: func(context.Context) error { return nil }},
			{Name: "quota_redis", Check: func(context.Context) error { return errors.New("dial tcp 10.0.0.5:6379: connect: connection refused") }},
		},
	}, nil)

	if status, _ := getJSON(t, handler, "/livez", nil); status != http.StatusOK {
		t.Fatalf("/livez must stay 200 while a dependency is down, got %d", status)
	}

	status, payload := getJSON(t, handler, "/readyz", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with a dead dependency: status=%d payload=%v, want 503", status, payload)
	}
	checks, _ := payload["checks"].(map[string]any)
	if checks["mysql"] != "ok" || checks["quota_redis"] != "unavailable" {
		t.Fatalf("/readyz checks=%v; want mysql=ok quota_redis=unavailable", checks)
	}
	// /readyz 不鉴权，所以不能把底层错误原文吐给匿名调用者。
	if body, _ := json.Marshal(payload); strings.Contains(string(body), "10.0.0.5") || strings.Contains(string(body), "connection refused") {
		t.Fatalf("/readyz leaked dependency error detail to an unauthenticated caller: %s", body)
	}
}

// 探活必须比 k8s 的 probe timeout 先返回，否则运维看到的是超时而不是「依赖不健康」。
func TestReadinessProbeTimesOutInsteadOfHanging(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)

	probes := []ReadinessProbe{{Name: "stuck", Check: func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-blocked:
			return nil
		}
	}}}

	start := time.Now()
	checks, ready := runReadinessProbes(context.Background(), probes)
	elapsed := time.Since(start)

	if ready || checks["stuck"] != "unavailable" {
		t.Fatalf("a hung probe must count as not ready: checks=%v ready=%v", checks, ready)
	}
	if elapsed > readinessProbeTimeout+2*time.Second {
		t.Fatalf("probe took %v; it must give up at %v", elapsed, readinessProbeTimeout)
	}
}
