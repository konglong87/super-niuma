package workflow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type engineHookStore struct {
	Store
	get  func(context.Context, Scope, string) (Run, error)
	save func(context.Context, uint64, Run, []Event) (Run, error)
}

func (s engineHookStore) Get(ctx context.Context, scope Scope, id string) (Run, error) {
	if s.get != nil {
		return s.get(ctx, scope, id)
	}
	return s.Store.Get(ctx, scope, id)
}
func (s engineHookStore) Save(ctx context.Context, rev uint64, r Run, events []Event) (Run, error) {
	if s.save != nil {
		return s.save(ctx, rev, r, events)
	}
	return s.Store.Save(ctx, rev, r, events)
}

type engineBarrier struct {
	ch   chan struct{}
	once sync.Once
}

func newEngineBarrier(t *testing.T) *engineBarrier {
	t.Helper()
	b := &engineBarrier{ch: make(chan struct{})}
	t.Cleanup(b.release)
	return b
}
func (b *engineBarrier) release() { b.once.Do(func() { close(b.ch) }) }
func awaitEngineSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(4 * time.Second):
		t.Fatal("engine barrier was not reached")
	}
}
func singleStepDefinition() Definition {
	def := testDefinition()
	def.Nodes = def.Nodes[:1]
	return def
}
func hasEngineEvent(events []Event, typ EventType) bool {
	for _, ev := range events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}
func closeEngine(t *testing.T, e *Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := e.Close(ctx); err != nil {
		t.Error(err)
	}
}

func TestEngineOldCleanupCannotAdoptRecoveredEpoch(t *testing.T) {
	store := newMemoryStore()
	cleanupRead, releaseRead := newEngineBarrier(t), newEngineBarrier(t)
	oldStarted, oldRelease := newEngineBarrier(t), newEngineBarrier(t)
	newStarted, newRelease := newEngineBarrier(t), newEngineBarrier(t)
	var arm atomic.Bool
	oldStore := engineHookStore{Store: store, get: func(ctx context.Context, scope Scope, id string) (Run, error) {
		deadline, bounded := ctx.Deadline()
		if bounded && time.Until(deadline) <= cleanupGrace && arm.CompareAndSwap(true, false) {
			cleanupRead.release()
			<-releaseRead.ch
		}
		return store.Get(ctx, scope, id)
	}}
	parent, stopOld := context.WithCancel(context.Background())
	defer stopOld()
	old := NewEngine(oldStore, ExecutorFunc(func(ctx context.Context, _ Input, _ Sink) (Result, error) {
		oldStarted.release()
		<-ctx.Done()
		<-oldRelease.ch
		return Result{Status: Completed, Output: "stale output", Usage: Usage{InputTokens: 9}}, nil
	}), EngineOptions{PollInterval: time.Millisecond})
	if err := old.Start(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { oldRelease.release(); releaseRead.release(); closeEngine(t, old) })
	r, _, err := old.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, oldStarted.ch)
	arm.Store(true)
	stopOld()
	oldRelease.release()
	awaitEngineSignal(t, cleanupRead.ch)

	// Model a lease takeover while the old worker's stop read is delayed. Its
	// later read must not become the authority used to finalize this new owner.
	prior, _ := store.Get(context.Background(), testScope, r.ID)
	past := time.Now().Add(-time.Second)
	prior.LeaseUntil = &past
	if _, err = store.Save(context.Background(), prior.Revision, prior, nil); err != nil {
		t.Fatal(err)
	}
	fresh := NewEngine(store, ExecutorFunc(func(_ context.Context, _ Input, _ Sink) (Result, error) {
		newStarted.release()
		<-newRelease.ch
		return Result{Status: Completed, Output: "fresh output", Usage: Usage{InputTokens: 3, OutputTokens: 2}}, nil
	}), EngineOptions{PollInterval: time.Millisecond})
	t.Cleanup(func() { newRelease.release(); closeEngine(t, fresh) })
	fresh.reclaim(context.Background(), prior)
	lost, _ := store.Get(context.Background(), testScope, r.ID)
	if lost.Epoch <= prior.Epoch {
		t.Fatal("lease reclaim did not fence the old epoch")
	}
	if _, err = fresh.Recover(context.Background(), testScope, r.ID, lost.Revision); err != nil {
		t.Fatal(err)
	}
	if err = fresh.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, newStarted.ch)
	current, _ := store.Get(context.Background(), testScope, r.ID)
	releaseRead.release()
	closeEngine(t, old)
	after, _ := store.Get(context.Background(), testScope, r.ID)
	if after.Epoch != current.Epoch || after.LeaseOwner != current.LeaseOwner || after.Status != Running || len(after.Artifacts) != 0 || after.Usage.Total() != 0 {
		t.Fatalf("old cleanup crossed ownership: before=%+v after=%+v", current, after)
	}
	newRelease.release()
	done := waitRun(t, store, r.ID, func(r Run) bool { return r.Status == Completed })
	if len(done.Artifacts) != 1 || done.Artifacts[0].Content != "fresh output" || done.Usage.Total() != 5 {
		t.Fatalf("stale outcome published: %+v", done)
	}
}

