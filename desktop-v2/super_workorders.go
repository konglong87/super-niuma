package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/internal/super/sqlitestore"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const (
	superWorkOrderTimeout = 10 * time.Second
	superActionCreate     = "work_order.create"
	superActionList       = "work_order.list"
	superActionGet        = "work_order.get"
	superActionCancel     = "work_order.cancel"
)

// The bridge owns only intake persistence, never a Session or execution runtime.
// Keep this lifecycle separate from app.mu so legacy desktop calls cannot block
// on database work. The zero value is ready and performs no filesystem I/O.
type superWorkOrderBridge struct {
	once    sync.Once
	gate    chan struct{}
	ctx     context.Context
	stop    context.CancelFunc
	store   superWorkOrderStore // protected by gate
	service *workorder.Service  // protected by gate

	// Optional composition seams must be set before the first call. Production
	// uses the private SQLite store and the application's structured logger.
	openStore func(context.Context, string) (superWorkOrderStore, error)
	logger    *slog.Logger
}

type superWorkOrderStore interface {
	workorder.Repository
	Close() error
}

func (b *superWorkOrderBridge) initialize() {
	b.once.Do(func() {
		b.ctx, b.stop = context.WithCancel(context.Background())
		b.gate = make(chan struct{}, 1)
		b.gate <- struct{}{}
		if b.logger == nil {
			b.logger = slog.Default()
		}
		b.logger = b.logger.With("component", "desktop.super.workorder")
		if b.openStore == nil {
			b.openStore = func(ctx context.Context, path string) (superWorkOrderStore, error) {
				return sqlitestore.Open(ctx, path)
			}
		}
	})
}

// call's deadline includes queueing, lazy opening, and the Service operation.
// Shutdown cancels queued/active requests before acquiring the same gate to
// close. Once stopped, this bridge can never initialize another store.
func (b *superWorkOrderBridge) call(action string, operation func(context.Context, *workorder.Service) error) (err error) {
	b.initialize()
	started := time.Now()
	ctx, cancel := context.WithTimeout(b.ctx, superWorkOrderTimeout)
	defer cancel()
	defer func() {
		if err != nil {
			var code string
			err, code = safeSuperWorkOrderError(err)
			b.logger.Warn("desktop work-order request refused", "action", action, "code", code, "duration_ms", time.Since(started).Milliseconds())
			return
		}
		b.logger.Info("desktop work-order request completed", "action", action, "duration_ms", time.Since(started).Milliseconds())
	}()
	if b.ctx.Err() != nil {
		return workorder.ErrUnavailable
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.gate:
	}
	defer func() { b.gate <- struct{}{} }()
	if b.ctx.Err() != nil {
		return workorder.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.service == nil {
		if err := b.open(ctx); err != nil {
			return err
		}
	}
	return operation(ctx, b.service)
}

func (b *superWorkOrderBridge) open(ctx context.Context) error {
	directory, err := super.StateDirectory()
	if err != nil {
		return err
	}
	b.logger.DebugContext(ctx, "opening desktop work-order store", "action", "store.open")
	store, err := b.openStore(ctx, filepath.Join(directory, super.DatabaseName))
	if err != nil {
		return err
	}
	b.store = store
	b.service = workorder.NewService(store, b.logger)
	b.logger.InfoContext(ctx, "desktop work-order store ready", "action", "store.open")
	return nil
}

func (b *superWorkOrderBridge) close() {
	b.initialize()
	b.stop() // cancel immediately, even if an operation currently owns the gate
	<-b.gate
	defer func() { b.gate <- struct{}{} }()
	if b.store == nil {
		return
	}
	err := b.store.Close()
	b.store, b.service = nil, nil
	if err != nil {
		_, code := safeSuperWorkOrderError(err)
		b.logger.Warn("desktop work-order store close failed", "action", "store.close", "code", code)
		return
	}
	b.logger.Info("desktop work-order store closed", "action", "store.close")
}

// Return canonical recognized errors, not wrappers that may contain SQL, paths,
// or caller content. errors.Is identity survives both direct and wrapped errors.
func safeSuperWorkOrderError(err error) (error, string) {
	for _, known := range []struct {
		err  error
		code string
	}{
		{workorder.ErrInvalidInput, "invalid_input"},
		{workorder.ErrNotFound, "not_found"},
		{workorder.ErrIdempotencyConflict, "idempotency_conflict"},
		{workorder.ErrUnavailable, "service_unavailable"},
		{context.Canceled, "request_cancelled"},
		{context.DeadlineExceeded, "deadline_exceeded"},
	} {
		if errors.Is(err, known.err) {
			return known.err, known.code
		}
	}
	return workorder.ErrUnavailable, "service_unavailable"
}

func (a *app) CreateSuperWorkOrder(key string, input workorder.CreateInput) (order workorder.WorkOrder, err error) {
	err = a.superWorkOrders.call(superActionCreate, func(ctx context.Context, service *workorder.Service) error {
		var callErr error
		order, _, callErr = service.Create(ctx, key, input)
		return callErr
	})
	return order, err
}

func (a *app) ListSuperWorkOrders() (orders []workorder.WorkOrder, err error) {
	err = a.superWorkOrders.call(superActionList, func(ctx context.Context, service *workorder.Service) error {
		var callErr error
		orders, callErr = service.List(ctx)
		return callErr
	})
	return orders, err
}

func (a *app) GetSuperWorkOrder(id string) (order workorder.WorkOrder, err error) {
	err = a.superWorkOrders.call(superActionGet, func(ctx context.Context, service *workorder.Service) error {
		var callErr error
		order, callErr = service.Get(ctx, id)
		return callErr
	})
	return order, err
}

func (a *app) CancelSuperWorkOrder(id string) (order workorder.WorkOrder, err error) {
	err = a.superWorkOrders.call(superActionCancel, func(ctx context.Context, service *workorder.Service) error {
		var callErr error
		order, callErr = service.Cancel(ctx, id)
		return callErr
	})
	return order, err
}
