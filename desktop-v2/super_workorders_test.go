package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

// Compile-time assertion protects the exact Wails contract, including Create's
// intentionally omitted Service-created flag and the absence of context inputs.
var _ interface {
	CreateSuperWorkOrder(string, workorder.CreateInput) (workorder.WorkOrder, error)
	ListSuperWorkOrders() ([]workorder.WorkOrder, error)
	GetSuperWorkOrder(string) (workorder.WorkOrder, error)
	CancelSuperWorkOrder(string) (workorder.WorkOrder, error)
} = (*app)(nil)

func superTestApp(t *testing.T) (*app, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "state")
	t.Setenv(super.StateDirectoryEnv, directory)
	a := &app{}
	a.superWorkOrders.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	t.Cleanup(a.superWorkOrders.close)
	return a, directory
}

func superTestInput() workorder.CreateInput {
	return workorder.CreateInput{Title: "synthetic intake", Objective: "record only", AcceptanceCriteria: []string{"no execution"}}
}

func assertSuperNotStarted(t *testing.T, order workorder.WorkOrder) {
	t.Helper()
	if order.ExecutionStatus != workorder.NotStarted || order.VerificationStatus != workorder.NotStarted || order.DeliveryStatus != workorder.NotStarted {
		t.Fatalf("phantom execution: %+v", order)
	}
}

func assertSuperClosed(t *testing.T, a *app) {
	t.Helper()
	_, createErr := a.CreateSuperWorkOrder("after-close", superTestInput())
	_, listErr := a.ListSuperWorkOrders()
	_, getErr := a.GetSuperWorkOrder("after-close")
	_, cancelErr := a.CancelSuperWorkOrder("after-close")
	for _, err := range []error{createErr, listErr, getErr, cancelErr} {
		if err != workorder.ErrUnavailable {
			t.Fatalf("closed bridge error = %v", err)
		}
	}
}

func TestSuperWorkOrderBridgeLazyLifecycle(t *testing.T) {
	a, directory := superTestApp(t)
	options := desktopWailsOptions(a, desktopConfig{}, &url.URL{})
	if len(options.Bind) != 1 || options.Bind[0] != a || options.OnShutdown == nil {
		t.Fatal("Super is not on the existing app binding/lifecycle")
	}
	if a.superWorkOrders.store != nil || a.superWorkOrders.ctx != nil {
		t.Fatal("composition eagerly initialized Super")
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("composition created state: %v", err)
	}
	a.superWorkOrders.close()
	a.superWorkOrders.close()
	assertSuperClosed(t, a)
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unused shutdown or refused request created state: %v", err)
	}
}

func TestSuperWorkOrderBridgePersistenceAndIdempotency(t *testing.T) {
	a, directory := superTestApp(t)
	orders, err := a.ListSuperWorkOrders()
	if err != nil || orders == nil || len(orders) != 0 {
		t.Fatalf("empty list = %#v, %v", orders, err)
	}
	input := superTestInput()
	order, err := a.CreateSuperWorkOrder("retry-key", input)
	if err != nil || order.Status != workorder.StatusRecorded {
		t.Fatalf("create = %+v, %v", order, err)
	}
	assertSuperNotStarted(t, order)
	retryInput := input
	retryInput.Title = "  " + input.Title + "  "
	retry, err := a.CreateSuperWorkOrder("retry-key", retryInput)
	if err != nil || !reflect.DeepEqual(retry, order) {
		t.Fatalf("normalized retry = %+v, %v", retry, err)
	}
	conflict := input
	conflict.Objective = "different intake"
	if _, err := a.CreateSuperWorkOrder("retry-key", conflict); err != workorder.ErrIdempotencyConflict {
		t.Fatalf("conflict = %v", err)
	}
	cancelled, err := a.CancelSuperWorkOrder(order.ID)
	if err != nil || cancelled.Status != workorder.StatusCancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	assertSuperNotStarted(t, cancelled)
	again, err := a.CancelSuperWorkOrder(order.ID)
	if err != nil || !reflect.DeepEqual(again, cancelled) {
		t.Fatalf("cancel retry = %+v, %v", again, err)
	}
	a.superWorkOrders.close()
	assertSuperClosed(t, a)
	b := &app{}
	t.Cleanup(b.superWorkOrders.close)
	persisted, err := b.GetSuperWorkOrder(order.ID)
	if err != nil || !reflect.DeepEqual(persisted, cancelled) {
		t.Fatalf("reopen = %+v, %v", persisted, err)
	}
	retry, err = b.CreateSuperWorkOrder("retry-key", input)
	if err != nil || !reflect.DeepEqual(retry, cancelled) {
		t.Fatalf("cancelled retry after restart = %+v, %v", retry, err)
	}
	orders, err = b.ListSuperWorkOrders()
	if err != nil || len(orders) != 1 || orders[0].ID != order.ID {
		t.Fatalf("persisted list = %+v, %v", orders, err)
	}
	info, err := os.Stat(filepath.Join(directory, super.DatabaseName))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private database: %v, %v", info, err)
	}
}

