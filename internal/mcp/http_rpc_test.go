package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestStartHTTPAndListTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-test") != "yes" {
			t.Fatalf("missing header")
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": protocolVersion}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}}}}
		case "prompts/list":
			result = map[string]any{"prompts": []map[string]any{{"name": "review", "description": "Review code", "arguments": []map[string]any{{"name": "topic", "required": true}}}}}
		case "prompts/get":
			result = map[string]any{
				"description": "Review code",
				"messages": []map[string]any{{
					"role":    "user",
					"content": map[string]any{"type": "text", "text": "Review this code"},
				}},
			}
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(result)})
	}))
	defer server.Close()

	client, err := StartConfigured(t.Context(), "http", config.MCPServerConfig{Type: "http", URL: server.URL, Headers: map[string]string{"x-test": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	prompts, err := client.ListPrompts()
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Name != "review" || len(prompts[0].Arguments) != 1 {
		t.Fatalf("prompts = %+v", prompts)
	}
	prompt, err := client.GetPrompt("review", map[string]string{"topic": "code"})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Description != "Review code" || len(prompt.Messages) != 1 || prompt.Messages[0].Content.Text != "Review this code" {
		t.Fatalf("prompt = %+v", prompt)
	}
}

func TestStartHTTPParsesSSEResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		if req.Method == "tools/list" {
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}}}}
		}
		w.Header().Set("content-type", "text/event-stream")
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRaw(result)}
		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("event: message\n"))
		_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
	}))
	defer server.Close()

	client, err := StartConfigured(t.Context(), "sse", config.MCPServerConfig{Type: "streamable-http", URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
}

func mustRaw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