func TestEngineOwnerFenceRejectsStaleEventsAndOutcomes(t *testing.T) {
	store := newMemoryStore()
	e := NewEngine(store, nil, EngineOptions{})
	r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	r.Status, r.LeaseOwner = Running, e.owner
	r.Nodes[0].Status = Running
	r.Nodes[0].Attempts = []Attempt{{Number: 1, Epoch: r.Epoch, Status: Running}}
	r, _ = store.Save(context.Background(), r.Revision, r, nil)
	other := cloneRun(r)
	other.LeaseOwner = "another-owner"
	other, _ = store.Save(context.Background(), other.Revision, other, nil)
	if _, err = e.appendEvents(context.Background(), r, []Event{{Type: EventDelta, Text: "late"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale events accepted: %v", err)
	}
	if _, err = e.commitOutcome(context.Background(), r, executionOutcome{nodeID: "plan", attempt: 1, epoch: r.Epoch, result: Result{Status: Completed, Output: "late", Usage: Usage{InputTokens: 9}}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale outcome accepted: %v", err)
	}
	if _, err = e.finishStopped(context.Background(), r, Cancelled, "cancelled", "late"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale terminal write accepted: %v", err)
	}
	after, _ := store.Get(context.Background(), testScope, r.ID)
	if after.Revision != other.Revision || after.Usage.Total() != 0 || after.Status != Running {
		t.Fatal(after)
	}
}

func TestEngineStopSettlesUsageAndRecoveryRetainsUnknownReservation(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "known", false: "unknown"}[known], func(t *testing.T) {
			store := newMemoryStore()
			started := newEngineBarrier(t)
			parent, stop := context.WithCancel(context.Background())
			defer stop()
			usage := Usage{}
			budget := 700
			if known {
				usage = Usage{InputTokens: 1700, OutputTokens: 100}
				budget = 2000
			}
			e := NewEngine(store, ExecutorFunc(func(ctx context.Context, _ Input, _ Sink) (Result, error) {
				started.release()
				<-ctx.Done()
				return Result{Status: Completed, Output: "do not publish", Usage: usage, UsageKnown: known}, ctx.Err()
			}), EngineOptions{PollInterval: time.Millisecond})
			if err := e.Start(parent); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stop(); closeEngine(t, e) })
			def := singleStepDefinition()
			def.Limits.MaxTotalTokens = budget
			r, _, err := e.Create(context.Background(), newTestRun(t, def))
			if err != nil {
				t.Fatal(err)
			}
			awaitEngineSignal(t, started.ch)
			stop()
			closeEngine(t, e)
			stopped, _ := store.Get(context.Background(), testScope, r.ID)
			if stopped.Status != Interrupted || stopped.Usage != usage || stopped.Nodes[0].Attempts[0].Usage != usage || len(stopped.Artifacts) != 0 || stopped.Nodes[0].Output != "" {
				t.Fatalf("stop lost usage or published output: %+v", stopped)
			}
			if !known && reservedTokens(stopped) == 0 {
				t.Fatal("unknown usage was treated as a free request")
			}
			var calls atomic.Int32
			fresh := NewEngine(store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
				calls.Add(1)
				return Result{Status: Completed, Output: "must not run"}, nil
			}), EngineOptions{PollInterval: time.Millisecond})
			t.Cleanup(func() { closeEngine(t, fresh) })
			if _, err = fresh.Recover(context.Background(), testScope, r.ID, stopped.Revision); err != nil {
				t.Fatal(err)
			}
			if err = fresh.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
			if done.Status != Failed || calls.Load() != 0 || done.Nodes[0].Failure.Code != "token_budget_exceeded" || done.Usage != usage {
				t.Fatalf("recovery refunded consumed/unknown tokens: %+v calls=%d", done, calls.Load())
			}
		})
	}
}

