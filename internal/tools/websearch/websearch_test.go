package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestWebSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "golang" {
			t.Fatalf("query = %q", r.URL.Query().Get("q"))
		}
		_, _ = w.Write([]byte(`<html><body>
		  <a href="https://blocked.example/">Blocked</a>
		  <a href="https://go.dev/">The Go Programming Language</a>
		</body></html>`))
	}))
	defer server.Close()
	t.Setenv("NIUMA_WEBSEARCH_URL", server.URL)

	input, _ := json.Marshal(map[string]any{"query": "golang", "limit": 1, "blocked_domains": []string{"blocked.example"}})
	res := New().Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "The Go Programming Language") || !strings.Contains(res.Content, "https://go.dev/") || strings.Contains(res.Content, "Blocked") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestWebSearchUsesConfiguredEndpoint(t *testing.T) {
	t.Setenv("NIUMA_WEBSEARCH_URL", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "golang" {
			t.Fatalf("query = %q", r.URL.Query().Get("q"))
		}
		_, _ = w.Write([]byte(`<html><body><a href="https://go.dev/">Go</a></body></html>`))
	}))
	defer server.Close()

	input, _ := json.Marshal(map[string]any{"query": "golang"})
	res := NewWithEndpoint(server.URL).Run(context.Background(), input, tools.Context{})
	if res.IsError || !strings.Contains(res.Content, "https://go.dev/") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebSearchEnvironmentEndpointOverridesConfiguredEndpoint(t *testing.T) {
	configuredServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("configured endpoint should not be called when env endpoint is set")
	}))
	defer configuredServer.Close()
	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><a href="https://env.example/">Env</a></body></html>`))
	}))
	defer envServer.Close()
	t.Setenv("NIUMA_WEBSEARCH_URL", envServer.URL)

	input, _ := json.Marshal(map[string]any{"query": "golang"})
	res := NewWithEndpoint(configuredServer.URL).Run(context.Background(), input, tools.Context{})
	if res.IsError || !strings.Contains(res.Content, "https://env.example/") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebSearchAllowedDomains(t *testing.T) {
	results := filterResults([]Result{
		{Title: "A", URL: "https://a.example/page"},
		{Title: "B", URL: "https://docs.example.com/page"},
	}, []string{"example.com"}, nil, 5)
	if len(results) != 1 || results[0].Title != "B" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWebSearchHonorsNetworkDisabled(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"query": "golang"})
	res := New().Run(context.Background(), input, tools.Context{Sandbox: tools.SandboxConfig{NetworkDisabled: true}})
	if !res.IsError || !strings.Contains(res.Content, "network access is disabled") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebSearchHonorsRequiredProxyPolicy(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"query": "golang"})
	res := New().Run(context.Background(), input, tools.Context{Sandbox: tools.SandboxConfig{NetworkProxyRequired: true}})
	if !res.IsError || !strings.Contains(res.Content, "proxy.url is empty") {
		t.Fatalf("res = %+v", res)
	}
}

func TestWebSearchCombinesSandboxDomainPolicy(t *testing.T) {
	allowed := combinedAllowedDomains([]string{"example.com"}, []string{"docs.example.com"})
	results := filterResults([]Result{
		{Title: "Docs", URL: "https://docs.example.com/page"},
		{Title: "Other", URL: "https://other.example.com/page"},
	}, allowed, nil, 5)
	if len(results) != 1 || results[0].Title != "Docs" {
		t.Fatalf("allowed=%+v results=%+v", allowed, results)
	}
}

// TestWebSearchIgnoresNonResultLinks locks AUDIT-P1-15: parseResults matched every
// <a href> on the page, so the engine's own nav and footer chrome came back as
// search results.
func TestWebSearchIgnoresNonResultLinks(t *testing.T) {
	page := `<html><body>
	  <div class="header"><a href="/settings">Settings</a><a href="/params">All Regions</a></div>
	  <div class="result results_links web-result"><div class="links_main result__body">
	    <h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F&amp;rut=x">The Go Programming Language</a></h2>
	    <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F">Build simple, secure software.</a>
	  </div></div>
	  <div class="result results_links web-result"><div class="links_main result__body">
	    <h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2F&amp;rut=y">Go Packages</a></h2>
	  </div></div>
	  <div class="footer"><a href="/about">About</a><a href="https://duckduckgo.com/privacy">Privacy Policy</a></div>
	</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	t.Setenv("NIUMA_WEBSEARCH_URL", server.URL)

	input, _ := json.Marshal(map[string]any{"query": "golang", "limit": 10})
	res := New().Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("Run() error: %s", res.Content)
	}
	for _, chrome := range []string{"Settings", "All Regions", "About", "Privacy Policy"} {
		if strings.Contains(res.Content, chrome) {
			t.Errorf("navigation/footer link %q returned as a search result:\n%s", chrome, res.Content)
		}
	}
	for _, want := range []string{"The Go Programming Language", "https://go.dev/", "Go Packages", "https://pkg.go.dev/"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("real result %q missing:\n%s", want, res.Content)
		}
	}
}

// TestDescriptionStatesActualSource locks AUDIT-P1-15: results are scraped from
// DuckDuckGo's HTML page, not returned by a search API, and the caller deserves
// to know that.
func TestDescriptionStatesActualSource(t *testing.T) {
	description := strings.ToLower(New().Description())
	for _, want := range []string{"duckduckgo", "scrap", "not a search api"} {
		if !strings.Contains(description, want) {
			t.Errorf("Description() does not disclose %q:\n%s", want, New().Description())
		}
	}
}
