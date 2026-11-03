package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"github.com/konglong87/super-niuma/internal/super/sqlitestore"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

type operatorAuthority struct {
	seen []control.Principal
	deny bool
}

func (a *operatorAuthority) Current(_ context.Context, p control.Principal, _ control.Grant) error {
	a.seen = append(a.seen, p)
	if a.deny || p.Scope != p.Owner {
		return control.ErrDenied
	}
	return nil
}
func operatorFixture(t *testing.T) (*Service, *sqlitestore.Store, *operatorAuthority, control.Grant) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(ctx, filepath.Join(root, "state", "operator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	svc := New(store, filepath.Join(root, "config"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(svc.Close)
	authority := &operatorAuthority{}
	svc.Engine.Authority = authority
	grant := control.Grant{ProfileID: "fixture", Revision: "v1", Workspace: root, Provider: "fixture", Model: "fixture-model", MaxTokens: 10000, TimeoutMS: 10000, ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), Nodes: []control.Node{{ID: "job", Instruction: "Never execute in operator tests", ReadPaths: []string{"inputs"}, MaxTurns: 1, MaxOutputTokens: 64}}, Verification: verification.Policy{Version: "v1", Rules: []verification.Rule{{Path: "artifacts/result.txt", Kind: verification.KindTextContains, Expected: "done"}}}, Targets: []control.Target{{ID: "desktop", Provider: control.ProviderDesktop}}}
	return svc, store, authority, grant
}
func operatorStart(t *testing.T, s *Service, g control.Grant, scope, key string) control.Run {
	t.Helper()
	r, created, err := s.Engine.Start(context.Background(), control.Principal{Scope: scope, Owner: scope}, key, "offline operator fixture", "", g, nil)
	if err != nil || !created {
		t.Fatalf("start created=%v err=%v", created, err)
	}
	return r
}

