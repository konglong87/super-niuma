package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

type parallelToolStreamer struct {
	toolNames []string
	calls     int
	requests  []anthropic.MessagesRequest
}

func (s *parallelToolStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	s.requests = append(s.requests, req)
	if s.calls == 1 {
		blocks := make([]anthropic.ContentBlock, 0, len(s.toolNames))
		for i, name := range s.toolNames {
			blocks = append(blocks, anthropic.ContentBlock{Type: blockTypeToolUse, ID: "tool-" + string(rune('a'+i)), Name: name, Input: json.RawMessage(`{"value":` + string(rune('1'+i)) + `}`)})
		}
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: blocks}, StopReason: "tool_use"}, nil
	}
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "done"}}}, StopReason: "end_turn"}, nil
}

type overlapProbe struct {
	active  atomic.Int32
	max     atomic.Int32
	release chan struct{}
	once    sync.Once
}

func newOverlapProbe() *overlapProbe {
	return &overlapProbe{release: make(chan struct{})}
}

func (p *overlapProbe) run(ctx context.Context) {
	active := p.active.Add(1)
	for {
		current := p.max.Load()
		if active <= current || p.max.CompareAndSwap(current, active) {
			break
		}
	}
	if active >= 2 {
		p.once.Do(func() { close(p.release) })
	}
	select {
	case <-p.release:
	case <-ctx.Done():
	case <-time.After(250 * time.Millisecond):
	}
	p.active.Add(-1)
}

type readOnlyProbeTool struct {
	name        string
	probe       *overlapProbe
	fail        bool
	mu          *sync.Mutex
	invocations map[string]tools.Invocation
}

