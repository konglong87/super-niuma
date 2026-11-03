package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

func jobIndex(r Run, id string) int {
	for i := range r.Jobs {
		if r.Jobs[i].Spec.ID == id {
			return i
		}
	}
	return -1
}
func claimEqual(a, b Claim) bool {
	return a.Scope == b.Scope && a.RunID == b.RunID && a.JobID == b.JobID && a.Owner == b.Owner && a.Token != "" && a.Token == b.Token && a.Epoch == b.Epoch
}
func ready(r Run, j Job) bool {
	if j.Status != JobQueued || j.Attempts >= MaxAttempts {
		return false
	}
	for _, d := range j.Spec.DependsOn {
		i := jobIndex(r, d)
		if i < 0 || r.Jobs[i].Status != JobSucceeded {
			return false
		}
	}
	return true
}
func allSucceeded(r Run) bool {
	for _, j := range r.Jobs {
		if j.Status != JobSucceeded {
			return false
		}
	}
	return true
}
func (e *Engine) newClaim(r Run, id, owner string, epoch uint64) Claim {
	return Claim{Scope: r.Scope, RunID: r.ID, JobID: id, Owner: owner, Token: uuid.NewString(), Epoch: epoch + 1, UntilMS: e.now() + e.leaseMS()}
}

// Step performs one bounded unit. Competing processes must win the same durable
// CAS/resource claim before invoking an executor, verifier or transport.
func (e *Engine) Step(ctx context.Context, owner string) (bool, error) {
	runs, err := e.Store.ActiveRuns(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range runs {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if r.Status == AwaitingApproval {
			continue
		}
		if e.expired(r) {
			_, err = e.mutate(ctx, r.Scope, r.ID, func(n *Run) error { return e.markExpired(n) })
			if err != nil && !errors.Is(err, ErrConflict) {
				return false, err
			}
			continue
		}
		if r.Status == Cancelling || IsTerminal(r.Status) {
			continue
		}
		if err = e.authorized(ctx, r); err != nil {
			_, _ = e.Cancel(ctx, Principal{Scope: r.Scope, Owner: r.Owner}, r.ID, 0)
			continue
		}
		if r.Status == DeliveryPending {
			if e.DeliveryViaBus {
				continue
			}
			for _, notice := range r.Notices {
				if (notice.Status == DeliveryQueued || notice.Status == DeliveryRetry) && notice.RetryAtMS <= e.now() {
					return e.deliver(ctx, r, notice, owner)
				}
			}
			continue
		}
		if allSucceeded(r) {
			return e.verify(ctx, r, owner)
		}
		for _, j := range r.Jobs {
			if ready(r, j) {
				claimed, err := e.claimJob(ctx, r, j.Spec.ID, owner)
				if errors.Is(err, ErrConflict) || errors.Is(err, ErrResourceBusy) {
					continue
				}
				if err != nil {
					return false, err
				}
				return true, e.execute(ctx, claimed, j.Spec.ID)
			}
		}
	}
	return false, nil
}
func (e *Engine) expired(r Run) bool {
	for _, j := range r.Jobs {
		if j.Status == JobRunning && j.Claim.UntilMS <= e.now() {
			return true
		}
	}
	// NeedsAttention already quarantines this verifier. Retain the claim/read
	// lock as stop-proof provenance, but do not settle the same expiry again.
	if r.Status != NeedsAttention && r.VerificationClaim.Token != "" && r.VerificationClaim.UntilMS <= e.now() {
		return true
	}
	for _, n := range r.Notices {
		if n.Status == DeliverySending && n.Claim.UntilMS <= e.now() {
			return true
		}
	}
	return false
}
func (e *Engine) markExpired(r *Run) error {
	changed := false
	for i := range r.Jobs {
		j := &r.Jobs[i]
		if j.Status == JobRunning && j.Claim.UntilMS <= e.now() {
			j.Status = JobUnknown
			j.SafeCode = "lease_lost"
			changed = true
		}
	}
	if r.Status != NeedsAttention && r.VerificationClaim.Token != "" && r.VerificationClaim.UntilMS <= e.now() {
		changed = true
		r.SafeCode = "verification_lease_lost"
	}
	for i := range r.Notices {
		n := &r.Notices[i]
		if n.Status == DeliverySending && n.Claim.UntilMS <= e.now() {
			n.Status = DeliveryUnknown
			n.SafeCode = "delivery_outcome_unknown"
			changed = true
		}
	}
	if !changed {
		return ErrConflict
	}
	r.Status = NeedsAttention
	r.SafeCode = "lease_lost"
	return nil
}
func (e *Engine) claimJob(ctx context.Context, r Run, id, owner string) (Run, error) {
	return e.mutate(ctx, r.Scope, r.ID, func(n *Run) error {
		if e.authorized(ctx, *n) != nil {
			return ErrDenied
		}
		i := jobIndex(*n, id)
		if i < 0 || !ready(*n, n.Jobs[i]) {
			return ErrConflict
		}
		j := &n.Jobs[i]
		j.Status = JobRunning
		j.Attempts++
		j.Claim = e.newClaim(*n, id, owner, j.Claim.Epoch)
		j.StartedAtMS = e.now()
		j.SafeCode = ""
		n.Status = Running
		return nil
	})
}
func (e *Engine) check(ctx context.Context, c Claim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.Store.CheckClaim(ctx, c); err != nil {
		return err
	}
	r, err := e.Store.GetRun(ctx, c.Scope, c.RunID)
	if err != nil {
		return err
	}
	return e.authorized(ctx, r)
}
func (e *Engine) renew(ctx context.Context, c Claim) error {
	_, err := e.mutate(ctx, c.Scope, c.RunID, func(r *Run) error {
		if e.authorized(ctx, *r) != nil {
			return ErrDenied
		}
		if c.JobID == "super.verify" {
			if !claimEqual(r.VerificationClaim, c) || r.VerificationClaim.UntilMS <= e.now() {
				return ErrDenied
			}
			r.VerificationClaim.UntilMS = e.now() + e.leaseMS()
			return nil
		}
		i := jobIndex(*r, c.JobID)
		if i < 0 || r.Jobs[i].Status != JobRunning || !claimEqual(r.Jobs[i].Claim, c) || r.Jobs[i].Claim.UntilMS <= e.now() {
			return ErrDenied
		}
		r.Jobs[i].Claim.UntilMS = e.now() + e.leaseMS()
		return nil
	})
	return err
}
func (e *Engine) execute(parent context.Context, r Run, id string) error {
	i := jobIndex(r, id)
	j := r.Jobs[i]
	claim := j.Claim
	ctx, cancel := context.WithTimeout(parent, time.Duration(r.Grant.TimeoutMS)*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		interval := time.Duration(e.leaseMS()/3) * time.Millisecond
		if interval > 500*time.Millisecond {
			interval = 500 * time.Millisecond
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if e.check(ctx, claim) != nil || e.renew(ctx, claim) != nil {
					cancel()
					return
				}
			}
		}
	}()
	req := execution.Request{RunID: r.ID, JobID: id, AttemptID: claim.Token, Workspace: r.Grant.Workspace, Objective: r.Objective, Instruction: j.Spec.Instruction, SystemPrompt: r.Grant.SystemPrompt, Provider: r.Grant.Provider, Model: r.Grant.Model, ReadPaths: j.Spec.ReadPaths, WritePaths: j.Spec.WritePaths, MaxTurns: j.Spec.MaxTurns, MaxOutputTokens: j.Spec.MaxOutputTokens, CheckAuthority: func(c context.Context) error { return e.check(c, claim) }, Budget: &requestBudget{engine: e, claim: claim}}
	for _, d := range j.Spec.DependsOn {
		k := jobIndex(r, d)
		req.Dependencies = append(req.Dependencies, execution.Dependency{NodeID: d, Result: r.Jobs[k].Result.Text})
	}
	e.logger().InfoContext(ctx, "Super worker started", "action", "worker.started", "run_id", r.ID, "job_id", id, "epoch", claim.Epoch)
	result, runErr := e.Executor.Execute(ctx, req)
	ctxErr := ctx.Err()
	cancel()
	<-done
	// A short independent cleanup deadline permits recording cancellation after
	// the worker context was cancelled; it cannot authorize another tool action.
	finishCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, err := e.mutate(finishCtx, r.Scope, r.ID, func(n *Run) error {
		k := jobIndex(*n, id)
		if k < 0 || n.Jobs[k].Status != JobRunning || !claimEqual(n.Jobs[k].Claim, claim) {
			return ErrConflict
		}
		job := &n.Jobs[k]
		// Preserve only typed, sanitized execution facts on failed/cancelled
		// attempts. Successful text and write receipts are attached below.
		job.Result = execution.Result{Diagnostic: result.Diagnostic, Usage: result.Usage, UsageKnown: result.UsageKnown}
		if job.Claim.UntilMS <= e.now() {
			// Use the same fenced recovery as Step, including sibling expired
			// claims. Keep the typed diagnostic/usage attached above.
			return e.markExpired(n)
		}
		job.FinishedAtMS = e.now()
		if n.Status == Cancelling {
			job.Status = JobCancelled
			job.SafeCode = "cancelled"
			active := false
			for _, s := range n.Jobs {
				if s.Status == JobRunning {
					active = true
				}
			}
			if !active {
				n.Status = Cancelled
			}
			return nil
		}
		if IsTerminal(n.Status) {
			job.Status = JobFailed
			job.SafeCode = "root_stopped"
			return nil
		}
		if runErr != nil || ctxErr != nil || !result.UsageKnown {
			job.Status = JobFailed
			job.SafeCode = "execution_failed"
			n.Status = Failed
			if runErr != nil {
				job.SafeCode = execution.FailureCode(runErr)
			} else if ctxErr != nil {
				job.SafeCode = execution.FailureCode(ctxErr)
			}
			if !result.UsageKnown {
				if runErr == nil && ctxErr == nil {
					job.SafeCode = "usage_unknown"
				}
				n.Status = NeedsAttention
			}
			n.SafeCode = job.SafeCode
			return nil
		}
		if len(result.Text) > execution.MaxResultBytes {
			return ErrCapacity
		}
		job.Status = JobSucceeded
		job.Result = result
		job.SafeCode = ""
		if allSucceeded(*n) {
			n.Status = Verifying
		}
		return nil
	})
	e.logger().Info("Super worker settled", "action", "worker.settled", "run_id", r.ID, "job_id", id, "success", runErr == nil && ctxErr == nil && err == nil)
	return err
}