func TestSuperWorkOrderBridgeIndependentInstances(t *testing.T) {
	a, _ := superTestApp(t)
	order, err := a.CreateSuperWorkOrder("shared", superTestInput())
	if err != nil {
		t.Fatal(err)
	}
	b := &app{}
	t.Cleanup(b.superWorkOrders.close)
	if got, err := b.GetSuperWorkOrder(order.ID); err != nil || got.ID != order.ID {
		t.Fatalf("second instance = %+v, %v", got, err)
	}
	if a.superWorkOrders.store == b.superWorkOrders.store || a.superWorkOrders.ctx == b.superWorkOrders.ctx {
		t.Fatal("app instances share lifecycle resources")
	}
	a.superWorkOrders.close()
	if _, err := b.CancelSuperWorkOrder(order.ID); err != nil {
		t.Fatalf("closing first app affected second: %v", err)
	}
	// A different explicit state directory is isolated, even in the same process.
	t.Setenv(super.StateDirectoryEnv, filepath.Join(t.TempDir(), "second-state"))
	c := &app{}
	t.Cleanup(c.superWorkOrders.close)
	if orders, err := c.ListSuperWorkOrders(); err != nil || len(orders) != 0 {
		t.Fatalf("different directory shares data: %+v, %v", orders, err)
	}
	if orders, err := b.ListSuperWorkOrders(); err != nil || len(orders) != 1 {
		t.Fatalf("opened instance changed directory: %+v, %v", orders, err)
	}
	assertSuperClosed(t, a)
}

