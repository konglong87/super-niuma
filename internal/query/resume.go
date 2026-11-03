package query

// This file holds the transcript<->messages bridge: turning persisted session
// entries back into API messages on resume, and the sanitization that repairs a
// transcript interrupted mid tool-turn or mid prompt. All of it is pure — no
// Session state, no network, no filesystem.
//
// Split out of query.go verbatim by AUDIT-P2-01 step 3. Its tests already live in
// resume_v2_test.go and query_test.go and are unchanged by the split.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	providerstate "github.com/konglong87/super-niuma/internal/provider"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/toolresult"
)

const (
	ResumeInterruptionNone    ResumeInterruptionKind = "none"
	ResumeInterruptedTurn     ResumeInterruptionKind = "interrupted_turn"
	ResumeInterruptedPrompt   ResumeInterruptionKind = "interrupted_prompt"
	resumeSyntheticToolResult                        = "[Tool result missing due to internal error]"
	resumeContinuePrompt                             = "Continue from where you left off."
	resumeNoResponseRequested                        = "No response requested."
)

// MessagesFromTranscript rebuilds API-ready messages from the append-only JSONL transcript.
// It intentionally delegates to the repair path so resume never sends orphaned tool blocks.
func MessagesFromTranscript(entries []session.Entry) []anthropic.MessageParam {
	messages, _ := MessagesFromTranscriptWithReport(entries)
	return messages
}

func ToolResultReplacementsFromTranscript(entries []session.Entry) []toolresult.ReplacementRecord {
	var out []toolresult.ReplacementRecord
	for _, entry := range entries {
		if entry.Type != "content_replacement" {
			continue
		}
		for _, replacement := range entry.Replacements {
			kind := strings.TrimSpace(replacement.Kind)
			toolUseID := strings.TrimSpace(replacement.ToolUseID)
			if kind == "tool-result" && toolUseID != "" && replacement.Replacement != "" {
				out = append(out, toolresult.ReplacementRecord{
					Kind:        kind,
					ToolUseID:   toolUseID,
					Replacement: replacement.Replacement,
				})
			}
		}
	}
	return out
}

func toolResultReplacementMap(records []toolresult.ReplacementRecord) map[string]string {
	out := map[string]string{}
	for _, record := range records {
		if strings.TrimSpace(record.Kind) != "tool-result" {
			continue
		}
		toolUseID := strings.TrimSpace(record.ToolUseID)
		if toolUseID != "" && record.Replacement != "" {
			out[toolUseID] = record.Replacement
		}
	}
	return out
}

// ResumeInterruptionKind classifies the recovery action the resume loader had to take.
// TUI/CLI code uses it to explain whether we repaired a tool turn or an unanswered prompt.
type ResumeInterruptionKind string

// ResumeReport summarizes transcript repairs applied before resuming a session.
// Keep this small and stable because it is used for user-facing TUI recovery status.
type ResumeReport struct {
	Interrupted                bool                   `json:"interrupted"`
	Kind                       ResumeInterruptionKind `json:"kind"`
	AutoPrompt                 string                 `json:"auto_prompt,omitempty"`
	SyntheticToolResults       int                    `json:"synthetic_tool_results,omitempty"`
	DroppedOrphanedToolResults int                    `json:"dropped_orphaned_tool_results,omitempty"`
	DroppedThinking            int                    `json:"dropped_thinking,omitempty"`
}

