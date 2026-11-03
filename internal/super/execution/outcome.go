package execution

import (
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

// Inspects shape only. Unknown fields and all text/reasoning values are discarded;
// there is deliberately no string-to-diagnostic passthrough.
func classifyOutcome(protocol config.ProviderProtocol, body []byte) (CompletionKind, ContentKind) {
	var envelope struct {
		StopReason string            `json:"stop_reason"`
		Status     string            `json:"status"`
		Content    []json.RawMessage `json:"content"`
		Output     []json.RawMessage `json:"output"`
		Choices    []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Content            json.RawMessage   `json:"content"`
				Calls              []json.RawMessage `json:"tool_calls"`
				Reasoning          json.RawMessage   `json:"reasoning_content"`
				AlternateReasoning json.RawMessage   `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return CompletionUnknown, ContentUnknown
	}
	completion := CompletionUnknown
	text, tool, reasoning, unsupported := false, false, false, false
	switch protocol {
	case config.ProviderProtocolOpenAIChatCompletions:
		if len(envelope.Choices) != 1 {
			return completion, ContentUnknown
		}
		choice := envelope.Choices[0]
		completion = completionFor(choice.Finish)
		text = nonemptyJSONText(choice.Message.Content)
		tool = len(choice.Message.Calls) > 0
		reasoning = nonemptyJSONText(choice.Message.Reasoning) || nonemptyJSONText(choice.Message.AlternateReasoning)
	case config.ProviderProtocolAnthropicMessages:
		completion = completionFor(envelope.StopReason)
		for _, raw := range envelope.Content {
			var b struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(raw, &b) != nil {
				unsupported = true
				continue
			}
			switch b.Type {
			case "text":
				text = text || strings.TrimSpace(b.Text) != ""
			case "tool_use":
				tool = true
			case "thinking", "redacted_thinking":
				reasoning = true
			default:
				unsupported = true
			}
		}
	case config.ProviderProtocolOpenAIResponses:
		completion = completionFor(envelope.Status)
		for _, raw := range envelope.Output {
			var item struct {
				Type    string `json:"type"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(raw, &item) != nil {
				unsupported = true
				continue
			}
			switch item.Type {
			case "message":
				for _, b := range item.Content {
					if b.Type != "output_text" {
						unsupported = true
					} else {
						text = text || strings.TrimSpace(b.Text) != ""
					}
				}
			case "function_call":
				tool = true
			case "reasoning":
				reasoning = true
			default:
				unsupported = true
			}
		}
	}
	if unsupported {
		return completion, ContentUnsupported
	}
	kinds := 0
	for _, present := range []bool{text, tool, reasoning} {
		if present {
			kinds++
		}
	}
	if kinds > 1 {
		return completion, ContentMixed
	}
	if tool {
		return completion, ContentToolCall
	}
	if reasoning {
		return completion, ContentReasoning
	}
	if text {
		return completion, ContentText
	}
	return completion, ContentEmpty
}
func nonemptyJSONText(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != ""
}
func completionFor(value string) CompletionKind {
	switch value {
	case "stop", "end_turn", "completed":
		return CompletionComplete
	case "tool_calls", "tool_use":
		return CompletionToolCall
	case "length", "max_tokens", "incomplete":
		return CompletionTruncated
	case "refusal", "content_filter":
		return CompletionRefused
	default:
		return CompletionUnknown
	}
}
