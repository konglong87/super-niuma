package computeruse

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type runnerTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newRunnerTestClock() *runnerTestClock {
	return &runnerTestClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *runnerTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *runnerTestClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

func newTestRunner(t *testing.T, budget RunBudget, clock *runnerTestClock) *Runner {
	t.Helper()
	runner, err := NewRunnerWithClock(budget, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func transition(t *testing.T, runner *Runner, from, to RunState) {
	t.Helper()
	if err := runner.Transition(Transition{From: from, To: to}); err != nil {
		t.Fatalf("transition %s -> %s: %v", from, to, err)
	}
}

func consume(t *testing.T, runner *Runner, kind BudgetKind, duration time.Duration) {
	t.Helper()
	if err := runner.Consume(Consumption{Kind: kind, Duration: duration}); err != nil {
		t.Fatalf("consume %s: %v", kind, err)
	}
}

func TestRunBudgetValidation(t *testing.T) {
	for name, budget := range map[string]RunBudget{
		"negative total": {TotalDuration: -time.Second},
		"negative phase": {LaunchDuration: -time.Second},
		"negative count": {MaxInputActions: -1},
		"unknown replay": {MaxUnknownReplays: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := budget.Validate(); !errors.Is(err, ErrInvalidRunBudget) {
				t.Fatalf("Validate() error = %v, want ErrInvalidRunBudget", err)
			}
		})
	}

	clock := newRunnerTestClock()
	if _, err := NewRunnerWithClock(RunBudget{}, nil); !errors.Is(err, ErrInvalidRunBudget) {
		t.Fatalf("nil clock error = %v, want ErrInvalidRunBudget", err)
	}
	if _, err := NewRunnerWithClock(RunBudget{}, clock.Now); err != nil {
		t.Fatalf("zero budget should be valid: %v", err)
	}
}

func TestRunnerHappyPathAndUsage(t *testing.T) {
	clock := newRunnerTestClock()
	runner := newTestRunner(t, RunBudget{
		TotalDuration:     104 * time.Second,
		LaunchDuration:    8 * time.Second,
		BindDuration:      5 * time.Second,
		ObserveDuration:   3 * time.Second,
		MaxInputActions:   6,
		MaxWaitActions:    1,
		MaxModelTurns:     8,
		MaxUnknownReplays: 0,
	}, clock)

	consume(t, runner, BudgetLaunch, 2*time.Second)
	consume(t, runner, BudgetBind, 1*time.Second)
	transition(t, runner, RunStateCreated, RunStateBound)
	consume(t, runner, BudgetObserve, 500*time.Millisecond)
	transition(t, runner, RunStateBound, RunStateObserved)
	consume(t, runner, BudgetModelTurn, 0)
	consume(t, runner, BudgetInput, 0)
	transition(t, runner, RunStateObserved, RunStateExecuting)
	transition(t, runner, RunStateExecuting, RunStateVerifying)
	consume(t, runner, BudgetWait, 0)
	consume(t, runner, BudgetObserve, 750*time.Millisecond)
	transition(t, runner, RunStateVerifying, RunStateObserved)
	transition(t, runner, RunStateObserved, RunStateCompleted)

	snapshot := runner.Snapshot()
	if snapshot.State != RunStateCompleted {
		t.Fatalf("state = %s, want completed", snapshot.State)
	}
	if snapshot.EndedAt.IsZero() || snapshot.Usage.Elapsed < 0 {
		t.Fatalf("terminal timing = %+v", snapshot)
	}
	if snapshot.Usage.LaunchDuration != 2*time.Second || snapshot.Usage.BindDuration != time.Second || snapshot.Usage.ObserveDuration != 1250*time.Millisecond {
		t.Fatalf("durations = %+v", snapshot.Usage)
	}
	if snapshot.Usage.InputActions != 1 || snapshot.Usage.WaitActions != 1 || snapshot.Usage.ModelTurns != 1 || snapshot.Usage.UnknownReplays != 0 {
		t.Fatalf("counts = %+v", snapshot.Usage)
	}
}

func TestRunnerAllowsLaunchAfterInitialObservation(t *testing.T) {
	clock := newRunnerTestClock()
	runner := newTestRunner(t, RunBudget{LaunchDuration: time.Second}, clock)
	transition(t, runner, RunStateCreated, RunStateBound)
	transition(t, runner, RunStateBound, RunStateObserved)
	consume(t, runner, BudgetLaunch, 100*time.Millisecond)
	transition(t, runner, RunStateObserved, RunStateBound)
	if runner.State() != RunStateBound {
		t.Fatalf("state=%s, want bound", runner.State())
	}
}

func TestRunnerRejectsInvalidTransitionsAndPhaseOrdering(t *testing.T) {
	clock := newRunnerTestClock()
	runner := newTestRunner(t, RunBudget{}, clock)

	if err := runner.Transition(Transition{From: RunStateCreated, To: RunStateObserved}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid transition error = %v", err)
	}
	if err := runner.Consume(Consumption{Kind: BudgetInput}); !errors.Is(err, ErrInvalidConsumption) {
		t.Fatalf("input before observation error = %v", err)
	}
	if err := runner.Transition(Transition{From: RunStateBound, To: RunStateBound}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("same-state transition error = %v", err)
	}

	transition(t, runner, RunStateCreated, RunStateBound)
	consume(t, runner, BudgetObserve, 0)
	transition(t, runner, RunStateBound, RunStateObserved)
	transition(t, runner, RunStateObserved, RunStateCompleted)
	if err := runner.Consume(Consumption{Kind: BudgetModelTurn}); !errors.Is(err, ErrRunTerminal) {
		t.Fatalf("terminal consume error = %v", err)
	}
	if err := runner.Transition(Transition{To: RunStateStopped}); !errors.Is(err, ErrRunTerminal) {
		t.Fatalf("terminal transition error = %v", err)
	}
}

func TestRunnerEnforcesPhaseBudgetsAndCounts(t *testing.T) {
	clock := newRunnerTestClock()
	runner := newTestRunner(t, RunBudget{
		LaunchDuration:  2 * time.Second,
		BindDuration:    time.Second,
		ObserveDuration: 2 * time.Second,
		MaxInputActions: 2,
		MaxWaitActions:  1,
		MaxModelTurns:   1,
	}, clock)

	consume(t, runner, BudgetLaunch, 2*time.Second)
	if err := runner.Consume(Consumption{Kind: BudgetLaunch, Duration: time.Nanosecond}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("launch overage = %v", err)
	}
	consume(t, runner, BudgetBind, time.Second)
	transition(t, runner, RunStateCreated, RunStateBound)
	consume(t, runner, BudgetObserve, 2*time.Second)
	transition(t, runner, RunStateBound, RunStateObserved)
	consume(t, runner, BudgetInput, 0)
	consume(t, runner, BudgetInput, 0)
	if err := runner.Consume(Consumption{Kind: BudgetInput}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("input overage = %v", err)
	}
	consume(t, runner, BudgetModelTurn, 0)
	if err := runner.Consume(Consumption{Kind: BudgetModelTurn}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("model turn overage = %v", err)
	}
	transition(t, runner, RunStateObserved, RunStateExecuting)
	transition(t, runner, RunStateExecuting, RunStateVerifying)
	consume(t, runner, BudgetWait, 0)
	if err := runner.Consume(Consumption{Kind: BudgetWait}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("wait overage = %v", err)
	}
}

func TestRunnerNeverReplaysUnknownOutcomes(t *testing.T) {
	clock := newRunnerTestClock()
	runner := newTestRunner(t, RunBudget{TotalDuration: time.Second}, clock)

	if err := runner.Consume(Consumption{Kind: BudgetUnknownReplay}); !errors.Is(err, ErrUnknownReplay) {
		t.Fatalf("unknown replay error = %v", err)
	}
	if usage := runner.Snapshot().Usage.UnknownReplays; usage != 0 {
		t.Fatalf("unknown replay usage = %d, want 0", usage)
	}

	clock.Advance(2 * time.Second)
	if err := runner.Consume(Consumption{Kind: BudgetUnknownReplay}); !errors.Is(err, ErrUnknownReplay) {
		t.Fatalf("post-deadline unknown replay error = %v", err)
	}
}

func TestRunnerDeadlineAllowsOnlyFailureOrStopCleanup(t *testing.T) {
	clock := newRunnerTestClock()
	runner := newTestRunner(t, RunBudget{TotalDuration: time.Second}, clock)
	clock.Advance(2 * time.Second)

	if err := runner.Transition(Transition{To: RunStateBound}); !errors.Is(err, ErrRunDeadline) {
		t.Fatalf("deadline transition error = %v", err)
	}
	if err := runner.Transition(Transition{To: RunStateFailed}); err != nil {
		t.Fatalf("failure cleanup transition: %v", err)
	}
	if runner.State() != RunStateFailed {
		t.Fatalf("state = %s, want failed", runner.State())
	}
	if ended := runner.Snapshot().EndedAt; ended.IsZero() {
		t.Fatal("failure transition did not record EndedAt")
	}
}

func TestRunnerIsThreadSafeForConcurrentModelTurnConsumption(t *testing.T) {
	clock := newRunnerTestClock()
	const workers = 20
	const turnsPerWorker = 25
	runner := newTestRunner(t, RunBudget{MaxModelTurns: workers * turnsPerWorker}, clock)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range turnsPerWorker {
				if err := runner.Consume(Consumption{Kind: BudgetModelTurn}); err != nil {
					t.Errorf("concurrent model turn: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := runner.Snapshot().Usage.ModelTurns; got != workers*turnsPerWorker {
		t.Fatalf("model turns = %d, want %d", got, workers*turnsPerWorker)
	}
}
