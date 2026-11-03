package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func openControlTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := openTestStore(t)
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = migrateControl(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s, path
}
func controlFixture(id, scope, workspace string, write bool) control.Run {
	now := time.Now().UnixMilli()
	n := control.Node{ID: "job", Instruction: "Produce result", ReadPaths: []string{"result.txt"}, MaxTurns: 2, MaxOutputTokens: 100}
	if write {
		n.WritePaths = []string{"result.txt"}
	}
	g := control.Grant{ProfileID: "profile", Revision: "v1", Workspace: workspace, Provider: "test", Model: "test", MaxTokens: 10000, TimeoutMS: 10000, ExpiresAtMS: now + 3600000, Nodes: []control.Node{n}, Verification: verification.Policy{Version: "v1", Rules: []verification.Rule{{Path: "result.txt", Kind: verification.KindTextContains, Expected: "accepted"}}}, Targets: []control.Target{{ID: "desktop", Provider: control.ProviderDesktop}}, AutoApprove: true}
	return control.Run{ID: id, Scope: scope, Owner: "owner", RequestKey: "request." + id, Objective: "private objective", Grant: g, GrantHash: control.Digest(g), Version: 1, Status: control.Queued, Approved: true, Jobs: []control.Job{{Spec: n, Status: control.JobQueued}}, Reservations: []control.Reservation{}, Notices: []control.Notice{}, CreatedAtMS: now, UpdatedAtMS: now}
}
func copyControlRun(r control.Run) control.Run {
	data, _ := json.Marshal(r)
	var out control.Run
	_ = json.Unmarshal(data, &out)
	return out
}
func createControlRun(t *testing.T, s *Store, r control.Run) control.Run {
	t.Helper()
	out, created, err := s.CreateRun(context.Background(), r, nil)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	return out
}
func nextControlRun(r control.Run) control.Run {
	r = copyControlRun(r)
	r.Version++
	r.UpdatedAtMS = time.Now().UnixMilli()
	return r
}
func saveControlRun(t *testing.T, s *Store, r control.Run) control.Run {
	t.Helper()
	out, err := s.SaveRun(context.Background(), r.Version-1, r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func claimControlRun(r control.Run, owner string) control.Run {
	r = nextControlRun(r)
	r.Status = control.Running
	j := &r.Jobs[0]
	j.Status = control.JobRunning
	j.Attempts++
	j.StartedAtMS = time.Now().UnixMilli()
	j.Claim = control.Claim{Scope: r.Scope, RunID: r.ID, JobID: j.Spec.ID, Owner: owner, Token: fmt.Sprintf("token.%s.%d", owner, j.Attempts), Epoch: j.Claim.Epoch + 1, UntilMS: time.Now().Add(time.Minute).UnixMilli()}
	return r
}
func controlCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func controlEventCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM super_events WHERE topic=?", control.EventRunChanged).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestControlMigrationAndScopedRead(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	for _, scope := range []string{"tenant-a", "tenant-b"} {
		createControlRun(t, s, controlFixture("same-id", scope, "/work", false))
	}
	got, err := s.ListRuns(ctx, "tenant-a")
	if err != nil || len(got) != 1 || got[0].Scope != "tenant-a" {
		t.Fatalf("scoped list: %#v %v", got, err)
	}
	if _, err = s.GetRun(ctx, "missing-scope", "same-id"); !errors.Is(err, control.ErrNotFound) {
		t.Fatal(err)
	}
	empty, err := s.ListRuns(ctx, "missing-scope")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty: %v %v", empty, err)
	}
	for _, bad := range []string{"", "scope\n", "scope/other"} {
		if _, err = s.ListRuns(ctx, bad); !errors.Is(err, control.ErrInvalid) {
			t.Fatalf("scope accepted: %q %v", bad, err)
		}
	}
	all, err := s.ActiveRuns(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("scheduler list: %d %v", len(all), err)
	}
	r := got[0]
	r.Owner = "different-owner"
	r.ID = "new-id"
	r.RequestKey = "new-key"
	if _, _, err = s.CreateRun(ctx, r, nil); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("mixed principal scope: %v", err)
	}
	r = nextControlRun(got[0])
	r.Owner = "different-owner"
	if _, err = s.SaveRun(ctx, 1, r); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("owner mutation: %v", err)
	}
}
func TestControlCreateReplayAndRestart(t *testing.T) {
	s, path := openControlTestStore(t)
	ctx := context.Background()
	r := controlFixture("one", "tenant", "/work", false)
	original := createControlRun(t, s, r)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replay := copyControlRun(r)
	replay.ID = "new-id"
	got, created, err := s.CreateRun(ctx, replay, nil)
	if err != nil || created || !reflect.DeepEqual(got, original) {
		t.Fatalf("replay: %v %v", created, err)
	}
	replay.Objective = "changed"
	if _, _, err = s.CreateRun(ctx, replay, nil); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("intent change: %v", err)
	}
	replay = copyControlRun(r)
	replay.Owner = "other"
	if _, _, err = s.CreateRun(ctx, replay, nil); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("owner replay: %v", err)
	}
	if controlEventCount(t, s) != 1 {
		t.Fatal("replay emitted business event")
	}
}
func TestControlCASAcrossIndependentStores(t *testing.T) {
	first, path := openControlTestStore(t)
	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	r := createControlRun(t, first, controlFixture("cas", "tenant", "/work", false))
	const n = 20
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			next := nextControlRun(r)
			next.SafeCode = fmt.Sprintf("worker_%d", i)
			_, err := []*Store{first, second}[i%2].SaveRun(context.Background(), r.Version, next)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, control.ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("CAS winners=%d", wins)
	}
	got, err := first.GetRun(context.Background(), r.Scope, r.ID)
	if err != nil || got.Version != 2 {
		t.Fatalf("revision: %d %v", got.Version, err)
	}
	if controlEventCount(t, first) != 1 {
		t.Fatal("metadata update emitted event")
	}
}
func TestControlAtomicCreateAndSaveRollback(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := controlFixture("atomic", "tenant", "/work", true)
	in := control.Ingress{Account: "account", EventID: "event", MessageID: "message", Nonce: "nonce", ContentHash: control.Digest("body")}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_control_event BEFORE INSERT ON super_events WHEN NEW.topic='super.run.changed' BEGIN SELECT RAISE(ABORT,'SECRET_PAYLOAD'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateRun(ctx, r, &in); !errors.Is(err, control.ErrUnavailable) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error: %v", err)
	}
	for _, table := range []string{"super_runs", "super_ingress", "super_resources"} {
		if controlCount(t, s, table) != 0 {
			t.Fatalf("partial %s", table)
		}
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_control_event"); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.CreateRun(ctx, r, &in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_control_event BEFORE INSERT ON super_events WHEN NEW.topic='super.run.changed' BEGIN SELECT RAISE(ABORT,'SECRET_PAYLOAD'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveRun(ctx, r.Version, claimControlRun(r, "worker")); !errors.Is(err, control.ErrUnavailable) {
		t.Fatal(err)
	}
	got, err := s.GetRun(ctx, r.Scope, r.ID)
	if err != nil || got.Version != 1 || got.Status != control.Queued || controlCount(t, s, "super_resources") != 0 || controlEventCount(t, s) != 1 {
		t.Fatalf("partial save: %#v %v", got, err)
	}
}
func TestControlBudgetAndRenewDoNotEmitEvents(t *testing.T) {
	s, _ := openControlTestStore(t)
	r := createControlRun(t, s, controlFixture("budget", "tenant", "/work", true))
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	before := controlEventCount(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER forbid_control_resource_churn BEFORE DELETE ON super_resources BEGIN SELECT RAISE(ABORT,'unnecessary lock churn'); END`); err != nil {
		t.Fatal(err)
	}
	n := nextControlRun(r)
	n.Reservations = []control.Reservation{{ID: "reservation", JobID: "job", AttemptID: r.Jobs[0].Claim.Token, Input: 100, Output: 100}}
	r = saveControlRun(t, s, n)
	n = nextControlRun(r)
	n.Reservations[0].Settled = true
	n.Reservations[0].Actual = execution.Usage{InputTokens: 10, OutputTokens: 10}
	r = saveControlRun(t, s, n)
	n = nextControlRun(r)
	n.Jobs[0].Claim.UntilMS += 1000
	r = saveControlRun(t, s, n)
	if controlEventCount(t, s) != before {
		t.Fatal("budget/renew event amplification")
	}
	if err := s.CheckClaim(context.Background(), r.Jobs[0].Claim); err != nil {
		t.Fatal(err)
	}
}
func TestControlRejectImmutableAndOversizedData(t *testing.T) {
	s, _ := openControlTestStore(t)
	r := createControlRun(t, s, controlFixture("immutable", "tenant", "/work", false))
	cases := []func(*control.Run){func(n *control.Run) { n.Objective = "new" }, func(n *control.Run) { n.Grant.Model = "changed"; n.GrantHash = control.Digest(n.Grant) }, func(n *control.Run) { n.RequestKey = "new" }, func(n *control.Run) { n.CreatedAtMS-- }}
	for _, change := range cases {
		n := nextControlRun(r)
		change(&n)
		if _, err := s.SaveRun(context.Background(), r.Version, n); !errors.Is(err, control.ErrConflict) {
			t.Fatalf("immutable accepted: %v", err)
		}
	}
	n := nextControlRun(r)
	n.SafeCode = strings.Repeat("x", control.MaxRunBytes)
	if _, err := s.SaveRun(context.Background(), r.Version, n); !errors.Is(err, control.ErrCapacity) {
		t.Fatal(err)
	}
	n = nextControlRun(r)
	n.Grant.Model = "changed"
	if _, err := s.SaveRun(context.Background(), r.Version, n); !errors.Is(err, control.ErrInvalid) {
		t.Fatalf("hash mismatch: %v", err)
	}
	if _, err := s.db.Exec("UPDATE super_runs SET owner='inconsistent' WHERE scope=? AND id=?", r.Scope, r.ID); err == nil {
		t.Fatal("projection corruption accepted")
	}
}
func TestControlLocalIntakeAndCancellation(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := controlFixture("local", "local", "/work", true)
	if _, _, err := s.CreateRun(ctx, r, nil); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("missing local intake: %v", err)
	}
	r.IntakeID = "missing"
	if _, _, err := s.CreateRun(ctx, r, nil); !errors.Is(err, control.ErrDenied) {
		t.Fatal(err)
	}
	svc := workorder.NewService(s, nil)
	order, _, err := svc.Create(ctx, "local-key", intake())
	if err != nil {
		t.Fatal(err)
	}
	r.IntakeID = order.ID
	r = createControlRun(t, s, r)
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	if err = s.CheckClaim(ctx, r.Jobs[0].Claim); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Cancel(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckClaim(ctx, r.Jobs[0].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("old intake cancel ignored: %v", err)
	}
	n := nextControlRun(r)
	n.Jobs[0].Claim.UntilMS += 1000
	if _, err = s.SaveRun(ctx, r.Version, n); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("cancelled intake lease renewed: %v", err)
	}
	fresh := controlFixture("cancelled-intake", "local", "/work", true)
	fresh.IntakeID = order.ID
	if _, _, err = s.CreateRun(ctx, fresh, nil); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("cancelled intake admitted: %v", err)
	}
}
