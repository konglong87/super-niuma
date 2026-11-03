package sessioncontrol

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/scheduler"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestSessionMonitorPreflightRejectsUnboundFeishuBeforeWrites(t *testing.T) {
	store := &sessionMonitorStoreFake{resolveErr: mysqlstore.ErrNotFound}
	root := t.TempDir()
	monitor := NewSessionMonitor(SessionMonitorDependencies{
		Store: store, Schedules: &scheduler.Store{Root: root},
	})
	request := monitorRequest("monitor-1", 300, "feishu")
	_, err := monitor.Monitor(context.Background(), request)
	assertServiceErrorCode(t, err, CodeSchedulerUnavailable)
	if store.linkWrites != 0 {
		t.Fatalf("link writes = %d, want 0", store.linkWrites)
	}
	items, listErr := (&scheduler.Store{Root: root}).List()
	if listErr != nil || len(items) != 0 {
		t.Fatalf("schedules=%#v err=%v", items, listErr)
	}
}

func TestSessionMonitorPreflightRejectsLocalSourceWithoutExplicitReadCapability(t *testing.T) {
	store := &sessionMonitorStoreFake{target: mysqlstore.SessionMonitorTarget{SessionID: 41, SessionKey: "target", AccountID: 9, AccountKey: "primary", ConversationID: 17, ExternalChatID: "chat-1"}}
	root := t.TempDir()
	monitor := NewSessionMonitor(SessionMonitorDependencies{Store: store, Schedules: &scheduler.Store{Root: root}})
	request := monitorRequest("monitor-1", 300, "feishu")
	request.Sources = []SessionRef{localRef("source")}
	request.ReplayIdentity = managedOperationIdentityForRequest(OperationMonitor, request.Context, request.IdempotencyKey, request)
	_, err := monitor.Monitor(context.Background(), request)
	assertServiceErrorCode(t, err, CodeForbidden)
	if store.linkWrites != 0 {
		t.Fatalf("link writes = %d, want 0", store.linkWrites)
	}
}

