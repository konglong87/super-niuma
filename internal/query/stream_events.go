package query

// SSE 流事件类型，与 Anthropic Messages 流式协议对齐，值不可修改。
const (
	streamEventMessageStart      = "message_start"
	streamEventMessageDelta      = "message_delta"
	streamEventMessageStop       = "message_stop"
	streamEventContentBlockStart = "content_block_start"
	streamEventContentBlockDelta = "content_block_delta"
	streamEventContentBlockStop  = "content_block_stop"
)

// 内容块类型，与 Anthropic 内容块协议对齐，值不可修改。
const (
	blockTypeText                 = "text"
	blockTypeThinking             = "thinking"
	blockTypeRedactedThinking     = "redacted_thinking"
	blockTypeToolUse              = "tool_use"
	blockTypeToolResult           = "tool_result"
	blockTypeImage                = "image"
	blockTypeProviderContinuation = "provider_continuation"
)
