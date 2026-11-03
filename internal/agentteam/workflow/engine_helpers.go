package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var (
	errOwnershipLost  = fmt.Errorf("%w: workflow lease is no longer owned", ErrConflict)
	errStopCheckpoint = errors.New("workflow stop checkpoint could not be persisted")
)

// Ownership is captured exactly once, at claim time; no refreshed snapshot may
// replace this fence. In particular a stale worker cannot adopt a recovered epoch.
type runOwnership struct {
	scope Scope
	id    string
	epoch uint64
	owner string
}

func ownershipOf(r Run) runOwnership {
	return runOwnership{r.Scope, r.ID, r.Epoch, r.LeaseOwner}
}
func (o runOwnership) matches(r Run) bool {
	return o.scope == r.Scope && o.id == r.ID && o.epoch == r.Epoch && o.owner == r.LeaseOwner &&
		(r.Status == Running || r.Status == CancelRequested || o.owner == "" && r.Status == Queued)
}

type stopReason struct {
	status Status
	code   string
	text   string
}

func checkpointFailure() *stopReason {
	return &stopReason{Interrupted, "checkpoint_failed", "Execution stopped because a durable checkpoint could not be confirmed; review before recovering"}
}

type runExecution struct {
	engine   *Engine
	owned    runOwnership
	run      Run // The last acknowledged checkpoint, never assigned a failed Save result.
	ctx      context.Context
	cancel   context.CancelFunc
	outcomes chan executionOutcome
	deltas   chan Event
	active   map[string]*nodeWatchdog
	expired  atomic.Bool
	pending  []executionOutcome
	buffered []Event
}

// The timer cancels the Run context even if the scheduler is blocked inside a
// context-aware Store operation. It never mutates Run state or releases a slot.
// Its atomic latch arbitrates timeout against actual Executor return.
type nodeWatchdog struct {
	deadline     time.Time
	realDeadline time.Time
	timer        *time.Timer
	settled      atomic.Bool
	expire       func()
}

func nodeTimeoutReason() *stopReason {
	return &stopReason{TimedOut, "node_timeout", "An Agent exceeded its step deadline"}
}
func (w *nodeWatchdog) finish() {
	// A cooperative return at the deadline must not disarm a timer whose
	// callback has not been scheduled yet (the scheduler may be in Store.Get).
	if !time.Now().Before(w.realDeadline) {
		w.expire()
	}
	w.settled.Store(true)
	w.timer.Stop()
}
func (x *runExecution) supervise(node Node) *nodeWatchdog {
	duration := time.Duration(node.TimeoutSeconds) * time.Second
	w := &nodeWatchdog{deadline: x.engine.now().Add(duration), realDeadline: time.Now().Add(duration)}
	w.expire = func() {
		if x.ctx.Err() == nil && w.settled.CompareAndSwap(false, true) {
			x.expired.Store(true)
			x.cancel()
		}
	}
	w.timer = time.AfterFunc(duration, w.expire)
	return w
}

func (x *runExecution) loop() *stopReason {
	e := x.engine
	ticker := time.NewTicker(e.poll)
	defer ticker.Stop()
	lastHeartbeat := e.now()
	for {
		if x.expired.Load() {
			return nodeTimeoutReason()
		}
		if err := x.ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return &stopReason{TimedOut, "run_timeout", "Workflow deadline exceeded"}
			}
			return &stopReason{Interrupted, "service_stopped", "Execution service stopped; review interrupted steps before recovering"}
		}
		if x.run.CancelRequestedAt != nil {
			return &stopReason{Cancelled, "cancelled", "Cancelled by the user"}
		}
		// This watchdog runs outside Execute: a provider ignoring child.Done must
		// not silently extend a step's deadline to the entire workflow deadline.
		for _, watchdog := range x.active {
			if !watchdog.deadline.After(e.now()) {
				return nodeTimeoutReason()
			}
		}
		if e.now().Sub(lastHeartbeat) >= heartbeatInterval {
			updated, err := e.mutate(x.ctx, x.owned.scope, x.owned.id, func(r *Run) ([]Event, error) {
				if !x.owned.matches(*r) {
					return nil, ErrConflict
				}
				until := e.now().Add(leaseDuration)
				r.LeaseUntil = &until
				return nil, nil
			})
			if err != nil {
				return checkpointFailure()
			}
			x.run = updated
			lastHeartbeat = e.now()
			if x.run.CancelRequestedAt != nil {
				continue
			}
		}
		if err := x.flush(); err != nil {
			return checkpointFailure()
		}
		fatal, done := runDisposition(x.run)
		if fatal {
			return &stopReason{Failed, "step_failed", "A required step failed; dependent steps were not executed"}
		}
		if done {
			return x.complete()
		}
		if err := x.dispatch(); err != nil {
			return checkpointFailure()
		}
		select {
		case <-x.ctx.Done():
		case ev := <-x.deltas:
			x.buffered = append(x.buffered, ev)
		case out := <-x.outcomes:
			watchdog := x.active[out.nodeID]
			delete(x.active, out.nodeID)
			// Retain the outcome until Save acknowledges it, even if progress
			// persistence fails first. Cleanup must still account its usage.
			x.pending = append(x.pending, out)
			if x.ctx.Err() != nil {
				continue // Settle usage through cleanup, never publish a stopped result.
			}
			if watchdog != nil && !watchdog.deadline.After(e.now()) {
				return nodeTimeoutReason()
			}
			for count := len(x.deltas); count > 0; count-- {
				x.buffered = append(x.buffered, <-x.deltas)
			}
			if err := x.flush(); err != nil {
				return checkpointFailure()
			}
			updated, err := e.commitOutcome(x.ctx, x.run, out)
			if err != nil {
				return checkpointFailure()
			}
			x.run = updated
			x.pending = nil
		case <-ticker.C:
			latest, err := e.store.Get(x.ctx, x.owned.scope, x.owned.id)
			if err != nil || !x.owned.matches(latest) {
				return checkpointFailure()
			}
			x.run = latest
		}
	}
}