func TestSessionMonitorCreatesReplaysRepairsAndUpdatesOneSchedule(t *testing.T) {
	root := t.TempDir()
	store := &sessionMonitorStoreFake{target: mysqlstore.SessionMonitorTarget{
		SessionID: 41, SessionKey: "target", AccountID: 9, AccountKey: "primary",
		ConversationID: 17, ExternalChatID: "chat-1",
	}}
	schedules := &scheduler.Store{Root: root}
	monitor := NewSessionMonitor(SessionMonitorDependencies{
		Store: store, Schedules: schedules,
	})
	request := monitorRequest("monitor-1", 300, "feishu")
	result, err := monitor.Monitor(context.Background(), request)
	if err != nil || result.ScheduleID == "" || len(result.LinkIDs) != 1 {
		t.Fatalf("create result=%#v err=%v", result, err)
	}
	firstScheduleID := result.ScheduleID
	if got, err := schedules.List(); err != nil || len(got) != 1 {
		t.Fatalf("schedules=%#v err=%v", got, err)
	}

	identity := request.ReplayIdentity
	recovered, err := monitor.RecoverMonitor(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Result.ScheduleID != firstScheduleID {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
	conflictRequest := monitorRequest(request.IdempotencyKey, 600, "feishu")
	_, err = monitor.RecoverMonitor(context.Background(), conflictRequest, conflictRequest.ReplayIdentity)
	assertServiceErrorCode(t, err, CodeIdempotencyConflict)

	// A committed MySQL anchor without a schedule ID is a repairable projection,
	// not a second logical monitor.
	metadata, err := decodeSessionMonitorMetadata(store.link.MetadataJSON)
	if err != nil {
		t.Fatal(err)
	}
	metadata.ScheduleID = ""
	store.link.MetadataJSON, err = encodeSessionMonitorMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := monitor.Monitor(context.Background(), request)
	if err != nil || repaired.ScheduleID == "" {
		t.Fatalf("repair result=%#v err=%v", repaired, err)
	}
	if got, err := schedules.List(); err != nil || len(got) != 1 {
		t.Fatalf("repair schedules=%#v err=%v", got, err)
	}

	changed := monitorRequest("monitor-2", 600, "feishu")
	updated, err := monitor.Monitor(context.Background(), changed)
	if err != nil || updated.ScheduleID != repaired.ScheduleID {
		t.Fatalf("update result=%#v err=%v", updated, err)
	}
	items, err := schedules.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("updated schedules=%#v err=%v", items, err)
	}
	current, ok, err := schedules.Find(repaired.ScheduleID)
	if err != nil || !ok || current.IntervalSeconds != 600 || current.Spec != "@every 10m" {
		t.Fatalf("current=%#v ok=%v err=%v", current, ok, err)
	}
	if got := current.AllowedTools; len(got) != 2 || got[0] != SessionMonitorToolSessionGet || got[1] != SessionMonitorToolChannelReport {
		t.Fatalf("allowed tools = %#v", got)
	}
}

func TestSessionMonitorConcurrentUpdatesConvergeProjectionToAuthoritativeLink(t *testing.T) {
	root := t.TempDir()
	store := &sessionMonitorConvergenceStoreFake{
		target: mysqlstore.SessionMonitorTarget{
			SessionID: 41, SessionKey: "target", AccountID: 9, AccountKey: "primary",
			ConversationID: 17, ExternalChatID: "chat-1",
		},
	}
	monitor := NewSessionMonitor(SessionMonitorDependencies{Store: store, Schedules: &scheduler.Store{Root: root}})
	if _, err := monitor.Monitor(context.Background(), monitorRequest("monitor-seed", 120, "feishu")); err != nil {
		t.Fatal(err)
	}

	store.blockFrequency = 300
	store.blocked = make(chan struct{})
	store.release = make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := monitor.Monitor(context.Background(), monitorRequest("monitor-a", 300, "feishu"))
		firstDone <- err
	}()
	<-store.blocked
	if _, err := monitor.Monitor(context.Background(), monitorRequest("monitor-b", 600, "feishu")); err != nil {
		t.Fatal(err)
	}
	close(store.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	link := store.currentLink()
	metadata, err := decodeSessionMonitorMetadata(link.MetadataJSON)
	if err != nil {
		t.Fatal(err)
	}
	projection, ok, err := (&scheduler.Store{Root: root}).Find(metadata.ScheduleID)
	if err != nil || !ok {
		t.Fatalf("projection=%#v ok=%v err=%v", projection, ok, err)
	}
	if projection.IntervalSeconds != metadata.FrequencySeconds {
		t.Fatalf("projection interval=%d, authoritative interval=%d", projection.IntervalSeconds, metadata.FrequencySeconds)
	}
}

func TestSessionMonitorConcurrentSameKeyDifferentConfigReturnsIdempotencyConflict(t *testing.T) {
	store := newSessionMonitorTOCTOUStoreFake()
	monitor := NewSessionMonitor(SessionMonitorDependencies{Store: store, Schedules: &scheduler.Store{Root: t.TempDir()}})
	requests := []MonitorRequest{
		monitorRequest("same-key", 300, "feishu"),
		monitorRequest("same-key", 600, "feishu"),
	}
	errs := make(chan error, len(requests))
	for _, request := range requests {
		request := request
		go func() {
			_, err := monitor.Monitor(context.Background(), request)
			errs <- err
		}()
	}
	var successes, conflicts int
	for range requests {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var serviceErr *ServiceError
		if errors.As(err, &serviceErr) && serviceErr.Code == CodeIdempotencyConflict {
			conflicts++
			continue
		}
		t.Fatalf("Monitor() error = %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
}

func TestSessionMonitorConfigUpdatePreservesConcurrentObservation(t *testing.T) {
	seed := monitorRequest("old-key", 300, "feishu")
	metadata := monitorMetadata(seed, seed.ReplayIdentity)
	metadata.ScheduleID = "session-monitor-71"
	metadata.LastObservedCursor = "old-cursor"
	metadata.LastObservedStatus = "tenant:source=idle"
	raw, err := encodeSessionMonitorMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	store := &sessionMonitorObservationRaceStoreFake{
		target:            mysqlstore.SessionMonitorTarget{SessionID: 41, SessionKey: "target", AccountID: 9, AccountKey: "primary", ConversationID: 17, ExternalChatID: "chat-1"},
		link:              mysqlstore.SessionLink{ID: 71, TenantID: 7, UserID: 11, TargetSessionID: 41, SourceKind: "tenant", SourceSessionKey: "source", RelationType: sessionMonitorRelationObserved, Status: "active", MetadataJSON: raw},
		controllerReady:   make(chan struct{}),
		releaseController: make(chan struct{}),
	}
	monitor := NewSessionMonitor(SessionMonitorDependencies{Store: store, Schedules: &scheduler.Store{Root: t.TempDir()}})
	done := make(chan error, 1)
	go func() {
		_, err := monitor.Monitor(context.Background(), monitorRequest("new-key", 600, "feishu"))
		done <- err
	}()
	<-store.controllerReady
	if err := store.commitObservation("new-cursor", "tenant:source=running"); err != nil {
		t.Fatal(err)
	}
	close(store.releaseController)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	latest, err := store.currentMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if latest.LastObservedCursor != "new-cursor" || latest.LastObservedStatus != "tenant:source=running" {
		t.Fatalf("observation=%q/%q, want new child state", latest.LastObservedCursor, latest.LastObservedStatus)
	}
}

func TestSessionMonitorProjectionCannotOverwriteNewerAuthorityAfterRead(t *testing.T) {
	root := t.TempDir()
	store := &sessionMonitorConvergenceStoreFake{
		target: mysqlstore.SessionMonitorTarget{
			SessionID: 41, SessionKey: "target", AccountID: 9, AccountKey: "primary",
			ConversationID: 17, ExternalChatID: "chat-1",
		},
	}
	monitor := NewSessionMonitor(SessionMonitorDependencies{Store: store, Schedules: &scheduler.Store{Root: root}})
	if _, err := monitor.Monitor(context.Background(), monitorRequest("monitor-seed", 120, "feishu")); err != nil {
		t.Fatal(err)
	}

	store.blockReadLabel = "a"
	store.blockReadFrequency = 300
	store.readBlocked = make(chan struct{})
	store.readRelease = make(chan struct{})
	store.signalWriteFrequency = 600
	store.writeSignaled = make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		ctx := context.WithValue(context.Background(), sessionMonitorTestContextKey{}, "a")
		_, err := monitor.Monitor(ctx, monitorRequest("monitor-a", 300, "feishu"))
		firstDone <- err
	}()
	<-store.readBlocked
	secondDone := make(chan error, 1)
	go func() {
		ctx := context.WithValue(context.Background(), sessionMonitorTestContextKey{}, "b")
		_, err := monitor.Monitor(ctx, monitorRequest("monitor-b", 600, "feishu"))
		secondDone <- err
	}()
	<-store.writeSignaled
	close(store.readRelease)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}

	link := store.currentLink()
	metadata, err := decodeSessionMonitorMetadata(link.MetadataJSON)
	if err != nil {
		t.Fatal(err)
	}
	projection, ok, err := (&scheduler.Store{Root: root}).Find(metadata.ScheduleID)
	if err != nil || !ok {
		t.Fatalf("projection=%#v ok=%v err=%v", projection, ok, err)
	}
	if projection.IntervalSeconds != metadata.FrequencySeconds {
		t.Fatalf("projection interval=%d, authoritative interval=%d", projection.IntervalSeconds, metadata.FrequencySeconds)
	}
}

func TestSessionMonitorDaemonFailureLeavesNoEnabledWork(t *testing.T) {
	root := t.TempDir()
	store := &sessionMonitorStoreFake{target: mysqlstore.SessionMonitorTarget{
		SessionID: 41, SessionKey: "target", AccountID: 9, AccountKey: "primary",
		ConversationID: 17, ExternalChatID: "chat-1",
	}}
	monitor := NewSessionMonitor(SessionMonitorDependencies{
		Store: store, Schedules: &scheduler.Store{Root: root},
		EnsureDaemon: func() error { return errors.New("daemon unavailable") },
	})
	_, err := monitor.Monitor(context.Background(), monitorRequest("monitor-1", 300, "feishu"))
	assertServiceErrorCode(t, err, CodeSchedulerUnavailable)
	if store.linkWrites != 0 {
		t.Fatalf("link writes=%d, want 0", store.linkWrites)
	}
	items, listErr := (&scheduler.Store{Root: root}).List()
	if listErr != nil || len(items) != 0 {
		t.Fatalf("schedules=%#v err=%v", items, listErr)
	}
}

func TestSessionMonitorChildUsesTrustedRecordAndOnlyReportsChangesOrWindow(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	metadata := SessionMonitorMetadata{
		Version: 1, ControllerTargetRef: "tenant:target", ObservedSourceRefs: []string{"tenant:source"},
		FrequencySeconds: 300, Channel: "feishu", ConfigurationFingerprint: strings.Repeat("a", 64) + "." + strings.Repeat("b", 64), ScheduleID: "session-monitor-71",
	}
	raw, err := encodeSessionMonitorMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	store := &sessionMonitorChildStoreFake{record: mysqlstore.SessionMonitorRecord{
		Link:      mysqlstore.SessionLink{ID: 71, TenantID: 7, UserID: 11, TargetSessionID: 41, MetadataJSON: raw, UpdatedAt: now.Add(-time.Minute)},
		TenantKey: "tenant-a", UserKey: "user-a", AccountID: 9, AccountKey: "primary",
		ConversationID: 17, ExternalChatID: "chat-1",
	}}
	getter := &sessionMonitorGetterFake{snapshots: map[string]SessionSnapshot{
		"tenant:target": {Ref: tenantRef("target"), Status: StatusRunning, UpdatedAt: now.Add(-time.Second)},
		"tenant:source": {Ref: tenantRef("source"), Status: StatusWaitingPermission, UpdatedAt: now.Add(-2 * time.Second)},
	}}
	child := SessionMonitorChild{Store: store, Sessions: getter, Schedules: &scheduler.Store{Root: t.TempDir()}, Now: func() time.Time { return now }}
	if err := child.Run(context.Background(), "session-monitor-71"); err != nil {
		t.Fatal(err)
	}
	if got := getter.refs; len(got) != 2 || got[0] != "tenant:target" || got[1] != "tenant:source" {
		t.Fatalf("SessionGet refs = %#v", got)
	}
	if store.commits != 1 || store.lastCommit.TenantID != 7 || store.lastCommit.UserID != 11 || !store.lastCommit.Deliver {
		t.Fatalf("commit = %#v count=%d", store.lastCommit, store.commits)
	}
	if store.lastCommit.PayloadJSON == "" || store.lastCommit.AccountID != 9 || store.lastCommit.ConversationID != 17 {
		t.Fatalf("delivery routing = %#v", store.lastCommit)
	}

	store.record.Link.MetadataJSON = store.lastCommit.MetadataJSON
	store.record.Link.UpdatedAt = now
	if err := child.Run(context.Background(), "session-monitor-71"); err != nil {
		t.Fatal(err)
	}
	if store.commits != 1 {
		t.Fatalf("unchanged state commits = %d, want 1", store.commits)
	}
	store.record.Link.UpdatedAt = now.Add(-SessionMonitorReportWindow)
	if err := child.Run(context.Background(), "session-monitor-71"); err != nil {
		t.Fatal(err)
	}
	if store.commits != 2 || !store.lastCommit.Deliver {
		t.Fatalf("window commits = %d commit=%#v", store.commits, store.lastCommit)
	}
}

func TestSessionMonitorNilDependenciesFailClosed(t *testing.T) {
	monitor := NewSessionMonitor(SessionMonitorDependencies{})
	_, err := monitor.Monitor(context.Background(), monitorRequest("monitor-1", 300, "feishu"))
	assertServiceErrorCode(t, err, CodeSchedulerUnavailable)
	child := SessionMonitorChild{}
	if err := child.Run(context.Background(), "sched-1"); err == nil {
		t.Fatal("child error = nil")
	}
}

func monitorRequest(key string, interval int, channel string) MonitorRequest {
	request := MonitorRequest{Context: managedRequestContext(), Target: tenantRef("target"), Sources: []SessionRef{tenantRef("source")}, IntervalSeconds: interval, Channel: channel, IdempotencyKey: key}
	request.ReplayIdentity = managedOperationIdentityForRequest(OperationMonitor, request.Context, key, request)
	return request
}

func managedOperationIdentityForRequest(operation Operation, requestContext RequestContext, key string, request MonitorRequest) OperationIdentity {
	sources := append([]SessionRef(nil), request.Sources...)
	fingerprint := operationFingerprint(operation, requestContext, struct {
		Target          SessionRef   `json:"target"`
		Sources         []SessionRef `json:"sources"`
		IntervalSeconds int          `json:"interval_seconds"`
		Channel         string       `json:"channel"`
	}{request.Target, sources, request.IntervalSeconds, request.Channel})
	identity, _ := newOperationIdentity(operation, requestContext, key, fingerprint)
	return identity
}

type sessionMonitorStoreFake struct {
	target     mysqlstore.SessionMonitorTarget
	resolveErr error
	link       mysqlstore.SessionLink
	linkWrites int
}

type sessionMonitorConvergenceStoreFake struct {
	mu                   sync.Mutex
	target               mysqlstore.SessionMonitorTarget
	link                 mysqlstore.SessionLink
	blockFrequency       int
	blocked              chan struct{}
	release              chan struct{}
	blockOnce            sync.Once
	blockReadLabel       string
	blockReadFrequency   int
	readBlocked          chan struct{}
	readRelease          chan struct{}
	readBlockOnce        sync.Once
	signalWriteFrequency int
	writeSignaled        chan struct{}
	writeSignalOnce      sync.Once
}

type sessionMonitorTestContextKey struct{}

type sessionMonitorTOCTOUStoreFake struct {
	target mysqlstore.SessionMonitorTarget
	mu     sync.Mutex
	link   mysqlstore.SessionLink
}

func newSessionMonitorTOCTOUStoreFake() *sessionMonitorTOCTOUStoreFake {
	return &sessionMonitorTOCTOUStoreFake{
		target: mysqlstore.SessionMonitorTarget{
			SessionID: 41, SessionKey: "target", AccountID: 9, AccountKey: "primary",
			ConversationID: 17, ExternalChatID: "chat-1",
		},
	}
}

func (f *sessionMonitorTOCTOUStoreFake) ResolveSessionMonitorTarget(context.Context, string, string) (mysqlstore.SessionMonitorTarget, error) {
	return f.target, nil
}

func (*sessionMonitorTOCTOUStoreFake) GetSessionControlSessionByKey(context.Context, string) (mysqlstore.SessionControlSession, error) {
	return mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 42, SessionKey: "source"}}, nil
}

