package sessioncontrol

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServiceRejectsInvalidContextBeforePorts(t *testing.T) {
	fixture := newServiceFixture()
	_, err := fixture.service.Create(context.Background(), CreateRequest{IdempotencyKey: "create-1"})
	assertServiceErrorCode(t, err, CodeInvalidState)
	assertCalls(t, fixture.calls, nil)
}

func TestServiceListAndGetDispatchBySource(t *testing.T) {
	fixture := newServiceFixture()
	ctx := context.Background()
	requestContext := testRequestContext()
	if _, err := fixture.service.List(ctx, ListRequest{Context: requestContext, Source: SourceTenant}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.List(ctx, ListRequest{Context: requestContext, Source: SourceLocal}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Get(ctx, GetRequest{Context: requestContext, Ref: tenantRef("tenant-1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Get(ctx, GetRequest{Context: requestContext, Ref: localRef("local-1")}); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, fixture.calls, []string{"authorize:list", "managed.list", "authorize:list", "local.list", "authorize:get", "managed.get", "authorize:get", "local.get"})
}

func TestServiceRejectsMalformedDirectRefBeforeAdapter(t *testing.T) {
	for _, ref := range []SessionRef{
		{Source: SourceTenant, Key: "bad key"},
		{Source: Source("TENANT"), Key: "session-1"},
		{Source: SourceLocal, Key: "bad:key"},
	} {
		fixture := newServiceFixture()
		_, err := fixture.service.Get(context.Background(), GetRequest{Context: testRequestContext(), Ref: ref})
		var refErr *RefError
		if !errors.As(err, &refErr) || refErr.Code != CodeInvalidRef {
			t.Fatalf("Get(%#v) error = %v, want invalid_ref", ref, err)
		}
		assertCalls(t, fixture.calls, nil)
	}
}

func TestServiceRejectsLocalMutationBeforePorts(t *testing.T) {
	fixture := newServiceFixture()
	_, err := fixture.service.Send(context.Background(), SendRequest{Context: testRequestContext(), Ref: localRef("local-1"), IdempotencyKey: "send-1"})
	assertServiceErrorCode(t, err, CodeForbidden)
	assertCalls(t, fixture.calls, nil)
}

func TestServiceCoreMutationsRejectEmptyIdempotencyBeforePorts(t *testing.T) {
	for name, invoke := range map[string]func(*Service) error{
		"create": func(service *Service) error {
			_, err := service.Create(context.Background(), CreateRequest{Context: testRequestContext()})
			return err
		},
		"send": func(service *Service) error {
			_, err := service.Send(context.Background(), SendRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1")})
			return err
		},
		"stop": func(service *Service) error {
			_, err := service.Stop(context.Background(), StopRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1")})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture()
			assertServiceErrorCode(t, invoke(fixture.service), CodeInvalidState)
			assertCalls(t, fixture.calls, nil)
		})
	}
}

func TestServiceCreateOrdersRecoveryMutationReadbackAudit(t *testing.T) {
	fixture := newServiceFixture()
	result, err := fixture.service.Create(context.Background(), CreateRequest{Context: testRequestContext(), SessionKey: "created-1", Title: "Demo", IdempotencyKey: "create-1"})
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, fixture.calls, []string{"authorize:create", "recover:create", "managed.create", "managed.get", "audit:create"})
	assertSuccessfulResult(t, result, "tenant:created-1", 0)
}

func TestServiceSendOrdersRecoveryMutationReadbackAudit(t *testing.T) {
	fixture := newServiceFixture()
	result, err := fixture.service.Send(context.Background(), SendRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), Content: "private", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, fixture.calls, []string{"authorize:send", "recover:send", "managed.send", "managed.get", "audit:send"})
	assertSuccessfulResult(t, result, "tenant:tenant-1", 5)
}

func TestServiceStopOrdersRecoveryMutationReadbackAudit(t *testing.T) {
	fixture := newServiceFixture()
	result, err := fixture.service.Stop(context.Background(), StopRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), IdempotencyKey: "stop-1"})
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, fixture.calls, []string{"authorize:stop", "recover:stop", "managed.stop", "managed.get", "audit:stop"})
	assertSuccessfulResult(t, result, "tenant:tenant-1", 6)
}

func TestServiceFingerprintExcludesTraceIDAndIncludesSemanticContent(t *testing.T) {
	first := newServiceFixture()
	request := SendRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), Content: "first", IdempotencyKey: "send-1"}
	request.Context.TraceID = "trace-a"
	if _, err := first.service.Send(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	second := newServiceFixture()
	request.Context.TraceID = "trace-b"
	if _, err := second.service.Send(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	changed := newServiceFixture()
	request.Content = "second"
	if _, err := changed.service.Send(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	firstIdentity := first.recovery.got[OperationSend]
	secondIdentity := second.recovery.got[OperationSend]
	changedIdentity := changed.recovery.got[OperationSend]
	if firstIdentity.Fingerprint == "" || firstIdentity.Fingerprint != secondIdentity.Fingerprint {
		t.Fatalf("trace changed fingerprint: %q != %q", firstIdentity.Fingerprint, secondIdentity.Fingerprint)
	}
	if firstIdentity.Fingerprint == changedIdentity.Fingerprint || firstIdentity.KeyHash != changedIdentity.KeyHash {
		t.Fatalf("semantic content did not preserve key lookup and change fingerprint: %#v %#v", firstIdentity, changedIdentity)
	}
}

func TestServiceAttachFingerprintIsIndependentOfSourceOrder(t *testing.T) {
	first, second := newServiceFixture(), newServiceFixture()
	request := AttachRequest{Context: testRequestContext(), Target: tenantRef("target-1"), TargetTaskID: 17, TargetContextWindowTokens: 16_384, Sources: []SessionRef{tenantRef("source-b"), localRef("source-a")}, IdempotencyKey: "attach-1"}
	if _, err := first.service.Attach(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Sources[0], request.Sources[1] = request.Sources[1], request.Sources[0]
	if _, err := second.service.Attach(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if first.recovery.got[OperationAttach].Fingerprint != second.recovery.got[OperationAttach].Fingerprint {
		t.Fatalf("source order changed attach fingerprint: %#v != %#v", first.recovery.got[OperationAttach], second.recovery.got[OperationAttach])
	}
}

func TestServiceAttachChangedBodyWithSameKeyConflictsBeforeHandoff(t *testing.T) {
	fixture := newServiceFixture()
	fixture.recovery.err[OperationAttach] = &ServiceError{Code: CodeIdempotencyConflict, Message: "semantic request changed"}
	_, err := fixture.service.Attach(context.Background(), AttachRequest{Context: testRequestContext(), Target: tenantRef("target-1"), TargetTaskID: 17, TargetContextWindowTokens: 16_384, Sources: []SessionRef{tenantRef("changed-source")}, IdempotencyKey: "attach-1"})
	assertServiceErrorCode(t, err, CodeIdempotencyConflict)
	assertCalls(t, fixture.calls, []string{"authorize:attach", "recover:attach"})
}

func TestServiceAttachAllowsLocalSourceAndUsesMutationPipeline(t *testing.T) {
	fixture := newServiceFixture()
	request := AttachRequest{
		Context:                   testRequestContext(),
		Target:                    tenantRef("target-1"),
		TargetTaskID:              17,
		TargetContextWindowTokens: 16_384,
		Sources:                   []SessionRef{localRef("local-1"), tenantRef("source-1")},
		RelationType:              "attached",
		IdempotencyKey:            "attach-1",
	}
	result, err := fixture.service.Attach(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, fixture.calls, []string{"authorize:attach", "recover:attach", "handoff.attach", "handoff.readback", "managed.get", "audit:attach"})
	if !reflect.DeepEqual(result.LinkIDs, []uint64{41, 42}) || result.Session.Ref != request.Target {
		t.Fatalf("Attach result = %#v", result)
	}
}

func TestServiceAttachRejectsLocalTargetBeforePorts(t *testing.T) {
	fixture := newServiceFixture()
	_, err := fixture.service.Attach(context.Background(), AttachRequest{Context: testRequestContext(), Target: localRef("target-1"), Sources: []SessionRef{tenantRef("source-1")}, IdempotencyKey: "attach-1"})
	assertServiceErrorCode(t, err, CodeForbidden)
	assertCalls(t, fixture.calls, nil)
}

func TestServiceAttachAndMonitorRejectEmptyIdempotencyBeforePorts(t *testing.T) {
	for name, invoke := range map[string]func(*Service) error{
		"attach": func(service *Service) error {
			_, err := service.Attach(context.Background(), AttachRequest{Context: testRequestContext(), Target: tenantRef("target-1"), Sources: []SessionRef{localRef("source-1")}})
			return err
		},
		"monitor": func(service *Service) error {
			_, err := service.Monitor(context.Background(), MonitorRequest{Context: testRequestContext(), Target: tenantRef("target-1"), Sources: []SessionRef{localRef("source-1")}, IntervalSeconds: 60, Channel: "feishu"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture()
			assertServiceErrorCode(t, invoke(fixture.service), CodeInvalidState)
			assertCalls(t, fixture.calls, nil)
		})
	}
}

func TestServiceMonitorValidatesConfigurationBeforeUnavailable(t *testing.T) {
	valid := MonitorRequest{Context: testRequestContext(), Target: tenantRef("target-1"), Sources: []SessionRef{localRef("source-1")}, IntervalSeconds: 60, Channel: "feishu", IdempotencyKey: "monitor-1"}
	for name, mutate := range map[string]func(*MonitorRequest){
		"local target":       func(request *MonitorRequest) { request.Target = localRef("target-1") },
		"malformed ref":      func(request *MonitorRequest) { request.Sources = []SessionRef{{Source: SourceLocal, Key: "bad key"}} },
		"zero interval":      func(request *MonitorRequest) { request.IntervalSeconds = 0 },
		"oversized interval": func(request *MonitorRequest) { request.IntervalSeconds = SessionMonitorMaxIntervalSeconds + 1 },
		"empty channel":      func(request *MonitorRequest) { request.Channel = " " },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture()
			request := valid
			mutate(&request)
			_, err := fixture.service.Monitor(context.Background(), request)
			if err == nil {
				t.Fatal("Monitor error = nil")
			}
			assertCalls(t, fixture.calls, nil)
		})
	}
	fixture := newServiceFixture()
	fixture.service.deps.MonitorRecovery = nil
	result, err := fixture.service.Monitor(context.Background(), valid)
	assertServiceErrorCode(t, err, CodeSchedulerUnavailable)
	if result.ErrorCode != string(CodeSchedulerUnavailable) {
		t.Fatalf("Monitor result = %#v", result)
	}
	assertCalls(t, fixture.calls, []string{"authorize:monitor"})
}

func TestServiceFailurePathsStopPipeline(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*serviceFixture)
		want  []string
	}{
		{name: "mutation", setup: func(f *serviceFixture) { f.managed.sendErr = errors.New("send failed") }, want: []string{"authorize:send", "recover:send", "managed.send"}},
		{name: "readback", setup: func(f *serviceFixture) { f.managed.getErr = errors.New("readback failed") }, want: []string{"authorize:send", "recover:send", "managed.send", "managed.get"}},
		{name: "audit", setup: func(f *serviceFixture) { f.audit.err = errors.New("audit failed") }, want: []string{"authorize:send", "recover:send", "managed.send", "managed.get", "audit:send"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture()
			test.setup(fixture)
			_, err := fixture.service.Send(context.Background(), SendRequest{Context: testRequestContext(), Ref: tenantRef("tenant-1"), Content: "message", IdempotencyKey: "send-1"})
			if err == nil {
				t.Fatal("Send error = nil")
			}
			assertCalls(t, fixture.calls, test.want)
		})
	}
}

type serviceFixture struct {
	service  *Service
	calls    *[]string
	managed  *managedPortFake
	recovery *operationRecoveryFake
	audit    *auditFake
}

func newServiceFixture() *serviceFixture {
	calls := []string{}
	managed := &managedPortFake{calls: &calls}
	recovery := &operationRecoveryFake{calls: &calls, replay: make(map[Operation]bool), got: make(map[Operation]OperationIdentity), err: make(map[Operation]error)}
	audit := &auditFake{calls: &calls}
	return &serviceFixture{
		calls:    &calls,
		managed:  managed,
		recovery: recovery,
		audit:    audit,
		service: NewService(Dependencies{
			Managed: managed, Local: &localPortFake{calls: &calls}, Authorizer: authorizerFake{calls: &calls}, Audit: audit,
			CreateRecovery: recovery, SendRecovery: recovery, StopRecovery: recovery, AttachRecovery: recovery, RefreshRecovery: recovery, MonitorRecovery: recovery,
			Monitor: monitorPortFake{calls: &calls}, Links: linkPortFake{calls: &calls}, Handoff: handoffPortFake{calls: &calls},
			Clock: fixedClock{now: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)},
		}),
	}
}

