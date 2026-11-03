package webbrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestWebBrowserOpenExtractsPageSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><head><title>Test Page</title></head><body><a href="/next">Next</a><form action="/submit" method="post"><input name="email"></form><p>Hello world</p></body></html>`))
	}))
	defer server.Close()

	res := New().Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": server.URL}), tools.Context{})
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.Contains(res.Content, "Test Page") || !strings.Contains(res.Content, server.URL+"/next") || !strings.Contains(res.Content, "email") {
		t.Fatalf("snapshot = %s", res.Content)
	}
}

func TestWebBrowserClickInputSubmit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><title>Home</title></head><body><a href="/form">Form</a></body></html>`))
		case "/form":
			_, _ = w.Write([]byte(`<html><head><title>Form</title></head><body><form action="/submit" method="post"><input name="email"></form></body></html>`))
		case "/submit":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`<html><head><title>Submitted</title></head><body>` + r.Form.Get("email") + `</body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tool := New()
	res := tool.Run(context.Background(), mustJSON(map[string]any{"action": "navigate", "url": server.URL, "session_id": "s1"}), tools.Context{})
	if res.IsError {
		t.Fatal(res.Content)
	}
	res = tool.Run(context.Background(), mustJSON(map[string]any{"action": "click", "selector": "Form", "session_id": "s1"}), tools.Context{})
	if res.IsError || !strings.Contains(res.Content, `"title": "Form"`) {
		t.Fatalf("click = %+v", res)
	}
	res = tool.Run(context.Background(), mustJSON(map[string]any{"action": "input", "selector": "email", "value": "a@example.com", "session_id": "s1"}), tools.Context{})
	if res.IsError {
		t.Fatalf("input = %+v", res)
	}
	res = tool.Run(context.Background(), mustJSON(map[string]any{"action": "submit", "session_id": "s1"}), tools.Context{})
	if res.IsError || !strings.Contains(res.Content, "a@example.com") {
		t.Fatalf("submit = %+v", res)
	}
}

// TestWebBrowserScreenshotRefusesWithoutRealBrowser locks AUDIT-P1-14: the fallback
// fetcher renders nothing, so screenshot used to synthesise an SVG containing only
// the title and URL and hand it back as if it were a picture of the page. It must
// fail loudly and say how to get a real one instead.
func TestWebBrowserScreenshotRefusesWithoutRealBrowser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Home</title></head><body>hi</body></html>`))
	}))
	defer server.Close()

	t.Setenv("NIUMA_WEBBROWSER_MODE", "")
	t.Setenv("NIUMA_PLAYWRIGHT_RUNNER", "")

	tool := New()
	if res := tool.Run(context.Background(), mustJSON(map[string]any{"action": "navigate", "url": server.URL, "session_id": "shot"}), tools.Context{}); res.IsError {
		t.Fatal(res.Content)
	}
	res := tool.Run(context.Background(), mustJSON(map[string]any{"action": "screenshot", "session_id": "shot"}), tools.Context{})
	if !res.IsError {
		t.Fatalf("screenshot must be an error in fallback mode, got %+v", res)
	}
	if strings.Contains(res.Content, "base64") || strings.Contains(res.Content, "data_url") {
		t.Errorf("screenshot still returns image data: %s", res.Content)
	}
	for _, want := range []string{"NIUMA_WEBBROWSER_MODE", "NIUMA_PLAYWRIGHT_RUNNER", "playwright-browser-runner.mjs"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("error message does not explain %q: %s", want, res.Content)
		}
	}
}

