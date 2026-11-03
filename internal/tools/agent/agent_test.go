package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentruntime"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/session"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "niuma-agent-transcripts-")
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

type singleTurnStreamer struct{}

func (singleTurnStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type structuredCapabilityStreamer struct{}

func (structuredCapabilityStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	text := strings.Join([]string{
		"Summary: checked foreground Agent context.",
		"",
		"Evidence:",
		"- AGENT_COMPAT_EVIDENCE: internal/tools/agent/agent.go returns structured context.",
		"",
		"Assumptions:",
		"- AGENT_COMPAT_ASSUMPTION: parent uses foreground Agent output directly.",
		"",
		"Unknowns:",
		"- AGENT_COMPAT_UNKNOWN: live provider behavior is not sampled here.",
		"",
		"Verification:",
		"- go test ./internal/tools/agent -count=1",
		"",
		"Risks:",
		"- AGENT_COMPAT_RISK: parent may still ignore tool result fields.",
		"",
		"Next action:",
		"- AGENT_COMPAT_NEXT_ACTION: parent should cite structured evidence before answering.",
	}, "\n")
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

type namedTestTool struct {
	name string
}

func (t namedTestTool) Name() string        { return t.name }
func (t namedTestTool) Description() string { return t.name }
func (t namedTestTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t namedTestTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: t.name}
}

type blockingStreamer struct {
	started chan struct{}
}

func (s blockingStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	close(s.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

type captureMessageStreamer struct {
	mu       sync.Mutex
	models   []string
	messages []anthropic.MessageParam
}

func (s *captureMessageStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.mu.Lock()
	s.models = append(s.models, req.Model)
	s.messages = append([]anthropic.MessageParam(nil), req.Messages...)
	s.mu.Unlock()
	_ = cb.OnText("resumed done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "resumed done"}}},
		StopReason: "end_turn",
	}, nil
}

func (s *captureMessageStreamer) messageText(index int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.messages) || len(s.messages[index].Content) == 0 {
		return ""
	}
	return s.messages[index].Content[0].Text
}

func (s *captureMessageStreamer) lastModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.models) == 0 {
		return ""
	}
	return s.models[len(s.models)-1]
}

func (s *captureMessageStreamer) toolResultText(toolUseID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range s.messages {
		for _, block := range message.Content {
			if block.Type == "tool_result" && block.ToolUseID == toolUseID {
				return block.Content
			}
		}
	}
	return ""
}

func (s *captureMessageStreamer) containsText(snippets ...string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range s.messages {
		for _, block := range message.Content {
			if block.Type != "text" {
				continue
			}
			matched := true
			for _, snippet := range snippets {
				if !strings.Contains(block.Text, snippet) {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}

type writeMarkerStreamer struct {
	calls int
}

func (s *writeMarkerStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_write_marker",
				Name:  "WriteMarker",
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	_ = cb.OnText("changed")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "changed"}}},
		StopReason: "end_turn",
	}, nil
}

type writeMarkerTool struct{}

func (writeMarkerTool) Name() string        { return "WriteMarker" }
func (writeMarkerTool) Description() string { return "write marker in cwd" }
func (writeMarkerTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (writeMarkerTool) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	path := filepath.Join(toolContext.CWD, "changed-by-agent.txt")
	if err := os.WriteFile(path, []byte("changed\n"), 0600); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: path}
}

type cwdProbeStreamer struct {
	calls int
}

func (s *cwdProbeStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_cwd_probe",
				Name:  "CWDProbe",
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	_ = cb.OnText("cwd checked")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "cwd checked"}}},
		StopReason: "end_turn",
	}, nil
}

type cwdProbeTool struct {
	mu  sync.Mutex
	cwd string
}

func (t *cwdProbeTool) Name() string        { return "CWDProbe" }
func (t *cwdProbeTool) Description() string { return "capture cwd" }
func (t *cwdProbeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (t *cwdProbeTool) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	t.mu.Lock()
	t.cwd = toolContext.CWD
	t.mu.Unlock()
	return tools.Result{Content: toolContext.CWD}
}
func (t *cwdProbeTool) capturedCWD() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cwd
}