func testRequestContext() RequestContext {
	return RequestContext{TenantID: 1, UserID: 2, ActorUserID: 3}
}

func tenantRef(key string) SessionRef { return SessionRef{Source: SourceTenant, Key: key} }
func localRef(key string) SessionRef  { return SessionRef{Source: SourceLocal, Key: key} }

func assertServiceErrorCode(t *testing.T, err error, want ServiceErrorCode) {
	t.Helper()
	var typed *ServiceError
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("error = %v, want code %q", err, want)
	}
}

func assertCalls(t *testing.T, got *[]string, want []string) {
	t.Helper()
	if len(*got) != len(want) {
		t.Fatalf("calls = %v, want %v", *got, want)
	}
	for index := range want {
		if (*got)[index] != want[index] {
			t.Fatalf("calls = %v, want %v", *got, want)
		}
	}
}

func assertSuccessfulResult(t *testing.T, result OperationResult, ref string, runID uint64) {
	t.Helper()
	if !validOperationHash(result.OperationID) || result.AuditID != 9 || result.Replayed || result.Session.Ref.String() != ref || result.RunID != runID {
		t.Fatalf("result = %#v", result)
	}
}

type managedPortFake struct {
	calls   *[]string
	sendErr error
	getErr  error
}

func (f *managedPortFake) Create(context.Context, CreateRequest) (SessionSnapshot, error) {
	*f.calls = append(*f.calls, "managed.create")
	return SessionSnapshot{Ref: tenantRef("created-1")}, nil
}
func (f *managedPortFake) List(context.Context, ListRequest) ([]SessionSnapshot, error) {
	*f.calls = append(*f.calls, "managed.list")
	return nil, nil
}
func (f *managedPortFake) Get(_ context.Context, request GetRequest) (SessionSnapshot, error) {
	*f.calls = append(*f.calls, "managed.get")
	if f.getErr != nil {
		return SessionSnapshot{}, f.getErr
	}
	return SessionSnapshot{Ref: request.Ref}, nil
}
func (f *managedPortFake) Send(context.Context, SendRequest) (OperationResult, error) {
	*f.calls = append(*f.calls, "managed.send")
	return OperationResult{RunID: 5}, f.sendErr
}
func (f *managedPortFake) Stop(context.Context, StopRequest) (OperationResult, error) {
	*f.calls = append(*f.calls, "managed.stop")
	return OperationResult{RunID: 6}, nil
}

