package handoff

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
)

func TestHandoffServiceAttachCapturesSourcesInCanonicalOrderAndPersistsEvents(t *testing.T) {
	calls := []string{}
	service := NewService(Dependencies{
		Sources: map[sessioncontrol.Source]SourceReader{
			sessioncontrol.SourceTenant: sourceReaderFake{calls: &calls, snapshot: testSourceSnapshot("tenant:source-b", "message:2")},
			sessioncontrol.SourceLocal:  sourceReaderFake{calls: &calls, snapshot: testSourceSnapshot("local:source-a", "entry:one")},
		},
		Target:      targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}},
		Persistence: &persistenceFake{calls: &calls, results: []PersistResult{{LinkID: 101, EventID: 201}, {LinkID: 102, EventID: 202}}},
	})

	result, err := service.Attach(context.Background(), sessioncontrol.AttachRequest{
		Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000,
		Sources: []sessioncontrol.SessionRef{tenantRef("source-b"), localRef("source-a")}, OperationIdentity: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if got, want := result.SourceResults[0].EventID, uint64(201); got != want {
		t.Fatalf("local source event ID = %d, want %d", got, want)
	}
	if got, want := result.SourceResults[1].EventID, uint64(202); got != want {
		t.Fatalf("tenant source event ID = %d, want %d", got, want)
	}
	if !reflect.DeepEqual(calls, []string{"capture:local:source-a", "capture:tenant:source-b", "persist:local:source-a", "persist:tenant:source-b"}) {
		t.Fatalf("calls = %v", calls)
	}
	if persistence := service.deps.Persistence.(*persistenceFake); persistence.batchCalls != 1 {
		t.Fatalf("batch persistence calls = %d, want 1", persistence.batchCalls)
	}
}

func TestHandoffOperationMetadataRequiresMatchingKeyHash(t *testing.T) {
	identity := sessioncontrol.OperationIdentity{
		Schema:      "niuma.session-control-operation.v1",
		Operation:   sessioncontrol.OperationAttach,
		OperationID: strings.Repeat("b", 64),
		KeyHash:     strings.Repeat("a", 64),
		Fingerprint: strings.Repeat("c", 64),
	}

	if _, err := handoffOperationMetadataJSON(strings.Repeat("d", 64), identity); err == nil {
		t.Fatal("handoffOperationMetadataJSON() error = nil for mismatched key hash")
	}
	if got, err := handoffOperationMetadataJSON(strings.Repeat("a", 64), sessioncontrol.OperationIdentity{}); err != nil || got != "" {
		t.Fatalf("legacy metadata = %q, err = %v", got, err)
	}
}

func TestHandoffServiceBatchFailureMarksNoSourceSuccessful(t *testing.T) {
	persistence := &persistenceFake{batchErr: errors.New("transaction rolled back")}
	service := NewService(Dependencies{Sources: map[sessioncontrol.Source]SourceReader{sessioncontrol.SourceTenant: sourceReaderFake{snapshot: testSourceSnapshot("tenant:source", "message:2")}}, Target: targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}}, Persistence: persistence})
	result, err := service.Attach(context.Background(), sessioncontrol.AttachRequest{Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000, Sources: []sessioncontrol.SessionRef{tenantRef("source")}, OperationIdentity: strings.Repeat("a", 64)})
	if err == nil || result.SourceResults[0].Success || persistence.batchCalls != 1 {
		t.Fatalf("Attach() result=%#v error=%v batch_calls=%d", result, err, persistence.batchCalls)
	}
}

func TestHandoffServiceAttachDoesNotPersistWhenAnySourceCaptureFails(t *testing.T) {
	calls := []string{}
	service := NewService(Dependencies{
		Sources: map[sessioncontrol.Source]SourceReader{
			sessioncontrol.SourceLocal: sourceReaderFake{calls: &calls, err: &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "source denied"}},
		},
		Target:      targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}},
		Persistence: &persistenceFake{calls: &calls},
	})

	result, err := service.Attach(context.Background(), sessioncontrol.AttachRequest{
		Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000,
		Sources: []sessioncontrol.SessionRef{localRef("source-a")}, OperationIdentity: strings.Repeat("a", 64),
	})
	if err == nil || result.SourceResults[0].Success || result.SourceResults[0].ErrorCode != string(sessioncontrol.CodeForbidden) {
		t.Fatalf("Attach() result/error = %#v / %v", result, err)
	}
	if !reflect.DeepEqual(calls, []string{"capture:local:source-a"}) {
		t.Fatalf("calls = %v, want no persistence", calls)
	}
}