func TestAgentCreateStartsBackgroundTask(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	store := &fakeStore{}
	input := json.RawMessage(`{"description":"demo","prompt":"do it"}`)
	res := NewCreate(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), input, tools.Context{CWD: project, TaskStore: store})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	waitForFinishedTask(t, store)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	outputFile, _ := decoded["output_file"].(string)
	if decoded["status"] != agenttasks.StatusRunning || decoded["background"] != true || decoded["agent_name"] != "general-purpose" || decoded["task_id"].(float64) != 42 || strings.TrimSpace(outputFile) == "" {
		t.Fatalf("decoded = %+v", decoded)
	}
	deadline := time.After(time.Second)
	for store.finished() == "" {
		select {
		case <-deadline:
			t.Fatal("background task did not finish")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestAgentAcceptsExecutionOverrideFields(t *testing.T) {
	project := t.TempDir()
	streamer := &captureMessageStreamer{}
	input := json.RawMessage(`{"description":"override","prompt":"do it","model":"gpt-test","effort":"high","max_output_tokens":123,"max_turns":2,"timeout_ms":1000}`)
	res := NewCompat(streamer, "fallback", tools.NewRegistry()).Run(context.Background(), input, tools.Context{CWD: project})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if streamer.lastModel() != "gpt-test" {
		t.Fatalf("effective model = %q", streamer.lastModel())
	}
}

func TestAgentCompatSchemaGuidesSubagentTypeSelection(t *testing.T) {
	schema := string(NewCompat(singleTurnStreamer{}, "model", tools.NewRegistry()).InputSchema())
	if !strings.Contains(schema, "Omit to use the default general-purpose agent; do not pass 'default'.") {
		t.Fatalf("schema missing subagent_type omission guidance:\n%s", schema)
	}
}

func TestAgentToolDescriptionsIncludePromptContract(t *testing.T) {
	compat := NewCompat(singleTurnStreamer{}, "model", tools.NewRegistry())
	compatDescription := compat.Description()
	compatSchema := string(compat.InputSchema())
	for _, want := range []string{
		"Claude Code compatible",
		"run_in_background",
		"complete brief",
		"known facts",
		"ruled-out paths",
		"When NOT to use Agent",
		"Launch multiple agents concurrently",
		"Foreground is the default",
		"multiple Agent tool use content blocks",
		"Each Agent invocation starts with fresh context",
		"you own the synthesis",
		"must not modify files",
		"Good brief checklist",
		"Summary, Evidence",
		"Assumptions",
		"Unknowns",
		"Verification",
		"Risks",
		"Next action",
		"exact headings",
		"one concise bullet under",
		"None observed",
	} {
		if !strings.Contains(compatDescription, want) {
			t.Fatalf("Agent description missing %q:\n%s", want, compatDescription)
		}
	}
	for _, want := range []string{
		`"$schema"`,
		`"description"`,
		`"prompt"`,
		`Summary, Evidence`,
		`Assumptions, Unknowns`,
		`Verification, Risks`,
		`Next action`,
		`exact headings`,
		`one concise bullet under each heading`,
		`None observed`,
		`"subagent_type"`,
		`"model"`,
		`Takes precedence over the agent definition`,
		`"run_in_background"`,
		`"isolation"`,
		`\"worktree\" creates`,
		`"required":["description","prompt"]`,
	} {
		if !strings.Contains(compatSchema, want) {
			t.Fatalf("Agent schema missing %q:\n%s", want, compatSchema)
		}
	}
	if strings.Contains(compatSchema, "unsupported") {
		t.Fatalf("Agent schema should not describe worktree isolation as unsupported:\n%s", compatSchema)
	}

	create := NewCreate(singleTurnStreamer{}, "model", tools.NewRegistry())
	createDescription := create.Description()
	createSchema := string(create.InputSchema())
	for _, want := range []string{
		"fresh conversation",
		"does not automatically",
		"known facts",
		"ruled-out paths",
		"files/functions/line",
		"Summary, Evidence",
		"Assumptions, Unknowns",
		"Verification",
		"Risks",
		"Next action",
		"exact headings",
		"one concise bullet under",
		"None observed",
		"omit subagent_type",
		"real specialized agent type",
		"do not fabricate or predict",
		"synthesize completed agent results",
	} {
		if !strings.Contains(createDescription, want) {
			t.Fatalf("AgentCreate description missing %q:\n%s", want, createDescription)
		}
	}
	for _, want := range []string{
		"Complete brief for the fresh background sub-agent",
		"relevant files/functions/line numbers",
		"Summary, Evidence",
		"Assumptions, Unknowns",
		"Verification, Risks",
		"Next action",
		"exact headings",
		"one concise bullet under",
		"None observed",
		"based on your findings",
		"Omit for the default background agent",
		"Do not set this to mode names",
	} {
		if !strings.Contains(createSchema, want) {
			t.Fatalf("AgentCreate schema missing %q:\n%s", want, createSchema)
		}
	}
	getDescription := NewGet().Description()
	for _, want := range []string{
		"sanitized final result",
		"tool call inputs and outputs are omitted",
		"result.capability_loop",
		"structured parent decision context",
		"before finalizing, retrying, or recovering",
		"status only",
		"do not infer or invent",
		"task is completed",
	} {
		if !strings.Contains(getDescription, want) {
			t.Fatalf("AgentGet description missing %q:\n%s", want, getDescription)
		}
	}
	messageDescription := NewMessage().Description()
	for _, want := range []string{
		"clarify scope",
		"concrete facts",
		"avoid sending",
		"infer missing context",
	} {
		if !strings.Contains(messageDescription, want) {
			t.Fatalf("AgentMessage description missing %q:\n%s", want, messageDescription)
		}
	}
}

func TestAgentCompatDescriptionMatchesEnabledDirectTools(t *testing.T) {
	readGrep := NewCompat(singleTurnStreamer{}, "model", tools.NewRegistry(
		namedTestTool{name: "Read"},
		namedTestTool{name: "Grep"},
	)).Description()
	for _, want := range []string{
		"use Read directly",
		"try Grep\n  first",
		"use Read directly",
	} {
		if !strings.Contains(readGrep, want) {
			t.Fatalf("Agent description missing %q:\n%s", want, readGrep)
		}
	}
	for _, forbidden := range []string{"Read or Glob", "Glob or Grep", "try Glob"} {
		if strings.Contains(readGrep, forbidden) {
			t.Fatalf("Agent description mentions unavailable Glob via %q:\n%s", forbidden, readGrep)
		}
	}

	withGlob := NewCompat(singleTurnStreamer{}, "model", tools.NewRegistry(
		namedTestTool{name: "Read"},
		namedTestTool{name: "Grep"},
		namedTestTool{name: "Glob"},
	)).Description()
	for _, want := range []string{"Read or Glob", "Grep or Glob"} {
		if !strings.Contains(withGlob, want) {
			t.Fatalf("Agent description missing %q when Glob is enabled:\n%s", want, withGlob)
		}
	}
}

func TestAgentCompatDescriptionListsWorkspaceAgents(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "agents", "reviewer.md"), `---
name: reviewer
description: Independent code review
tools: [Read, Grep, Bash]
disallowedTools: [Bash]
---
Review carefully.`)
	mustWrite(t, filepath.Join(project, ".claude", "agents", "planner.md"), `---
name: planner
description: Implementation planner
disallowedTools: [Write, Edit]
---
Plan carefully.`)

	description := NewCompatWithHooksAndCWD(singleTurnStreamer{}, "model", tools.NewRegistry(), hooks.Runner{}, project).Description()
	for _, want := range []string{
		"- general-purpose:",
		"- Explore: Fast agent specialized for exploring codebases.",
		"- Plan: Software architect agent for designing implementation plans.",
		"- planner: Implementation planner (Tools: All tools except Write, Edit)",
		"- reviewer: Independent code review (Tools: Read, Grep)",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("Agent description missing %q:\n%s", want, description)
		}
	}
}