func (t readOnlyProbeTool) Name() string               { return t.name }
func (readOnlyProbeTool) Description() string          { return "read-only overlap probe" }
func (readOnlyProbeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (readOnlyProbeTool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{Concurrency: tools.ConcurrencyReadOnly}
}
func (t readOnlyProbeTool) Run(ctx context.Context, _ json.RawMessage, tc tools.Context) tools.Result {
	if t.mu != nil {
		t.mu.Lock()
		t.invocations[t.name] = tc.Invocation
		t.mu.Unlock()
	}
	t.probe.run(ctx)
	return tools.Result{Content: t.name, IsError: t.fail}
}

type serialProbeTool struct {
	name  string
	probe *overlapProbe
}

type cancellationReadOnlyTool struct {
	name         string
	runs         *atomic.Int32
	gateCalls    *atomic.Int32
	cancelAtGate context.CancelFunc
}

func (t cancellationReadOnlyTool) Name() string      { return t.name }
func (cancellationReadOnlyTool) Description() string { return "read-only cancellation probe" }
func (cancellationReadOnlyTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (cancellationReadOnlyTool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{Concurrency: tools.ConcurrencyReadOnly}
}
func (t cancellationReadOnlyTool) ParallelSafe(json.RawMessage, tools.Context) bool {
	if t.gateCalls != nil {
		t.gateCalls.Add(1)
	}
	if t.cancelAtGate != nil {
		t.cancelAtGate()
	}
	return true
}
func (t cancellationReadOnlyTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	t.runs.Add(1)
	return tools.Result{Content: t.name}
}

func (t serialProbeTool) Name() string               { return t.name }
func (serialProbeTool) Description() string          { return "serial overlap probe" }
func (serialProbeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t serialProbeTool) Run(ctx context.Context, _ json.RawMessage, _ tools.Context) tools.Result {
	t.probe.run(ctx)
	return tools.Result{Content: t.name}
}

func TestReadOnlyToolTurnExecutesInParallelAndCommitsInModelOrder(t *testing.T) {
	probe := newOverlapProbe()
	streamer := &parallelToolStreamer{toolNames: []string{"ReadOne", "ReadTwo"}}
	runtime := New(streamer, tools.NewRegistry(
		readOnlyProbeTool{name: "ReadOne", probe: probe},
		readOnlyProbeTool{name: "ReadTwo", probe: probe, fail: true},
	), Options{MaxTurns: 3, CWD: t.TempDir()})

	result, err := runtime.Run(context.Background(), "inspect", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if probe.max.Load() != 2 {
		t.Fatalf("max concurrent tools = %d, want 2", probe.max.Load())
	}
	if len(result.ToolCalls) != 2 || result.ToolCalls[0].ID != "tool-a" || result.ToolCalls[1].ID != "tool-b" || !result.ToolCalls[1].IsError {
		t.Fatalf("tool call commit order/result = %+v", result.ToolCalls)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d", len(streamer.requests))
	}
	last := streamer.requests[1].Messages[len(streamer.requests[1].Messages)-1].Content
	if len(last) != 2 || last[0].ToolUseID != "tool-a" || last[1].ToolUseID != "tool-b" {
		t.Fatalf("model context result order = %+v", last)
	}
}

func TestParallelToolsReceiveDistinctRuntimeInvocationsInOneBatch(t *testing.T) {
	probe := newOverlapProbe()
	streamer := &parallelToolStreamer{toolNames: []string{"ReadOne", "ReadTwo"}}
	var mu sync.Mutex
	invocations := make(map[string]tools.Invocation)
	runtime := New(streamer, tools.NewRegistry(
		readOnlyProbeTool{name: "ReadOne", probe: probe, mu: &mu, invocations: invocations},
		readOnlyProbeTool{name: "ReadTwo", probe: probe, mu: &mu, invocations: invocations},
	), Options{Model: "test", MaxTurns: 3, CWD: t.TempDir(), RunID: "run-123"})

	if _, err := runtime.Run(context.Background(), "inspect", io.Discard); err != nil {
		t.Fatal(err)
	}
	wantBatch := "145820fa9a0724d38e7c323f79cf82840cff55ea552f38ba12d9ddefd14d9c64"
	if got := invocations["ReadOne"]; got.RunID != "run-123" || got.ToolUseID != "tool-a" || got.BatchID != wantBatch {
		t.Fatalf("ReadOne invocation = %+v", got)
	}
	if got := invocations["ReadTwo"]; got.RunID != "run-123" || got.ToolUseID != "tool-b" || got.BatchID != wantBatch {
		t.Fatalf("ReadTwo invocation = %+v", got)
	}
}

func TestParallelOnToolCallCancellationStopsCallbacksAndToolEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	streamer := &parallelToolStreamer{toolNames: []string{"ReadOne", "ReadTwo"}}
	var runs atomic.Int32
	recorder := cancellationRecorder(t)
	runtime := New(streamer, tools.NewRegistry(
		cancellationReadOnlyTool{name: "ReadOne", runs: &runs},
		cancellationReadOnlyTool{name: "ReadTwo", runs: &runs},
	), Options{MaxTurns: 3, CWD: t.TempDir(), Recorder: recorder})
	var calls, results []string
	result, err := runtime.RunWithCallbacks(ctx, "inspect", io.Discard, RunCallbacks{
		OnToolCall: func(event ToolCallEvent) error {
			calls = append(calls, event.ID)
			cancel()
			return nil
		},
		OnToolResult: func(trace ToolTrace) error { results = append(results, trace.ID); return nil },
	})
	if !errors.Is(err, context.Canceled) || runs.Load() != 0 {
		t.Fatalf("err=%v runs=%d result=%+v", err, runs.Load(), result)
	}
	if !reflect.DeepEqual(calls, []string{"tool-a"}) || !reflect.DeepEqual(results, []string{"tool-a"}) {
		t.Fatalf("callbacks calls=%v results=%v", calls, results)
	}
	assertCancelledTurnPairing(t, recorder, result, "tool-a", "tool-b")
}

func TestParallelGateCancellationStopsFurtherGatesCallbacksAndToolEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	streamer := &parallelToolStreamer{toolNames: []string{"ReadOne", "ReadTwo"}}
	var runs, gateCalls atomic.Int32
	recorder := cancellationRecorder(t)
	runtime := New(streamer, tools.NewRegistry(
		cancellationReadOnlyTool{name: "ReadOne", runs: &runs, gateCalls: &gateCalls, cancelAtGate: cancel},
		cancellationReadOnlyTool{name: "ReadTwo", runs: &runs, gateCalls: &gateCalls},
	), Options{MaxTurns: 3, CWD: t.TempDir(), Recorder: recorder})
	var calls, results []string
	result, err := runtime.RunWithCallbacks(ctx, "inspect", io.Discard, RunCallbacks{
		OnToolCall:   func(event ToolCallEvent) error { calls = append(calls, event.ID); return nil },
		OnToolResult: func(trace ToolTrace) error { results = append(results, trace.ID); return nil },
	})
	if !errors.Is(err, context.Canceled) || runs.Load() != 0 || gateCalls.Load() != 1 {
		t.Fatalf("err=%v runs=%d gates=%d result=%+v", err, runs.Load(), gateCalls.Load(), result)
	}
	if len(calls) != 0 || len(results) != 0 {
		t.Fatalf("callbacks calls=%v results=%v", calls, results)
	}
	assertCancelledTurnPairing(t, recorder, result, "tool-a", "tool-b")
}

func TestMixedOrSerialToolTurnFallsBackToSerialExecution(t *testing.T) {
	probe := newOverlapProbe()
	streamer := &parallelToolStreamer{toolNames: []string{"ReadOne", "WriteLike"}}
	runtime := New(streamer, tools.NewRegistry(
		readOnlyProbeTool{name: "ReadOne", probe: probe},
		serialProbeTool{name: "WriteLike", probe: probe},
	), Options{MaxTurns: 3, CWD: t.TempDir()})

	if _, err := runtime.Run(context.Background(), "inspect then mutate", io.Discard); err != nil {
		t.Fatal(err)
	}
	if probe.max.Load() != 1 {
		t.Fatalf("mixed turn max concurrent tools = %d, want serial execution", probe.max.Load())
	}
}

func TestParallelReadOnlyToolsCanBeDisabled(t *testing.T) {
	probe := newOverlapProbe()
	streamer := &parallelToolStreamer{toolNames: []string{"ReadOne", "ReadTwo"}}
	runtime := New(streamer, tools.NewRegistry(
		readOnlyProbeTool{name: "ReadOne", probe: probe},
		readOnlyProbeTool{name: "ReadTwo", probe: probe},
	), Options{MaxTurns: 3, CWD: t.TempDir(), MaxParallelReadOnlyTools: -1})

	if _, err := runtime.Run(context.Background(), "inspect", io.Discard); err != nil {
		t.Fatal(err)
	}
	if probe.max.Load() != 1 {
		t.Fatalf("disabled parallel max = %d, want 1", probe.max.Load())
	}
}

func TestEffectiveMaxParallelReadOnlyTools(t *testing.T) {
	tests := []struct {
		configured int
		workers    int
		enabled    bool
	}{
		{configured: -1, workers: 1, enabled: false},
		{configured: 0, workers: DefaultMaxParallelReadOnlyTools, enabled: true},
		{configured: 8, workers: 8, enabled: true},
	}
	for _, test := range tests {
		workers, enabled := EffectiveMaxParallelReadOnlyTools(test.configured)
		if workers != test.workers || enabled != test.enabled {
			t.Fatalf("configured %d: workers=%d enabled=%t", test.configured, workers, enabled)
		}
	}
}

func TestStreamJSONWithHookEventsFallsBackToOrderedSerialTools(t *testing.T) {
	probe := newOverlapProbe()
	streamer := &parallelToolStreamer{toolNames: []string{"ReadOne", "ReadTwo"}}
	runtime := New(streamer, tools.NewRegistry(
		readOnlyProbeTool{name: "ReadOne", probe: probe},
		readOnlyProbeTool{name: "ReadTwo", probe: probe},
	), Options{MaxTurns: 3, CWD: t.TempDir(), IncludeHookEvents: true})

	var output bytes.Buffer
	if err := runtime.RunStreamJSON(context.Background(), "inspect", &output); err != nil {
		t.Fatal(err)
	}
	if probe.max.Load() != 1 {
		t.Fatalf("hook event stream max concurrent tools = %d, want serial execution", probe.max.Load())
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode JSONL event: %v\n%s", err, output.String())
		}
	}
}
