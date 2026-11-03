package computeruse

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RunState describes the lifecycle of one bounded Computer Use orchestration.
// The runner is deliberately generic: it does not know which application is
// being controlled or what a particular observation means.
type RunState string

const (
	RunStateCreated   RunState = "created"
	RunStateBound     RunState = "bound"
	RunStateObserved  RunState = "observed"
	RunStateExecuting RunState = "executing"
	RunStateVerifying RunState = "verifying"
	RunStateCompleted RunState = "completed"
	RunStateFailed    RunState = "failed"
	RunStateStopped   RunState = "stopped"
)

func (s RunState) valid() bool {
	switch s {
	case RunStateCreated, RunStateBound, RunStateObserved, RunStateExecuting,
		RunStateVerifying, RunStateCompleted, RunStateFailed, RunStateStopped:
		return true
	default:
		return false
	}
}

func (s RunState) terminal() bool {
	return s == RunStateCompleted || s == RunStateFailed || s == RunStateStopped
}

// BudgetKind identifies one independently consumable run budget.
type BudgetKind string

const (
	BudgetLaunch        BudgetKind = "launch"
	BudgetBind          BudgetKind = "bind"
	BudgetObserve       BudgetKind = "observe"
	BudgetInput         BudgetKind = "input"
	BudgetWait          BudgetKind = "wait"
	BudgetModelTurn     BudgetKind = "model_turn"
	BudgetUnknownReplay BudgetKind = "unknown_replay"
)

func (k BudgetKind) valid() bool {
	switch k {
	case BudgetLaunch, BudgetBind, BudgetObserve, BudgetInput, BudgetWait, BudgetModelTurn, BudgetUnknownReplay:
		return true
	default:
		return false
	}
}

func (k BudgetKind) durationBudget() bool {
	return k == BudgetLaunch || k == BudgetBind || k == BudgetObserve
}

// RunBudget contains the hard ceilings for one runner.
//
// A zero duration or zero count means that the corresponding budget is not
// limited, except MaxUnknownReplays: unknown outcomes are never replayable and
// this field must remain zero.
// DefaultRunBudget bounds a normal desktop Computer Use run while leaving
// application semantics to the model/verifier. Hosts can replace it with a
// stricter budget for a particular workflow.
func DefaultRunBudget() RunBudget {
	return RunBudget{
		TotalDuration: 300 * time.Second, LaunchDuration: 10 * time.Second,
		BindDuration: 10 * time.Second, ObserveDuration: 10 * time.Second,
		MaxInputActions: 96, MaxWaitActions: 16, MaxModelTurns: 24,
		MaxUnknownReplays: 0,
	}
}

type RunBudget struct {
	TotalDuration   time.Duration
	LaunchDuration  time.Duration
	BindDuration    time.Duration
	ObserveDuration time.Duration

	MaxInputActions   int
	MaxWaitActions    int
	MaxModelTurns     int
	MaxUnknownReplays int
}

func (b RunBudget) Validate() error {
	for name, value := range map[string]time.Duration{
		"total duration":   b.TotalDuration,
		"launch duration":  b.LaunchDuration,
		"bind duration":    b.BindDuration,
		"observe duration": b.ObserveDuration,
	} {
		if value < 0 {
			return fmt.Errorf("%w: %s must not be negative", ErrInvalidRunBudget, name)
		}
	}
	for name, value := range map[string]int{
		"max input actions":   b.MaxInputActions,
		"max wait actions":    b.MaxWaitActions,
		"max model turns":     b.MaxModelTurns,
		"max unknown replays": b.MaxUnknownReplays,
	} {
		if value < 0 {
			return fmt.Errorf("%w: %s must not be negative", ErrInvalidRunBudget, name)
		}
	}
	if b.MaxUnknownReplays != 0 {
		return fmt.Errorf("%w: unknown replay budget must be zero", ErrInvalidRunBudget)
	}
	return nil
}

// RunUsage is the immutable-by-convention snapshot returned by Runner.
type RunUsage struct {
	Elapsed         time.Duration
	LaunchDuration  time.Duration
	BindDuration    time.Duration
	ObserveDuration time.Duration
	InputActions    int
	WaitActions     int
	ModelTurns      int
	UnknownReplays  int
}

// Transition describes one state transition. From is optional; when supplied
// it acts as an optimistic concurrency guard and must match the current state.
type Transition struct {
	From RunState
	To   RunState
}

// Consumption describes one budget event. Duration is used only by launch,
// bind, and observe; all other kinds consume one count per call.
type Consumption struct {
	Kind     BudgetKind
	Duration time.Duration
}