func TestAgentCompatRunsSynchronously(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	res := NewCompat(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"do it"}`), tools.Context{CWD: project})
	if res.IsError || res.Content != "done" {
		t.Fatalf("result = %+v", res)
	}
}

func TestAgentCompatReturnsCapabilityLoopDecisionContext(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	res := NewCompat(structuredCapabilityStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"do it"}`), tools.Context{CWD: project})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{
		"Summary: checked foreground Agent context.",
		"<capability_loop>",
		`"capability_loop"`,
		"AGENT_COMPAT_EVIDENCE: internal/tools/agent/agent.go returns structured context.",
		`"assumptions": [`,
		`"risks": [`,
		`"next_action": "AGENT_COMPAT_NEXT_ACTION: parent should cite structured evidence before answering."`,
		"These structured fields are parent decision context",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, res.Content)
		}
	}
}

func TestAgentCompatPassesModelOverride(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".niuma"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".niuma", "settings.json"), []byte(`{"subagentModelTiers":{"opus":"configured-large"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	streamer := &captureMessageStreamer{}
	res := NewCompat(streamer, "claude-sonnet-4-6", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"do it","model":"opus"}`), tools.Context{CWD: project})
	if res.IsError || res.Content != "resumed done" {
		t.Fatalf("result = %+v", res)
	}
	if got := streamer.lastModel(); got != "configured-large" {
		t.Fatalf("model = %q", got)
	}
}

func TestAgentCompatRunsInBackground(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	store := &fakeStore{}
	res := NewCompat(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"do it","run_in_background":true}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError || !strings.Contains(res.Content, `"background": true`) || !strings.Contains(res.Content, `"task_id": 42`) || !strings.Contains(res.Content, `"output_file"`) {
		t.Fatalf("result = %+v", res)
	}
	waitForFinishedTask(t, store)
}

func TestAgentCompatBackgroundWorktreeDoesNotReturnPathBeforeCompletion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	project := initGitRepo(t)
	store := &fakeStore{}
	res := NewCompat(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"do it","run_in_background":true,"isolation":"worktree"}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Content, "worktree_path") || strings.Contains(res.Content, "worktree_branch") {
		t.Fatalf("background launch should not expose worktree path before completion: %s", res.Content)
	}
	waitForFinishedTask(t, store)
	if strings.Contains(store.resultJSON, "worktree_path") {
		t.Fatalf("unchanged background worktree should be cleaned from final result: %s", store.resultJSON)
	}
}

func TestAgentCompatCleansUnchangedWorktreeIsolation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	project := initGitRepo(t)
	streamer := &captureMessageStreamer{}
	res := NewCompat(streamer, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"do it","isolation":"worktree"}`), tools.Context{CWD: project})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Content, "worktreePath:") {
		t.Fatalf("unchanged worktree should be cleaned and omitted from result: %s", res.Content)
	}
	if !streamer.containsText("isolated git worktree", project) {
		t.Fatalf("worktree notice missing from request messages: %+v", streamer.messages)
	}
	entries, err := os.ReadDir(filepath.Join(project, ".niuma", "worktrees"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unchanged worktree entries were not cleaned: %v", entries)
	}
}

func TestAgentCompatKeepsChangedWorktreeIsolation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	project := initGitRepo(t)
	res := NewCompat(&writeMarkerStreamer{}, "model", tools.NewRegistry(writeMarkerTool{})).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"write marker","isolation":"worktree"}`), tools.Context{CWD: project})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	worktreePath := textAfter(res.Content, "worktreePath: ")
	if worktreePath == "" || !strings.Contains(worktreePath, filepath.Join(".niuma", "worktrees")) {
		t.Fatalf("missing changed worktree path in result: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("worktree was not kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "changed-by-agent.txt")); err != nil {
		t.Fatalf("marker was not written in worktree cwd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "changed-by-agent.txt")); !os.IsNotExist(err) {
		t.Fatalf("marker leaked into parent cwd: %v", err)
	}
}

