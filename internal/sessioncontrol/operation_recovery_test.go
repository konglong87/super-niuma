package sessioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestServiceMutationsUseOperationSpecificRecovery(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation Operation
		invoke    func(*Service) (OperationResult, error)
		applyCall string
		target    SessionRef
	}{
		{name: "create", operation: OperationCreate, invoke: func(service *Service) (OperationResult, error) {
			return service.Create(context.Background(), CreateRequest{Context: testRequestContext(), SessionKey: "created-1", Title: "Demo", IdempotencyKey: "create-key"})
		}, applyCall: "managed.create", target: tenantRef("created-1")},
		{name: "send", operation: OperationSend, invoke: func(service *Service) (OperationResult, error) {
			return service.Send(context.Background(), SendRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), Content: "message", IdempotencyKey: "send-key"})
		}, applyCall: "managed.send", target: tenantRef("tenant-1")},
		{name: "stop", operation: OperationStop, invoke: func(service *Service) (OperationResult, error) {
			return service.Stop(context.Background(), StopRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), IdempotencyKey: "stop-key"})
		}, applyCall: "managed.stop", target: tenantRef("tenant-1")},
		{name: "attach", operation: OperationAttach, invoke: func(service *Service) (OperationResult, error) {
			return service.Attach(context.Background(), AttachRequest{Context: testRequestContext(), Target: tenantRef("target-1"), TargetTaskID: 17, TargetContextWindowTokens: 16_384, Sources: []SessionRef{tenantRef("source-1")}, IdempotencyKey: "attach-key"})
		}, applyCall: "handoff.attach", target: tenantRef("target-1")},
		{name: "refresh", operation: OperationHandoffRefresh, invoke: func(service *Service) (OperationResult, error) {
			return service.RefreshHandoff(context.Background(), RefreshHandoffRequest{Context: testRequestContext(), Target: tenantRef("target-1"), TargetTaskID: 18, TargetContextWindowTokens: 16_384, Source: tenantRef("source-1"), PreviousPackageID: "handoff:prior", PreviousEventID: 51, IdempotencyKey: "refresh-key"})
		}, applyCall: "handoff.refresh", target: tenantRef("target-1")},
		{name: "monitor", operation: OperationMonitor, invoke: func(service *Service) (OperationResult, error) {
			return service.Monitor(context.Background(), MonitorRequest{Context: testRequestContext(), Target: tenantRef("target-1"), Sources: []SessionRef{tenantRef("source-1")}, IntervalSeconds: 300, Channel: "feishu", IdempotencyKey: "monitor-key"})
		}, applyCall: "monitor.apply", target: tenantRef("target-1")},
	} {
		t.Run(test.name+" new", func(t *testing.T) {
			fixture := newOperationRecoveryFixture()
			result, err := test.invoke(fixture.service)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"authorize:" + string(test.operation), "recover:" + string(test.operation), test.applyCall}
			if test.operation == OperationAttach || test.operation == OperationHandoffRefresh {
				want = append(want, "handoff.readback")
			}
			want = append(want, "managed.get", "audit:"+string(test.operation))
			assertCalls(t, fixture.calls, want)
			if result.OperationID == "" || result.Replayed || result.AuditID != 9 || result.Session.Ref != test.target {
				t.Fatalf("result = %#v", result)
			}
		})

		t.Run(test.name+" replay", func(t *testing.T) {
			fixture := newOperationRecoveryFixture()
			fixture.recovery.replay[test.operation] = true
			result, err := test.invoke(fixture.service)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"authorize:" + string(test.operation), "recover:" + string(test.operation)}
			if test.operation == OperationAttach || test.operation == OperationHandoffRefresh {
				want = append(want, "handoff.readback")
			}
			want = append(want, "managed.get", "audit:"+string(test.operation))
			assertCalls(t, fixture.calls, want)
			if result.OperationID == "" || !result.Replayed || result.AuditID != 9 || result.Session.Ref != test.target {
				t.Fatalf("replay result = %#v", result)
			}
		})
	}
}

