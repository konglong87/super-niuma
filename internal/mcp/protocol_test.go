package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

// compliantServer mimics a spec-compliant streamable-http server: it issues a
// session id at initialize and rejects every later request that fails to
// replay it.
type compliantServer struct {
	protocolVersion string

	mu       sync.Mutex
	requests []*http.Request
}

func (s *compliantServer) handler(t *testing.T) http.HandlerFunc {
	const sessionID = "sess-abc123"
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Clone(context.Background()))
		s.mu.Unlock()

		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		if req.Method == "initialize" {
			w.Header().Set(sessionIDHeader, sessionID)
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{
				"protocolVersion": s.protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
			})})
			return
		}
		if r.Header.Get(sessionIDHeader) != sessionID {
			// This is exactly what a compliant server does, and what the old
			// client tripped over on its very second call.
			http.Error(w, "missing or unknown session id", http.StatusNotFound)
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{
			"tools": []map[string]any{{"name": "echo"}},
		})})
	}
}

func (s *compliantServer) headerValues(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, req := range s.requests {
		out = append(out, req.Header.Get(name))
	}
	return out
}

// AUDIT-P1-18: without Mcp-Session-Id a compliant server rejects the second
// call outright, so the client was unusable against real streamable-http.
func TestHTTPClientReplaysTheSessionIdIssuedAtInitialize(t *testing.T) {
	shortTimeouts(t, testBudget())
	server := &compliantServer{protocolVersion: protocolVersion}
	httpServer := httptest.NewServer(server.handler(t))
	defer httpServer.Close()

	client, err := StartHTTP(t.Context(), "compliant", HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	tools, err := client.ListToolsContext(t.Context())
	if err != nil {
		t.Fatalf("tools/list was rejected for want of a session id: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	sessions := server.headerValues(sessionIDHeader)
	if len(sessions) < 2 || sessions[0] != "" {
		t.Fatalf("session headers = %q (initialize must not carry one)", sessions)
	}
	for i, value := range sessions[1:] {
		if value != "sess-abc123" {
			t.Fatalf("request %d did not replay the session id: %q", i+1, sessions)
		}
	}
}

// The client must adopt the version the server answered with, and advertise it
// on later requests. The old client hardcoded 2024-11-05 and threw the
// initialize result away without reading it.
func TestHTTPClientAdoptsTheProtocolVersionTheServerAnswered(t *testing.T) {
	shortTimeouts(t, testBudget())
	server := &compliantServer{protocolVersion: "2025-06-18"}
	httpServer := httptest.NewServer(server.handler(t))
	defer httpServer.Close()

	client, err := StartHTTP(t.Context(), "modern", HTTPConfig{URL: httpServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListToolsContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	versions := server.headerValues(protocolVersionHeader)
	if len(versions) < 2 {
		t.Fatalf("expected at least two requests, got %q", versions)
	}
	if versions[0] != "" {
		t.Fatalf("initialize must not carry a negotiated version yet: %q", versions)
	}
	for i, value := range versions[1:] {
		if value != "2025-06-18" {
			t.Fatalf("request %d advertised %q, want the negotiated 2025-06-18 (all: %q)", i+1, value, versions)
		}
	}
}

// The HTTP path had a blanket 30s http.Client.Timeout, which is the same
// mistake AUDIT-P0-07 fixed for provider streaming: it kills long but healthy
// tool calls. tools/call must get the long idle budget while metadata calls
// stay on the short one.
func TestHTTPToolCallGetsTheIdleBudgetNotTheRequestBudget(t *testing.T) {
	budget := testBudget()
	budget.request = 150 * time.Millisecond
	budget.toolCallIdle = 5 * time.Second
	shortTimeouts(t, budget)

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{})})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case methodToolsCall:
			time.Sleep(600 * time.Millisecond) // 4x the metadata budget
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "done"}},
			})})
		default:
			time.Sleep(600 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{})})
		}
	}))
	defer slow.Close()

	client, err := StartHTTP(t.Context(), "slow", HTTPConfig{URL: slow.URL})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := client.CallToolWithCallback(t.Context(), "slow", nil, nil)
	if err != nil {
		t.Fatalf("a 600ms tool call was killed by the 150ms metadata budget: %v", err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	// The same 600ms on a metadata call must still be cut off.
	if _, err := client.ListToolsContext(t.Context()); err == nil {
		t.Fatal("a slow metadata call should have timed out")
	}
}

// http.NewRequest ignored the caller's context entirely.
func TestHTTPCallStopsWhenTheCallerCancels(t *testing.T) {
	budget := testBudget()
	budget.toolCallIdle = time.Hour
	shortTimeouts(t, budget)

	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "initialize" {
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{})})
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		<-release
	}))
	defer hang.Close()
	defer close(release)

	client, err := StartHTTP(t.Context(), "hang", HTTPConfig{URL: hang.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := client.CallToolWithCallback(ctx, "wedged", nil, nil)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the cancelled HTTP call to fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the context did not abort the HTTP request")
	}
}

