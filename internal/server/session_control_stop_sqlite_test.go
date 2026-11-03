package server

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

// Desktop stop regression: the stop idempotency recovery query used the
// MySQL-only JSON_UNQUOTE function, so every stop click on the SQLite desktop
// build failed with HTTP 500 before the cancel ever executed. This test drives
// the real service composition over a real SQLite database: send, stop while
// the provider call is in flight, then replay the same stop idempotency key.
func TestSessionControlStopCancelsInflightRunAndReplaysOnSQLite(t *testing.T) {
	ctx := observability.WithRequestValues(context.Background(), "trace-stop-sqlite", "webui-local-user", "webui-local")
	repo, err := mysqlstore.OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "desktop.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	tenantID, err := repo.UpsertTenant(ctx, mysqlstore.TenantInput{TenantKey: "webui-local", Name: "Local Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "webui-local-user")
	if err != nil {
		t.Fatal(err)
	}
	tenantSvc := tenantservice.NewService(repo, nil)

	queryStarted := make(chan struct{})
	queryCancelled := make(chan struct{})
	var startedOnce, cancelledOnce sync.Once
	opts := Options{
		Workspace:                 t.TempDir(),
		TenantService:             tenantSvc,
		AgentTaskStore:            tenantSvc,
		PendingInputQueue:         repo,
		AgentTaskController:       agenttasks.NewController(),
		SessionControlRunDetached: true,
		SessionControlCWDValidator: func(cwd string) (string, error) {
			return cwd, nil
		},
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			startedOnce.Do(func() { close(queryStarted) })
			<-ctx.Done()
			cancelledOnce.Do(func() { close(queryCancelled) })
			return query.Result{}, ctx.Err()
		},
	}
	svc, err := NewSessionControlService(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Cancellation notification only means the provider returned, not that the
	// detached runner finished its persistence. Drain before closing/removing
	// SQLite, also when Send or Stop fails, so no worker outlives the fixture.
	defer func() {
		drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.(SessionControlDrainer).Drain(drainCtx); err != nil {
			t.Errorf("drain session control runner: %v", err)
		}
	}()
	requestContext := sessioncontrol.RequestContext{TenantID: tenantID, UserID: userID, ActorUserID: userID}
	created, err := svc.Create(ctx, sessioncontrol.CreateRequest{Context: requestContext, Title: "stop", CWD: opts.Workspace, IdempotencyKey: "sqlite-stop-create"})
	if err != nil {
		var svcErr *sessioncontrol.ServiceError
		if errors.As(err, &svcErr) {
			t.Fatalf("create: %+v cause=%+v", err, svcErr.Cause)
		}
		t.Fatalf("create: %+v", err)
	}
	ref := created.Session.Ref
	if _, err := svc.Send(ctx, sessioncontrol.SendRequest{Context: requestContext, Ref: ref, Content: "hello", IdempotencyKey: "sqlite-stop-send"}); err != nil {
		var svcErr *sessioncontrol.ServiceError
		if errors.As(err, &svcErr) {
			t.Fatalf("send: %v cause=%+v", err, svcErr.Cause)
		}
		t.Fatal(err)
	}
	select {
	case <-queryStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("query did not start")
	}

	stopped, err := svc.Stop(ctx, sessioncontrol.StopRequest{Context: requestContext, Ref: ref, IdempotencyKey: "sqlite-stop-stop"})
	if err != nil {
		var svcErr *sessioncontrol.ServiceError
		if errors.As(err, &svcErr) {
			t.Fatalf("Stop() error = %v cause=%+v", err, svcErr.Cause)
		}
		t.Fatalf("Stop() error = %v", err)
	}
	if stopped.Replayed {
		t.Fatal("first stop must not be a replay")
	}
	select {
	case <-queryCancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not cancel the in-flight query context")
	}

	replayed, err := svc.Stop(ctx, sessioncontrol.StopRequest{Context: requestContext, Ref: ref, IdempotencyKey: "sqlite-stop-stop"})
	if err != nil {
		t.Fatalf("Stop() replay error = %v", err)
	}
	if !replayed.Replayed {
		t.Fatal("second stop with the same idempotency key must replay")
	}
}

// Replay of a no-op stop (session already idle, so no stop event row exists)
// must recover through the audit fallback instead of failing. This mirrors the
// production failure where the first stop silently no-ops on a stale "latest
// task" and the retry then 500s.
func TestSessionControlStopReplayWithoutStopEventOnSQLite(t *testing.T) {
	ctx := observability.WithRequestValues(context.Background(), "trace-stop-idle-sqlite", "webui-local-user", "webui-local")
	repo, err := mysqlstore.OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "desktop.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	tenantID, err := repo.UpsertTenant(ctx, mysqlstore.TenantInput{TenantKey: "webui-local", Name: "Local Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "webui-local-user")
	if err != nil {
		t.Fatal(err)
	}
	tenantSvc := tenantservice.NewService(repo, nil)
	opts := Options{
		Workspace:           t.TempDir(),
		TenantService:       tenantSvc,
		AgentTaskStore:      tenantSvc,
		PendingInputQueue:   repo,
		AgentTaskController: agenttasks.NewController(),
		SessionControlCWDValidator: func(cwd string) (string, error) {
			return cwd, nil
		},
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			return query.Result{}, nil
		},
	}
	svc, err := NewSessionControlService(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestContext := sessioncontrol.RequestContext{TenantID: tenantID, UserID: userID, ActorUserID: userID}
	created, err := svc.Create(ctx, sessioncontrol.CreateRequest{Context: requestContext, Title: "idle", CWD: opts.Workspace, IdempotencyKey: "sqlite-idle-create"})
	if err != nil {
		t.Fatal(err)
	}
	ref := created.Session.Ref

	first, err := svc.Stop(ctx, sessioncontrol.StopRequest{Context: requestContext, Ref: ref, IdempotencyKey: "sqlite-idle-stop"})
	if err != nil {
		t.Fatalf("Stop() on idle session error = %v", err)
	}
	if first.Replayed {
		t.Fatal("first stop must not be a replay")
	}
	replayed, err := svc.Stop(ctx, sessioncontrol.StopRequest{Context: requestContext, Ref: ref, IdempotencyKey: "sqlite-idle-stop"})
	if err != nil {
		var svcErr *sessioncontrol.ServiceError
		if errors.As(err, &svcErr) {
			t.Fatalf("Stop() replay on idle session error = %v cause=%+v", err, svcErr.Cause)
		}
		t.Fatalf("Stop() replay on idle session error = %v", err)
	}
	if !replayed.Replayed {
		t.Fatal("second stop with the same idempotency key must replay")
	}
}
