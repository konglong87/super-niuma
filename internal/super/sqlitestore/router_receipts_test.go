package sqlitestore

import (
	"context"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"testing"
)

func routerReceiptStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := openControlTestStore(t)
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = MigrateRouter(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s, path
}
func routerRequest(r control.Run) router.CommandRequest {
	return router.CommandRequest{Principal: control.Principal{Scope: r.Scope, Owner: r.Owner}, Ingress: control.Ingress{Account: "account", EventID: "event", MessageID: "message", Nonce: "nonce", ContentHash: control.Digest("status")}, RunID: r.ID, Action: router.ActionStatus, ExpectedVersion: r.Version}
}
func TestRouterReceiptPersistsReplaysAndSharedIngressAuthority(t *testing.T) {
	s, path := routerReceiptStore(t)
	ctx := context.Background()
	run := createControlRun(t, s, controlFixture("router-run", "scope", "/work", false))
	q := routerRequest(run)
	receipt, err := s.BeginCommand(ctx, q)
	if err != nil || receipt.Version != 1 || receipt.Decision != nil {
		t.Fatalf("%+v %v", receipt, err)
	}
	decision := router.Decision{Action: router.ActionStatus, RunID: run.ID, Code: "status_queued"}
	if err = s.FinishCommand(ctx, receipt, decision); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// New delivery aliases on the same message share the original receipt.
	q.Ingress.EventID = "event-retry"
	q.Ingress.Nonce = "nonce-retry"
	replay, err := reopened.BeginCommand(ctx, q)
	if err != nil || replay.Decision == nil || *replay.Decision != decision {
		t.Fatalf("%+v %v", replay, err)
	}
	if got := controlCount(t, s, "super_ingress"); got != 5 {
		t.Fatalf("shared ingress keys=%d", got)
	}
	if got := controlCount(t, s, "super_router_receipts"); got != 1 {
		t.Fatalf("receipts=%d", got)
	}
	// A run may not reuse a command nonce/event/message under different content.
	other := controlFixture("other-run", "scope", "/work", false)
	in := q.Ingress
	in.ContentHash = control.Digest("different")
	if _, _, err = s.CreateRun(ctx, other, &in); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("separate authority accepted collision: %v", err)
	}
}
func TestRouterReceiptRejectsOwnerScopeContentAndActionConflicts(t *testing.T) {
	s, _ := routerReceiptStore(t)
	ctx := context.Background()
	r := createControlRun(t, s, controlFixture("run", "scope", "/work", false))
	q := routerRequest(r)
	original, err := s.BeginCommand(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"owner", "scope", "content", "action"} {
		t.Run(kind, func(t *testing.T) {
			bad := q
			switch kind {
			case "owner":
				bad.Principal.Owner = "other"
			case "scope":
				bad.Principal.Scope = "other"
			case "content":
				bad.Ingress.ContentHash = control.Digest("changed")
			case "action":
				bad.Action = router.ActionCancel
			}
			if _, err := s.BeginCommand(ctx, bad); err == nil {
				t.Fatal("conflict accepted")
			}
		})
	}
	d := router.Decision{RunID: r.ID, Action: router.ActionStatus, Code: "status_queued", UsedLLM: true}
	if err = s.FinishCommand(ctx, original, d); !errors.Is(err, control.ErrInvalid) {
		t.Fatalf("unsafe decision=%v", err)
	}
	d.UsedLLM = false
	if err = s.FinishCommand(ctx, original, d); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishCommand(ctx, original, d); err != nil {
		t.Fatal(err)
	}
	d.Code = "status_failed"
	if err = s.FinishCommand(ctx, original, d); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("receipt overwrite=%v", err)
	}
}
func TestRouterReceiptPendingSurvivesRestartAndRetainsRevision(t *testing.T) {
	s, path := routerReceiptStore(t)
	ctx := context.Background()
	r := createControlRun(t, s, controlFixture("run", "scope", "/work", false))
	q := routerRequest(r)
	q.Action = router.ActionCancel
	first, err := s.BeginCommand(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	next := nextControlRun(r)
	next.Status = control.Cancelled
	next.Jobs[0].Status = control.JobCancelled
	saveControlRun(t, s, next)
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	q.ExpectedVersion = next.Version
	pending, err := reopened.BeginCommand(ctx, q)
	if err != nil || pending.Decision != nil || pending.Version != first.Version || pending.Request.ExpectedVersion != first.Version {
		t.Fatalf("lost original revision %+v %v", pending, err)
	}
}
func TestRouterMigrationRollsBackWithCoordinator(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	// Open now installs v3, including this table. Remove only the empty table
	// in this test-owned temporary database to exercise migration rollback.
	if _, err := s.db.ExecContext(ctx, "DROP TABLE super_router_receipts"); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = MigrateRouter(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='super_router_receipts'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("nontransactional schema %d %v", n, err)
	}
}