type localPortFake struct{ calls *[]string }

func (f *localPortFake) List(context.Context, ListRequest) ([]SessionSnapshot, error) {
	*f.calls = append(*f.calls, "local.list")
	return nil, nil
}
func (f *localPortFake) Get(_ context.Context, request GetRequest) (SessionSnapshot, error) {
	*f.calls = append(*f.calls, "local.get")
	return SessionSnapshot{Ref: request.Ref}, nil
}

type authorizerFake struct{ calls *[]string }

func (f authorizerFake) Authorize(_ context.Context, _ RequestContext, operation Operation) error {
	*f.calls = append(*f.calls, "authorize:"+string(operation))
	return nil
}

type linkPortFake struct{ calls *[]string }

func (f linkPortFake) Attach(context.Context, RequestContext, AttachRequest) ([]SessionLink, error) {
	*f.calls = append(*f.calls, "link.attach")
	return []SessionLink{{ID: 41}, {ID: 42}}, nil
}

type handoffPortFake struct{ calls *[]string }

func (f handoffPortFake) Attach(_ context.Context, request AttachRequest) (HandoffResult, error) {
	*f.calls = append(*f.calls, "handoff.attach")
	result := HandoffResult{TargetTaskID: request.TargetTaskID, SourceResults: make([]HandoffSourceResult, 0, len(request.Sources))}
	for index, source := range request.Sources {
		result.SourceResults = append(result.SourceResults, HandoffSourceResult{Source: source, LinkID: uint64(41 + index), EventID: uint64(51 + index), PackageID: "pkg", PackageSHA256Prefix: "sha", Success: true})
	}
	return result, nil
}

