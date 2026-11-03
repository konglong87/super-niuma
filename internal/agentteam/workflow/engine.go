package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	defaultPoll       = 100 * time.Millisecond
	leaseDuration     = 5 * time.Second
	heartbeatInterval = time.Second
	cleanupGrace      = 2 * time.Second
	maxQueuedPerUser  = 20
	maxRunsPerUser    = 2
)

type EngineOptions struct {
	Parallel     int
	PollInterval time.Duration
	Now          func() time.Time
	Sanitize     func(string) string
}

// Engine schedules a bounded number of static, persisted workflows. Model and
// tool code never executes inside a Store transaction. A Run's CAS/epoch is the
// authority; the cancel map and wake channel are only latency optimizations.
type Engine struct {
	store         Store
	executor      Executor
	now           func() time.Time
	sanitize      func(string) string
	poll          time.Duration
	owner         string
	slots         chan struct{}
	wake          chan struct{}
	mu            sync.Mutex
	active        map[string]activeRun
	inflight      map[string]int
	root          context.Context
	stop          context.CancelFunc
	wg            sync.WaitGroup
	calls         sync.WaitGroup
	closeOnce     sync.Once
	closed        chan struct{}
	checkpointErr error
	started       bool
	closing       bool
}
type activeRun struct {
	scope  Scope
	cancel context.CancelFunc
}