func TestServiceRetriesReadbackAndAuditFromAuthoritativeRecovery(t *testing.T) {
	for _, failure := range []string{"readback", "audit"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newOperationRecoveryFixture()
			request := SendRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), Content: "message", IdempotencyKey: "send-key"}
			if failure == "readback" {
				fixture.managed.getErr = errors.New("readback unavailable")
			} else {
				fixture.audit.err = errors.New("audit unavailable")
			}
			if _, err := fixture.service.Send(context.Background(), request); err == nil {
				t.Fatal("first Send() error = nil")
			}
			fixture.managed.getErr = nil
			fixture.audit.err = nil
			fixture.recovery.replay[OperationSend] = true
			*fixture.calls = nil

			result, err := fixture.service.Send(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			assertCalls(t, fixture.calls, []string{"authorize:send", "recover:send", "managed.get", "audit:send"})
			if !result.Replayed || result.RunID != 5 || result.Session.Ref != request.Ref {
				t.Fatalf("recovered result = %#v", result)
			}
		})
	}
}

func TestServiceRejectsLocalTargetsBeforeOperationRecovery(t *testing.T) {
	for name, invoke := range map[string]func(*Service) error{
		"send": func(service *Service) error {
			_, err := service.Send(context.Background(), SendRequest{Context: testRequestContext(), Ref: localRef("local-1"), Content: "message", IdempotencyKey: "send-key"})
			return err
		},
		"stop": func(service *Service) error {
			_, err := service.Stop(context.Background(), StopRequest{Context: testRequestContext(), Ref: localRef("local-1"), IdempotencyKey: "stop-key"})
			return err
		},
		"attach": func(service *Service) error {
			_, err := service.Attach(context.Background(), AttachRequest{Context: testRequestContext(), Target: localRef("local-1"), TargetTaskID: 17, TargetContextWindowTokens: 16_384, Sources: []SessionRef{tenantRef("source-1")}, IdempotencyKey: "attach-key"})
			return err
		},
		"monitor": func(service *Service) error {
			_, err := service.Monitor(context.Background(), MonitorRequest{Context: testRequestContext(), Target: localRef("local-1"), Sources: []SessionRef{tenantRef("source-1")}, IntervalSeconds: 300, Channel: "feishu", IdempotencyKey: "monitor-key"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOperationRecoveryFixture()
			assertServiceErrorCode(t, invoke(fixture.service), CodeForbidden)
			assertCalls(t, fixture.calls, nil)
		})
	}
}

func TestServiceAuditUsesOnlyHashedOperationIdentity(t *testing.T) {
	fixture := newOperationRecoveryFixture()
	request := SendRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), Content: "private message", IdempotencyKey: "private-key"}
	if _, err := fixture.service.Send(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(fixture.audit.records) != 1 {
		t.Fatalf("audit records = %#v", fixture.audit.records)
	}
	record := fixture.audit.records[0]
	if !validOperationHash(record.OperationID) || !validOperationHash(record.KeyHash) || !validOperationHash(record.Fingerprint) {
		t.Fatalf("audit identity is incomplete: %#v", record)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), request.IdempotencyKey) || strings.Contains(string(payload), request.Content) {
		t.Fatalf("audit leaked private request data: %s", payload)
	}
}

type operationRecoveryFixture struct {
	service  *Service
	calls    *[]string
	managed  *managedPortFake
	recovery *operationRecoveryFake
	audit    *auditFake
}