func TestEngineFailedSiblingDrainKeepsUsageWithoutArtifact(t *testing.T) {
	store := newMemoryStore()
	both := newEngineBarrier(t)
	var started atomic.Int32
	def := singleStepDefinition()
	def.Nodes = []Node{{ID: "left", AgentRef: "a"}, {ID: "right", AgentRef: "a"}}
	e := runEngine(t, store, ExecutorFunc(func(ctx context.Context, in Input, _ Sink) (Result, error) {
		if started.Add(1) == 2 {
			both.release()
		}
		<-both.ch
		if in.NodeID == "left" {
			return Result{Status: Failed, Usage: Usage{InputTokens: 11}, Failure: &Failure{Code: "required_failure", Message: "failed"}}, nil
		}
		<-ctx.Done()
		return Result{Status: Completed, Output: "cancelled sibling", Usage: Usage{InputTokens: 17}}, ctx.Err()
	}))
	r, _, err := e.Create(context.Background(), newTestRun(t, def))
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != Failed || done.Usage.InputTokens != 28 || len(done.Artifacts) != 0 {
		t.Fatalf("sibling drain discarded usage: %+v", done)
	}
}

func TestEnginePersistenceErrorUsesBoundedCleanupAndCloseWaitsForExecutor(t *testing.T) {
	store := newMemoryStore()
	cancelled, release := newEngineBarrier(t), newEngineBarrier(t)
	var failed atomic.Bool
	hooks := engineHookStore{Store: store, save: func(ctx context.Context, rev uint64, r Run, ev []Event) (Run, error) {
		if hasEngineEvent(ev, EventPhase) && failed.CompareAndSwap(false, true) {
			return Run{}, errors.New("injected progress database failure")
		}
		return store.Save(ctx, rev, r, ev)
	}}
	e := NewEngine(hooks, ExecutorFunc(func(ctx context.Context, _ Input, sink Sink) (Result, error) {
		sink(Event{Type: EventPhase, Text: "force a progress checkpoint"})
		<-ctx.Done()
		cancelled.release()
		<-release.ch
		return Result{Status: Completed, Output: "orphan output", Usage: Usage{InputTokens: 19}}, ctx.Err()
	}), EngineOptions{Parallel: 1, PollInterval: time.Millisecond})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { release.release(); closeEngine(t, e) })
	r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, cancelled.ch)
	stopped := waitRun(t, store, r.ID, func(r Run) bool { return r.Status == Interrupted })
	if stopped.Failure.Code != "cleanup_timeout" || len(e.slots) != 1 || reservedTokens(stopped) == 0 {
		t.Fatalf("DB-error exit bypassed cleanup: %+v slots=%d", stopped, len(e.slots))
	}
	if _, err = e.Recover(context.Background(), testScope, r.ID, stopped.Revision); err == nil {
		t.Fatal("recovered while an orphan executor was alive")
	}
	assertEngineCloseDeadline(t, e)
	release.release()
	closeEngine(t, e)
	after, _ := store.Get(context.Background(), testScope, r.ID)
	if after.Status != Interrupted || len(after.Artifacts) != 0 || after.Usage.Total() != 0 || reservedTokens(after) == 0 || len(e.slots) != 0 {
		t.Fatalf("late orphan outcome changed its terminal checkpoint: %+v", after)
	}
}

