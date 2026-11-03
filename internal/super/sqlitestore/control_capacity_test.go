package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

func TestControlActiveCapacityAndListBounds(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	base := controlFixture("base", "tenant", "/work", false)
	for i := 0; i < control.MaxRuns; i++ {
		r := copyControlRun(base)
		r.ID = fmt.Sprintf("r%03d", i)
		r.RequestKey = r.ID
		createControlRun(t, s, r)
	}
	if _, _, err := s.CreateRun(ctx, base, nil); !errors.Is(err, control.ErrCapacity) {
		t.Fatalf("active limit: %v", err)
	}
	active, err := s.ActiveRuns(ctx)
	if err != nil || len(active) != control.MaxRuns {
		t.Fatalf("active list: %d %v", len(active), err)
	}
	r := active[0]
	n := nextControlRun(r)
	n.Status = control.Cancelled
	n.Jobs[0].Status = control.JobCancelled
	saveControlRun(t, s, n)
	createControlRun(t, s, base)
	if _, err = s.ListRuns(ctx, "tenant"); !errors.Is(err, control.ErrCapacity) {
		t.Fatalf("oversized list silently truncated: %v", err)
	}
	recent, more, err := s.RecentRuns(ctx, "tenant", control.MaxRuns)
	if err != nil || !more || len(recent) != control.MaxRuns || recent[0].ID != "base" {
		t.Fatalf("recent history projection %d %v %v", len(recent), more, err)
	}
}
func TestControlRetentionHardLimit(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	base := controlFixture("base", "tenant", "/work", false)
	// Seed completed historical records directly to exercise retention without
	// running ten thousand artificial business transitions or generating logs.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO super_runs(scope,id,owner,request_key,revision,status,data) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < controlRetentionLimit; i++ {
		r := copyControlRun(base)
		r.ID = fmt.Sprintf("historical-%d", i)
		r.RequestKey = r.ID
		r.Status = control.Cancelled
		r.Jobs[0].Status = control.JobCancelled
		data, err := controlJSON(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.ExecContext(ctx, r.Scope, r.ID, r.Owner, r.RequestKey, r.Version, r.Status, data); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateRun(ctx, base, nil); !errors.Is(err, control.ErrCapacity) {
		t.Fatalf("retention limit: %v", err)
	}
}
func TestControlCannotReplaceIntakeIdentity(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := createControlRun(t, s, controlFixture("root", "tenant", "/work", true))
	// An immutable foreign-key-style reference must be valid, not repaired by a
	// later CAS. An external run needs no legacy intake at all.
	n := nextControlRun(r)
	n.IntakeID = "unknown"
	if _, err := s.SaveRun(ctx, r.Version, n); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("intake identity mutable: %v", err)
	}
}
func TestControlEventContainsOnlyRunIDAndMatchesPersistedRevision(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := createControlRun(t, s, controlFixture("root", "tenant", "/work", false))
	events, err := s.ReadEvents(ctx, r.Scope, 0, 100)
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %d %v", len(events), err)
	}
	e := events[0]
	if string(e.Payload) != `{"run_id":"root"}` || e.Topic != control.EventRunChanged {
		t.Fatalf("unsafe payload: %s", e.Payload)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	e.Sequence = 0
	if err = validateRunControlEvent(ctx, tx, e); err != nil {
		t.Fatal(err)
	}
	bad := e
	bad.ID = "forged"
	if err = validateRunControlEvent(ctx, tx, bad); !errors.Is(err, eventbus.ErrInvalidInput) {
		t.Fatalf("forged envelope: %v", err)
	}
	bad = e
	bad.Payload = []byte(`{"run_id":"root","objective":"private"}`)
	if err = validateRunControlEvent(ctx, tx, bad); !errors.Is(err, eventbus.ErrInvalidInput) {
		t.Fatalf("unsafe control event: %v", err)
	}
}

func TestControlAdmissionBackpressurePreservesExistingObligations(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := createControlRun(t, s, controlFixture("existing", "tenant", "/work", false))
	_, err := s.db.Exec(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<?)
 INSERT INTO super_events(event_id,scope,topic,aggregate_id,version,payload,occurred_at_ms)
 SELECT 'quota.'||n,'quota','fixture.ready','fixture',1,'{}',1 FROM seq`, eventbus.MaxRetainedEvents)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateRun(ctx, controlFixture("new-root", "other", "/work", false), nil); !errors.Is(err, control.ErrCapacity) {
		t.Fatalf("new admission bypassed bus quota: %v", err)
	}
	got, created, err := s.CreateRun(ctx, r, nil)
	if err != nil || created || got.ID != r.ID {
		t.Fatalf("replay blocked by quota: %v %v", created, err)
	}
	n := nextControlRun(r)
	n.Status = control.Cancelled
	n.Jobs[0].Status = control.JobCancelled
	r = saveControlRun(t, s, n)
	if r.Status != control.Cancelled || controlEventCount(t, s) != 2 {
		t.Fatal("full bus blocked existing cancellation")
	}
}