func (f *sessionMonitorTOCTOUStoreFake) GetSessionMonitorLink(context.Context, uint64, string, string) (mysqlstore.SessionLink, error) {
	f.mu.Lock()
	link := f.link
	f.mu.Unlock()
	if link.ID == 0 {
		return mysqlstore.SessionLink{}, mysqlstore.ErrNotFound
	}
	return link, nil
}

func (f *sessionMonitorTOCTOUStoreFake) UpsertSessionMonitorLink(_ context.Context, input mysqlstore.SessionMonitorLinkInput) (mysqlstore.SessionLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.link.ID == 0 {
		f.link = mysqlstore.SessionLink{ID: 71, TenantID: input.TenantID, UserID: input.UserID, TargetSessionID: input.TargetSessionID, SourceKind: input.SourceKind, SourceSessionKey: input.SourceSessionKey, RelationType: input.RelationType, Status: "active", CreatedByUserID: input.CreatedByUserID}
	}
	f.link.MetadataJSON = input.MetadataJSON
	return f.link, nil
}

func (f *sessionMonitorTOCTOUStoreFake) ApplySessionMonitorConfiguration(_ context.Context, input mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result, err := applySessionMonitorConfigurationForTest(f.link, input)
	if err == nil && !result.Conflict {
		f.link = result.Link
	}
	return result, err
}