func assertEngineCloseDeadline(t *testing.T, e *Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close reported executor cleanup while still in flight: %v", err)
	}
}

func TestEngineNodeWatchdogStopsContextIgnoringExecutorWithoutRetry(t *testing.T) {
	store := newMemoryStore()
	started, cancelled, release := newEngineBarrier(t), newEngineBarrier(t), newEngineBarrier(t)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	var calls atomic.Int32
	e := NewEngine(store, ExecutorFunc(func(ctx context.Context, _ Input, _ Sink) (Result, error) {
		calls.Add(1)
		started.release()
		<-ctx.Done()
		cancelled.release()
		<-release.ch
		return Result{Status: Completed, Output: "late", Usage: Usage{InputTokens: 13}}, nil
	}), EngineOptions{Parallel: 1, PollInterval: time.Millisecond, Now: func() time.Time { return time.Unix(0, now.Load()) }})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { release.release(); closeEngine(t, e) })
	def := singleStepDefinition()
	def.Nodes[0].TimeoutSeconds = 1
	r, _, err := e.Create(context.Background(), newTestRun(t, def))
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, started.ch)
	// Advance the scheduler clock only after Execute is entered. No sleep is
	// used to guess the node deadline or the executor's cancellation timing.
	now.Add(int64(2 * time.Second))
	e.Wake()
	awaitEngineSignal(t, cancelled.ch)
	stopped := waitRun(t, store, r.ID, func(r Run) bool { return r.Status == Interrupted })
	if stopped.Failure.Code != "cleanup_timeout" || calls.Load() != 1 || len(stopped.Nodes[0].Attempts) != 1 || len(e.slots) != 1 {
		t.Fatalf("watchdog retried/released an unacknowledged request: %+v calls=%d", stopped, calls.Load())
	}
	if _, err = e.Recover(context.Background(), testScope, r.ID, stopped.Revision); err == nil {
		t.Fatal("recovery allowed before actual Executor exit")
	}
	assertEngineCloseDeadline(t, e)
	release.release()
	closeEngine(t, e)
	after, _ := store.Get(context.Background(), testScope, r.ID)
	if len(after.Artifacts) != 0 || after.Status != Interrupted || after.Usage.Total() != 0 || reservedTokens(after) == 0 {
		t.Fatal(after)
	}
	fresh := NewEngine(store, nil, EngineOptions{})
	if _, err = fresh.Recover(context.Background(), testScope, r.ID, after.Revision); err != nil {
		t.Fatalf("recovery still blocked after executor exit: %v", err)
	}
}

func TestEngineCompletionDBFailureIsNotCancellationOrFakeCheckpoint(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop_checkpoint_available", true: "database_unavailable"}[unavailable], func(t *testing.T) {
			store := newMemoryStore()
			stopAttempted := newEngineBarrier(t)
			hooks := engineHookStore{Store: store, save: func(ctx context.Context, rev uint64, r Run, events []Event) (Run, error) {
				if hasEngineEvent(events, EventCompleted) {
					return Run{}, errors.New("completion database failure")
				}
				if hasEngineEvent(events, EventInterrupted) {
					stopAttempted.release()
					if unavailable {
						return Run{}, errors.New("database remains unavailable")
					}
				}
				return store.Save(ctx, rev, r, events)
			}}
			e := NewEngine(hooks, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
				return Result{Status: Completed, Output: "node result", Usage: Usage{InputTokens: 2, OutputTokens: 3}}, nil
			}), EngineOptions{PollInterval: time.Millisecond})
			if err := e.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				_ = e.Close(ctx) // The unavailable-DB arm deliberately retains a shutdown error.
			})
			r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
			if err != nil {
				t.Fatal(err)
			}
			awaitEngineSignal(t, stopAttempted.ch)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			closeErr := e.Close(ctx)
			if unavailable && !errors.Is(closeErr, errStopCheckpoint) || !unavailable && closeErr != nil {
				t.Fatalf("Close misreported checkpoint durability: %v", closeErr)
			}
			after, _ := store.Get(context.Background(), testScope, r.ID)
			expected := Interrupted
			if unavailable {
				expected = Running
			}
			if after.Status != expected || after.CancelRequestedAt != nil || after.Usage.Total() != 5 {
				t.Fatalf("fabricated terminal checkpoint/cancellation: %+v", after)
			}
			events, _ := store.Events(context.Background(), testScope, r.ID, 0, 200)
			if hasEngineEvent(events, EventCancelled) || hasEngineEvent(events, EventCompleted) || unavailable && hasEngineEvent(events, EventInterrupted) {
				t.Fatalf("uncommitted final event published: %+v", events)
			}
		})
	}
}

