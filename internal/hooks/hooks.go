package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/procenv"
)

const (
	SessionStart       = "SessionStart"
	Notification       = "Notification"
	PreToolUse         = "PreToolUse"
	PostToolUse        = "PostToolUse"
	PostToolUseFailure = "PostToolUseFailure"
	PreCompact         = "PreCompact"
	PostCompact        = "PostCompact"
	Stop               = "Stop"
	SubagentStart      = "SubagentStart"
	SubagentStop       = "SubagentStop"
	UserPromptSubmit   = "UserPromptSubmit"
	WorktreeCreate     = "WorktreeCreate"
	WorktreeRemove     = "WorktreeRemove"
)

type Runner struct {
	Hooks map[string][]config.HookCommand
}

func New(hooks map[string][]config.HookCommand) Runner {
	return Runner{Hooks: hooks}
}

func (r Runner) WithAdditional(extra map[string][]config.HookCommand) Runner {
	if len(extra) == 0 {
		return r
	}
	merged := map[string][]config.HookCommand{}
	for event, list := range r.Hooks {
		merged[event] = append([]config.HookCommand(nil), list...)
	}
	for event, list := range extra {
		merged[event] = append(merged[event], list...)
	}
	return Runner{Hooks: merged}
}

func (r Runner) Run(ctx context.Context, event, cwd, toolName string) error {
	_, err := r.RunWithPayload(ctx, event, cwd, Payload{ToolName: toolName})
	return err
}

type Payload struct {
	Event                string          `json:"event,omitempty"`
	ToolName             string          `json:"tool_name,omitempty"`
	Input                json.RawMessage `json:"input,omitempty"`
	Result               string          `json:"result,omitempty"`
	IsError              bool            `json:"is_error,omitempty"`
	Message              string          `json:"message,omitempty"`
	Prompt               string          `json:"prompt,omitempty"`
	Name                 string          `json:"name,omitempty"`
	WorktreePath         string          `json:"worktree_path,omitempty"`
	StopReason           string          `json:"stop_reason,omitempty"`
	SessionID            string          `json:"session_id,omitempty"`
	TaskID               uint64          `json:"task_id,omitempty"`
	Status               string          `json:"status,omitempty"`
	AgentID              string          `json:"agent_id,omitempty"`
	AgentType            string          `json:"agent_type,omitempty"`
	AgentName            string          `json:"agent_name,omitempty"`
	AgentTranscriptPath  string          `json:"agent_transcript_path,omitempty"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
	DurationMS           int64           `json:"duration_ms,omitempty"`
	Error                string          `json:"error,omitempty"`
}

type Result struct {
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
	Message                  string          `json:"message,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
	HookSpecificOutput       HookOutput      `json:"hookSpecificOutput,omitempty"`
}

type HookOutput struct {
	HookEventName string `json:"hookEventName,omitempty"`
	WorktreePath  string `json:"worktreePath,omitempty"`
}

func (r Runner) RunWithPayload(ctx context.Context, event, cwd string, payload Payload) (Result, error) {
	payload.Event = event
	var result Result
	for _, hook := range r.Hooks[event] {
		if hook.Command == "" {
			continue
		}
		if !matchesHook(hook, payload) {
			continue
		}
		next, err := runCommand(ctx, cwd, hook.Command, payload)
		if err != nil {
			return result, err
		}
		result = mergeResult(result, next)
		if len(next.UpdatedInput) > 0 {
			payload.Input = next.UpdatedInput
		}
	}
	return result, nil
}

func Validate(hooks map[string][]config.HookCommand) error {
	for event, commands := range hooks {
		event = strings.TrimSpace(event)
		if event == "" {
			return fmt.Errorf("hook event is required")
		}
		for _, command := range commands {
			if strings.TrimSpace(command.Command) == "" {
				return fmt.Errorf("hook %s command is required", event)
			}
			if strings.TrimSpace(command.Matcher) != "" && (strings.TrimSpace(command.Tool) != "" || len(command.Tools) > 0) {
				return fmt.Errorf("hook %s cannot set matcher and tool/tools together", event)
			}
		}
	}
	return nil
}

func matchesHook(hook config.HookCommand, payload Payload) bool {
	var patterns []string
	if strings.TrimSpace(hook.Matcher) != "" {
		patterns = append(patterns, splitMatchers(hook.Matcher)...)
	}
	if strings.TrimSpace(hook.Tool) != "" {
		patterns = append(patterns, hook.Tool)
	}
	patterns = append(patterns, hook.Tools...)
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if permissions.MatchAnyRule([]string{pattern}, payload.ToolName, payload.Input) {
			return true
		}
	}
	return false
}

func splitMatchers(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if text := strings.TrimSpace(field); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func runCommand(ctx context.Context, cwd, command string, payload Payload) (Result, error) {
	shell, shellArg := "/bin/sh", "-c"
	if runtime.GOOS == "windows" {
		shell, shellArg = "cmd.exe", "/C"
	}
	cmd := exec.CommandContext(ctx, shell, shellArg, command)
	cmd.Dir = cwd
	payloadBytes, _ := json.Marshal(payload)
	cmd.Env = procenv.Sanitized("CLAUDE_TOOL_NAME="+payload.ToolName, "CLAUDE_HOOK_EVENT="+payload.Event, "CLAUDE_HOOK_PAYLOAD="+string(payloadBytes))
	cmd.Stdin = bytes.NewReader(payloadBytes)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("hook %s failed: %w\n%s", payload.Event, err, out.String())
	}
	return parseResult(out.String()), nil
}

func parseResult(output string) Result {
	output = strings.TrimSpace(output)
	if output == "" {
		return Result{}
	}
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var result Result
		if err := json.Unmarshal([]byte(line), &result); err == nil {
			return result
		}
	}
	return Result{Message: output}
}

func mergeResult(a, b Result) Result {
	if b.PermissionDecision != "" {
		a.PermissionDecision = b.PermissionDecision
	}
	if b.PermissionDecisionReason != "" {
		a.PermissionDecisionReason = b.PermissionDecisionReason
	}
	if len(b.UpdatedInput) > 0 {
		a.UpdatedInput = b.UpdatedInput
	}
	if b.Message != "" {
		a.Message = b.Message
	}
	if b.AdditionalContext != "" {
		a.AdditionalContext = b.AdditionalContext
	}
	if b.HookSpecificOutput.HookEventName != "" || b.HookSpecificOutput.WorktreePath != "" {
		a.HookSpecificOutput = b.HookSpecificOutput
	}
	return a
}
