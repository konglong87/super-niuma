package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/query"
)

// AUDIT-P1-21：这条锁的是「空 token + 绑 0.0.0.0 = 全站匿名」这个组合根本无法启动。
// 修复前 Run 会照常监听，authorize 对空 token 直接返回 true。
func TestServerRunRefusesNonLoopbackBindWithoutAuthToken(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::", "192.168.1.10", "example.internal"} {
		t.Run(host, func(t *testing.T) {
			// 用已取消的 ctx：校验通过的话 Run 会照常起 scheduler daemon 并监听，
			// 这里要的是「它在那之前就返回错误」，而不是让测试挂住等 shutdown。
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := Run(ctx, Options{Host: host, Port: 0}, nil)
			if err == nil {
				t.Fatalf("Run(host=%q) with an empty auth token started; it must refuse", host)
			}
			for _, want := range []string{"refusing to start", "--auth-token", "--host 127.0.0.1"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error message missing %q, got: %v", want, err)
				}
			}
		})
	}
}

// 反方向断言：本机自用（127.0.0.1，不配 token）是仓库主人每天在跑的路径，
// 绝对不能被上面那道闸门挡住。
func TestServerBindAllowsLoopbackWithoutAuthToken(t *testing.T) {
	for _, host := range []string{"", "127.0.0.1", "localhost", "::1", "[::1]", "127.0.0.2"} {
		if err := validateServerBind(Options{Host: host}); err != nil {
			t.Fatalf("validateServerBind(host=%q) rejected a loopback bind: %v", host, err)
		}
	}
}

// 配了 token 之后，绑哪里都是调用方自己的决定。
func TestServerBindAllowsNonLoopbackWithAuthToken(t *testing.T) {
	if err := validateServerBind(Options{Host: "0.0.0.0", AuthToken: "s3cret"}); err != nil {
		t.Fatalf("validateServerBind rejected a tokened remote bind: %v", err)
	}
	if err := validateServerBind(Options{Host: "0.0.0.0", AuthToken: "   "}); err == nil {
		t.Fatal("a whitespace-only token must not count as configured")
	}
}

// AUDIT-P1-21：/trace/api/* 吐完整会话，只认 Authorization 头。`?token=` 会进
// Nginx access log、浏览器历史和 Referer —— 那里泄漏 token 等于泄漏全量对话。
func TestTraceAPIRejectsQueryStringToken(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)

	for _, path := range []string{
		"/trace/api/sessions?source=local&token=token",
		"/trace/api/sessions/some-session?source=local&token=token",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s accepted a query-string token: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

// 反方向断言：HTML 外壳仍然接受 `?token=`，否则浏览器里根本打不开 trace 页面
// （加载一个链接时没有地方能塞 header）。外壳本身不含任何会话内容。
func TestTraceUIShellStillAcceptsQueryStringToken(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/trace?token=token", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("trace shell rejected ?token=: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "niuma Trace") {
		t.Fatalf("trace shell did not render: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/trace?token=wrong", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("trace shell accepted a wrong token: status=%d", rec.Code)
	}
}

// AUDIT-P1-21：/prompt-dump 吐完整 prompt 正文（system prompt、全部历史消息），
// 除了 token 还必须是本机直连。带转发头的请求即使 RemoteAddr 是回环也要拒绝，
// 否则同机部署的 Nginx 会把整道门代理穿透。
func TestPromptDumpRejectsNonLocalClients(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)

	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
	}{
		{name: "remote client", remoteAddr: "203.0.113.9:41000"},
		{name: "loopback behind proxy", remoteAddr: "127.0.0.1:41000", headers: map[string]string{"X-Forwarded-For": "203.0.113.9"}},
		{name: "loopback behind real-ip proxy", remoteAddr: "127.0.0.1:41000", headers: map[string]string{"X-Real-Ip": "203.0.113.9"}},
	}
	for _, path := range []string{"/prompt-dump", "/prompt-dump/api/records"} {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.RemoteAddr = tc.remoteAddr
				req.Header.Set("authorization", "Bearer token")
				for key, value := range tc.headers {
					req.Header.Set(key, value)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status=%d body=%s; a valid token must not be enough for a non-local client", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// AUDIT-P1-21：即使是本机客户端，/prompt-dump/api/records 也只认 Authorization 头。
func TestPromptDumpAPIRejectsQueryStringToken(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/prompt-dump/api/records?token=token", nil)
	req.RemoteAddr = "127.0.0.1:41000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("prompt dump API accepted a query-string token: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// AUDIT-P1-21：WebUI 的 `?token=` → Cookie 流程保留（浏览器加载 SPA bundle 时没有
// 地方能塞 header），但换来的 Cookie 只能开静态资源。这条锁的是它开不了会话数据。
func TestWebUICookieCannotReachConversationEndpoints(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/webui/?token=token", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("webui did not hand out an auth cookie")
	}
	cookie := cookies[0]
	if cookie.Path != "/webui/" {
		t.Fatalf("auth cookie path=%q; it must stay scoped to the static bundle", cookie.Path)
	}

	// 就算客户端硬把 Cookie 送到会话端点，也必须无效：authorize 根本不读 Cookie。
	for _, path := range []string{"/trace/api/sessions?source=local", "/query", "/sessions"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s accepted the webui cookie: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

// 反方向断言：本机不配 token 时全部端点照常可用 —— 这是 127.0.0.1 自用的默认形态。
func TestLocalNoTokenWorkflowStillWorks(t *testing.T) {
	handler := NewHandler(Options{Workspace: "/tmp/work"}, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "ok"}, nil
	})

	for _, path := range []string{"/health", "/trace", "/trace/api/sessions?source=local", "/prompt-dump"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:41000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("%s blocked the local no-token workflow: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}
