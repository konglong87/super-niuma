package anthropic

import (
	"encoding/json"

	"github.com/konglong87/super-niuma/internal/provider"
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// Data 承载 redacted_thinking 块的密文载荷。这类块没有可读内容，但 Anthropic 要求
	// 在后续轮次原样回传，因此必须和 thinking 一样持久化并回放（AUDIT-P1-07）。
	Data          string                 `json:"data,omitempty"`
	ConnectorText string                 `json:"connector_text,omitempty"`
	Source        *ContentSource         `json:"source,omitempty"`
	Citations     []json.RawMessage      `json:"citations,omitempty"`
	ID            string                 `json:"id,omitempty"`
	Name          string                 `json:"name,omitempty"`
	Input         json.RawMessage        `json:"input,omitempty"`
	ToolUseID     string                 `json:"tool_use_id,omitempty"`
	Content       string                 `json:"content,omitempty"`
	IsError       bool                   `json:"is_error,omitempty"`
	CacheControl  *CacheControl          `json:"cache_control,omitempty"`
	Continuation  *provider.Continuation `json:"provider_continuation,omitempty"`
}

type ContentSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type MessageParam struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type MessagesRequest struct {
	Model           string           `json:"model"`
	MaxTokens       int              `json:"max_tokens"`
	System          string           `json:"system,omitempty"`
	SystemBlocks    []SystemBlock    `json:"system_blocks,omitempty"`
	Messages        []MessageParam   `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	Thinking        *ThinkingConfig  `json:"thinking,omitempty"`
	ResponseFormat  *ResponseFormat  `json:"response_format,omitempty"`
	Stream          bool             `json:"stream"`
	TenantSessionID uint64           `json:"-"`
}

type ResponseFormat struct {
	Type       string                `json:"type,omitempty"`
	JSONSchema *ResponseFormatSchema `json:"json_schema,omitempty"`
}

type ResponseFormatSchema struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

type ThinkingConfig struct {
	Type         string `json:"type,omitempty"`
	Effort       string `json:"effort,omitempty"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

type SystemBlock struct {
	Type         string        `json:"type,omitempty"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
	Source       string        `json:"-"`
}

type CacheControl struct {
	Type  string `json:"type"`
	TTL   string `json:"ttl,omitempty"`
	Scope string `json:"scope,omitempty"`
}

type StreamResult struct {
	Message    MessageParam `json:"message"`
	StopReason string       `json:"stop_reason,omitempty"`
	Usage      Usage        `json:"usage,omitempty"`
}

type PartialStreamError struct {
	Err     error
	Partial *StreamResult
}

func (e *PartialStreamError) Error() string {
	if e == nil || e.Err == nil {
		return "partial stream error"
	}
	return e.Err.Error()
}

func (e *PartialStreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Usage struct {
	InputTokens                         int                `json:"input_tokens,omitempty"`
	OutputTokens                        int                `json:"output_tokens,omitempty"`
	CacheCreationInputTokens            int                `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int                `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int                `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int                `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	CacheCreation                       UsageCacheCreation `json:"cache_creation,omitempty"`
	ServiceTier                         string             `json:"service_tier,omitempty"`
	InferenceGeo                        string             `json:"inference_geo,omitempty"`
	Speed                               string             `json:"speed,omitempty"`
	InputTokensIncludeCacheRead         bool               `json:"-"`
	// ReasoningOutputTokens is the reasoning share of OutputTokens as reported
	// by OpenAI-compatible gateways (completion_tokens_details.reasoning_tokens).
	// Zero when the provider does not break it out (Anthropic includes thinking
	// in output_tokens without a separate figure).
	ReasoningOutputTokens int `json:"reasoning_output_tokens,omitempty"`
}

type UsageCacheCreation struct {
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens,omitempty"`
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens,omitempty"`
}
