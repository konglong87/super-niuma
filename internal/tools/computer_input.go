package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const computerUseToolName = "ComputerUse"
const computerInputUnknownAction = "unknown"

// IsComputerUseTool identifies the one tool with session-scoped desktop input.
// These helpers are identity operations for every other tool.
func IsComputerUseTool(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), computerUseToolName)
}

type computerInputSummary struct {
	Redacted   bool   `json:"redacted"`
	Action     string `json:"action"`
	InputBytes int    `json:"input_bytes"`
	TextLength int    `json:"text_length"`
	KeyLength  int    `json:"key_length"`
	KeysCount  int    `json:"keys_count"`
}

// RedactToolInput is an output-boundary projection, NOT executable input. Never
// overwrite the input passed to Tool.Run with it. No model-controlled strings
// survive except an exact allowlisted action kind; even identifiers are omitted.
func RedactToolInput(name string, input json.RawMessage) json.RawMessage {
	if !IsComputerUseTool(name) {
		return input
	}
	summary := computerInputSummary{Redacted: true, Action: computerInputUnknownAction, InputBytes: len(input)}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) == nil {
		var action string
		_ = json.Unmarshal(fields["action"], &action)
		summary.Action = computerActionSummary(action)
		// Permit summaries to pass multiple independent boundaries without losing
		// their original counts. Even forged summaries contain only bounded numbers
		// and an allowlisted enum; they cannot restore executable input or authority.
		var previous computerInputSummary
		if json.Unmarshal(input, &previous) == nil && previous.Redacted {
			summary.InputBytes = nonnegativeComputerCount(previous.InputBytes)
			summary.TextLength = nonnegativeComputerCount(previous.TextLength)
			summary.KeyLength = nonnegativeComputerCount(previous.KeyLength)
			summary.KeysCount = nonnegativeComputerCount(previous.KeysCount)
		} else {
			var text, key string
			var keys []json.RawMessage
			_ = json.Unmarshal(fields["text"], &text)
			_ = json.Unmarshal(fields["key"], &key)
			_ = json.Unmarshal(fields["keys"], &keys)
			summary.TextLength = utf8.RuneCountInString(text)
			summary.KeyLength = utf8.RuneCountInString(key)
			summary.KeysCount = len(keys)
		}
	}
	encoded, _ := json.Marshal(summary)
	return encoded
}

func nonnegativeComputerCount(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
func computerActionSummary(action string) string {
	switch cu.ActionKind(action) {
	case cu.ActionObserve, cu.ActionLaunchApp, cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick, cu.ActionMove, cu.ActionDrag, cu.ActionType, cu.ActionKey, cu.ActionHotkey, cu.ActionScroll, cu.ActionWait, cu.ActionPause, cu.ActionStop:
		return action
	default:
		return computerInputUnknownAction
	}
}

func RedactPermissionPromptRequest(request PermissionPromptRequest) PermissionPromptRequest {
	if !IsComputerUseTool(request.ToolName) {
		return request
	}
	request.Input = RedactToolInput(request.ToolName, request.Input)
	request.Request = string(request.Input)
	request.Rule = ""
	request.Reason = "ComputerUse requires permission approval for this invocation"
	request.Source = "computer_session"
	// Persisting an input-specific rule leaks data; replacing it with a broad
	// tool rule would widen authority. ComputerSession approval stays separate.
	request.OneShot = true
	return request
}
func RedactPermissionPromptResponse(name string, response PermissionPromptResponse) PermissionPromptResponse {
	if !IsComputerUseTool(name) {
		return response
	}
	decision := "deny"
	if response.Allowed {
		decision = "allow"
	}
	return PermissionPromptResponse{Allowed: response.Allowed, Decision: decision, Destination: "once", Reason: "ComputerUse permission " + decision}
}
func RedactPermissionAudit(audit PermissionAudit) PermissionAudit {
	if !IsComputerUseTool(audit.ToolName) {
		return audit
	}
	audit.Request = string(RedactToolInput(audit.ToolName, json.RawMessage(audit.Request)))
	audit.Rule = ""
	audit.Source = "computer_session"
	audit.Reason = "ComputerUse permission denied"
	if audit.Allowed {
		audit.Reason = "ComputerUse permission allowed"
	}
	return audit
}
func RedactPermissionUpdate(update PermissionUpdate) PermissionUpdate {
	if !IsComputerUseTool(update.ToolName) {
		return update
	}
	update.Input = RedactToolInput(update.ToolName, update.Input)
	update.Request = string(update.Input)
	update.Rule = ""
	update.Destination = "once"
	if update.Decision != "allow" {
		update.Decision = "deny"
	}
	update.Reason = "ComputerUse permission " + update.Decision
	return update
}

// Wrap before Guard exposes any policy payload. The raw input is neither
// captured by these callbacks nor substituted at the actual execution site.
func redactComputerPermissionContext(name string, tc Context) Context {
	if !IsComputerUseTool(name) {
		return tc
	}
	if prompt := tc.PermissionPrompt; prompt != nil {
		tc.PermissionPrompt = func(ctx context.Context, request PermissionPromptRequest) PermissionPromptResponse {
			request.ToolName = name
			return RedactPermissionPromptResponse(name, prompt(ctx, RedactPermissionPromptRequest(request)))
		}
	}
	if audit := tc.PermissionAudit; audit != nil {
		tc.PermissionAudit = func(event PermissionAudit) { event.ToolName = name; audit(RedactPermissionAudit(event)) }
	}
	if update := tc.PermissionUpdate; update != nil {
		tc.PermissionUpdate = func(event PermissionUpdate) error {
			event.ToolName = name
			if err := update(RedactPermissionUpdate(event)); err != nil {
				return errors.New("ComputerUse permission update failed")
			}
			return nil
		}
	}
	return tc
}
