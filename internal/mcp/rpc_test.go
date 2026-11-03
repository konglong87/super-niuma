package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 这些用例原来喂一个预置好响应的 strings.Reader。多路分解读循环落地后，那种
// fixture 会在调用方登记之前就把响应读掉，所以改用 scriptedServer：先等请求真的
// 写上线，再回帧。测的契约没变，只是不再依赖「读发生在 call 内部」这个实现细节。

func TestRPCConnCall(t *testing.T) {
	server := newScriptedServer(t)
	var result listToolsResult
	done := make(chan error, 1)
	go func() {
		done <- server.conn.call(context.Background(), "tools/list", map[string]any{}, &result)
	}()
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), "tools/list") })
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}}]}}`)
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Fatalf("result = %+v", result)
	}
	var req rpcRequest
	if err := json.Unmarshal([]byte(firstLine(server.fromClient.String())), &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "tools/list" || req.ID != 1 {
		t.Fatalf("request = %+v", req)
	}
}

func TestRPCConnReadResourceShape(t *testing.T) {
	server := newScriptedServer(t)
	var result readResourceResult
	done := make(chan error, 1)
	go func() {
		done <- server.conn.call(context.Background(), "resources/read", map[string]any{"uri": "file://a"}, &result)
	}()
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), "resources/read") })
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"contents":[{"uri":"file://a","text":"hello"}]}}`)
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "hello" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRPCConnDeniesServerCallbacksWhileWaitingForResponse(t *testing.T) {
	server := newScriptedServer(t)
	var result listToolsResult
	done := make(chan error, 1)
	go func() {
		done <- server.conn.call(context.Background(), "tools/list", map[string]any{}, &result)
	}()
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), "tools/list") })
	server.send(t, `{"jsonrpc":"2.0","id":99,"method":"elicitation/create","params":{"message":"approve?"}}`)
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"id":99`) })
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}}]}}`)

	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("result = %+v", result)
	}
	callback := findResponseByID(t, server.fromClient.String(), 99)
	if callback.Error == nil || !strings.Contains(callback.Error.Message, "denied") {
		t.Fatalf("callback response = %+v", callback)
	}
}

func TestRPCConnServerCallbackHandlerReturnsResult(t *testing.T) {
	server := newScriptedServer(t)
	var result callToolResult
	done := make(chan error, 1)
	go func() {
		done <- server.conn.callWithCallback(context.Background(), "tools/call", map[string]any{"name": "demo"}, &result,
			func(method string, params json.RawMessage) (json.RawMessage, *rpcError) {
				if method != "permissions/request" || !strings.Contains(string(params), "demo") {
					t.Errorf("callback method=%s params=%s", method, params)
				}
				return json.RawMessage(`{"decision":"allow"}`), nil
			})
	}()
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), "tools/call") })
	server.send(t, `{"jsonrpc":"2.0","id":99,"method":"permissions/request","params":{"tool":"demo"}}`)
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"id":99`) })
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`)

	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	callback := findResponseByID(t, server.fromClient.String(), 99)
	if callback.Error != nil || string(callback.Result) != `{"decision":"allow"}` {
		t.Fatalf("callback = %+v", callback)
	}
}

func firstLine(value string) string {
	return strings.SplitN(strings.TrimSpace(value), "\n", 2)[0]
}

func findResponseByID(t *testing.T, written string, id int64) rpcResponse {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(written), "\n") {
		var res rpcResponse
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			continue
		}
		if res.ID == id && (res.Result != nil || res.Error != nil) {
			return res
		}
	}
	t.Fatalf("no response with id %d in %s", id, written)
	return rpcResponse{}
}
