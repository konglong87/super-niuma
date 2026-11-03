package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/tools"
)

type ToolAdapter struct {
	ServerName string
	ToolInfo   ToolInfo
	Client     *Client

	// NameOverride 在 safeName 把两个不同的工具压成同一个名字时由 LoadTools 填上，
	// 避免后注册的那个把前一个从注册表里静默顶掉。
	NameOverride string
}

func (t ToolAdapter) Name() string {
	if t.NameOverride != "" {
		return t.NameOverride
	}
	return "mcp__" + safeName(t.ServerName) + "__" + safeName(t.ToolInfo.Name)
}

func (t ToolAdapter) Description() string {
	if t.ToolInfo.Description == "" {
		return "MCP tool " + t.ToolInfo.Name + " from server " + t.ServerName
	}
	return t.ToolInfo.Description
}

func (t ToolAdapter) InputSchema() json.RawMessage {
	if len(t.ToolInfo.InputSchema) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.ToolInfo.InputSchema
}

func (t ToolAdapter) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	// ctx 一路传进 RPC 层：旧实现只在这里 check 一次，之后调用方按 ESC 也叫不停
	// 一次已经发出去的工具调用。
	content, isError, err := t.Client.callToolContent(ctx, t.ToolInfo.Name, input,
		mcpServerCallbackHandler(ctx, t.Name(), input, toolContext))
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	result := tools.Result{Content: content.Text, IsError: isError}
	if !isError {
		// 失败的调用不带图：错误路径上的 content 大概率是残缺的，而 ContextMessages
		// 在 query / agentruntime 两侧都只在非错误时才回灌。
		result.ContextMessages = mediaContextMessage(t.Name(), content.Media)
	}
	return result
}

func mcpServerCallbackHandler(ctx context.Context, toolName string, originalInput json.RawMessage, toolContext tools.Context) serverCallbackHandler {
	if toolContext.PermissionPrompt == nil {
		return nil
	}
	return func(method string, params json.RawMessage) (json.RawMessage, *rpcError) {
		req := tools.PermissionPromptRequest{
			ToolName: toolName,
			Input:    callbackPromptInput(originalInput, method, params),
			Reason:   mcpCallbackReason(method, params),
			Request:  method,
			Rule:     toolName + ":" + method,
			Source:   "mcp_callback",
		}
		decision := toolContext.PermissionPrompt(ctx, req)
		if decision.Destination != "" && decision.Destination != "once" && toolContext.PermissionUpdate != nil {
			_ = toolContext.PermissionUpdate(tools.PermissionUpdate{
				ToolName:    toolName,
				Input:       originalInput,
				Request:     method,
				Rule:        firstNonEmpty(decision.Rule, req.Rule),
				Decision:    firstNonEmpty(decision.Decision, allowDeny(decision.Allowed)),
				Destination: decision.Destination,
				Reason:      decision.Reason,
			})
		}
		if !decision.Allowed {
			reason := firstNonEmpty(decision.Reason, "MCP callback denied by client permissions")
			return nil, &rpcError{Code: -32001, Message: reason}
		}
		if len(decision.Payload) > 0 {
			if !json.Valid(decision.Payload) {
				return nil, &rpcError{Code: -32602, Message: "permission prompt returned invalid MCP callback payload"}
			}
			return decision.Payload, nil
		}
		return defaultMCPCallbackResult(method), nil
	}
}

func callbackPromptInput(originalInput json.RawMessage, method string, params json.RawMessage) json.RawMessage {
	payload, _ := json.Marshal(map[string]json.RawMessage{
		"tool_input": originalInput,
		"callback":   json.RawMessage(`{"method":` + quoteJSON(method) + `,"params":` + rawOrEmptyObject(params) + `}`),
	})
	return payload
}

func mcpCallbackReason(method string, params json.RawMessage) string {
	switch method {
	case "elicitation/create", "elicitation/request":
		return "MCP server requested user elicitation: " + callbackMessage(params)
	case "sampling/createMessage":
		return "MCP server requested model sampling"
	case "permissions/request":
		return "MCP server requested client permissions"
	default:
		return "MCP server requested client callback: " + method
	}
}