func TestHandoffServiceAttachRejectsAggregateBudgetBeforePersistence(t *testing.T) {
	calls := []string{}
	service := NewService(Dependencies{
		Sources: map[sessioncontrol.Source]SourceReader{
			sessioncontrol.SourceTenant: sourceReaderFake{calls: &calls, snapshot: testSourceSnapshot("tenant:source", "message:2")},
		},
		Target:      targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}},
		Persistence: &persistenceFake{calls: &calls},
	})

	result, err := service.Attach(context.Background(), sessioncontrol.AttachRequest{
		Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 1,
		Sources: []sessioncontrol.SessionRef{tenantRef("source")}, OperationIdentity: strings.Repeat("a", 64),
	})
	assertServiceCode(t, err, sessioncontrol.CodeBudgetExceeded)
	if len(result.SourceResults) != 1 || result.SourceResults[0].Success || result.SourceResults[0].ErrorCode != string(sessioncontrol.CodeBudgetExceeded) || result.SourceResults[0].PackageSHA256Prefix == "" || result.SourceResults[0].EstimatedTokens == 0 || len(result.SourceResults[0].CandidateRemovals) == 0 || result.EstimatedTokens != result.SourceResults[0].EstimatedTokens {
		t.Fatalf("budget failure result does not describe final attempted state: %#v", result)
	}
	if !reflect.DeepEqual(calls, []string{"capture:tenant:source"}) {
		t.Fatalf("calls = %v, want no persistence", calls)
	}
}

func TestHandoffServiceAttachRejectsNonReadyTargetBeforeCapture(t *testing.T) {
	calls := []string{}
	service := NewService(Dependencies{
		Sources: map[sessioncontrol.Source]SourceReader{
			sessioncontrol.SourceTenant: sourceReaderFake{calls: &calls, snapshot: testSourceSnapshot("tenant:source", "message:2")},
		},
		Target:      targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "running"}},
		Persistence: &persistenceFake{calls: &calls},
	})

	_, err := service.Attach(context.Background(), sessioncontrol.AttachRequest{
		Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000,
		Sources: []sessioncontrol.SessionRef{tenantRef("source")}, OperationIdentity: strings.Repeat("a", 64),
	})
	assertServiceCode(t, err, sessioncontrol.CodeInvalidState)
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want no capture/persistence", calls)
	}
}

func TestHandoffAttachRecoversDurableBatchBeforeReadyOrSourceChecks(t *testing.T) {
	calls := []string{}
	persistence := &persistenceFake{recovery: RecoverBatchResult{Found: true, Items: []agenttasks.HandoffRecoveredItem{{LinkID: 101, EventID: 202, SourceRef: "tenant:source", SourceCursor: "message:2", PackageID: "handoff:existing", PackageSHA256: strings.Repeat("a", 64), EstimatedTokens: 123}}}}
	service := NewService(Dependencies{Sources: map[sessioncontrol.Source]SourceReader{sessioncontrol.SourceTenant: sourceReaderFake{calls: &calls, err: errors.New("source changed")}}, Target: targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "running"}}, Persistence: persistence})
	result, err := service.Attach(context.Background(), sessioncontrol.AttachRequest{Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000, Sources: []sessioncontrol.SessionRef{tenantRef("source")}, OperationIdentity: strings.Repeat("b", 64)})
	if err != nil || !result.Replayed || result.SourceResults[0].EventID != 202 || len(calls) != 0 || persistence.batchCalls != 0 || persistence.recoverCalls != 1 {
		t.Fatalf("Attach() result=%#v error=%v calls=%v persistence=%#v", result, err, calls, persistence)
	}
}

func TestHandoffServiceObservesOnlyRedactedAttachMetadata(t *testing.T) {
	observed := []Observation{}
	service := NewService(Dependencies{
		Sources: map[sessioncontrol.Source]SourceReader{
			sessioncontrol.SourceTenant: sourceReaderFake{snapshot: testSourceSnapshot("tenant:source", "message:2")},
		},
		Target:      targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}},
		Persistence: &persistenceFake{},
		Observer: observerFunc(func(_ context.Context, observation Observation) {
			observed = append(observed, observation)
		}),
	})
	request := sessioncontrol.AttachRequest{
		Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11, TraceID: "trace-1"},
		Target:  tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000,
		Sources:           []sessioncontrol.SessionRef{tenantRef("source")},
		OperationIdentity: strings.Repeat("a", 64),
	}
	if _, err := service.Attach(context.Background(), request); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if len(observed) != 1 || observed[0].Operation != "attach" || observed[0].TraceID != "trace-1" || observed[0].SourceCount != 1 || observed[0].PackageSHA256Prefixes[0] == "" || observed[0].ErrorCode != "" {
		t.Fatalf("observation = %#v", observed)
	}
	if reflect.TypeOf(observed[0]).NumField() != 14 {
		t.Fatalf("observation must remain a narrow redacted contract: %#v", observed[0])
	}
}