func TestEngineCancelWinsCompletionCASWithoutFabricatingDBCancel(t *testing.T) {
	store := newMemoryStore()
	completion, release := newEngineBarrier(t), newEngineBarrier(t)
	var once atomic.Bool
	hooks := engineHookStore{Store: store, save: func(ctx context.Context, rev uint64, r Run, events []Event) (Run, error) {
		if hasEngineEvent(events, EventCompleted) && once.CompareAndSwap(false, true) {
			completion.release()
			<-release.ch
		}
		return store.Save(ctx, rev, r, events)
	}}
	e := NewEngine(hooks, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
		return Result{Status: Completed, Output: "node result", Usage: Usage{InputTokens: 1}}, nil
	}), EngineOptions{PollInterval: time.Millisecond})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { release.release(); closeEngine(t, e) })
	r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, completion.ch)
	if _, err = e.Cancel(context.Background(), testScope, r.ID); err != nil {
		t.Fatal(err)
	}
	release.release()
	done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != Cancelled {
		t.Fatal(done)
	}
	events, _ := store.Events(context.Background(), testScope, r.ID, 0, 200)
	if hasEngineEvent(events, EventCompleted) || !hasEngineEvent(events, EventCancelled) {
		t.Fatal(events)
	}
}

func TestEngineFailedOutcomeSaveStillSettlesPendingUsage(t *testing.T) {
	store := newMemoryStore()
	var rejected, stopConflict atomic.Bool
	hooks := engineHookStore{Store: store, save: func(ctx context.Context, rev uint64, r Run, events []Event) (Run, error) {
		if hasEngineEvent(events, EventAgentCompleted) && rejected.CompareAndSwap(false, true) {
			return Run{}, errors.New("outcome checkpoint unavailable")
		}
		if hasEngineEvent(events, EventInterrupted) && stopConflict.CompareAndSwap(false, true) {
			return Run{}, ErrConflict
		}
		return store.Save(ctx, rev, r, events)
	}}
	e := NewEngine(hooks, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
		return Result{Status: Completed, Output: "uncommitted output", Usage: Usage{InputTokens: 20, OutputTokens: 3}, UsageKnown: true}, nil
	}), EngineOptions{PollInterval: time.Millisecond})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeEngine(t, e) })
	r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	stopped := waitRun(t, store, r.ID, func(r Run) bool { return r.Status == Interrupted })
	if stopped.Failure.Code != "checkpoint_failed" || stopped.Usage.Total() != 23 || stopped.Nodes[0].Attempts[0].Usage.Total() != 23 || len(stopped.Artifacts) != 0 || UncertainTokens(stopped) != 0 {
		t.Fatalf("unacknowledged outcome lost real usage or published output: %+v", stopped)
	}
}