func TestOperatorCrossScopeApprovalCASAndRevokedConfigCancellation(t *testing.T) {
	s, store, authority, g := operatorFixture(t)
	ctx := context.Background()
	r := operatorStart(t, s, g, "alice", "approval")
	snap, err := s.Snapshot(ctx)
	if err != nil || len(snap.Runs) != 1 || snap.Runs[0].Scope != "alice" || snap.Runs[0].Status != control.AwaitingApproval {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
	if _, err = s.ApproveRun(ctx, r.ID, r.Version+1, r.GrantHash); !errors.Is(err, control.ErrConflict) {
		t.Fatal("stale approval accepted", err)
	}
	if _, err = s.ApproveRun(ctx, r.ID, r.Version, "wrong-hash"); !errors.Is(err, control.ErrConflict) {
		t.Fatal("wrong consent accepted", err)
	}
	approved, err := s.ApproveRun(ctx, r.ID, r.Version, r.GrantHash)
	if err != nil || approved.Status != control.Queued {
		t.Fatalf("approve=%+v err=%v", approved, err)
	}
	p := authority.seen[len(authority.seen)-1]
	if p.Scope != "alice" || p.Owner != "alice" || !p.LocalAdmin {
		t.Fatalf("principal not derived from durable run: %+v", p)
	}
	if _, err = s.Engine.Cancel(ctx, control.Principal{Scope: "alice", Owner: "alice"}, r.ID, r.Version); !errors.Is(err, control.ErrConflict) {
		t.Fatal("external stale cancellation accepted", err)
	}
	// No profiles/bindings/scopes survive. History and stop controls must still work;
	// actual execution authority is revoked, not inherited from LocalAdmin.
	if _, err = s.config.save(`{"version":1,"workers":1,"profiles":[],"bindings":[],"accounts":[]}`); err != nil {
		t.Fatal(err)
	}
	authority.deny = true
	snap, err = s.Snapshot(ctx)
	if err != nil || len(snap.Runs) != 1 || snap.Runs[0].ID != r.ID {
		t.Fatalf("removed binding hid history %+v %v", snap, err)
	}
	cancelled, err := s.CancelRun(ctx, r.ID, r.Version)
	if err != nil || cancelled.Status != control.Cancelled {
		t.Fatalf("revoked run cannot cancel %+v %v", cancelled, err)
	}
	again, err := s.CancelRun(ctx, r.ID, r.Version)
	if err != nil || again.Version != cancelled.Version {
		t.Fatal("stop not idempotent", err)
	}
	saved, err := store.GetRun(ctx, "alice", r.ID)
	if err != nil || saved.Status != control.Cancelled || saved.Version != cancelled.Version {
		t.Fatal("cancel not persisted", err)
	}
}

func TestOperatorRouterNonAdminStillScoped(t *testing.T) {
	s, store, authority, g := operatorFixture(t)
	ctx := context.Background()
	bindings := []router.Binding{
		{ID: "binding-alice", Provider: control.ProviderWebhook, Account: "acct", Chat: "chat-alice", Sender: "sender-alice", Scope: "alice", Owner: "alice", ProfileID: g.ProfileID, Enabled: true},
		{ID: "binding-bob", Provider: control.ProviderWebhook, Account: "acct", Chat: "chat-bob", Sender: "sender-bob", Scope: "bob", Owner: "bob", ProfileID: g.ProfileID, Enabled: true},
	}
	gateway := router.New(s.Engine, func() []router.Binding { return bindings }, func(_ context.Context, p control.Principal, _ string) (control.Grant, error) {
		if p.LocalAdmin {
			t.Fatal("router escalated")
		}
		return g, nil
	}, nil)
	envelope := func(who, id, text string) router.Envelope {
		return router.Envelope{Provider: control.ProviderWebhook, Account: "acct", EventID: "event-" + id, MessageID: "message-" + id, Chat: "chat-" + who, Sender: "sender-" + who, Text: text, Authenticated: true, ThreadKnown: true}
	}
	decision, err := gateway.Handle(ctx, envelope("alice", "run", "/run offline task"))
	if err != nil || decision.Action != router.ActionRun || decision.UsedLLM {
		t.Fatalf("router admission %+v %v", decision, err)
	}
	r, err := store.GetRun(ctx, "alice", decision.RunID)
	if err != nil || r.Status != control.AwaitingApproval || r.Approved {
		t.Fatalf("autoapproval expanded %+v %v", r, err)
	}
	for _, command := range []string{"/status ", "/cancel "} {
		d, err := gateway.Handle(ctx, envelope("bob", command[1:7], command+r.ID))
		if err == nil || d.Action != router.ActionRejected || d.UsedLLM {
			t.Fatalf("cross-scope router access %+v %v", d, err)
		}
	}
	d, err := gateway.Handle(ctx, envelope("alice", "approve", "/approve "+r.ID))
	if !errors.Is(err, control.ErrDenied) || d.Code != router.CodeCommand {
		t.Fatalf("external approve accepted %+v %v", d, err)
	}
	if _, err = s.Engine.Approve(ctx, control.Principal{Scope: "alice", Owner: "alice"}, r.ID, r.Version, r.GrantHash); !errors.Is(err, control.ErrDenied) {
		t.Fatal("nonadmin approved", err)
	}
	if _, err = s.ApproveRun(ctx, r.ID, r.Version, r.GrantHash); err != nil {
		t.Fatal("trusted operator could not approve", err)
	}
	last := authority.seen[len(authority.seen)-1]
	if !last.LocalAdmin || last.Scope != "alice" || last.Owner != "alice" {
		t.Fatal("bad operator principal")
	}
	// Disabling/removing the authenticated route does not hide the durable run.
	bindings = nil
	authority.deny = true
	snap, err := s.Snapshot(ctx)
	if err != nil || len(snap.Runs) != 1 {
		t.Fatal("binding removal hid run", err)
	}
	if _, err = s.CancelRun(ctx, r.ID, snap.Runs[0].Version); err != nil {
		t.Fatal("old run could not cancel", err)
	}
}

func TestOperatorScopeCollisionDeniesEveryManagementAction(t *testing.T) {
	s, store, _, g := operatorFixture(t)
	ctx := context.Background()
	alice := operatorStart(t, s, g, "alice", "collision")
	duplicate := alice
	duplicate.Scope = "bob"
	duplicate.Owner = "bob"
	if _, created, err := store.CreateRun(ctx, duplicate, nil); err != nil || !created {
		t.Fatalf("fixture collision %v %v", created, err)
	}
	for _, action := range []func() (control.Run, error){
		func() (control.Run, error) { return s.ApproveRun(ctx, alice.ID, alice.Version, alice.GrantHash) },
		func() (control.Run, error) { return s.CancelRun(ctx, alice.ID, alice.Version) },
		func() (control.Run, error) { return s.RetryRun(ctx, alice.ID, alice.Version) },
		func() (control.Run, error) {
			return s.AbandonRun(ctx, alice.ID, alice.Version, control.StopConfirmation)
		},
	} {
		if _, err := action(); !errors.Is(err, control.ErrConflict) {
			t.Fatalf("ambiguous operator action: %v", err)
		}
	}
	if _, err := s.Snapshot(ctx); !errors.Is(err, control.ErrConflict) {
		t.Fatal("ambiguous snapshot", err)
	}
	for _, scope := range []string{"alice", "bob"} {
		r, err := store.GetRun(ctx, scope, alice.ID)
		if err != nil || r.Status != control.AwaitingApproval || r.Version != alice.Version {
			t.Fatal("collision mutated a scope", err)
		}
	}
}

func TestOperatorSnapshotGlobalRecentLimitAndMore(t *testing.T) {
	s, _, _, g := operatorFixture(t)
	ctx := context.Background()
	var newest string
	for i := 0; i < control.MaxRuns+1; i++ {
		scope := []string{"alice", "bob"}[i%2]
		r := operatorStart(t, s, g, scope, fmt.Sprintf("history-%03d", i))
		if _, err := s.CancelRun(ctx, r.ID, r.Version); err != nil {
			t.Fatal(err)
		}
		newest = r.ID
	}
	snap, err := s.Snapshot(ctx)
	if err != nil || !snap.HasMoreRuns || len(snap.Runs) != control.MaxRuns || snap.Runs[0].ID != newest {
		t.Fatalf("history count=%d more=%v err=%v", len(snap.Runs), snap.HasMoreRuns, err)
	}
	if snap.Runs[0].Scope != "alice" || snap.Runs[1].Scope != "bob" {
		t.Fatal("history is still scoped")
	}
}

type scopedOnlyRepository struct{ control.Repository }

func TestOperatorMissingTrustedPortFailsClosed(t *testing.T) {
	s, store, _, _ := operatorFixture(t)
	s.Engine.Store = scopedOnlyRepository{store}
	if _, err := s.Snapshot(context.Background()); !errors.Is(err, control.ErrUnavailable) {
		t.Fatal("silent local-only snapshot fallback", err)
	}
	if _, err := s.CancelRun(context.Background(), "id", 1); !errors.Is(err, control.ErrUnavailable) {
		t.Fatal("silent local mutation fallback", err)
	}
}

func TestOperatorEmergencyStopIgnoresHeartbeatVersionAndIsIdempotent(t *testing.T) {
	s, store, _, g := operatorFixture(t)
	ctx := context.Background()
	r := operatorStart(t, s, g, "alice", "heartbeat-stop")
	r, err := s.ApproveRun(ctx, r.ID, r.Version, r.GrantHash)
	if err != nil {
		t.Fatal(err)
	}
	snapshotVersion := r.Version
	running := r
	running.Version++
	running.UpdatedAtMS = time.Now().UnixMilli()
	running.Status = control.Running
	running.Jobs = append([]control.Job(nil), r.Jobs...)
	running.Jobs[0].Status = control.JobRunning
	running.Jobs[0].Attempts = 1
	running.Jobs[0].StartedAtMS = running.UpdatedAtMS
	running.Jobs[0].Claim = control.Claim{Scope: r.Scope, RunID: r.ID, JobID: r.Jobs[0].Spec.ID, Owner: "fixture-worker", Token: "fixture-claim", Epoch: 1, UntilMS: time.Now().Add(time.Minute).UnixMilli()}
	r, err = store.SaveRun(ctx, r.Version, running)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := r
	heartbeat.Version++
	heartbeat.UpdatedAtMS = time.Now().UnixMilli()
	heartbeat.Jobs = append([]control.Job(nil), r.Jobs...)
	heartbeat.Jobs[0].Claim.UntilMS += 1000
	r, err = store.SaveRun(ctx, r.Version, heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Engine.Cancel(ctx, control.Principal{Scope: "alice", Owner: "alice"}, r.ID, snapshotVersion); !errors.Is(err, control.ErrConflict) {
		t.Fatal("external CAS bypassed", err)
	}
	stopped, err := s.CancelRun(ctx, r.ID, snapshotVersion)
	if err != nil || stopped.Status != control.Cancelling {
		t.Fatalf("heartbeat defeated emergency stop: %+v %v", stopped, err)
	}
	again, err := s.CancelRun(ctx, r.ID, snapshotVersion)
	if err != nil || again.Version != stopped.Version || again.Status != control.Cancelling {
		t.Fatal("pending stop not idempotent", err)
	}
	if err = store.CheckClaim(ctx, r.Jobs[0].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatal("cancelled root retained execution authority", err)
	}
}