func TestAgentCompatUsesWorktreeCreateHookIsolationWithoutGit(t *testing.T) {
	parent := t.TempDir()
	hookWorktree := filepath.Join(t.TempDir(), "hook-worktree")
	hookRunner := hooks.New(map[string][]config.HookCommand{
		hooks.WorktreeCreate: {{Command: "mkdir -p " + strconv.Quote(hookWorktree) + " && printf %s " + strconv.Quote(hookWorktree)}},
	})
	res := NewCompatWithHooks(&writeMarkerStreamer{}, "model", tools.NewRegistry(writeMarkerTool{}), hookRunner).Run(context.Background(), json.RawMessage(`{"description":"audit","prompt":"write marker","isolation":"worktree"}`), tools.Context{CWD: parent})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Content, "worktreePath: "+hookWorktree) {
		t.Fatalf("missing hook worktree path in result: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(hookWorktree, "changed-by-agent.txt")); err != nil {
		t.Fatalf("marker was not written in hook worktree cwd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "changed-by-agent.txt")); !os.IsNotExist(err) {
		t.Fatalf("marker leaked into parent cwd: %v", err)
	}
}

func TestAgentCreateTimeoutMarksBackgroundTaskTimedOut(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	store := &fakeStore{}
	streamer := blockingStreamer{started: make(chan struct{})}
	res := NewCreate(streamer, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"description":"demo","prompt":"do it","timeout_ms":10}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	select {
	case <-streamer.started:
	case <-time.After(time.Second):
		t.Fatal("background task did not start")
	}
	deadline := time.After(time.Second)
	for store.finished() == "" {
		select {
		case <-deadline:
			t.Fatal("background task was not marked timeout")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if store.finished() != agenttasks.StatusTimeout {
		t.Fatalf("status = %s", store.finished())
	}
}

func TestAgentListGetAndStop(t *testing.T) {
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{ID: 42, AgentName: "reviewer", Status: agenttasks.StatusRunning}}}
	listRes := NewList().Run(context.Background(), json.RawMessage(`{"limit":5}`), tools.Context{TaskStore: store})
	if listRes.IsError || !strings.Contains(listRes.Content, `"agent_name": "reviewer"`) {
		t.Fatalf("list result = %+v", listRes)
	}
	getRes := NewGet().Run(context.Background(), json.RawMessage(`{"task_id":42}`), tools.Context{TaskStore: store})
	if getRes.IsError || !strings.Contains(getRes.Content, `"id": 42`) {
		t.Fatalf("get result = %+v", getRes)
	}
	controller := agenttasks.NewController()
	ctx, cancel := context.WithCancel(context.Background())
	controller.Register(42, cancel)
	attachedGetRes := NewGet().Run(context.Background(), json.RawMessage(`{"task_id":42}`), tools.Context{TaskStore: store, TaskController: controller})
	if attachedGetRes.IsError || !strings.Contains(attachedGetRes.Content, `"process_attachment": "attached_to_current_process"`) {
		t.Fatalf("attached get result = %+v", attachedGetRes)
	}
	stopRes := NewStop().Run(ctx, json.RawMessage(`{"task_id":42,"reason":"done"}`), tools.Context{TaskStore: store, TaskController: controller})
	if stopRes.IsError || !strings.Contains(stopRes.Content, `"cancelled": true`) || store.cancelledTaskID != 42 {
		t.Fatalf("stop result = %+v store=%+v", stopRes, store)
	}
	detachedGetRes := NewGet().Run(context.Background(), json.RawMessage(`{"task_id":42}`), tools.Context{TaskStore: store, TaskController: controller})
	if detachedGetRes.IsError || !strings.Contains(detachedGetRes.Content, `"process_attachment": "not_attached_to_current_process"`) {
		t.Fatalf("detached get result = %+v", detachedGetRes)
	}
	msgRes := NewMessage().Run(context.Background(), json.RawMessage(`{"task_id":42,"from_agent":"coordinator","content":"please continue"}`), tools.Context{TaskStore: store, TraceID: "trace-1"})
	if msgRes.IsError || !strings.Contains(msgRes.Content, `"sent": true`) || len(store.events) != 1 || store.events[0].EventType != agenttasks.EventMessage {
		t.Fatalf("message result = %+v events=%+v", msgRes, store.events)
	}
	if store.events[0].TraceID != "trace-1" || !strings.Contains(store.events[0].PayloadJSON, "please continue") {
		t.Fatalf("message event = %+v", store.events[0])
	}
}

func TestAgentStopStoresPartialTextResult(t *testing.T) {
	existingResult, err := json.Marshal(agentruntime.Result{
		OutputFile:        "/tmp/agent-stop.output",
		WorktreePath:      "/tmp/agent-stop-worktree",
		WorktreeBranch:    "agent/stop",
		WorktreeHookBased: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		tasks: []mysqlstore.AgentTask{{
			ID:                 42,
			SubagentSessionKey: "sub-session",
			AgentName:          "reviewer",
			Status:             agenttasks.StatusRunning,
			Model:              "model",
			ResultJSON:         string(existingResult),
		}},
		agentEvents: []mysqlstore.AgentTaskEvent{
			{ID: 1, TaskID: 42, EventType: agenttasks.EventTextDelta, PayloadJSON: `{"text":"GO_AGENT_CHILD_PARTIAL_MARKER: partial result before cancellation"}`},
		},
	}
	controller := agenttasks.NewController()
	ctx, cancel := context.WithCancel(context.Background())
	controller.Register(42, cancel)

	res := NewStop().Run(ctx, json.RawMessage(`{"task_id":42,"reason":"done"}`), tools.Context{TaskStore: store, TaskController: controller})
	if res.IsError {
		t.Fatalf("stop result = %+v", res)
	}
	var stored agentruntime.Result
	if err := json.Unmarshal([]byte(store.cancelledResultJSON), &stored); err != nil {
		t.Fatalf("cancelled result json = %q: %v", store.cancelledResultJSON, err)
	}
	if stored.Status != agenttasks.StatusCancelled || stored.TaskID != 42 || stored.AgentName != "reviewer" || stored.SessionID != "sub-session" {
		t.Fatalf("stored result = %+v", stored)
	}
	if stored.OutputFile != "/tmp/agent-stop.output" || stored.WorktreePath != "/tmp/agent-stop-worktree" || stored.WorktreeBranch != "agent/stop" || !stored.WorktreeHookBased {
		t.Fatalf("stored artifacts = output=%q worktree=%q branch=%q hook=%v", stored.OutputFile, stored.WorktreePath, stored.WorktreeBranch, stored.WorktreeHookBased)
	}
	if !strings.Contains(stored.Content, "GO_AGENT_CHILD_PARTIAL_MARKER") || !strings.Contains(stored.Content, "Cancelled: sub-agent task cancelled: done") {
		t.Fatalf("stored content = %q", stored.Content)
	}
	if stored.CapabilityLoop == nil || len(stored.CapabilityLoop.Evidence) == 0 || !strings.Contains(stored.CapabilityLoop.Evidence[0], "GO_AGENT_CHILD_PARTIAL_MARKER") || stored.CapabilityLoop.NextAction == "" {
		t.Fatalf("stored capability loop = %+v", stored.CapabilityLoop)
	}
}

func TestAgentMessageRejectsNonRunningTask(t *testing.T) {
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{
		ID:        42,
		AgentName: "reviewer",
		Status:    agenttasks.StatusCompleted,
	}}}
	res := NewMessage().Run(context.Background(), json.RawMessage(`{"task_id":42,"from_agent":"coordinator","content":"please continue"}`), tools.Context{TaskStore: store})
	if !res.IsError || !strings.Contains(res.Content, "only send to running") || !strings.Contains(res.Content, "completed") {
		t.Fatalf("message result = %+v", res)
	}
	if len(store.events) != 0 {
		t.Fatalf("terminal task should not receive message events: %+v", store.events)
	}
}

