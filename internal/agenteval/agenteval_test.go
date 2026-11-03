package agenteval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunDefaultSuiteProducesReport(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	report, err := Run(context.Background(), Options{
		CWD:       t.TempDir(),
		FixedTime: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" || report.Total != 10 || report.Passed != 10 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}
	byID := map[string]CaseResult{}
	for _, item := range report.Cases {
		byID[item.ID] = item
	}
	for _, id := range []string{"chat_basic", "tool_read", "skill_load", "subagent_task", "auto_compact", "permission_boundary", "trace_observability", "workflow_closure", "subagent_multiagent_parity", "multiagent_e2e"} {
		if byID[id].Status != "passed" {
			t.Fatalf("case %s = %+v", id, byID[id])
		}
	}
	if byID["workflow_closure"].Evidence["response"] != "workflow closure ok" {
		t.Fatalf("missing workflow closure evidence: %+v", byID["workflow_closure"].Evidence)
	}
	if byID["subagent_task"].Evidence["agent_tasks"].(int) == 0 {
		t.Fatalf("missing subagent evidence: %+v", byID["subagent_task"].Evidence)
	}
	if byID["auto_compact"].Evidence["summary_requests"].(int) == 0 {
		t.Fatalf("missing compact evidence: %+v", byID["auto_compact"].Evidence)
	}
	if byID["subagent_multiagent_parity"].Evidence["agent_tasks"].(int) == 0 {
		t.Fatalf("missing parity evidence: %+v", byID["subagent_multiagent_parity"].Evidence)
	}
	if byID["multiagent_e2e"].Evidence["agent_tasks"].(int) == 0 || byID["multiagent_e2e"].Evidence["agent_events"].(int) == 0 {
		t.Fatalf("missing multi-agent evidence: %+v", byID["multiagent_e2e"].Evidence)
	}
}

func TestRunLiveProfileSkipsWhenBaseURLMissing(t *testing.T) {
	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", "")
	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", "")
	report, err := Run(context.Background(), Options{
		CWD:       t.TempDir(),
		Profile:   "live",
		FixedTime: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "skipped" || report.Skipped != 1 || report.Failed != 0 || report.Environment.Mode != "live-api-server" {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunAnthropicThinkingProfileSkipsWhenAuthMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	report, err := Run(context.Background(), Options{
		CWD:       t.TempDir(),
		Profile:   "anthropic-thinking",
		FixedTime: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "skipped" || report.Skipped != 1 || report.Failed != 0 || report.Environment.Mode != "live-anthropic-thinking" {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunLiveAgentAPIProfileSkipsWhenBaseURLMissing(t *testing.T) {
	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", "")
	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", "")
	report, err := Run(context.Background(), Options{
		CWD:       t.TempDir(),
		Profile:   "live-agent-api",
		FixedTime: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "skipped" || report.Skipped != 1 || report.Failed != 0 || report.Environment.Mode != "live-agent-api-server" {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunAnthropicThinkingProfileWithFakeServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	var sawThinking bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Fatalf("missing api key header: %+v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		thinking, ok := body["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"].(float64) != 1024 {
			t.Fatalf("thinking body = %#v", body["thinking"])
		}
		sawThinking = true
		w.Header().Set("content-type", "text/event-stream")
		writeAnthropicThinkingStream(t, w)
	}))
	defer server.Close()

	data, err := json.Marshal(map[string]string{
		"provider": "anthropic-compatible", "model": "test-model",
		"apiKey": "test-key", "baseURL": server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("NIUMA_CONFIG_DIR"), "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{CWD: t.TempDir(), Profile: "anthropic-thinking"})
	if err != nil {
		t.Fatal(err)
	}
	if !sawThinking || report.Status != "passed" || report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v sawThinking=%v", report, sawThinking)
	}
}

func TestRunLiveAgentAPIProfileWithFakeServer(t *testing.T) {
	var paths []string
	var sawCreateBody bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("missing auth header for %s: %+v", r.URL.Path, r.Header)
		}
		if r.Header.Get("X-Tenant-Key") != "tenant-live" || r.Header.Get("X-User-Id") != "user-live" || r.Header.Get("X-Device-Id") != "device-live" {
			t.Fatalf("missing tenant headers for %s: %+v", r.URL.Path, r.Header)
		}
		w.Header().Set("content-type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/tenant/agent-tasks":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["agent_name"] != "live-e2e" || body["model"] != "eval-model" || body["trace_id"] == "" {
				t.Fatalf("create body = %#v", body)
			}
			meta, ok := body["metadata_json"].(map[string]any)
			if !ok || meta["source"] != "agent_eval_live" {
				t.Fatalf("metadata_json = %#v", body["metadata_json"])
			}
			sawCreateBody = true
			_, _ = w.Write([]byte(`{"id":77}`))
		case r.Method == http.MethodPost && r.URL.Path == "/tenant/agent-tasks/77/message":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["from_agent"] != "coordinator" || body["content"] != "please continue" || body["trace_id"] == "" {
				t.Fatalf("message body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"id":78,"task_id":77}`))
		case r.Method == http.MethodGet && r.URL.Path == "/tenant/agent-tasks/77":
			if len(paths) < 6 {
				_, _ = w.Write([]byte(`{"id":77,"agent_name":"live-e2e","status":"running","model":"eval-model"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":77,"agent_name":"live-e2e","status":"cancelled","model":"eval-model"}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/tenant/agent-tasks/77/events":
			if r.URL.Query().Get("limit") != "20" {
				t.Fatalf("events query = %s", r.URL.RawQuery)
			}
			if len(paths) < 7 {
				_, _ = w.Write([]byte(`{"data":[{"id":1,"task_id":77,"event_type":"started"},{"id":2,"task_id":77,"event_type":"message"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"data":[{"id":1,"task_id":77,"event_type":"started"},{"id":2,"task_id":77,"event_type":"message"},{"id":3,"task_id":77,"event_type":"cancelled"}]}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/tenant/agent-tasks/77/cancel":
			_, _ = w.Write([]byte(`{"id":77,"cancelled":true,"in_process":false}`))
		case r.Method == http.MethodGet && r.URL.Path == "/tenant/agent-tasks":
			if r.URL.Query().Get("limit") != "20" {
				t.Fatalf("list query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":77,"agent_name":"live-e2e","status":"cancelled","model":"eval-model"}]}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()

	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", server.URL)
	t.Setenv("NIUMA_EVAL_LIVE_AUTH_TOKEN", "test-token")
	t.Setenv("NIUMA_EVAL_LIVE_TENANT_KEY", "tenant-live")
	t.Setenv("NIUMA_EVAL_LIVE_USER_KEY", "user-live")
	t.Setenv("NIUMA_EVAL_LIVE_DEVICE_ID", "device-live")
	t.Setenv("NIUMA_EVAL_LIVE_MYSQL_DSN", "")
	t.Setenv("NIUMA_MYSQL_DSN", "")
	t.Setenv("MYSQL_DSN", "")
	report, err := Run(context.Background(), Options{CWD: t.TempDir(), Profile: "live-agent-api"})
	if err != nil {
		t.Fatal(err)
	}
	if !sawCreateBody || report.Status != "passed" || report.Total != 1 || report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v sawCreateBody=%v", report, sawCreateBody)
	}
	want := []string{
		"POST /tenant/agent-tasks",
		"POST /tenant/agent-tasks/77/message",
		"GET /tenant/agent-tasks/77",
		"GET /tenant/agent-tasks/77/events?limit=20",
		"POST /tenant/agent-tasks/77/cancel",
		"GET /tenant/agent-tasks/77",
		"GET /tenant/agent-tasks/77/events?limit=20",
		"GET /tenant/agent-tasks?limit=20",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestRunLiveProfileCallsAPIServerEndpoints(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.URL.Path != "/health" {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("missing auth header for %s", r.URL.Path)
			}
			if r.Header.Get("X-Tenant-Key") != "tenant-live" || r.Header.Get("X-User-Id") != "user-live" {
				t.Fatalf("missing tenant headers for %s: %+v", r.URL.Path, r.Header)
			}
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"data":[]}`))
	}))
	defer server.Close()

	t.Setenv("NIUMA_EVAL_LIVE_API_BASE", server.URL)
	t.Setenv("NIUMA_EVAL_LIVE_AUTH_TOKEN", "test-token")
	t.Setenv("NIUMA_EVAL_LIVE_TENANT_KEY", "tenant-live")
	t.Setenv("NIUMA_EVAL_LIVE_USER_KEY", "user-live")
	report, err := Run(context.Background(), Options{CWD: t.TempDir(), Profile: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" || report.Total != 4 || report.Passed != 4 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}
	want := []string{"/health", "/tenant/sessions?limit=1", "/mobile/chat/sessions?limit=1", "/trace/api/sessions?source=tenant&limit=1"}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %#v", paths)
	}
}

func writeAnthropicThinkingStream(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	payload := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_eval","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":8,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"consider"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_eval"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"thinking live ok"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":6}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
}

func TestRunCanLoadDatasetAndWriteReports(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	dir := t.TempDir()
	dataset := filepath.Join(dir, "dataset.json")
	data, _ := json.Marshal(Suite{
		ID: "custom",
		Cases: []Case{{
			ID:             "custom_chat",
			Type:           "query",
			Prompt:         "hello",
			ExpectContains: []string{"hello"},
		}},
	})
	if err := os.WriteFile(dataset, data, 0644); err != nil {
		t.Fatal(err)
	}
	jsonOut := filepath.Join(dir, "report.json")
	report, err := Run(context.Background(), Options{CWD: dir, Dataset: dataset, Output: jsonOut, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	if report.SuiteID != "custom" || report.Total != 1 {
		t.Fatalf("report = %+v", report)
	}
	jsonData, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonData), `"suite_id": "custom"`) {
		t.Fatalf("json report = %s", string(jsonData))
	}
	mdOut := filepath.Join(dir, "report.md")
	if err := WriteReport(report, mdOut, "markdown"); err != nil {
		t.Fatal(err)
	}
	mdData, err := os.ReadFile(mdOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mdData), "Agent Eval Report") || !strings.Contains(string(mdData), "custom_chat") {
		t.Fatalf("markdown report = %s", string(mdData))
	}
}