func (x *runExecution) flush() error {
	if len(x.buffered) == 0 {
		return nil
	}
	updated, err := x.engine.appendEvents(x.ctx, x.run, x.buffered)
	if err == nil {
		x.run = updated
		x.buffered = nil
	}
	return err
}

func (x *runExecution) complete() *stopReason {
	_, err := x.engine.mutate(x.ctx, x.owned.scope, x.owned.id, func(r *Run) ([]Event, error) {
		if !x.owned.matches(*r) || r.CancelRequestedAt != nil {
			return nil, ErrConflict
		}
		if _, complete := runDisposition(*r); !complete {
			return nil, ErrConflict
		}
		now := x.engine.now().UTC()
		r.Status, r.FinishedAt = Completed, &now
		r.LeaseOwner, r.LeaseUntil = "", nil
		return []Event{{Type: EventCompleted, Status: Completed, CreatedAt: now}}, nil
	})
	if err == nil {
		return nil
	}
	// Only checkpointStop's fresh, fenced read may classify cancellation. A DB
	// failure, or an ownership conflict, is not evidence that Cancel won.
	return checkpointFailure()
}

func (x *runExecution) dispatch() error {
	e := x.engine
	for len(x.active) < x.run.Plan.Definition.Limits.MaxParallel {
		node, ready := nextReady(x.run, e.now())
		if !ready {
			return nil
		}
		select {
		case e.slots <- struct{}{}:
		default:
			return nil
		}
		in, updated, err := x.claim(node)
		if err != nil || in.NodeID == "" {
			<-e.slots
			if err == nil {
				x.run = updated
			}
			return err
		}
		x.run = updated
		n, _ := updated.Node(in.NodeID)
		in.TaskID = n.Attempts[len(n.Attempts)-1].TaskID
		watchdog := x.supervise(node)
		x.active[in.NodeID] = watchdog
		e.mu.Lock()
		e.inflight[in.RunID]++
		e.calls.Add(1)
		e.mu.Unlock()
		go e.call(x.ctx, node, in, x.deltas, x.outcomes, watchdog.finish)
	}
	return nil
}

func (x *runExecution) claim(node Node) (Input, Run, error) {
	e := x.engine
	var in Input
	updated, err := e.mutate(x.ctx, x.owned.scope, x.owned.id, func(r *Run) ([]Event, error) {
		in = Input{} // A failed CAS must not leak a previous callback's claim.
		if !x.owned.matches(*r) || r.Status != Running || r.CancelRequestedAt != nil {
			return nil, ErrConflict
		}
		index := nodeIndex(r, node.ID)
		if index < 0 {
			return nil, ErrConflict
		}
		n := &r.Nodes[index]
		if n.Status != Ready && n.Status != Pending && n.Status != RetryWait {
			return nil, ErrConflict
		}
		agent, _ := r.Plan.Agent(node.AgentRef)
		dependencies := dependencyArtifacts(*r, node)
		reserved := inputEstimate(*r, node, agent, dependencies) + agent.MaxOutputTokens
		if !budgetAllows(*r, reserved) {
			n.Status = Failed
			n.Failure = &Failure{Code: "token_budget_exceeded", Message: "Insufficient remaining workflow token budget for this step"}
			return []Event{{Type: EventAgentFailed, NodeID: n.NodeID, Status: Failed, Failure: n.Failure, CreatedAt: e.now().UTC()}}, nil
		}
		now := e.now().UTC()
		attempt := Attempt{BudgetReserved: reserved, Number: len(n.Attempts) + 1, Epoch: r.Epoch, Status: Running, StartedAt: now, Provider: agent.Provider, Model: agent.Model}
		n.Attempts = append(n.Attempts, attempt)
		n.Status, n.Failure, n.NextRetryAt = Running, nil, nil
		in = Input{RunID: r.ID, NodeID: node.ID, AttemptNo: attempt.Number, Epoch: r.Epoch, Agent: agent, Objective: r.Input, Instruction: node.Instruction, Dependencies: dependencies}
		return []Event{{Type: EventAgentStarted, NodeID: node.ID, AttemptNo: attempt.Number, Status: Running, CreatedAt: now}}, nil
	})
	return in, updated, err
}

