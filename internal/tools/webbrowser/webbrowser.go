package webbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/procenv"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Tool struct {
	client *http.Client
	mu     *sync.Mutex
	pages  map[string]*pageState
}

func New() Tool {
	return Tool{client: &http.Client{Timeout: 20 * time.Second}, mu: &sync.Mutex{}, pages: map[string]*pageState{}}
}

func (t Tool) Name() string { return "WebBrowser" }

func (t Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (t Tool) Description() string {
	return `Inspect and interact with web pages: navigate/open, click links, input form values, submit forms, and extract text/links/forms.

Use WebBrowser for interactive web exploration: filling forms, clicking links, navigating
multi-page sites. For simple URL content retrieval, prefer WebFetch instead — it's faster
and doesn't require session management.

Two modes. By default this runs in fallback mode: it fetches HTML over HTTP and parses it,
so there is no JavaScript execution, no layout, and no rendering — screenshot is refused
with instructions rather than faked. Real browser mode (JavaScript and screenshots) needs
NIUMA_WEBBROWSER_MODE=playwright plus NIUMA_PLAYWRIGHT_RUNNER
pointing at scripts/playwright-browser-runner.mjs.

Actions: open/navigate (load a URL), text (extract page text), links (list links),
forms (list forms), click (follow a link), input (set form value), submit (submit form),
screenshot (real browser mode only). Sessions persist across actions.`
}

func (t Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "action": {"type": "string", "enum": ["open", "navigate", "text", "links", "forms", "click", "input", "submit", "screenshot"], "description": "Browser action. open/navigate returns a compact page snapshot. screenshot requires real browser mode and errors out otherwise."},
	    "url": {"type": "string", "description": "HTTP or HTTPS URL to inspect."},
	    "session_id": {"type": "string", "description": "Optional browser session id. Defaults to default."},
	    "selector": {"type": "string", "description": "Link text, href substring, form index, input name, or #id-like selector."},
	    "value": {"type": "string", "description": "Input value for input action."},
	    "limit": {"type": "integer", "description": "Maximum text characters or extracted items. Default 50 for items, 4000 for text."}
	  },
	  "required": ["action"],
	  "additionalProperties": false
	}`)
}

type browserParams struct {
	Action    string `json:"action"`
	URL       string `json:"url"`
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
	Value     string `json:"value"`
	Limit     int    `json:"limit"`
}

func (t Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params browserParams
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	sessionID := firstNonEmpty(params.SessionID, "default")
	action := strings.ToLower(strings.TrimSpace(params.Action))
	if shouldUseBrowserAdapter() {
		return t.runBrowserAdapter(ctx, params, toolContext.Sandbox)
	}
	var page pageSnapshot
	if action == "open" || action == "navigate" {
		if strings.TrimSpace(params.URL) == "" {
			return tools.Result{Content: "url is required for open/navigate", IsError: true}
		}
		next, err := t.navigate(ctx, sessionID, params.URL, toolContext.Sandbox)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		page = next
	} else {
		state := t.state(sessionID)
		if state == nil {
			return tools.Result{Content: "browser session not found: " + sessionID, IsError: true}
		}
		page = state.Page
	}
	switch action {
	case "open", "navigate":
		if params.Limit <= 0 {
			params.Limit = 4000
		}
		page.Text = truncateRunes(page.Text, params.Limit)
		return encode(page)
	case "text":
		if params.Limit <= 0 {
			params.Limit = 4000
		}
		return tools.Result{Content: truncateRunes(page.Text, params.Limit)}
	case "links":
		if params.Limit <= 0 {
			params.Limit = 50
		}
		return encode(limitSlice(page.Links, params.Limit))
	case "forms":
		if params.Limit <= 0 {
			params.Limit = 50
		}
		return encode(limitSlice(page.Forms, params.Limit))
	case "click":
		nextURL := linkTarget(page, params.Selector)
		if nextURL == "" {
			return tools.Result{Content: "click target not found: " + params.Selector, IsError: true}
		}
		next, err := t.navigate(ctx, sessionID, nextURL, toolContext.Sandbox)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		return encode(next)
	case "input":
		if strings.TrimSpace(params.Selector) == "" {
			return tools.Result{Content: "selector is required for input", IsError: true}
		}
		t.setInput(sessionID, params.Selector, params.Value)
		return encode(map[string]any{"session_id": sessionID, "selector": params.Selector, "value_set": true})
	case "submit":
		next, err := t.submit(ctx, sessionID, params.Selector, toolContext.Sandbox)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		return encode(next)
	case "screenshot":
		return tools.Result{Content: screenshotUnavailable, IsError: true}
	default:
		return tools.Result{Content: "unsupported WebBrowser action: " + params.Action, IsError: true}
	}
}

func (t Tool) navigate(ctx context.Context, sessionID, rawURL string, sandbox tools.SandboxConfig) (pageSnapshot, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return pageSnapshot{}, fmt.Errorf("url must be http or https")
	}
	if err := tools.CheckNetworkURL(parsed.String(), sandbox); err != nil {
		return pageSnapshot{}, err
	}
	html, status, err := t.fetch(ctx, parsed.String(), sandbox)
	if err != nil {
		return pageSnapshot{}, err
	}
	page := parsePage(parsed.String(), status, html)
	page.SessionID = sessionID
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pages[sessionID] = &pageState{Page: page, Inputs: map[string]string{}}
	return page, nil
}

func (t Tool) state(sessionID string) *pageState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pages == nil {
		return nil
	}
	state := t.pages[sessionID]
	if state == nil {
		return nil
	}
	copyState := *state
	copyState.Inputs = map[string]string{}
	for key, value := range state.Inputs {
		copyState.Inputs[key] = value
	}
	return &copyState
}

func (t Tool) setInput(sessionID, selector, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pages == nil {
		t.pages = map[string]*pageState{}
	}
	state := t.pages[sessionID]
	if state == nil {
		state = &pageState{Inputs: map[string]string{}}
		t.pages[sessionID] = state
	}
	if state.Inputs == nil {
		state.Inputs = map[string]string{}
	}
	state.Inputs[strings.TrimPrefix(selector, "#")] = value
}

func (t Tool) submit(ctx context.Context, sessionID, selector string, sandbox tools.SandboxConfig) (pageSnapshot, error) {
	state := t.state(sessionID)
	if state == nil {
		return pageSnapshot{}, fmt.Errorf("browser session not found: %s", sessionID)
	}
	form := selectForm(state.Page.Forms, selector)
	if form.Action == "" {
		return pageSnapshot{}, fmt.Errorf("form target not found: %s", selector)
	}
	target, err := url.Parse(form.Action)
	if err != nil {
		return pageSnapshot{}, err
	}
	if err := tools.CheckNetworkURL(target.String(), sandbox); err != nil {
		return pageSnapshot{}, err
	}
	values := url.Values{}
	for _, input := range form.Inputs {
		if value, ok := state.Inputs[input]; ok {
			values.Set(input, value)
		}
	}
	method := strings.ToUpper(firstNonEmpty(form.Method, "GET"))
	if method == "POST" {
		html, status, err := t.postForm(ctx, target.String(), values, sandbox)
		if err != nil {
			return pageSnapshot{}, err
		}
		page := parsePage(target.String(), status, html)
		page.SessionID = sessionID
		t.mu.Lock()
		t.pages[sessionID] = &pageState{Page: page, Inputs: map[string]string{}}
		t.mu.Unlock()
		return page, nil
	}
	target.RawQuery = values.Encode()
	return t.navigate(ctx, sessionID, target.String(), sandbox)
}

func (t Tool) fetch(ctx context.Context, target string, sandbox tools.SandboxConfig) (string, int, error) {
	client := t.client
	if client == nil {
		client = http.DefaultClient
	}
	var err error
	client, err = tools.NetworkHTTPClient(client, sandbox)
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "niuma-webbrowser/1.0")
	res, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
	if err != nil {
		return "", res.StatusCode, err
	}
	return string(data), res.StatusCode, nil
}

func (t Tool) postForm(ctx context.Context, target string, values url.Values, sandbox tools.SandboxConfig) (string, int, error) {
	client := t.client
	if client == nil {
		client = http.DefaultClient
	}
	var err error
	client, err = tools.NetworkHTTPClient(client, sandbox)
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(values.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "niuma-webbrowser/1.0")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
	if err != nil {
		return "", res.StatusCode, err
	}
	return string(data), res.StatusCode, nil
}

type browserAdapterRequest struct {
	Action    string `json:"action"`
	URL       string `json:"url,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Value     string `json:"value,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type browserAdapterResponse struct {
	Content  string          `json:"content,omitempty"`
	IsError  bool            `json:"is_error,omitempty"`
	Page     *pageSnapshot   `json:"page,omitempty"`
	Snapshot *pageSnapshot   `json:"snapshot,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

func shouldUseBrowserAdapter() bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("NIUMA_WEBBROWSER_MODE")))
	runner := strings.TrimSpace(os.Getenv("NIUMA_PLAYWRIGHT_RUNNER"))
	switch mode {
	case "playwright", "browser", "real", "chromium":
		return runner != ""
	case "auto":
		return runner != ""
	default:
		return false
	}
}

func (t Tool) runBrowserAdapter(ctx context.Context, params browserParams, sandbox tools.SandboxConfig) tools.Result {
	action := strings.ToLower(strings.TrimSpace(params.Action))
	if action == "open" || action == "navigate" {
		if strings.TrimSpace(params.URL) == "" {
			return tools.Result{Content: "url is required for open/navigate", IsError: true}
		}
		parsed, err := url.Parse(strings.TrimSpace(params.URL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return tools.Result{Content: "url must be http or https", IsError: true}
		}
		if err := tools.CheckNetworkURL(parsed.String(), sandbox); err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		params.URL = parsed.String()
	}
	runner := strings.TrimSpace(os.Getenv("NIUMA_PLAYWRIGHT_RUNNER"))
	if runner == "" {
		return tools.Result{Content: "NIUMA_PLAYWRIGHT_RUNNER is required for real browser mode", IsError: true}
	}
	req := browserAdapterRequest{
		Action:    action,
		URL:       params.URL,
		SessionID: firstNonEmpty(params.SessionID, "default"),
		Selector:  params.Selector,
		Value:     params.Value,
		Limit:     params.Limit,
	}
	payload, _ := json.Marshal(req)
	out, err := runAdapterCommand(ctx, runner, payload)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return t.decodeAdapterOutput(req.SessionID, out)
}

func runAdapterCommand(ctx context.Context, runner string, payload []byte) ([]byte, error) {
	shell, shellArg := "/bin/sh", "-c"
	if runtime.GOOS == "windows" {
		shell, shellArg = "cmd.exe", "/C"
	}
	cmd := exec.CommandContext(ctx, shell, shellArg, runner)
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = procenv.Sanitized("CLAUDE_BROWSER_REQUEST=" + string(payload))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("web browser adapter failed: %w\n%s", err, truncateRunes(string(out), 4000))
	}
	return out, nil
}

func (t Tool) decodeAdapterOutput(sessionID string, out []byte) tools.Result {
	raw := json.RawMessage(strings.TrimSpace(string(lastJSONLine(out))))
	if len(raw) == 0 {
		return tools.Result{Content: "web browser adapter returned empty output", IsError: true}
	}
	var res browserAdapterResponse
	if err := json.Unmarshal(raw, &res); err == nil {
		if res.Content != "" {
			return tools.Result{Content: res.Content, IsError: res.IsError}
		}
		if len(res.Data) > 0 {
			return tools.Result{Content: string(res.Data), IsError: res.IsError}
		}
		if res.Page != nil {
			page := *res.Page
			page.SessionID = firstNonEmpty(page.SessionID, sessionID)
			t.storePage(page)
			return encode(page)
		}
		if res.Snapshot != nil {
			page := *res.Snapshot
			page.SessionID = firstNonEmpty(page.SessionID, sessionID)
			t.storePage(page)
			return encode(page)
		}
		if res.IsError {
			return tools.Result{Content: string(raw), IsError: true}
		}
	}
	var page pageSnapshot
	if err := json.Unmarshal(raw, &page); err == nil && page.URL != "" {
		page.SessionID = firstNonEmpty(page.SessionID, sessionID)
		t.storePage(page)
		return encode(page)
	}
	return tools.Result{Content: string(raw)}
}

func (t Tool) storePage(page pageSnapshot) {
	if strings.TrimSpace(page.SessionID) == "" {
		page.SessionID = "default"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pages == nil {
		t.pages = map[string]*pageState{}
	}
	t.pages[page.SessionID] = &pageState{Page: page, Inputs: map[string]string{}}
}

func lastJSONLine(out []byte) []byte {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
			return []byte(line)
		}
	}
	return []byte(strings.TrimSpace(string(out)))
}

type pageState struct {
	Page   pageSnapshot
	Inputs map[string]string
}

type pageSnapshot struct {
	SessionID string          `json:"session_id,omitempty"`
	URL       string          `json:"url"`
	Status    int             `json:"status"`
	Title     string          `json:"title,omitempty"`
	Text      string          `json:"text,omitempty"`
	Links     []link          `json:"links,omitempty"`
	Forms     []form          `json:"forms,omitempty"`
	Console   []string        `json:"console,omitempty"`
	Network   []networkRecord `json:"network,omitempty"`
}

type link struct {
	Text string `json:"text,omitempty"`
	Href string `json:"href"`
}

type form struct {
	Action string   `json:"action,omitempty"`
	Method string   `json:"method,omitempty"`
	Inputs []string `json:"inputs,omitempty"`
}

type networkRecord struct {
	Method string `json:"method,omitempty"`
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
}

var (
	titleRE  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	anchorRE = regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	formRE   = regexp.MustCompile(`(?is)<form\b([^>]*)>(.*?)</form>`)
	inputRE  = regexp.MustCompile(`(?is)<(?:input|textarea|select)\b([^>]*)>`)
	attrRE   = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*["']([^"']*)["']`)
	tagRE    = regexp.MustCompile(`(?is)<script\b.*?</script>|<style\b.*?</style>|<[^>]+>`)
	spaceRE  = regexp.MustCompile(`\s+`)
)