func TestSendMessageMapsPlainTextToAgentMessage(t *testing.T) {
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{
		ID:        42,
		AgentName: "reviewer",
		Status:    agenttasks.StatusRunning,
	}}}
	res := NewSendMessageWithRuntime(nil, "", nil).Run(context.Background(), json.RawMessage(`{"to":"#42","summary":"continue","message":"please continue"}`), tools.Context{TaskStore: store})
	if res.IsError {
		t.Fatalf("send message result = %+v", res)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %+v", store.events)
	}
	var payload agenttasks.MessageInput
	if err := json.Unmarshal([]byte(store.events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("payload = %q: %v", store.events[0].PayloadJSON, err)
	}
	if payload.TaskID != 42 || payload.FromAgent != "coordinator" || payload.Content != "please continue" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSendMessageMapsUniqueAgentNameToAgentMessage(t *testing.T) {
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{
		ID:        42,
		AgentName: "reviewer",
		Status:    agenttasks.StatusRunning,
	}}}
	res := NewSendMessageWithRuntime(nil, "", nil).Run(context.Background(), json.RawMessage(`{"to":"reviewer","summary":"continue","message":"please continue"}`), tools.Context{TaskStore: store})
	if res.IsError {
		t.Fatalf("send message result = %+v", res)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %+v", store.events)
	}
	var payload agenttasks.MessageInput
	if err := json.Unmarshal([]byte(store.events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("payload = %q: %v", store.events[0].PayloadJSON, err)
	}
	if payload.TaskID != 42 || payload.Content != "please continue" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSendMessageMapsUniqueDescriptionToAgentMessage(t *testing.T) {
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{
		ID:          42,
		AgentName:   "general-purpose",
		Description: "review auth flow",
		Status:      agenttasks.StatusRunning,
	}}}
	res := NewSendMessageWithRuntime(nil, "", nil).Run(context.Background(), json.RawMessage(`{"to":"review auth flow","summary":"continue","message":"please continue"}`), tools.Context{TaskStore: store})
	if res.IsError {
		t.Fatalf("send message result = %+v", res)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %+v", store.events)
	}
	var payload agenttasks.MessageInput
	if err := json.Unmarshal([]byte(store.events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("payload = %q: %v", store.events[0].PayloadJSON, err)
	}
	if payload.TaskID != 42 || payload.Content != "please continue" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSendMessageRejectsAmbiguousAgentName(t *testing.T) {
	store := &fakeStore{tasks: []mysqlstore.AgentTask{
		{ID: 42, AgentName: "reviewer", Status: agenttasks.StatusRunning},
		{ID: 43, AgentName: "reviewer", Status: agenttasks.StatusRunning},
	}}
	res := NewSendMessageWithRuntime(nil, "", nil).Run(context.Background(), json.RawMessage(`{"to":"reviewer","summary":"continue","message":"please continue"}`), tools.Context{TaskStore: store})
	if !res.IsError || !strings.Contains(res.Content, "ambiguous") || !strings.Contains(res.Content, "42") || !strings.Contains(res.Content, "43") {
		t.Fatalf("send message result = %+v", res)
	}
	if len(store.events) != 0 {
		t.Fatalf("ambiguous recipient should not receive message events: %+v", store.events)
	}
}

func TestSendMessageRejectsUnsupportedShapes(t *testing.T) {
	tool := NewSendMessageWithRuntime(nil, "", nil)
	for _, input := range []string{
		`{"to":"*","message":"hello"}`,
		`{"to":"reviewer","message":"hello"}`,
		`{"to":"42","message":{"type":"shutdown_request"}}`,
	} {
		res := tool.Run(context.Background(), json.RawMessage(input), tools.Context{TaskStore: &fakeStore{}})
		if !res.IsError {
			t.Fatalf("input %s should fail: %+v", input, res)
		}
	}
}

func TestAgentMessageResumesTerminalTaskFromTranscript(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	sessionID, err := session.NewID()
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := session.DefaultStore().NewRecorderWithID(project, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "original brief"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "assistant", Content: "previous finding"}); err != nil {
		t.Fatal(err)
	}
	original := strings.Repeat("R", 9000)
	replacement := "<persisted-output>\nresume preview\n</persisted-output>"
	if err := recorder.Append(session.Entry{Type: "tool_call", ToolID: "toolu_resume", ToolName: "Echo", Content: `{"text":"large"}`}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "tool_result", ToolID: "toolu_resume", ToolName: "Echo", Content: original}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "content_replacement", Replacements: []session.ReplacementRecord{{
		Kind:        "tool-result",
		ToolUseID:   "toolu_resume",
		Replacement: replacement,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(agentruntime.Result{TranscriptPath: recorder.Path, SessionID: sessionID, Status: agenttasks.StatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	streamer := &captureMessageStreamer{}
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{
		ID:                 41,
		SubagentSessionKey: sessionID,
		AgentName:          "general-purpose",
		Description:        "resume me",
		Status:             agenttasks.StatusCompleted,
		Model:              "model",
		ResultJSON:         string(resultJSON),
	}}}
	res := NewMessageWithRuntime(streamer, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"task_id":41,"from_agent":"coordinator","content":"please continue with new evidence"}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError || !strings.Contains(res.Content, `"resumed": true`) || !strings.Contains(res.Content, `"resumed_from_task_id": 41`) || !strings.Contains(res.Content, `"sent": true`) {
		t.Fatalf("message result = %+v", res)
	}
	waitForFinishedTask(t, store)
	if got := streamer.messageText(0); got != "original brief" {
		t.Fatalf("initial user message = %q", got)
	}
	if got := streamer.messageText(1); got != "previous finding" {
		t.Fatalf("initial assistant message = %q", got)
	}
	if got := streamer.toolResultText("toolu_resume"); got != replacement {
		t.Fatalf("resumed tool result = %.120q, want replacement", got)
	}
	if got := streamer.toolResultText("toolu_resume"); strings.Contains(got, original) {
		t.Fatalf("resumed request still contains raw original")
	}
	if !streamer.containsText("Agent message from coordinator", "please continue with new evidence") {
		t.Fatalf("resume prompt missing agent message")
	}
	if !streamer.containsText("## Sub-agent tool planning reminder") {
		t.Fatalf("resume prompt missing sub-agent planning reminder")
	}
}

func TestAgentMessageResumesTerminalTaskInRetainedWorktree(t *testing.T) {
	project := t.TempDir()
	retainedWorktree := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	sessionID, err := session.NewID()
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := session.DefaultStore().NewRecorderWithID(project, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "original worktree task"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "assistant", Content: "previous worktree finding"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(agentruntime.Result{
		TranscriptPath: recorder.Path,
		SessionID:      sessionID,
		Status:         agenttasks.StatusCompleted,
		WorktreePath:   retainedWorktree,
		WorktreeBranch: "worktree-agent-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{
		ID:                 41,
		SubagentSessionKey: sessionID,
		AgentName:          "general-purpose",
		Description:        "resume worktree",
		Status:             agenttasks.StatusCompleted,
		Model:              "model",
		ResultJSON:         string(resultJSON),
	}}}
	res := NewMessageWithRuntime(&writeMarkerStreamer{}, "model", tools.NewRegistry(writeMarkerTool{})).Run(context.Background(), json.RawMessage(`{"task_id":41,"from_agent":"coordinator","content":"continue in the retained worktree"}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError || !strings.Contains(res.Content, `"resumed": true`) {
		t.Fatalf("message result = %+v", res)
	}
	if !strings.Contains(res.Content, `"worktree_resume": "retained"`) || !strings.Contains(res.Content, retainedWorktree) {
		t.Fatalf("message result missing retained worktree evidence:\n%s", res.Content)
	}
	waitForFinishedTask(t, store)
	if _, err := os.Stat(filepath.Join(retainedWorktree, "changed-by-agent.txt")); err != nil {
		t.Fatalf("successor did not run in retained worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "changed-by-agent.txt")); !os.IsNotExist(err) {
		t.Fatalf("successor leaked write into parent cwd: %v", err)
	}
	if !strings.Contains(store.lastMetadataJSON, retainedWorktree) {
		t.Fatalf("successor metadata did not retain worktree path: %s", store.lastMetadataJSON)
	}
}

func TestAgentMessageResumeMissingWorktreeFallsBackWithEvidence(t *testing.T) {
	project := t.TempDir()
	missingWorktree := filepath.Join(t.TempDir(), "removed-worktree")
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	sessionID, err := session.NewID()
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := session.DefaultStore().NewRecorderWithID(project, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: "original missing worktree task"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(agentruntime.Result{
		TranscriptPath: recorder.Path,
		SessionID:      sessionID,
		Status:         agenttasks.StatusCompleted,
		WorktreePath:   missingWorktree,
		WorktreeBranch: "worktree-removed",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{tasks: []mysqlstore.AgentTask{{
		ID:                 41,
		SubagentSessionKey: sessionID,
		AgentName:          "general-purpose",
		Description:        "resume missing worktree",
		Status:             agenttasks.StatusCompleted,
		Model:              "model",
		ResultJSON:         string(resultJSON),
	}}}
	probe := &cwdProbeTool{}
	res := NewMessageWithRuntime(&cwdProbeStreamer{}, "model", tools.NewRegistry(probe)).Run(context.Background(), json.RawMessage(`{"task_id":41,"from_agent":"coordinator","content":"continue after missing worktree"}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError || !strings.Contains(res.Content, `"resumed": true`) {
		t.Fatalf("message result = %+v", res)
	}
	for _, want := range []string{
		`"worktree_resume": "fallback_parent_cwd"`,
		`"worktree_fallback_reason": "previous retained worktree no longer exists; resumed in parent cwd"`,
		`"previous_worktree_path": "` + missingWorktree + `"`,
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("message result missing %q:\n%s", want, res.Content)
		}
	}
	waitForFinishedTask(t, store)
	if got := probe.capturedCWD(); got != project {
		t.Fatalf("fallback successor cwd = %q, want parent cwd %q", got, project)
	}
	if _, err := os.Stat(missingWorktree); !os.IsNotExist(err) {
		t.Fatalf("missing worktree should not be recreated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "changed-by-agent.txt")); !os.IsNotExist(err) {
		t.Fatalf("fallback probe should not write parent cwd: %v", err)
	}
	if strings.Contains(store.lastMetadataJSON, missingWorktree) {
		t.Fatalf("successor metadata should not retain missing worktree path: %s", store.lastMetadataJSON)
	}
}

func TestWorktreeInfoCandidatesReadsResultAndMetadataHookFields(t *testing.T) {
	resultJSON := `{"worktree_path":"/tmp/result-worktree","worktree_branch":"result-branch","worktree_hook_based":true}`
	metadataJSON := `{"worktree_path":"/tmp/metadata-worktree","worktree_branch":"metadata-branch","worktree_head_commit":"abc123","worktree_git_root":"/tmp/root","worktree_hook_based":true}`
	candidates := worktreeInfoCandidates(mysqlstore.AgentTask{ResultJSON: resultJSON, MetadataJSON: metadataJSON})
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].Path != "/tmp/result-worktree" || candidates[0].Branch != "result-branch" || !candidates[0].HookBased {
		t.Fatalf("result candidate = %+v", candidates[0])
	}
	if candidates[1].Path != "/tmp/metadata-worktree" || candidates[1].Branch != "metadata-branch" || candidates[1].HeadCommit != "abc123" || candidates[1].GitRoot != "/tmp/root" || !candidates[1].HookBased {
		t.Fatalf("metadata candidate = %+v", candidates[1])
	}
}

func TestAgentCreateRequiresTaskStore(t *testing.T) {
	res := NewCreate(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"prompt":"do it"}`), tools.Context{})
	if !res.IsError || !strings.Contains(res.Content, "requires task store") {
		t.Fatalf("result = %+v", res)
	}
}

func TestAgentCreateLoadsSubagentDefinition(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\n---\nReview deeply.")
	store := &fakeStore{}
	res := NewCreate(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"prompt":"do it","subagent_type":"reviewer"}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	waitForFinishedTask(t, store)
}

func TestAgentCreateTeammateModeRecordsMetadata(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	store := &fakeStore{}
	res := NewCreate(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"prompt":"do it","mode":"teammate"}`), tools.Context{CWD: project, TaskStore: store})
	if res.IsError || !strings.Contains(res.Content, `"agent_mode": "teammate"`) {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(store.lastMetadataJSON, `"agent_mode":"teammate"`) {
		t.Fatalf("metadata = %s", store.lastMetadataJSON)
	}
	waitForFinishedTask(t, store)
}

func TestAgentCreateRejectsInvalidMode(t *testing.T) {
	res := NewCreate(singleTurnStreamer{}, "model", tools.NewRegistry()).Run(context.Background(), json.RawMessage(`{"prompt":"do it","mode":"worker"}`), tools.Context{CWD: t.TempDir(), TaskStore: &fakeStore{}})
	if !res.IsError || !strings.Contains(res.Content, "mode must be subagent or teammate") {
		t.Fatalf("result = %+v", res)
	}
}

func TestAgentGetIncludesProgressSummary(t *testing.T) {
	store := &fakeStore{
		tasks: []mysqlstore.AgentTask{{ID: 42, AgentName: "reviewer", Status: agenttasks.StatusRunning}},
		agentEvents: []mysqlstore.AgentTaskEvent{
			{ID: 1, TaskID: 42, EventType: agenttasks.EventStarted, PayloadJSON: `{"agent_name":"reviewer","model":"model"}`},
			{ID: 2, TaskID: 42, EventType: agenttasks.EventTurnStart, PayloadJSON: `{"turn":1}`},
			{ID: 3, TaskID: 42, EventType: agenttasks.EventMessage, PayloadJSON: `{"from_agent":"coordinator","content":"continue"}`},
			{ID: 4, TaskID: 42, EventType: agenttasks.EventToolCall, PayloadJSON: `{"tool_name":"Read"}`},
			{ID: 5, TaskID: 42, EventType: agenttasks.EventTextDelta, PayloadJSON: `{"text":"checking"}`},
		},
	}
	res := NewGet().Run(context.Background(), json.RawMessage(`{"task_id":42}`), tools.Context{TaskStore: store})
	if res.IsError || !strings.Contains(res.Content, `"progress_summary"`) || !strings.Contains(res.Content, `"messages": 1`) || !strings.Contains(res.Content, `"tool_calls": 1`) {
		t.Fatalf("result = %+v", res)
	}
}

func TestAgentGetOmitsSubagentToolOutputsFromStoredResult(t *testing.T) {
	resultJSON, err := json.Marshal(map[string]any{
		"content":         "final finding",
		"agent_name":      "reviewer",
		"model":           "model",
		"session_id":      "sub-session",
		"transcript_path": "/tmp/subagent.jsonl",
		"output_file":     "/tmp/subagent.output",
		"worktree_path":   "/tmp/subagent-worktree",
		"worktree_branch": "agent/audit",
		"turns":           2,
		"capability_loop": map[string]any{
			"evidence":     []string{"internal/tools/agent/agent.go:563 exposes sanitized result fields."},
			"assumptions":  []string{"Parent still needs to synthesize the finding."},
			"unknowns":     []string{"No live model transcript was inspected."},
			"verification": []string{"go test ./internal/tools/agent -count=1"},
			"risks":        []string{"Large content may be previewed instead of fully inlined."},
			"next_action":  "Parent should verify the evidence before answering.",
		},
		"tool_calls": []map[string]any{{
			"id":     "toolu_1",
			"name":   "Read",
			"input":  `{"file_path":"secret.go"}`,
			"output": strings.Repeat("large sidechain output ", 20),
		}},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		tasks: []mysqlstore.AgentTask{{
			ID:                 42,
			SubagentSessionKey: "sub-session",
			AgentName:          "reviewer",
			Description:        "review",
			Status:             agenttasks.StatusCompleted,
			Model:              "model",
			ResultJSON:         string(resultJSON),
		}},
		agentEvents: []mysqlstore.AgentTaskEvent{
			{ID: 1, TaskID: 42, EventType: agenttasks.EventStarted, PayloadJSON: `{"agent_name":"reviewer","model":"model"}`},
			{ID: 2, TaskID: 42, EventType: agenttasks.EventToolCall, PayloadJSON: `{"tool_name":"Read"}`},
			{ID: 3, TaskID: 42, EventType: agenttasks.EventCompleted, PayloadJSON: `{}`},
		},
	}
	res := NewGet().Run(context.Background(), json.RawMessage(`{"task_id":42}`), tools.Context{TaskStore: store})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{
		`"content": "final finding"`,
		`"tool_call_count": 1`,
		`"tool_calls_omitted"`,
		`"transcript_path": "/tmp/subagent.jsonl"`,
		`"output_file": "/tmp/subagent.output"`,
		`"worktree_path": "/tmp/subagent-worktree"`,
		`"worktree_branch": "agent/audit"`,
		`"progress_summary"`,
		`"capability_loop"`,
		`"evidence": [`,
		`"internal/tools/agent/agent.go:563 exposes sanitized result fields."`,
		`"unknowns": [`,
		`"verification": [`,
		`"next_action": "Parent should verify the evidence before answering."`,
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, res.Content)
		}
	}
	for _, forbidden := range []string{
		"large sidechain output",
		`"result_json"`,
		`"tool_calls": [`,
		`"input": "{`,
		`"output": "`,
	} {
		if strings.Contains(res.Content, forbidden) {
			t.Fatalf("result should omit %q:\n%s", forbidden, res.Content)
		}
	}
}

type fakeStore struct {
	mu                  sync.Mutex
	tasks               []mysqlstore.AgentTask
	events              []agenttasks.EventInput
	agentEvents         []mysqlstore.AgentTaskEvent
	finishedStatus      string
	resultJSON          string
	cancelledTaskID     uint64
	lastMetadataJSON    string
	cancelledResultJSON string
}

func (f *fakeStore) CreateAgentTask(_ context.Context, input agenttasks.TaskInput) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastMetadataJSON = input.MetadataJSON
	f.tasks = append(f.tasks, mysqlstore.AgentTask{ID: 42, AgentName: "reviewer", Status: agenttasks.StatusRunning})
	return 42, nil
}