type handoffReadbackErrorFake struct{ calls *[]string }

func (f handoffReadbackErrorFake) Attach(context.Context, AttachRequest) (HandoffResult, error) {
	return HandoffResult{}, errors.New("unexpected attach")
}
func (f handoffReadbackErrorFake) Read(context.Context, HandoffReadRequest) (HandoffResult, error) {
	return HandoffResult{}, nil
}
func (f handoffReadbackErrorFake) Refresh(context.Context, RefreshHandoffRequest) (HandoffResult, error) {
	return HandoffResult{}, nil
}
func (f handoffReadbackErrorFake) Readback(context.Context, HandoffReadbackRequest) (HandoffResult, error) {
	*f.calls = append(*f.calls, "handoff.readback")
	return HandoffResult{}, errors.New("event missing")
}

func (f handoffPortFake) Read(context.Context, HandoffReadRequest) (HandoffResult, error) {
	return HandoffResult{}, nil
}
func (f handoffPortFake) Refresh(context.Context, RefreshHandoffRequest) (HandoffResult, error) {
	*f.calls = append(*f.calls, "handoff.refresh")
	return HandoffResult{TargetTaskID: 18, SourceResults: []HandoffSourceResult{{Source: tenantRef("source-1"), Success: true, LinkID: 41, EventID: 52, PackageID: "pkg-refresh", PackageSHA256Prefix: "sha"}}}, nil
}
func (f handoffPortFake) Readback(_ context.Context, request HandoffReadbackRequest) (HandoffResult, error) {
	*f.calls = append(*f.calls, "handoff.readback")
	return request.Expected, nil
}

type auditFake struct {
	calls   *[]string
	err     error
	records []AuditRecord
}

func (f *auditFake) Record(_ context.Context, record AuditRecord) (uint64, error) {
	*f.calls = append(*f.calls, "audit:"+string(record.Operation))
	f.records = append(f.records, record)
	return 9, f.err
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }
