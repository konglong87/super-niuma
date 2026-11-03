package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestServerGoldenOpenAIChatCompletion(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token", Workspace: "/workspace"}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		if req.Prompt != "Hello" {
			t.Fatalf("prompt = %q", req.Prompt)
		}
		return query.Result{
			Response:   "Hi there",
			Model:      "claude-test",
			StopReason: "end_turn",
			Usage:      query.Usage{InputTokens: 4, OutputTokens: 2},
		}, nil
	})
	rec := serveGoldenRequest(handler, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-test","messages":[{"role":"user","content":"Hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertServerGolden(t, "openai_chat_completion.json", normalizeOpenAIChatJSON(t, rec.Body.Bytes()))
}

func TestServerGoldenOpenAIChatCompletionToolCalls(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token", Workspace: "/workspace"}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		return query.Result{
			Model:      "claude-test",
			StopReason: "tool_use",
			ToolCalls: []query.ToolTrace{{
				ID:    "toolu_1",
				Name:  "Read",
				Input: `{"file_path":"README.md"}`,
			}},
		}, nil
	})
	rec := serveGoldenRequest(handler, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-test","messages":[{"role":"user","content":"Use tool"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertServerGolden(t, "openai_chat_completion_tool_calls.json", normalizeOpenAIChatJSON(t, rec.Body.Bytes()))
}

func TestServerGoldenOpenAIChatCompletionStream(t *testing.T) {
	handler := NewHandler(Options{
		AuthToken: "token",
		Workspace: "/workspace",
		StreamQueryFunc: func(_ context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			if req.Prompt != "Hello" {
				t.Fatalf("prompt = %q", req.Prompt)
			}
			_, _ = sink.Write([]byte("H"))
			_, _ = sink.Write([]byte("i"))
			return query.Result{Model: "claude-test", StopReason: "end_turn"}, nil
		},
	}, nil)
	rec := serveGoldenRequest(handler, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"Hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertServerGolden(t, "openai_chat_completion_stream.txt", normalizeOpenAISSE(t, rec.Body.String()))
}

func TestServerGoldenOpenAIModels(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token", Models: []string{"model-a", "model-b"}}, nil)
	rec := serveGoldenRequest(handler, http.MethodGet, "/v1/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertServerGolden(t, "openai_models.json", prettyJSON(t, rec.Body.Bytes()))
}

// When no explicit Models are configured on the server, /v1/models should reflect
// the user's actually-configured providers (ConfiguredModels), not the built-in
// Anthropic KnownModels list.
func TestOpenAIModelsFallsBackToConfiguredProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := `{
	  "model": "glm-5.1",
	  "fallback": {"enabled": true, "providers": [
	    {"name": "gpt", "type": "custom", "baseURL": "https://x", "apiKey": "y", "model": "gpt-5.5"}
	  ]}
	}`
	settingsPath := filepath.Join(home, ".niuma", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Options{AuthToken: "token", Workspace: t.TempDir()}, nil)
	rec := serveGoldenRequest(handler, http.MethodGet, "/v1/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "glm-5.1") || !strings.Contains(body, "gpt-5.5") {
		t.Fatalf("/v1/models should list configured providers, got:\n%s", body)
	}
	if strings.Contains(body, "claude-sonnet-4-6") {
		t.Fatalf("/v1/models must not fall back to Anthropic KnownModels when providers are configured:\n%s", body)
	}
}

func TestServerGoldenTenantAPICurlFlow(t *testing.T) {
	fixed := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	fake := &fakeTenantService{
		sessionID: 6,
		messageID: 9,
		sessions: []mysqlstore.Session{{
			ID:            5,
			SessionKey:    "sess-golden",
			Title:         "Golden Session",
			Status:        "active",
			Model:         "claude-test",
			CWD:           "/workspace",
			StartedAt:     fixed,
			LastMessageAt: fixed.Add(2 * time.Minute),
		}},
		messageRows: []mysqlstore.Message{{
			ID:        7,
			SessionID: 5,
			TurnIndex: 1,
			Role:      "user",
			Content:   "hi",
			TraceID:   "trace-golden",
			CreatedAt: fixed.Add(time.Minute),
		}, {
			ID:           8,
			SessionID:    5,
			TurnIndex:    2,
			Role:         "assistant",
			Content:      "hello",
			Model:        "claude-test",
			InputTokens:  3,
			OutputTokens: 2,
			TraceID:      "trace-golden",
			CreatedAt:    fixed.Add(2 * time.Minute),
		}},
		agentTasks: []mysqlstore.AgentTask{{
			ID:                 41,
			TenantID:           1,
			UserID:             2,
			ParentSessionID:    5,
			SubagentSessionKey: "subagent-1",
			AgentName:          "reviewer",
			Description:        "review changes",
			Status:             agenttasks.StatusCompleted,
			Model:              "claude-test",
			ResultJSON:         `{"content":"ok"}`,
			TraceID:            "trace-golden",
			StartedAt:          fixed.Add(3 * time.Minute),
			FinishedAt:         fixed.Add(4 * time.Minute),
		}},
		agentTaskEvents: []mysqlstore.AgentTaskEvent{{
			ID:          51,
			TaskID:      41,
			EventType:   agenttasks.EventStarted,
			PayloadJSON: `{"agent_name":"reviewer"}`,
			TraceID:     "trace-golden",
			CreatedAt:   fixed.Add(3 * time.Minute),
		}, {
			ID:          52,
			TaskID:      41,
			EventType:   agenttasks.EventCompleted,
			PayloadJSON: `{"turns":1}`,
			TraceID:     "trace-golden",
			CreatedAt:   fixed.Add(4 * time.Minute),
		}},
	}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake}, nil)
	steps := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/tenant/sessions?limit=2"},
		{method: http.MethodPost, path: "/tenant/sessions", body: `{"session_key":"sess-created","title":"Created","model":"claude-test","cwd":"/workspace"}`},
		{method: http.MethodPatch, path: "/tenant/sessions/5", body: `{"title":"Renamed","status":"active"}`},
		{method: http.MethodDelete, path: "/tenant/sessions/5"},
		{method: http.MethodGet, path: "/tenant/messages?session_id=5&limit=10"},
		{method: http.MethodPost, path: "/tenant/messages", body: `{"session_id":5,"turn_index":3,"role":"user","content":"again"}`},
		{method: http.MethodGet, path: "/tenant/agent-tasks?limit=4"},
		{method: http.MethodGet, path: "/tenant/agent-tasks/41/events?limit=8"},
	}
	var transcript strings.Builder
	for i, step := range steps {
		if i > 0 {
			transcript.WriteString("\n")
		}
		transcript.WriteString("$ curl -s")
		if step.method != http.MethodGet {
			transcript.WriteString(" -X ")
			transcript.WriteString(step.method)
		}
		if step.body != "" {
			transcript.WriteString(" -d '")
			transcript.WriteString(step.body)
			transcript.WriteString("'")
		}
		transcript.WriteString(" ")
		transcript.WriteString(step.path)
		transcript.WriteString("\n")
		rec := serveGoldenTenantRequest(handler, step.method, step.path, step.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", step.path, rec.Code, rec.Body.String())
		}
		transcript.WriteString(prettyJSON(t, rec.Body.Bytes()))
	}
	assertServerGolden(t, "tenant_api_curl_flow.txt", transcript.String())
}

