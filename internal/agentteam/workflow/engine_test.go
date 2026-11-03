package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryStore struct {
	mu           sync.Mutex
	runs         map[string]Run
	events       map[string][]Event
	nextTask     uint64
	failTerminal bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{runs: map[string]Run{}, events: map[string][]Event{}}
}
func cloneRun(r Run) Run {
	raw, _ := json.Marshal(r)
	var copy Run
	_ = json.Unmarshal(raw, &copy)
	copy.KeyHash = r.KeyHash
	copy.Fingerprint = r.Fingerprint
	return copy
}
func (s *memoryStore) Create(_ context.Context, r Run, events []Event) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.runs {
		if old.Scope == r.Scope && old.KeyHash == r.KeyHash {
			if old.Fingerprint != r.Fingerprint {
				return Run{}, false, ErrIdempotencyConflict
			}
			return cloneRun(old), false, nil
		}
	}
	r.Revision = 1
	r = s.assign(r, events)
	s.runs[r.ID] = cloneRun(r)
	return cloneRun(r), true, nil
}
func (s *memoryStore) assign(r Run, events []Event) Run {
	for i := range r.Nodes {
		for j := range r.Nodes[i].Attempts {
			a := &r.Nodes[i].Attempts[j]
			if a.TaskID == 0 {
				s.nextTask++
				a.TaskID = s.nextTask
			}
		}
	}
	for _, event := range events {
		r.LastSequence++
		event.Sequence = r.LastSequence
		s.events[r.ID] = append(s.events[r.ID], event)
	}
	return r
}
func (s *memoryStore) Get(_ context.Context, scope Scope, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok || r.Scope != scope {
		return Run{}, ErrNotFound
	}
	return cloneRun(r), nil
}
func (s *memoryStore) List(_ context.Context, scope Scope, limit int) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Run{}
	for _, r := range s.runs {
		if r.Scope == scope {
			out = append(out, cloneRun(r))
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
func (s *memoryStore) Save(_ context.Context, revision uint64, r Run, events []Event) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.runs[r.ID]
	if !ok || old.Scope != r.Scope {
		return Run{}, ErrNotFound
	}
	if old.Revision != revision {
		return Run{}, ErrConflict
	}
	if s.failTerminal {
		for _, n := range r.Nodes {
			if n.Status == Completed {
				return Run{}, errors.New("database unavailable")
			}
		}
	}
	r.Revision = revision + 1
	r = s.assign(r, events)
	s.runs[r.ID] = cloneRun(r)
	return cloneRun(r), nil
}
func (s *memoryStore) Events(_ context.Context, scope Scope, id string, after uint64, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok || r.Scope != scope {
		return nil, ErrNotFound
	}
	out := []Event{}
	for _, e := range s.events[id] {
		if e.Sequence > after {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
func (s *memoryStore) Active(context.Context) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Run{}
	for _, r := range s.runs {
		if !Terminal(r.Status) {
			out = append(out, cloneRun(r))
		}
	}
	return out, nil
}

var testScope = Scope{TenantID: 1, UserID: 2}

func newTestRun(t *testing.T, def Definition) Run {
	t.Helper()
	plan, err := Compile(def, []Agent{testAgent("a")})
	if err != nil {
		t.Fatal(err)
	}
	return Run{Scope: testScope, TeamID: 1, TeamKey: "test", TeamVersion: 1, Name: "test", Input: "Complete the goal", Plan: plan, KeyHash: "request-key", Fingerprint: "request-fingerprint"}
}
func runEngine(t *testing.T, store Store, executor Executor) *Engine {
	t.Helper()
	e := NewEngine(store, executor, EngineOptions{PollInterval: time.Millisecond})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return e
}
func waitRun(t *testing.T, store Store, id string, predicate func(Run) bool) Run {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		r, err := store.Get(context.Background(), testScope, id)
		if err == nil && predicate(r) {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	r, _ := store.Get(context.Background(), testScope, id)
	t.Fatalf("run did not reach expected state: %+v", r)
	return Run{}
}
func TestEngineSequentialContextAndArtifacts(t *testing.T) {
	store := newMemoryStore()
	var mu sync.Mutex
	calls := []string{}
	e := runEngine(t, store, ExecutorFunc(func(ctx context.Context, in Input, sink Sink) (Result, error) {
		mu.Lock()
		calls = append(calls, in.NodeID)
		mu.Unlock()
		if in.TaskID == 0 {
			t.Error("missing prepared task")
		}
		if in.NodeID != "plan" && (len(in.Dependencies) != 1 || in.Dependencies[0].NodeID == in.NodeID) {
			t.Error("missing isolated dependency input")
		}
		sink(Event{Type: EventDelta, Text: "safe delta"})
		return Result{Status: Completed, Output: "output for " + in.NodeID, Usage: Usage{InputTokens: 10, OutputTokens: 20}}, nil
	}))
	run, _, err := e.Create(context.Background(), newTestRun(t, testDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, run.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != Completed || len(done.Artifacts) != 3 || done.Usage.Total() != 90 {
		t.Fatalf("bad completion: %+v", done)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(calls, ",") != "plan,work,review" {
		t.Fatal(calls)
	}
	events, _ := store.Events(context.Background(), testScope, run.ID, 0, 200)
	for i, e := range events {
		if e.Sequence != uint64(i+1) {
			t.Fatal("unordered journal", events)
		}
	}
}
func TestEngineParallelJoinAndGlobalBound(t *testing.T) {
	store := newMemoryStore()
	def := testDefinition()
	def.Nodes = []Node{{ID: "left", AgentRef: "a"}, {ID: "right", AgentRef: "a"}, {ID: "join", AgentRef: "a", DependsOn: []string{"left", "right"}}}
	def.Limits.MaxParallel = 2
	started := make(chan string, 3)
	release := make(chan struct{})
	var current, peak atomic.Int32
	e := runEngine(t, store, ExecutorFunc(func(ctx context.Context, in Input, _ Sink) (Result, error) {
		v := current.Add(1)
		defer current.Add(-1)
		for old := peak.Load(); v > old; old = peak.Load() {
			if peak.CompareAndSwap(old, v) {
				break
			}
		}
		started <- in.NodeID
		if in.NodeID != "join" {
			select {
			case <-release:
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
		} else if len(in.Dependencies) != 2 {
			t.Error("join did not receive both outputs")
		}
		return Result{Status: Completed, Output: in.NodeID}, nil
	}))
	run, _, err := e.Create(context.Background(), newTestRun(t, def))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case id := <-started:
			if id == "join" {
				t.Fatal("join ran early")
			}
		case <-time.After(time.Second):
			t.Fatal("parallel worker did not start")
		}
	}
	close(release)
	done := waitRun(t, store, run.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != Completed || peak.Load() != 2 {
		t.Fatal(done.Status, peak.Load())
	}
}
func TestEngineRetryAndFailedStatusCannotSucceed(t *testing.T) {
	for _, transient := range []bool{true, false} {
		t.Run(fmt.Sprint(transient), func(t *testing.T) {
			store := newMemoryStore()
			def := testDefinition()
			def.Nodes = def.Nodes[:1]
			var calls atomic.Int32
			e := runEngine(t, store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
				if calls.Add(1) == 1 {
					return Result{Status: Failed, Failure: &Failure{Code: "temporary", Message: "try later", Retryable: transient}}, nil
				}
				return Result{Status: Completed, Output: "recovered"}, nil
			}))
			run, _, err := e.Create(context.Background(), newTestRun(t, def))
			if err != nil {
				t.Fatal(err)
			}
			done := waitRun(t, store, run.ID, func(r Run) bool { return Terminal(r.Status) })
			if transient {
				if done.Status != Completed || calls.Load() != 2 || len(done.Nodes[0].Attempts) != 2 {
					t.Fatal(done)
				}
			} else if done.Status != Failed || calls.Load() != 1 {
				t.Fatal(done)
			}
		})
	}
}
func TestEngineCancellationStopsChildrenAndIsIdempotent(t *testing.T) {
	store := newMemoryStore()
	started := make(chan struct{})
	var calls atomic.Int32
	e := runEngine(t, store, ExecutorFunc(func(ctx context.Context, _ Input, _ Sink) (Result, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	}))
	run, _, err := e.Create(context.Background(), newTestRun(t, testDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	if _, err = e.Cancel(context.Background(), testScope, run.ID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, run.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != Cancelled || calls.Load() != 1 {
		t.Fatal(done)
	}
	again, err := e.Cancel(context.Background(), testScope, run.ID)
	if err != nil || again.Status != Cancelled {
		t.Fatal(again, err)
	}
}
func TestEnginePanicAndBudgetNeverMarkSuccess(t *testing.T) {
	for _, budget := range []bool{false, true} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			store := newMemoryStore()
			def := testDefinition()
			if budget {
				def.Limits.MaxTotalTokens = 1
			}
			var calls atomic.Int32
			e := runEngine(t, store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) { calls.Add(1); panic("must not crash scheduler") }))
			r, _, err := e.Create(context.Background(), newTestRun(t, def))
			if err != nil {
				t.Fatal(err)
			}
			done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
			if done.Status != Failed {
				t.Fatal(done)
			}
			if budget && calls.Load() != 0 {
				t.Fatal("model invoked over budget")
			}
		})
	}
}
func TestEngineRunTimeout(t *testing.T) {
	store := newMemoryStore()
	def := testDefinition()
	def.Limits.RunTimeoutSeconds = 1
	e := runEngine(t, store, ExecutorFunc(func(ctx context.Context, _ Input, _ Sink) (Result, error) { <-ctx.Done(); return Result{}, ctx.Err() }))
	r, _, err := e.Create(context.Background(), newTestRun(t, def))
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != TimedOut {
		t.Fatal(done.Status)
	}
}
func TestEngineRecoversOnlyInterruptedUnexpiredAttempts(t *testing.T) {
	store := newMemoryStore()
	e := NewEngine(store, ExecutorFunc(func(_ context.Context, in Input, _ Sink) (Result, error) {
		if in.NodeID == "plan" {
			t.Error("completed predecessor replayed")
		}
		return Result{Status: Completed, Output: "remaining " + in.NodeID}, nil
	}), EngineOptions{PollInterval: time.Millisecond})
	r, _, err := e.Create(context.Background(), newTestRun(t, testDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	r.Status = Running
	past := time.Now().Add(-time.Second)
	r.LeaseUntil = &past
	r.LeaseOwner = "lost-process"
	r.Epoch = 2
	r.Nodes[0].Status = Completed
	r.Nodes[0].Output = "preserved"
	r.Nodes[0].ArtifactID = "old-artifact"
	r.Artifacts = []Artifact{{ID: "old-artifact", NodeID: "plan", Kind: "text", Content: "preserved"}}
	r.Nodes[1].Status = Running
	r.Nodes[1].Attempts = []Attempt{{Number: 1, Epoch: 2, Status: Running, StartedAt: past}}
	r, err = store.Save(context.Background(), r.Revision, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Close(context.Background())
	lost := waitRun(t, store, r.ID, func(r Run) bool { return r.Status == Interrupted })
	if lost.Nodes[0].Status != Completed {
		t.Fatal(lost)
	}
	if _, err = e.Recover(context.Background(), testScope, r.ID, lost.Revision); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != Completed || len(done.Nodes[1].Attempts) != 2 {
		t.Fatal(done)
	}
}
func TestEngineIdempotencyAndScope(t *testing.T) {
	store := newMemoryStore()
	e := NewEngine(store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) { return Result{}, nil }), EngineOptions{})
	input := newTestRun(t, testDefinition())
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, err := e.Create(context.Background(), input)
			if err != nil {
				t.Error(err)
			}
			ids <- r.ID
		}()
	}
	wg.Wait()
	close(ids)
	var id string
	for value := range ids {
		if id != "" && id != value {
			t.Fatal("duplicate Run")
		}
		id = value
	}
	input.Fingerprint = "different"
	if _, _, err := e.Create(context.Background(), input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if _, err := e.Cancel(context.Background(), Scope{TenantID: 1, UserID: 3}, id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func TestEnginePersistenceFailureCannotComplete(t *testing.T) {
	store := newMemoryStore()
	e := NewEngine(store, nil, EngineOptions{})
	r, _, err := e.Create(context.Background(), newTestRun(t, testDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	r.Status = Running
	r.Nodes[0].Status = Running
	r.Nodes[0].Attempts = []Attempt{{Number: 1, Epoch: r.Epoch, Status: Running}}
	r, err = store.Save(context.Background(), r.Revision, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	store.failTerminal = true
	_, err = e.commitOutcome(context.Background(), r, executionOutcome{nodeID: "plan", attempt: 1, epoch: r.Epoch, result: Result{Status: Completed, Output: "must not appear"}})
	if err == nil {
		t.Fatal("ignored persistence failure")
	}
	stored, _ := store.Get(context.Background(), testScope, r.ID)
	if stored.Nodes[0].Status == Completed || len(stored.Artifacts) > 0 {
		t.Fatal(stored)
	}
}
func TestReadyBackoffUsesInjectedTime(t *testing.T) {
	r := newTestRun(t, testDefinition())
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	later := now.Add(time.Second)
	r.Nodes = []NodeRun{{NodeID: "plan", Status: RetryWait, NextRetryAt: &later}, {NodeID: "work", Status: Pending}, {NodeID: "review", Status: Pending}}
	if _, ok := nextReady(r, now); ok {
		t.Fatal("retry ran early")
	}
	if n, ok := nextReady(r, later); !ok || n.ID != "plan" {
		t.Fatal(n, ok)
	}
}