func TestSuperWorkOrderBridgeConcurrentCalls(t *testing.T) {
	a, _ := superTestApp(t)
	const callers = 24
	var wg sync.WaitGroup
	results := make(chan workorder.WorkOrder, callers)
	errorsCh := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			order, err := a.CreateSuperWorkOrder("same-key", superTestInput())
			results <- order
			errorsCh <- err
		})
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for order := range results {
		if id != "" && id != order.ID {
			t.Fatal("concurrent retry duplicated intake")
		}
		id = order.ID
		assertSuperNotStarted(t, order)
	}
	for i := range callers {
		wg.Go(func() {
			if _, err := a.CreateSuperWorkOrder(fmt.Sprintf("unique-%d", i), superTestInput()); err != nil {
				t.Error(err)
			}
			if _, err := a.GetSuperWorkOrder(id); err != nil {
				t.Error(err)
			}
			if _, err := a.CancelSuperWorkOrder(id); err != nil {
				t.Error(err)
			}
			if _, err := a.ListSuperWorkOrders(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	orders, err := a.ListSuperWorkOrders()
	if err != nil || len(orders) != callers+1 {
		t.Fatalf("concurrent list = %d, %v", len(orders), err)
	}
	for _, order := range orders {
		assertSuperNotStarted(t, order)
	}
}

// Embedded Repository fails immediately if a test unexpectedly calls a different
// operation. List and Close model only the lifecycle boundary under examination.
type superTestStore struct {
	workorder.Repository
	list  func(context.Context, int) ([]workorder.WorkOrder, error)
	close func() error
}

func (s *superTestStore) List(ctx context.Context, limit int) ([]workorder.WorkOrder, error) {
	return s.list(ctx, limit)
}
func (s *superTestStore) Close() error { return s.close() }

func waitSuperSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func TestSuperWorkOrderBridgeShutdownDrainsActiveAndQueued(t *testing.T) {
	a, _ := superTestApp(t)
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var opens, closes atomic.Int32
	var active atomic.Bool
	a.superWorkOrders.openStore = func(ctx context.Context, _ string) (superWorkOrderStore, error) {
		opens.Add(1)
		return &superTestStore{
			list: func(ctx context.Context, _ int) ([]workorder.WorkOrder, error) {
				deadline, bounded := ctx.Deadline()
				if !bounded || time.Until(deadline) > superWorkOrderTimeout {
					t.Error("unbounded bridge context")
				}
				active.Store(true)
				close(entered)
				<-ctx.Done()
				close(cancelled)
				<-release
				active.Store(false)
				return nil, ctx.Err()
			},
			close: func() error {
				if active.Load() {
					t.Error("store closed before the active operation drained")
				}
				closes.Add(1)
				return nil
			},
		}, nil
	}
	// Always release a blocked fake before the app's cleanup closes its store.
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	const queued = 12
	errorsCh := make(chan error, queued+1)
	go func() { _, err := a.ListSuperWorkOrders(); errorsCh <- err }()
	waitSuperSignal(t, entered, "active request")
	for range queued {
		go func() { _, err := a.ListSuperWorkOrders(); errorsCh <- err }()
	}
	closed := make(chan struct{})
	go func() { a.superWorkOrders.close(); close(closed) }()
	waitSuperSignal(t, cancelled, "shutdown cancellation")
	select {
	case <-closed:
		t.Fatal("shutdown returned before active request released")
	default:
	}
	assertSuperClosed(t, a)
	unblock()
	waitSuperSignal(t, closed, "drained shutdown")
	for range queued + 1 {
		select {
		case err := <-errorsCh:
			if !errors.Is(err, context.Canceled) && !errors.Is(err, workorder.ErrUnavailable) {
				t.Fatalf("shutdown request = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("queued call did not stop")
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(a.superWorkOrders.close)
	}
	wg.Wait()
	assertSuperClosed(t, a)
	if opens.Load() != 1 || closes.Load() != 1 {
		t.Fatalf("opens=%d closes=%d", opens.Load(), closes.Load())
	}
}

func TestSuperWorkOrderBridgeShutdownDuringLazyOpen(t *testing.T) {
	a, _ := superTestApp(t)
	entered := make(chan struct{})
	var opens, closes atomic.Int32
	a.superWorkOrders.openStore = func(ctx context.Context, _ string) (superWorkOrderStore, error) {
		opens.Add(1)
		close(entered)
		<-ctx.Done()
		// Even a store returned as cancellation races with a successful open is
		// owned and closed; Service must not start an operation on it.
		return &superTestStore{
			list: func(context.Context, int) ([]workorder.WorkOrder, error) {
				t.Error("operation reached repository after shutdown")
				return nil, nil
			},
			close: func() error { closes.Add(1); return nil },
		}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := a.ListSuperWorkOrders(); done <- err }()
	waitSuperSignal(t, entered, "lazy open")
	a.superWorkOrders.close()
	if err := <-done; err != context.Canceled {
		t.Fatalf("cancelled open = %v", err)
	}
	assertSuperClosed(t, a)
	if opens.Load() != 1 || closes.Load() != 1 {
		t.Fatalf("opens=%d closes=%d", opens.Load(), closes.Load())
	}
}

func TestSuperWorkOrderBridgeQueueDeadline(t *testing.T) {
	a, directory := superTestApp(t)
	a.superWorkOrders.initialize()
	<-a.superWorkOrders.gate // hold queue without opening a database
	defer func() { a.superWorkOrders.gate <- struct{}{} }()
	done := make(chan error, 1)
	go func() { _, err := a.ListSuperWorkOrders(); done <- err }()
	select {
	case err := <-done:
		if err != context.DeadlineExceeded {
			t.Fatalf("queued request = %v", err)
		}
	case <-time.After(superWorkOrderTimeout + 3*time.Second):
		t.Fatal("queue wait exceeded the call deadline")
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired queued call created state: %v", err)
	}
}

func TestSuperWorkOrderBridgeErrorsAreSafe(t *testing.T) {
	for _, known := range []error{workorder.ErrInvalidInput, workorder.ErrNotFound, workorder.ErrIdempotencyConflict, workorder.ErrUnavailable, context.Canceled, context.DeadlineExceeded} {
		t.Run(known.Error(), func(t *testing.T) {
			a, _ := superTestApp(t)
			a.superWorkOrders.openStore = func(context.Context, string) (superWorkOrderStore, error) {
				return nil, fmt.Errorf("private-db-path SQL caller-secret: %w", known)
			}
			if _, err := a.ListSuperWorkOrders(); err != known {
				t.Fatalf("did not preserve canonical error identity: %v", err)
			}
		})
	}
	for _, source := range []string{"open", "repository", "close"} {
		t.Run(source, func(t *testing.T) {
			a, _ := superTestApp(t)
			var logs bytes.Buffer
			a.superWorkOrders.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			raw := errors.New("SQL /private/user/work-orders.db secret-content")
			a.superWorkOrders.openStore = func(context.Context, string) (superWorkOrderStore, error) {
				if source == "open" {
					return nil, raw
				}
				return &superTestStore{
					list: func(context.Context, int) ([]workorder.WorkOrder, error) {
						if source == "repository" {
							return nil, raw
						}
						return nil, nil
					},
					close: func() error { return raw },
				}, nil
			}
			_, err := a.ListSuperWorkOrders()
			if source != "close" && err != workorder.ErrUnavailable {
				t.Fatalf("raw error escaped: %v", err)
			}
			a.superWorkOrders.close()
			assertSuperClosed(t, a)
			if strings.Contains(logs.String(), "private") || strings.Contains(logs.String(), "secret-content") || strings.Contains(logs.String(), "SQL") {
				t.Fatalf("raw error in logs: %s", logs.String())
			}
			if !strings.Contains(logs.String(), "service_unavailable") {
				t.Fatal("missing structured failure code")
			}
		})
	}
}

func TestSuperWorkOrderBridgeFailedOpenCanRetry(t *testing.T) {
	a, directory := superTestApp(t)
	if err := os.WriteFile(directory, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ListSuperWorkOrders(); err != workorder.ErrUnavailable {
		t.Fatalf("filesystem error escaped: %v", err)
	}
	if a.superWorkOrders.store != nil || a.superWorkOrders.service != nil {
		t.Fatal("failed open retained partial state")
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if orders, err := a.ListSuperWorkOrders(); err != nil || len(orders) != 0 {
		t.Fatalf("retry after repairing directory = %+v, %v", orders, err)
	}
}

func TestSuperWorkOrderBridgeInvalidStateFailsClosed(t *testing.T) {
	for _, directory := range []string{"", "relative-private-state", "~/private-state"} {
		t.Run(fmt.Sprintf("%q", directory), func(t *testing.T) {
			a, _ := superTestApp(t)
			t.Setenv(super.StateDirectoryEnv, directory)
			a.superWorkOrders.openStore = func(context.Context, string) (superWorkOrderStore, error) {
				t.Fatal("invalid directory reached storage")
				return nil, nil
			}
			if _, err := a.ListSuperWorkOrders(); err != workorder.ErrUnavailable {
				t.Fatalf("invalid directory = %v", err)
			}
		})
	}
}

func TestSuperWorkOrderBridgeNoPhantomExecutionOrSensitiveLogs(t *testing.T) {
	a, directory := superTestApp(t)
	var logs bytes.Buffer
	a.superWorkOrders.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	input := workorder.CreateInput{Title: "secret-title", Objective: "secret-objective", AcceptanceCriteria: []string{"secret-acceptance"}, Workspace: filepath.Join(t.TempDir(), "secret-workspace")}
	const key = "secret-idempotency-key"
	// A legacy lock is deliberately held: the new bridge must not acquire it.
	a.mu.Lock()
	defer a.mu.Unlock()
	order, err := a.CreateSuperWorkOrder(key, input)
	if err != nil {
		t.Fatal(err)
	}
	assertSuperNotStarted(t, order)
	if _, err := a.GetSuperWorkOrder("secret-unvalidated-id"); err != workorder.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := a.CancelSuperWorkOrder("secret-unvalidated-id"); err != workorder.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := a.CreateSuperWorkOrder("bad key secret", workorder.CreateInput{}); err != workorder.ErrInvalidInput {
		t.Fatal(err)
	}
	cancelled, err := a.CancelSuperWorkOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSuperNotStarted(t, cancelled)
	if a.service != nil || a.computerRuntime != nil || a.providerRuntime != nil || a.computerManager != nil || a.computerBridge != nil || a.windowCtx != nil || a.config.Workspace != "" {
		t.Fatal("intake initialized or mutated legacy runtime/configuration")
	}
	if _, err := os.Stat(input.Workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace accessed: %v", err)
	}
	a.superWorkOrders.close()
	for _, secret := range []string{input.Title, input.Objective, input.AcceptanceCriteria[0], input.Workspace, directory, key, "secret-unvalidated-id", "bad key secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("sensitive log: %s", secret)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil || event["action"] == nil || event["component"] == nil {
			t.Fatalf("unstructured log = %s, %v", line, err)
		}
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(directory, super.DatabaseName), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tables, []string{"work_order_events", "work_orders"}) {
		t.Fatalf("unexpected execution/session schema: %v", tables)
	}
}
