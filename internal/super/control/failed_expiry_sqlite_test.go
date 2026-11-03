package control_test

// Real SQLite/Engine/resource-fence regression; executors are local fixtures.
// No model, application, channel transport or repository test script is invoked.
import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

const (
	failedReadJob  = "known-failure"
	crashedReadJob = "crashed-reader"
)

func knownReadFailure(ctx context.Context, req execution.Request) (execution.Result, error) {
	result := execution.Result{
		Diagnostic: &execution.Diagnostic{Code: execution.CodeInvalidRequest, FailureStage: execution.StageRequest, UsageState: execution.UsageKnown},
		Usage:      execution.Usage{InputTokens: 5, OutputTokens: 3}, UsageKnown: true,
	}
	id := req.AttemptID + ".known-request"
	if err := req.Budget.Reserve(ctx, id, 20, 10); err != nil {
		return result, err
	}
	if err := req.Budget.Settle(ctx, id, result.Usage, true); err != nil {
		return result, err
	}
	return result, execution.ErrInvalid
}

func expiryClone(t *testing.T, r control.Run) control.Run {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var next control.Run
	if err = json.Unmarshal(data, &next); err != nil {
		t.Fatal(err)
	}
	return next
}

func expiryCandidate(t *testing.T, r control.Run) control.Run {
	t.Helper()
	next := expiryClone(t, r)
	next.Version++
	next.UpdatedAtMS = time.Now().UnixMilli()
	next.Status = control.NeedsAttention
	next.SafeCode = "lease_lost"
	return next
}

func assertExpiryWriteRejected(t *testing.T, h *sqliteHarness, before, candidate control.Run) {
	t.Helper()
	if _, err := h.store.SaveRun(context.Background(), before.Version, candidate); err == nil {
		t.Fatal("unproven or evidence-changing terminal recovery accepted")
	}
	if control.Digest(h.get(t, before.ID)) != control.Digest(before) {
		t.Fatal("rejected recovery changed persisted evidence")
	}
}