func TestEngineUncertainReservationSurvivesSerializationWithoutFakeUsage(t *testing.T) {
	r := newTestRun(t, singleStepDefinition())
	r.Nodes = []NodeRun{{NodeID: "plan", Status: Interrupted, Attempts: []Attempt{
		{Number: 1, Status: Failed, Usage: Usage{InputTokens: 4, OutputTokens: 3}, UsageKnown: true},
		{Number: 2, Status: Interrupted},
	}}}
	r.Usage = Usage{InputTokens: 4, OutputTokens: 3}
	roundTrip := cloneRun(r)
	def, _ := r.Plan.Node("plan")
	agent, _ := r.Plan.Agent(def.AgentRef)
	want := inputEstimate(r, def, agent, nil) + agent.MaxOutputTokens
	if UncertainTokens(roundTrip) != want || reservedTokens(roundTrip) != want || roundTrip.Usage.Total() != 7 || roundTrip.Nodes[0].Attempts[1].Usage.Total() != 0 {
		t.Fatalf("unknown usage was refunded or fabricated: %+v uncertain=%d", roundTrip, UncertainTokens(roundTrip))
	}
	roundTrip.Nodes[0].Attempts[1].Status = Running
	if UncertainTokens(roundTrip) != want || reservedTokens(roundTrip) != want {
		t.Fatal("active reservation was lost or double counted")
	}
}

func TestEngineRecoveryWaitsForAcknowledgedStopAfterExecutorReturns(t *testing.T) {
	store := newMemoryStore()
	started, checkpointSaved, releaseStop := newEngineBarrier(t), newEngineBarrier(t), newEngineBarrier(t)
	hooks := engineHookStore{Store: store, save: func(ctx context.Context, rev uint64, r Run, events []Event) (Run, error) {
		saved, err := store.Save(ctx, rev, r, events)
		if err == nil && hasEngineEvent(events, EventInterrupted) {
			checkpointSaved.release()
			<-releaseStop.ch
		}
		return saved, err
	}}
	parent, stop := context.WithCancel(context.Background())
	defer stop()
	e := NewEngine(hooks, ExecutorFunc(func(ctx context.Context, _ Input, _ Sink) (Result, error) {
		started.release()
		<-ctx.Done()
		return Result{Usage: Usage{InputTokens: 11}}, ctx.Err()
	}), EngineOptions{PollInterval: time.Millisecond})
	if err := e.Start(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); releaseStop.release(); closeEngine(t, e) })
	r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, started.ch)
	stop()
	awaitEngineSignal(t, checkpointSaved.ch)
	callsDone := make(chan struct{})
	go func() { e.calls.Wait(); close(callsDone) }()
	awaitEngineSignal(t, callsDone)
	persisted, _ := store.Get(context.Background(), testScope, r.ID)
	if persisted.Status != Interrupted {
		t.Fatal(persisted)
	}
	if _, err = e.Recover(context.Background(), testScope, r.ID, persisted.Revision); err == nil {
		t.Fatal("recovery raced the not-yet-acknowledged stop checkpoint")
	}
	releaseStop.release()
	closeEngine(t, e)
	fresh := NewEngine(store, nil, EngineOptions{})
	if _, err = fresh.Recover(context.Background(), testScope, r.ID, persisted.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestEngineConcurrentProgressSinkIsRaceSafe(t *testing.T) {
	store := newMemoryStore()
	e := runEngine(t, store, ExecutorFunc(func(_ context.Context, _ Input, sink Sink) (Result, error) {
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				for range 20 {
					sink(Event{Type: EventDelta, Text: "safe"})
				}
			})
		}
		workers.Wait()
		return Result{Status: Completed, Output: "finished", Usage: Usage{InputTokens: 1}}, nil
	}))
	r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, store, r.ID, func(r Run) bool { return r.Status == Completed })
	events, _ := store.Events(context.Background(), testScope, r.ID, 0, 200)
	deltas := 0
	for _, ev := range events {
		if ev.Type == EventDelta {
			deltas++
		}
	}
	if deltas != 160 {
		t.Fatalf("lost concurrent progress: %d", deltas)
	}
}

