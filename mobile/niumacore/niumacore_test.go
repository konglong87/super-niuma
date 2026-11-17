package niumacore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

func decode(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("report is not JSON: %v: %s", err, s)
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func resetInit(t *testing.T) {
	t.Helper()
	t.Setenv(super.StateDirectoryEnv, "")
	initMu.Lock()
	stateDir = ""
	initMu.Unlock()
	t.Cleanup(func() {
		initMu.Lock()
		stateDir = ""
		initMu.Unlock()
	})
}

func TestInitSetsStateDirectoryOnce(t *testing.T) {
	resetInit(t)
	dir := canonicalTempDir(t)
	var r initReport
	decode(t, Init(dir), &r)
	if !r.OK || os.Getenv(super.StateDirectoryEnv) != dir {
		t.Fatalf("init failed: %+v", r)
	}
	if got, err := super.StateDirectory(); err != nil || got != dir {
		t.Fatalf("state directory %q, %v", got, err)
	}
	decode(t, Init(dir), &r)
	if !r.OK {
		t.Fatalf("repeating the same dir refused: %+v", r)
	}
	decode(t, Init(canonicalTempDir(t)), &r)
	if r.OK || os.Getenv(super.StateDirectoryEnv) != dir {
		t.Fatalf("second dir accepted: %+v", r)
	}
}

func TestInitRejectsUnusableDirectories(t *testing.T) {
	resetInit(t)
	for _, dir := range []string{"", "relative", "/tmp/../tmp", filepath.Join(canonicalTempDir(t), "missing")} {
		var r initReport
		decode(t, Init(dir), &r)
		if r.OK {
			t.Fatalf("%q accepted", dir)
		}
	}
}

func TestProbeRoundTripsSQLite(t *testing.T) {
	var r probeReport
	decode(t, Probe(canonicalTempDir(t)), &r)
	if !r.OK || r.SQLiteVersion == "" || r.GOOS == "" || r.Core != Version() {
		t.Fatalf("probe failed: %+v", r)
	}
}

func TestProbeRejectsRelativeDataDir(t *testing.T) {
	var r probeReport
	decode(t, Probe("relative/dir"), &r)
	if r.OK || !strings.HasPrefix(r.Error, "sqlite:") {
		t.Fatalf("relative dir accepted: %+v", r)
	}
}

func TestHTTPSProbeRefusesNonHTTPS(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://user:pw@example.com", "not a url", ""} {
		var r httpsReport
		decode(t, HTTPSProbe(u), &r)
		if r.OK || r.Error == "" {
			t.Fatalf("%q accepted: %+v", u, r)
		}
	}
}

func TestHTTPSProbeReportsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	var r httpsReport
	decode(t, HTTPSProbe(server.URL), &r)
	if r.OK || !strings.Contains(r.Error, "certificate") {
		t.Fatalf("self-signed certificate trusted: %+v", r)
	}
}

func TestModelCallRunsOneExecutionAttempt(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil || parsed["model"] != "m0-model" || parsed["tools"] != nil {
			t.Errorf("unexpected body %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"core is alive"},"finish_reason":"stop"}],"usage":{"prompt_tokens":40,"completion_tokens":4}}`)
	}))
	defer server.Close()
	route := `{"base_url":"` + server.URL + `/v1","model":"m0-model","token":"test-token"}`
	var r modelReport
	decode(t, ModelCall(route, canonicalTempDir(t), "Say the core is alive."), &r)
	if !r.OK || r.Text != "core is alive" || !r.UsageKnown || r.InputTokens != 40 || r.OutputTokens != 4 || calls != 1 {
		t.Fatalf("model call failed: %+v calls=%d", r, calls)
	}
}

func TestModelCallDoesNotRetryOrLeakProviderErrors(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "upstream secret detail", http.StatusTooManyRequests)
	}))
	defer server.Close()
	route := `{"base_url":"` + server.URL + `/v1","model":"m0-model","token":"test-token"}`
	out := ModelCall(route, canonicalTempDir(t), "Hello")
	var r modelReport
	decode(t, out, &r)
	if r.OK || calls != 1 || r.HTTPStatus != http.StatusTooManyRequests || strings.Contains(out, "secret") {
		t.Fatalf("429 handled wrongly: %s calls=%d", out, calls)
	}
}

func TestModelCallRefusesPlainHTTPToNonLoopback(t *testing.T) {
	route := `{"base_url":"http://model.example.test/v1","model":"m0-model","token":"test-token"}`
	var r modelReport
	decode(t, ModelCall(route, canonicalTempDir(t), "Hello"), &r)
	if r.OK || r.Error == "" {
		t.Fatalf("plain http to a remote host accepted: %+v", r)
	}
}

func TestModelCallRejectsIncompleteRoute(t *testing.T) {
	for _, route := range []string{`{`, `{"base_url":"http://127.0.0.1:1/v1","model":"m"}`, `{"model":"m","token":"t"}`} {
		var r modelReport
		decode(t, ModelCall(route, canonicalTempDir(t), "Hello"), &r)
		if r.OK || r.Error == "" {
			t.Fatalf("%s accepted: %+v", route, r)
		}
	}
}

func TestScreenSummaryCountsUsableNodes(t *testing.T) {
	snapshot := `{"package":"com.android.settings","nodes":[
		{"parent":-1,"cls":"android.widget.FrameLayout","b":[0,0,1080,2400]},
		{"parent":0,"cls":"android.widget.TextView","text":"Network & internet","b":[0,200,1080,300],"click":true},
		{"parent":0,"cls":"android.widget.EditText","desc":"Search settings","b":[0,100,1080,180],"edit":true},
		{"parent":0,"cls":"androidx.recyclerview.widget.RecyclerView","b":[0,180,1080,2400],"scroll":true}]}`
	var r screenReport
	decode(t, ScreenSummary(snapshot), &r)
	if !r.OK || r.Package != "com.android.settings" || r.Nodes != 4 || r.Interactive != 3 || r.Labeled != 2 {
		t.Fatalf("unexpected summary: %+v", r)
	}
}

func TestScreenSummaryRejectsBrokenTrees(t *testing.T) {
	for _, s := range []string{`[`, `{"nodes":[{"parent":0,"cls":"x","b":[0,0,1,1]}]}`, `{"nodes":[{"parent":-2,"cls":"x","b":[0,0,1,1]}]}`} {
		var r screenReport
		decode(t, ScreenSummary(s), &r)
		if r.OK || r.Error == "" {
			t.Fatalf("%s accepted: %+v", s, r)
		}
	}
}

func TestTokenBudgetKeepsUnknownUsageAndRefusesOverLimit(t *testing.T) {
	b := &tokenBudget{limit: 100}
	ctx := context.Background()
	if err := b.Reserve(ctx, "a", 30, 20); err != nil {
		t.Fatal(err)
	}
	if err := b.Settle(ctx, "a", execution.Usage{}, false); err != nil || b.spent != 50 {
		t.Fatalf("unknown usage released the reservation: spent=%d err=%v", b.spent, err)
	}
	if err := b.Reserve(ctx, "b", 40, 20); err == nil {
		t.Fatal("reservation over the limit accepted")
	}
	if err := b.Settle(ctx, "missing", execution.Usage{}, true); err == nil {
		t.Fatal("unknown reservation settled")
	}
}