func TestSQLiteIntegrationFailedReadJobExpiredSiblingRecovery(t *testing.T) {
	h := newSQLiteHarness(t)
	ctx := context.Background()
	var calls atomic.Int32
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, req execution.Request) (execution.Result, error) {
		calls.Add(1)
		if req.JobID != failedReadJob {
			return execution.Result{}, errors.New("crashed job unexpectedly executed")
		}
		// The other read claim must really coexist in SQLite with this claim.
		r, err := h.store.GetRun(ctx, control.LocalScope, req.RunID)
		if err != nil {
			return execution.Result{}, err
		}
		if r.Jobs[0].Status != control.JobRunning || r.Jobs[1].Status != control.JobRunning {
			return execution.Result{}, errors.New("parallel read claims were not persisted")
		}
		return knownReadFailure(ctx, req)
	})
	r := h.start(t, "failed-read-expired-sibling", h.grant(
		sqliteNode(failedReadJob, nil, []string{"src"}, nil),
		sqliteNode(crashedReadJob, nil, []string{"src"}, nil),
	))
	// Persist B's actual resource claim/reservation, then simulate its process
	// dying. No executor exists for B; the later manual stop attestation is true.
	claimed := expiryClone(t, r)
	claimed.Version++
	claimed.UpdatedAtMS = time.Now().UnixMilli()
	claimed.Status = control.Running
	claim := control.Claim{Scope: r.Scope, RunID: r.ID, JobID: crashedReadJob, Owner: "crashed-owner", Token: "crashed-read-claim", Epoch: 1, UntilMS: time.Now().Add(time.Second).UnixMilli()}
	claimed.Jobs[1].Status = control.JobRunning
	claimed.Jobs[1].Attempts = 1
	claimed.Jobs[1].Claim = claim
	claimed.Jobs[1].StartedAtMS = claimed.UpdatedAtMS
	claimed.Reservations = []control.Reservation{{ID: "unknown-read-request", JobID: crashedReadJob, AttemptID: claim.Token, Input: 90, Output: 10}}
	if _, err := h.store.SaveRun(ctx, r.Version, claimed); err != nil {
		t.Fatal(err)
	}
	h.step(t, "known-failure-worker")
	failed := h.get(t, r.ID)
	if failed.Status != control.Failed || failed.Jobs[0].Status != control.JobFailed || failed.Jobs[1].Status != control.JobRunning || !failed.Jobs[0].Result.UsageKnown || failed.Jobs[0].Result.Diagnostic == nil || control.ReservedTokens(failed) != 108 {
		t.Fatalf("failure facts not persisted: status=%s jobs=%v budget=%d", failed.Status, sqliteJobStates(failed), control.ReservedTokens(failed))
	}
	if time.Now().UnixMilli() >= claim.UntilMS {
		t.Fatal("fixture lease expired before known failure settled")
	}
	premature := expiryCandidate(t, failed)
	premature.Jobs[1].Status, premature.Jobs[1].SafeCode = control.JobUnknown, "lease_lost"
	assertExpiryWriteRejected(t, h, failed, premature)
	if _, err := h.engine.Retry(ctx, h.principal, failed.ID, failed.Version); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("running sibling allowed blind retry: %v", err)
	}
	time.Sleep(time.Until(time.UnixMilli(claim.UntilMS)) + 10*time.Millisecond)
	for _, tc := range []struct {
		name   string
		change func(*control.Run)
	}{
		{"missing_unknown", func(n *control.Run) {}},
		{"release_claim", func(n *control.Run) {
			n.Jobs[1].Status = control.JobCancelled
			n.Jobs[1].FinishedAtMS = time.Now().UnixMilli()
		}},
		{"replace_claim", func(n *control.Run) {
			n.Jobs[1].Status = control.JobUnknown
			n.Jobs[1].SafeCode = "lease_lost"
			n.Jobs[1].Claim.Token = "forged-token"
		}},
		{"erase_failure_usage", func(n *control.Run) {
			n.Jobs[1].Status = control.JobUnknown
			n.Jobs[1].SafeCode = "lease_lost"
			n.Jobs[0].Result = execution.Result{}
		}},
		{"refund_unknown", func(n *control.Run) {
			n.Jobs[1].Status = control.JobUnknown
			n.Jobs[1].SafeCode = "lease_lost"
			n.Reservations[0].Settled = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := expiryCandidate(t, failed)
			tc.change(&next)
			assertExpiryWriteRejected(t, h, failed, next)
		})
	}
	h.reopen(t)
	h.step(t, "restart-recovery")
	recovered := h.get(t, r.ID)
	if recovered.Status != control.NeedsAttention || recovered.Jobs[1].Status != control.JobUnknown || recovered.Jobs[1].Claim != claim || recovered.Jobs[1].Attempts != 1 || calls.Load() != 1 {
		t.Fatalf("expired failed root not quarantined: status=%s jobs=%v calls=%d", recovered.Status, sqliteJobStates(recovered), calls.Load())
	}
	if control.Digest(failed.Jobs[0]) != control.Digest(recovered.Jobs[0]) || control.Digest(failed.Reservations) != control.Digest(recovered.Reservations) || control.Digest(failed.Notices) != control.Digest(recovered.Notices) {
		t.Fatal("recovery changed failure diagnostic, usage, reservations or receipts")
	}
	listed, err := h.engine.List(ctx, h.principal)
	if err != nil {
		t.Fatal(err)
	}
	visible := false
	for _, item := range listed {
		if item.ID == r.ID && item.Status == control.NeedsAttention {
			visible = true
		}
	}
	if !visible {
		t.Fatal("needs_attention root not visible to owner")
	}
	if err = h.store.CheckClaim(ctx, claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("unknown claim still authorized: %v", err)
	}
	if _, err = h.engine.Retry(ctx, h.principal, r.ID, recovered.Version); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("unknown work blindly retried: %v", err)
	}

	successor := h.start(t, "failed-read-successor", h.grant())
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, req execution.Request) (execution.Result, error) {
		calls.Add(1)
		return sqliteWork(ctx, req)
	})
	h.reopen(t)
	worked, err := h.engine.Step(ctx, "blocked-writer")
	if err != nil || worked || calls.Load() != 1 || h.get(t, successor.ID).Jobs[0].Attempts != 0 {
		t.Fatalf("unknown read claim released before inspection: worked=%v err=%v", worked, err)
	}
	recovered = h.get(t, r.ID)
	abandoned, err := h.engine.AbandonAfterInspection(ctx, h.principal, r.ID, recovered.Version, control.StopConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	sqliteAssertOperatorResolution(t, recovered, abandoned)
	if abandoned.Jobs[1].Claim != claim || control.Digest(abandoned.Reservations) != control.Digest(failed.Reservations) || control.Digest(abandoned.Jobs[0]) != control.Digest(failed.Jobs[0]) {
		t.Fatal("manual abandon erased evidence or refunded unknown usage")
	}
	h.reopen(t)
	if _, err = h.engine.Retry(ctx, h.principal, r.ID, abandoned.Version); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("abandoned root retried: %v", err)
	}
	worked, err = h.engine.Step(ctx, "writer-after-confirmed-stop")
	if err != nil || !worked || calls.Load() != 2 || h.get(t, successor.ID).Jobs[0].Status != control.JobSucceeded || h.sender.count() != 0 {
		t.Fatalf("confirmed stop did not release successor only: worked=%v err=%v calls=%d", worked, err, calls.Load())
	}
}

