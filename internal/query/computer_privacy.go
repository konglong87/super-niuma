package query

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/tools"
)

func redactComputerToolCall(block anthropic.ContentBlock) anthropic.ContentBlock {
	if tools.IsComputerUseTool(block.Name) {
		block.Input = tools.RedactToolInput(block.Name, block.Input)
	}
	return block
}
func redactComputerAssistant(message anthropic.MessageParam) anthropic.MessageParam {
	blocks := append([]anthropic.ContentBlock(nil), message.Content...)
	for i, block := range blocks {
		if block.Type == blockTypeToolUse {
			blocks[i] = redactComputerToolCall(block)
		}
	}
	message.Content = blocks
	return message
}

// Runner normally lets one hook's UpdatedInput flow to the next hook. Desktop
// input is different: each hook gets only the same non-executable summary. No
// hook can read raw input, rewrite it, or send arbitrary text to event sinks.
func runComputerHooks(ctx context.Context, runner hooks.Runner, event, cwd string, payload hooks.Payload) (hooks.Result, error) {
	payload.Input = tools.RedactToolInput(payload.ToolName, payload.Input)
	if payload.Result != "" {
		summary, _ := json.Marshal(struct {
			IsError bool `json:"is_error"`
		}{payload.IsError})
		payload.Result = string(summary)
	}
	var result hooks.Result
	for _, command := range runner.Hooks[event] {
		isolated := hooks.New(map[string][]config.HookCommand{event: {command}})
		next, err := isolated.RunWithPayload(ctx, event, cwd, payload)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, errors.New("ComputerUse hook failed")
		}
		switch strings.ToLower(strings.TrimSpace(next.PermissionDecision)) {
		case "deny":
			// A later hook cannot undo a denial either.
			result.PermissionDecision = "deny"
			result.PermissionDecisionReason = "ComputerUse denied by hook"
		case "allow", "ask":
			if result.PermissionDecision != "deny" {
				result.PermissionDecision = strings.ToLower(strings.TrimSpace(next.PermissionDecision))
			}
		}
	}
	return result, nil
}

// Keep only content hashes, not screenshot bytes. This also covers retained
// screenshots being recorded again by either v1 or v2 compaction paths.
func (s *Session) rememberTransientComputerImages(messages []anthropic.MessageParam) {
	s.toolStateMu.Lock()
	defer s.toolStateMu.Unlock()
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type != blockTypeImage || block.Source == nil {
				continue
			}
			if s.transientComputerImages == nil {
				s.transientComputerImages = make(map[[sha256.Size]byte]struct{})
			}
			s.transientComputerImages[sha256.Sum256([]byte(block.Source.Data))] = struct{}{}
		}
	}
}
func (s *Session) isTransientComputerImage(block anthropic.ContentBlock) bool {
	if block.Source == nil {
		return false
	}
	s.toolStateMu.Lock()
	defer s.toolStateMu.Unlock()
	_, ok := s.transientComputerImages[sha256.Sum256([]byte(block.Source.Data))]
	return ok
}

