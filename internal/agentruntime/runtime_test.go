package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/promptdump"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
)

type sharedStateAuthorizationSpy struct {
	runs int
}

type panicTool struct {
	name string
}

func (t panicTool) Name() string        { return t.name }
func (t panicTool) Description() string { return "panic test tool" }
func (t panicTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t panicTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	panic("panic-tool-marker")
}

func TestRuntimeRecoversToolPanicAsToolError(t *testing.T) {
	runtime := Runtime{}
	trace := runtime.runTool(context.Background(), tools.NewRegistry(panicTool{name: "PanicTool"}),
		anthropic.ContentBlock{ID: "toolu_panic", Name: "PanicTool"},
		tools.Context{}, nil, nil, Request{CWD: t.TempDir()}, 42, "worker")
	if !trace.IsError {
		t.Fatalf("trace = %+v, want tool error", trace)
	}
	if !strings.Contains(trace.Output, "panic-tool-marker") {
		t.Fatalf("trace output = %q, want panic marker", trace.Output)
	}
}

func TestRuntimeRecoversHookPanicAsHookError(t *testing.T) {
	_, err := runHookWithRecovery(context.Background(), hooks.PreToolUse, func() (hooks.Result, error) {
		panic("panic-hook-marker")
	})
	if err == nil || !strings.Contains(err.Error(), "panic-hook-marker") {
		t.Fatalf("error = %v, want hook panic marker", err)
	}
}

type panicStreamer struct{}

func (panicStreamer) StreamMessages(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	panic("panic-runtime-marker")
}

func TestRuntimeConvergesTaskAfterRuntimePanic(t *testing.T) {
	project := t.TempDir()
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    panicStreamer{},
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
		MaxTurns:  1,
	}

	result, err := runtime.Run(context.Background(), Request{Prompt: "panic", CWD: project}, tools.Context{CWD: project})
	if err == nil || !strings.Contains(err.Error(), "panic-runtime-marker") {
		t.Fatalf("Run() error = %v, want runtime panic marker", err)
	}
	if result.Status != agenttasks.StatusFailed {
		t.Fatalf("result status = %q, want failed", result.Status)
	}
	if store.finished() != agenttasks.StatusFailed {
		t.Fatalf("task status = %q, want failed", store.finished())
	}
	if store.finishCalls != 1 {
		t.Fatalf("finish calls = %d, want exactly one", store.finishCalls)
	}
}

func TestRuntimeFailsBeforeTaskCreationWhenRecorderCannotInitialize(t *testing.T) {
	rootFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(rootFile, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:        &finalTextStreamer{text: "should not run"},
		Registry:      tools.NewRegistry(echoTool{}),
		Model:         "base-model",
		TaskStore:     store,
		RecorderStore: session.Store{TranscriptProjectsRoot: rootFile},
	}

	_, err := runtime.Run(context.Background(), Request{Prompt: "persist", CWD: t.TempDir()}, tools.Context{})
	if err == nil || !strings.Contains(err.Error(), "create transcript recorder") {
		t.Fatalf("Run() error = %v, want recorder initialization error", err)
	}
	if len(store.createdTasks()) != 0 {
		t.Fatalf("created tasks = %+v, want none", store.createdTasks())
	}
}

func TestRuntimeSurfacesTranscriptAppendFailure(t *testing.T) {
	recorder, err := (session.Store{TranscriptProjectsRoot: t.TempDir()}).NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	appendErr := recordAssistant(recorder, []anthropic.ContentBlock{{Type: "text", Text: "after close"}})
	if appendErr == nil {
		t.Fatal("recordAssistant() returned nil after recorder close")
	}
	var persistence persistenceState
	persistence.record("append assistant transcript", appendErr)
	var result Result
	persistence.apply(&result)
	if !result.PersistenceDegraded || !strings.Contains(result.PersistenceError, "append assistant transcript") {
		t.Fatalf("result persistence state = %+v", result)
	}
}

func TestAtomicWriteFilesReplaceCompleteContents(t *testing.T) {
	output := filepath.Join(t.TempDir(), "agent.output")
	if err := writeAgentOutputFile(output, "complete output"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "complete output" {
		t.Fatalf("output = %q err=%v", string(got), err)
	}
	if err := writeAgentOutputStateFile(output, agenttasks.StatusCompleted, []byte(`{"content":"complete output"}`)); err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(output + ".state.json")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(state) || !strings.Contains(string(state), `"status":"completed"`) {
		t.Fatalf("state = %q", string(state))
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(output), ".*.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestFinishTaskSurfacesOutputPersistenceFailure(t *testing.T) {
	rootFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(rootFile, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &fakeTaskStore{}
	runtime := Runtime{TaskStore: store}

	result := runtime.finishTask(context.Background(), Request{}, 101, agenttasks.StatusCompleted, Result{
		OutputFile: filepath.Join(rootFile, "agent.output"),
		Content:    "done",
	})
	if !result.PersistenceDegraded || !strings.Contains(result.PersistenceError, "write output state") {
		t.Fatalf("result persistence state = %+v", result)
	}
	var stored Result
	if err := json.Unmarshal([]byte(store.resultJSON), &stored); err != nil {
		t.Fatalf("stored result = %q: %v", store.resultJSON, err)
	}
	if !stored.PersistenceDegraded {
		t.Fatalf("stored result persistence state = %+v", stored)
	}
}

func (s *sharedStateAuthorizationSpy) Name() string        { return "Bash" }
func (s *sharedStateAuthorizationSpy) Description() string { return "spy" }
func (s *sharedStateAuthorizationSpy) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (s *sharedStateAuthorizationSpy) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	s.runs++
	return tools.Result{Content: "executed"}
}

func TestSubagentCannotExpandParentSharedStateAuthorization(t *testing.T) {
	spy := &sharedStateAuthorizationSpy{}
	registry := tools.NewRegistry(spy)
	runtime := Runtime{}
	block := anthropic.ContentBlock{ID: "toolu_commit", Name: "Bash", Input: json.RawMessage(`{"command":"git commit -m x"}`)}

	trace := runtime.runTool(context.Background(), registry, block, tools.Context{}, nil, nil, Request{
		Prompt: "model-generated delegated prompt: commit the changes",
		CWD:    t.TempDir(),
	}, 1, "worker")
	if !trace.IsError || spy.runs != 0 || !strings.Contains(trace.Output, "Shared-State Authorization Gate") {
		t.Fatalf("delegated prompt expanded parent authorization: trace=%+v runs=%d", trace, spy.runs)
	}

	parent := tools.Context{SharedStateAuthorization: gitpolicy.ParseAuthorization("Commit the current changes.")}
	trace = runtime.runTool(context.Background(), registry, block, parent, nil, nil, Request{
		Prompt: "delegated implementation task",
		CWD:    t.TempDir(),
	}, 2, "worker")
	if trace.IsError || spy.runs != 1 {
		t.Fatalf("explicit parent authorization did not propagate: trace=%+v runs=%d", trace, spy.runs)
	}
}

func TestSubagentRechecksGitAuthorizationAfterHookRewrite(t *testing.T) {
	spy := &sharedStateAuthorizationSpy{}
	runtime := Runtime{Hooks: hooks.New(map[string][]config.HookCommand{
		hooks.PreToolUse: {{Command: `printf '%s' '{"updatedInput":{"command":"git commit -m hook"}}'`}},
	})}
	block := anthropic.ContentBlock{ID: "toolu_hook_commit", Name: "Bash", Input: json.RawMessage(`{"command":"git status --short"}`)}
	trace := runtime.runTool(context.Background(), tools.NewRegistry(spy), block, tools.Context{}, nil, nil, Request{CWD: t.TempDir()}, 1, "worker")
	if !trace.IsError || spy.runs != 0 || !strings.Contains(trace.Output, "Shared-State Authorization Gate") {
		t.Fatalf("hook-rewritten subagent commit bypassed authorization: trace=%+v runs=%d", trace, spy.runs)
	}
}

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "niuma-agentruntime-transcripts-")
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

type scriptedStreamer struct {
	calls              int
	systems            []string
	models             []string
	maxTokens          []int
	tools              [][]anthropic.ToolDefinition
	messages           [][]anthropic.MessageParam
	thinking           []*anthropic.ThinkingConfig
	toolName           string
	expectedToolResult string
}

type finalTextStreamer struct {
	text    string
	systems []string
}

func TestRuntimeUsesProviderResolverAndReportsEffectiveProvider(t *testing.T) {
	streamer := &finalTextStreamer{text: "resolved"}
	runtime := Runtime{
		Model: "base-model",
		ClientResolver: func(_ context.Context, provider, model string) (MessageStreamer, func(), error) {
			if provider != "alternate" || model != "base-model" {
				t.Fatalf("resolver request provider=%q model=%q", provider, model)
			}
			return streamer, func() {}, nil
		},
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", Provider: "alternate", CWD: t.TempDir()}, tools.Context{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "alternate" || result.Content != "resolved" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimeRejectsProviderOverrideWithoutResolver(t *testing.T) {
	runtime := Runtime{Client: &finalTextStreamer{text: "unused"}, Model: "base-model"}
	if _, err := runtime.Run(context.Background(), Request{Prompt: "do it", Provider: "alternate", CWD: t.TempDir()}, tools.Context{CWD: t.TempDir()}); err == nil {
		t.Fatal("expected provider override preflight error")
	}
}

func (s *finalTextStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.systems = append(s.systems, req.System)
	if err := cb.OnText(s.text); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: s.text}}},
		StopReason: "end_turn",
	}, nil
}

func (s *scriptedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	s.systems = append(s.systems, req.System)
	s.models = append(s.models, req.Model)
	s.maxTokens = append(s.maxTokens, req.MaxTokens)
	s.tools = append(s.tools, req.Tools)
	s.messages = append(s.messages, req.Messages)
	s.thinking = append(s.thinking, req.Thinking)
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  firstNonEmpty(s.toolName, "Echo"),
				Input: json.RawMessage(`{"text":"from subagent"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	wantToolResult := firstNonEmpty(s.expectedToolResult, "from subagent")
	if !requestMessagesContainText(req.Messages, wantToolResult) {
		return nil, io.ErrUnexpectedEOF
	}
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func requestMessagesContainText(messages []anthropic.MessageParam, want string) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Content == want || block.Text == want {
				return true
			}
		}
	}
	return false
}

func requestMessagesContainSubstring(messages []anthropic.MessageParam, want string) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if strings.Contains(block.Content, want) || strings.Contains(block.Text, want) {
				return true
			}
		}
	}
	return false
}

func toolDefinitionNames(defs []anthropic.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

type echoTool struct{}

func (echoTool) Name() string        { return "Echo" }
func (echoTool) Description() string { return "echo input text" }
func (echoTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
}
func (echoTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(input, &params)
	return tools.Result{Content: params.Text}
}

type namedNoopTool struct {
	name string
}

func (t namedNoopTool) Name() string        { return t.name }
func (t namedNoopTool) Description() string { return t.name }
func (t namedNoopTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (t namedNoopTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: t.name}
}

type cwdTool struct{}

func (cwdTool) Name() string        { return "Cwd" }
func (cwdTool) Description() string { return "return cwd" }
func (cwdTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (cwdTool) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	return tools.Result{Content: toolContext.CWD}
}

type largeTool struct {
	content       string
	maxResultSize int
}

func (largeTool) Name() string        { return "LongTool" }
func (largeTool) Description() string { return "long output tool" }
func (largeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}
func (t largeTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: t.content}
}
func (t largeTool) MaxResultSizeChars() int { return t.maxResultSize }

type persistedSubagentStreamer struct {
	calls      int
	toolResult string
}

func (s *persistedSubagentStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_large",
				Name:  "LongTool",
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if results := lastSubagentToolResultTexts(req.Messages); len(results) > 0 {
		s.toolResult = results[len(results)-1]
	}
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type aggregateSubagentStreamer struct {
	calls       int
	large       string
	medium      string
	small       string
	toolResults []string
}

func (s *aggregateSubagentStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
				{Type: "tool_use", ID: "toolu_large", Name: "Echo", Input: json.RawMessage(`{"text":"` + s.large + `"}`)},
				{Type: "tool_use", ID: "toolu_medium", Name: "Echo", Input: json.RawMessage(`{"text":"` + s.medium + `"}`)},
				{Type: "tool_use", ID: "toolu_small", Name: "Echo", Input: json.RawMessage(`{"text":"` + s.small + `"}`)},
			}},
			StopReason: "tool_use",
		}, nil
	}
	s.toolResults = append(s.toolResults, lastSubagentToolResultTexts(req.Messages)...)
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type historySubagentStreamer struct {
	calls       int
	old         string
	fresh       string
	toolResults []string
}

func (s *historySubagentStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	switch s.calls {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_old",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"` + s.old + `"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_fresh",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"` + s.fresh + `"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		for _, msg := range req.Messages {
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					s.toolResults = append(s.toolResults, block.Content)
				}
			}
		}
		_ = cb.OnText("done")
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
		}, nil
	}
}

type usageSubagentStreamer struct{}

func (usageSubagentStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage: anthropic.Usage{
			InputTokens:                         10,
			CacheCreationInputTokens:            2,
			CacheReadInputTokens:                3,
			CacheCreationEphemeral1hInputTokens: 2,
			OutputTokens:                        7,
			ServiceTier:                         "standard",
		},
	}, nil
}

type failingSubagentStreamer struct{}

func (failingSubagentStreamer) StreamMessages(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	return nil, fmt.Errorf("GO_AGENT_CHILD_FAILED_MARKER: simulated child provider failure")
}

type partialThenCancelledStreamer struct {
	started chan struct{}
	once    sync.Once
}

func (s *partialThenCancelledStreamer) StreamMessages(ctx context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if err := cb.OnText("GO_AGENT_CHILD_PARTIAL_MARKER: partial result before cancellation"); err != nil {
		return nil, err
	}
	s.once.Do(func() {
		close(s.started)
	})
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRuntimeLoadsAgentScopedContextSources(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	mustWriteRuntimeTest(t, filepath.Join(project, ".claude", "agents", "reviewer.md"), `---
name: reviewer
tools: [Echo, mcp__local__lookup]
mcpServers:
  - inherited
  - local:
      type: http
      url: https://example.test/mcp
skills: [review-skill]
memory: all
---
Review deeply.`)
	mustWriteRuntimeTest(t, filepath.Join(project, ".claude", "skills", "review-skill", "SKILL.md"), "# Review Skill\nUse project-specific review heuristics.")
	mustWriteRuntimeTest(t, filepath.Join(configDir, "agent-memory", "reviewer", "MEMORY.md"), "user agent memory")
	mustWriteRuntimeTest(t, filepath.Join(project, ".claude", "agent-memory", "reviewer", "MEMORY.md"), "project agent memory")
	mustWriteRuntimeTest(t, filepath.Join(project, ".claude", "agent-memory-local", "reviewer", "MEMORY.md"), "local agent memory")
	baseRegistry := tools.NewRegistry(echoTool{})
	streamer := &scriptedStreamer{toolName: "mcp__local__lookup", expectedToolResult: "from mcp"}
	cleanupCalled := false
	runtime := Runtime{
		Client:   streamer,
		Registry: baseRegistry,
		Model:    "base-model",
		LoadMCPTools: func(_ context.Context, configs map[string]config.MCPServerConfig) ([]tools.Tool, func()) {
			if len(configs) != 1 || configs["local"].URL != "https://example.test/mcp" {
				t.Fatalf("configs = %+v", configs)
			}
			return []tools.Tool{mcpLookupTool{}}, func() { cleanupCalled = true }
		},
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", SubagentType: "reviewer", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "mcp__local__lookup" {
		t.Fatalf("result = %+v", result)
	}
	if !cleanupCalled {
		t.Fatal("mcp cleanup was not called")
	}
	if _, ok := baseRegistry.Get("mcp__local__lookup"); ok {
		t.Fatal("agent MCP tool polluted parent registry")
	}
	system := streamer.systems[0]
	for _, want := range []string{
		"# Preloaded Skills",
		"Use project-specific review heuristics.",
		"# Agent Memory",
		"user agent memory",
		"project agent memory",
		"local agent memory",
		"# Agent MCP Servers",
		"- inherited: referenced from inherited MCP configuration",
		"- local: mcp__local__lookup",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q:\n%s", want, system)
		}
	}
	if len(streamer.tools[0]) != 2 || streamer.tools[0][0].Name != "Echo" || streamer.tools[0][1].Name != "mcp__local__lookup" {
		t.Fatalf("tools = %+v", streamer.tools[0])
	}
}

func TestRuntimeUsesDefaultGeneralPurposeAgentPrompt(t *testing.T) {
	project := t.TempDir()
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect the repo", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.AgentName != "general-purpose" {
		t.Fatalf("agent = %q, want general-purpose", result.AgentName)
	}
	if len(streamer.systems) == 0 {
		t.Fatal("no system prompt captured")
	}
	system := streamer.systems[0]
	for _, want := range []string{
		"You are a focused niuma sub-agent",
		"Treat the delegated prompt as your full scope",
		"Return an evidence-first result for the parent agent to synthesize",
		"- Summary: the direct answer or current status.",
		"- Evidence: concrete file paths, function/type names, line numbers, commands, tests, or transcript/request-dump references when available.",
		"file list alone is not implementation evidence",
		"- Assumptions: assumptions you made while working.",
		"- Unknowns: anything you could not prove from the delegated context and tools.",
		"- Verification:",
		"- Risks:",
		"- Next action:",
		"Do not omit these headings.",
		"Put one concise bullet under each heading",
		`"None observed" or an explicit reason`,
		"extracts these headings into capability_loop context",
		"Do not fabricate evidence",
		"Agent instructions:",
		"When you complete the task, respond with a concise report",
		"start broad and narrow down",
		"After Grep identifies the exact file/function",
		"scoped Read with offset/limit",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
}

func TestRuntimeAddsEvidenceContractToSubagentRequest(t *testing.T) {
	project := t.TempDir()
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	if _, err := runtime.Run(context.Background(), Request{Prompt: "inspect the repo", CWD: project}, tools.Context{CWD: project}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(streamer.messages) == 0 {
		t.Fatal("no request messages captured")
	}
	for _, want := range []string{
		"inspect the repo",
		"Sub-agent evidence output contract",
		"Summary, Evidence, Assumptions, Unknowns, Verification, Risks, and Next action",
		"concrete files, functions, line numbers, commands, tests",
		"exact top-level headings",
		"Put one concise bullet under each heading",
		`"None observed" or explain why it remains unverified`,
		"Missing headings weaken parent planning, compact/resume recovery, and failure handling.",
		"failed, cancelled, blocked, or partial",
		"partial evidence plus remaining unknowns, risks, verification",
	} {
		if !requestMessagesContainSubstring(streamer.messages[0], want) {
			t.Fatalf("sub-agent request missing %q:\n%+v", want, streamer.messages[0])
		}
	}
}

func TestWithSubagentEvidenceContractMessageDoesNotDuplicate(t *testing.T) {
	messages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: "Sub-agent evidence output contract\nDo it."}},
	}}
	out := withSubagentEvidenceContractMessage(messages)
	if len(out) != 1 {
		t.Fatalf("messages = %+v", out)
	}
}

func TestRuntimePersistsCapabilityLoopFromStructuredSections(t *testing.T) {
	project := t.TempDir()
	content := strings.Join([]string{
		"Summary: checked the parent runtime path.",
		"",
		"Evidence:",
		"- internal/query/query.go:3909 carries agent task result hints.",
		"",
		"Assumptions:",
		"- Parent session has access to the task store.",
		"",
		"Unknowns:",
		"- No live transcript was captured in this unit test.",
		"",
		"Verification:",
		"- go test ./internal/query -count=1",
		"",
		"Risks:",
		"- Parser only extracts concise section bullets.",
		"",
		"Next action:",
		"- Parent should inspect AgentGet before finalizing.",
	}, "\n")
	store := &fakeTaskStore{}
	streamer := &finalTextStreamer{text: content}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(), Model: "base-model", TaskStore: store, MaxTurns: 1}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.CapabilityLoop == nil {
		t.Fatalf("result missing capability loop: %+v", result)
	}
	if got := result.CapabilityLoop.Evidence; len(got) != 1 || got[0] != "internal/query/query.go:3909 carries agent task result hints." {
		t.Fatalf("evidence = %#v", got)
	}
	if got := result.CapabilityLoop.Assumptions; len(got) != 1 || got[0] != "Parent session has access to the task store." {
		t.Fatalf("assumptions = %#v", got)
	}
	if got := result.CapabilityLoop.Unknowns; len(got) != 1 || got[0] != "No live transcript was captured in this unit test." {
		t.Fatalf("unknowns = %#v", got)
	}
	if got := result.CapabilityLoop.Verification; len(got) != 1 || got[0] != "go test ./internal/query -count=1" {
		t.Fatalf("verification = %#v", got)
	}
	if got := result.CapabilityLoop.Risks; len(got) != 1 || got[0] != "Parser only extracts concise section bullets." {
		t.Fatalf("risks = %#v", got)
	}
	if result.CapabilityLoop.NextAction != "Parent should inspect AgentGet before finalizing." {
		t.Fatalf("next action = %q", result.CapabilityLoop.NextAction)
	}

	var stored Result
	if err := json.Unmarshal([]byte(store.resultJSON), &stored); err != nil {
		t.Fatalf("stored result json = %s: %v", store.resultJSON, err)
	}
	if stored.CapabilityLoop == nil || len(stored.CapabilityLoop.Evidence) != 1 || stored.CapabilityLoop.NextAction == "" {
		t.Fatalf("stored capability loop = %+v", stored.CapabilityLoop)
	}
}

func TestRuntimePersistsCapabilityLoopFromInlineStructuredSections(t *testing.T) {
	project := t.TempDir()
	content := strings.Join([]string{
		"Summary: checked the parent runtime path.",
		"Evidence: internal/query/query.go:3909 carries agent task result hints.",
		"Assumptions: Parent session has access to the task store.",
		"Unknowns: No live transcript was captured in this unit test.",
		"Verification: go test ./internal/query -count=1",
		"Risks: Inline sections previously failed extraction.",
		"Next action: Parent should inspect AgentGet before finalizing.",
	}, "\n")
	store := &fakeTaskStore{}
	streamer := &finalTextStreamer{text: content}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(), Model: "base-model", TaskStore: store, MaxTurns: 1}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.CapabilityLoop == nil {
		t.Fatalf("result missing capability loop: %+v", result)
	}
	if got := result.CapabilityLoop.Evidence; len(got) != 1 || got[0] != "internal/query/query.go:3909 carries agent task result hints." {
		t.Fatalf("evidence = %#v", got)
	}
	if got := result.CapabilityLoop.Assumptions; len(got) != 1 || got[0] != "Parent session has access to the task store." {
		t.Fatalf("assumptions = %#v", got)
	}
	if got := result.CapabilityLoop.Unknowns; len(got) != 1 || got[0] != "No live transcript was captured in this unit test." {
		t.Fatalf("unknowns = %#v", got)
	}
	if got := result.CapabilityLoop.Verification; len(got) != 1 || got[0] != "go test ./internal/query -count=1" {
		t.Fatalf("verification = %#v", got)
	}
	if got := result.CapabilityLoop.Risks; len(got) != 1 || got[0] != "Inline sections previously failed extraction." {
		t.Fatalf("risks = %#v", got)
	}
	if result.CapabilityLoop.NextAction != "Parent should inspect AgentGet before finalizing." {
		t.Fatalf("next action = %q", result.CapabilityLoop.NextAction)
	}

	var stored Result
	if err := json.Unmarshal([]byte(store.resultJSON), &stored); err != nil {
		t.Fatalf("stored result json = %s: %v", store.resultJSON, err)
	}
	if stored.CapabilityLoop == nil || len(stored.CapabilityLoop.Evidence) != 1 || stored.CapabilityLoop.NextAction == "" {
		t.Fatalf("stored capability loop = %+v", stored.CapabilityLoop)
	}
}

func TestRuntimePersistsCapabilityLoopResolutionSections(t *testing.T) {
	project := t.TempDir()
	content := strings.Join([]string{
		"Summary: resolved a parent follow-up.",
		"Evidence: inspected the old sub-agent output.",
		"Verification: reran focused parent acceptance.",
		"Resolved follow-up: old Task next_action was handled.",
		"Supersedes evidence id: tool:toolu_old_task",
		"Supersedes evidence id: task:1",
	}, "\n")
	store := &fakeTaskStore{}
	streamer := &finalTextStreamer{text: content}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(), Model: "base-model", TaskStore: store, MaxTurns: 1}

	result, err := runtime.Run(context.Background(), Request{Prompt: "resolve", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.CapabilityLoop == nil {
		t.Fatalf("result missing capability loop: %+v", result)
	}
	if result.CapabilityLoop.ResolvedFollowUp != "old Task next_action was handled." {
		t.Fatalf("resolved follow-up = %q", result.CapabilityLoop.ResolvedFollowUp)
	}
	if result.CapabilityLoop.SupersedesEvidenceID != "tool:toolu_old_task" {
		t.Fatalf("supersedes evidence id = %q", result.CapabilityLoop.SupersedesEvidenceID)
	}
	if got := result.CapabilityLoop.SupersedesEvidenceIDs; len(got) != 1 || got[0] != "task:1" {
		t.Fatalf("supersedes evidence ids = %#v", got)
	}
	var stored Result
	if err := json.Unmarshal([]byte(store.resultJSON), &stored); err != nil {
		t.Fatalf("stored result json = %s: %v", store.resultJSON, err)
	}
	if stored.CapabilityLoop == nil ||
		stored.CapabilityLoop.ResolvedFollowUp != "old Task next_action was handled." ||
		stored.CapabilityLoop.SupersedesEvidenceID != "tool:toolu_old_task" ||
		len(stored.CapabilityLoop.SupersedesEvidenceIDs) != 1 ||
		stored.CapabilityLoop.SupersedesEvidenceIDs[0] != "task:1" {
		t.Fatalf("stored capability loop = %+v", stored.CapabilityLoop)
	}
}

func TestEnsureCapabilityLoopPreservesUnstructuredPartialEvidence(t *testing.T) {
	result := EnsureCapabilityLoop(Result{
		Content: "GO_AGENT_CHILD_PARTIAL_MARKER: partial result before cancellation\n\nCancelled: sub-agent task cancelled",
	}, agenttasks.StatusCancelled)
	if result.CapabilityLoop == nil {
		t.Fatal("missing capability loop")
	}
	if got := result.CapabilityLoop.Evidence; len(got) != 1 || !strings.Contains(got[0], "GO_AGENT_CHILD_PARTIAL_MARKER") {
		t.Fatalf("evidence = %+v", got)
	}
	if result.CapabilityLoop.NextAction == "" || !strings.Contains(strings.ToLower(result.CapabilityLoop.NextAction), "partial evidence") {
		t.Fatalf("next action = %q", result.CapabilityLoop.NextAction)
	}
}

func TestCapabilityLoopPlaceholdersAreNotActionable(t *testing.T) {
	placeholderOnly := &CapabilityLoop{
		Evidence:     []string{"None observed"},
		Assumptions:  []string{"None observed"},
		Unknowns:     []string{"None observed"},
		Verification: []string{"None observed"},
		Risks:        []string{"None observed"},
		NextAction:   "None observed",
	}
	if CapabilityLoopHasActionableEvidence(placeholderOnly) {
		t.Fatalf("placeholder loop should not be actionable: %+v", placeholderOnly)
	}
	if got := ResultWithCapabilityLoopDecisionContext(Result{Content: "plain", CapabilityLoop: placeholderOnly}); got != "plain" {
		t.Fatalf("placeholder loop should not append context: %q", got)
	}

	withEvidence := &CapabilityLoop{
		Evidence:     []string{"internal/agentruntime/runtime.go normalizes placeholders."},
		Assumptions:  []string{"None observed"},
		Unknowns:     []string{"None observed"},
		Verification: []string{"None observed"},
		Risks:        []string{"None observed"},
		NextAction:   "Parent should use the concrete evidence.",
	}
	if !CapabilityLoopHasActionableEvidence(withEvidence) {
		t.Fatalf("concrete evidence should be actionable: %+v", withEvidence)
	}
}

func TestCapabilityLoopDecisionContextIncludesArtifactHints(t *testing.T) {
	result := Result{
		TaskID:         42,
		Status:         agenttasks.StatusCompleted,
		SessionID:      "sub-session",
		TranscriptPath: "/tmp/subagent.jsonl",
		OutputFile:     "/tmp/subagent.output",
		WorktreePath:   "/tmp/subagent-worktree",
		WorktreeBranch: "agent/audit",
		CapabilityLoop: &CapabilityLoop{
			Evidence:   []string{"internal/agentruntime/runtime.go carries artifact hints."},
			NextAction: "Parent should verify sub-agent artifacts before finalizing.",
		},
	}
	context := CapabilityLoopDecisionContext(result)
	for _, want := range []string{
		`"task_id": 42`,
		`"status": "completed"`,
		`"session_id": "sub-session"`,
		`"transcript_path": "/tmp/subagent.jsonl"`,
		`"output_file": "/tmp/subagent.output"`,
		`"worktree_path": "/tmp/subagent-worktree"`,
		`"worktree_branch": "agent/audit"`,
		"internal/agentruntime/runtime.go carries artifact hints.",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("context missing %q:\n%s", want, context)
		}
	}
}

func TestRuntimeAcceptsExplicitGeneralPurposeAgentType(t *testing.T) {
	project := t.TempDir()
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect the repo", SubagentType: "general-purpose", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.AgentName != "general-purpose" || result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	if len(streamer.systems) == 0 || !strings.Contains(streamer.systems[0], "Agent instructions:") {
		t.Fatalf("default general-purpose prompt was not used: %+v", streamer.systems)
	}
}

func TestRuntimeAliasesDefaultSubagentTypeToGeneralPurpose(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	for _, alias := range []string{"default", "Default", "DEFAULT"} {
		project := t.TempDir()
		streamer := &scriptedStreamer{}
		runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

		result, err := runtime.Run(context.Background(), Request{Prompt: "inspect the repo", SubagentType: alias, CWD: project}, tools.Context{CWD: project})
		if err != nil {
			t.Fatalf("Run(subagent_type=%q) error = %v", alias, err)
		}
		if result.AgentName != "general-purpose" || result.Content != "done" {
			t.Fatalf("subagent_type=%q result = %+v", alias, result)
		}
	}
}

func TestRuntimeLocalDefaultAgentOverridesAlias(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	mustWriteRuntimeTest(t, filepath.Join(project, ".claude", "agents", "default.md"), `---
name: default
---
Use the project default reviewer instructions.`)
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", SubagentType: "default", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.AgentName != "default" {
		t.Fatalf("agent = %q, want default", result.AgentName)
	}
	if len(streamer.systems) == 0 || !strings.Contains(streamer.systems[0], "Use the project default reviewer instructions.") {
		t.Fatalf("local default agent prompt was not used: %+v", streamer.systems)
	}
}

func TestRuntimeUnknownSubagentTypeErrorListsAvailableAgents(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	_, err := runtime.Run(context.Background(), Request{Prompt: "do it", SubagentType: "no-such-agent", CWD: project}, tools.Context{CWD: project})
	if err == nil {
		t.Fatal("Run() error = nil, want unknown subagent_type error")
	}
	for _, want := range []string{
		"unknown subagent_type: no-such-agent",
		"available: ",
		"Explore",
		"Plan",
		"general-purpose",
		"omit subagent_type for the default general-purpose agent",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestAgentBackgroundUnknownSubagentTypeErrorListsAvailableAgents(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)

	_, err := AgentBackground(project, "no-such-agent")
	if err == nil {
		t.Fatal("AgentBackground() error = nil, want unknown subagent_type error")
	}
	for _, want := range []string{
		"unknown subagent_type: no-such-agent",
		"available: ",
		"omit subagent_type for the default general-purpose agent",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestRuntimeLoadsBuiltInExploreAgent(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runGitRuntimeTest(project, "init"); err != nil {
		t.Skipf("git unavailable for snapshot test: %v", err)
	}
	streamer := &scriptedStreamer{}
	registry := tools.NewRegistry(
		echoTool{},
		namedNoopTool{name: "Agent"},
		namedNoopTool{name: "Edit"},
		namedNoopTool{name: "Write"},
		namedNoopTool{name: "Read"},
		namedNoopTool{name: "Grep"},
	)
	runtime := Runtime{Client: streamer, Registry: registry, Model: "claude-sonnet-4-6", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "find prompt assembly", SubagentType: "Explore", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.AgentName != "Explore" || result.Model != "claude-sonnet-4-6" || result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	if len(streamer.systems) == 0 {
		t.Fatal("no system prompt captured")
	}
	system := streamer.systems[0]
	for _, want := range []string{
		"READ-ONLY exploration task",
		"STRICTLY PROHIBITED",
		"Use Grep for searching file contents",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("Explore system prompt missing %q:\n%s", want, system)
		}
	}
	if !strings.Contains(system, "# Environment") || !strings.Contains(system, "Current working directory: "+project) {
		t.Fatalf("Explore system prompt missing environment details:\n%s", system)
	}
	if strings.Contains(system, "# Git Snapshot") || strings.Contains(system, "Status:") {
		t.Fatalf("Explore should omit stale git snapshot:\n%s", system)
	}
	gotTools := toolDefinitionNames(streamer.tools[0])
	for _, denied := range []string{"Agent", "Edit", "Write"} {
		if strings.Contains(","+strings.Join(gotTools, ",")+",", ","+denied+",") {
			t.Fatalf("Explore exposed denied tool %s in %v", denied, gotTools)
		}
	}
	for _, allowed := range []string{"Echo", "Read", "Grep"} {
		if !strings.Contains(","+strings.Join(gotTools, ",")+",", ","+allowed+",") {
			t.Fatalf("Explore missing allowed tool %s in %v", allowed, gotTools)
		}
	}
}

func TestRuntimeLoadsBuiltInPlanAgent(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runGitRuntimeTest(project, "init"); err != nil {
		t.Skipf("git unavailable for snapshot test: %v", err)
	}
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}, namedNoopTool{name: "Edit"}, namedNoopTool{name: "Write"}), Model: "claude-sonnet-4-6", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "plan prompt parity", SubagentType: "Plan", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.AgentName != "Plan" || result.Model != "claude-sonnet-4-6" || result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	system := streamer.systems[0]
	for _, want := range []string{
		"READ-ONLY planning task",
		"Your Process",
		"### Critical Files for Implementation",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("Plan system prompt missing %q:\n%s", want, system)
		}
	}
	if !strings.Contains(system, "# Environment") || !strings.Contains(system, "Current working directory: "+project) {
		t.Fatalf("Plan system prompt missing environment details:\n%s", system)
	}
	if strings.Contains(system, "# Git Snapshot") || strings.Contains(system, "Status:") {
		t.Fatalf("Plan should omit stale git snapshot:\n%s", system)
	}
	gotTools := strings.Join(toolDefinitionNames(streamer.tools[0]), ",")
	if strings.Contains(gotTools, "Edit") || strings.Contains(gotTools, "Write") {
		t.Fatalf("Plan exposed write tools: %s", gotTools)
	}
}

func TestRuntimeAddsSubagentEnvironmentDetails(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runGitRuntimeTest(project, "init"); err != nil {
		t.Skipf("git unavailable for snapshot test: %v", err)
	}
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect the repo", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	if len(streamer.systems) == 0 {
		t.Fatal("no system prompt captured")
	}
	system := streamer.systems[0]
	for _, want := range []string{
		"Notes:",
		"use absolute file paths",
		"# Environment",
		"Current working directory: " + project,
		"Date: " + time.Now().Format("2006-01-02"),
		"Model: base-model",
		"# Git Snapshot",
		"Status:",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
}

func TestRuntimeAddsSubagentTurnBudgetReminderRequestOnly(t *testing.T) {
	project := t.TempDir()
	streamer := &subagentRuntimeReminderStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 3}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect many files", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	if len(streamer.messages) != 3 {
		t.Fatalf("requests = %d, want 3", len(streamer.messages))
	}
	secondText := subagentRuntimeReminderText(streamer.messages[1])
	if countSubagentRuntimeReminderMessages(streamer.messages[1]) != 1 {
		t.Fatalf("second request reminders = %d, messages=%+v", countSubagentRuntimeReminderMessages(streamer.messages[1]), streamer.messages[1])
	}
	if !strings.Contains(secondText, "## Sub-agent tool planning reminder") || strings.Contains(secondText, "## Sub-agent turn budget reminder") {
		t.Fatalf("second request reminder text:\n%s", secondText)
	}
	thirdText := subagentRuntimeReminderText(streamer.messages[2])
	if countSubagentRuntimeReminderMessages(streamer.messages[2]) != 1 {
		t.Fatalf("third request should contain one request-only reminder, messages=%+v", streamer.messages[2])
	}
	for _, want := range []string{
		"## Sub-agent turn budget reminder",
		"model turn 3 of 3",
		"last turn allowed",
		"Tools are disabled on this final sub-agent request",
		"produce the concise final sub-agent answer now",
		"delegated evidence-first output contract",
		"## Sub-agent tool planning reminder",
		"already gathered 11 tool results from 11 tool calls",
	} {
		if !strings.Contains(thirdText, want) {
			t.Fatalf("third reminder missing %q:\n%s", want, thirdText)
		}
	}
	if len(streamer.tools) != 3 || len(streamer.tools[2]) != 0 {
		t.Fatalf("final request tools = %+v, want disabled tools", streamer.tools)
	}
}

func TestRuntimeAddsSubagentToolPlanningReminderAfterFirstToolResult(t *testing.T) {
	project := t.TempDir()
	streamer := &lowToolContextFinalTurnStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 3}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect one thing", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	if len(streamer.messages) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.messages))
	}
	secondText := subagentRuntimeReminderText(streamer.messages[1])
	for _, want := range []string{
		"## Sub-agent tool planning reminder",
		"already gathered 1 tool results from 1 tool calls",
		"narrow read-only diagnostics",
		`output_mode:"content"`,
		"RE2 regular expressions",
		"multiple simple Grep calls",
		"complex unbalanced groups",
		"offset and limit",
		"smallest useful line range",
	} {
		if !strings.Contains(secondText, want) {
			t.Fatalf("second reminder missing %q:\n%s", want, secondText)
		}
	}
	if strings.Contains(secondText, "## Sub-agent turn budget reminder") {
		t.Fatalf("non-final request included turn budget reminder:\n%s", secondText)
	}
}

func TestRuntimeDisablesSubagentToolsOnFinalTurnWithLowToolContext(t *testing.T) {
	project := t.TempDir()
	streamer := &lowToolContextFinalTurnStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "inspect one thing", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	if len(streamer.tools) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.tools))
	}
	if len(streamer.tools[0]) == 0 {
		t.Fatalf("first request tools were disabled unexpectedly")
	}
	if len(streamer.tools[1]) != 0 {
		t.Fatalf("final request tools = %+v, want disabled tools", streamer.tools[1])
	}
	finalText := subagentRuntimeReminderText(streamer.messages[1])
	for _, want := range []string{
		"## Sub-agent turn budget reminder",
		"model turn 2 of 2",
		"Tools are disabled on this final sub-agent request",
		"delegated evidence-first output contract",
	} {
		if !strings.Contains(finalText, want) {
			t.Fatalf("final reminder missing %q:\n%s", want, finalText)
		}
	}
}

func TestRuntimeDumpsSubagentPromptRequests(t *testing.T) {
	project := t.TempDir()
	dumpPath := filepath.Join(t.TempDir(), "agent-prompts.jsonl")
	t.Setenv(promptdump.PathEnv, dumpPath)
	t.Setenv(promptdump.FullEnv, "")
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "subagent secret marker", CWD: project, ParentSessionID: 123}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, leaked := range []string{"subagent secret marker", `"content":"from subagent"`} {
		if strings.Contains(text, leaked) {
			t.Fatalf("summary dump leaked raw content %q:\n%s", leaked, text)
		}
	}
	records := parseAgentPromptDumpRecords(t, raw)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2:\n%s", len(records), text)
	}
	first := records[0]
	if first.Scope != "subagent" || first.QuerySource != "agentruntime" || first.PromptMode != "subagent" || first.AgentName != "general-purpose" {
		t.Fatalf("first record metadata = %+v", first)
	}
	if first.ParentSessionID != 123 || first.TenantSessionID != 123 || first.SessionID == "" {
		t.Fatalf("session metadata = %+v", first)
	}
	if first.Request != nil || first.RequestRedaction.RawRequestIncluded || first.RequestRedaction.Mode != "summary" || !first.RequestRedaction.TextOmitted {
		t.Fatalf("redaction = %+v request=%+v", first.RequestRedaction, first.Request)
	}
	second := records[1]
	if second.ToolResultStats.Count != 1 || second.ToolResultStats.TotalBytes != len("from subagent") {
		t.Fatalf("tool result stats = %+v", second.ToolResultStats)
	}
	if !promptDumpSummaryContainsToolResult(second.MessagesSummary, "Echo") {
		t.Fatalf("messages summary missing Echo tool_result: %+v", second.MessagesSummary)
	}
}

func TestRuntimeFullDumpShowsBuiltInExploreOmitsGitSnapshot(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runGitRuntimeTest(project, "init"); err != nil {
		t.Skipf("git unavailable for snapshot test: %v", err)
	}
	dumpPath := filepath.Join(t.TempDir(), "explore-prompts.jsonl")
	t.Setenv(promptdump.PathEnv, dumpPath)
	t.Setenv(promptdump.FullEnv, "true")
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	result, err := runtime.Run(context.Background(), Request{Prompt: "find prompt assembly", SubagentType: "Explore", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	records := parseAgentPromptDumpRecords(t, raw)
	if len(records) == 0 || records[0].Request == nil {
		t.Fatalf("full dump missing request:\n%s", raw)
	}
	system := records[0].Request.System
	for _, want := range []string{"READ-ONLY exploration task", "# Environment", "Current working directory: " + project} {
		if !strings.Contains(system, want) {
			t.Fatalf("Explore full dump system missing %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "# Git Snapshot") || strings.Contains(system, "Status:") {
		t.Fatalf("Explore full dump should omit stale git snapshot:\n%s", system)
	}
}

func promptDumpSummaryContainsToolResult(messages []promptdump.MessageSummary, toolName string) bool {
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Type == "tool_result" && block.ToolName == toolName {
				return true
			}
		}
	}
	return false
}

func TestRuntimePromptDumpWriteErrorStopsBeforeModelRequest(t *testing.T) {
	t.Setenv(promptdump.PathEnv, t.TempDir())
	streamer := &scriptedStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "base-model", MaxTurns: 2}

	_, err := runtime.Run(context.Background(), Request{Prompt: "inspect the repo", CWD: t.TempDir()}, tools.Context{CWD: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "agent prompt dump") {
		t.Fatalf("err = %v, want agent prompt dump error", err)
	}
	if streamer.calls != 0 {
		t.Fatalf("model was called despite dump failure: calls=%d", streamer.calls)
	}
}

type subagentRuntimeReminderStreamer struct {
	messages [][]anthropic.MessageParam
	tools    [][]anthropic.ToolDefinition
}

func (s *subagentRuntimeReminderStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.messages = append(s.messages, req.Messages)
	s.tools = append(s.tools, req.Tools)
	switch len(s.messages) {
	case 1:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: manyEchoToolUses(10)},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_extra",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"extra"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
		}, nil
	}
}

type lowToolContextFinalTurnStreamer struct {
	messages [][]anthropic.MessageParam
	tools    [][]anthropic.ToolDefinition
}

func (s *lowToolContextFinalTurnStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.messages = append(s.messages, req.Messages)
	s.tools = append(s.tools, req.Tools)
	switch len(s.messages) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_one",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"result"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
		}, nil
	}
}

func manyEchoToolUses(count int) []anthropic.ContentBlock {
	blocks := make([]anthropic.ContentBlock, 0, count)
	for i := 0; i < count; i++ {
		blocks = append(blocks, anthropic.ContentBlock{
			Type:  "tool_use",
			ID:    fmt.Sprintf("toolu_%02d", i),
			Name:  "Echo",
			Input: json.RawMessage(fmt.Sprintf(`{"text":"result-%02d"}`, i)),
		})
	}
	return blocks
}

func countSubagentRuntimeReminderMessages(messages []anthropic.MessageParam) int {
	count := 0
	for _, message := range messages {
		if strings.Contains(testMessageText(message), "Current sub-agent runtime status") {
			count++
		}
	}
	return count
}

func subagentRuntimeReminderText(messages []anthropic.MessageParam) string {
	for _, message := range messages {
		text := testMessageText(message)
		if strings.Contains(text, "Current sub-agent runtime status") {
			return text
		}
	}
	return ""
}

func lastSubagentToolResultTexts(messages []anthropic.MessageParam) []string {
	for i := len(messages) - 1; i >= 0; i-- {
		var out []string
		for _, block := range messages[i].Content {
			if block.Type == "tool_result" {
				out = append(out, block.Content)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func testMessageText(message anthropic.MessageParam) string {
	var parts []string
	for _, block := range message.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestRuntimePersistsLargeToolResultPreview(t *testing.T) {
	project := t.TempDir()
	content := strings.Repeat("subagent-output-", 500)
	streamer := &persistedSubagentStreamer{}
	runtime := Runtime{
		Client:          streamer,
		Registry:        tools.NewRegistry(largeTool{content: content}),
		Model:           "test",
		ToolResultLimit: 100,
		RecorderStore:   session.Store{Root: t.TempDir()},
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "run long tool", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(streamer.toolResult, "<persisted-output>") || !strings.Contains(streamer.toolResult, "Full output saved to:") {
		t.Fatalf("tool result = %q", streamer.toolResult)
	}
	if strings.Contains(streamer.toolResult, content) {
		t.Fatalf("tool result contains full content")
	}
	path := filepath.Join(filepath.Dir(result.TranscriptPath), result.SessionID, "tool-results", "toolu_large.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("persisted content mismatch")
	}
}

func TestRuntimeUsesToolSpecificResultLimit(t *testing.T) {
	project := t.TempDir()
	content := strings.Repeat("subagent-specific-output-", 200)
	streamer := &persistedSubagentStreamer{}
	runtime := Runtime{
		Client:          streamer,
		Registry:        tools.NewRegistry(largeTool{content: content, maxResultSize: 10}),
		Model:           "test",
		ToolResultLimit: 1_000,
		RecorderStore:   session.Store{Root: t.TempDir()},
	}
	if _, err := runtime.Run(context.Background(), Request{Prompt: "run long tool", CWD: project}, tools.Context{CWD: project}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(streamer.toolResult, "<persisted-output>") {
		t.Fatalf("tool-specific limit was not applied: %.120q", streamer.toolResult)
	}
	if strings.Contains(streamer.toolResult, content) {
		t.Fatalf("tool result contains full content")
	}
}

func TestRuntimeAppliesToolResultMessageBudgetBeforeRequest(t *testing.T) {
	project := t.TempDir()
	large := strings.Repeat("L", 9000)
	medium := strings.Repeat("M", 6000)
	small := strings.Repeat("S", 2000)
	streamer := &aggregateSubagentStreamer{large: large, medium: medium, small: small}
	runtime := Runtime{
		Client:                  streamer,
		Registry:                tools.NewRegistry(echoTool{}),
		Model:                   "test",
		ToolResultLimit:         10_000,
		ToolResultMessageBudget: 12_000,
		RecorderStore:           session.Store{Root: t.TempDir()},
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "run tools", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.toolResults) != 3 {
		t.Fatalf("tool results = %d", len(streamer.toolResults))
	}
	if !strings.Contains(streamer.toolResults[0], "<persisted-output>") {
		t.Fatalf("largest result was not persisted: %.120q", streamer.toolResults[0])
	}
	if streamer.toolResults[1] != medium || streamer.toolResults[2] != small {
		t.Fatalf("unexpected replacements")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(result.TranscriptPath), result.SessionID, "tool-results", "toolu_large.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != large {
		t.Fatalf("persisted content mismatch")
	}
}

func TestRuntimeFreezesLiveToolResultAfterFirstBudgetPass(t *testing.T) {
	project := t.TempDir()
	old := strings.Repeat("O", 9000)
	fresh := strings.Repeat("F", 7000)
	streamer := &historySubagentStreamer{old: old, fresh: fresh}
	runtime := Runtime{
		Client:                  streamer,
		Registry:                tools.NewRegistry(echoTool{}),
		Model:                   "test",
		ToolResultLimit:         10_000,
		ToolResultMessageBudget: 20_000,
		ToolResultHistoryBudget: 12_000,
		RecorderStore:           session.Store{Root: t.TempDir()},
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "run tools", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.toolResults) != 2 {
		t.Fatalf("tool results = %d", len(streamer.toolResults))
	}
	if streamer.toolResults[0] != old {
		t.Fatalf("old result should stay frozen after first budget pass: %.120q", streamer.toolResults[0])
	}
	if streamer.toolResults[1] != fresh {
		t.Fatalf("fresh result changed")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(result.TranscriptPath), result.SessionID, "tool-results", "toolu_old.txt")); !os.IsNotExist(err) {
		t.Fatalf("live seen old result should not be newly persisted, stat err=%v", err)
	}
}

func TestRuntimeRunsIsolatedToolLoopAndTranscript(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	agentDir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}
	agent := "---\nname: reviewer\nmodel: sub-model\ntools: [Echo]\n---\nReview deeply."
	if err := os.WriteFile(filepath.Join(agentDir, "reviewer.md"), []byte(agent), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &scriptedStreamer{}
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client: streamer,
		Registry: tools.NewRegistry(
			echoTool{},
			blockedTool{},
		),
		Model:     "base-model",
		TaskStore: store,
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", Description: "review task", SubagentType: "reviewer", CWD: project, TenantID: 1, UserID: 2, ParentSessionID: 3, TraceID: "trace-1"}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" || result.Model != "sub-model" || result.SessionID == "" || result.TranscriptPath == "" || result.OutputFile == "" || result.TaskID != 101 {
		t.Fatalf("result = %+v", result)
	}
	output, err := os.ReadFile(result.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "done" {
		t.Fatalf("output file = %q", string(output))
	}
	stateData, err := os.ReadFile(result.OutputFile + ".state.json")
	if err != nil {
		t.Fatal(err)
	}
	var outputState struct {
		Status string `json:"status"`
		Result Result `json:"result"`
	}
	if err := json.Unmarshal(stateData, &outputState); err != nil {
		t.Fatal(err)
	}
	if outputState.Status != agenttasks.StatusCompleted || outputState.Result.Status != agenttasks.StatusCompleted || outputState.Result.Content != "done" {
		t.Fatalf("output state = %+v", outputState)
	}
	if len(store.tasks) != 1 || store.tasks[0].Description != "review task" || store.tasks[0].TenantID != 1 || store.finishedStatus != agenttasks.StatusCompleted {
		t.Fatalf("store = %+v status=%s", store.tasks, store.finishedStatus)
	}
	if got := store.eventTypesWithoutCacheState(); strings.Join(got, ",") != "started,turn_start,tool_call,tool_result,turn_start,text_delta,completed" {
		t.Fatalf("events = %+v", got)
	}
	startedPayload := store.eventPayload(t, 0)
	if startedPayload["description"] != "review task" || startedPayload["prompt_preview"] != "do it" || startedPayload["max_turns"].(float64) != 100 || int(startedPayload["max_tokens"].(float64)) != defaults.CodeMaxTokens {
		t.Fatalf("started payload = %+v", startedPayload)
	}
	completedPayload := store.eventPayload(t, len(store.events)-1)
	if _, ok := completedPayload["duration_ms"].(float64); !ok || completedPayload["session_id"] == "" {
		t.Fatalf("completed payload = %+v", completedPayload)
	}
	if completedPayload["output_file"] != result.OutputFile || completedPayload["transcript_path"] != result.TranscriptPath {
		t.Fatalf("completed payload missing trace paths: %+v result=%+v", completedPayload, result)
	}
	if _, ok := completedPayload["capability_loop"].(map[string]any); !ok {
		t.Fatalf("completed payload missing capability loop: %+v", completedPayload)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "Echo" {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	if len(streamer.tools[0]) != 1 || streamer.tools[0][0].Name != "Echo" {
		t.Fatalf("tools were not filtered by agent frontmatter: %+v", streamer.tools[0])
	}
	if !strings.Contains(streamer.systems[0], "Review deeply") || streamer.models[0] != "sub-model" {
		t.Fatalf("systems=%+v models=%+v", streamer.systems, streamer.models)
	}
	if streamer.maxTokens[0] != defaults.CodeMaxTokens {
		t.Fatalf("subagent max_tokens = %d, want %d", streamer.maxTokens[0], defaults.CodeMaxTokens)
	}
	if len(streamer.messages[0]) != 2 || streamer.messages[0][0].Content[0].Text != "do it" ||
		!requestMessagesContainSubstring([]anthropic.MessageParam{streamer.messages[0][1]}, "Sub-agent evidence output contract") {
		t.Fatalf("subagent received shared context: %+v", streamer.messages[0])
	}
}

func TestRuntimePersistsModelErrorInFailedTaskResult(t *testing.T) {
	project := t.TempDir()
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    failingSubagentStreamer{},
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
		MaxTurns:  1,
	}

	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", Description: "failing task", CWD: project}, tools.Context{CWD: project})
	if err == nil || !strings.Contains(err.Error(), "GO_AGENT_CHILD_FAILED_MARKER") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Content, "GO_AGENT_CHILD_FAILED_MARKER") {
		t.Fatalf("result content = %q", result.Content)
	}
	if store.finished() != agenttasks.StatusFailed {
		t.Fatalf("status = %s", store.finished())
	}
	var stored Result
	if err := json.Unmarshal([]byte(store.resultJSON), &stored); err != nil {
		t.Fatalf("stored result json = %q: %v", store.resultJSON, err)
	}
	if stored.Status != agenttasks.StatusFailed || !strings.Contains(stored.Content, "GO_AGENT_CHILD_FAILED_MARKER") {
		t.Fatalf("stored result = %+v", stored)
	}
	output, err := os.ReadFile(result.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "GO_AGENT_CHILD_FAILED_MARKER") {
		t.Fatalf("output file = %q", string(output))
	}
}

func TestRuntimePersistsPartialContentInCancelledTaskResult(t *testing.T) {
	project := t.TempDir()
	store := &fakeTaskStore{}
	streamer := &partialThenCancelledStreamer{started: make(chan struct{})}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
		MaxTurns:  1,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := runtime.Run(ctx, Request{Prompt: "do it", Description: "cancelled task", CWD: project}, tools.Context{CWD: project})
		done <- struct {
			result Result
			err    error
		}{result: result, err: err}
	}()

	select {
	case <-streamer.started:
	case <-time.After(time.Second):
		t.Fatal("streamer did not emit partial content")
	}
	cancel()

	var got struct {
		result Result
		err    error
	}
	select {
	case got = <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not finish after cancellation")
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "cancel") {
		t.Fatalf("Run() error = %v", got.err)
	}
	if !strings.Contains(got.result.Content, "GO_AGENT_CHILD_PARTIAL_MARKER") {
		t.Fatalf("result content = %q", got.result.Content)
	}
	if store.finished() != agenttasks.StatusCancelled {
		t.Fatalf("status = %s", store.finished())
	}
	var stored Result
	if err := json.Unmarshal([]byte(store.resultJSON), &stored); err != nil {
		t.Fatalf("stored result json = %q: %v", store.resultJSON, err)
	}
	if stored.Status != agenttasks.StatusCancelled || !strings.Contains(stored.Content, "GO_AGENT_CHILD_PARTIAL_MARKER") {
		t.Fatalf("stored result = %+v", stored)
	}
	output, err := os.ReadFile(got.result.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "GO_AGENT_CHILD_PARTIAL_MARKER") {
		t.Fatalf("output file = %q", string(output))
	}
}

func TestRuntimePersistsPartialContentInTimeoutTaskResult(t *testing.T) {
	project := t.TempDir()
	store := &fakeTaskStore{}
	streamer := &partialThenCancelledStreamer{started: make(chan struct{})}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
		MaxTurns:  1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	got := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := runtime.Run(ctx, Request{Prompt: "do it", Description: "timeout task", CWD: project}, tools.Context{CWD: project})
		got <- struct {
			result Result
			err    error
		}{result: result, err: err}
	}()

	select {
	case <-streamer.started:
	case <-time.After(time.Second):
		t.Fatal("streamer did not emit partial content")
	}

	var out struct {
		result Result
		err    error
	}
	select {
	case out = <-got:
	case <-time.After(time.Second):
		t.Fatal("runtime did not finish after deadline")
	}
	if out.err == nil || !errors.Is(out.err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v", out.err)
	}
	if out.result.Status != agenttasks.StatusTimeout || store.finished() != agenttasks.StatusTimeout {
		t.Fatalf("result status=%s store status=%s", out.result.Status, store.finished())
	}
	if !strings.Contains(out.result.Content, "GO_AGENT_CHILD_PARTIAL_MARKER") || !strings.Contains(out.result.Content, "Timeout:") {
		t.Fatalf("result content = %q", out.result.Content)
	}
	var stored Result
	if err := json.Unmarshal([]byte(store.resultJSON), &stored); err != nil {
		t.Fatalf("stored result json = %q: %v", store.resultJSON, err)
	}
	if stored.Status != agenttasks.StatusTimeout || !strings.Contains(stored.Content, "GO_AGENT_CHILD_PARTIAL_MARKER") {
		t.Fatalf("stored result = %+v", stored)
	}
	stateData, err := os.ReadFile(out.result.OutputFile + ".state.json")
	if err != nil {
		t.Fatal(err)
	}
	var outputState struct {
		Status string `json:"status"`
		Result Result `json:"result"`
	}
	if err := json.Unmarshal(stateData, &outputState); err != nil {
		t.Fatal(err)
	}
	if outputState.Status != agenttasks.StatusTimeout || outputState.Result.Status != agenttasks.StatusTimeout {
		t.Fatalf("output state = %+v", outputState)
	}
	if gotEvents := store.eventTypesWithoutCacheState(); strings.Join(gotEvents, ",") != "started,turn_start,text_delta,timeout" {
		t.Fatalf("events = %+v", gotEvents)
	}
}

func TestRuntimeRunBackgroundReturnsTaskHandle(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	mustWriteRuntimeTest(t, filepath.Join(project, ".claude", "agents", "reviewer.md"), `---
name: reviewer
background: true
permissionMode: ask
---
Review deeply.`)
	streamer := &slowSingleTurnStreamer{done: make(chan struct{})}
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
	}
	result, err := runtime.RunBackground(context.Background(), Request{Prompt: "do it", SubagentType: "reviewer", CWD: project, TenantID: 1, UserID: 2}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Background || result.Status != agenttasks.StatusRunning || result.TaskID != 101 || result.AgentName != "reviewer" || result.SessionID == "" || result.OutputFile == "" || result.PermissionMode != "ask" {
		t.Fatalf("background result = %+v", result)
	}
	select {
	case streamer.done <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("streamer did not start")
	}
	deadline := time.After(time.Second)
	for store.finished() == "" {
		select {
		case <-deadline:
			t.Fatalf("background task did not finish; events=%+v", store.eventTypes())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if store.finished() != agenttasks.StatusCompleted {
		t.Fatalf("status = %s", store.finished())
	}
	tasks := store.createdTasks()
	if len(tasks) != 1 || tasks[0].MetadataJSON == "" {
		t.Fatalf("tasks = %+v", tasks)
	}
	if got := store.eventTypesWithoutCacheState(); strings.Join(got, ",") != "started,turn_start,text_delta,completed" {
		t.Fatalf("events = %+v", got)
	}
}

func TestRuntimeAssignsLocalProgressTaskIDWithoutStore(t *testing.T) {
	project := t.TempDir()
	var progress []agenttasks.EventInput
	runtime := Runtime{
		Client:   &singleTurnStreamer{},
		Registry: tools.NewRegistry(),
		Model:    "base-model",
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", Description: "local task", CWD: project}, tools.Context{
		CWD: project,
		TaskProgress: func(event agenttasks.EventInput) {
			progress = append(progress, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != 0 {
		t.Fatalf("local-only progress id should not be exposed as persisted task id: %+v", result)
	}
	if len(progress) == 0 {
		t.Fatal("expected progress events")
	}
	localID := progress[0].TaskID
	if localID == 0 {
		t.Fatalf("expected local progress task id, events=%+v", progress)
	}
	for _, event := range progress {
		if event.TaskID != localID {
			t.Fatalf("progress events should share local id %d: %+v", localID, progress)
		}
	}
}

func TestRuntimeInjectsPendingAgentMessages(t *testing.T) {
	project := t.TempDir()
	streamer := &agentMessageStreamer{}
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
		MaxTurns:  2,
	}
	delivered := false
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", CWD: project}, tools.Context{
		CWD: project,
		AgentMessages: func(taskID uint64) []agenttasks.MessageInput {
			if taskID == 0 || delivered {
				return nil
			}
			delivered = true
			return []agenttasks.MessageInput{{TaskID: taskID, FromAgent: "coordinator", Content: "please continue"}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" || !streamer.sawMessage {
		t.Fatalf("result=%+v sawMessage=%v", result, streamer.sawMessage)
	}
	if got := store.eventTypes(); !containsString(got, agenttasks.EventMessage) {
		t.Fatalf("events = %+v", got)
	}
}

func TestRuntimeStartsFromInitialMessagesThenPrompt(t *testing.T) {
	project := t.TempDir()
	streamer := &captureSingleTurnStreamer{}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: &fakeTaskStore{},
		MaxTurns:  1,
	}
	initial := []anthropic.MessageParam{
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "original brief"}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "previous finding"}}},
	}
	result, err := runtime.Run(context.Background(), Request{
		Prompt:          "resume with this new instruction",
		InitialMessages: initial,
		CWD:             project,
	}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	if len(streamer.messages) != 4 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	checkMessageText := func(index int, role, text string) {
		t.Helper()
		if streamer.messages[index].Role != role {
			t.Fatalf("message %d role = %q", index, streamer.messages[index].Role)
		}
		if len(streamer.messages[index].Content) != 1 || streamer.messages[index].Content[0].Text != text {
			t.Fatalf("message %d content = %+v", index, streamer.messages[index].Content)
		}
	}
	checkMessageText(0, "user", "original brief")
	checkMessageText(1, "assistant", "previous finding")
	checkMessageText(2, "user", "resume with this new instruction")
	if streamer.messages[3].Role != "user" || !requestMessagesContainSubstring([]anthropic.MessageParam{streamer.messages[3]}, "Sub-agent evidence output contract") {
		t.Fatalf("message 3 evidence contract = %+v", streamer.messages[3])
	}
}

func TestRuntimeDoesNotExposeSendMessageToSubagent(t *testing.T) {
	project := t.TempDir()
	streamer := &captureSingleTurnStreamer{}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}, namedNoopTool{name: "SendMessage"}),
		Model:     "base-model",
		TaskStore: &fakeTaskStore{},
		MaxTurns:  1,
	}
	if _, err := runtime.Run(context.Background(), Request{Prompt: "do it", CWD: project}, tools.Context{CWD: project}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, def := range streamer.tools {
		names = append(names, def.Name)
	}
	if containsString(names, "SendMessage") {
		t.Fatalf("subagent tools unexpectedly exposed SendMessage: %v", names)
	}
	if !containsString(names, "Echo") {
		t.Fatalf("subagent tools = %v, missing Echo", names)
	}
}

func TestRuntimeRunsToolsWithRequestCWD(t *testing.T) {
	parent := t.TempDir()
	child := t.TempDir()
	streamer := &scriptedStreamer{toolName: "Cwd", expectedToolResult: child}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(cwdTool{}),
		Model:     "base-model",
		TaskStore: &fakeTaskStore{},
		MaxTurns:  2,
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "report cwd", CWD: child}, tools.Context{CWD: parent})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimeReappliesInitialToolResultReplacement(t *testing.T) {
	project := t.TempDir()
	streamer := &captureSingleTurnStreamer{}
	runtime := Runtime{
		Client:                  streamer,
		Registry:                tools.NewRegistry(echoTool{}),
		Model:                   "base-model",
		TaskStore:               &fakeTaskStore{},
		MaxTurns:                1,
		ToolResultMessageBudget: 20_000,
	}
	original := strings.Repeat("R", 9000)
	replacement := "<persisted-output>\nresume preview\n</persisted-output>"
	result, err := runtime.Run(context.Background(), Request{
		Prompt: "resume with this new instruction",
		InitialMessages: []anthropic.MessageParam{
			{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_resume", Name: "Echo", Input: json.RawMessage(`{"text":"large"}`)}}},
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_resume", Content: original}}},
		},
		InitialToolResultReplacements: []toolresult.ReplacementRecord{{
			Kind:        "tool-result",
			ToolUseID:   "toolu_resume",
			Replacement: replacement,
		}},
		CWD: project,
	}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	if got := requestToolResultText(streamer.messages, "toolu_resume"); got != replacement {
		t.Fatalf("resumed tool result = %.120q, want replacement", got)
	}
	if got := requestToolResultText(streamer.messages, "toolu_resume"); strings.Contains(got, original) {
		t.Fatalf("resumed request still contains raw original")
	}
}

func TestRuntimeFreezesInitialToolResultWithoutReplacement(t *testing.T) {
	project := t.TempDir()
	streamer := &captureSingleTurnStreamer{}
	runtime := Runtime{
		Client:                  streamer,
		Registry:                tools.NewRegistry(echoTool{}),
		Model:                   "base-model",
		TaskStore:               &fakeTaskStore{},
		MaxTurns:                1,
		ToolResultMessageBudget: 100,
		ToolResultHistoryBudget: 100,
	}
	original := strings.Repeat("F", 9000)
	result, err := runtime.Run(context.Background(), Request{
		Prompt: "resume with this new instruction",
		InitialMessages: []anthropic.MessageParam{
			{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_frozen", Name: "Echo", Input: json.RawMessage(`{"text":"large"}`)}}},
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_frozen", Content: original}}},
		},
		CWD: project,
	}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	if got := requestToolResultText(streamer.messages, "toolu_frozen"); got != original {
		t.Fatalf("frozen tool result = %.120q, want original", got)
	}
	if got := requestToolResultText(streamer.messages, "toolu_frozen"); strings.Contains(got, "<persisted-output>") {
		t.Fatalf("frozen tool result was replaced: %.120q", got)
	}
}

func TestRuntimeAppliesAgentRuntimeFields(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	agentDir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}
	agent := `---
name: reviewer
model: inherit
maxTurns: 1
effort: high
criticalSystemReminder_EXPERIMENTAL: Stay focused.
---
Review deeply.`
	if err := os.WriteFile(filepath.Join(agentDir, "reviewer.md"), []byte(agent), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &scriptedStreamer{}
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client: streamer,
		Registry: tools.NewRegistry(
			echoTool{},
		),
		Model:     "base-model",
		TaskStore: store,
	}
	_, err := runtime.Run(context.Background(), Request{Prompt: "do it", Description: "review task", SubagentType: "reviewer", CWD: project, TenantID: 1, UserID: 2}, tools.Context{CWD: project})
	if err == nil || !strings.Contains(err.Error(), "max turns reached (1)") {
		t.Fatalf("err = %v", err)
	}
	if streamer.models[0] != "base-model" {
		t.Fatalf("model = %+v", streamer.models)
	}
	if streamer.thinking[0] == nil || streamer.thinking[0].Effort != "high" || streamer.thinking[0].BudgetTokens != 4096 {
		t.Fatalf("thinking = %+v", streamer.thinking)
	}
	if !strings.Contains(streamer.systems[0], "Critical reminder:\nStay focused.") {
		t.Fatalf("system = %q", streamer.systems[0])
	}
	startedPayload := store.eventPayload(t, 0)
	if startedPayload["max_turns"].(float64) != 1 || int(startedPayload["max_tokens"].(float64)) != defaults.CodeMaxTokens || startedPayload["effort"] != "high" {
		t.Fatalf("started payload = %+v", startedPayload)
	}
	var result Result
	if err := json.Unmarshal([]byte(store.resultJSON), &result); err != nil {
		t.Fatalf("result json = %s: %v", store.resultJSON, err)
	}
	if result.Model != "base-model" || result.Effort != "high" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimeUsesDefaultEffortWhenUnset(t *testing.T) {
	streamer := &scriptedStreamer{}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		MaxTurns:  1,
		TaskStore: &fakeTaskStore{},
	}
	_, err := runtime.Run(context.Background(), Request{Prompt: "do it", CWD: t.TempDir()}, tools.Context{})
	if err == nil || !strings.Contains(err.Error(), "max turns reached (1)") {
		t.Fatalf("err = %v", err)
	}
	if len(streamer.thinking) != 1 || streamer.thinking[0] == nil || streamer.thinking[0].Effort != defaults.Effort {
		t.Fatalf("thinking = %+v, want default effort %q", streamer.thinking, defaults.Effort)
	}
}

func TestResolveSubagentModelPriorityAndAliases(t *testing.T) {
	tests := []struct {
		name         string
		requestModel string
		agentModel   string
		parentModel  string
		tierModels   map[string]string
		want         string
	}{
		{
			name:         "request model overrides agent frontmatter",
			requestModel: "opus",
			agentModel:   "haiku",
			parentModel:  "claude-sonnet-4-6",
			tierModels:   map[string]string{"opus": "configured-large"},
			want:         "configured-large",
		},
		{
			name:         "same-tier request alias keeps exact parent model",
			requestModel: "sonnet",
			agentModel:   "haiku",
			parentModel:  "claude-sonnet-4-6",
			want:         "claude-sonnet-4-6",
		},
		{
			name:        "agent inherit uses parent model",
			agentModel:  "inherit",
			parentModel: "gpt-5.5",
			want:        "gpt-5.5",
		},
		{
			name:        "unconfigured family alias inherits parent",
			agentModel:  "haiku",
			parentModel: "claude-sonnet-4-6",
			want:        "claude-sonnet-4-6",
		},
		{
			name:        "tier alias on non-Anthropic parent inherits parent (phase 1)",
			agentModel:  "haiku",
			parentModel: "glm5.1",
			want:        "glm5.1",
		},
		{
			name:        "tier alias on non-Anthropic parent uses configured tier model (phase 2)",
			agentModel:  "haiku",
			parentModel: "glm5.1",
			tierModels:  map[string]string{"haiku": "glm-4-flash"},
			want:        "glm-4-flash",
		},
		{
			name:        "tier alias on non-Anthropic parent without matching tier key inherits",
			agentModel:  "haiku",
			parentModel: "glm5.1",
			tierModels:  map[string]string{"sonnet": "glm-4"},
			want:        "glm5.1",
		},
		{
			name:        "concrete agent model passes through on non-Anthropic parent",
			agentModel:  "glm-4-air",
			parentModel: "glm5.1",
			want:        "glm-4-air",
		},
		{
			name:         "request tier alias on non-Anthropic parent inherits, overriding agent concrete model",
			requestModel: "opus",
			agentModel:   "glm-4-air",
			parentModel:  "glm5.1",
			want:         "glm5.1",
		},
		{
			name:        "explicit tier map applies to every provider",
			agentModel:  "haiku",
			parentModel: "claude-sonnet-4-6",
			tierModels:  map[string]string{"haiku": "glm-4-flash"},
			want:        "glm-4-flash",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveSubagentModel(tt.requestModel, tt.agentModel, tt.parentModel, tt.tierModels); got != tt.want {
				t.Fatalf("resolveSubagentModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRuntimeRequestModelOverridesAgentFrontmatter(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	if err := config.SaveGlobalSettings(config.Settings{SubagentModelTiers: map[string]string{"opus": "configured-large"}}); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}
	agent := `---
name: reviewer
model: haiku
---
Review with the requested model.`
	if err := os.WriteFile(filepath.Join(agentDir, "reviewer.md"), []byte(agent), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &captureSingleTurnStreamer{}
	runtime := Runtime{
		Client:   streamer,
		Registry: tools.NewRegistry(echoTool{}),
		Model:    "claude-sonnet-4-6",
		MaxTurns: 1,
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", SubagentType: "reviewer", Model: "opus", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.models) != 1 || streamer.models[0] != "configured-large" {
		t.Fatalf("models = %+v", streamer.models)
	}
	if result.Model != "configured-large" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimeAppliesAgentToolPolicy(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	agentDir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}
	agent := "---\nname: reviewer\ntools: [Echo, Blocked]\ndisallowedTools: [Blocked]\n---\nReview deeply."
	if err := os.WriteFile(filepath.Join(agentDir, "reviewer.md"), []byte(agent), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &scriptedStreamer{toolName: "Blocked", expectedToolResult: "unknown tool: Blocked"}
	runtime := Runtime{
		Client: streamer,
		Registry: tools.NewRegistry(
			echoTool{},
			blockedTool{},
		),
		Model: "base-model",
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", SubagentType: "reviewer", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError || result.ToolCalls[0].Output != "unknown tool: Blocked" {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	if len(streamer.tools[0]) != 1 || streamer.tools[0][0].Name != "Echo" {
		t.Fatalf("tools = %+v", streamer.tools[0])
	}
}

func TestRuntimeEmitsCacheSafeSignatureEvents(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    &scriptedStreamer{},
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", Description: "cache task", CWD: project, TenantID: 1, UserID: 2}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
	events := store.eventsByType(agenttasks.EventCacheState)
	if len(events) != 2 {
		t.Fatalf("cache events = %+v", store.eventTypes())
	}
	firstPayload := eventPayloadFromInput(t, events[0])
	if firstPayload["initialized"] != false || firstPayload["breaks_cache"] != false || firstPayload["signature_hash"] == "" {
		t.Fatalf("first payload = %+v", firstPayload)
	}
	secondPayload := eventPayloadFromInput(t, events[1])
	if secondPayload["initialized"] != true || secondPayload["breaks_cache"] != true || secondPayload["message_changed"] != true {
		t.Fatalf("second payload = %+v", secondPayload)
	}
}

func TestRuntimeEmitsAgentUsageTelemetry(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	sink := telemetry.NewMemorySink()
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    usageSubagentStreamer{},
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "claude-sonnet-4-6",
		TaskStore: store,
	}
	result, err := runtime.Run(ctx, Request{Prompt: "do it", Description: "usage task", CWD: project, TenantID: 1, UserID: 2, ParentSessionID: 3, TraceID: "trace-agent"}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.InputTokens != 15 || result.Usage.OutputTokens != 7 || result.CostUSD <= 0 {
		t.Fatalf("result usage = %+v cost=%f", result.Usage, result.CostUSD)
	}
	usageEvents := store.eventsByType(agenttasks.EventUsage)
	if len(usageEvents) != 1 {
		t.Fatalf("events = %+v", store.eventTypes())
	}
	usagePayload := eventPayloadFromInput(t, usageEvents[0])
	if usagePayload["model"] != "claude-sonnet-4-6" || usagePayload["input_tokens"].(float64) != 15 || usagePayload["output_tokens"].(float64) != 7 {
		t.Fatalf("usage payload = %+v", usagePayload)
	}
	names := telemetryEventNames(sink.Events())
	for _, want := range []string{"agent.run.started", "agent.model.request.started", "agent.model.request.finished", "agent.run.finished"} {
		if !containsString(names, want) {
			t.Fatalf("missing telemetry %q in %+v", want, names)
		}
	}
}

func TestRuntimeAppliesAgentPermissionMode(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))
	agentDir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}
	agent := "---\nname: reviewer\ntools: [Write]\npermissionMode: ask\n---\nReview deeply."
	if err := os.WriteFile(filepath.Join(agentDir, "reviewer.md"), []byte(agent), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &scriptedStreamer{toolName: "Write"}
	prompted := false
	runtime := Runtime{
		Client: streamer,
		Registry: tools.NewRegistry(
			tools.Guard(writeTool{}, permissions.Policy{DefaultMode: "allow"}),
		),
		Model: "base-model",
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", SubagentType: "reviewer", CWD: project}, tools.Context{
		CWD: project,
		PermissionPrompt: func(context.Context, tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			prompted = true
			return tools.PermissionPromptResponse{Allowed: true, Destination: "once", Reason: "ok"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !prompted {
		t.Fatal("permission prompt was not called")
	}
	if result.PermissionMode != "ask" || result.Content != "done" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRuntimePersistsTaskFinishAfterCancellation(t *testing.T) {
	store := &fakeTaskStore{}
	runtime := Runtime{TaskStore: store}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runtime.appendEvent(ctx, Request{TenantID: 1, UserID: 2, TraceID: "trace-1"}, 101, agenttasks.EventCancelled, map[string]any{"source": "test"})
	runtime.finishTask(ctx, Request{}, 101, agenttasks.StatusFailed, Result{TaskID: 101})

	if store.appendCtxErr != nil {
		t.Fatalf("append context was cancelled: %v", store.appendCtxErr)
	}
	if store.finishCtxErr != nil {
		t.Fatalf("finish context was cancelled: %v", store.finishCtxErr)
	}
	if store.finishedStatus != agenttasks.StatusCancelled {
		t.Fatalf("status = %s", store.finishedStatus)
	}
	if got := store.eventTypes(); strings.Join(got, ",") != "cancelled" {
		t.Fatalf("events = %+v", got)
	}
}

func TestRuntimeStopsWhenPersistentTaskIsCancelled(t *testing.T) {
	project := t.TempDir()
	streamer := &scriptedStreamer{}
	store := &fakeTaskStore{cancelled: true}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
	}
	_, err := runtime.Run(context.Background(), Request{Prompt: "do it", CWD: project, TenantID: 1, UserID: 2}, tools.Context{CWD: project})
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
	if streamer.calls != 0 {
		t.Fatalf("streamer calls = %d", streamer.calls)
	}
	if store.finishedStatus != agenttasks.StatusCancelled {
		t.Fatalf("status = %s", store.finishedStatus)
	}
	if got := store.eventTypes(); strings.Join(got, ",") != "started,cancelled" {
		t.Fatalf("events = %+v", got)
	}
}

func TestRuntimeRunsSubagentStopHook(t *testing.T) {
	project := t.TempDir()
	startOut := filepath.Join(project, "subagent-start.json")
	hookOut := filepath.Join(project, "subagent-stop.json")
	streamer := &singleTurnStreamer{}
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: store,
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.SubagentStart: {{Command: `cat > subagent-start.json`}},
			hooks.SubagentStop:  {{Command: `cat > subagent-stop.json`}},
		}),
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", CWD: project, TenantID: 1, UserID: 2}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(startOut)
	if err != nil {
		t.Fatal(err)
	}
	var startPayload hooks.Payload
	if err := json.Unmarshal(data, &startPayload); err != nil {
		t.Fatalf("start payload = %s: %v", string(data), err)
	}
	if startPayload.Event != hooks.SubagentStart || startPayload.Status != agenttasks.StatusRunning || startPayload.TaskID != result.TaskID || startPayload.AgentID != "agent-101" || startPayload.AgentType != "general-purpose" || startPayload.AgentTranscriptPath != result.TranscriptPath {
		t.Fatalf("start payload = %+v result=%+v", startPayload, result)
	}
	data, err = os.ReadFile(hookOut)
	if err != nil {
		t.Fatal(err)
	}
	var payload hooks.Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("payload = %s: %v", string(data), err)
	}
	if payload.Event != hooks.SubagentStop || payload.Status != agenttasks.StatusCompleted || payload.TaskID != result.TaskID || payload.AgentID != "agent-101" || payload.AgentType != "general-purpose" || payload.SessionID != result.SessionID || payload.AgentName != "general-purpose" || payload.AgentTranscriptPath != result.TranscriptPath || payload.LastAssistantMessage != "done" || payload.IsError {
		t.Fatalf("payload = %+v result=%+v", payload, result)
	}
}

func TestRuntimeInjectsSubagentStartHookAdditionalContext(t *testing.T) {
	project := t.TempDir()
	streamer := &captureSingleTurnStreamer{}
	runtime := Runtime{
		Client:    streamer,
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "base-model",
		TaskStore: &fakeTaskStore{},
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.SubagentStart: {{Command: `printf '{"additionalContext":"HOOK_CONTEXT_MARKER"}'`}},
		}),
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", CWD: project, TenantID: 1, UserID: 2}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q", result.Content)
	}
	if !requestMessagesContainSubstring(streamer.messages, "SubagentStart hook additional context: HOOK_CONTEXT_MARKER") {
		t.Fatalf("hook additional context missing from request messages: %+v", streamer.messages)
	}
}

type blockedTool struct{}

func (blockedTool) Name() string                 { return "Blocked" }
func (blockedTool) Description() string          { return "blocked" }
func (blockedTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (blockedTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: "blocked"}
}

type mcpLookupTool struct{}

func (mcpLookupTool) Name() string                 { return "mcp__local__lookup" }
func (mcpLookupTool) Description() string          { return "lookup" }
func (mcpLookupTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (mcpLookupTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: "from mcp"}
}

type writeTool struct{}

func (writeTool) Name() string                 { return "Write" }
func (writeTool) Description() string          { return "write" }
func (writeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (writeTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: "from subagent"}
}

type singleTurnStreamer struct{}

func (s *singleTurnStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type captureSingleTurnStreamer struct {
	models   []string
	messages []anthropic.MessageParam
	tools    []anthropic.ToolDefinition
}

func (s *captureSingleTurnStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.models = append(s.models, req.Model)
	s.messages = append([]anthropic.MessageParam(nil), req.Messages...)
	s.tools = append([]anthropic.ToolDefinition(nil), req.Tools...)
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func requestToolResultText(messages []anthropic.MessageParam, toolUseID string) string {
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == "tool_result" && block.ToolUseID == toolUseID {
				return block.Content
			}
		}
	}
	return ""
}

type slowSingleTurnStreamer struct {
	done chan struct{}
}

func (s *slowSingleTurnStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	select {
	case <-s.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type agentMessageStreamer struct {
	calls      int
	sawMessage bool
}

func (s *agentMessageStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"ok"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, "please continue") {
				s.sawMessage = true
			}
		}
	}
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type fakeTaskStore struct {
	mu             sync.Mutex
	tasks          []agenttasks.TaskInput
	events         []agenttasks.EventInput
	finishedStatus string
	resultJSON     string
	finishCalls    int
	finishCtxErr   error
	appendCtxErr   error
	cancelled      bool
}

func (f *fakeTaskStore) CreateAgentTask(_ context.Context, input agenttasks.TaskInput) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, input)
	return 101, nil
}

func (f *fakeTaskStore) FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if taskID != 101 {
		panic("unexpected task id")
	}
	f.finishCalls++
	f.finishCtxErr = ctx.Err()
	f.finishedStatus = status
	f.resultJSON = resultJSON
	return nil
}

func (f *fakeTaskStore) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendCtxErr = ctx.Err()
	f.events = append(f.events, input)
	return uint64(len(f.events)), nil
}

func (f *fakeTaskStore) IsAgentTaskCancelled(ctx context.Context, taskID uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled, nil
}

func (f *fakeTaskStore) eventTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.events))
	for _, event := range f.events {
		out = append(out, event.EventType)
	}
	return out
}

func (f *fakeTaskStore) eventTypesWithoutCacheState() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.events))
	for _, event := range f.events {
		if event.EventType != agenttasks.EventCacheState {
			out = append(out, event.EventType)
		}
	}
	return out
}

func (f *fakeTaskStore) eventsByType(eventType string) []agenttasks.EventInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]agenttasks.EventInput, 0)
	for _, event := range f.events {
		if event.EventType == eventType {
			out = append(out, event)
		}
	}
	return out
}

func (f *fakeTaskStore) finished() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finishedStatus
}

func (f *fakeTaskStore) createdTasks() []agenttasks.TaskInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agenttasks.TaskInput(nil), f.tasks...)
}

func (f *fakeTaskStore) eventPayload(t *testing.T, index int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.events) {
		t.Fatalf("event index %d out of range", index)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(f.events[index].PayloadJSON), &payload); err != nil {
		t.Fatalf("payload %d = %q: %v", index, f.events[index].PayloadJSON, err)
	}
	return payload
}

func eventPayloadFromInput(t *testing.T, event agenttasks.EventInput) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		t.Fatalf("payload = %q: %v", event.PayloadJSON, err)
	}
	return payload
}

func mustWriteRuntimeTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func runGitRuntimeTest(cwd string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	return cmd.Run()
}

func parseAgentPromptDumpRecords(t *testing.T, raw []byte) []promptdump.Record {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	records := make([]promptdump.Record, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record promptdump.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal agent prompt dump record: %v\nline=%s", err, line)
		}
		records = append(records, record)
	}
	return records
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func telemetryEventNames(events []telemetry.Event) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Name)
	}
	return out
}