func TestSQLiteIntegrationFailedReadJobLateExpiredSettlement(t *testing.T) {
	h := newSQLiteHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var releaseOnce sync.Once
	late := execution.Result{Diagnostic: &execution.Diagnostic{Code: execution.CodeTimeout, FailureStage: execution.StageNetwork, UsageState: execution.UsageKnown}, Usage: execution.Usage{InputTokens: 7, OutputTokens: 2}, UsageKnown: true}
	h.engine.Lease = time.Second
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, req execution.Request) (execution.Result, error) {
		if req.JobID == failedReadJob {
			return knownReadFailure(ctx, req)
		}
		if err := req.Budget.Reserve(ctx, req.AttemptID+".uncertain", 90, 10); err != nil {
			close(started)
			return execution.Result{}, err
		}
		close(started)
		<-release // simulate a delayed uncooperative callback, never a real model
		return late, execution.ErrFailed
	})
	r := h.start(t, "failed-read-late-settlement", h.grant(
		sqliteNode(crashedReadJob, nil, []string{"src"}, nil),
		sqliteNode(failedReadJob, nil, []string{"src"}, nil),
	))
	go func() { _, err := h.engine.Step(ctx, "late-reader"); done <- err }()
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("late fixture did not stop")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("late reader did not start")
	}
	h.step(t, "failing-peer")
	failed := h.get(t, r.ID)
	if failed.Status != control.Failed || failed.Jobs[0].Status != control.JobRunning || failed.Jobs[1].Status != control.JobFailed {
		t.Fatalf("failed peer fixture: status=%s jobs=%v", failed.Status, sqliteJobStates(failed))
	}
	claim := failed.Jobs[0].Claim
	time.Sleep(time.Until(time.UnixMilli(claim.UntilMS)) + 10*time.Millisecond)
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil {
			done <- nil
			t.Fatal(err)
		}
		done <- nil // cleanup still joins the completed fixture
	case <-time.After(5 * time.Second):
		t.Fatal("late settlement did not finish")
	}
	recovered := h.get(t, r.ID)
	if recovered.Status != control.NeedsAttention || recovered.Jobs[0].Status != control.JobUnknown || recovered.Jobs[0].Claim != claim || control.Digest(recovered.Jobs[0].Result) != control.Digest(late) {
		t.Fatalf("late facts lost or expired claim revived: status=%s job=%+v", recovered.Status, recovered.Jobs[0])
	}
	if control.Digest(recovered.Jobs[1]) != control.Digest(failed.Jobs[1]) || control.Digest(recovered.Reservations) != control.Digest(failed.Reservations) || control.ReservedTokens(recovered) != 108 {
		t.Fatal("late settlement changed peer facts or refunded unknown request")
	}
	h.reopen(t)
	if control.Digest(h.get(t, r.ID)) != control.Digest(recovered) {
		t.Fatal("late expiry facts did not survive restart")
	}
}

func TestSQLiteIntegrationFailedRootWithoutExpiredClaimCannotEscalate(t *testing.T) {
	h := newSQLiteHarness(t)
	h.engine.Executor = sqliteExecutor(knownReadFailure)
	r := h.start(t, "failed-no-unsettled-claim", h.grant(sqliteNode(failedReadJob, nil, []string{"src"}, nil)))
	h.step(t, "failed-reader")
	failed := h.get(t, r.ID)
	if failed.Status != control.Failed {
		t.Fatalf("fixture status: %s", failed.Status)
	}
	assertExpiryWriteRejected(t, h, failed, expiryCandidate(t, failed))
}
