package control

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

const maxCASRetries = 16

type Engine struct {
	DeliveryViaBus bool
	WorkerLimit    func() int
	Store          Repository
	Executor       execution.Executor
	Authority      Authority
	Verifier       Verifier
	Sender         Sender
	Logger         *slog.Logger
	Workers        int
	Lease          time.Duration
	Now            func() time.Time
}

func (e *Engine) now() int64 {
	if e.Now != nil {
		return e.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}
func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}
func (e *Engine) leaseMS() int64 {
	if e.Lease > 0 {
		return e.Lease.Milliseconds()
	}
	return DefaultLeaseMS
}
func (e *Engine) authorized(ctx context.Context, r Run) error {
	if r.Grant.ExpiresAtMS <= e.now() || !r.Approved || r.Status == Cancelling || IsTerminal(r.Status) {
		return ErrDenied
	}
	if e.Authority == nil {
		return ErrUnavailable
	}
	return e.Authority.Current(ctx, Principal{Scope: r.Scope, Owner: r.Owner}, r.Grant)
}
func (e *Engine) Start(ctx context.Context, p Principal, key, objective, intakeID string, g Grant, in *Ingress) (Run, bool, error) {
	if e.Store == nil || e.Executor == nil || e.Verifier == nil || e.Sender == nil || e.Authority == nil {
		return Run{}, false, ErrUnavailable
	}
	if !ValidID(p.Scope) || !ValidID(p.Owner) || !ValidID(key) || ValidateGrant(g) != nil {
		return Run{}, false, ErrInvalid
	}
	root, err := filepath.EvalSymlinks(g.Workspace)
	if err != nil || root != g.Workspace {
		return Run{}, false, ErrInvalid
	}
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return Run{}, false, ErrInvalid
	}
	if g.ExpiresAtMS <= e.now() {
		return Run{}, false, ErrDenied
	}
	if err := e.Authority.Current(ctx, p, g); err != nil {
		return Run{}, false, ErrDenied
	}
	r := Run{ID: uuid.NewString(), Scope: p.Scope, Owner: p.Owner, IntakeID: intakeID, RequestKey: key, Objective: objective, Grant: g, GrantHash: Digest(g), Version: 1, Status: AwaitingApproval, Approved: g.AutoApprove, CreatedAtMS: e.now(), UpdatedAtMS: e.now(), Jobs: []Job{}, Reservations: []Reservation{}, Notices: []Notice{}}
	if r.Approved {
		r.Status = Queued
	}
	for _, n := range g.Nodes {
		r.Jobs = append(r.Jobs, Job{Spec: n, Status: JobQueued})
	}
	if err := ValidateRun(r); err != nil {
		return Run{}, false, err
	}
	saved, created, err := e.Store.CreateRun(ctx, r, in)
	if err == nil {
		e.logger().InfoContext(ctx, "Super admission committed", "action", "super.admit", "run_id", saved.ID, "created", created, "scope", p.Scope)
	}
	return saved, created, err
}
func (e *Engine) Get(ctx context.Context, p Principal, id string) (Run, error) {
	r, err := e.Store.GetRun(ctx, p.Scope, id)
	if err != nil {
		return Run{}, err
	}
	if !CanRead(p, r) {
		return Run{}, ErrDenied
	}
	return r, nil
}
func (e *Engine) List(ctx context.Context, p Principal) ([]Run, error) {
	all, err := e.Store.ListRuns(ctx, p.Scope)
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(all))
	for _, r := range all {
		if CanRead(p, r) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (e *Engine) mutate(ctx context.Context, scope, id string, fn func(*Run) error) (Run, error) {
	for i := 0; i < maxCASRetries; i++ {
		r, err := e.Store.GetRun(ctx, scope, id)
		if err != nil {
			return Run{}, err
		}
		version := r.Version
		if err := fn(&r); err != nil {
			return Run{}, err
		}
		r.Version++
		r.UpdatedAtMS = e.now()
		out, err := e.Store.SaveRun(ctx, version, r)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return out, err
	}
	return Run{}, ErrConflict
}
func (e *Engine) Approve(ctx context.Context, p Principal, id string, version uint64, digest string) (Run, error) {
	if !p.LocalAdmin {
		return Run{}, ErrDenied
	}
	return e.mutate(ctx, p.Scope, id, func(r *Run) error {
		if !CanRead(p, *r) {
			return ErrDenied
		}
		if r.Version != version || r.GrantHash != digest || r.Status != AwaitingApproval {
			return ErrConflict
		}
		if e.Authority.Current(ctx, p, r.Grant) != nil || r.Grant.ExpiresAtMS <= e.now() {
			return ErrDenied
		}
		r.Approved = true
		r.Status = Queued
		return nil
	})
}
func (e *Engine) Cancel(ctx context.Context, p Principal, id string, version uint64) (Run, error) {
	return e.mutate(ctx, p.Scope, id, func(r *Run) error {
		if !CanRead(p, *r) {
			return ErrDenied
		}
		if version > 0 && r.Version != version {
			return ErrConflict
		}
		if IsTerminal(r.Status) {
			return ErrConflict
		}
		r.Status = Cancelling
		r.SafeCode = "cancel_requested"
		running := false
		for i := range r.Jobs {
			switch r.Jobs[i].Status {
			case JobRunning:
				running = true
			case JobQueued:
				r.Jobs[i].Status = JobCancelled
			}
		}
		if r.VerificationClaim.Token != "" {
			running = true
		}
		for i := range r.Notices {
			if r.Notices[i].Status == DeliverySending {
				running = true
			} else if r.Notices[i].Status == DeliveryQueued || r.Notices[i].Status == DeliveryRetry {
				r.Notices[i].Status = DeliveryDead
				r.Notices[i].SafeCode = "cancelled"
			}
		}
		if !running {
			r.Status = Cancelled
		}
		return nil
	})
}
func (e *Engine) Retry(ctx context.Context, p Principal, id string, version uint64) (Run, error) {
	if !p.LocalAdmin {
		return Run{}, ErrDenied
	}
	return e.mutate(ctx, p.Scope, id, func(r *Run) error {
		if !CanRead(p, *r) {
			return ErrDenied
		}
		if r.Version != version || r.Status != Failed {
			return ErrConflict
		}
		if e.Authority.Current(ctx, p, r.Grant) != nil || r.Grant.ExpiresAtMS <= e.now() {
			return ErrDenied
		}
		for _, j := range r.Jobs {
			if j.Status == JobRunning || j.Status == JobUnknown || j.Attempts >= MaxAttempts {
				return ErrDenied
			}
		}
		for i := range r.Jobs {
			if r.Jobs[i].Status != JobSucceeded {
				r.Jobs[i].Status = JobQueued
				r.Jobs[i].SafeCode = ""
			}
		}
		r.Status = Queued
		r.Verification = nil
		r.SafeCode = ""
		return nil
	})
}

// Serve owns goroutines only for this Super engine. Legacy sessions are not used.
func (e *Engine) Serve(ctx context.Context) error {
	if e.Store == nil || e.Executor == nil || e.Verifier == nil || e.Sender == nil || e.Authority == nil {
		return ErrUnavailable
	}
	n := e.Workers
	if e.WorkerLimit != nil {
		n = MaxWorkers
	}
	if n == 0 {
		n = DefaultWorkers
	}
	if n < 1 || n > MaxWorkers {
		return ErrInvalid
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		index := i
		go func() {
			defer wg.Done()
			owner := uuid.NewString()
			for ctx.Err() == nil {
				if e.WorkerLimit != nil && index >= e.WorkerLimit() {
					select {
					case <-ctx.Done():
						return
					case <-time.After(100 * time.Millisecond):
						continue
					}
				}
				worked, err := e.Step(ctx, owner)
				if err != nil && !errors.Is(err, context.Canceled) {
					e.logger().Warn("Super worker step refused", "action", "worker.step", "code", "step_failed")
				}
				if !worked {
					select {
					case <-ctx.Done():
						return
					case <-time.After(100 * time.Millisecond):
					}
				}
			}
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}