func (*sessionMonitorTOCTOUStoreFake) GetSessionControlAuditByKeyHash(context.Context, string, string) (mysqlstore.AuditLog, error) {
	return mysqlstore.AuditLog{}, mysqlstore.ErrNotFound
}

type sessionMonitorObservationRaceStoreFake struct {
	target            mysqlstore.SessionMonitorTarget
	mu                sync.Mutex
	link              mysqlstore.SessionLink
	controllerReady   chan struct{}
	releaseController chan struct{}
	readyOnce         sync.Once
	writeOnce         sync.Once
}

func (f *sessionMonitorObservationRaceStoreFake) ResolveSessionMonitorTarget(context.Context, string, string) (mysqlstore.SessionMonitorTarget, error) {
	return f.target, nil
}

func (*sessionMonitorObservationRaceStoreFake) GetSessionControlSessionByKey(context.Context, string) (mysqlstore.SessionControlSession, error) {
	return mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 42, SessionKey: "source"}}, nil
}

func (f *sessionMonitorObservationRaceStoreFake) GetSessionMonitorLink(context.Context, uint64, string, string) (mysqlstore.SessionLink, error) {
	f.mu.Lock()
	link := f.link
	f.mu.Unlock()
	f.readyOnce.Do(func() { close(f.controllerReady) })
	return link, nil
}