// MessagesFromTranscriptWithReport converts transcript entries into Anthropic messages
// and returns a repair report for UI/status surfaces. The important invariant is that
// every tool_use sent to the model has a matching user tool_result immediately after it.
func MessagesFromTranscriptWithReport(entries []session.Entry) ([]anthropic.MessageParam, ResumeReport) {
	var messages []anthropic.MessageParam
	entries, report := sanitizeResumeEntriesWithReport(entries)
	for _, entry := range entries {
		switch entry.Type {
		case "message":
			if entry.Role == "user" || entry.Role == "assistant" {
				appendResumeMessage(&messages, anthropic.MessageParam{
					Role:    entry.Role,
					Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: entry.Content}},
				})
			}
		case blockTypeThinking, blockTypeRedactedThinking:
			appendResumeMessage(&messages, anthropic.MessageParam{
				Role: "assistant",
				Content: []anthropic.ContentBlock{{
					Type:      entry.Type,
					Thinking:  entry.Content,
					Signature: entry.Signature,
				}},
			})
		case blockTypeProviderContinuation:
			continuation, err := providerstate.DecodeContinuation(entry.Metadata)
			if err == nil && !continuation.Invalidated {
				appendResumeMessage(&messages, anthropic.MessageParam{
					Role: "assistant",
					Content: []anthropic.ContentBlock{{
						Type: blockTypeProviderContinuation, Continuation: &continuation,
					}},
				})
			}
		case "tool_call":
			appendResumeMessage(&messages, anthropic.MessageParam{
				Role: "assistant",
				Content: []anthropic.ContentBlock{{
					Type:  blockTypeToolUse,
					ID:    entry.ToolID,
					Name:  entry.ToolName,
					Input: json.RawMessage(entry.Content),
				}},
			})
		case blockTypeToolResult:
			appendResumeMessage(&messages, anthropic.MessageParam{
				Role: "user",
				Content: []anthropic.ContentBlock{{
					Type:      blockTypeToolResult,
					ToolUseID: entry.ToolID,
					Content:   entry.Content,
					IsError:   entry.IsError,
				}},
			})
		case blockTypeImage:
			appendResumeMessage(&messages, anthropic.MessageParam{
				Role:    "user",
				Content: []anthropic.ContentBlock{imageBlockFromEntry(entry)},
			})
		case "compact_summary":
			messages = []anthropic.MessageParam{{
				Role:    "user",
				Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "Conversation summary so far:\n" + entry.Content}},
			}}
		case "recap_summary":
			// Recap is a UI/session convenience entry and must never become model context.
			continue
		}
	}
	var droppedThinking int
	messages, droppedThinking = dropInvalidTrailingThinking(messages)
	report.DroppedThinking += droppedThinking
	if droppedThinking > 0 {
		report.Interrupted = true
	}
	messages, sentinelKind, sentinelPrompt := addResumeInterruptionSentinel(messages)
	if sentinelKind != ResumeInterruptionNone {
		report.Interrupted = true
		report.Kind = sentinelKind
		report.AutoPrompt = sentinelPrompt
	} else if report.Kind == "" {
		report.Kind = ResumeInterruptionNone
	}
	return messages, report
}

// imageBlockFromEntry 从侧车文件把 base64 载荷读回来，重建一个真正的 image 块
// （TODO-080）。
//
// 载荷读不回来（会话目录被清理、换了机器 resume、校验和不符）时降级成一个文本块。
// 降级文本必须重写：entry.Content 是记录时写的 "content attached below"，图片没了
// 之后那句话就是句谎话，而「说不清为什么看不到」正是 AUDIT-P1-18/TODO-061 要修的毛病。
//
// 重放整张图不会撑爆 context：估算器给内联图片按像素封顶，单张 ~1600 token
// （compact.estimateInlineSourceTokens），不是按 base64 长度算的。
func imageBlockFromEntry(entry session.Entry) anthropic.ContentBlock {
	ref, err := session.UnmarshalMediaRef(entry.Metadata)
	if err == nil {
		if payload, loadErr := session.LoadMedia(ref); loadErr == nil {
			return anthropic.ContentBlock{
				Type:   blockTypeImage,
				Text:   entry.Content,
				Source: &anthropic.ContentSource{Type: "base64", MediaType: ref.MediaType, Data: payload},
			}
		}
	}
	return anthropic.ContentBlock{Type: blockTypeText, Text: unavailableImageText(entry, ref)}
}

func unavailableImageText(entry session.Entry, ref session.MediaRef) string {
	mediaType := strings.TrimSpace(ref.MediaType)
	if mediaType == "" {
		mediaType = "image"
	}
	text := fmt.Sprintf("[%s from an earlier turn is no longer available: its stored copy could not be read]", mediaType)
	if description := strings.TrimSpace(entry.Content); description != "" {
		text = description + "\n" + text
	}
	return text
}

func sanitizeResumeEntries(entries []session.Entry) []session.Entry {
	clean, _ := sanitizeResumeEntriesWithReport(entries)
	return clean
}

// sanitizeResumeEntriesWithReport normalizes transcript ordering before conversion.
// Pending tool calls are closed with synthetic error results instead of being left
// dangling, and orphaned results/thinking are removed to avoid upstream API 400s.
func sanitizeResumeEntriesWithReport(entries []session.Entry) ([]session.Entry, ResumeReport) {
	out := make([]session.Entry, 0, len(entries))
	pending := map[string]session.Entry{}
	pendingOrder := []string{}
	report := ResumeReport{Kind: ResumeInterruptionNone}
	for _, entry := range entries {
		switch entry.Type {
		case "compact_summary":
			out = out[:0]
			pending = map[string]session.Entry{}
			pendingOrder = nil
			out = append(out, entry)
		case "usage", "permission", "file_change", "checkpoint", "rewind", "fork", "recap_summary", "prompt_context", "content_replacement", "task_contract", "action_record", "evidence_record", "delta_record", "completion_gate", session.EntryTypeRuntimeSpan,
			"session_meta", "branch_head", "provider_error":
			// Session metadata is not model context and can be interleaved with a
			// tool turn. It must not cause pending tool_use blocks to be repaired.
			// (session_meta/branch_head are v2 graph control lines, never context.)
			continue
		case "tool_call":
			if strings.TrimSpace(entry.ToolID) == "" {
				report.Interrupted = true
				continue
			}
			if _, exists := pending[entry.ToolID]; exists {
				report.SyntheticToolResults += appendSyntheticToolResults(&out, pending, pendingOrder)
				report.Interrupted = true
				pending = map[string]session.Entry{}
				pendingOrder = nil
			}
			pending[entry.ToolID] = entry
			pendingOrder = append(pendingOrder, entry.ToolID)
			out = append(out, entry)
		case blockTypeToolResult:
			if strings.TrimSpace(entry.ToolID) == "" {
				report.Interrupted = true
				report.DroppedOrphanedToolResults++
				continue
			}
			if _, ok := pending[entry.ToolID]; !ok {
				report.Interrupted = true
				report.DroppedOrphanedToolResults++
				continue
			}
			delete(pending, entry.ToolID)
			out = append(out, entry)
		default:
			if strings.EqualFold(entry.Type, "orphaned_thinking") {
				report.Interrupted = true
				report.DroppedThinking++
				continue
			}
			if len(pending) > 0 {
				report.SyntheticToolResults += appendSyntheticToolResults(&out, pending, pendingOrder)
				report.Interrupted = true
				pending = map[string]session.Entry{}
				pendingOrder = nil
			}
			out = append(out, entry)
		}
	}
	if len(pending) == 0 {
		return out, report
	}
	report.SyntheticToolResults += appendSyntheticToolResults(&out, pending, pendingOrder)
	report.Interrupted = true
	return out, report
}

