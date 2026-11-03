package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/product"
	"github.com/konglong87/super-niuma/internal/tools"
)

const (
	maxSearchBodyBytes = 2 * 1024 * 1024
	webSearchURLEnv    = "NIUMA_WEBSEARCH_URL"
)

type Tool struct {
	client   *http.Client
	endpoint string
}

type Result struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func New() Tool {
	return Tool{client: &http.Client{Timeout: 30 * time.Second}}
}

func NewWithEndpoint(endpoint string) Tool {
	tool := New()
	tool.endpoint = strings.TrimSpace(endpoint)
	return tool
}

func (t Tool) Name() string { return "WebSearch" }

func (t Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (t Tool) Description() string {
	return `Search the web and return a concise list of result titles and URLs.

Use when you need current information not available in the local codebase:
documentation, news, package versions, API references, error solutions.

This is not a search API: it scrapes DuckDuckGo's HTML results page and reads the
result links out of it, so results carry only a title and a URL — no snippets, no
ranking signals, no result count — and an engine-side layout change can degrade
them. Set NIUMA_WEBSEARCH_URL to point at a different endpoint.

For full page content, follow up with WebFetch. Domain filtering uses the current
network sandbox policy. For specialized searches (code, documentation, technical
references), consider WebFetch with known documentation URLs instead.`
}

func (t Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "query": {"type": "string", "description": "Search query."},
	    "limit": {"type": "integer", "description": "Maximum results, default 5."},
	    "allowed_domains": {"type": "array", "items": {"type": "string"}, "description": "Optional domains to include."},
	    "blocked_domains": {"type": "array", "items": {"type": "string"}, "description": "Optional domains to exclude."}
	  },
	  "required": ["query"],
	  "additionalProperties": false
	}`)
}

func (t Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Query          string   `json:"query"`
		Limit          int      `json:"limit"`
		AllowedDomains []string `json:"allowed_domains"`
		BlockedDomains []string `json:"blocked_domains"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if strings.TrimSpace(params.Query) == "" {
		return tools.Result{Content: "query is required", IsError: true}
	}
	if params.Limit <= 0 || params.Limit > 10 {
		params.Limit = 5
	}
	endpoint := strings.TrimSpace(product.Getenv(webSearchURLEnv))
	if endpoint == "" {
		endpoint = strings.TrimSpace(t.endpoint)
	}
	if endpoint == "" {
		endpoint = "https://duckduckgo.com/html/"
	}
	reqURL, err := buildSearchURL(endpoint, params.Query)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if err := tools.CheckNetworkURL(reqURL, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	req.Header.Set("user-agent", "niuma/0.1")
	client, err := tools.NetworkHTTPClient(t.client, toolContext.Sandbox)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	resp, err := client.Do(req)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes+1))
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if len(body) > maxSearchBodyBytes {
		return tools.Result{Content: fmt.Sprintf("response exceeds %d bytes", maxSearchBodyBytes), IsError: true}
	}
	results := filterResults(parseResults(string(body), endpoint, params.Limit*4), combinedAllowedDomains(params.AllowedDomains, toolContext.Sandbox.NetworkAllowDomains), append(append([]string(nil), params.BlockedDomains...), toolContext.Sandbox.NetworkDenyDomains...), params.Limit)
	if len(results) == 0 {
		return tools.Result{Content: "No search results found"}
	}
	var lines []string
	for i, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s\n%s", i+1, result.Title, result.URL))
	}
	return tools.Result{Content: strings.Join(lines, "\n")}
}

func combinedAllowedDomains(requested, sandbox []string) []string {
	if len(requested) == 0 {
		return append([]string(nil), sandbox...)
	}
	if len(sandbox) == 0 {
		return append([]string(nil), requested...)
	}
	var out []string
	seen := map[string]bool{}
	add := func(domain string) {
		domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain != "" && !seen[domain] {
			out = append(out, domain)
			seen[domain] = true
		}
	}
	for _, domain := range requested {
		if tools.DomainMatches(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "."), sandbox) {
			add(domain)
		}
	}
	for _, domain := range sandbox {
		if tools.DomainMatches(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "."), requested) {
			add(domain)
		}
	}
	if len(out) == 0 {
		return []string{"__no_domain_allowed__"}
	}
	return out
}

func filterResults(results []Result, allowedDomains, blockedDomains []string, limit int) []Result {
	var out []Result
	for _, result := range results {
		if matchesAnyDomain(result.URL, blockedDomains) {
			continue
		}
		if len(allowedDomains) > 0 && !matchesAnyDomain(result.URL, allowedDomains) {
			continue
		}
		out = append(out, result)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func matchesAnyDomain(rawURL string, domains []string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		domain = strings.TrimPrefix(domain, ".")
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func buildSearchURL(endpoint, query string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

var anchorRE = regexp.MustCompile(`(?is)<a\s([^>]*)>(.*?)</a>`)
var hrefAttrRE = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
var classAttrRE = regexp.MustCompile(`(?is)\bclass\s*=\s*["']([^"']*)["']`)
var tagRE = regexp.MustCompile(`(?s)<[^>]+>`)

// parseResults extracts search results from a results page.
//
// It used to match every <a href> on the page, so the engine's own navigation
// and footer came back as search results (AUDIT-P1-15). DuckDuckGo's HTML
// endpoint tags result links with class="result__a"; when that marker is present
// only those anchors count. A custom endpoint without the marker falls back to
// absolute off-site links, which at least excludes relative site chrome and
// links back to the engine itself.
func parseResults(body, endpoint string, limit int) []Result {
	anchors := anchorRE.FindAllStringSubmatch(body, -1)
	var scoped [][]string
	for _, match := range anchors {
		if class := classAttrRE.FindStringSubmatch(match[1]); class != nil && strings.Contains(class[1], "result__a") {
			scoped = append(scoped, match)
		}
	}
	if len(scoped) > 0 {
		anchors = scoped
	}
	var engineHost string
	if parsed, err := url.Parse(endpoint); err == nil {
		engineHost = parsed.Host
	}

	var results []Result
	for _, match := range anchors {
		hrefAttr := hrefAttrRE.FindStringSubmatch(match[1])
		if hrefAttr == nil {
			continue
		}
		href := html.UnescapeString(hrefAttr[1])
		title := cleanTitle(match[2])
		if title == "" {
			continue
		}
		if resolved := extractDuckDuckGoURL(href); resolved != "" {
			href = resolved
		}
		parsed, err := url.Parse(href)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		if engineHost != "" && strings.EqualFold(parsed.Host, engineHost) {
			continue
		}
		results = append(results, Result{Title: title, URL: href})
		if len(results) >= limit {
			break
		}
	}
	return results
}

func cleanTitle(input string) string {
	input = tagRE.ReplaceAllString(input, " ")
	input = html.UnescapeString(input)
	return strings.Join(strings.Fields(input), " ")
}

func extractDuckDuckGoURL(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if raw := u.Query().Get("uddg"); raw != "" {
		return raw
	}
	return ""
}