func TestWebBrowserHonorsNetworkDisabled(t *testing.T) {
	res := New().Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": "https://example.test"}), tools.Context{Sandbox: tools.SandboxConfig{NetworkDisabled: true}})
	if !res.IsError || !strings.Contains(res.Content, "network access is disabled") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebBrowserHonorsDomainPolicy(t *testing.T) {
	res := New().Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": "https://blocked.example.test"}), tools.Context{Sandbox: tools.SandboxConfig{NetworkDenyDomains: []string{"example.test"}}})
	if !res.IsError || !strings.Contains(res.Content, "denied") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebBrowserHonorsRequiredProxyPolicy(t *testing.T) {
	res := New().Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": "https://example.test"}), tools.Context{Sandbox: tools.SandboxConfig{NetworkProxyRequired: true}})
	if !res.IsError || !strings.Contains(res.Content, "proxy.url is empty") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebBrowserPlaywrightAdapter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell runner test is unix-only")
	}
	tmp := t.TempDir()
	requestPath := filepath.Join(tmp, "request.json")
	runnerPath := filepath.Join(tmp, "runner.sh")
	script := `#!/bin/sh
cat > "$1"
printf '%s\n' '{"page":{"url":"https://example.test/app","status":200,"title":"Rendered","text":"JS rendered content","links":[{"text":"Next","href":"https://example.test/next"}]}}'
`
	if err := os.WriteFile(runnerPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIUMA_WEBBROWSER_MODE", "playwright")
	t.Setenv("NIUMA_PLAYWRIGHT_RUNNER", shellQuote(runnerPath)+" "+shellQuote(requestPath))

	res := New().Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": "https://example.test/app", "session_id": "s1"}), tools.Context{})
	if res.IsError || !strings.Contains(res.Content, "JS rendered content") || !strings.Contains(res.Content, `"session_id": "s1"`) {
		t.Fatalf("res = %+v", res)
	}
	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(requestData), `"action":"open"`) || !strings.Contains(string(requestData), `"session_id":"s1"`) {
		t.Fatalf("request = %s", requestData)
	}
}

func TestWebBrowserPlaywrightAdapterHonorsNetworkPolicyBeforeRunner(t *testing.T) {
	t.Setenv("NIUMA_WEBBROWSER_MODE", "playwright")
	t.Setenv("NIUMA_PLAYWRIGHT_RUNNER", "printf '{}'")
	res := New().Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": "https://blocked.example.test"}), tools.Context{Sandbox: tools.SandboxConfig{NetworkDenyDomains: []string{"example.test"}}})
	if !res.IsError || !strings.Contains(res.Content, "denied") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebBrowserPlaywrightRunnerGolden(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("playwright runner golden is unix-only")
	}
	runner := repoPath(t, "scripts", "playwright-browser-runner.mjs")
	if _, err := os.Stat(runner); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	cmd := exec.Command("node", runner, "--self-test")
	cmd.Env = append(os.Environ(), "NIUMA_PLAYWRIGHT_STATE_DIR="+stateDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("playwright runtime unavailable: %v\n%s", err, out)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/submitted" {
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Submitted</title></head><body>submitted ` + r.URL.Query().Get("email") + `</body></html>`))
			return
		}
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head><title>Playwright Golden</title></head>
<body>
  <main id="app">Loading...</main>
  <a id="next" href="/next">Next Page</a>
  <form id="signup" action="/submitted" method="get">
    <input id="email" name="email">
    <button id="submit" type="submit">Submit</button>
  </form>
  <script>
    document.getElementById("app").textContent = "Rendered by JavaScript";
    console.log("golden ready");
  </script>
</body>
</html>`))
	}))
	defer server.Close()

	t.Setenv("NIUMA_WEBBROWSER_MODE", "playwright")
	t.Setenv("NIUMA_PLAYWRIGHT_RUNNER", "node "+shellQuote(runner))
	t.Setenv("NIUMA_PLAYWRIGHT_STATE_DIR", stateDir)

	tool := New()
	res := tool.Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": server.URL, "session_id": "golden"}), tools.Context{})
	if res.IsError {
		t.Fatal(res.Content)
	}
	normalized := normalizePlaywrightGolden(t, res.Content, server.URL)
	expected, err := os.ReadFile(repoPath(t, "internal", "tools", "webbrowser", "testdata", "golden", "playwright_snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(normalized) != strings.TrimSpace(string(expected)) {
		t.Fatalf("golden mismatch\nwant:\n%s\n\ngot:\n%s", expected, normalized)
	}

	res = tool.Run(context.Background(), mustJSON(map[string]any{"action": "input", "selector": "email", "value": "a@example.com", "session_id": "golden"}), tools.Context{})
	if res.IsError {
		t.Fatalf("input = %+v", res)
	}
	res = tool.Run(context.Background(), mustJSON(map[string]any{"action": "submit", "selector": "signup", "session_id": "golden"}), tools.Context{})
	if res.IsError || !strings.Contains(res.Content, "a@example.com") {
		t.Fatalf("submit = %+v", res)
	}
	res = tool.Run(context.Background(), mustJSON(map[string]any{"action": "screenshot", "session_id": "golden"}), tools.Context{})
	if res.IsError || !strings.Contains(res.Content, "data:image/png;base64,") {
		t.Fatalf("screenshot = %+v", res)
	}
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	base, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	all := append([]string{base}, parts...)
	return filepath.Join(all...)
}

func normalizePlaywrightGolden(t *testing.T, content, baseURL string) string {
	t.Helper()
	var snapshot pageSnapshot
	if err := json.Unmarshal([]byte(content), &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.URL = strings.ReplaceAll(snapshot.URL, baseURL, "http://webbrowser.test")
	for i := range snapshot.Links {
		snapshot.Links[i].Href = strings.ReplaceAll(snapshot.Links[i].Href, baseURL, "http://webbrowser.test")
	}
	for i := range snapshot.Forms {
		snapshot.Forms[i].Action = strings.ReplaceAll(snapshot.Forms[i].Action, baseURL, "http://webbrowser.test")
	}
	for i := range snapshot.Network {
		snapshot.Network[i].URL = strings.ReplaceAll(snapshot.Network[i].URL, baseURL, "http://webbrowser.test")
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// TestBrowserAdapterDoesNotLeakSecrets locks AUDIT-P1-16 for the playwright runner.
func TestBrowserAdapterDoesNotLeakSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell runner test is unix-only")
	}
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, "env.txt")
	runnerPath := filepath.Join(tmp, "runner.sh")
	script := `#!/bin/sh
printf 'A=%s G=%s W=%s O=%s P=%s' "$ANTHROPIC_API_KEY" "$GITHUB_TOKEN" "$AWS_SECRET_ACCESS_KEY" "$OPENAI_API_KEY" "$GOPATH" > "$1"
printf '%s\n' '{"page":{"url":"https://example.test/app","status":200,"title":"T","text":"body"}}'
`
	if err := os.WriteFile(runnerPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "leaked-"+key)
	}
	t.Setenv("GOPATH", "/keep/gopath")
	t.Setenv("NIUMA_WEBBROWSER_MODE", "playwright")
	t.Setenv("NIUMA_PLAYWRIGHT_RUNNER", shellQuote(runnerPath)+" "+shellQuote(envPath))

	if res := New().Run(context.Background(), mustJSON(map[string]any{"action": "open", "url": "https://example.test/app"}), tools.Context{}); res.IsError {
		t.Fatal(res.Content)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "A= G= W= O= P=/keep/gopath" {
		t.Fatalf("adapter saw %q", data)
	}
}