func TestHandoffServiceRefreshAppendsLineageOnlyWhenSourceFingerprintChanged(t *testing.T) {
	old, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "message:2"}, Target: Target{Ref: "tenant:target"}, Objective: "continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeSessionHandoffEvent(old, 55)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &persistenceFake{results: []PersistResult{{LinkID: 101, EventID: 202}}}
	service := NewService(Dependencies{
		Sources:     map[sessioncontrol.Source]SourceReader{sessioncontrol.SourceTenant: sourceReaderFake{snapshot: testSourceSnapshot("tenant:source", "message:3")}},
		Target:      targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}, event: sessioncontrol.HandoffEvent{EventID: 70, TaskID: 55, PayloadJSON: payload}},
		Persistence: persistence,
	})
	result, err := service.Refresh(context.Background(), sessioncontrol.RefreshHandoffRequest{
		Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000,
		Source: tenantRef("source"), PreviousPackageID: old.PackageID, PreviousEventID: 70,
		OperationIdentity: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.Replayed || result.SourceResults[0].EventID != 202 || len(persistence.requests) != 1 || persistence.requests[0].RefreshOfPackageID != old.PackageID || persistence.requests[0].RefreshOfEventID != 70 {
		t.Fatalf("refresh result/request = %#v / %#v", result, persistence.requests)
	}
}

func TestHandoffRefreshUnchangedSourceAndTargetReturnsOriginalIdentities(t *testing.T) {
	old, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "message:2"}, Target: Target{Ref: "tenant:target"}, Objective: "continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeSessionHandoffEvent(old, 55)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &persistenceFake{}
	service := NewService(Dependencies{Sources: map[sessioncontrol.Source]SourceReader{sessioncontrol.SourceTenant: sourceReaderFake{snapshot: testSourceSnapshot("tenant:source", "message:2")}}, Target: targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}, event: sessioncontrol.HandoffEvent{EventID: 70, TaskID: 55, PayloadJSON: payload}, linkID: 60}, Persistence: persistence})
	result, err := service.Refresh(context.Background(), sessioncontrol.RefreshHandoffRequest{Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000, Source: tenantRef("source"), PreviousPackageID: old.PackageID, PreviousEventID: 70, OperationIdentity: strings.Repeat("a", 64)})
	if err != nil || !result.Replayed || result.SourceResults[0].EventID != 70 || result.SourceResults[0].LinkID != 60 || persistence.batchCalls != 0 {
		t.Fatalf("Refresh() result=%#v error=%v batch_calls=%d", result, err, persistence.batchCalls)
	}
}

func TestHandoffReadAfterCompressionDoesNotReportFalseStale(t *testing.T) {
	snapshot := testSourceSnapshot("tenant:source", "message:2")
	snapshot.Stages = []Candidate{{Locator: "tenant:source#task_event:2", Text: "Long deterministic stage summary", Precedence: PrecedenceStructuredStatus}}
	snapshot.Target = Target{Ref: "tenant:target"}
	pkg, err := Extract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := CompressPackage(context.Background(), pkg, compressorFunc(func(_ context.Context, skeleton FactSkeleton) (CompressedFields, error) {
		fields := skeleton.Fields()
		fields.StageSummary = "Long"
		return fields, nil
	}), 100_000)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeSessionHandoffEvent(compressed.Package, 55)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(Dependencies{Sources: map[sessioncontrol.Source]SourceReader{sessioncontrol.SourceTenant: sourceReaderFake{snapshot: snapshot}}, Target: targetReaderFake{event: sessioncontrol.HandoffEvent{EventID: 70, TaskID: 55, PayloadJSON: payload}}})
	result, err := service.Read(context.Background(), sessioncontrol.HandoffReadRequest{Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, EventID: 70})
	if err != nil || result.Stale {
		t.Fatalf("Read() result=%#v error=%v, want non-stale", result, err)
	}
}