func TestEnginePartialUsageKeepsUnreportedComponentReservation(t *testing.T) {
	r := newTestRun(t, singleStepDefinition())
	def, _ := r.Plan.Node("plan")
	agent, _ := r.Plan.Agent(def.AgentRef)
	reserved := inputEstimate(r, def, agent, nil) + agent.MaxOutputTokens
	for _, tc := range []struct {
		name      string
		usage     Usage
		known     bool
		uncertain int
	}{
		{"unknown", Usage{}, false, reserved},
		{"input_only", Usage{InputTokens: 7}, false, reserved - 7},
		{"cache_input_only", Usage{CacheReadTokens: 7}, false, reserved - 7},
		{"output_only", Usage{OutputTokens: 3}, false, reserved - 3},
		{"nonzero_unconfirmed", Usage{InputTokens: 7, OutputTokens: 3}, false, reserved - 10},
		{"confirmed", Usage{InputTokens: 7, OutputTokens: 3}, true, 0},
		{"proven_zero", Usage{}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := cloneRun(r)
			candidate.Nodes = []NodeRun{{NodeID: "plan", Status: Interrupted, Attempts: []Attempt{{Number: 1, Status: Interrupted, Usage: tc.usage, UsageKnown: tc.known, BudgetReserved: reserved}}}}
			candidate.Usage = tc.usage
			candidate.UncertainTokens = UncertainTokens(candidate)
			candidate = cloneRun(candidate)
			if UncertainTokens(candidate) != tc.uncertain || candidate.UncertainTokens != tc.uncertain || candidate.Usage != tc.usage || candidate.Nodes[0].Attempts[0].Usage != tc.usage {
				t.Fatalf("partial actual usage was fabricated/refunded: %+v uncertain=%d want=%d", candidate, UncertainTokens(candidate), tc.uncertain)
			}
		})
	}
}

func TestEngineRetryCannotRefundUnknownOutputUsage(t *testing.T) {
	store := newMemoryStore()
	var calls atomic.Int32
	e := runEngine(t, store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
		calls.Add(1)
		return Result{Status: Failed, Usage: Usage{InputTokens: 7}, Failure: &Failure{Code: "partial_transient", Message: "output billing unknown", Retryable: true}}, nil
	}))
	def := singleStepDefinition()
	def.Limits.MaxTotalTokens = 700
	r, _, err := e.Create(context.Background(), newTestRun(t, def))
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
	if calls.Load() != 1 || done.Usage.InputTokens != 7 || done.Nodes[0].Failure.Code != "token_budget_exceeded" || UncertainTokens(done) != done.Nodes[0].Attempts[0].BudgetReserved-7 || done.UncertainTokens != UncertainTokens(done) {
		t.Fatalf("retry refunded unreported output usage: %+v calls=%d uncertain=%d", done, calls.Load(), UncertainTokens(done))
	}
}

func TestEngineNodeWatchdogCancelsBlockedStoreOperation(t *testing.T) {
	store := newMemoryStore()
	started, readBlocked, cancelled := newEngineBarrier(t), newEngineBarrier(t), newEngineBarrier(t)
	var arm atomic.Bool
	hooks := engineHookStore{Store: store, get: func(ctx context.Context, scope Scope, id string) (Run, error) {
		if arm.CompareAndSwap(true, false) {
			readBlocked.release()
			<-ctx.Done() // Simulate a context-aware database stalled until cancellation.
			return Run{}, ctx.Err()
		}
		return store.Get(ctx, scope, id)
	}}
	e := NewEngine(hooks, ExecutorFunc(func(ctx context.Context, _ Input, _ Sink) (Result, error) {
		arm.Store(true)
		started.release()
		<-ctx.Done()
		cancelled.release()
		return Result{Usage: Usage{InputTokens: 5, OutputTokens: 2}}, ctx.Err()
	}), EngineOptions{PollInterval: time.Millisecond})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeEngine(t, e) })
	def := singleStepDefinition()
	def.Nodes[0].TimeoutSeconds = 1
	r, _, err := e.Create(context.Background(), newTestRun(t, def))
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, started.ch)
	awaitEngineSignal(t, readBlocked.ch)
	awaitEngineSignal(t, cancelled.ch)
	done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
	if done.Status != TimedOut || done.Failure.Code != "node_timeout" || done.Usage.Total() != 7 || len(done.Artifacts) != 0 {
		t.Fatalf("Store stall hid the independent node deadline: %+v", done)
	}
}