// User cancellation and a timeout must stay distinguishable on the HTTP path
// too: the caller ctx is wrapped in WithTimeout, so a naive `ctx.Err() != nil`
// reports an ESC as "the server did not answer in time".
func TestHTTPCancellationIsNotReportedAsATimeout(t *testing.T) {
	budget := testBudget()
	budget.toolCallIdle = time.Hour
	shortTimeouts(t, budget)

	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{})})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			<-release
		}
	}))
	defer hang.Close()
	defer close(release)

	client, err := StartHTTP(t.Context(), "hang", HTTPConfig{URL: hang.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := client.CallToolWithCallback(ctx, "wedged", nil, nil)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	err = receive(t, done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; errors.Is(err, context.Canceled) must hold for a user cancellation", err)
	}
	if errors.Is(err, errMCPTimeout) {
		t.Fatalf("a cancellation was mislabelled as a timeout: %v", err)
	}
}

// The other half of the same contract: a real deadline must still say timeout.
func TestHTTPTimeoutIsStillReportedAsATimeout(t *testing.T) {
	budget := testBudget()
	budget.toolCallIdle = 150 * time.Millisecond
	shortTimeouts(t, budget)

	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(map[string]any{})})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			<-release
		}
	}))
	defer hang.Close()
	defer close(release)

	client, err := StartHTTP(t.Context(), "slow", HTTPConfig{URL: hang.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.CallToolWithCallback(context.Background(), "wedged", nil, nil)
	if !errors.Is(err, errMCPTimeout) {
		t.Fatalf("err = %v, want errMCPTimeout", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("a timeout must not look like a user cancellation: %v", err)
	}
}

// A server whose tool list spans pages must be read to the end; the old client
// silently exposed only the first page.
func TestListToolsFollowsThePaginationCursor(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "paged", "paginated-tools")

	tools, err := client.ListToolsContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "first" || tools[1].Name != "second" {
		t.Fatalf("tools = %+v, want both pages", tools)
	}
}

// Pagination must not be able to spin forever on a server that keeps handing
// back the same cursor.
func TestPaginationStopsOnARepeatedCursor(t *testing.T) {
	pages := 0
	err := paginate(func(string) (string, error) {
		pages++
		return "same-cursor", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 {
		t.Fatalf("pages = %d, want 2 (the repeat must stop it)", pages)
	}
}

// Non-text content used to be dropped on the floor, so a tool that answers
// with an image looked to the model like it had returned nothing at all.
func TestCallToolKeepsNonTextContentVisible(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "imager", "image-tool")

	out, isError, err := client.CallToolWithCallback(t.Context(), "screenshot", nil, nil)
	if err != nil || isError {
		t.Fatalf("out=%q isError=%v err=%v", out, isError, err)
	}
	if !strings.Contains(out, "here it is") {
		t.Fatalf("text content was lost: %q", out)
	}
	if !strings.Contains(out, "image") || !strings.Contains(out, "image/png") {
		t.Fatalf("image content was silently dropped: %q", out)
	}
}

// safeName is not injective: `get-item` and `get.item` both sanitize to
// `get_item`, and the tool registry keeps only the last one registered.
func TestLoadToolsDisambiguatesCollidingToolNames(t *testing.T) {
	shortTimeouts(t, testBudget())
	captureLogs(t)

	loaded, cleanup := LoadTools(t.Context(), map[string]config.MCPServerConfig{
		"db": stubServerConfig("colliding-tools"),
	})
	defer cleanup()

	names := toolNames(loaded)
	if len(names) != 2 {
		t.Fatalf("names = %v, want 2 tools", names)
	}
	if names[0] == names[1] {
		t.Fatalf("both tools share the name %q; one would silently overwrite the other", names[0])
	}
	if names[0] != "mcp__db__get_item" {
		t.Fatalf("names = %v", names)
	}
}

// Each adapter must still call the tool by its *server-side* name, so
// disambiguation cannot break invocation.
func TestRenamedToolStillCallsItsOriginalServerSideName(t *testing.T) {
	shortTimeouts(t, testBudget())
	captureLogs(t)

	loaded, cleanup := LoadTools(t.Context(), map[string]config.MCPServerConfig{
		"db": stubServerConfig("colliding-tools"),
	})
	defer cleanup()

	renamed, ok := loaded[1].(ToolAdapter)
	if !ok {
		t.Fatalf("unexpected tool type %T", loaded[1])
	}
	if renamed.ToolInfo.Name != "get.item" {
		t.Fatalf("renaming clobbered the server-side name: %q", renamed.ToolInfo.Name)
	}
	if renamed.Name() == "mcp__db__get_item" {
		t.Fatal("the second tool was not renamed")
	}
}

// The initialize result used to be unmarshalled into a json.RawMessage and
// thrown away, so the client never knew what the server could do. It matters:
// ListMcpResources walks every configured server, and one that only serves
// tools answers resources/list with a bare "-32601 Method not found" that
// surfaces to the user as an unexplained ERROR line.
func TestListResourcesReportsUnsupportedInsteadOfARawMethodNotFound(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "tools-only", "ok")

	_, err := client.ListResourcesContext(t.Context())
	if err == nil {
		t.Fatal("expected an error from a server that does not serve resources")
	}
	if !strings.Contains(err.Error(), "does not support resources") {
		t.Fatalf("error does not explain the capability gap: %v", err)
	}
}

func TestListResourcesWorksWhenTheServerDeclaresTheCapability(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "with-resources", "with-resources")

	resources, err := client.ListResourcesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].URI != "file://a" {
		t.Fatalf("resources = %+v", resources)
	}
}

