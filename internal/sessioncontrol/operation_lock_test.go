package sessioncontrol

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecuteMutationSerializesSameKeyThroughAuditAndThenConflicts(t *testing.T) {
	locker := newOperationLockFake()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var auditComplete atomic.Bool
	var stored OperationMetadata
	service := NewService(Dependencies{
		Managed: &operationLockManagedFake{}, Authorizer: operationLockAuthorizerFake{},
		Audit: operationLockAuditFake{complete: &auditComplete}, OperationLock: locker,
	})
	ctx := testRequestContext()
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.executeMutation(context.Background(), ctx, OperationCreate, "shared-key", strings.Repeat("a", 64), tenantRef("first"), func(identity OperationIdentity) (RecoveredOperation, error) {
			return RecoveredOperation{}, nil
		}, func(identity OperationIdentity) (OperationResult, SessionRef, error) {
			stored = identity.Metadata()
			close(firstEntered)
			<-releaseFirst
			return OperationResult{}, tenantRef("first"), nil
		})
		firstDone <- err
	}()
	<-firstEntered
	secondRecovered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		_, err := service.executeMutation(context.Background(), ctx, OperationCreate, "shared-key", strings.Repeat("b", 64), tenantRef("second"), func(identity OperationIdentity) (RecoveredOperation, error) {
			if !auditComplete.Load() {
				t.Error("second recovery entered before first audit completed")
			}
			close(secondRecovered)
			return RecoveredOperation{Found: true, Metadata: stored, Target: tenantRef("first")}, nil
		}, func(OperationIdentity) (OperationResult, SessionRef, error) {
			t.Error("conflicting second request applied")
			return OperationResult{}, SessionRef{}, nil
		})
		secondDone <- err
	}()
	select {
	case <-secondRecovered:
		t.Fatal("same-key recovery was not serialized")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	err := <-secondDone
	assertServiceErrorCode(t, err, CodeIdempotencyConflict)
}

func TestExecuteMutationDifferentKeysDoNotBlockEachOther(t *testing.T) {
	locker := newOperationLockFake()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	service := NewService(Dependencies{
		Managed: &operationLockManagedFake{}, Authorizer: operationLockAuthorizerFake{},
		Audit: operationLockAuditFake{}, OperationLock: locker,
	})
	ctx := testRequestContext()
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.executeMutation(context.Background(), ctx, OperationCreate, "key-a", strings.Repeat("a", 64), tenantRef("first"), func(OperationIdentity) (RecoveredOperation, error) { return RecoveredOperation{}, nil }, func(OperationIdentity) (OperationResult, SessionRef, error) {
			close(firstEntered)
			<-releaseFirst
			return OperationResult{}, tenantRef("first"), nil
		})
		firstDone <- err
	}()
	<-firstEntered
	secondDone := make(chan error, 1)
	go func() {
		_, err := service.executeMutation(context.Background(), ctx, OperationCreate, "key-b", strings.Repeat("b", 64), tenantRef("second"), func(OperationIdentity) (RecoveredOperation, error) { return RecoveredOperation{}, nil }, func(OperationIdentity) (OperationResult, SessionRef, error) {
			return OperationResult{}, tenantRef("second"), nil
		})
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("different operation keys blocked each other")
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

type operationLockFake struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newOperationLockFake() *operationLockFake {
	return &operationLockFake{locks: make(map[string]*sync.Mutex)}
}

func (f *operationLockFake) AcquireOperationLock(_ context.Context, keyHash string) (OperationUnlock, error) {
	f.mu.Lock()
	lock := f.locks[keyHash]
	if lock == nil {
		lock = &sync.Mutex{}
		f.locks[keyHash] = lock
	}
	f.mu.Unlock()
	lock.Lock()
	return OperationUnlock(func(context.Context) error { lock.Unlock(); return nil }), nil
}

type operationLockAuthorizerFake struct{}

func (operationLockAuthorizerFake) Authorize(context.Context, RequestContext, Operation) error {
	return nil
}

type operationLockAuditFake struct{ complete *atomic.Bool }

func (f operationLockAuditFake) Record(context.Context, AuditRecord) (uint64, error) {
	if f.complete != nil {
		f.complete.Store(true)
	}
	return 1, nil
}

type operationLockManagedFake struct{}

func (*operationLockManagedFake) Create(context.Context, CreateRequest) (SessionSnapshot, error) {
	return SessionSnapshot{}, nil
}
func (*operationLockManagedFake) List(context.Context, ListRequest) ([]SessionSnapshot, error) {
	return nil, nil
}
func (*operationLockManagedFake) Get(_ context.Context, request GetRequest) (SessionSnapshot, error) {
	return SessionSnapshot{Ref: request.Ref}, nil
}
func (*operationLockManagedFake) Send(context.Context, SendRequest) (OperationResult, error) {
	return OperationResult{}, nil
}
func (*operationLockManagedFake) Stop(context.Context, StopRequest) (OperationResult, error) {
	return OperationResult{}, nil
}