func callbackMessage(params json.RawMessage) string {
	var decoded struct {
		Message string `json:"message"`
		Prompt  string `json:"prompt"`
		Title   string `json:"title"`
	}
	if err := json.Unmarshal(params, &decoded); err == nil {
		if strings.TrimSpace(decoded.Message) != "" {
			return decoded.Message
		}
		if strings.TrimSpace(decoded.Prompt) != "" {
			return decoded.Prompt
		}
		if strings.TrimSpace(decoded.Title) != "" {
			return decoded.Title
		}
	}
	if strings.TrimSpace(string(params)) == "" {
		return "{}"
	}
	return string(params)
}

func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func rawOrEmptyObject(raw json.RawMessage) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return "{}"
	}
	return string(raw)
}

func defaultMCPCallbackResult(method string) json.RawMessage {
	switch method {
	case "permissions/request":
		return json.RawMessage(`{"decision":"allow"}`)
	case "elicitation/create", "elicitation/request":
		return json.RawMessage(`{"action":"accept","content":{}}`)
	default:
		return json.RawMessage(`{}`)
	}
}

func allowDeny(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// LoadTools 启动每个配置的 MCP server 并把它的工具适配成本地工具。
//
// 单个 server 起不来不会连累其它 server —— 但**失败必须留下痕迹**。旧实现两处
// 失败都是裸 continue：配错的 server 完全静默地消失，用户只看到工具不见了，
// 没有任何线索。
func LoadTools(ctx context.Context, configs map[string]config.MCPServerConfig) ([]tools.Tool, func()) {
	var loaded []tools.Tool
	var clients []*Client
	taken := map[string]bool{}
	// 按名字排序，让工具顺序和重名消歧的结果都可复现。
	for _, name := range sortedKeys(configs) {
		cfg := configs[name]
		client, err := StartConfigured(ctx, name, cfg)
		if err != nil {
			observability.Warn(ctx, nil, "mcp.server.start_failed", "mcp.LoadTools",
				"MCP server failed to start; its tools will be unavailable",
				"server", name, "error", err.Error())
			continue
		}
		clients = append(clients, client)
		infos, err := client.ListToolsContext(ctx)
		if err != nil {
			observability.Warn(ctx, nil, "mcp.server.list_tools_failed", "mcp.LoadTools",
				"MCP server started but tools/list failed; its tools will be unavailable",
				"server", name, "error", err.Error())
			continue
		}
		for _, info := range infos {
			adapter := ToolAdapter{ServerName: name, ToolInfo: info, Client: client}
			adapter.NameOverride = uniqueToolName(adapter.Name(), taken, name, info.Name)
			loaded = append(loaded, adapter)
		}
	}
	cleanup := func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}
	return loaded, cleanup
}

// uniqueToolName 处理 safeName 的非单射：`a-b` 和 `a.b` 都会变成 `a_b`，两个不同
// 的工具于是抢同一个注册表键，后来的静默覆盖前一个。这里给冲突者加数字后缀，
// 让两个工具都还在，并把冲突记进日志。
func uniqueToolName(preferred string, taken map[string]bool, serverName, toolName string) string {
	if !taken[preferred] {
		taken[preferred] = true
		return preferred
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", preferred, suffix)
		if taken[candidate] {
			continue
		}
		taken[candidate] = true
		observability.Warn(context.Background(), nil, "mcp.tool.name_collision", "mcp.uniqueToolName",
			"two MCP tools sanitize to the same name; the later one was renamed",
			"server", serverName, "tool", toolName, "collided_with", preferred, "renamed_to", candidate)
		return candidate
	}
}

func sortedKeys(configs map[string]config.MCPServerConfig) []string {
	keys := make([]string, 0, len(configs))
	for name := range configs {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

var safeNameRE = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func safeName(name string) string {
	name = safeNameRE.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return "unnamed"
	}
	return name
}