func TestEngineValidTextWithoutUsageCompletesWithPersistedReservation(t *testing.T) {
	store := newMemoryStore()
	e := runEngine(t, store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
		return Result{Status: Completed, Output: "valid text without usage", UsageKnown: false}, nil
	}))
	r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, r.ID, func(r Run) bool { return r.Status == Completed })
	a := done.Nodes[0].Attempts[0]
	if a.UsageKnown || a.Usage.Total() != 0 || done.Usage.Total() != 0 || a.BudgetReserved <= 0 || done.UncertainTokens != a.BudgetReserved || len(done.Artifacts) != 1 {
		t.Fatalf("unknown usage prevented valid text or fabricated actuals: %+v", done)
	}
	roundTrip := cloneRun(done)
	if UncertainTokens(roundTrip) != done.UncertainTokens || roundTrip.Nodes[0].Attempts[0].BudgetReserved != a.BudgetReserved {
		t.Fatal("unknown billing reservation did not survive persistence")
	}
}

func TestEngineUsageKnownFlagControlsRetryReservation(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[known], func(t *testing.T) {
			store := newMemoryStore()
			var calls atomic.Int32
			e := runEngine(t, store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
				if calls.Add(1) == 1 {
					return Result{Status: Failed, Usage: Usage{InputTokens: 10, OutputTokens: 10}, UsageKnown: known, Failure: &Failure{Code: "temporary", Message: "retry", Retryable: true}}, nil
				}
				return Result{Status: Completed, Output: "retry completed", Usage: Usage{InputTokens: 4, OutputTokens: 3}, UsageKnown: true}, nil
			}))
			def := singleStepDefinition()
			def.Limits.MaxTotalTokens = 700
			r, _, err := e.Create(context.Background(), newTestRun(t, def))
			if err != nil {
				t.Fatal(err)
			}
			done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
			if known {
				if done.Status != Completed || calls.Load() != 2 || done.Usage.Total() != 27 || done.UncertainTokens != 0 {
					t.Fatalf("confirmed usage was not settled: %+v calls=%d", done, calls.Load())
				}
			} else if done.Status != Failed || calls.Load() != 1 || done.Usage.Total() != 20 || done.UncertainTokens != done.Nodes[0].Attempts[0].BudgetReserved-20 {
				t.Fatalf("positive but unconfirmed usage refunded its reservation: %+v calls=%d", done, calls.Load())
			}
		})
	}
}

func TestEngineRejectsNegativeAndOverflowUsageWithoutSuccessfulArtifact(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, usage := range []Usage{
		{InputTokens: -1, OutputTokens: 2},
		{InputTokens: maxInt, OutputTokens: 1},
	} {
		store := newMemoryStore()
		e := runEngine(t, store, ExecutorFunc(func(context.Context, Input, Sink) (Result, error) {
			return Result{Status: Completed, Output: "invalid counters", Usage: usage, UsageKnown: true}, nil
		}))
		r, _, err := e.Create(context.Background(), newTestRun(t, singleStepDefinition()))
		if err != nil {
			t.Fatal(err)
		}
		done := waitRun(t, store, r.ID, func(r Run) bool { return Terminal(r.Status) })
		if done.Status != Failed || done.Nodes[0].Failure.Code != "invalid_usage" || len(done.Artifacts) != 0 || done.Usage.Total() != 0 || done.UncertainTokens <= 0 || done.Nodes[0].Attempts[0].UsageKnown {
			t.Fatalf("invalid usage was trusted: %+v", done)
		}
	}
	// A representable but over-budget actual value must not wrap the admission
	// sum when outstanding reservations are added.
	r := newTestRun(t, singleStepDefinition())
	r.Usage.InputTokens = maxInt
	if budgetAllows(r, 1) {
		t.Fatal("overflow turned exceeded actual usage into available budget")
	}
}
