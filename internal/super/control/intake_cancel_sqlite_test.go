package control_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

// A run whose work order was cancelled can never claim again. It must be
// stopped with a visible reason and must not starve runs ordered after it.
func TestSQLiteIntegrationCancelledIntakeStopsRunWithoutStarvation(t *testing.T) {
	h := newSQLiteHarness(t)
	a := h.start(t, "intake-a", h.grant())
	b := h.start(t, "intake-b", h.grant())
	runs := []control.Run{a, b}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	stopped, healthy := runs[0], runs[1]
	if _, err := workorder.NewService(h.store, nil).Cancel(context.Background(), stopped.IntakeID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		h.step(t, "worker")
	}
	got := h.get(t, stopped.ID)
	if got.Status != control.Cancelled || got.SafeCode != control.SafeIntakeCancelled || got.Jobs[0].Status != control.JobCancelled || got.Jobs[0].Attempts != 0 {
		t.Fatalf("cancelled intake run status=%s code=%s jobs=%v", got.Status, got.SafeCode, sqliteJobStates(got))
	}
	if other := h.get(t, healthy.ID); other.Jobs[0].Attempts == 0 {
		t.Fatalf("healthy run starved: status=%s jobs=%v", other.Status, sqliteJobStates(other))
	}
}

func TestSQLiteIntegrationRevokedAuthorityKeepsVisibleReason(t *testing.T) {
	h := newSQLiteHarness(t)
	r := h.start(t, "revoked-reason", h.grant())
	h.authority.revoked.Store(true)
	h.step(t, "worker")
	got := h.get(t, r.ID)
	if got.Status != control.Cancelled || got.SafeCode != control.SafeAuthorityRevoked {
		t.Fatalf("status=%s code=%s", got.Status, got.SafeCode)
	}
}

// Cancelling the work order while a job runs stops the executor through its
// heartbeat; that is a cancellation, not a failure or an unknown outcome.
func TestSQLiteIntegrationIntakeCancelledDuringExecutionSettlesCancelled(t *testing.T) {
	h := newSQLiteHarness(t)
	started := make(chan struct{}, 1)
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		started <- struct{}{}
		<-ctx.Done()
		return execution.Result{UsageKnown: false}, ctx.Err()
	})
	r := h.start(t, "intake-running", h.grant(sqliteNode("write", nil, nil, []string{"result.txt"}), sqliteNode("after", []string{"write"}, nil, nil)))
	done := make(chan error, 1)
	go func() { _, err := h.engine.Step(context.Background(), "worker"); done <- err }()
	<-started
	if _, err := workorder.NewService(h.store, nil).Cancel(context.Background(), r.IntakeID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := h.get(t, r.ID)
	if got.Status != control.Cancelled || got.SafeCode != control.SafeIntakeCancelled || got.Jobs[0].Status != control.JobCancelled || got.Jobs[1].Status != control.JobCancelled {
		t.Fatalf("status=%s code=%s jobs=%v", got.Status, got.SafeCode, sqliteJobStates(got))
	}
}

type intakeCancellingVerifier struct {
	cancel func()
}

func (v intakeCancellingVerifier) Verify(context.Context, string, verification.Policy) (verification.Report, error) {
	v.cancel()
	return verification.Report{}, errors.New("fixture verifier outcome after cancellation")
}

func TestSQLiteIntegrationIntakeCancelledDuringVerificationSettlesCancelled(t *testing.T) {
	h := newSQLiteHarness(t)
	var r control.Run
	h.engine.Verifier = intakeCancellingVerifier{cancel: func() {
		if _, err := workorder.NewService(h.store, nil).Cancel(context.Background(), r.IntakeID); err != nil {
			t.Error(err)
		}
	}}
	r = h.start(t, "intake-verifying", h.grant())
	for i := 0; i < 3; i++ {
		h.step(t, "worker")
	}
	got := h.get(t, r.ID)
	if got.Status != control.Cancelled || got.SafeCode != control.SafeIntakeCancelled || got.VerificationClaim.Token != "" {
		t.Fatalf("status=%s code=%s", got.Status, got.SafeCode)
	}
}
