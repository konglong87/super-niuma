package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestToolAdapterName(t *testing.T) {
	tool := ToolAdapter{ServerName: "my server", ToolInfo: ToolInfo{Name: "echo-tool", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	if tool.Name() != "mcp__my_server__echo_tool" {
		t.Fatalf("name = %q", tool.Name())
	}
}

func TestMCPToolRequiresPermissionPromptInAskMode(t *testing.T) {
	tool := ToolAdapter{ServerName: "db", ToolInfo: ToolInfo{Name: "query", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	wrapped := tools.Guard(tool, permissions.FromSettings(config.PermissionSettings{DefaultMode: "ask"}))
	res := wrapped.Run(context.Background(), json.RawMessage(`{"query":"select 1"}`), tools.Context{})
	if !res.IsError || !strings.Contains(res.Content, "requires permission approval") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMCPServerCallbackHandlerUsesPermissionPromptPayload(t *testing.T) {
	var prompted tools.PermissionPromptRequest
	var update tools.PermissionUpdate
	handler := mcpServerCallbackHandler(context.Background(), "mcp__db__query", json.RawMessage(`{"query":"select 1"}`), tools.Context{
		PermissionPrompt: func(ctx context.Context, req tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			prompted = req
			return tools.PermissionPromptResponse{
				Allowed:     true,
				Decision:    "allow",
				Destination: "session",
				Payload:     json.RawMessage(`{"decision":"allow"}`),
			}
		},
		PermissionUpdate: func(next tools.PermissionUpdate) error {
			update = next
			return nil
		},
	})
	payload, rpcErr := handler("permissions/request", json.RawMessage(`{"tool":"query"}`))
	if rpcErr != nil {
		t.Fatalf("rpcErr = %+v", rpcErr)
	}
	if string(payload) != `{"decision":"allow"}` || prompted.Request != "permissions/request" || prompted.Source != "mcp_callback" || !strings.Contains(string(prompted.Input), `"callback"`) || update.Destination != "session" {
		t.Fatalf("payload=%s prompted=%+v update=%+v", payload, prompted, update)
	}
}