func TestServerGoldenTenantAdminAuditCurlFlow(t *testing.T) {
	fixed := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	fake := &fakeTenantService{
		currentUser: mysqlstore.User{ID: 2, Role: "owner", Status: "active"},
		userID:      3,
		tenantID:    4,
		tenants: []mysqlstore.Tenant{{
			ID:           1,
			TenantKey:    "yutang",
			Name:         "Yutang",
			Status:       "active",
			SettingsJSON: `{"tier":"pro"}`,
		}, {
			ID:        4,
			TenantKey: "acme",
			Name:      "Acme",
			Status:    "active",
		}},
		users: []mysqlstore.User{{
			ID:          2,
			TenantID:    1,
			UserKey:     "user-test",
			Email:       "user-test@example.test",
			DisplayName: "User Test",
			Role:        "owner",
			Status:      "active",
			UpdatedAt:   fixed,
		}, {
			ID:          3,
			TenantID:    1,
			UserKey:     "user-two",
			DisplayName: "User Two",
			Role:        "member",
			Status:      "active",
			UpdatedAt:   fixed.Add(time.Minute),
		}},
		auditLogs: []mysqlstore.AuditLog{{
			ID:           71,
			TenantID:     1,
			ActorUserID:  2,
			Action:       "tenant.user.admin_save",
			ResourceType: "user",
			ResourceID:   "3",
			TraceID:      "trace-golden",
			CreatedAt:    fixed.Add(2 * time.Minute),
		}, {
			ID:           72,
			TenantID:     1,
			ActorUserID:  2,
			Action:       "tenant.user.archive",
			ResourceType: "user",
			ResourceID:   "user-three",
			TraceID:      "trace-golden",
			CreatedAt:    fixed.Add(3 * time.Minute),
		}},
	}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake}, nil)
	steps := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/tenant/tenants?limit=3"},
		{method: http.MethodPost, path: "/tenant/tenants", body: `{"tenant_key":"acme","name":"Acme","status":"active","settings_json":"{\"tier\":\"team\"}"}`},
		{method: http.MethodDelete, path: "/tenant/tenants?tenant_key=acme"},
		{method: http.MethodPost, path: "/tenant/skills/rollback", body: `{"skill_key":"go-review","version":1,"target_version":3}`},
		{method: http.MethodGet, path: "/tenant/users?limit=2"},
		{method: http.MethodPost, path: "/tenant/users", body: `{"user_key":"user-three","email":"user-three@example.test","display_name":"User Three","role":"member","status":"active"}`},
		{method: http.MethodDelete, path: "/tenant/users?user_key=user-three"},
		{method: http.MethodGet, path: "/tenant/audit?limit=5"},
	}
	var transcript strings.Builder
	for i, step := range steps {
		if i > 0 {
			transcript.WriteString("\n")
		}
		transcript.WriteString("$ curl -s")
		if step.method != http.MethodGet {
			transcript.WriteString(" -X ")
			transcript.WriteString(step.method)
		}
		if step.body != "" {
			transcript.WriteString(" -d '")
			transcript.WriteString(step.body)
			transcript.WriteString("'")
		}
		transcript.WriteString(" ")
		transcript.WriteString(step.path)
		transcript.WriteString("\n")
		rec := serveGoldenTenantRequest(handler, step.method, step.path, step.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", step.path, rec.Code, rec.Body.String())
		}
		transcript.WriteString(prettyJSON(t, rec.Body.Bytes()))
	}
	if fake.lastArchivedTenantKey != "acme" || fake.lastArchivedUserKey != "user-three" || fake.lastAudit.Action != "tenant.user.archive" {
		t.Fatalf("admin audit flow did not record archive audit: %+v", fake)
	}
	assertServerGolden(t, "tenant_admin_audit_curl_flow.txt", transcript.String())
}

