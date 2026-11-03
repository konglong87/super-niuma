package webfetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestWebFetchHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Hello</h1><script>bad()</script><p>World</p></body></html>`))
	}))
	defer server.Close()

	input, _ := json.Marshal(map[string]string{"url": server.URL})
	res := New().Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Hello") || !strings.Contains(res.Content, "World") || strings.Contains(res.Content, "bad()") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestWebFetchHonorsNetworkDisabled(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"url": "https://example.test"})
	res := New().Run(context.Background(), input, tools.Context{Sandbox: tools.SandboxConfig{NetworkDisabled: true}})
	if !res.IsError || !strings.Contains(res.Content, "network access is disabled") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebFetchHonorsDomainPolicy(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"url": "https://blocked.example.test"})
	res := New().Run(context.Background(), input, tools.Context{Sandbox: tools.SandboxConfig{NetworkDenyDomains: []string{"example.test"}}})
	if !res.IsError || !strings.Contains(res.Content, "denied") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebFetchHonorsRequiredProxyPolicy(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"url": "https://example.test"})
	res := New().Run(context.Background(), input, tools.Context{Sandbox: tools.SandboxConfig{NetworkProxyRequired: true}})
	if !res.IsError || !strings.Contains(res.Content, "proxy.url is empty") {
		t.Fatalf("res = %+v", res)
	}
}