func newOperationRecoveryFixture() *operationRecoveryFixture {
	calls := []string{}
	managed := &managedPortFake{calls: &calls}
	recovery := &operationRecoveryFake{calls: &calls, replay: make(map[Operation]bool), got: make(map[Operation]OperationIdentity), err: make(map[Operation]error)}
	audit := &auditFake{calls: &calls}
	service := NewService(Dependencies{
		Managed: managed, Local: &localPortFake{calls: &calls}, Authorizer: authorizerFake{calls: &calls}, Audit: audit,
		CreateRecovery: recovery, SendRecovery: recovery, StopRecovery: recovery, AttachRecovery: recovery, RefreshRecovery: recovery, MonitorRecovery: recovery,
		Handoff: handoffPortFake{calls: &calls}, Monitor: monitorPortFake{calls: &calls}, Clock: fixedClock{},
	})
	return &operationRecoveryFixture{service: service, calls: &calls, managed: managed, recovery: recovery, audit: audit}
}

type operationRecoveryFake struct {
	calls  *[]string
	replay map[Operation]bool
	got    map[Operation]OperationIdentity
	err    map[Operation]error
}

func (f *operationRecoveryFake) result(operation Operation, identity OperationIdentity, target SessionRef) (RecoveredOperation, error) {
	*f.calls = append(*f.calls, "recover:"+string(operation))
	f.got[operation] = identity
	if f.err[operation] != nil {
		return RecoveredOperation{}, f.err[operation]
	}
	if !f.replay[operation] {
		return RecoveredOperation{}, nil
	}
	result := OperationResult{OperationID: identity.OperationID, Session: SessionSnapshot{Ref: target}}
	switch operation {
	case OperationSend:
		result.RunID = 5
	case OperationStop:
		result.RunID = 6
	case OperationAttach:
		result.Handoff = HandoffResult{TargetTaskID: 17, SourceResults: []HandoffSourceResult{{Source: tenantRef("source-1"), Success: true, LinkID: 41, EventID: 51, PackageID: "pkg", PackageSHA256Prefix: "sha"}}}
		result.LinkIDs = []uint64{41}
	case OperationHandoffRefresh:
		result.Handoff = HandoffResult{TargetTaskID: 18, SourceResults: []HandoffSourceResult{{Source: tenantRef("source-1"), Success: true, LinkID: 41, EventID: 52, PackageID: "pkg-refresh", PackageSHA256Prefix: "sha"}}}
		result.LinkIDs = []uint64{41}
	}
	return RecoveredOperation{Found: true, Metadata: identity.Metadata(), Target: target, Result: result}, nil
}

func (f *operationRecoveryFake) RecoverCreate(_ context.Context, request CreateRequest, identity OperationIdentity) (RecoveredOperation, error) {
	return f.result(OperationCreate, identity, tenantRef(request.SessionKey))
}
func (f *operationRecoveryFake) RecoverSend(_ context.Context, request SendRequest, identity OperationIdentity) (RecoveredOperation, error) {
	return f.result(OperationSend, identity, request.Ref)
}
func (f *operationRecoveryFake) RecoverStop(_ context.Context, request StopRequest, identity OperationIdentity) (RecoveredOperation, error) {
	return f.result(OperationStop, identity, request.Ref)
}
func (f *operationRecoveryFake) RecoverAttach(_ context.Context, request AttachRequest, identity OperationIdentity) (RecoveredOperation, error) {
	return f.result(OperationAttach, identity, request.Target)
}
func (f *operationRecoveryFake) RecoverRefresh(_ context.Context, request RefreshHandoffRequest, identity OperationIdentity) (RecoveredOperation, error) {
	return f.result(OperationHandoffRefresh, identity, request.Target)
}
func (f *operationRecoveryFake) RecoverMonitor(_ context.Context, request MonitorRequest, identity OperationIdentity) (RecoveredOperation, error) {
	return f.result(OperationMonitor, identity, request.Target)
}

type monitorPortFake struct{ calls *[]string }

func (f monitorPortFake) Monitor(_ context.Context, request MonitorRequest) (OperationResult, error) {
	*f.calls = append(*f.calls, "monitor.apply")
	return OperationResult{Session: SessionSnapshot{Ref: request.Target}}, nil
}