func TestServerGoldenTenantAgentTaskCancelErrorCurlFlow(t *testing.T) {
	fixed := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	controller := agenttasks.NewController()
	controller.Register(41, func() {})
	fake := &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{{
			ID:              41,
			ParentSessionID: 5,
			AgentName:       "reviewer",
			Description:     "review changes",
			Status:          agenttasks.StatusRunning,
			Model:           "claude-test",
			TraceID:         "trace-golden",
			StartedAt:       fixed.Add(3 * time.Minute),
		}, {
			ID:              42,
			ParentSessionID: 5,
			AgentName:       "reviewer",
			Description:     "review changes after restart",
			Status:          agenttasks.StatusRunning,
			Model:           "claude-test",
			TraceID:         "trace-golden",
			StartedAt:       fixed.Add(4 * time.Minute),
		}},
	}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, AgentTaskController: controller}, nil)
	steps := []struct {
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{method: http.MethodPost, path: "/tenant/agent-tasks/42/cancel", wantStatus: http.StatusOK},
		{method: http.MethodPost, path: "/tenant/agent-tasks/41/cancel", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/tenant/agent-tasks/not-a-number/events?limit=8", wantStatus: http.StatusBadRequest},
	}
	var transcript strings.Builder
	for i, step := range steps {
		if i > 0 {
			transcript.WriteString("\n")
		}
		transcript.WriteString("$ curl -s -X ")
		transcript.WriteString(step.method)
		transcript.WriteString(" ")
		transcript.WriteString(step.path)
		transcript.WriteString("\n")
		rec := serveGoldenTenantRequest(handler, step.method, step.path, step.body)
		transcript.WriteString("HTTP ")
		transcript.WriteString(http.StatusText(rec.Code))
		transcript.WriteString("\n")
		if rec.Code != step.wantStatus {
			t.Fatalf("%s status = %d body=%s", step.path, rec.Code, rec.Body.String())
		}
		transcript.WriteString(prettyJSON(t, rec.Body.Bytes()))
	}
	assertServerGolden(t, "tenant_agent_task_cancel_error_curl_flow.txt", transcript.String())
}

func serveGoldenRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("authorization", "Bearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func serveGoldenTenantRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("x-tenant-key", "yutang")
	req.Header.Set("x-user-id", "user-test")
	req.Header.Set("x-trace-id", "trace-golden")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func normalizeOpenAIChatJSON(t *testing.T, data []byte) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode response: %v\n%s", err, data)
	}
	value["id"] = "<chatcmpl>"
	value["created"] = float64(0)
	return marshalPrettyNoEscape(t, value)
}

func normalizeOpenAISSE(t *testing.T, body string) string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			out = append(out, "")
			continue
		}
		if line == "data: [DONE]" {
			out = append(out, line)
			continue
		}
		raw := strings.TrimPrefix(line, "data: ")
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatalf("decode SSE line: %v\n%s", err, line)
		}
		value["id"] = "<chatcmpl>"
		value["created"] = float64(0)
		out = append(out, "data: "+strings.TrimSpace(marshalPrettyNoEscape(t, value)))
	}
	return strings.Join(out, "\n")
}

func prettyJSON(t *testing.T, data []byte) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode json: %v\n%s", err, data)
	}
	return marshalPrettyNoEscape(t, value)
}

func marshalPrettyNoEscape(t *testing.T, value any) string {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func assertServerGolden(t *testing.T, name string, got string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "golden", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n--- got ---\n%s", name, err, got)
	}
	want := strings.TrimRight(string(data), "\n")
	got = strings.TrimRight(got, "\n")
	if got != want {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