func (f *sessionMonitorObservationRaceStoreFake) UpsertSessionMonitorLink(_ context.Context, input mysqlstore.SessionMonitorLinkInput) (mysqlstore.SessionLink, error) {
	f.writeOnce.Do(func() { <-f.releaseController })
	f.mu.Lock()
	defer f.mu.Unlock()
	f.link.MetadataJSON = input.MetadataJSON
	return f.link, nil
}

func (f *sessionMonitorObservationRaceStoreFake) ApplySessionMonitorConfiguration(_ context.Context, input mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error) {
	f.readyOnce.Do(func() { close(f.controllerReady) })
	f.writeOnce.Do(func() { <-f.releaseController })
	f.mu.Lock()
	defer f.mu.Unlock()
	result, err := applySessionMonitorConfigurationForTest(f.link, input)
	if err == nil && !result.Conflict {
		f.link = result.Link
	}
	return result, err
}

func (*sessionMonitorObservationRaceStoreFake) GetSessionControlAuditByKeyHash(context.Context, string, string) (mysqlstore.AuditLog, error) {
	return mysqlstore.AuditLog{}, mysqlstore.ErrNotFound
}

func (f *sessionMonitorObservationRaceStoreFake) commitObservation(cursor, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	metadata, err := decodeSessionMonitorMetadata(f.link.MetadataJSON)
	if err != nil {
		return err
	}
	metadata.LastObservedCursor = cursor
	metadata.LastObservedStatus = status
	f.link.MetadataJSON, err = encodeSessionMonitorMetadata(metadata)
	return err
}

