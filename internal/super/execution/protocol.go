package execution

import (
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
)

const (
	contentText       = "text"
	contentToolUse    = "tool_use"
	contentToolResult = "tool_result"
	roleAssistant     = "assistant"
	roleUser          = "user"
	stopComplete      = "end_turn"
	stopTools         = "tool_use"
)

// All protocols are stateless, text + explicit function tools only. No server
// tools, previous-response IDs, remote URLs, files or automatic continuations.
func encodeRequest(protocol config.ProviderProtocol, req anthropic.MessagesRequest) ([]byte, string, error) {
	if req.Thinking != nil || req.ResponseFormat != nil {
		return nil, "", ErrUnavailable
	}
	for _, t := range req.Tools {
		if t.Name != ToolRead && t.Name != ToolWrite {
			return nil, "", ErrAuthority
		}
	}
	for _, m := range req.Messages {
		if m.Role != roleUser && m.Role != roleAssistant {
			return nil, "", ErrInvalid
		}
		for _, b := range m.Content {
			if b.Source != nil || b.Continuation != nil || (b.Type != contentText && b.Type != contentToolUse && b.Type != contentToolResult) {
				return nil, "", ErrUnavailable
			}
		}
	}
	switch protocol {
	case config.ProviderProtocolAnthropicMessages:
		body := struct {
			Model     string                     `json:"model"`
			MaxTokens int                        `json:"max_tokens"`
			System    string                     `json:"system"`
			Messages  []anthropic.MessageParam   `json:"messages"`
			Tools     []anthropic.ToolDefinition `json:"tools,omitempty"`
			Stream    bool                       `json:"stream"`
		}{req.Model, req.MaxTokens, systemText(req), req.Messages, req.Tools, false}
		data, err := json.Marshal(body)
		return data, "/messages", err
	case config.ProviderProtocolOpenAIChatCompletions:
		return encodeChat(req)
	case config.ProviderProtocolOpenAIResponses:
		return encodeResponses(req)
	default:
		return nil, "", ErrUnavailable
	}
}
func systemText(req anthropic.MessagesRequest) string {
	if len(req.SystemBlocks) == 0 {
		return req.System
	}
	parts := make([]string, 0, len(req.SystemBlocks))
	for _, b := range req.SystemBlocks {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n\n")
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type chatCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}
type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Calls      []chatCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type chatTool struct {
	Type     string         `json:"type"`
	Function functionSchema `json:"function"`
}
type functionSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func encodeChat(req anthropic.MessagesRequest) ([]byte, string, error) {
	messages := []chatMessage{{Role: "system", Content: systemText(req)}}
	for _, m := range req.Messages {
		out := chatMessage{Role: m.Role}
		for _, b := range m.Content {
			switch b.Type {
			case contentText:
				out.Content += b.Text
			case contentToolUse:
				out.Calls = append(out.Calls, chatCall{b.ID, "function", functionCall{b.Name, string(b.Input)}})
			case contentToolResult:
				messages = append(messages, chatMessage{Role: "tool", Content: b.Content, ToolCallID: b.ToolUseID})
			}
		}
		if out.Content != "" || len(out.Calls) > 0 {
			messages = append(messages, out)
		}
	}
	var definitions []chatTool
	for _, t := range req.Tools {
		definitions = append(definitions, chatTool{"function", functionSchema{t.Name, t.Description, t.InputSchema}})
	}
	body := struct {
		Model     string        `json:"model"`
		MaxTokens int           `json:"max_completion_tokens"`
		Messages  []chatMessage `json:"messages"`
		Tools     []chatTool    `json:"tools,omitempty"`
		Stream    bool          `json:"stream"`
	}{req.Model, req.MaxTokens, messages, definitions, false}
	data, err := json.Marshal(body)
	return data, "/chat/completions", err
}

type responseText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type responseInput struct {
	Type      string         `json:"type"`
	Role      string         `json:"role,omitempty"`
	Content   []responseText `json:"content,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"`
	Output    *string        `json:"output,omitempty"`
}
type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func encodeResponses(req anthropic.MessagesRequest) ([]byte, string, error) {
	var input []responseInput
	for _, m := range req.Messages {
		for _, b := range m.Content {
			switch b.Type {
			case contentText:
				kind := "input_text"
				if m.Role == roleAssistant {
					kind = "output_text"
				}
				input = append(input, responseInput{Type: "message", Role: m.Role, Content: []responseText{{kind, b.Text}}})
			case contentToolUse:
				input = append(input, responseInput{Type: "function_call", CallID: b.ID, Name: b.Name, Arguments: string(b.Input)})
			case contentToolResult:
				output := b.Content
				input = append(input, responseInput{Type: "function_call_output", CallID: b.ToolUseID, Output: &output})
			}
		}
	}
	var definitions []responseTool
	for _, t := range req.Tools {
		definitions = append(definitions, responseTool{"function", t.Name, t.Description, t.InputSchema})
	}
	body := struct {
		Model        string          `json:"model"`
		Instructions string          `json:"instructions"`
		MaxTokens    int             `json:"max_output_tokens"`
		Input        []responseInput `json:"input"`
		Tools        []responseTool  `json:"tools,omitempty"`
		Store        bool            `json:"store"`
		Stream       bool            `json:"stream"`
	}{req.Model, systemText(req), req.MaxTokens, input, definitions, false, false}
	data, err := json.Marshal(body)
	return data, "/responses", err
}

// Pointer counters distinguish missing reports from a real zero. Missing any
// required counter discards the incomplete report: the reservation stays held.
type wireUsage struct {
	Input       *int `json:"input_tokens"`
	Output      *int `json:"output_tokens"`
	CacheCreate *int `json:"cache_creation_input_tokens"`
	CacheRead   *int `json:"cache_read_input_tokens"`
	Prompt      *int `json:"prompt_tokens"`
	Completion  *int `json:"completion_tokens"`
}

func (u wireUsage) usage(protocol config.ProviderProtocol) anthropic.Usage {
	in, out := u.Input, u.Output
	if protocol == config.ProviderProtocolOpenAIChatCompletions {
		in, out = u.Prompt, u.Completion
	}
	if in == nil || out == nil {
		return anthropic.Usage{}
	}
	result := anthropic.Usage{InputTokens: *in, OutputTokens: *out, InputTokensIncludeCacheRead: protocol != config.ProviderProtocolAnthropicMessages}
	if protocol == config.ProviderProtocolAnthropicMessages {
		if u.CacheCreate != nil {
			result.CacheCreationInputTokens = *u.CacheCreate
		}
		if u.CacheRead != nil {
			result.CacheReadInputTokens = *u.CacheRead
		}
	}
	return result
}
func decodeResponse(protocol config.ProviderProtocol, data []byte) (*anthropic.StreamResult, error) {
	switch protocol {
	case config.ProviderProtocolAnthropicMessages:
		var response struct {
			Role       string                   `json:"role"`
			Content    []anthropic.ContentBlock `json:"content"`
			StopReason string                   `json:"stop_reason"`
			Usage      wireUsage                `json:"usage"`
		}
		if json.Unmarshal(data, &response) != nil {
			return nil, ErrFailed
		}
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: response.Role, Content: response.Content}, StopReason: response.StopReason, Usage: response.Usage.usage(protocol)}, nil
	case config.ProviderProtocolOpenAIChatCompletions:
		return decodeChat(data)
	case config.ProviderProtocolOpenAIResponses:
		return decodeResponses(data)
	default:
		return nil, ErrUnavailable
	}
}
func decodeChat(data []byte) (*anthropic.StreamResult, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Role      string     `json:"role"`
				Content   string     `json:"content"`
				ToolCalls []chatCall `json:"tool_calls"`
				Refusal   string     `json:"refusal"`
			} `json:"message"`
			Finish string `json:"finish_reason"`
		} `json:"choices"`
		Usage wireUsage `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Choices) != 1 {
		return nil, ErrFailed
	}
	c := response.Choices[0]
	if c.Message.Role != roleAssistant || c.Message.Refusal != "" {
		return nil, ErrFailed
	}
	result := &anthropic.StreamResult{Message: anthropic.MessageParam{Role: roleAssistant}, Usage: response.Usage.usage(config.ProviderProtocolOpenAIChatCompletions)}
	if c.Message.Content != "" {
		result.Message.Content = append(result.Message.Content, anthropic.ContentBlock{Type: contentText, Text: c.Message.Content})
	}
	for _, tc := range c.Message.ToolCalls {
		if tc.Type != "function" {
			return nil, ErrFailed
		}
		result.Message.Content = append(result.Message.Content, anthropic.ContentBlock{Type: contentToolUse, ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(tc.Function.Arguments)})
	}
	switch c.Finish {
	case "stop":
		result.StopReason = stopComplete
	case "tool_calls":
		result.StopReason = stopTools
	default:
		return nil, ErrFailed
	}
	return result, nil
}
func decodeResponses(data []byte) (*anthropic.StreamResult, error) {
	var response struct {
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
		Output []struct {
			Type      string         `json:"type"`
			Role      string         `json:"role"`
			Status    string         `json:"status"`
			Content   []responseText `json:"content"`
			CallID    string         `json:"call_id"`
			Name      string         `json:"name"`
			Arguments string         `json:"arguments"`
		} `json:"output"`
		Usage wireUsage `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil || response.Status != "completed" || (len(response.Error) > 0 && string(response.Error) != "null") {
		return nil, ErrFailed
	}
	result := &anthropic.StreamResult{Message: anthropic.MessageParam{Role: roleAssistant}, StopReason: stopComplete, Usage: response.Usage.usage(config.ProviderProtocolOpenAIResponses)}
	for _, out := range response.Output {
		switch out.Type {
		case "message":
			if out.Role != roleAssistant || (out.Status != "" && out.Status != "completed") {
				return nil, ErrFailed
			}
			for _, b := range out.Content {
				if b.Type != "output_text" {
					return nil, ErrFailed
				}
				result.Message.Content = append(result.Message.Content, anthropic.ContentBlock{Type: contentText, Text: b.Text})
			}
		case "function_call":
			result.StopReason = stopTools
			result.Message.Content = append(result.Message.Content, anthropic.ContentBlock{Type: contentToolUse, ID: out.CallID, Name: out.Name, Input: json.RawMessage(out.Arguments)})
		default:
			// Reasoning/encrypted/server-tool continuations are intentionally not
			// replayed or silently dropped by this narrowly supported protocol.
			return nil, ErrUnavailable
		}
	}
	return result, nil
}