func parsePage(target string, status int, html string) pageSnapshot {
	return pageSnapshot{
		URL:    target,
		Status: status,
		Title:  htmlText(firstMatch(titleRE, html)),
		Text:   htmlText(tagRE.ReplaceAllString(html, " ")),
		Links:  parseLinks(target, html),
		Forms:  parseForms(target, html),
	}
}

func linkTarget(page pageSnapshot, selector string) string {
	selector = strings.TrimSpace(strings.TrimPrefix(selector, "#"))
	for _, link := range page.Links {
		if selector == "" || strings.EqualFold(strings.TrimSpace(link.Text), selector) || strings.Contains(link.Href, selector) {
			return link.Href
		}
	}
	return ""
}

func selectForm(forms []form, selector string) form {
	selector = strings.TrimSpace(strings.TrimPrefix(selector, "#"))
	if selector == "" && len(forms) > 0 {
		return forms[0]
	}
	for i, form := range forms {
		if selector == fmt.Sprint(i) || strings.Contains(form.Action, selector) {
			return form
		}
	}
	return form{}
}

// screenshotUnavailable explains why the fallback fetcher cannot produce an image.
// It used to synthesise an SVG holding nothing but the page title and URL and return
// it as a data URL, so the model believed it was looking at the rendered page
// (AUDIT-P1-14). Nothing is ever rendered here, so say so.
const screenshotUnavailable = `screenshot is unavailable: WebBrowser is running in fallback mode, which fetches HTML over HTTP and never renders a page, so there is no image to capture.

To capture real screenshots, enable real browser mode:
  1. export NIUMA_WEBBROWSER_MODE=playwright
  2. export NIUMA_PLAYWRIGHT_RUNNER="node /path/to/scripts/playwright-browser-runner.mjs"

Without that, use action=text or action=links to inspect the page content instead.`

