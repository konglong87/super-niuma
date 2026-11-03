package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "work-orders.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}
func intake() workorder.CreateInput {
	return workorder.CreateInput{Title: "Title", Objective: "Objective", AcceptanceCriteria: []string{"Criterion"}, Workspace: "/metadata/only"}
}
func eventCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM work_order_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestRestartDurabilityIdempotencyAndCancelledDuplicate(t *testing.T) {
	ctx := context.Background()
	store, path := openTestStore(t)
	service := workorder.NewService(store, nil)
	empty, err := service.List(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty list: %v %v", empty, err)
	}
	original, created, err := service.Create(ctx, "durable-key", intake())
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if eventCount(t, store) != 1 {
		t.Fatal("missing intake event")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	service = workorder.NewService(reopened, nil)
	loaded, err := service.Get(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(original, loaded) {
		t.Fatalf("restart lost aggregate: %v", err)
	}
	retry := intake()
	retry.Title = " Title "
	retry.Workspace = "/metadata/../metadata/only"
	duplicated, created, err := service.Create(ctx, "durable-key", retry)
	if err != nil || created || !reflect.DeepEqual(original, duplicated) {
		t.Fatalf("exact retry: %v %v", created, err)
	}
	changed := intake()
	changed.Objective = "changed"
	if _, _, err := service.Create(ctx, "durable-key", changed); !errors.Is(err, workorder.ErrIdempotencyConflict) {
		t.Fatalf("conflict: %v", err)
	}
	cancelled, err := service.Cancel(ctx, original.ID)
	if err != nil || cancelled.Status != workorder.StatusCancelled {
		t.Fatalf("cancel: %v", err)
	}
	again, err := service.Cancel(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(cancelled, again) {
		t.Fatal("repeated cancel changed order")
	}
	duplicated, created, err = service.Create(ctx, "durable-key", intake())
	if err != nil || created || !reflect.DeepEqual(cancelled, duplicated) {
		t.Fatal("duplicate resurrected cancelled intake")
	}
	if eventCount(t, reopened) != 2 {
		t.Fatal("duplicate audit event")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	final, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	loaded, err = workorder.NewService(final, nil).Get(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(cancelled, loaded) {
		t.Fatal("cancellation was not durable")
	}
}
func TestConcurrentSameKeyAcrossIndependentStores(t *testing.T) {
	ctx := context.Background()
	first, path := openTestStore(t)
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	services := []*workorder.Service{workorder.NewService(first, nil), workorder.NewService(second, nil)}
	const writers = 24
	type result struct {
		order   workorder.WorkOrder
		created bool
		err     error
	}
	results := make(chan result, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			order, created, err := services[i%2].Create(ctx, "same-key", intake())
			results <- result{order, created, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	id := ""
	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
		}
		if id == "" {
			id = result.order.ID
		}
		if id != result.order.ID {
			t.Fatal("multiple identities for one key")
		}
	}
	if createdCount != 1 || eventCount(t, first) != 1 {
		t.Fatalf("created=%d events=%d", createdCount, eventCount(t, first))
	}
}
func TestTransactionalEventsRollback(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := workorder.NewService(store, nil)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_events BEFORE INSERT ON work_order_events BEGIN SELECT RAISE(ABORT, 'SECRET_SQL'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, "key", intake()); !errors.Is(err, workorder.ErrUnavailable) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("rollback error: %v", err)
	}
	items, err := service.List(ctx)
	if err != nil || len(items) != 0 || eventCount(t, store) != 0 {
		t.Fatal("partial creation transaction")
	}
	if _, err := store.db.Exec("DROP TRIGGER fail_events"); err != nil {
		t.Fatal(err)
	}
	order, _, err := service.Create(ctx, "key", intake())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_events BEFORE INSERT ON work_order_events BEGIN SELECT RAISE(ABORT, 'SECRET_SQL'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cancel(ctx, order.ID); !errors.Is(err, workorder.ErrUnavailable) {
		t.Fatalf("cancel rollback: %v", err)
	}
	loaded, err := service.Get(ctx, order.ID)
	if err != nil || !reflect.DeepEqual(order, loaded) || eventCount(t, store) != 1 {
		t.Fatal("partial cancellation transaction")
	}
}
func TestListBoundAndOrdering(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := workorder.NewService(store, nil)
	var last workorder.WorkOrder
	for i := 0; i < workorder.ListLimit+7; i++ {
		input := intake()
		input.Title = strings.Repeat("a", i+1)
		order, _, err := service.Create(ctx, input.Title, input)
		if err != nil {
			t.Fatal(err)
		}
		last = order
	}
	items, err := service.List(ctx)
	if err != nil || len(items) != workorder.ListLimit || items[0].ID != last.ID {
		t.Fatalf("list length/order: %d %v", len(items), err)
	}
	for i := 1; i < len(items); i++ {
		if !items[i-1].CreatedAt.After(items[i].CreatedAt) && !items[i-1].CreatedAt.Equal(items[i].CreatedAt) {
			t.Fatal("list not latest first")
		}
	}
	for _, limit := range []int{0, -1, workorder.ListLimit + 1} {
		if _, err := store.List(ctx, limit); !errors.Is(err, workorder.ErrInvalidInput) {
			t.Fatalf("unbounded limit %d: %v", limit, err)
		}
	}
}
func TestPrivateFilesAndRejectedPaths(t *testing.T) {
	store, path := openTestStore(t)
	if _, _, err := workorder.NewService(store, nil).Create(context.Background(), "permission-check", intake()); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{filepath.Dir(path), path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("insecure mode: %o", info.Mode().Perm())
		}
	}
	_ = store.Close()
	for _, path := range []string{"", ":memory:", "relative.db", "file:/tmp/secret.db", "/bad\x00path"} {
		if store, err := Open(context.Background(), path); err == nil {
			store.Close()
			t.Fatal("accepted invalid path")
		}
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, directoryMode); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(context.Background(), link); err == nil {
		store.Close()
		t.Fatal("accepted symlink database")
	}
	bytes, err := os.ReadFile(target)
	if err != nil || string(bytes) != "private" {
		t.Fatal("modified symlink target")
	}
}
func TestRejectForeignDatabaseWithoutModifyingIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, directoryMode); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "legacy.db")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE sessions (id TEXT); INSERT INTO sessions VALUES ('legacy')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := Open(context.Background(), path); err == nil {
		store.Close()
		t.Fatal("accepted legacy schema")
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("modified foreign database")
	}
}
func TestCancelledContextAndClosedStore(t *testing.T) {
	store, _ := openTestStore(t)
	service := workorder.NewService(store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := service.Create(ctx, "key", intake()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if eventCount(t, store) != 0 {
		t.Fatal("cancelled request persisted")
	}
	store.Close()
	if _, err := service.List(context.Background()); !errors.Is(err, workorder.ErrUnavailable) {
		t.Fatalf("closed store: %v", err)
	}
}