func (f *fakeStore) FinishAgentTask(_ context.Context, taskID uint64, status string, resultJSON string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishedStatus = status
	f.resultJSON = resultJSON
	return nil
}

func (f *fakeStore) AppendAgentTaskEvent(_ context.Context, input agenttasks.EventInput) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, input)
	return uint64(len(f.events)), nil
}

func (f *fakeStore) ListAgentTasks(context.Context, int) ([]mysqlstore.AgentTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mysqlstore.AgentTask(nil), f.tasks...), nil
}

func (f *fakeStore) ListAgentTaskEvents(_ context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]mysqlstore.AgentTaskEvent, 0)
	for _, event := range f.agentEvents {
		if event.TaskID == taskID {
			out = append(out, event)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) CancelAgentTask(_ context.Context, taskID uint64, resultJSON string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelledTaskID = taskID
	f.cancelledResultJSON = resultJSON
	return nil
}

func (f *fakeStore) IsAgentTaskCancelled(context.Context, uint64) (bool, error) {
	return false, nil
}

func (f *fakeStore) finished() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finishedStatus
}

func waitForFinishedTask(t *testing.T, store *fakeStore) {
	t.Helper()
	deadline := time.After(time.Second)
	for store.finished() == "" {
		select {
		case <-deadline:
			t.Fatal("background task did not finish")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func textAfter(text, prefix string) string {
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return ""
	}
	value := text[idx+len(prefix):]
	if end := strings.IndexByte(value, '\n'); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