type requestBudget struct {
	engine *Engine
	claim  Claim
}

func (b *requestBudget) Reserve(ctx context.Context, id string, input, output int) error {
	e := b.engine
	if !ValidID(id) || input < 0 || output < 1 || input > MaxBudgetTokens || output > MaxBudgetTokens {
		return execution.ErrBudget
	}
	if e.check(ctx, b.claim) != nil {
		return execution.ErrAuthority
	}
	_, err := e.mutate(ctx, b.claim.Scope, b.claim.RunID, func(r *Run) error {
		if e.authorized(ctx, *r) != nil {
			return ErrDenied
		}
		i := jobIndex(*r, b.claim.JobID)
		if i < 0 || r.Jobs[i].Status != JobRunning || !claimEqual(r.Jobs[i].Claim, b.claim) || r.Jobs[i].Claim.UntilMS <= e.now() {
			return ErrDenied
		}
		for _, q := range r.Reservations {
			if q.ID == id {
				return ErrConflict
			}
		}
		if input+output > r.Grant.MaxTokens-ReservedTokens(*r) || len(r.Reservations) >= MaxReservations {
			return execution.ErrBudget
		}
		r.Reservations = append(r.Reservations, Reservation{ID: id, JobID: b.claim.JobID, AttemptID: b.claim.Token, Input: input, Output: output})
		return nil
	})
	return err
}
func (b *requestBudget) Settle(ctx context.Context, id string, u execution.Usage, known bool) error {
	if !known {
		return nil
	}
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.InputTokens > MaxBudgetTokens || u.OutputTokens > MaxBudgetTokens {
		return execution.ErrBudget
	}
	_, err := b.engine.mutate(ctx, b.claim.Scope, b.claim.RunID, func(r *Run) error {
		for i := range r.Reservations {
			v := &r.Reservations[i]
			if v.ID != id {
				continue
			}
			if v.JobID != b.claim.JobID || v.AttemptID != b.claim.Token || v.Settled {
				return ErrConflict
			}
			if u.InputTokens > v.Input || u.OutputTokens > v.Output {
				v.Actual = u
				v.Settled = true
				r.Status = NeedsAttention
				r.SafeCode = "budget_violation"
				return nil
			}
			v.Settled = true
			v.Actual = u
			return nil
		}
		return fmt.Errorf("%w: reservation missing", ErrInvalid)
	})
	return err
}
