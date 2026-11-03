package mcpresources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/mcp"
	"github.com/konglong87/super-niuma/internal/tools"
)

type ListTool struct {
	Configs map[string]config.MCPServerConfig
}

type ReadTool struct {
	Configs map[string]config.MCPServerConfig
}

func NewList(configs map[string]config.MCPServerConfig) ListTool {
	return ListTool{Configs: configs}
}

func NewRead(configs map[string]config.MCPServerConfig) ReadTool {
	return ReadTool{Configs: configs}
}

func (t ListTool) Name() string { return "ListMcpResources" }

func (t ListTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (t ListTool) Description() string {
	return `List resources exposed by configured MCP servers.

Use ListMcpResources to discover what data MCP servers can provide.
Filter by server name to inspect a specific server. Returns server name, URI, and resource name.`
}
func (t ListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "server": {"type": "string", "description": "Optional MCP server name."}
	  },
	  "additionalProperties": false
	}`)
}
func (t ListTool) Run(ctx context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Server string `json:"server"`
	}
	_ = json.Unmarshal(input, &params)
	var lines []string
	for name, cfg := range t.Configs {
		if params.Server != "" && params.Server != name {
			continue
		}
		client, err := mcp.StartConfigured(ctx, name, cfg)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s ERROR %s", name, err))
			continue
		}
		resources, err := client.ListResourcesContext(ctx)
		_ = client.Close()
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s ERROR %s", name, err))
			continue
		}
		for _, resource := range resources {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s", name, resource.URI, resource.Name))
		}
	}
	if len(lines) == 0 {
		return tools.Result{Content: "No MCP resources found"}
	}
	return tools.Result{Content: strings.Join(lines, "\n")}
}

func (t ReadTool) Name() string { return "ReadMcpResource" }

func (t ReadTool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (t ReadTool) Description() string {
	return `Read a resource from a configured MCP server.

Use ReadMcpResource to fetch specific data from an MCP server by URI.
Requires both the server name and the resource URI. Use ListMcpResources first
to discover available URIs.`
}
func (t ReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "server": {"type": "string", "description": "MCP server name."},
	    "uri": {"type": "string", "description": "Resource URI."}
	  },
	  "required": ["server", "uri"],
	  "additionalProperties": false
	}`)
}
func (t ReadTool) Run(ctx context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	cfg, ok := t.Configs[params.Server]
	if !ok {
		return tools.Result{Content: "unknown MCP server: " + params.Server, IsError: true}
	}
	client, err := mcp.StartConfigured(ctx, params.Server, cfg)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	defer client.Close()
	contents, err := client.ReadResourceContext(ctx, params.URI)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	var parts []string
	for _, content := range contents {
		if content.Text != "" {
			parts = append(parts, content.Text)
		} else if content.Blob != "" {
			parts = append(parts, content.Blob)
		}
	}
	return tools.Result{Content: strings.Join(parts, "\n")}
}