// Post-hook failures do not undo a dispatched action or erase its receipt/image.
func toolHookFailure(name string, result tools.Result, err error) tools.Result {
	if !tools.IsComputerUseTool(name) {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	result.IsError = true
	if result.Content == "" {
		result.Content = "ComputerUse hook failed"
	}
	return result
}

// Restored computer audit projections are not valid executable inputs.
// Do not apply this projection to the current live model/tool exchange.
// Replay them as plain history, not as malformed examples of the live tool's
// schema. Keep matching results paired as text; other tool protocols and images
// remain untouched. No raw desktop input is reintroduced into model history.
func computerModelHistory(messages []anthropic.MessageParam) []anthropic.MessageParam {
	ids := make(map[string]struct{})
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == blockTypeToolUse && tools.IsComputerUseTool(block.Name) {
				ids[block.ID] = struct{}{}
			}
		}
	}
	if len(ids) == 0 {
		return messages
	}
	out := append([]anthropic.MessageParam(nil), messages...)
	for i, message := range messages {
		out[i].Content = append([]anthropic.ContentBlock(nil), message.Content...)
		for j, block := range message.Content {
			if block.Type == blockTypeToolUse && tools.IsComputerUseTool(block.Name) {
				var summary struct {
					Action string `json:"action"`
				}
				_ = json.Unmarshal(tools.RedactToolInput(block.Name, block.Input), &summary)
				out[i].Content[j] = anthropic.ContentBlock{Type: blockTypeText, Text: fmt.Sprintf("Historical ComputerUse action: %s. Sensitive arguments are omitted. This is an audit note, not executable tool arguments; use the current tool schema for any new action.", summary.Action)}
			} else if block.Type == blockTypeToolResult {
				if _, ok := ids[block.ToolUseID]; ok {
					out[i].Content[j] = anthropic.ContentBlock{Type: blockTypeText, Text: "Historical ComputerUse result: " + block.Content}
				}
			}
		}
	}
	return out
}

// Compaction can produce persisted text, and full prompt dumps are persistence.
// Neither may receive raw computer inputs or transient screenshot bytes.
func (s *Session) computerAuditHistory(messages []anthropic.MessageParam) []anthropic.MessageParam {
	history := computerModelHistory(messages)
	out := append([]anthropic.MessageParam(nil), history...)
	for i, message := range history {
		out[i].Content = append([]anthropic.ContentBlock(nil), message.Content...)
		for j, block := range message.Content {
			if block.Type == blockTypeImage && s.isTransientComputerImage(block) {
				out[i].Content[j] = anthropic.ContentBlock{Type: blockTypeText, Text: "Transient desktop screenshot omitted from audit/summary context."}
			}
		}
	}
	return out
}

const maxLiveComputerImages = 2

func (s *Session) transientComputerImageStats(messages []anthropic.MessageParam) (count, bytes int) {
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type != blockTypeImage || !s.isTransientComputerImage(block) || block.Source == nil {
				continue
			}
			count++
			bytes += len(block.Source.Data)
		}
	}
	return count, bytes
}

func (s *Session) liveComputerImageLimit() int {
	if s.options.ComputerUseFastPath {
		// The fast path always receives a fresh authoritative observation after
		// every successful input. Retaining an older frame only adds image tokens
		// and latency, while the receipt still records the prior action.
		return 1
	}
	return maxLiveComputerImages
}

// Keep current and previous desktop frames for visual comparison. Receipt and
// observation metadata stay in history; user attachments/other media stay intact.
// Do not let repeated observations grow provider input and latency unboundedly.
func (s *Session) limitComputerImageHistory(messages []anthropic.MessageParam) []anthropic.MessageParam {
	out := append([]anthropic.MessageParam(nil), messages...)
	retained := 0
	for i := len(messages) - 1; i >= 0; i-- {
		out[i].Content = append([]anthropic.ContentBlock(nil), messages[i].Content...)
		for j := len(messages[i].Content) - 1; j >= 0; j-- {
			block := messages[i].Content[j]
			if block.Type != blockTypeImage || !s.isTransientComputerImage(block) {
				continue
			}
			retained++
			if retained > s.liveComputerImageLimit() {
				if s.options.ComputerUseFastPath {
					// Fast-path model requests need only the latest authoritative
					// screenshot. Drop the entire old observation message below
					// instead of replacing its image with another text block.
					out[i].Content = nil
					break
				}
				out[i].Content[j] = anthropic.ContentBlock{Type: blockTypeText, Text: "Older desktop screenshot omitted; only a fresh observation may authorize new input."}
			}
		}
	}
	if s.options.ComputerUseFastPath {
		filtered := out[:0]
		for _, message := range out {
			if len(message.Content) > 0 {
				filtered = append(filtered, message)
			}
		}
		return filtered
	}
	return out
}
