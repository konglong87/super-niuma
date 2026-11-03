package query

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/session"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tools"
	tasktool "github.com/konglong87/super-niuma/internal/tools/task"
)

func TestQueryGoldenToolTranscript(t *testing.T) {
	store := session.Store{Root: t.TempDir()}
	const sessionID = "11111111-1111-4111-8111-111111111111"
	recorder, err := store.NewRecorderWithID(t.TempDir(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	querySession := New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:    "test-model",
		MaxTurns: 3,
		CWD:      t.TempDir(),
		Recorder: recorder,
	})
	var sink strings.Builder
	result, err := querySession.Run(context.Background(), "hi", &sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"text_sink":       sink.String(),
		"result":          normalizeGoldenResult(result),
		"transcript":      normalizeGoldenEntries(entries),
		"resume_messages": MessagesFromTranscript(entries),
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		t.Fatal(err)
	}
	assertQueryGolden(t, "tool_transcript.json", got.String())
}

func TestQueryGoldenResumeRecovery(t *testing.T) {
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "inspect the repo"},
		{Type: "tool_result", ToolID: "toolu_orphan", ToolName: "Read", Content: "orphaned"},
		{Type: "message", Role: "assistant", Content: "I will inspect it."},
		{Type: "tool_call", ToolID: "toolu_missing", ToolName: "Read", Content: `{"file_path":"README.md"}`},
	}
	payload := map[string]any{
		"resume_messages": MessagesFromTranscript(entries),
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		t.Fatal(err)
	}
	assertQueryGolden(t, "resume_recovery.json", got.String())
}

func TestQueryGoldenContinuationIntent(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "93939393-9393-4939-8939-939393939393")
	if err != nil {
		t.Fatal(err)
	}
	streamer := &continuationAckStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test-model",
		MaxTurns: 4,
		CWD:      project,
		Recorder: recorder,
		InitialMessages: []anthropic.MessageParam{{
			Role: "assistant",
			Content: []anthropic.ContentBlock{{Type: "text", Text: `如果继续推进，优先级建议：
1. 补强 3-ai-agents — 这是最大缺口
2. 扩充 prompts

你想聊哪个方向？`}},
		}},
	})
	var sink strings.Builder
	result, err := querySession.Run(context.Background(), "好", &sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"text_sink":       sink.String(),
		"result":          normalizeGoldenResult(result),
		"transcript":      normalizeGoldenEntries(entries),
		"resume_messages": MessagesFromTranscript(entries),
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		t.Fatal(err)
	}
	assertQueryGolden(t, "continuation_intent.json", got.String())
}

func TestQueryGoldenCodeModeRecoveryMatrix(t *testing.T) {
	payload := map[string]any{
		"runtime_status_context":              goldenRuntimeStatusContext(t),
		"final_turn_tools":                    goldenFinalTurnTools(t),
		"tool_result_budget":                  goldenToolResultBudget(t),
		"partial_stream_recover":              goldenPartialStreamRecovery(t),
		"partial_task_recent_evidence":        goldenPartialTaskRecentEvidence(t),
		"terminal_task_store_recent_evidence": goldenTerminalTaskStoreRecentEvidence(t),
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		t.Fatal(err)
	}
	assertQueryGolden(t, "code_mode_recovery_matrix.json", got.String())
}

func TestQueryGoldenStreamJSONLongTailEvents(t *testing.T) {
	t.Setenv("NIUMA_DETERMINISTIC_STREAM_RESULT", "true")
	querySession := New(&taskProgressStreamer{}, tools.NewRegistry(progressTaskTool{}), Options{
		Model:                  "test-model",
		MaxTurns:               2,
		CWD:                    t.TempDir(),
		IncludeHookEvents:      true,
		IncludePartialMessages: true,
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.PreToolUse:  {{Command: `printf '{"message":"pre-ok"}'`}},
			hooks.PostToolUse: {{Command: `printf '{"message":"post-ok"}'`}},
		}),
	})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	assertStreamEventOrder(t, out.String(), []string{
		"turn_start",
		"message_start",
		"message_delta",
		"message_stop",
		"content_block_start",
		"input_json_delta",
		"content_block_stop",
		"tool_call",
		"hook_start",
		"hook_result",
		"nested_agent_progress",
		"nested_agent_progress",
		"nested_agent_progress",
		"nested_agent_progress",
		"nested_agent_progress",
		"nested_agent_progress",
		"nested_agent_progress",
		"nested_agent_progress",
		"hook_start",
		"hook_result",
		"tool_result",
		"nested_agent_progress",
		"turn_start",
		"message_start",
		"content_block_start",
		"text_delta",
		"partial_message",
		"content_block_stop",
		"message_delta",
		"message_stop",
		"result",
		"done",
	})
	assertQueryGolden(t, "stream_json_long_tail.jsonl", out.String())
}

