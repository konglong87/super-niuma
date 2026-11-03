package mcpresources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestReadUnknownServer(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"server": "missing", "uri": "file://x"})
	res := NewRead(nil).Run(context.Background(), input, tools.Context{})
	if !res.IsError || !strings.Contains(res.Content, "unknown MCP server") {
		t.Fatalf("result = %+v", res)
	}
}

// resourceServer is a minimal streamable-http MCP server that serves resources.
func resourceServer(t *testing.T, capabilities string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		reply := func(result string) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.FormatInt(req.ID, 10) + `,"result":` + result + "}\n"))
		}
		switch req.Method {
		case "initialize":
			reply(`{"protocolVersion":"2024-11-05","capabilities":` + capabilities + `}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list":
			reply(`{"resources":[{"uri":"file://notes.md","name":"notes"}]}`)
		case "resources/read":
			reply(`{"contents":[{"uri":"file://notes.md","text":"hello from mcp"}]}`)
		default:
			reply(`{}`)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func httpConfig(url string) map[string]config.MCPServerConfig {
	return map[string]config.MCPServerConfig{"docs": {Type: "http", URL: url}}
}

func TestListReturnsResourcesFromAConfiguredServer(t *testing.T) {
	url := resourceServer(t, `{"resources":{}}`)
	res := NewList(httpConfig(url)).Run(context.Background(), json.RawMessage(`{}`), tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Content, "file://notes.md") || !strings.Contains(res.Content, "docs") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestReadReturnsResourceText(t *testing.T) {
	url := resourceServer(t, `{"resources":{}}`)
	input, _ := json.Marshal(map[string]string{"server": "docs", "uri": "file://notes.md"})
	res := NewRead(httpConfig(url)).Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if res.Content != "hello from mcp" {
		t.Fatalf("content = %q", res.Content)
	}
}

// A server that never comes up must produce a labelled ERROR line rather than
// disappearing from the listing.
func TestListReportsUnreachableServers(t *testing.T) {
	configs := map[string]config.MCPServerConfig{
		"broken": {Command: "/nonexistent/mcp-server-binary"},
	}
	res := NewList(configs).Run(context.Background(), json.RawMessage(`{}`), tools.Context{})
	if !strings.Contains(res.Content, "broken ERROR") {
		t.Fatalf("content = %q", res.Content)
	}
}

// A tools-only server must say so plainly instead of leaking a raw JSON-RPC
// "method not found" into the listing (see AUDIT-P1-18 capability handling).
func TestListExplainsServersThatDoNotServeResources(t *testing.T) {
	url := resourceServer(t, `{"tools":{}}`)
	res := NewList(httpConfig(url)).Run(context.Background(), json.RawMessage(`{}`), tools.Context{})
	if !strings.Contains(res.Content, "does not support resources") {
		t.Fatalf("content = %q", res.Content)
	}
}

// The server filter must be honoured so one bad server cannot poison a
// targeted query.
func TestListFiltersByServerName(t *testing.T) {
	configs := httpConfig(resourceServer(t, `{"resources":{}}`))
	configs["other"] = config.MCPServerConfig{Command: "/nonexistent/mcp-server-binary"}

	input, _ := json.Marshal(map[string]string{"server": "docs"})
	res := NewList(configs).Run(context.Background(), input, tools.Context{})
	if strings.Contains(res.Content, "other") {
		t.Fatalf("filter leaked the other server: %q", res.Content)
	}
	if !strings.Contains(res.Content, "file://notes.md") {
		t.Fatalf("content = %q", res.Content)
	}
}

// The caller's context must reach the transport: a cancelled context has to
// abort the call instead of running it to completion.
func TestReadHonoursACancelledContext(t *testing.T) {
	url := resourceServer(t, `{"resources":{}}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	input, _ := json.Marshal(map[string]string{"server": "docs", "uri": "file://notes.md"})
	res := NewRead(httpConfig(url)).Run(ctx, input, tools.Context{})
	if !res.IsError {
		t.Fatalf("a cancelled context still produced a result: %+v", res)
	}
}

func TestListReportsNothingFoundWhenNoServersAreConfigured(t *testing.T) {
	res := NewList(nil).Run(context.Background(), json.RawMessage(`{}`), tools.Context{})
	if res.IsError || res.Content != "No MCP resources found" {
		t.Fatalf("result = %+v", res)
	}
}

func TestReadRejectsMalformedInput(t *testing.T) {
	res := NewRead(nil).Run(context.Background(), json.RawMessage(`{`), tools.Context{})
	if !res.IsError {
		t.Fatalf("result = %+v", res)
	}
}