func NewEngine(store Store, executor Executor, options EngineOptions) *Engine {
	if options.Parallel < 1 || options.Parallel > 8 {
		options.Parallel = 4
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPoll
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Sanitize == nil {
		options.Sanitize = Redact
	}
	return &Engine{store: store, executor: executor, now: options.Now, sanitize: options.Sanitize, poll: options.PollInterval, owner: uuid.NewString(), slots: make(chan struct{}, options.Parallel), wake: make(chan struct{}, 1), active: map[string]activeRun{}, inflight: map[string]int{}, closed: make(chan struct{})}
}
func (e *Engine) Start(ctx context.Context) error {
	if e.store == nil || e.executor == nil {
		return errors.New("workflow runtime dependencies are incomplete")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing {
		return errors.New("workflow execution service is closed")
	}
	if e.started {
		return nil
	}
	e.started = true
	e.root, e.stop = context.WithCancel(ctx)
	e.wg.Add(1)
	go func() { defer e.wg.Done(); e.pump(e.root) }()
	return nil
}
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closing = true
	if e.stop != nil {
		e.stop()
	}
	e.mu.Unlock()
	// Schedulers are the only producers of calls.Add. Waiting for them first
	// prevents Add/Wait races and includes executors surviving bounded cleanup.
	e.closeOnce.Do(func() {
		go func() { e.wg.Wait(); e.calls.Wait(); close(e.closed) }()
	})
	select {
	case <-e.closed:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.checkpointErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *Engine) pump(ctx context.Context) {
	ticker := time.NewTicker(e.poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		e.scan(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.wake:
		}
	}
}
func (e *Engine) scan(ctx context.Context) {
	runs, err := e.store.Active(ctx)
	if err != nil {
		return
	}
	for _, r := range runs {
		if ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		_, active := e.active[r.ID]
		e.mu.Unlock()
		if active {
			continue
		}
		if r.Status == Running || r.Status == CancelRequested {
			if r.LeaseUntil != nil && r.LeaseUntil.After(e.now()) {
				continue
			}
			e.reclaim(ctx, r)
			continue
		}
		if r.Status != Queued {
			continue
		}
		e.mu.Lock()
		count := 0
		for _, item := range e.active {
			if item.scope == r.Scope {
				count++
			}
		}
		if count >= maxRunsPerUser || len(e.active) >= cap(e.slots)*2 {
			e.mu.Unlock()
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		e.active[r.ID] = activeRun{scope: r.Scope, cancel: cancel}
		e.wg.Add(1)
		e.mu.Unlock()
		go func(run Run) {
			defer e.wg.Done()
			defer cancel()
			defer func() { e.mu.Lock(); delete(e.active, run.ID); e.mu.Unlock(); e.Wake() }()
			e.execute(runCtx, run)
		}(r)
	}
}

func (e *Engine) Create(ctx context.Context, run Run) (Run, bool, error) {
	if strings.TrimSpace(run.Input) == "" || len(run.Input) > MaxInputBytes {
		return Run{}, false, invalid("input", "enter a goal, up to 32 KiB")
	}
	if run.Scope.TenantID == 0 || run.Scope.UserID == 0 || run.TeamID == 0 || run.KeyHash == "" || run.Fingerprint == "" {
		return Run{}, false, invalid("run", "authenticated scope, published Team and idempotency key are required")
	}
	plan, err := Compile(run.Plan.Definition, run.Plan.Agents)
	if err != nil {
		return Run{}, false, err
	}
	run.Plan = plan
	// Replays must remain available at queue capacity, so the durable Create
	// operation is authoritative for duplicate detection. The queue cap is a
	// conservative admission precheck; independent scopes share the global slots.
	runs, err := e.store.List(ctx, run.Scope, 100)
	if err != nil {
		return Run{}, false, err
	}
	queued := 0
	duplicate := false
	for _, r := range runs {
		if r.KeyHash == run.KeyHash {
			duplicate = true
		}
		if !Terminal(r.Status) {
			queued++
		}
	}
	if queued >= maxQueuedPerUser && !duplicate {
		return Run{}, false, ErrCapacity
	}
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	now := e.now().UTC()
	run.CreatedAt = now
	run.Deadline = now.Add(time.Duration(plan.Definition.Limits.RunTimeoutSeconds) * time.Second)
	run.Status = Queued
	run.Epoch = 1
	run.Input = e.sanitize(run.Input)
	run.Nodes = make([]NodeRun, 0, len(plan.Definition.Nodes))
	events := []Event{{Type: EventQueued, Status: Queued, CreatedAt: now}}
	for _, n := range plan.Definition.Nodes {
		status := Pending
		typ := EventWaiting
		if len(n.DependsOn) == 0 {
			status = Ready
			typ = EventTaskCreated
		}
		run.Nodes = append(run.Nodes, NodeRun{NodeID: n.ID, Status: status, Attempts: []Attempt{}})
		events = append(events, Event{Type: typ, NodeID: n.ID, Status: status, CreatedAt: now})
	}
	run.Artifacts = []Artifact{}
	run.UncertainTokens = 0
	saved, created, err := e.store.Create(ctx, run, events)
	if err == nil {
		e.Wake()
	}
	return saved, created, err
}
func (e *Engine) Cancel(ctx context.Context, scope Scope, id string) (Run, error) {
	run, err := e.mutate(ctx, scope, id, func(r *Run) ([]Event, error) {
		if Terminal(r.Status) || r.Status == CancelRequested {
			return nil, nil
		}
		now := e.now().UTC()
		r.CancelRequestedAt = &now
		r.Status = CancelRequested
		return []Event{{Type: EventCancelRequested, Status: CancelRequested, CreatedAt: now}}, nil
	})
	if err != nil {
		return Run{}, err
	}
	e.mu.Lock()
	active, ok := e.active[id]
	e.mu.Unlock()
	if ok {
		active.cancel()
	} else if run.Status == CancelRequested && run.LeaseOwner == "" {
		run, err = e.finishStopped(ctx, run, Cancelled, "cancelled", "Cancelled by the user")
	}
	e.Wake()
	return run, err
}
func (e *Engine) Recover(ctx context.Context, scope Scope, id string, revision uint64) (Run, error) {
	if _, err := e.store.Get(ctx, scope, id); err != nil {
		return Run{}, err
	}
	e.mu.Lock()
	activeCalls := e.inflight[id]
	_, activeRunner := e.active[id]
	e.mu.Unlock()
	if activeCalls > 0 || activeRunner {
		return Run{}, invalid("cleanup", "an Agent is still stopping; wait for it to exit before recovering")
	}
	run, err := e.mutate(ctx, scope, id, func(r *Run) ([]Event, error) {
		e.mu.Lock()
		busy := e.inflight[id] > 0
		_, running := e.active[id]
		e.mu.Unlock()
		if busy || running {
			return nil, invalid("cleanup", "an Agent or its checkpoint is still stopping")
		}
		if r.Revision != revision {
			return nil, ErrConflict
		}
		if r.Status != Interrupted {
			return nil, invalid("status", "only interrupted runs can be recovered; start a new run for a finished workflow")
		}
		if !r.Deadline.After(e.now()) {
			return nil, invalid("deadline", "the original run deadline has expired; start a new run")
		}
		for i := range r.Nodes {
			n := &r.Nodes[i]
			if n.Status == Completed {
				continue
			}
			def, _ := r.Plan.Node(n.NodeID)
			if len(n.Attempts) >= def.Retry.MaxAttempts {
				return nil, invalid("attempts", "the interrupted step has reached its attempt limit; start a new run")
			}
			n.Status = Pending
			n.Failure = nil
			n.NextRetryAt = nil
		}
		r.Epoch++
		r.Status = Queued
		r.Failure = nil
		r.LeaseOwner = ""
		r.LeaseUntil = nil
		r.CancelRequestedAt = nil
		r.FinishedAt = nil
		return []Event{{Type: EventRecovered, Status: Queued, Text: "Explicit recovery: completed steps retained; interrupted text requests may be sent again", CreatedAt: e.now().UTC()}}, nil
	})
	if err == nil {
		e.Wake()
	}
	return run, err
}
func (e *Engine) mutate(ctx context.Context, scope Scope, id string, fn func(*Run) ([]Event, error)) (Run, error) {
	for attempt := 0; attempt < 12; attempt++ {
		run, err := e.store.Get(ctx, scope, id)
		if err != nil {
			return Run{}, err
		}
		revision := run.Revision
		events, err := fn(&run)
		if err != nil {
			return Run{}, err
		}
		// Save even a no-event change (lease/checkpoint), but never publish an
		// uncommitted in-memory state to callers.
		run.UncertainTokens = UncertainTokens(run)
		saved, err := e.store.Save(ctx, revision, run, events)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return saved, err
	}
	return Run{}, ErrConflict
}
func (e *Engine) reclaim(ctx context.Context, old Run) {
	_, _ = e.mutate(ctx, old.Scope, old.ID, func(r *Run) ([]Event, error) {
		if r.Status != Running && r.Status != CancelRequested {
			return nil, ErrConflict
		}
		if r.LeaseUntil != nil && r.LeaseUntil.After(e.now()) {
			return nil, ErrConflict
		}
		status := Interrupted
		code := "runner_lost"
		text := "The previous execution process stopped. Completed steps are retained; review before recovering."
		if r.CancelRequestedAt != nil {
			status = Cancelled
			code = "cancelled"
			text = "Cancellation retained across service restart"
		}
		r.Epoch++
		return e.stopState(r, status, code, text), nil
	})
}
func (e *Engine) stopState(r *Run, status Status, code, text string) []Event {
	now := e.now().UTC()
	events := []Event{}
	for i := range r.Nodes {
		n := &r.Nodes[i]
		if n.Status == Completed {
			continue
		}
		if n.Status == Failed || n.Status == TimedOut {
			continue
		}
		n.Status = Cancelled
		if status == Interrupted {
			n.Status = Interrupted
		}
		if status == Failed || status == TimedOut {
			n.Status = Blocked
		}
		n.Failure = &Failure{Code: code, Message: e.sanitize(text)}
		if len(n.Attempts) > 0 {
			a := &n.Attempts[len(n.Attempts)-1]
			if a.Status == Running {
				a.Status = Cancelled
				if status == Interrupted {
					a.Status = Interrupted
				}
				a.FinishedAt = &now
				a.Failure = n.Failure
			}
		}
		events = append(events, Event{Type: EventTaskCancelled, NodeID: n.NodeID, Status: n.Status, Failure: n.Failure, CreatedAt: now})
	}
	r.Status = status
	r.LeaseOwner = ""
	r.LeaseUntil = nil
	r.FinishedAt = &now
	r.Failure = &Failure{Code: code, Message: e.sanitize(text)}
	typ := EventFailed
	switch status {
	case Cancelled:
		typ = EventTaskCancelled
	case Interrupted:
		typ = EventInterrupted
	case TimedOut:
		typ = EventTimedOut
	}
	if status == Cancelled {
		typ = EventCancelled
	}
	return append(events, Event{Type: typ, Status: status, Failure: r.Failure, CreatedAt: now})
}
func (e *Engine) finishStopped(ctx context.Context, r Run, status Status, code, text string) (Run, error) {
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupGrace)
	defer cancel()
	return e.checkpointStop(clean, ownershipOf(r), stopReason{status, code, text}, nil)
}

type executionOutcome struct {
	nodeID  string
	attempt int
	epoch   uint64
	result  Result
	err     error
}

func (e *Engine) execute(parent context.Context, initial Run) {
	run, err := e.mutate(parent, initial.Scope, initial.ID, func(r *Run) ([]Event, error) {
		if r.Status != Queued {
			return nil, ErrConflict
		}
		now := e.now().UTC()
		r.Status = Running
		r.Epoch++
		r.LeaseOwner = e.owner
		until := now.Add(leaseDuration)
		r.LeaseUntil = &until
		if r.StartedAt == nil {
			r.StartedAt = &now
		}
		return []Event{{Type: EventStarted, Status: Running, CreatedAt: now}}, nil
	})
	if err != nil {
		return // No executor was launched unless its claim was acknowledged.
	}
	ctx, cancel := context.WithDeadline(parent, run.Deadline)
	runner := runExecution{
		engine: e, owned: ownershipOf(run), run: run, ctx: ctx, cancel: cancel,
		outcomes: make(chan executionOutcome, run.Plan.Definition.Limits.MaxParallel),
		deltas:   make(chan Event, 128), active: map[string]*nodeWatchdog{},
	}
	defer cancel()
	if reason := runner.loop(); reason != nil {
		runner.cleanup(*reason)
	}
}
func (e *Engine) call(ctx context.Context, node Node, in Input, events chan<- Event, results chan<- executionOutcome, finished func()) {
	defer func() {
		<-e.slots
		e.mu.Lock()
		e.inflight[in.RunID]--
		if e.inflight[in.RunID] == 0 {
			delete(e.inflight, in.RunID)
		}
		e.mu.Unlock()
		e.Wake()
		e.calls.Done()
	}()
	out := executionOutcome{nodeID: in.NodeID, attempt: in.AttemptNo, epoch: in.Epoch}
	defer func() {
		if recovered := recover(); recovered != nil {
			out.err = errors.New("Agent executor panicked")
			out.result = Result{Status: Failed, Failure: &Failure{Code: "executor_panic", Message: "Agent executor stopped unexpectedly"}}
		}
		finished() // Stop its watchdog on actual return, not on result consumption.
		results <- out
	}()
	child, cancel := context.WithTimeout(ctx, time.Duration(node.TimeoutSeconds)*time.Second)
	defer cancel()
	var progressBytes atomic.Int64
	out.result, out.err = e.executor.Execute(child, in, func(ev Event) {
		// A provider cannot manufacture control events through a progress callback.
		if ev.Type != EventDelta && ev.Type != EventPhase {
			return
		}
		ev = Event{Type: ev.Type, NodeID: in.NodeID, AttemptNo: in.AttemptNo, CreatedAt: e.now().UTC(), Text: e.sanitize(ev.Text)}
		if progressBytes.Add(int64(len(ev.Text))) > MaxOutputBytes {
			cancel()
			return
		}
		if len(ev.Text) > MaxOutputBytes {
			ev.Text = ev.Text[:MaxOutputBytes]
		}
		select {
		case events <- ev:
		case <-child.Done():
		}
	})
	if child.Err() != nil {
		out.err = child.Err()
		out.result.Status = Failed
	}
}
func (e *Engine) appendEvents(ctx context.Context, run Run, events []Event) (Run, error) {
	return e.mutate(ctx, run.Scope, run.ID, func(r *Run) ([]Event, error) {
		if !ownershipOf(run).matches(*r) {
			return nil, ErrConflict
		}
		return events, nil
	})
}
func (e *Engine) commitOutcome(ctx context.Context, run Run, out executionOutcome) (Run, error) {
	return e.mutate(ctx, run.Scope, run.ID, func(r *Run) ([]Event, error) {
		if !ownershipOf(run).matches(*r) || r.Epoch != out.epoch {
			return nil, ErrConflict
		}
		index := nodeIndex(r, out.nodeID)
		if index < 0 {
			return nil, ErrConflict
		}
		n := &r.Nodes[index]
		if n.Status != Running || len(n.Attempts) != out.attempt {
			return nil, ErrConflict
		}
		a := &n.Attempts[len(n.Attempts)-1]
		if a.Epoch != out.epoch {
			return nil, ErrConflict
		}
		now := e.now().UTC()
		a.FinishedAt = &now
		if err := e.accountOutcome(r, a, out); err != nil {
			n.Status, a.Status = Failed, Failed
			n.Failure = &Failure{Code: "invalid_usage", Message: "Agent returned invalid or overflowing usage"}
			a.Failure = n.Failure
			return []Event{{Type: EventAgentFailed, NodeID: n.NodeID, AttemptNo: a.Number, Status: Failed, Failure: n.Failure, CreatedAt: now}}, nil
		}
		failure := out.result.Failure
		if out.result.Usage.InputTokens < 0 || out.result.Usage.OutputTokens < 0 || out.result.Usage.CacheReadTokens < 0 || out.result.Usage.CacheWriteTokens < 0 {
			failure = &Failure{Code: "invalid_usage", Message: "Agent returned invalid token usage"}
			out.result.Status = Failed
			a.Usage = Usage{}
			r.Usage = run.Usage
		}
		if r.CancelRequestedAt != nil {
			a.Status = Cancelled
			n.Status = Cancelled
			return []Event{{Type: EventTaskCancelled, NodeID: n.NodeID, AttemptNo: a.Number, Status: Cancelled, CreatedAt: now}}, nil
		}
		if out.err != nil || out.result.Status != Completed {
			if failure == nil {
				failure = &Failure{Code: "agent_failed", Message: "Agent did not complete successfully"}
				if out.err != nil {
					failure.Message = e.sanitize(out.err.Error())
				}
				if errors.Is(out.err, context.DeadlineExceeded) {
					failure.Code = "agent_timeout"
					failure.Retryable = true
				}
			}
			copyFailure := *failure
			failure = &copyFailure
			failure.Message = e.sanitize(failure.Message)
			a.Status = Failed
			a.Failure = failure
			n.Failure = failure
			node, _ := r.Plan.Node(n.NodeID)
			n.Status = Failed
			events := []Event{{Type: EventAgentFailed, NodeID: n.NodeID, AttemptNo: a.Number, Status: Failed, Failure: failure, CreatedAt: now}}
			if failure.Retryable && len(n.Attempts) < node.Retry.MaxAttempts && r.Deadline.After(now) {
				delay := node.Retry.BackoffMS * (1 << (a.Number - 1))
				if delay > 10000 {
					delay = 10000
				}
				next := now.Add(time.Duration(delay) * time.Millisecond)
				n.NextRetryAt = &next
				n.Status = RetryWait
				events = append(events, Event{Type: EventRetry, NodeID: n.NodeID, AttemptNo: a.Number + 1, Status: RetryWait, Failure: failure, CreatedAt: now})
			}
			return events, nil
		}
		output := e.sanitize(out.result.Output)
		if len(output) > MaxOutputBytes || strings.TrimSpace(output) == "" {
			n.Status = Failed
			a.Status = Failed
			n.Failure = &Failure{Code: "invalid_output", Message: "Agent output was empty or exceeded the artifact limit"}
			a.Failure = n.Failure
			return []Event{{Type: EventAgentFailed, NodeID: n.NodeID, AttemptNo: a.Number, Status: Failed, Failure: n.Failure, CreatedAt: now}}, nil
		}
		// Settle this attempt before checking historical unknown reservations.
		a.Status = Completed
		n.Status = Completed
		if !budgetAllows(*r, 0) {
			n.Status = Failed
			a.Status = Failed
			n.Failure = &Failure{Code: "token_budget_exceeded", Message: "The workflow token budget was exceeded"}
			a.Failure = n.Failure
			return []Event{{Type: EventAgentFailed, NodeID: n.NodeID, AttemptNo: a.Number, Status: Failed, Failure: n.Failure, CreatedAt: now}}, nil
		}
		sum := sha256.Sum256([]byte(output))
		artifact := Artifact{ID: uuid.NewString(), NodeID: n.NodeID, AttemptNo: a.Number, Kind: "text", Content: output, SHA256: hex.EncodeToString(sum[:]), CreatedAt: now}
		r.Artifacts = append(r.Artifacts, artifact)
		n.Output = output
		n.ArtifactID = artifact.ID
		n.Status = Completed
		a.Status = Completed
		return []Event{{Type: EventAgentCompleted, NodeID: n.NodeID, AttemptNo: a.Number, Status: Completed, CreatedAt: now}}, nil
	})
}
func nodeIndex(run *Run, id string) int {
	for i := range run.Nodes {
		if run.Nodes[i].NodeID == id {
			return i
		}
	}
	return -1
}
func runDisposition(run Run) (failed, done bool) {
	done = len(run.Nodes) > 0
	for _, n := range run.Nodes {
		if n.Status == Failed || n.Status == TimedOut || n.Status == Blocked {
			failed = true
		}
		if n.Status != Completed {
			done = false
		}
	}
	return
}
func nextReady(run Run, now time.Time) (Node, bool) {
	for _, def := range run.Plan.Definition.Nodes {
		n, _ := run.Node(def.ID)
		if n.Status != Ready && n.Status != Pending && n.Status != RetryWait {
			continue
		}
		if n.NextRetryAt != nil && n.NextRetryAt.After(now) {
			continue
		}
		ok := true
		for _, dep := range def.DependsOn {
			up, _ := run.Node(dep)
			if up.Status != Completed {
				ok = false
				break
			}
		}
		if ok {
			return def, true
		}
	}
	return Node{}, false
}
func dependencyArtifacts(run Run, node Node) []Artifact {
	out := []Artifact{}
	for _, dep := range node.DependsOn {
		for _, a := range run.Artifacts {
			if a.NodeID == dep {
				out = append(out, a)
			}
		}
	}
	return out
}
func inputEstimate(run Run, node Node, agent Agent, deps []Artifact) int {
	n := len(run.Input) + len(node.Instruction) + len(agent.SystemPrompt) + 512
	for _, a := range deps {
		n += len(a.Content)
	}
	return n
}

// UncertainTokens recomputes all unresolved reservations, including in-flight
// attempts. Run.UncertainTokens is persisted from this function at every CAS.
// Missing legacy reservation fields are derived from the immutable Plan; terminal
// attempts themselves remain immutable. The result is not actual billed usage.
func UncertainTokens(run Run) int {
	total := 0
	for _, n := range run.Nodes {
		def, _ := run.Plan.Node(n.NodeID)
		agent, _ := run.Plan.Agent(def.AgentRef)
		for _, attempt := range n.Attempts {
			if attempt.UsageKnown {
				continue
			}
			reserved := attempt.BudgetReserved
			if reserved <= 0 {
				reserved = inputEstimate(run, def, agent, dependencyArtifacts(run, def)) + agent.MaxOutputTokens
			}
			actual, valid := checkedUsage(attempt.Usage)
			if !valid {
				actual = 0
			}
			if actual < reserved {
				total += reserved - actual
			}
		}
	}
	return total
}

func reservedTokens(run Run) int { return UncertainTokens(run) }

// Compare by subtraction so malformed provider counters cannot overflow an
// addition and turn an exceeded budget into available capacity.
func budgetAllows(run Run, extra int) bool {
	actual, valid := checkedUsage(run.Usage)
	limit := run.Plan.Definition.Limits.MaxTotalTokens
	if !valid || extra < 0 || actual > limit {
		return false
	}
	uncertain := UncertainTokens(run)
	return uncertain >= 0 && uncertain <= limit-actual && extra <= limit-actual-uncertain
}

// Identity creates scope-bound idempotency identities without storing the raw key.
func Identity(scope Scope, key string, payload []byte) (string, string, error) {
	if len(key) < 8 || len(key) > 200 {
		return "", "", invalid("idempotency_key", "use a stable request key between 8 and 200 bytes")
	}
	keySum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", scope.TenantID, scope.UserID, key)))
	fingerprint := sha256.Sum256(payload)
	return hex.EncodeToString(keySum[:]), hex.EncodeToString(fingerprint[:]), nil
}