func TestQueryGoldenStreamJSONNestedFailureEvents(t *testing.T) {
	t.Setenv("NIUMA_DETERMINISTIC_STREAM_RESULT", "true")
	querySession := New(&taskProgressStreamer{}, tools.NewRegistry(failedProgressTaskTool{}), Options{
		Model:    "test-model",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	assertStreamEventOrder(t, out.String(), []string{
		"turn_start",
		"message_start",
		"message_delta",
		"message_stop",
		"content_block_start",
		"input_json_delta",
		"content_block_stop",
		"tool_call",
		"nested_agent_progress",
		"nested_agent_progress",
		"nested_agent_progress",
		"tool_result",
		"nested_agent_progress",
		"turn_start",
		"message_start",
		"content_block_start",
		"text_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
		"result",
		"done",
	})
	assertQueryGolden(t, "stream_json_nested_failure.jsonl", out.String())
}

func TestQueryGoldenStreamJSONLongTailDeltaEvents(t *testing.T) {
	t.Setenv("NIUMA_DETERMINISTIC_STREAM_RESULT", "true")
	querySession := New(&longTailDeltaStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:               "test-model",
		MaxTurns:            1,
		CWD:                 t.TempDir(),
		IncludeStreamEvents: true,
	})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	assertStreamEventOrder(t, out.String(), []string{
		"turn_start",
		"message_start",
		"stream_event",
		"content_block_start",
		"stream_event",
		"thinking_delta",
		"stream_event",
		"signature_delta",
		"stream_event",
		"content_block_stop",
		"stream_event",
		"content_block_start",
		"stream_event",
		"text_delta",
		"stream_event",
		"citations_delta",
		"stream_event",
		"content_block_stop",
		"stream_event",
		"content_block_start",
		"stream_event",
		"connector_text_delta",
		"stream_event",
		"content_block_stop",
		"stream_event",
		"message_delta",
		"stream_event",
		"message_stop",
		"stream_event",
		"result",
		"done",
	})
	assertQueryGolden(t, "stream_json_long_tail_deltas.jsonl", out.String())
}

func goldenRuntimeStatusContext(t *testing.T) map[string]any {
	t.Helper()
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma", "todos.json"), `[
  {"id":"todo-1","content":"Collect code-mode evidence","status":"in_progress","priority":"high"}
]`)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma", "plan_mode.json"), `{"active":true,"plan":"Keep request-only runtime context visible."}`)
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          7,
		AgentName:   "general-purpose",
		Description: "audit prompt context",
		Status:      "running",
	}}}
	streamer := &runtimeStatusLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test-model",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
		TaskStore:  store,
	})
	result, err := querySession.Run(context.Background(), "inspect runtime context", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	requests := make([]map[string]any, 0, len(streamer.requests))
	for _, req := range streamer.requests {
		requests = append(requests, map[string]any{
			"tools":            len(req.Tools),
			"runtime_sections": queryGoldenRuntimeSections(req.Messages),
			"runtime_messages": queryGoldenRuntimeMessageCount(req.Messages),
		})
	}
	return map[string]any{
		"result_stop_reason": result.StopReason,
		"requests":           requests,
	}
}

func goldenFinalTurnTools(t *testing.T) map[string]any {
	t.Helper()
	streamer := &finalTurnToolDisableStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test-model",
		MaxTurns:   2,
		CWD:        t.TempDir(),
		PromptMode: "code",
	})
	result, err := querySession.Run(context.Background(), "inspect", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	toolCounts := make([]int, 0, len(streamer.requests))
	sections := make([][]string, 0, len(streamer.requests))
	for _, req := range streamer.requests {
		toolCounts = append(toolCounts, len(req.Tools))
		sections = append(sections, queryGoldenRuntimeSections(req.Messages))
	}
	return map[string]any{
		"result_stop_reason":  result.StopReason,
		"request_tool_counts": toolCounts,
		"runtime_sections":    sections,
	}
}

func goldenToolResultBudget(t *testing.T) map[string]any {
	t.Helper()
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "61616161-6161-4616-8616-616161616161")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	large := strings.Repeat("L", 9000)
	medium := strings.Repeat("M", 6000)
	small := strings.Repeat("S", 2000)
	streamer := &aggregateBudgetStreamer{large: large, medium: medium, small: small}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                   "test-model",
		CWD:                     project,
		Recorder:                recorder,
		MaxTurns:                2,
		ToolResultLimit:         10_000,
		ToolResultMessageBudget: 12_000,
		PromptMode:              "code",
	})
	result, err := querySession.Run(context.Background(), "run tools", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"result_stop_reason":        result.StopReason,
		"tool_result_count":         len(streamer.toolResults),
		"largest_persisted":         len(streamer.toolResults) > 0 && strings.Contains(streamer.toolResults[0], "<persisted-output>"),
		"medium_raw_preserved":      len(streamer.toolResults) > 1 && streamer.toolResults[1] == medium,
		"small_raw_preserved":       len(streamer.toolResults) > 2 && streamer.toolResults[2] == small,
		"content_replacement_count": queryGoldenContentReplacementCount(entries),
	}
}

func goldenPartialStreamRecovery(t *testing.T) map[string]any {
	t.Helper()
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "62626262-6262-4626-8626-626262626262")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	querySession := New(&partialTextStreamErrorStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:    "test-model",
		MaxTurns: 1,
		CWD:      project,
		Recorder: recorder,
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "answer", &out)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"result_stop_reason":         result.StopReason,
		"response":                   result.Response,
		"stdout":                     out.String(),
		"assistant_message_recorded": queryGoldenAssistantMessageRecorded(entries, "partial answer"),
	}
}

func goldenPartialTaskRecentEvidence(t *testing.T) map[string]any {
	t.Helper()
	project := t.TempDir()
	streamer := &partialTaskParentStreamer{}
	querySession := New(streamer, tools.NewRegistry(tasktool.New(partialTaskChildStreamer{}, "test-model")), Options{
		Model:      "test-model",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
	})
	result, err := querySession.Run(context.Background(), "delegate and recover", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var runtimeSections [][]string
	for _, req := range streamer.requests {
		runtimeSections = append(runtimeSections, queryGoldenRuntimeSections(req.Messages))
	}
	secondText := ""
	if len(streamer.requests) > 1 {
		secondText = allMessageText(streamer.requests[1].Messages)
	}
	return map[string]any{
		"request_count":                       len(streamer.requests),
		"result_stop_reason":                  result.StopReason,
		"runtime_sections":                    runtimeSections,
		"tool_result_has_capability_loop":     len(streamer.requests) > 1 && lastToolResultContains(streamer.requests[1].Messages, "<capability_loop>"),
		"recent_context_marks_failed":         strings.Contains(secondText, "Task result failed: partial stream audit"),
		"recent_context_has_partial_evidence": strings.Contains(secondText, "PARTIAL_TASK_STREAM_EVIDENCE"),
		"recent_context_has_recovery_risk":    strings.Contains(secondText, "Partial sub-agent result may be incomplete"),
		"recent_context_not_marked_completed": !strings.Contains(secondText, "Task result completed: partial stream audit"),
	}
}

func goldenTerminalTaskStoreRecentEvidence(t *testing.T) map[string]any {
	t.Helper()
	project := t.TempDir()
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          77,
		AgentName:   "reviewer",
		Description: "terminal task evidence",
		Status:      agenttasks.StatusFailed,
		ResultJSON:  `{"status":"failed","transcript_path":"/tmp/terminal-task.jsonl","output_file":"/tmp/terminal-task.output","capability_loop":{"evidence":["TERMINAL_TASK_STORE_EVIDENCE"],"unknowns":["TERMINAL_TASK_STORE_UNKNOWN"],"verification":["TERMINAL_TASK_STORE_VERIFY"],"risks":["TERMINAL_TASK_STORE_RISK"],"next_action":"TERMINAL_TASK_STORE_NEXT_ACTION"}}`,
	}}}
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test-model",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
		TaskStore:  store,
	})
	result, err := querySession.Run(context.Background(), "continue from terminal task", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	var messages []anthropic.MessageParam
	if len(streamer.messages) > 0 {
		messages = streamer.messages[0]
		text = allMessageText(messages)
	}
	return map[string]any{
		"result_stop_reason":              result.StopReason,
		"runtime_sections":                queryGoldenRuntimeSections(messages),
		"background_mentions_unavailable": strings.Contains(text, "AgentGet is not available"),
		"recent_context_has_source":       strings.Contains(text, "evidence_source: terminal_agent_task_store"),
		"recent_context_marks_failed":     strings.Contains(text, "#77 reviewer failed: terminal task evidence"),
		"recent_context_has_evidence":     strings.Contains(text, "TERMINAL_TASK_STORE_EVIDENCE"),
		"follow_up_has_source":            strings.Contains(text, "pending_follow_up: #77 reviewer failed: terminal task evidence; evidence_source: terminal_agent_task_store"),
		"follow_up_has_source_action":     strings.Contains(text, "source_action: This is fallback terminal task-store evidence; inspect the listed artifacts or rerun verification before relying on it as complete."),
		"follow_up_has_next_action":       strings.Contains(text, "must_handle_next_action: TERMINAL_TASK_STORE_NEXT_ACTION"),
		"follow_up_has_verification":      strings.Contains(text, "verification_required: TERMINAL_TASK_STORE_VERIFY"),
		"follow_up_has_risk":              strings.Contains(text, "risk_to_account_for: TERMINAL_TASK_STORE_RISK"),
	}
}

func queryGoldenRuntimeSections(messages []anthropic.MessageParam) []string {
	out := []string{}
	text := allMessageText(messages)
	for _, section := range []struct {
		heading string
		name    string
	}{
		{"## Turn budget reminder", "turn_budget"},
		{"## Tool planning reminder", "tool_planning"},
		{"## Permission context", "permission_context"},
		{"## Active todos", "active_todos"},
		{"## Plan mode", "plan_mode"},
		{"## Background agent tasks", "background_agent_tasks"},
		{"## Recent agent evidence decision context", "agent_evidence_decision_context"},
		{"## Agent capability follow-up gate", "agent_capability_follow_up_gate"},
	} {
		if strings.Contains(text, section.heading) {
			out = append(out, section.name)
		}
	}
	return out
}

func queryGoldenRuntimeMessageCount(messages []anthropic.MessageParam) int {
	count := 0
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == "text" && strings.Contains(block.Text, "Current runtime status for this coding session") {
				count++
			}
		}
	}
	return count
}

func queryGoldenContentReplacementCount(entries []session.Entry) int {
	count := 0
	for _, entry := range entries {
		if entry.Type == "content_replacement" {
			count += len(entry.Replacements)
		}
	}
	return count
}

func queryGoldenAssistantMessageRecorded(entries []session.Entry, content string) bool {
	for _, entry := range entries {
		if entry.Type == "message" && entry.Role == "assistant" && entry.Content == content {
			return true
		}
	}
	return false
}

type failedProgressTaskTool struct{}

func (failedProgressTaskTool) Name() string        { return "Task" }
func (failedProgressTaskTool) Description() string { return "failed progress task" }
func (failedProgressTaskTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (failedProgressTaskTool) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	if toolContext.TaskProgress != nil {
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 88, EventType: agenttasks.EventStarted, PayloadJSON: `{"agent_name":"debugger","model":"test-model"}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 88, EventType: agenttasks.EventFailed, PayloadJSON: `{"turn":1,"error":"sub-agent failed"}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 88, EventType: agenttasks.EventCancelled, PayloadJSON: `{"source":"retry-budget"}`})
	}
	return tools.Result{Content: "sub-agent failed", IsError: true}
}

func assertStreamEventOrder(t *testing.T, stream string, want []string) {
	t.Helper()
	var got []string
	dec := json.NewDecoder(strings.NewReader(stream))
	for {
		var event struct {
			Type string `json:"type"`
		}
		if err := dec.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		got = append(got, event.Type)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event order = %v, want %v\nstream:\n%s", got, want, stream)
	}
}

func normalizeGoldenResult(result Result) Result {
	result.TranscriptPath = "<TRANSCRIPT>"
	return result
}

func normalizeGoldenEntries(entries []session.Entry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.Type == session.EntryTypeRuntimeSpan {
			continue
		}
		record := map[string]any{
			"timestamp": "<timestamp>",
			"type":      entry.Type,
		}
		if entry.Role != "" {
			record["role"] = entry.Role
		}
		if entry.Content != "" {
			if entry.Type == "checkpoint" && strings.HasPrefix(entry.Name, "auto-") {
				record["content"] = "<message_id>"
			} else {
				record["content"] = entry.Content
			}
		}
		if entry.ToolID != "" {
			if entry.Type == "prompt_context" {
				record["tool_id"] = "<message_id>"
			} else {
				record["tool_id"] = entry.ToolID
			}
		}
		if entry.ToolName != "" {
			record["tool_name"] = entry.ToolName
		}
		if entry.IsError {
			record["is_error"] = entry.IsError
		}
		if entry.Name != "" {
			if entry.Type == "checkpoint" && strings.HasPrefix(entry.Name, "auto-") {
				record["name"] = "auto-<message_id>"
			} else {
				record["name"] = entry.Name
			}
		}
		if entry.Model != "" {
			record["model"] = entry.Model
		}
		if entry.InputTokens != 0 {
			record["input_tokens"] = entry.InputTokens
		}
		if entry.OutputTokens != 0 {
			record["output_tokens"] = entry.OutputTokens
		}
		out = append(out, record)
	}
	return out
}

func assertQueryGolden(t *testing.T, name string, got string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(strings.TrimRight(got, "\n")+"\n"), 0644); err != nil {
			t.Fatalf("update golden %s: %v", name, err)
		}
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n--- got ---\n%s", name, err, got)
	}
	want := strings.TrimRight(string(data), "\n")
	got = strings.TrimRight(got, "\n")
	if got != want {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
