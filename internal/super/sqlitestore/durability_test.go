package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const restartChildVariable = "SUPER_INTAKE_RESTART_TEST"
const restartPathVariable = "SUPER_INTAKE_RESTART_PATH"

func TestAbruptProcessExitPreservesCommittedIntakeAndCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "work-orders.db")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestartChild$")
	command.Env = append(os.Environ(), restartChildVariable+"=1", restartPathVariable+"="+path)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("child process: %v", err)
	}
	var original workorder.WorkOrder
	if err := json.Unmarshal(output, &original); err != nil {
		t.Fatalf("child response: %v", err)
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	order, created, err := workorder.NewService(store, nil).Create(ctx, "restart-key", intake())
	if err != nil || created || !reflect.DeepEqual(order, original) || eventCount(t, store) != 2 {
		t.Fatalf("process durability: created=%v err=%v", created, err)
	}
}

func TestRestartChild(t *testing.T) {
	if os.Getenv(restartChildVariable) != "1" {
		return
	}
	ctx := context.Background()
	store, err := Open(ctx, os.Getenv(restartPathVariable))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := workorder.NewService(store, logger)
	order, _, err := service.Create(ctx, "restart-key", intake())
	if err != nil {
		t.Fatal(err)
	}
	order, err = service.Cancel(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(order); err != nil {
		t.Fatal(err)
	}
	// Intentionally no Close or cleanup: exercise committed WAL crash recovery.
	os.Exit(0)
}

func TestConcurrentCancelAndRetryNeverResurrect(t *testing.T) {
	ctx := context.Background()
	first, path := openTestStore(t)
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	services := []*workorder.Service{workorder.NewService(first, nil), workorder.NewService(second, nil)}
	order, _, err := services[0].Create(ctx, "race-key", intake())
	if err != nil {
		t.Fatal(err)
	}
	const callers = 24
	var wg sync.WaitGroup
	errorsOut := make(chan error, callers)
	cancelled := make(chan workorder.WorkOrder, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			service := services[i%2]
			if i%3 == 0 {
				retry, created, err := service.Create(ctx, "race-key", intake())
				if err != nil {
					errorsOut <- err
				} else if created || retry.ID != order.ID {
					errorsOut <- fmt.Errorf("duplicate recreated intake")
				}
				return
			}
			current, err := service.Cancel(ctx, order.ID)
			if err != nil {
				errorsOut <- err
				return
			}
			cancelled <- current
		}(i)
	}
	close(start)
	wg.Wait()
	close(errorsOut)
	close(cancelled)
	for err := range errorsOut {
		t.Error(err)
	}
	var firstCancel workorder.WorkOrder
	for current := range cancelled {
		if firstCancel.ID == "" {
			firstCancel = current
		}
		if !reflect.DeepEqual(firstCancel, current) {
			t.Error("cancellation timestamp changed")
		}
	}
	final, created, err := services[0].Create(ctx, "race-key", intake())
	if err != nil || created || !reflect.DeepEqual(final, firstCancel) || eventCount(t, first) != 2 {
		t.Fatalf("resurrected intake: created=%v err=%v", created, err)
	}
}

func TestEveryNormalizedContentFieldConflicts(t *testing.T) {
	store, _ := openTestStore(t)
	service := workorder.NewService(store, nil)
	ctx := context.Background()
	if _, _, err := service.Create(ctx, "key", intake()); err != nil {
		t.Fatal(err)
	}
	cases := []func(*workorder.CreateInput){func(v *workorder.CreateInput) { v.Title = "different" }, func(v *workorder.CreateInput) { v.Objective = "different" }, func(v *workorder.CreateInput) { v.AcceptanceCriteria = []string{"different"} }, func(v *workorder.CreateInput) { v.Workspace = "/different" }}
	for _, change := range cases {
		input := intake()
		change(&input)
		if _, _, err := service.Create(ctx, "key", input); !errors.Is(err, workorder.ErrIdempotencyConflict) {
			t.Fatalf("conflict: %v", err)
		}
	}
	if eventCount(t, store) != 1 {
		t.Fatal("conflicts emitted events")
	}
}

func TestDatabaseEnforcesUniqueKeysAndNeverStartedStates(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	order, _, err := workorder.NewService(store, nil).Create(ctx, "key", intake())
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"execution_status", "verification_status", "delivery_status"} {
		if _, err := store.db.Exec("UPDATE work_orders SET "+column+" = 'running' WHERE id = ?", order.ID); err == nil {
			t.Fatal("database accepted execution")
		}
	}
	if _, err := store.db.Exec("UPDATE work_orders SET status = 'completed' WHERE id = ?", order.ID); err == nil {
		t.Fatal("database accepted completion")
	}
	if _, err := store.db.Exec(`INSERT INTO work_orders (` + orderColumns + `, idempotency_key_hash, content_hash)
 SELECT 'new-id', title, objective, acceptance_criteria, workspace, status, execution_status, verification_status, delivery_status, created_at, updated_at, idempotency_key_hash, content_hash FROM work_orders`); err == nil {
		t.Fatal("missing unique key constraint")
	}
	if _, err := store.db.Exec("INSERT INTO work_order_events(work_order_id, kind, occurred_at) VALUES('missing','recorded','now')"); err == nil {
		t.Fatal("foreign keys disabled")
	}
	var keyHash string
	if err := store.db.QueryRow("SELECT idempotency_key_hash FROM work_orders").Scan(&keyHash); err != nil || keyHash == "key" || len(keyHash) != 64 {
		t.Fatal("raw idempotency key persisted")
	}
}

func TestFileURICharactersAndPrivatePathRefusals(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, directoryMode); err != nil {
		t.Fatal(err)
	}
	specialPath := filepath.Join(private, "state #?%中文.db")
	store, err := Open(ctx, specialPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workorder.NewService(store, nil).Create(ctx, "key", intake()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := os.Stat(specialPath); err != nil {
		t.Fatal("file URI altered path")
	}
	insecure := filepath.Join(root, "public")
	if err := os.Mkdir(insecure, 0755); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(ctx, filepath.Join(insecure, "db")); err == nil {
		store.Close()
		t.Fatal("accepted non-private directory")
	}
	publicFile := filepath.Join(private, "public.db")
	if err := os.WriteFile(publicFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(ctx, publicFile); err == nil {
		store.Close()
		t.Fatal("accepted non-private database")
	}
	target := filepath.Join(private, "target")
	if err := os.WriteFile(target, []byte("sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		path := filepath.Join(private, "sidecar"+strings.TrimPrefix(suffix, "-")+".db")
		if err := os.Symlink(target, path+suffix); err != nil {
			t.Fatal(err)
		}
		if store, err := Open(ctx, path); err == nil {
			store.Close()
			t.Fatal("accepted sidecar symlink")
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(ctx, filepath.Join(alias, "db")); err == nil {
		store.Close()
		t.Fatal("accepted direct parent symlink")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "sentinel" {
		t.Fatal("modified symlink target")
	}
}