// A streamable-http server may interleave notifications ahead of the actual
// answer in one SSE body. Taking the first data event blindly means parsing a
// progress ping as if it were the response.
func TestSSEResponsePicksTheAnswerNotAnEarlierNotification(t *testing.T) {
	shortTimeouts(t, testBudget())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := `{}`
		if req.Method == "tools/list" {
			result = `{"tools":[{"name":"echo"}]}`
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: " +
			`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}` + "\n\n"))
		_, _ = w.Write([]byte("event: message\ndata: " +
			`{"jsonrpc":"2.0","id":` + strconv.FormatInt(req.ID, 10) + `,"result":` + result + "}\n\n"))
	}))
	defer server.Close()

	client, err := StartHTTP(t.Context(), "chatty", HTTPConfig{URL: server.URL})
	if err != nil {
		t.Fatalf("initialize misread a progress notification as the answer: %v", err)
	}
	tools, err := client.ListToolsContext(t.Context())
	if err != nil {
		t.Fatalf("tools/list misread a progress notification as the answer: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
}

// The legacy two-endpoint HTTP+SSE transport is not implemented; saying so
// beats silently treating it as streamable-http.
func TestSSETransportIsRejectedWithAnActionableMessage(t *testing.T) {
	_, err := StartConfigured(t.Context(), "legacy", config.MCPServerConfig{Type: "sse", URL: "http://example.invalid"})
	if err == nil {
		t.Fatal("expected the legacy SSE transport to be refused")
	}
	if !strings.Contains(err.Error(), "not implemented") || !strings.Contains(err.Error(), "streamable-http") {
		t.Fatalf("error is not actionable: %v", err)
	}
}