// Every exit after claim converges here, including persistence and lease errors.
// Cancellation reaches providers immediately; local collection and the durable
// stop checkpoint each have a bounded grace. No unacknowledged state is returned.
func (x *runExecution) cleanup(reason stopReason) {
	x.cancel()
	for _, watchdog := range x.active {
		watchdog.finish()
	}
	if x.expired.Load() {
		reason = *nodeTimeoutReason()
	}
	timer := time.NewTimer(cleanupGrace)
	defer timer.Stop()
	clean := true
collect:
	for len(x.active) > 0 {
		select {
		case out := <-x.outcomes:
			delete(x.active, out.nodeID)
			x.pending = append(x.pending, out)
		case <-timer.C:
			clean = false
			break collect
		}
	}
	if !clean {
		reason = stopReason{Interrupted, "cleanup_timeout", "An Agent has not acknowledged stop; execution capacity remains reserved until it exits"}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(x.ctx), cleanupGrace)
	defer cancel()
	_, err := x.engine.checkpointStop(ctx, x.owned, reason, x.pending)
	if err != nil && !errors.Is(err, errOwnershipLost) {
		// Close must not imply that an unavailable database acknowledged a stop.
		// Keep DB payloads out of this shutdown error; their diagnostics belong
		// to the Store boundary, not user-visible workflow failure text.
		x.engine.mu.Lock()
		if x.engine.checkpointErr == nil {
			x.engine.checkpointErr = fmt.Errorf("workflow %s: %w", x.owned.id, errStopCheckpoint)
		}
		x.engine.mu.Unlock()
	}
	// On DB failure the last persisted Run remains authoritative. Its lease
	// expires into reclaim; unknown attempts retain their budget reservations.
}

func (e *Engine) checkpointStop(ctx context.Context, owned runOwnership, reason stopReason, pending []executionOutcome) (Run, error) {
	return e.mutate(ctx, owned.scope, owned.id, func(r *Run) ([]Event, error) {
		if !owned.matches(*r) {
			return nil, errOwnershipLost
		}
		chosen := reason
		for _, out := range pending {
			index := nodeIndex(r, out.nodeID)
			if out.epoch != owned.epoch || index < 0 {
				continue
			}
			n := &r.Nodes[index]
			if len(n.Attempts) != out.attempt || out.attempt == 0 {
				continue
			}
			a := &n.Attempts[out.attempt-1]
			if a.Status == Running && a.Epoch == owned.epoch {
				if err := e.accountOutcome(r, a, out); err != nil && chosen.code != "cleanup_timeout" {
					chosen = stopReason{Interrupted, "invalid_usage", "Agent returned invalid or overflowing usage; its reservation remains unresolved"}
				}
			}
		}
		if r.CancelRequestedAt != nil && chosen.code != "cleanup_timeout" {
			chosen = stopReason{Cancelled, "cancelled", "Cancelled by the user"}
		}
		return e.stopState(r, chosen.status, chosen.code, chosen.text), nil
	})
}

func checkedUsage(u Usage) (int, bool) {
	total := 0
	maxInt := int(^uint(0) >> 1)
	for _, count := range [...]int{u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens} {
		if count < 0 || count > maxInt-total {
			return 0, false
		}
		total += count
	}
	return total, true
}

func (e *Engine) accountOutcome(r *Run, a *Attempt, out executionOutcome) error {
	actual, valid := checkedUsage(out.result.Usage)
	previous, priorValid := checkedUsage(r.Usage)
	if !valid || !priorValid || actual > int(^uint(0)>>1)-previous {
		return errors.New("invalid Agent usage")
	}
	a.Usage = out.result.Usage
	a.UsageKnown = out.result.UsageKnown
	r.Usage.InputTokens += a.Usage.InputTokens
	r.Usage.OutputTokens += a.Usage.OutputTokens
	r.Usage.CacheReadTokens += a.Usage.CacheReadTokens
	r.Usage.CacheWriteTokens += a.Usage.CacheWriteTokens
	a.Provider = e.sanitize(out.result.Provider)
	a.Model = e.sanitize(out.result.Model)
	return nil
}
