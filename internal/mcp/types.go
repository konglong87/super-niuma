package mcp

import (
	"encoding/json"
)

const protocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// callbackResponse 是我们回答 server→client 请求时用的形状。id 保持 RawMessage：
// 那个 id 是 server 生成的，JSON-RPC 允许它是字符串，原样回传才对得上。
// （rpcResponse.ID 是 int64 且带 omitempty，用它会把字符串 id 变成 0 再整个丢掉。）
type callbackResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type listToolsResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// toolContentBlock 覆盖 MCP 的全部 content 变体，不只是 text —— 见
// renderToolContent 里为什么非文本块必须留痕而不是丢掉。
type toolContentBlock struct {
	Type     string               `json:"type"`
	Text     string               `json:"text,omitempty"`
	MimeType string               `json:"mimeType,omitempty"`
	Data     string               `json:"data,omitempty"`
	Resource *ReadResourceContent `json:"resource,omitempty"`
}

type callToolResult struct {
	Content []toolContentBlock `json:"content"`
	IsError bool               `json:"isError,omitempty"`
}

// initializeResult 是握手的回复。旧实现把它 unmarshal 成 json.RawMessage 后直接
// 丢掉，于是 server 声明的协议版本和 capabilities 从来没人看过。
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion,omitempty"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name,omitempty"`
		Version string `json:"version,omitempty"`
	} `json:"serverInfo"`
}

// serverCapabilities 只记我们真的会据此改变行为的几项。指针语义区分「没声明」
// 和「声明了但是空对象」—— 后者是合法的「支持，但没有子选项」。
type serverCapabilities struct {
	Tools     *json.RawMessage `json:"tools,omitempty"`
	Resources *json.RawMessage `json:"resources,omitempty"`
	Prompts   *json.RawMessage `json:"prompts,omitempty"`
}

type ResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type listResourcesResult struct {
	Resources  []ResourceInfo `json:"resources"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type ReadResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type readResourceResult struct {
	Contents []ReadResourceContent `json:"contents"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type listPromptsResult struct {
	Prompts    []PromptInfo `json:"prompts"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type PromptContent struct {
	Type     string               `json:"type"`
	Text     string               `json:"text,omitempty"`
	MimeType string               `json:"mimeType,omitempty"`
	Data     string               `json:"data,omitempty"`
	Resource *ReadResourceContent `json:"resource,omitempty"`
	Raw      json.RawMessage      `json:"-"`
}

type PromptMessage struct {
	Role    string        `json:"role"`
	Content PromptContent `json:"content"`
}

type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}