// appendSyntheticToolResults closes interrupted tool_use blocks with explicit error
// results. This preserves the assistant trajectory while making the message sequence valid.
func appendSyntheticToolResults(out *[]session.Entry, pending map[string]session.Entry, order []string) int {
	if len(pending) == 0 {
		return 0
	}
	count := 0
	for _, id := range order {
		entry, ok := pending[id]
		if !ok {
			continue
		}
		*out = append(*out, session.Entry{
			Type:     blockTypeToolResult,
			ToolID:   entry.ToolID,
			ToolName: entry.ToolName,
			Content:  resumeSyntheticToolResult,
			IsError:  true,
		})
		count++
	}
	return count
}

// appendResumeMessage merges adjacent messages with the same role. Transcript entries
// are fine-grained, while the model API expects role-alternating message groups.
func appendResumeMessage(messages *[]anthropic.MessageParam, message anthropic.MessageParam) {
	if len(message.Content) == 0 {
		return
	}
	if len(*messages) > 0 && (*messages)[len(*messages)-1].Role == message.Role {
		last := &(*messages)[len(*messages)-1]
		last.Content = append(last.Content, message.Content...)
		return
	}
	*messages = append(*messages, message)
}

// addResumeInterruptionSentinel marks the two user-visible resume cases:
// tool turns continue from the repaired result, plain prompts get a no-response sentinel.
func addResumeInterruptionSentinel(messages []anthropic.MessageParam) ([]anthropic.MessageParam, ResumeInterruptionKind, string) {
	if len(messages) == 0 {
		return messages, ResumeInterruptionNone, ""
	}
	last := &messages[len(messages)-1]
	if last.Role != "user" {
		return messages, ResumeInterruptionNone, ""
	}
	if hasToolResultBlock(last.Content) {
		appendResumeMessage(&messages, anthropic.MessageParam{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: resumeContinuePrompt}},
		})
		return messages, ResumeInterruptedTurn, resumeContinuePrompt
	}
	appendResumeMessage(&messages, anthropic.MessageParam{
		Role:    "assistant",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: resumeNoResponseRequested}},
	})
	return messages, ResumeInterruptedPrompt, latestUserText(*last)
}

func hasToolResultBlock(blocks []anthropic.ContentBlock) bool {
	for _, block := range blocks {
		if block.Type == blockTypeToolResult {
			return true
		}
	}
	return false
}

func latestUserText(message anthropic.MessageParam) string {
	for i := len(message.Content) - 1; i >= 0; i-- {
		if message.Content[i].Type == blockTypeText && strings.TrimSpace(message.Content[i].Text) != "" {
			return strings.TrimSpace(message.Content[i].Text)
		}
	}
	return ""
}

// dropInvalidTrailingThinking keeps only thinking blocks that are followed by safe
// assistant content. Trailing thinking signatures are often incomplete after interrupts.
func dropInvalidTrailingThinking(messages []anthropic.MessageParam) ([]anthropic.MessageParam, int) {
	out := messages[:0]
	dropped := 0
	for _, message := range messages {
		if message.Role != "assistant" {
			out = append(out, message)
			continue
		}
		content := append([]anthropic.ContentBlock(nil), message.Content...)
		for len(content) > 0 && isThinkingBlock(content[len(content)-1]) {
			content = content[:len(content)-1]
			dropped++
		}
		if len(content) == 0 {
			continue
		}
		message.Content = content
		out = append(out, message)
	}
	return out, dropped
}

func isThinkingBlock(block anthropic.ContentBlock) bool {
	return block.Type == blockTypeThinking || block.Type == blockTypeRedactedThinking
}