// RunSnapshot is a consistent point-in-time view of the runner.
type RunSnapshot struct {
	State     RunState
	StartedAt time.Time
	UpdatedAt time.Time
	EndedAt   time.Time
	Budget    RunBudget
	Usage     RunUsage
}

var (
	ErrInvalidRunBudget   = errors.New("computer use: invalid run budget")
	ErrInvalidTransition  = errors.New("computer use: invalid run state transition")
	ErrInvalidConsumption = errors.New("computer use: invalid budget consumption")
	ErrBudgetExceeded     = errors.New("computer use: run budget exceeded")
	ErrUnknownReplay      = errors.New("computer use: unknown outcome cannot be replayed")
	ErrRunTerminal        = errors.New("computer use: run is terminal")
	ErrRunDeadline        = errors.New("computer use: total run deadline exceeded")
)

// Runner is a thread-safe, platform-neutral FSM and budget ledger for one
// Computer Use run. It does not perform native actions and has no application
// semantics; callers supply transitions and consumption events after their
// own operation has completed.
type Runner struct {
	mu sync.RWMutex

	budget RunBudget
	state  RunState
	usage  RunUsage

	now       func() time.Time
	startedAt time.Time
	updatedAt time.Time
	endedAt   time.Time
}

// NewRunner creates a runner using time.Now for the total deadline clock.
func NewRunner(budget RunBudget) (*Runner, error) {
	return NewRunnerWithClock(budget, time.Now)
}

// NewRunnerWithClock creates a runner with an injected clock, making deadline
// behavior deterministic in tests and reusable by hosts with a clock seam.
func NewRunnerWithClock(budget RunBudget, now func() time.Time) (*Runner, error) {
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidRunBudget)
	}
	startedAt := now()
	return &Runner{
		budget:    budget,
		state:     RunStateCreated,
		now:       now,
		startedAt: startedAt,
		updatedAt: startedAt,
	}, nil
}

// State returns the current FSM state.
func (r *Runner) State() RunState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// Snapshot returns a detached, consistent view of state, timing, and usage.
func (r *Runner) Snapshot() RunSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked()
}

// Transition advances the FSM after validating the transition and total run
// deadline. Failed and stopped are emergency terminal transitions and remain
// allowed after the total deadline so callers can always close a run.
func (r *Runner) Transition(transition Transition) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !transition.To.valid() || transition.To == r.state {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, r.state, transition.To)
	}
	if transition.From != "" && transition.From != r.state {
		return fmt.Errorf("%w: expected %s, current state is %s", ErrInvalidTransition, transition.From, r.state)
	}
	if r.state.terminal() {
		return fmt.Errorf("%w: %s", ErrRunTerminal, r.state)
	}
	if !allowedTransition(r.state, transition.To) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, r.state, transition.To)
	}
	if transition.To != RunStateFailed && transition.To != RunStateStopped {
		if err := r.checkDeadlineLocked(); err != nil {
			return err
		}
	}

	now := r.now()
	r.state = transition.To
	r.updatedAt = now
	if transition.To.terminal() {
		r.endedAt = now
	}
	return nil
}

// ResetToObserved reopens the observation phase after a cooperative host
// pause. It does not clear usage or permit replay; the session still requires a
// fresh observation before input.
func (r *Runner) ResetToObserved() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.terminal() {
		return fmt.Errorf("%w: %s", ErrRunTerminal, r.state)
	}
	if err := r.checkDeadlineLocked(); err != nil {
		return err
	}
	r.state = RunStateObserved
	r.updatedAt = r.now()
	return nil
}

// Consume records one launch/bind/observe duration or one input/wait/model
// turn. Unknown replay is deliberately rejected before mutating any usage.
func (r *Runner) Consume(consumption Consumption) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !consumption.Kind.valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidConsumption, consumption.Kind)
	}
	if consumption.Duration < 0 {
		return fmt.Errorf("%w: duration must not be negative", ErrInvalidConsumption)
	}
	if !consumption.Kind.durationBudget() && consumption.Duration != 0 {
		return fmt.Errorf("%w: %s does not accept a duration", ErrInvalidConsumption, consumption.Kind)
	}
	if r.state.terminal() {
		return fmt.Errorf("%w: %s", ErrRunTerminal, r.state)
	}
	// Unknown outcomes are never replayable, even if the run has also passed
	// its wall-clock deadline. Preserve the stronger safety signal.
	if consumption.Kind == BudgetUnknownReplay {
		return ErrUnknownReplay
	}
	if err := r.checkDeadlineLocked(); err != nil {
		return err
	}
	if err := r.checkPhaseStateLocked(consumption.Kind); err != nil {
		return err
	}
	if err := r.checkPhaseBudgetLocked(consumption); err != nil {
		return err
	}

	switch consumption.Kind {
	case BudgetLaunch:
		r.usage.LaunchDuration += consumption.Duration
	case BudgetBind:
		r.usage.BindDuration += consumption.Duration
	case BudgetObserve:
		r.usage.ObserveDuration += consumption.Duration
	case BudgetInput:
		r.usage.InputActions++
	case BudgetWait:
		r.usage.WaitActions++
	case BudgetModelTurn:
		r.usage.ModelTurns++
	}
	r.updatedAt = r.now()
	return nil
}