func TestHandoffRefreshRejectsSourceDifferentFromPriorPackage(t *testing.T) {
	old, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "message:2"}, Target: Target{Ref: "tenant:target"}, Objective: "continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeSessionHandoffEvent(old, 55)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(Dependencies{Target: targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}, event: sessioncontrol.HandoffEvent{EventID: 70, TaskID: 55, PayloadJSON: payload}}, Persistence: &persistenceFake{}})
	_, err = service.Refresh(context.Background(), sessioncontrol.RefreshHandoffRequest{Context: testRequestContext(), Target: tenantRef("target"), TargetTaskID: 55, TargetContextWindowTokens: 100_000, Source: tenantRef("other"), PreviousPackageID: old.PackageID, PreviousEventID: 70, OperationIdentity: strings.Repeat("a", 64)})
	assertServiceCode(t, err, sessioncontrol.CodeForbidden)
}

func TestHandoffRefreshSameSourceAppendsWhenTargetTaskChanges(t *testing.T) {
	old, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "message:2"}, Target: Target{Ref: "tenant:old-target"}, Objective: "continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeSessionHandoffEvent(old, 55)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &persistenceFake{results: []PersistResult{{LinkID: 101, EventID: 202}}}
	service := NewService(Dependencies{Sources: map[sessioncontrol.Source]SourceReader{sessioncontrol.SourceTenant: sourceReaderFake{snapshot: testSourceSnapshot("tenant:source", "message:2")}}, Target: targetReaderFake{targets: map[uint64]sessioncontrol.HandoffTarget{56: {SessionID: 45, TaskID: 56, TaskStatus: "ready"}}, events: map[uint64]sessioncontrol.HandoffEvent{70: {EventID: 70, TaskID: 55, PayloadJSON: payload}}}, Persistence: persistence})
	result, err := service.Refresh(context.Background(), sessioncontrol.RefreshHandoffRequest{Context: testRequestContext(), Target: tenantRef("new-target"), TargetTaskID: 56, TargetContextWindowTokens: 100_000, Source: tenantRef("source"), PreviousTarget: tenantRef("old-target"), PreviousTargetTaskID: 55, PreviousPackageID: old.PackageID, PreviousEventID: 70, OperationIdentity: strings.Repeat("a", 64)})
	if err != nil || result.Replayed || result.Stale || result.SourceResults[0].EventID != 202 {
		t.Fatalf("Refresh() result=%#v error=%v", result, err)
	}
}

func TestHandoffReadbackVerifiesReadyTargetLinkEventAndPackageIdentity(t *testing.T) {
	pkg, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "message:2"}, Target: Target{Ref: "tenant:target"}, Objective: "continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeSessionHandoffEvent(pkg, 55)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(Dependencies{Target: targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: "ready"}, event: sessioncontrol.HandoffEvent{EventID: 70, TaskID: 55, PayloadJSON: payload}}})
	expected := sessioncontrol.HandoffResult{TargetTaskID: 55, SourceResults: []sessioncontrol.HandoffSourceResult{{Source: tenantRef("source"), LinkID: 60, EventID: 70, PackageID: pkg.PackageID, PackageSHA256Prefix: shortPrefix(pkg.PackageSHA256)}}}
	verified, err := service.Readback(context.Background(), sessioncontrol.HandoffReadbackRequest{Context: testRequestContext(), Target: tenantRef("target"), Expected: expected})
	if err != nil || !verified.SourceResults[0].Success {
		t.Fatalf("Readback() result=%#v error=%v", verified, err)
	}
	expected.SourceResults[0].PackageSHA256Prefix = "wrong"
	if _, err := service.Readback(context.Background(), sessioncontrol.HandoffReadbackRequest{Context: testRequestContext(), Target: tenantRef("target"), Expected: expected}); err == nil {
		t.Fatal("Readback() accepted mismatched package SHA")
	}
}

func TestHandoffAppliedReadbackAllowsTargetStatusTransition(t *testing.T) {
	pkg, err := BuildPackage(PackageInput{Source: Source{Ref: "tenant:source", Cursor: "message:2"}, Target: Target{Ref: "tenant:target"}, Objective: "continue", Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeSessionHandoffEvent(pkg, 55)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"running", "completed"} {
		service := NewService(Dependencies{Target: targetReaderFake{target: sessioncontrol.HandoffTarget{SessionID: 44, TaskID: 55, TaskStatus: status}, event: sessioncontrol.HandoffEvent{EventID: 70, TaskID: 55, PayloadJSON: payload}}})
		expected := sessioncontrol.HandoffResult{TargetTaskID: 55, SourceResults: []sessioncontrol.HandoffSourceResult{{Source: tenantRef("source"), LinkID: 60, EventID: 70, PackageID: pkg.PackageID, PackageSHA256Prefix: shortPrefix(pkg.PackageSHA256)}}}
		if _, err := service.Readback(context.Background(), sessioncontrol.HandoffReadbackRequest{Context: testRequestContext(), Target: tenantRef("target"), Expected: expected}); err != nil {
			t.Fatalf("Readback(status=%s) error=%v", status, err)
		}
	}
}

func testSourceSnapshot(ref, cursor string) SourceSnapshot {
	return SourceSnapshot{Source: Source{Ref: ref, Cursor: cursor}, Objectives: []Candidate{{Locator: ref + "#objective:1", Text: "continue", Precedence: PrecedenceTaskDescription}}, Budget: Budget{LimitTokens: DefaultPackageTokenLimit}}
}

func testRequestContext() sessioncontrol.RequestContext {
	return sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}
}