func (f *sessionMonitorObservationRaceStoreFake) currentMetadata() (SessionMonitorMetadata, error) {
	f.mu.Lock()
	raw := f.link.MetadataJSON
	f.mu.Unlock()
	return decodeSessionMonitorMetadata(raw)
}

func (f *sessionMonitorConvergenceStoreFake) ResolveSessionMonitorTarget(context.Context, string, string) (mysqlstore.SessionMonitorTarget, error) {
	return f.target, nil
}

func (*sessionMonitorConvergenceStoreFake) GetSessionControlSessionByKey(context.Context, string) (mysqlstore.SessionControlSession, error) {
	return mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 42, SessionKey: "source"}}, nil
}

func (f *sessionMonitorConvergenceStoreFake) GetSessionMonitorLink(ctx context.Context, _ uint64, _, _ string) (mysqlstore.SessionLink, error) {
	f.mu.Lock()
	if f.link.ID == 0 {
		f.mu.Unlock()
		return mysqlstore.SessionLink{}, mysqlstore.ErrNotFound
	}
	link := f.link
	f.mu.Unlock()
	metadata, _ := decodeSessionMonitorMetadata(link.MetadataJSON)
	if label, _ := ctx.Value(sessionMonitorTestContextKey{}).(string); label == f.blockReadLabel && metadata.FrequencySeconds == f.blockReadFrequency && f.readBlocked != nil && f.readRelease != nil {
		f.readBlockOnce.Do(func() {
			close(f.readBlocked)
			<-f.readRelease
		})
	}
	return link, nil
}

