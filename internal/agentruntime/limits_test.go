package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/agentbudget"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

// depthRecordingTool captures the sub-agent depth stamped onto the tool context
// so the wiring (Run -> runTool -> childContext) is asserted, not assumed.
type depthRecordingTool struct {
	mu     sync.Mutex
	depths []int
	budget *agentbudget.Budget
}

func (t *depthRecordingTool) Name() string        { return "DepthProbe" }
func (t *depthRecordingTool) Description() string { return "record sub-agent depth" }
func (t *depthRecordingTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (t *depthRecordingTool) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.depths = append(t.depths, toolContext.SubagentDepth)
	t.budget = toolContext.AgentBudget
	return tools.Result{Content: "probe"}
}

func (t *depthRecordingTool) seen() ([]int, *agentbudget.Budget) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]int(nil), t.depths...), t.budget
}

// nestingTool re-enters Runtime.Run with the tool context it was handed, the way
// Task / AgentCreate do. It is how the recursion ceiling is exercised end to end
// instead of by poking the counter directly.
type nestingTool struct {
	runtime  Runtime
	cwd      string
	attempts *nestAttempts
}

// nestAttempts records every nested Run outcome. The top-level result only shows
// its own tool calls, so a rejection three frames down is otherwise invisible.
//
// runawayGuard is what makes a regression fail instead of hang: if the depth
// counter stops being propagated, nothing bounds the nesting and the tool would
// recurse until the process dies. The guard stops at a depth the fix would never
// reach, so the assertions below report the runaway as a normal failure.
type nestAttempts struct {
	mu           sync.Mutex
	entered      int
	depths       []int
	failures     []string
	guardTripped bool
}

const runawayGuard = 8

// enter counts entries, not completions: in a real runaway no frame ever returns,
// so a completion-based guard would never fire.
func (a *nestAttempts) enter() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.entered >= runawayGuard {
		a.guardTripped = true
		return false
	}
	a.entered++
	return true
}

func (a *nestAttempts) record(depth int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.depths = append(a.depths, depth)
	if err != nil {
		a.failures = append(a.failures, err.Error())
	}
}

func (a *nestAttempts) snapshot() ([]int, []string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.depths...), append([]string(nil), a.failures...), a.guardTripped
}

func (nestingTool) Name() string        { return "Nest" }
func (nestingTool) Description() string { return "spawn a nested sub-agent" }
func (nestingTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (t nestingTool) Run(ctx context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	if t.attempts != nil && !t.attempts.enter() {
		return tools.Result{Content: "runaway guard: nesting never stopped", IsError: true}
	}
	result, err := t.runtime.Run(ctx, Request{Prompt: "nested work", CWD: t.cwd}, toolContext)
	if t.attempts != nil {
		t.attempts.record(toolContext.SubagentDepth+1, err)
	}
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: result.Content}
}

// singleToolThenTextStreamer asks for one tool call, then finishes. The decision
// is driven by the request contents, not by a call counter, so nested sub-agent
// frames sharing one streamer each behave the same way. Usage is reported on
// every turn so budget accounting has something to accumulate.
type singleToolThenTextStreamer struct {
	mu            sync.Mutex
	calls         int
	toolName      string
	usage         anthropic.Usage
	toolEveryTurn bool
	toolDefs      [][]anthropic.ToolDefinition
}

func (s *singleToolThenTextStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.mu.Lock()
	s.calls++
	s.toolDefs = append(s.toolDefs, req.Tools)
	s.mu.Unlock()
	if s.toolEveryTurn || !messagesContainToolResult(req.Messages) {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_limit",
				Name:  s.toolName,
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
			Usage:      s.usage,
		}, nil
	}
	_ = cb.OnText("done")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage:      s.usage,
	}, nil
}

func (s *singleToolThenTextStreamer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// firstTools returns the tool definitions offered on the first request. The last
// turn deliberately ships no tools (shouldDisableSubagentToolsForFinalTurn), so
// the first request is the one that shows what the sub-agent was allowed.
func (s *singleToolThenTextStreamer) firstTools() []anthropic.ToolDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.toolDefs) == 0 {
		return nil
	}
	return append([]anthropic.ToolDefinition(nil), s.toolDefs[0]...)
}

func messagesContainToolResult(messages []anthropic.MessageParam) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == "tool_result" {
				return true
			}
		}
	}
	return false
}

func TestRuntimeStampsSubagentDepthOnToolContext(t *testing.T) {
	project := t.TempDir()
	probe := &depthRecordingTool{}
	streamer := &singleToolThenTextStreamer{toolName: probe.Name()}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(probe), Model: "claude-sonnet-4-6", MaxTurns: 2}

	if _, err := runtime.Run(context.Background(), Request{Prompt: "probe depth", CWD: project}, tools.Context{CWD: project}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	depths, budget := probe.seen()
	if len(depths) != 1 || depths[0] != 1 {
		t.Fatalf("tool saw sub-agent depths %v, want [1]; Run must stamp the depth onto the child tool context", depths)
	}
	if budget == nil {
		t.Fatal("tool saw a nil AgentBudget; Run must hand a budget down so nested sub-agents share one counter")
	}
}