func tenantRef(key string) sessioncontrol.SessionRef {
	return sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: key}
}
func localRef(key string) sessioncontrol.SessionRef {
	return sessioncontrol.SessionRef{Source: sessioncontrol.SourceLocal, Key: key}
}

type sourceReaderFake struct {
	calls    *[]string
	snapshot SourceSnapshot
	err      error
}

func (f sourceReaderFake) Capture(_ context.Context, _ sessioncontrol.RequestContext, ref sessioncontrol.SessionRef) (SourceSnapshot, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, "capture:"+ref.String())
	}
	if f.err != nil {
		return SourceSnapshot{}, f.err
	}
	return f.snapshot, nil
}

type targetReaderFake struct {
	target  sessioncontrol.HandoffTarget
	event   sessioncontrol.HandoffEvent
	targets map[uint64]sessioncontrol.HandoffTarget
	events  map[uint64]sessioncontrol.HandoffEvent
	linkID  uint64
	err     error
}

func (f targetReaderFake) ReadHandoffTarget(_ context.Context, _ sessioncontrol.RequestContext, _ sessioncontrol.SessionRef, taskID uint64) (sessioncontrol.HandoffTarget, error) {
	if target, ok := f.targets[taskID]; ok {
		return target, f.err
	}
	return f.target, f.err
}

func (f targetReaderFake) ReadHandoffEvent(_ context.Context, _ sessioncontrol.RequestContext, _ sessioncontrol.SessionRef, _ uint64, eventID uint64) (sessioncontrol.HandoffEvent, error) {
	if event, ok := f.events[eventID]; ok {
		return event, f.err
	}
	return f.event, f.err
}

func (f targetReaderFake) ReadHandoffLink(_ context.Context, _ sessioncontrol.RequestContext, target, source sessioncontrol.SessionRef, linkID uint64) (sessioncontrol.SessionLink, error) {
	if linkID == 0 {
		linkID = f.linkID
	}
	return sessioncontrol.SessionLink{ID: linkID, Target: target, Source: source, RelationType: SessionHandoffRelation}, f.err
}

type persistenceFake struct {
	calls        *[]string
	results      []PersistResult
	requests     []PersistRequest
	batchCalls   int
	batchErr     error
	recoverCalls int
	recovery     RecoverBatchResult
	recoverErr   error
}

type observerFunc func(context.Context, Observation)

func (f observerFunc) Observe(ctx context.Context, observation Observation) { f(ctx, observation) }

func (f *persistenceFake) Persist(_ context.Context, request PersistRequest) (PersistResult, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, "persist:"+request.Package.Source.Ref)
	}
	f.requests = append(f.requests, request)
	result := PersistResult{}
	if len(f.results) > 0 {
		result = f.results[0]
		f.results = f.results[1:]
	}
	return result, nil
}

func (f *persistenceFake) PersistBatch(_ context.Context, request PersistBatchRequest) (PersistBatchResult, error) {
	f.batchCalls++
	if f.batchErr != nil {
		return PersistBatchResult{}, f.batchErr
	}
	items := make([]PersistResult, 0, len(request.Items))
	for _, item := range request.Items {
		stored, err := f.Persist(context.Background(), item)
		if err != nil {
			return PersistBatchResult{}, err
		}
		items = append(items, stored)
	}
	return PersistBatchResult{Items: items}, nil
}

func (f *persistenceFake) RecoverBatch(context.Context, RecoverBatchRequest) (RecoverBatchResult, error) {
	f.recoverCalls++
	return f.recovery, f.recoverErr
}

func assertServiceCode(t *testing.T, err error, want sessioncontrol.ServiceErrorCode) {
	t.Helper()
	var typed *sessioncontrol.ServiceError
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