func (f *sessionMonitorConvergenceStoreFake) UpsertSessionMonitorLink(_ context.Context, input mysqlstore.SessionMonitorLinkInput) (mysqlstore.SessionLink, error) {
	metadata, err := decodeSessionMonitorMetadata(input.MetadataJSON)
	if err != nil {
		return mysqlstore.SessionLink{}, err
	}
	if metadata.FrequencySeconds == f.blockFrequency && f.blocked != nil && f.release != nil {
		f.blockOnce.Do(func() {
			close(f.blocked)
			<-f.release
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.link.ID == 0 {
		f.link.ID = 71
		f.link.TenantID = input.TenantID
		f.link.UserID = input.UserID
		f.link.TargetSessionID = input.TargetSessionID
		f.link.SourceKind = input.SourceKind
		f.link.SourceSessionKey = input.SourceSessionKey
		f.link.RelationType = input.RelationType
	}
	f.link.MetadataJSON = input.MetadataJSON
	if metadata.FrequencySeconds == f.signalWriteFrequency && f.writeSignaled != nil {
		f.writeSignalOnce.Do(func() { close(f.writeSignaled) })
	}
	return f.link, nil
}

func (f *sessionMonitorConvergenceStoreFake) ApplySessionMonitorConfiguration(_ context.Context, input mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error) {
	metadata, err := decodeSessionMonitorMetadata(input.MetadataJSON)
	if err != nil {
		return mysqlstore.SessionMonitorConfigurationResult{}, err
	}
	if metadata.FrequencySeconds == f.blockFrequency && f.blocked != nil && f.release != nil {
		f.blockOnce.Do(func() {
			close(f.blocked)
			<-f.release
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	result, err := applySessionMonitorConfigurationForTest(f.link, input)
	if err == nil && !result.Conflict {
		f.link = result.Link
	}
	if metadata.FrequencySeconds == f.signalWriteFrequency && f.writeSignaled != nil {
		f.writeSignalOnce.Do(func() { close(f.writeSignaled) })
	}
	return result, err
}

func (*sessionMonitorConvergenceStoreFake) GetSessionControlAuditByKeyHash(context.Context, string, string) (mysqlstore.AuditLog, error) {
	return mysqlstore.AuditLog{}, mysqlstore.ErrNotFound
}

func (f *sessionMonitorConvergenceStoreFake) currentLink() mysqlstore.SessionLink {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.link
}

func (f *sessionMonitorStoreFake) ResolveSessionMonitorTarget(context.Context, string, string) (mysqlstore.SessionMonitorTarget, error) {
	return f.target, f.resolveErr
}
func (f *sessionMonitorStoreFake) GetSessionControlSessionByKey(context.Context, string) (mysqlstore.SessionControlSession, error) {
	return mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 42, SessionKey: "source"}}, nil
}
func (f *sessionMonitorStoreFake) GetSessionMonitorLink(context.Context, uint64, string, string) (mysqlstore.SessionLink, error) {
	if f.link.ID == 0 {
		return mysqlstore.SessionLink{}, mysqlstore.ErrNotFound
	}
	return f.link, nil
}
func (f *sessionMonitorStoreFake) UpsertSessionMonitorLink(_ context.Context, input mysqlstore.SessionMonitorLinkInput) (mysqlstore.SessionLink, error) {
	f.linkWrites++
	if f.link.ID == 0 {
		f.link.ID = 71
		f.link.TenantID = input.TenantID
		f.link.UserID = input.UserID
		f.link.TargetSessionID = input.TargetSessionID
		f.link.SourceKind = input.SourceKind
		f.link.SourceSessionKey = input.SourceSessionKey
		f.link.RelationType = input.RelationType
	}
	f.link.MetadataJSON = input.MetadataJSON
	return f.link, nil
}

func (f *sessionMonitorStoreFake) ApplySessionMonitorConfiguration(_ context.Context, input mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error) {
	f.linkWrites++
	result, err := applySessionMonitorConfigurationForTest(f.link, input)
	if err == nil && !result.Conflict {
		f.link = result.Link
	}
	return result, err
}

func applySessionMonitorConfigurationForTest(link mysqlstore.SessionLink, input mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error) {
	requested, err := decodeSessionMonitorMetadata(input.MetadataJSON)
	if err != nil {
		return mysqlstore.SessionMonitorConfigurationResult{}, err
	}
	if link.ID == 0 {
		link = mysqlstore.SessionLink{ID: 71, TenantID: input.TenantID, UserID: input.UserID, TargetSessionID: input.TargetSessionID, SourceKind: input.SourceKind, SourceSessionKey: input.SourceSessionKey, RelationType: input.RelationType, Status: "active", CreatedByUserID: input.CreatedByUserID}
		requested.ScheduleID = sessionMonitorScheduleID(link.ID)
		link.MetadataJSON, err = encodeSessionMonitorMetadata(requested)
		return mysqlstore.SessionMonitorConfigurationResult{Link: link}, err
	}
	stored, err := decodeSessionMonitorMetadata(link.MetadataJSON)
	if err != nil {
		return mysqlstore.SessionMonitorConfigurationResult{}, err
	}
	storedKeyHash, storedFingerprint, ok := strings.Cut(stored.ConfigurationFingerprint, ".")
	if !ok {
		return mysqlstore.SessionMonitorConfigurationResult{}, errors.New("invalid stored monitor fingerprint")
	}
	if storedKeyHash == input.ConfigurationKeyHash {
		if storedFingerprint != input.ConfigurationFingerprint {
			return mysqlstore.SessionMonitorConfigurationResult{Link: link, Conflict: true}, nil
		}
		if stored.ScheduleID == "" {
			stored.ScheduleID = sessionMonitorScheduleID(link.ID)
			link.MetadataJSON, err = encodeSessionMonitorMetadata(stored)
		}
		return mysqlstore.SessionMonitorConfigurationResult{Link: link, Replayed: true}, err
	}
	requested.ScheduleID = stored.ScheduleID
	if requested.ScheduleID == "" {
		requested.ScheduleID = sessionMonitorScheduleID(link.ID)
	}
	requested.LastObservedCursor = stored.LastObservedCursor
	requested.LastObservedStatus = stored.LastObservedStatus
	link.Status = "active"
	link.MetadataJSON, err = encodeSessionMonitorMetadata(requested)
	return mysqlstore.SessionMonitorConfigurationResult{Link: link}, err
}
func (*sessionMonitorStoreFake) GetSessionControlAuditByKeyHash(context.Context, string, string) (mysqlstore.AuditLog, error) {
	return mysqlstore.AuditLog{}, mysqlstore.ErrNotFound
}

type sessionMonitorGetterFake struct {
	snapshots map[string]SessionSnapshot
	refs      []string
}

func (f *sessionMonitorGetterFake) Get(_ context.Context, request GetRequest) (SessionSnapshot, error) {
	f.refs = append(f.refs, request.Ref.String())
	snapshot, ok := f.snapshots[request.Ref.String()]
	if !ok {
		return SessionSnapshot{}, errors.New("missing snapshot")
	}
	return snapshot, nil
}

type sessionMonitorChildStoreFake struct {
	record     mysqlstore.SessionMonitorRecord
	commits    int
	lastCommit mysqlstore.SessionMonitorObservationInput
}

func (f *sessionMonitorChildStoreFake) ResolveSessionMonitorByScheduleID(context.Context, string) (mysqlstore.SessionMonitorRecord, error) {
	return f.record, nil
}
func (f *sessionMonitorChildStoreFake) CommitSessionMonitorObservation(_ context.Context, input mysqlstore.SessionMonitorObservationInput) (mysqlstore.SessionMonitorObservationResult, error) {
	f.commits++
	f.lastCommit = input
	return mysqlstore.SessionMonitorObservationResult{Committed: true, MessageID: 81, OutboxID: 91}, nil
}