func allowedTransition(from, to RunState) bool {
	if to == RunStateFailed || to == RunStateStopped {
		return !from.terminal()
	}
	switch from {
	case RunStateCreated:
		return to == RunStateBound
	case RunStateBound:
		return to == RunStateObserved || to == RunStateExecuting
	case RunStateObserved:
		return to == RunStateBound || to == RunStateExecuting || to == RunStateCompleted
	case RunStateExecuting:
		return to == RunStateVerifying
	case RunStateVerifying:
		return to == RunStateObserved || to == RunStateCompleted
	default:
		return false
	}
}

func (r *Runner) checkPhaseStateLocked(kind BudgetKind) error {
	allowed := false
	switch kind {
	case BudgetLaunch:
		allowed = r.state == RunStateCreated || r.state == RunStateBound || r.state == RunStateObserved
	case BudgetBind:
		allowed = r.state == RunStateCreated || r.state == RunStateObserved || r.state == RunStateBound
	case BudgetObserve:
		allowed = r.state == RunStateBound || r.state == RunStateVerifying || r.state == RunStateObserved
	case BudgetInput:
		allowed = r.state == RunStateObserved
	case BudgetWait:
		allowed = r.state == RunStateObserved || r.state == RunStateVerifying
	case BudgetModelTurn:
		allowed = !r.state.terminal()
	case BudgetUnknownReplay:
		allowed = !r.state.terminal()
	}
	if !allowed {
		return fmt.Errorf("%w: %s cannot consume %s", ErrInvalidConsumption, r.state, kind)
	}
	return nil
}

func (r *Runner) checkPhaseBudgetLocked(consumption Consumption) error {
	limit := time.Duration(0)
	used := time.Duration(0)
	countLimit := 0
	countUsed := 0
	switch consumption.Kind {
	case BudgetLaunch:
		limit, used = r.budget.LaunchDuration, r.usage.LaunchDuration
	case BudgetBind:
		limit, used = r.budget.BindDuration, r.usage.BindDuration
	case BudgetObserve:
		limit, used = r.budget.ObserveDuration, r.usage.ObserveDuration
	case BudgetInput:
		countLimit, countUsed = r.budget.MaxInputActions, r.usage.InputActions
	case BudgetWait:
		countLimit, countUsed = r.budget.MaxWaitActions, r.usage.WaitActions
	case BudgetModelTurn:
		countLimit, countUsed = r.budget.MaxModelTurns, r.usage.ModelTurns
	case BudgetUnknownReplay:
		countLimit, countUsed = r.budget.MaxUnknownReplays, r.usage.UnknownReplays
	}
	if limit > 0 && used+consumption.Duration > limit {
		return fmt.Errorf("%w: %s duration %s exceeds %s", ErrBudgetExceeded, consumption.Kind, used+consumption.Duration, limit)
	}
	if countLimit > 0 && countUsed+1 > countLimit {
		return fmt.Errorf("%w: %s count %d exceeds %d", ErrBudgetExceeded, consumption.Kind, countUsed+1, countLimit)
	}
	return nil
}

func (r *Runner) checkDeadlineLocked() error {
	if r.budget.TotalDuration <= 0 {
		return nil
	}
	elapsed := r.now().Sub(r.startedAt)
	if elapsed > r.budget.TotalDuration {
		return fmt.Errorf("%w: elapsed %s exceeds %s", ErrRunDeadline, elapsed, r.budget.TotalDuration)
	}
	return nil
}

func (r *Runner) snapshotLocked() RunSnapshot {
	end := r.endedAt
	if end.IsZero() {
		end = r.now()
	}
	elapsed := end.Sub(r.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	usage := r.usage
	usage.Elapsed = elapsed
	return RunSnapshot{
		State:     r.state,
		StartedAt: r.startedAt,
		UpdatedAt: r.updatedAt,
		EndedAt:   r.endedAt,
		Budget:    r.budget,
		Usage:     usage,
	}
}
