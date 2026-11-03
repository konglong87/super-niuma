package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func legacyDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path, err := preparePath(filepath.Join(t.TempDir(), "private", "v1.db"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(schema() + fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=1;", applicationID)); err != nil {
		t.Fatal(err)
	}
	return db, path
}
func seedLegacyOrder(t *testing.T, db *sql.DB, id, key string, cancelled bool) workorder.WorkOrder {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 11, 3, 1, 0, 0, 123000000, time.UTC)
	o := workorder.WorkOrder{ID: id, Title: "Legacy title", Objective: "PRIVATE objective", AcceptanceCriteria: []string{"Criterion"}, Workspace: "/metadata/only", Status: workorder.StatusRecorded, ExecutionStatus: workorder.NotStarted, VerificationStatus: workorder.NotStarted, DeliveryStatus: workorder.NotStarted, CreatedAt: at, UpdatedAt: at}
	fingerprint, err := contentHash(o)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = insertOrder(ctx, tx, key, fingerprint, o); err != nil {
		t.Fatal(err)
	}
	if err = insertEvent(ctx, tx, id, workorder.StatusRecorded, at); err != nil {
		t.Fatal(err)
	}
	if cancelled {
		o.Status = workorder.StatusCancelled
		o.UpdatedAt = at.Add(time.Second)
		if _, err = tx.Exec("UPDATE work_orders SET status=?,updated_at=? WHERE id=?", o.Status, o.UpdatedAt.Format(time.RFC3339Nano), id); err != nil {
			t.Fatal(err)
		}
		if err = insertEvent(ctx, tx, id, workorder.StatusCancelled, o.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return o
}
func TestMigrationV1PreservesOrdersKeysAndBackfillsOnce(t *testing.T) {
	ctx := context.Background()
	db, path := legacyDatabase(t)
	original := seedLegacyOrder(t, db, "legacy-order", "legacy-key", true)
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Get(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(original, loaded) {
		t.Fatalf("migration changed order: %#v %v", loaded, err)
	}
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("schema version %d %v", version, err)
	}
	page, err := s.ReadEvents(ctx, eventbus.LocalScope, 0, 100)
	if err != nil || len(page) != 2 || page[0].Topic != eventbus.TopicWorkOrderRecorded || page[1].Topic != eventbus.TopicWorkOrderCancelled {
		t.Fatalf("backfill: %#v %v", page, err)
	}
	if string(page[0].Payload) != `{"work_order_id":"legacy-order"}` {
		t.Fatalf("unsafe migration payload: %s", page[0].Payload)
	}
	if page[0].OccurredAtMS != original.CreatedAt.UnixMilli() {
		t.Fatal("changed source time")
	}
	if eventCount(t, s) != 2 {
		t.Fatal("changed legacy audit")
	}
	again, created, err := s.Create(ctx, "legacy-key", workorder.WorkOrder{ID: "new-id", Title: original.Title, Objective: original.Objective, AcceptanceCriteria: original.AcceptanceCriteria, Workspace: original.Workspace, Status: workorder.StatusRecorded, ExecutionStatus: workorder.NotStarted, VerificationStatus: workorder.NotStarted, DeliveryStatus: workorder.NotStarted, CreatedAt: original.CreatedAt, UpdatedAt: original.CreatedAt})
	if err != nil || created || again.ID != original.ID || again.Status != workorder.StatusCancelled {
		t.Fatalf("idempotency changed: %#v %v", again, err)
	}
	s.Close()
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replayed, err := s.ReadEvents(ctx, eventbus.LocalScope, 0, 100)
	if err != nil || !reflect.DeepEqual(page, replayed) {
		t.Fatal("reopen duplicated/reordered migration")
	}
}
func TestMigrationFailureRollsBackVersionAndDDL(t *testing.T) {
	db, path := legacyDatabase(t)
	seedLegacyOrder(t, db, "bad-time", "key", false)
	if _, err := db.Exec("UPDATE work_order_events SET occurred_at='not-a-time'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(context.Background(), path); err == nil {
		s.Close()
		t.Fatal("accepted malformed historical audit")
	}
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version, tables, orders int
	raw.QueryRow("PRAGMA user_version").Scan(&version)
	raw.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='super_events'").Scan(&tables)
	raw.QueryRow("SELECT count(*) FROM work_orders").Scan(&orders)
	if version != 1 || tables != 0 || orders != 1 {
		t.Fatalf("partial migration: version=%d bus=%d orders=%d", version, tables, orders)
	}
}
func TestMigrationAboveQuotaPreservesHistoryAndStillCancels(t *testing.T) {
	ctx := context.Background()
	db, path := legacyDatabase(t)
	_, err := db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < ?) INSERT INTO work_orders(id,idempotency_key_hash,content_hash,title,objective,acceptance_criteria,workspace,status,execution_status,verification_status,delivery_status,created_at,updated_at) SELECT 'legacy-'||x,'key-'||x,'hash-'||x,'title','objective','["criterion"]','/metadata/only','recorded','not_started','not_started','not_started','2026-11-03T00:00:00Z','2026-11-03T00:00:00Z' FROM n`, eventbus.MaxRetainedEvents+1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO work_order_events(work_order_id,kind,occurred_at) SELECT id,'recorded',created_at FROM work_orders ORDER BY sequence"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM super_events").Scan(&count); err != nil || count != eventbus.MaxRetainedEvents+1 {
		t.Fatalf("history truncated: %d %v", count, err)
	}
	if _, _, err = s.AppendEvent(ctx, busEvent("over", "scope", "test.ready")); !errors.Is(err, eventbus.ErrCapacity) {
		t.Fatalf("no backpressure: %v", err)
	}
	o, err := s.Cancel(ctx, "legacy-1", time.Now())
	if err != nil || o.Status != workorder.StatusCancelled {
		t.Fatalf("historical quota blocked cancellation: %v", err)
	}
}
func TestMigrationRejectsFutureAndMissingV2Schema(t *testing.T) {
	for _, future := range []bool{false, true} {
		t.Run(fmt.Sprint(future), func(t *testing.T) {
			s, path := openTestStore(t)
			query := "DROP TABLE super_events"
			if future {
				query = "PRAGMA user_version=999"
			}
			if _, err := s.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if reopened, err := Open(context.Background(), path); err == nil {
				reopened.Close()
				t.Fatal("silently repaired incompatible database")
			}
		})
	}
}
