package task

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentruntime"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "niuma-task-transcripts-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects")); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

type fakeStreamer struct{}

var lastSystem string

func (fakeStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	lastSystem = req.System
	if req.Messages[0].Content[0].Text != "do it" {
		panic("unexpected prompt")
	}
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func TestTaskTool(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"description": "demo", "prompt": "do it"})
	res := New(fakeStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError || res.Content != "done" {
		t.Fatalf("result = %+v", res)
	}
}

// modelCapturingStreamer records the model and thinking effort of every request
// it serves so tests can assert the full chain Task tool → agentruntime.Request →
// resolved model/effort → StreamMessages request.
type modelCapturingStreamer struct {
	mu        sync.Mutex
	models    []string
	efforts   []string
	maxTokens []int
}

func (s *modelCapturingStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	effort := ""
	if req.Thinking != nil {
		effort = req.Thinking.Effort
	}
	s.mu.Lock()
	s.models = append(s.models, req.Model)
	s.efforts = append(s.efforts, effort)
	s.maxTokens = append(s.maxTokens, req.MaxTokens)
	s.mu.Unlock()
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func (s *modelCapturingStreamer) captured() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.models...)
}

func (s *modelCapturingStreamer) capturedEfforts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.efforts...)
}

// usageReportingStreamer returns fixed token usage so batch observability can be
// asserted end-to-end.
type usageReportingStreamer struct {
	in  int
	out int
}

func (s usageReportingStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage:      anthropic.Usage{InputTokens: s.in, OutputTokens: s.out},
	}, nil
}