func TestRuntimeRejectsSubagentBeyondDepthLimit(t *testing.T) {
	project := t.TempDir()
	streamer := &singleToolThenTextStreamer{toolName: "DepthProbe"}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(&depthRecordingTool{}), Model: "claude-sonnet-4-6", MaxTurns: 2}

	// Entering at the ceiling means this run would be one frame too deep.
	atLimit := tools.Context{CWD: project, SubagentDepth: MaxSubagentDepth()}
	_, err := runtime.Run(context.Background(), Request{Prompt: "too deep", CWD: project}, atLimit)
	if err == nil {
		t.Fatal("Run() at the depth ceiling returned no error; recursion must be refused")
	}
	for _, want := range []string{"recursion depth limit reached", "do the remaining work directly", EnvMaxSubagentDepth} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("depth error %q missing %q; the message has to tell the model what to do instead", err, want)
		}
	}
	if streamer.callCount() != 0 {
		t.Fatalf("streamer was called %d times; a refused run must not reach the model", streamer.callCount())
	}
}

func TestRuntimeRejectsNestedSubagentsAtDepthLimit(t *testing.T) {
	project := t.TempDir()
	t.Setenv(EnvMaxSubagentDepth, "2")
	streamer := &singleToolThenTextStreamer{toolName: "Nest"}
	attempts := &nestAttempts{}
	registry := tools.NewRegistry()
	nested := Runtime{Client: streamer, Registry: registry, Model: "claude-sonnet-4-6", MaxTurns: 2}
	registry.Register(nestingTool{runtime: nested, cwd: project, attempts: attempts})

	// depth 1 spawns depth 2, which spawns the rejected depth 3.
	if _, err := nested.Run(context.Background(), Request{Prompt: "top level", CWD: project}, tools.Context{CWD: project}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	depths, failures, runaway := attempts.snapshot()
	if runaway {
		t.Fatalf("nesting never stopped: the test's own %d-frame guard tripped; the depth ceiling is not being enforced", runawayGuard)
	}
	// Recorded innermost-first (each entry lands after its own Run returns).
	sort.Ints(depths)
	if want := []int{2, 3}; len(depths) != len(want) || depths[0] != want[0] || depths[1] != want[1] {
		t.Fatalf("nested Run depths = %v, want %v; nesting must climb one frame at a time and then stop", depths, want)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "recursion depth limit reached") {
		t.Fatalf("nested failures = %v, want exactly one depth rejection; unbounded nesting is AUDIT-P0-14", failures)
	}
	// depth 1, depth 2, and the refused depth-3 attempt: three Run entries, but
	// only the two accepted ones reach the model, twice each (tool turn + final).
	if got := streamer.callCount(); got != 4 {
		t.Fatalf("streamer calls = %d, want 4 (two accepted sub-agent frames x two turns)", got)
	}
}

func TestRuntimeDeniesLateralAgentMessagingToSubagents(t *testing.T) {
	project := t.TempDir()
	streamer := &singleToolThenTextStreamer{toolName: "Echo"}
	registry := tools.NewRegistry(
		echoTool{},
		namedNoopTool{name: "AgentMessage"},
		namedNoopTool{name: "SendMessage"},
		namedNoopTool{name: "Read"},
	)
	runtime := Runtime{Client: streamer, Registry: registry, Model: "claude-sonnet-4-6", MaxTurns: 2}

	if _, err := runtime.Run(context.Background(), Request{Prompt: "check tools", CWD: project}, tools.Context{CWD: project}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	exposed := "," + strings.Join(toolDefinitionNames(streamer.firstTools()), ",") + ","
	for _, denied := range []string{"AgentMessage", "SendMessage"} {
		if strings.Contains(exposed, ","+denied+",") {
			t.Fatalf("sub-agent was offered %s; SendMessage only wraps AgentMessage, so both have to be denied (exposed: %s)", denied, exposed)
		}
	}
	if !strings.Contains(exposed, ",Read,") {
		t.Fatalf("normal tools were dropped along with the messaging tools: %s", exposed)
	}
}

func TestRuntimeStopsWhenAgentBudgetExhausted(t *testing.T) {
	project := t.TempDir()
	streamer := &singleToolThenTextStreamer{
		toolName:      "Echo",
		toolEveryTurn: true,
		usage:         anthropic.Usage{InputTokens: 400, OutputTokens: 100},
	}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "claude-sonnet-4-6", MaxTurns: 20}
	budget := agentbudget.New(agentbudget.Limits{MaxTotalTokens: 1000})

	_, err := runtime.Run(context.Background(), Request{Prompt: "burn tokens", CWD: project}, tools.Context{CWD: project, AgentBudget: budget})
	if err == nil {
		t.Fatal("Run() never tripped the budget; a sub-agent must not burn max_turns worth of tokens unchecked")
	}
	if !errors.Is(err, agentbudget.ErrExhausted) {
		t.Fatalf("Run() error = %v, want an agentbudget.ErrExhausted", err)
	}
	if !strings.Contains(err.Error(), agentbudget.EnvMaxTotalTokens) {
		t.Fatalf("budget error %q does not name the override env var", err)
	}
	// 500 tokens per turn against a 1000 ceiling: turns 1 and 2 run, turn 3 is
	// refused before the request. Stopping later would mean the breaker is
	// checked after paying rather than before.
	if got := streamer.callCount(); got != 2 {
		t.Fatalf("streamer calls = %d, want 2 before the breaker trips", got)
	}
	if snapshot := budget.Snapshot(); snapshot.TotalTokens != 1000 {
		t.Fatalf("budget snapshot = %+v, want 1000 accumulated tokens", snapshot)
	}
}

func TestRuntimeSharesOneBudgetAcrossNestedSubagents(t *testing.T) {
	project := t.TempDir()
	t.Setenv(EnvMaxSubagentDepth, "2")
	streamer := &singleToolThenTextStreamer{
		toolName: "Nest",
		usage:    anthropic.Usage{InputTokens: 10, OutputTokens: 5},
	}
	registry := tools.NewRegistry()
	nested := Runtime{Client: streamer, Registry: registry, Model: "claude-sonnet-4-6", MaxTurns: 2}
	registry.Register(nestingTool{runtime: nested, cwd: project})
	budget := agentbudget.New(agentbudget.Limits{MaxTotalTokens: 0})

	if _, err := nested.Run(context.Background(), Request{Prompt: "top", CWD: project}, tools.Context{CWD: project, AgentBudget: budget}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Four model turns across the two accepted frames, 15 tokens each. A fresh
	// budget per frame would only ever show 30.
	if snapshot := budget.Snapshot(); snapshot.TotalTokens != 60 {
		t.Fatalf("budget snapshot = %+v, want 60 tokens; the nested sub-agent must accumulate into the parent's budget", snapshot)
	}
}

func TestRunBackgroundRejectsBeyondBackgroundAgentLimit(t *testing.T) {
	project := t.TempDir()
	t.Setenv(EnvMaxBackgroundAgents, "1")
	// Hold the only slot so the next RunBackground has to be refused.
	release, ok := backgroundAgents.acquire()
	if !ok {
		t.Fatalf("could not take the first background slot (in flight = %d)", backgroundAgents.inFlight())
	}

	store := &fakeTaskStore{}
	streamer := &finalTextStreamer{text: "done"}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(), Model: "claude-sonnet-4-6", MaxTurns: 1, TaskStore: store}

	_, err := runtime.RunBackground(context.Background(), Request{Prompt: "detached work", CWD: project}, tools.Context{CWD: project})
	if err == nil {
		release()
		t.Fatal("RunBackground() over the limit returned no error; detached agents must be refused, not queued")
	}
	for _, want := range []string{"background sub-agent limit reached", "AgentGet", EnvMaxBackgroundAgents} {
		if !strings.Contains(err.Error(), want) {
			release()
			t.Fatalf("capacity error %q missing %q", err, want)
		}
	}
	if len(store.tasks) != 0 {
		release()
		t.Fatalf("a refused background run created %d task rows; rejection must leave no residue", len(store.tasks))
	}

	// Releasing the slot makes room again.
	release()
	if _, err := runtime.RunBackground(context.Background(), Request{Prompt: "detached work", CWD: project}, tools.Context{CWD: project}); err != nil {
		t.Fatalf("RunBackground() after release error = %v; the slot must be reusable", err)
	}
}

func TestBackgroundAgentLimiterReleaseIsIdempotent(t *testing.T) {
	limiter := &backgroundAgentLimiter{max: 1}
	release, ok := limiter.acquire()
	if !ok {
		t.Fatal("first acquire failed")
	}
	if _, ok := limiter.acquire(); ok {
		t.Fatal("second acquire succeeded over a limit of 1")
	}
	release()
	release()
	if limiter.inFlight() != 0 {
		t.Fatalf("in flight = %d after a double release, want 0", limiter.inFlight())
	}
	if _, ok := limiter.acquire(); !ok {
		t.Fatal("acquire failed after release")
	}
}

func TestMaxSubagentDepthEnvOverrideRejectsNonsense(t *testing.T) {
	t.Setenv(EnvMaxSubagentDepth, "4")
	if got := MaxSubagentDepth(); got != 4 {
		t.Fatalf("MaxSubagentDepth() = %d, want 4", got)
	}
	for _, raw := range []string{"0", "-1", "abc", ""} {
		t.Setenv(EnvMaxSubagentDepth, raw)
		if got := MaxSubagentDepth(); got != DefaultMaxSubagentDepth {
			t.Fatalf("MaxSubagentDepth() with %q = %d, want the %d default", raw, got, DefaultMaxSubagentDepth)
		}
	}
}
