package control_test

import (
	"context"
	"sort"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/control"
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