func parseLinks(baseURL, html string) []link {
	var out []link
	for _, match := range anchorRE.FindAllStringSubmatch(html, -1) {
		href := absolutize(baseURL, htmlText(match[1]))
		if href == "" {
			continue
		}
		out = append(out, link{Text: htmlText(tagRE.ReplaceAllString(match[2], " ")), Href: href})
	}
	return out
}

func parseForms(baseURL, html string) []form {
	var out []form
	for _, match := range formRE.FindAllStringSubmatch(html, -1) {
		formAttrs := attrs(match[1])
		f := form{Action: absolutize(baseURL, formAttrs["action"]), Method: strings.ToUpper(firstNonEmpty(formAttrs["method"], "GET"))}
		for _, inputMatch := range inputRE.FindAllStringSubmatch(match[2], -1) {
			inputAttrs := attrs(inputMatch[1])
			name := firstNonEmpty(inputAttrs["name"], inputAttrs["id"], inputAttrs["type"])
			if name != "" {
				f.Inputs = append(f.Inputs, name)
			}
		}
		out = append(out, f)
	}
	return out
}

func attrs(raw string) map[string]string {
	out := map[string]string{}
	for _, match := range attrRE.FindAllStringSubmatch(raw, -1) {
		out[strings.ToLower(match[1])] = htmlText(match[2])
	}
	return out
}

func firstMatch(re *regexp.Regexp, text string) string {
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func htmlText(text string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ")
	return strings.TrimSpace(spaceRE.ReplaceAllString(replacer.Replace(text), " "))
}

func absolutize(baseURL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "javascript:") {
		return ""
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return raw
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return base.ResolveReference(ref).String()
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + fmt.Sprintf("\n[truncated after %d chars]", limit)
}

func limitSlice[T any](items []T, limit int) []T {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func encode(value any) tools.Result {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}
