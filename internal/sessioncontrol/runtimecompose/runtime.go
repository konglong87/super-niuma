package runtimecompose

import (
	"context"
	"fmt"

	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/sessioncontrol/handoff"
)

// TenantService is the single tenant-scoped persistence boundary shared by
// managed operations, recovery, audit, and immutable Handoff persistence.
type TenantService interface {
	sessioncontrol.TenantManagedService
	sessioncontrol.RuntimeStore
	handoff.Store
	handoff.BatchStore
}

type Dependencies struct {
	Tenant            TenantService
	EventStore        sessioncontrol.ManagedEventStore
	HandoffEventStore handoff.EventStore
	Dispatcher        sessioncontrol.ManagedRunDispatcher
	PendingInputs     pendinginput.Queue
	LocalStore        sessioncontrol.LocalSessionStore
	LocalHandoffStore handoff.LocalSnapshotStore
	Compressor        handoff.Compressor
	Monitor           sessioncontrol.MonitorPort
	OperationLock     sessioncontrol.OperationLockPort
	OperationKeyGuard sessioncontrol.OperationKeyGuardPort
	Clock             sessioncontrol.Clock
	EnableLocalRead   bool
}

// NewReadService composes the exact SessionGet path without installing any
// mutation dispatcher. Scheduler children use it to avoid a model/tool loop.
func NewReadService(tenantService TenantService, localStore sessioncontrol.LocalSessionStore) (*sessioncontrol.Service, error) {
	if tenantService == nil {
		return nil, fmt.Errorf("session control tenant runtime is unavailable")
	}
	managed := sessioncontrol.NewManagedAdapter(sessioncontrol.NewTenantManagedStore(tenantService), nil)
	if provider, ok := tenantService.(interface{ PendingInputQueue() pendinginput.Queue }); ok {
		managed.SetPendingInputs(provider.PendingInputQueue())
	}
	runtime := sessioncontrol.NewRuntime(sessioncontrol.RuntimeDependencies{Store: tenantService})
	var local sessioncontrol.LocalSessionPort
	if localStore != nil {
		local = localSessionPort{adapter: sessioncontrol.NewLocalAdapter(localStore)}
	}
	return sessioncontrol.NewService(sessioncontrol.Dependencies{Managed: managed, Local: local, Authorizer: runtime}), nil
}

// NewService composes every transport over the same control-plane service.
// It performs no environment lookup and cannot fall back to in-memory tenant
// state, keeping production wiring explicit and testable.
func NewService(deps Dependencies) (*sessioncontrol.Service, error) {
	if deps.Tenant == nil || deps.Dispatcher == nil || deps.PendingInputs == nil {
		return nil, fmt.Errorf("session control production dependencies are incomplete")
	}
	managedStore := sessioncontrol.NewTenantManagedStore(deps.Tenant)
	managed := sessioncontrol.NewManagedAdapter(managedStore, deps.Dispatcher)
	managed.SetPendingInputs(deps.PendingInputs)
	managed.SetEventStore(deps.EventStore)
	var local sessioncontrol.LocalSessionPort = disabledLocalSessionPort{}
	runtime := sessioncontrol.NewRuntime(sessioncontrol.RuntimeDependencies{Store: deps.Tenant, PendingInputs: deps.PendingInputs})
	operationLock, operationKeyGuard := deps.OperationLock, deps.OperationKeyGuard
	if operationLock == nil {
		if _, ok := deps.Tenant.(interface {
			AcquireSessionControlOperationLock(context.Context, string) (func(context.Context) error, error)
		}); ok {
			operationLock, operationKeyGuard = runtime, runtime
		}
	}
	monitorRecovery := sessioncontrol.MonitorRecoveryPort(runtime)
	if recovery, ok := deps.Monitor.(sessioncontrol.MonitorRecoveryPort); ok {
		monitorRecovery = recovery
	}

	sources := map[sessioncontrol.Source]handoff.SourceReader{
		sessioncontrol.SourceTenant: func() handoff.SourceReader {
			reader := handoff.NewTenantSourceReader(managedStore)
			reader.SetEventStore(deps.HandoffEventStore)
			return reader
		}(),
	}
	if deps.EnableLocalRead {
		local = localSessionPort{adapter: sessioncontrol.NewLocalAdapter(deps.LocalStore)}
		if deps.LocalHandoffStore == nil {
			sources[sessioncontrol.SourceLocal] = handoff.NewDefaultLocalSourceReader()
		} else {
			sources[sessioncontrol.SourceLocal] = handoff.NewLocalSourceReader(deps.LocalHandoffStore)
		}
	}
	handoffService := handoff.NewService(handoff.Dependencies{
		Sources:     sources,
		Target:      managed,
		Persistence: handoff.NewStore(deps.Tenant),
		Compressor:  deps.Compressor,
	})
	managed.SetHandoff(handoffService)

	return sessioncontrol.NewService(sessioncontrol.Dependencies{
		Managed: managed, Local: local, Authorizer: runtime,
		CreateRecovery: runtime, SendRecovery: runtime, StopRecovery: runtime,
		AttachRecovery: runtime, RefreshRecovery: runtime, MonitorRecovery: monitorRecovery,
		Monitor: deps.Monitor, Handoff: handoffService, Audit: runtime,
		OperationLock: operationLock, OperationKeyGuard: operationKeyGuard, Clock: deps.Clock,
	}), nil
}

type localSessionPort struct{ adapter *sessioncontrol.LocalAdapter }

type disabledLocalSessionPort struct{}

func (disabledLocalSessionPort) List(context.Context, sessioncontrol.ListRequest) ([]sessioncontrol.SessionSnapshot, error) {
	return []sessioncontrol.SessionSnapshot{}, nil
}

func (disabledLocalSessionPort) Get(context.Context, sessioncontrol.GetRequest) (sessioncontrol.SessionSnapshot, error) {
	return sessioncontrol.SessionSnapshot{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "local session access is disabled"}
}

func (p localSessionPort) List(_ context.Context, request sessioncontrol.ListRequest) ([]sessioncontrol.SessionSnapshot, error) {
	if request.Source != sessioncontrol.SourceLocal {
		return nil, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "local adapter requires local sessions"}
	}
	return p.adapter.List()
}

func (p localSessionPort) Get(_ context.Context, request sessioncontrol.GetRequest) (sessioncontrol.SessionSnapshot, error) {
	detail, err := p.adapter.Get(request.Ref)
	return detail.Snapshot, err
}