func writeTaskSettings(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTaskToolSchemaExposesModelOverride(t *testing.T) {
	schema := string(New(fakeStreamer{}, "model").InputSchema())
	for _, want := range []string{`"model"`, `"sonnet"`, `"opus"`, `"haiku"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
	if got := strings.Count(schema, `"model"`); got < 2 {
		t.Fatalf("model override should exist at top level and inside tasks items, count=%d:\n%s", got, schema)
	}
}

func TestTaskToolSingleModelOverrideReachesRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTaskSettings(t, filepath.Join(os.Getenv("HOME"), ".niuma", "settings.json"), `{"subagentModelTiers":{"opus":"configured-large"}}`)
	streamer := &modelCapturingStreamer{}
	input, _ := json.Marshal(map[string]any{"description": "demo", "prompt": "do it", "model": "opus"})
	res := New(streamer, "claude-sonnet-4-6").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	got := streamer.captured()
	if len(got) != 1 || got[0] != "configured-large" {
		t.Fatalf("request model = %v, want [configured-large]", got)
	}
}

func TestTaskToolSingleModelOverrideInheritsOnNonAnthropicProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &modelCapturingStreamer{}
	input, _ := json.Marshal(map[string]any{"description": "demo", "prompt": "do it", "model": "haiku"})
	res := New(streamer, "glm5.1").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	got := streamer.captured()
	if len(got) != 1 || got[0] != "glm5.1" {
		t.Fatalf("request model = %v, want [glm5.1] (tier alias inherits parent on non-Anthropic)", got)
	}
}

func TestTaskToolSingleModelOverrideUsesConfiguredTierOnNonAnthropicProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTaskSettings(t, filepath.Join(home, ".niuma", "settings.json"), `{"subagentModelTiers":{"haiku":"glm-4-flash"}}`)
	streamer := &modelCapturingStreamer{}
	input, _ := json.Marshal(map[string]any{"description": "demo", "prompt": "do it", "model": "haiku"})
	res := New(streamer, "glm5.1").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	got := streamer.captured()
	if len(got) != 1 || got[0] != "glm-4-flash" {
		t.Fatalf("request model = %v, want [glm-4-flash] (configured tier)", got)
	}
}

func TestTaskToolBatchPerItemModelReachesRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTaskSettings(t, filepath.Join(os.Getenv("HOME"), ".niuma", "settings.json"), `{"subagentModelTiers":{"opus":"configured-large"}}`)
	streamer := &modelCapturingStreamer{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "a", "prompt": "do it", "model": "opus"},
			{"description": "b", "prompt": "do it"},
		},
		"max_concurrency": 1,
	})
	res := New(streamer, "claude-sonnet-4-6").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	got := streamer.captured()
	var sawOpus, sawParent bool
	for _, m := range got {
		switch m {
		case "configured-large":
			sawOpus = true
		case "claude-sonnet-4-6":
			sawParent = true
		}
	}
	if !sawOpus || !sawParent {
		t.Fatalf("batch request models = %v, want per-item opus override + inherited parent", got)
	}
}

func TestTaskToolDescriptionGuidesModelOverride(t *testing.T) {
	description := New(fakeStreamer{}, "model").Description()
	for _, want := range []string{"model override", "lower tier"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing conservative model-override guidance %q:\n%s", want, description)
		}
	}
}

func TestTaskToolSchemaExposesEffortOverride(t *testing.T) {
	schema := string(New(fakeStreamer{}, "model").InputSchema())
	for _, want := range []string{`"effort"`, `"low"`, `"medium"`, `"high"`, `"max"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
	if got := strings.Count(schema, `"effort"`); got < 2 {
		t.Fatalf("effort override should exist at top level and inside tasks items, count=%d:\n%s", got, schema)
	}
}

func TestTaskToolSchemaExposesExecutionOverrideFields(t *testing.T) {
	schema := string(New(fakeStreamer{}, "model").InputSchema())
	for _, want := range []string{`"provider"`, `"max_output_tokens"`, `"max_turns"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
}

func TestTaskToolMaxOutputTokensReachesRuntimeRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &modelCapturingStreamer{}
	input, _ := json.Marshal(map[string]any{"description": "bounded", "prompt": "do it", "max_output_tokens": 123})
	result := New(streamer, "claude-sonnet-4-6").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if len(streamer.maxTokens) != 1 || streamer.maxTokens[0] != 123 {
		t.Fatalf("max tokens = %v, want [123]", streamer.maxTokens)
	}
}

func TestTaskToolSingleEffortOverrideReachesRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &modelCapturingStreamer{}
	input, _ := json.Marshal(map[string]any{"description": "demo", "prompt": "do it", "effort": "high"})
	res := New(streamer, "claude-sonnet-4-6").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	got := streamer.capturedEfforts()
	if len(got) != 1 || got[0] != "high" {
		t.Fatalf("request thinking effort = %v, want [high]", got)
	}
}

func TestTaskToolBatchPerItemEffortReachesRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &modelCapturingStreamer{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "a", "prompt": "do it", "effort": "high"},
			{"description": "b", "prompt": "do it"},
		},
		"max_concurrency": 1,
	})
	res := New(streamer, "claude-sonnet-4-6").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	got := streamer.capturedEfforts()
	if len(got) != 2 || got[0] != "high" || got[1] != "high" {
		t.Fatalf("batch thinking efforts = %v, want explicit and default high", got)
	}
}

func TestTaskToolBatchCostUsesConfiguredPricing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeTaskSettings(t, filepath.Join(home, ".niuma", "settings.json"),
		`{"modelPricing":{"glm-5.1":{"input":2,"output":10}}}`)
	streamer := usageReportingStreamer{in: 1_000_000, out: 1_000_000}
	input, _ := json.Marshal(map[string]any{
		"tasks":           []map[string]any{{"description": "a", "prompt": "do it"}},
		"max_concurrency": 1,
	})
	res := New(streamer, "glm-5.1").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatalf("decode: %v\n%s", err, res.Content)
	}
	tasksAny, _ := decoded["tasks"].([]any)
	task, _ := tasksAny[0].(map[string]any)
	// glm-5.1 priced at 2/MTok input + 10/MTok output, 1M each → 12.
	if cost, _ := task["cost_usd"].(float64); cost != 12 {
		t.Fatalf("task cost_usd = %v, want 12 (configured pricing)", task["cost_usd"])
	}
	summary, _ := decoded["summary"].(map[string]any)
	if total, _ := summary["total_cost_usd"].(float64); total != 12 {
		t.Fatalf("summary total_cost_usd = %v, want 12", summary["total_cost_usd"])
	}
}

func TestTaskToolDescriptionGuidesEffortOverride(t *testing.T) {
	description := New(fakeStreamer{}, "model").Description()
	if !strings.Contains(description, "effort") {
		t.Fatalf("description missing effort-override guidance:\n%s", description)
	}
}

func TestTaskToolBatchReportsModelAndTokenUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := usageReportingStreamer{in: 100, out: 20}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "a", "prompt": "do it"},
			{"description": "b", "prompt": "do it"},
		},
		"max_concurrency": 1,
	})
	res := New(streamer, "claude-sonnet-4-6").Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatalf("decode batch response: %v\n%s", err, res.Content)
	}
	tasksAny, _ := decoded["tasks"].([]any)
	if len(tasksAny) != 2 {
		t.Fatalf("tasks = %v", decoded["tasks"])
	}
	for i, ta := range tasksAny {
		task, _ := ta.(map[string]any)
		if model, _ := task["model"].(string); model != "claude-sonnet-4-6" {
			t.Fatalf("task[%d] model = %v, want claude-sonnet-4-6", i, task["model"])
		}
		if out, _ := task["output_tokens"].(float64); int(out) != 20 {
			t.Fatalf("task[%d] output_tokens = %v, want 20", i, task["output_tokens"])
		}
		if in, _ := task["input_tokens"].(float64); int(in) != 100 {
			t.Fatalf("task[%d] input_tokens = %v, want 100", i, task["input_tokens"])
		}
	}
	summary, _ := decoded["summary"].(map[string]any)
	if total, _ := summary["total_output_tokens"].(float64); int(total) != 40 {
		t.Fatalf("summary total_output_tokens = %v, want 40", summary["total_output_tokens"])
	}
	if total, _ := summary["total_input_tokens"].(float64); int(total) != 200 {
		t.Fatalf("summary total_input_tokens = %v, want 200", summary["total_input_tokens"])
	}
}

func TestTaskToolReturnsCapabilityLoopDecisionContext(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"description": "demo", "prompt": "do it"})
	res := New(structuredCapabilityStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{
		"Summary: checked structured context.",
		"<capability_loop>",
		`"capability_loop"`,
		`"evidence": [`,
		"TASK_SYNC_EVIDENCE: internal/tools/task/task.go returns structured context.",
		`"unknowns": [`,
		`"verification": [`,
		`"next_action": "TASK_SYNC_NEXT_ACTION: parent should use the evidence before final answer."`,
		"These structured fields are parent decision context",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, res.Content)
		}
	}
}

func TestTaskToolDescriptionIncludesPromptContract(t *testing.T) {
	tool := New(fakeStreamer{}, "model")
	description := tool.Description()
	schema := string(tool.InputSchema())
	for _, want := range []string{
		"fresh conversation",
		"does not automatically see",
		"facts already known",
		"paths/functions/line numbers",
		"what was already ruled out",
		"Never delegate understanding",
		"prefer direct Glob/Grep/LS",
		"omit timeout_ms unless the user explicitly asks",
		"timeout_ms is an explicit hard deadline",
		"repo-wide audit, scan, docs alignment",
		"long single Task may return a background",
		"use AgentGet to check completion",
		"Expected output contract",
		"Summary, Evidence",
		"Assumptions, Unknowns",
		"Verification, Risks",
		"Next action",
		"reuse these fields in later planning",
		"exact headings",
		"one concise bullet under each heading",
		"None observed",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q:\n%s", want, description)
		}
	}
	for _, want := range []string{
		"Complete brief for the fresh sub-agent",
		"relevant files/functions/line numbers",
		"based on your findings",
		"Summary, Evidence",
		"Assumptions, Unknowns",
		"Verification, Risks",
		"Next action",
		"exact headings",
		"one concise bullet under each heading",
		"None observed",
		"For a single synchronous Task, omit this unless the user explicitly requested a deadline",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
}

func TestTaskToolSchemaGuidesSubagentTypeSelection(t *testing.T) {
	schema := string(New(fakeStreamer{}, "model").InputSchema())
	guidance := "Omit for the default general-purpose agent; do not pass 'default'."
	if got := strings.Count(schema, guidance); got != 2 {
		t.Fatalf("schema subagent_type guidance count = %d, want 2 (single and batch):\n%s", got, schema)
	}
	if !strings.Contains(schema, "general-purpose, Explore, Plan") {
		t.Fatalf("schema missing built-in agent type names:\n%s", schema)
	}
}

func TestTaskToolLoadsSubagent(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte("---\nname: reviewer\n---\nReview deeply."), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"description": "demo", "prompt": "do it", "subagent_type": "reviewer"})
	res := New(fakeStreamer{}, "model").Run(context.Background(), input, tools.Context{CWD: project})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(lastSystem, "Review deeply") {
		t.Fatalf("system = %q", lastSystem)
	}
}

func TestTaskToolRunsBackgroundAgent(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWriteTaskTest(t, filepath.Join(project, ".claude", "agents", "bg.md"), `---
name: bg
background: true
---
Run later.`)
	store := &taskFakeStore{}
	input, _ := json.Marshal(map[string]string{"description": "demo", "prompt": "do it", "subagent_type": "bg"})
	res := New(fakeStreamer{}, "model", WithTaskStore(store)).Run(context.Background(), input, tools.Context{CWD: project})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded backgroundResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatalf("content = %s: %v", res.Content, err)
	}
	if !decoded.Background || decoded.Status != agenttasks.StatusRunning || decoded.TaskID != 77 || decoded.AgentName != "bg" || decoded.SessionID == "" {
		t.Fatalf("decoded = %+v", decoded)
	}
	deadline := time.After(time.Second)
	for store.finished() == "" {
		select {
		case <-deadline:
			t.Fatalf("background task did not finish")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if store.finished() != agenttasks.StatusCompleted {
		t.Fatalf("status = %s", store.finished())
	}
}

func TestTaskToolBackgroundAgentRequiresTaskStore(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWriteTaskTest(t, filepath.Join(project, ".claude", "agents", "bg.md"), `---
name: bg
background: true
---
Run later.`)
	input, _ := json.Marshal(map[string]string{"description": "demo", "prompt": "do it", "subagent_type": "bg"})
	res := New(fakeStreamer{}, "model").Run(context.Background(), input, tools.Context{CWD: project})
	if !res.IsError || !strings.Contains(res.Content, "requires task store") {
		t.Fatalf("result = %+v", res)
	}
}

func TestTaskToolAutoBackgroundReturnsHandleAndContinues(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_AUTO_BACKGROUND_TASKS", "true")
	t.Setenv("NIUMA_AUTO_BACKGROUND_MS", "5")
	store := &taskFakeStore{}
	streamer := &autoBackgroundStreamer{release: make(chan struct{})}
	input, _ := json.Marshal(map[string]string{"description": "demo", "prompt": "do it"})
	res := New(streamer, "model", WithTaskStore(store)).Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded backgroundResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatalf("content = %s: %v", res.Content, err)
	}
	if !decoded.Background || decoded.Status != agenttasks.StatusRunning || decoded.TaskID != 77 || decoded.AgentName != "general-purpose" || decoded.Model != "model" {
		t.Fatalf("decoded = %+v", decoded)
	}
	if !strings.Contains(decoded.Instructions, "AgentGet") || !strings.Contains(decoded.Instructions, "Do not invent") {
		t.Fatalf("instructions = %q", decoded.Instructions)
	}
	close(streamer.release)
	deadline := time.After(time.Second)
	for store.finished() == "" {
		select {
		case <-deadline:
			t.Fatalf("background task did not finish")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if store.finished() != agenttasks.StatusCompleted {
		t.Fatalf("status = %s", store.finished())
	}
}

func TestTaskToolAutoBackgroundReturnsSyncResultWhenFast(t *testing.T) {
	t.Setenv("NIUMA_AUTO_BACKGROUND_TASKS", "true")
	t.Setenv("NIUMA_AUTO_BACKGROUND_MS", "1000")
	input, _ := json.Marshal(map[string]string{"description": "demo", "prompt": "do it"})
	res := New(fakeStreamer{}, "model", WithTaskStore(&taskFakeStore{})).Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError || res.Content != "done" {
		t.Fatalf("result = %+v", res)
	}
}

func TestTaskToolBatchCanStartBackgroundAgent(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWriteTaskTest(t, filepath.Join(project, ".claude", "agents", "bg.md"), `---
name: bg
background: true
---
Run later.`)
	store := &taskFakeStore{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]string{
			{"description": "demo", "prompt": "do it", "subagent_type": "bg"},
		},
	})
	res := New(fakeStreamer{}, "model", WithTaskStore(store)).Run(context.Background(), input, tools.Context{CWD: project})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Running != 1 || decoded.Summary.Completed != 0 || decoded.Summary.Failed != 0 {
		t.Fatalf("summary = %+v", decoded.Summary)
	}
	if len(decoded.Tasks) != 1 || !decoded.Tasks[0].Background || decoded.Tasks[0].Status != agenttasks.StatusRunning || decoded.Tasks[0].TaskID != 77 || decoded.Tasks[0].FinishedAt != "" {
		t.Fatalf("tasks = %+v", decoded.Tasks)
	}
}

func TestTaskToolRunsBatchConcurrently(t *testing.T) {
	streamer := &concurrentStreamer{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]string{
			{"description": "one", "prompt": "one"},
			{"description": "two", "prompt": "two"},
		},
		"max_concurrency": 2,
	})
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Total != 2 || decoded.Summary.Completed != 2 || decoded.Summary.MaxConcurrency != 2 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if len(decoded.Tasks) != 2 || decoded.Tasks[0].Content == "" || decoded.Tasks[1].Content == "" {
		t.Fatalf("tasks = %+v", decoded.Tasks)
	}
	if atomic.LoadInt32(&streamer.maxSeen) < 2 {
		t.Fatalf("batch did not run concurrently, maxSeen=%d", streamer.maxSeen)
	}
}

func TestTaskToolBatchReturnsCapabilityLoop(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]string{
			{"description": "one", "prompt": "do it"},
		},
	})
	res := New(structuredCapabilityStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{
		`"capability_loop":`,
		"TASK_SYNC_EVIDENCE: internal/tools/task/task.go returns structured context.",
		`"next_action": "TASK_SYNC_NEXT_ACTION: parent should use the evidence before final answer."`,
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, res.Content)
		}
	}
}

func TestTaskToolBatchRetriesFailures(t *testing.T) {
	streamer := &flakyStreamer{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]string{
			{"description": "flaky", "prompt": "flaky"},
		},
		"retry_attempts": 1,
	})
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Retries != 1 || decoded.Summary.Completed != 1 || decoded.Tasks[0].Attempts != 2 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Tasks[0].Status != "completed" || decoded.Tasks[0].Content != "flaky ok" {
		t.Fatalf("task = %+v", decoded.Tasks[0])
	}
}

func TestTaskToolSingleTimeout(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"description": "slow", "prompt": "slow", "timeout_ms": 5})
	res := New(blockingStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if !res.IsError || !strings.Contains(res.Content, context.DeadlineExceeded.Error()) {
		t.Fatalf("result = %+v", res)
	}
}

func TestTaskToolSingleTimeoutPreservesPartialEvidence(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"description": "partial slow", "prompt": "slow", "timeout_ms": 5})
	res := New(partialTextTimeoutStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("expected partial timeout result: %+v", res)
	}
	for _, want := range []string{
		"Sub-agent stopped before a clean final answer",
		context.DeadlineExceeded.Error(),
		"Partial answer:",
		"partial answer before timeout",
		`"status": "timeout"`,
		"Partial sub-agent content before timeout:",
		"Whether the partial sub-agent result covered all requested evidence before timeout.",
		"Partial sub-agent result may be incomplete, stale, or missing later evidence due to the timeout.",
		"Parent agent should preserve partial evidence, inspect transcript_path or output_file, then retry with a narrower scope or longer profile only if needed.",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, res.Content)
		}
	}
}

func TestTaskToolRespectsConfiguredMaxTurns(t *testing.T) {
	streamer := &alwaysToolStreamer{}
	registry := tools.NewRegistry(taskEchoTool{})
	input, _ := json.Marshal(map[string]string{"description": "bounded", "prompt": "use tools"})
	res := New(streamer, "model", WithRegistry(registry), WithMaxTurns(2)).Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("expected partial progress result: %+v", res)
	}
	if streamer.calls != 2 {
		t.Fatalf("stream calls = %d, want 2", streamer.calls)
	}
	for _, want := range []string{
		"sub-agent max turns reached (2)",
		"tool_calls: 2",
		"Treat this as an incomplete sub-agent result",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, res.Content)
		}
	}
}

func TestTaskToolReturnsPartialTextOnStreamError(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"description": "partial", "prompt": "partial"})
	res := New(partialTextErrorStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("expected partial success result: %+v", res)
	}
	for _, want := range []string{
		"Sub-agent stopped before a clean final answer",
		"upstream stream failed",
		"Partial answer:",
		"partial answer before failure",
		"<capability_loop>",
		`"status": "failed"`,
		`"capability_loop"`,
		"Partial sub-agent content before failed:",
		"Whether the partial sub-agent result covered all requested evidence before failure.",
		"Partial sub-agent result may be incomplete, stale, or missing later evidence due to the failure.",
		"Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation.",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, res.Content)
		}
	}
}

func TestPartialTaskResultContentKeepsArtifactHints(t *testing.T) {
	content, ok := partialTaskResultContent(agentruntime.Result{
		Content:        "partial evidence before error",
		Status:         agenttasks.StatusFailed,
		SessionID:      "partial-session",
		TranscriptPath: "/tmp/partial-task.jsonl",
		OutputFile:     "/tmp/partial-task.output",
		WorktreePath:   "/tmp/partial-task-worktree",
		WorktreeBranch: "agent/partial",
		TaskID:         77,
	}, errors.New("upstream stream failed"))
	if !ok {
		t.Fatal("expected partial task content")
	}
	for _, want := range []string{
		"session_id: partial-session",
		"transcript_path: /tmp/partial-task.jsonl",
		"output_file: /tmp/partial-task.output",
		"worktree_path: /tmp/partial-task-worktree",
		"worktree_branch: agent/partial",
		`"session_id": "partial-session"`,
		`"transcript_path": "/tmp/partial-task.jsonl"`,
		`"output_file": "/tmp/partial-task.output"`,
		`"worktree_path": "/tmp/partial-task-worktree"`,
		`"worktree_branch": "agent/partial"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestTaskToolReturnsToolProgressOnStreamError(t *testing.T) {
	registry := tools.NewRegistry(taskEchoTool{})
	input, _ := json.Marshal(map[string]string{"description": "partial", "prompt": "use tool"})
	res := New(&partialToolErrorStreamer{}, "model", WithRegistry(registry)).Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("expected partial progress result: %+v", res)
	}
	for _, want := range []string{
		"Sub-agent stopped before a clean final answer",
		"upstream stream failed after tool",
		"tool_calls: 1",
		"Echo input=",
		"Treat this as an incomplete sub-agent result",
		"<capability_loop>",
		`"status": "failed"`,
		"Partial sub-agent result may be incomplete, stale, or missing later evidence due to the failure.",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, res.Content)
		}
	}
}

func TestTaskToolBatchTimeout(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "slow", "prompt": "slow", "timeout_ms": 5},
		},
	})
	res := New(blockingStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if !res.IsError {
		t.Fatalf("expected timeout error result: %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Failed != 1 || decoded.Summary.Timeout != 1 || decoded.Tasks[0].Status != "timeout" || !strings.Contains(decoded.Tasks[0].Error, context.DeadlineExceeded.Error()) {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestTaskToolBatchRejectsTooSmallTimeoutForMultipleTasks(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "one", "prompt": "one"},
			{"description": "two", "prompt": "two"},
		},
		"max_concurrency": 2,
		"timeout_ms":      15000,
	})
	res := New(&concurrentStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if !res.IsError {
		t.Fatalf("expected invalid timeout result: %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Total != 2 || decoded.Summary.Failed != 2 || decoded.Summary.Completed != 0 || decoded.Summary.MaxConcurrency != 2 {
		t.Fatalf("summary = %+v", decoded.Summary)
	}
	for _, task := range decoded.Tasks {
		if task.Status != "invalid_timeout" || !task.IsError || task.Attempts != 0 {
			t.Fatalf("task = %+v", task)
		}
		if !strings.Contains(task.Error, "use >=30000") || !strings.Contains(task.Error, "Glob/Grep/LS") {
			t.Fatalf("error = %q", task.Error)
		}
	}
}

func TestTaskToolBatchRejectsShortTimeoutForDeepAudit(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "docs audit", "prompt": "scan the entire repo docs alignment"},
			{"description": "vitepress cross-reference", "prompt": "repo-wide VitePress link audit"},
		},
		"max_concurrency": 2,
		"timeout_ms":      60000,
	})
	res := New(&concurrentStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if !res.IsError {
		t.Fatalf("expected invalid timeout result: %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Total != 2 || decoded.Summary.Failed != 2 || decoded.Summary.Completed != 0 {
		t.Fatalf("summary = %+v", decoded.Summary)
	}
	for _, task := range decoded.Tasks {
		if task.Status != "invalid_timeout" || !task.IsError || task.Attempts != 0 {
			t.Fatalf("task = %+v", task)
		}
		if !strings.Contains(task.Error, "use >=180000") || !strings.Contains(task.Error, "repo-wide/deep-audit") {
			t.Fatalf("error = %q", task.Error)
		}
	}
}

func TestTaskToolBatchAllowsZeroTimeoutForMultipleTasks(t *testing.T) {
	streamer := &concurrentStreamer{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "one", "prompt": "one", "timeout_ms": 0},
			{"description": "two", "prompt": "two"},
		},
		"max_concurrency": 2,
		"timeout_ms":      0,
	})
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Total != 2 || decoded.Summary.Completed != 2 || decoded.Summary.Failed != 0 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestTaskToolBatchAllowsThirtySecondTimeoutForMultipleTasks(t *testing.T) {
	streamer := &concurrentStreamer{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "one", "prompt": "one", "timeout_ms": 30000},
			{"description": "two", "prompt": "two"},
		},
		"max_concurrency": 2,
	})
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.Total != 2 || decoded.Summary.Completed != 2 || decoded.Summary.Failed != 0 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestTaskToolBatchPriorityScheduling(t *testing.T) {
	streamer := &orderStreamer{}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "low", "prompt": "low", "priority": 1},
			{"description": "high", "prompt": "high", "priority": 10},
			{"description": "mid", "prompt": "mid", "priority": 5},
		},
		"max_concurrency":   1,
		"schedule_strategy": "priority",
	})
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.Join(streamer.prompts(), ","); got != "high,mid,low" {
		t.Fatalf("order = %s", got)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.ScheduleStrategy != "priority" || len(decoded.Tasks) != 3 || decoded.Tasks[0].Description != "low" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

type concurrentStreamer struct {
	active  int32
	maxSeen int32
}

func (s *concurrentStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	current := atomic.AddInt32(&s.active, 1)
	for {
		max := atomic.LoadInt32(&s.maxSeen)
		if current <= max || atomic.CompareAndSwapInt32(&s.maxSeen, max, current) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	atomic.AddInt32(&s.active, -1)
	text := req.Messages[0].Content[0].Text + " done"
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

type flakyStreamer struct {
	calls int32
}

type structuredCapabilityStreamer struct{}

func (structuredCapabilityStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if len(req.Messages) == 0 || req.Messages[0].Content[0].Text != "do it" {
		panic("unexpected prompt")
	}
	text := strings.Join([]string{
		"Summary: checked structured context.",
		"",
		"Evidence:",
		"- TASK_SYNC_EVIDENCE: internal/tools/task/task.go returns structured context.",
		"",
		"Unknowns:",
		"- TASK_SYNC_UNKNOWN: browser flow not sampled in this unit test.",
		"",
		"Verification:",
		"- go test ./internal/tools/task -count=1",
		"",
		"Risks:",
		"- TASK_SYNC_RISK: model may still ignore returned context.",
		"",
		"Next action:",
		"- TASK_SYNC_NEXT_ACTION: parent should use the evidence before final answer.",
	}, "\n")
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

func (s *flakyStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if atomic.AddInt32(&s.calls, 1) == 1 {
		return nil, errors.New("temporary failure")
	}
	text := req.Messages[0].Content[0].Text + " ok"
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

type blockingStreamer struct{}

func (blockingStreamer) StreamMessages(ctx context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type autoBackgroundStreamer struct {
	release chan struct{}
}

func (s *autoBackgroundStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if req.Messages[0].Content[0].Text != "do it" {
		panic("unexpected prompt")
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	_ = cb.OnText("auto done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "auto done"}}},
		StopReason: "end_turn",
	}, nil
}

type partialTextErrorStreamer struct{}

func (partialTextErrorStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		_ = cb.OnText("partial answer before failure")
	}
	return nil, errors.New("upstream stream failed")
}

type partialTextTimeoutStreamer struct{}

func (partialTextTimeoutStreamer) StreamMessages(ctx context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		_ = cb.OnText("partial answer before timeout")
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

type partialToolErrorStreamer struct {
	calls int
}

func (s *partialToolErrorStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_partial",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"partial evidence"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	return nil, errors.New("upstream stream failed after tool")
}

type alwaysToolStreamer struct {
	calls int
}

func (s *alwaysToolStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "toolu_loop_" + strconv.Itoa(s.calls),
			Name:  "Echo",
			Input: json.RawMessage(`{"text":"loop evidence"}`),
		}}},
		StopReason: "tool_use",
	}, nil
}

type taskEchoTool struct{}

func (taskEchoTool) Name() string        { return "Echo" }
func (taskEchoTool) Description() string { return "echo input text" }
func (taskEchoTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
}
func (taskEchoTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(input, &params)
	return tools.Result{Content: params.Text}
}

type orderStreamer struct {
	mu    sync.Mutex
	order []string
}

func (s *orderStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	text := req.Messages[0].Content[0].Text
	s.mu.Lock()
	s.order = append(s.order, text)
	s.mu.Unlock()
	_ = cb.OnText(text + " done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text + " done"}}},
		StopReason: "end_turn",
	}, nil
}

func (s *orderStreamer) prompts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

type taskFakeStore struct {
	mu     sync.Mutex
	status string
}

func (s *taskFakeStore) CreateAgentTask(context.Context, agenttasks.TaskInput) (uint64, error) {
	return 77, nil
}

func (s *taskFakeStore) FinishAgentTask(_ context.Context, taskID uint64, status string, resultJSON string) error {
	if taskID != 77 {
		panic("unexpected task id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	return nil
}

func (s *taskFakeStore) AppendAgentTaskEvent(context.Context, agenttasks.EventInput) (uint64, error) {
	return 1, nil
}

func (s *taskFakeStore) IsAgentTaskCancelled(context.Context, uint64) (bool, error) {
	return false, nil
}

func (s *taskFakeStore) finished() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func mustWriteTaskTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
