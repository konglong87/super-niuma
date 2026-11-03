package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/tools"
)

const maxBodyBytes = 2 * 1024 * 1024

type Tool struct {
	client *http.Client
}

func New() Tool {
	return Tool{client: &http.Client{Timeout: 30 * time.Second}}
}

func (t Tool) Name() string { return "WebFetch" }

func (t Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (t Tool) Description() string {
	return `Fetch a URL and return readable text content. Supports HTTP and HTTPS.

Use WebFetch to read full page content from a known URL — follow up after WebSearch
to get detailed information from a specific result.

HTML pages are automatically converted to plain text (scripts, styles, and tags removed).
Response body is limited to 2 MB. Non-HTML content (JSON, plain text) is returned as-is.

For searching the web without a specific URL, use WebSearch instead.`
}

func (t Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "url": {"type": "string", "description": "HTTP or HTTPS URL to fetch."}
	  },
	  "required": ["url"],
	  "additionalProperties": false
	}`)
}

func (t Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if !strings.HasPrefix(params.URL, "http://") && !strings.HasPrefix(params.URL, "https://") {
		return tools.Result{Content: "url must start with http:// or https://", IsError: true}
	}
	if err := tools.CheckNetworkURL(params.URL, toolContext.Sandbox); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params.URL, nil)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if len(body) > maxBodyBytes {
		return tools.Result{Content: fmt.Sprintf("response exceeds %d bytes", maxBodyBytes), IsError: true}
	}
	text := string(body)
	if strings.Contains(resp.Header.Get("content-type"), "html") || looksLikeHTML(text) {
		text = htmlToText(text)
	}
	return tools.Result{Content: fmt.Sprintf("Status: %s\nURL: %s\n\n%s", resp.Status, params.URL, strings.TrimSpace(text))}
}

func looksLikeHTML(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<body") || strings.Contains(lower, "<p")
}

var (
	scriptStyleRE = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRE         = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRE       = regexp.MustCompile(`[ \t\r\n]+`)
)

func htmlToText(input string) string {
	input = scriptStyleRE.ReplaceAllString(input, " ")
	input = tagRE.ReplaceAllString(input, " ")
	input = html.UnescapeString(input)
	input = spaceRE.ReplaceAllString(input, " ")
	return strings.TrimSpace(input)
}
