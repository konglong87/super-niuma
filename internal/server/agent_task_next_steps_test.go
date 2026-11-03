package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/nextsteps"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func dispatcherTestOptions(svc TenantService, fn NextStepsFunc) Options {
	if svc == nil {
		svc = &fakeTenantService{}
	}
	return Options{
		TenantService:       svc,
		NextStepsWebEnabled: true,
		NextStepsFunc:       fn,
	}
}

func TestAgentTaskNextStepsDispatcherRunsAcceptedJobsAsynchronously(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(nil, func(ctx context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
		close(started)
		<-release
		return nil, nil
	}), 1, 1)
	defer d.stop(context.Background())
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("enqueue rejected an available job")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("accepted job did not start asynchronously")
	}
	close(release)
}

func TestAgentTaskNextStepsDispatcherPreservesRequestTenantContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	called := make(chan struct{})
	telemetryDone := make(chan struct{})
	var telemetryEvents []telemetry.Event
	emitter := telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		telemetryEvents = append(telemetryEvents, event)
		if event.Name == "agent.next_steps.finished" {
			close(telemetryDone)
		}
		return nil
	}))
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			return []string{"next"}, nil
		},
	}, 1, 1)
	defer d.stop(context.Background())
	if !d.enqueue(agentTaskNextStepsJob{
		task: mysqlstore.AgentTask{ID: 19}, source: "webui-agent", response: "reply",
		traceID: "trace-original", userID: "user-original", tenantKey: "tenant-original",
		emitter: emitter,
		cfg:     nextsteps.Config{Enabled: true, Model: "claude-haiku-4-5", Count: 1},
	}) {
		t.Fatal("enqueue rejected")
	}
	waitForTestCondition(t, func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for _, event := range fake.agentTaskEvents {
			if event.EventType == agenttasks.EventNextSteps {
				close(called)
				return true
			}
		}
		return false
	})
	<-called
	select {
	case <-telemetryDone:
	case <-time.After(time.Second):
		t.Fatal("async telemetry did not reach injected emitter")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.lastContextUser != "user-original" {
		t.Fatalf("async append user=%q, want user-original", fake.lastContextUser)
	}
	if fake.lastTenant != "tenant-original" {
		t.Fatalf("async append tenant=%q, want tenant-original", fake.lastTenant)
	}
	if fake.lastTrace != "trace-original" {
		t.Fatalf("async append trace=%q, want trace-original", fake.lastTrace)
	}
	foundFinished := false
	for _, event := range telemetryEvents {
		if event.Name == "agent.next_steps.finished" {
			foundFinished = true
			break
		}
	}
	if !foundFinished {
		t.Fatalf("async telemetry did not reach injected emitter: %+v", telemetryEvents)
	}
}

func TestAgentTaskNextStepsDispatcherStopWaitsForReservedEnqueue(t *testing.T) {
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(nil, func(context.Context, string, string, []string, string, string, int) ([]string, error) {
		return nil, nil
	}), 1, 1)
	if !d.reserve() {
		t.Fatal("reservation rejected")
	}
	stopDone := make(chan struct{})
	go func() {
		d.stop(context.Background())
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("dispatcher stopped before reserved job was committed")
	case <-time.After(20 * time.Millisecond):
	}
	if !d.enqueueReserved(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("reserved enqueue rejected during shutdown handoff")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("dispatcher stop did not complete after reservation commit")
	}
}

func TestAgentTaskNextStepsDispatcherStopReleasesUncommittedReservation(t *testing.T) {
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(nil, func(context.Context, string, string, []string, string, string, int) ([]string, error) {
		return nil, nil
	}), 1, 1)
	if !d.reserve() {
		t.Fatal("reservation rejected")
	}
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	stopDone := make(chan struct{})
	go func() {
		d.stop(stopCtx)
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("dispatcher stop deadlocked with an uncommitted reservation")
	}
	if d.enqueueReserved(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("enqueue accepted after uncommitted reservation was released")
	}
}

func TestAgentTaskNextStepsDispatcherStopTimesOutReservationWait(t *testing.T) {
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(nil, func(context.Context, string, string, []string, string, string, int) ([]string, error) {
		return nil, nil
	}), 1, 1)
	d.reservationTimeout = 10 * time.Millisecond
	if !d.reserve() {
		t.Fatal("reservation rejected")
	}
	started := time.Now()
	d.stop(context.Background())
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond {
		t.Fatalf("stop returned before reservation timeout: %s", elapsed)
	}
	if d.enqueueReserved(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("enqueue accepted after reservation timeout")
	}
}

func TestAgentTaskNextStepsDispatcherRejectsCanceledLifecycleAdmission(t *testing.T) {
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	cancel()
	d := newAgentTaskNextStepsDispatcherWithLimits(lifecycleCtx, dispatcherTestOptions(nil, func(context.Context, string, string, []string, string, string, int) ([]string, error) {
		return nil, nil
	}), 1, 1)
	defer d.stop(context.Background())
	if d.reserve() {
		t.Fatal("reserve accepted after lifecycle cancellation")
	}
	if d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("enqueue accepted after lifecycle cancellation")
	}
}

func TestAgentTaskNextStepsRejectedAfterCompletionRevokesPending(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
			return []string{"next"}, nil
		},
	}
	opts.nextStepsDispatcher = &agentTaskNextStepsDispatcher{
		reserve:         func() bool { return true },
		enqueueReserved: func(agentTaskNextStepsJob) bool { return false },
	}
	_, err := runAgentTaskMessage(context.Background(), opts, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "reply"}, nil
	}, mysqlstore.AgentTask{ID: 7, Model: "claude-test", MetadataJSON: `{"cwd":"` + t.TempDir() + `","source":"webui-agent"}`}, agenttasks.MessageInput{Content: "prompt"})
	if err != nil {
		t.Fatalf("runAgentTaskMessage error: %v", err)
	}
	events := nextStepsPayloads(t, fake)
	if len(events) != 1 || len(events[0].Suggestions) != 0 {
		t.Fatalf("revocation events=%+v, want one empty next_steps event", events)
	}
	_, resultJSON := fake.lastFinishedAgentSnapshot()
	if !strings.Contains(resultJSON, `"next_steps_status":"pending"`) {
		t.Fatalf("completed result=%s, want pending marker before revocation", resultJSON)
	}
}

func TestAgentTaskNextStepsDispatcherLimitsWorkerConcurrency(t *testing.T) {
	var active, maxActive atomic.Int32
	release := make(chan struct{})
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), Options{
		TenantService:       &fakeTenantService{},
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			n := active.Add(1)
			for {
				old := maxActive.Load()
				if n <= old || maxActive.CompareAndSwap(old, n) {
					break
				}
			}
			<-release
			active.Add(-1)
			return nil, nil
		},
	}, 2, 4)
	for i := 0; i < 4; i++ {
		if !d.enqueue(agentTaskNextStepsJob{task: mysqlstore.AgentTask{ID: uint64(i + 1)}, source: "webui-agent", response: "reply"}) {
			t.Fatalf("enqueue %d rejected unexpectedly", i)
		}
	}
	deadline := time.After(time.Second)
	for maxActive.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("workers did not reach configured concurrency; max=%d", maxActive.Load())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := maxActive.Load(); got > 2 {
		t.Fatalf("max concurrent jobs=%d, want <=2", got)
	}
	close(release)
	d.stop(context.Background())
}

func TestAgentTaskNextStepsDispatcherUsesDefaultWorkerCount(t *testing.T) {
	var active, maxActive atomic.Int32
	release := make(chan struct{})
	d := newAgentTaskNextStepsDispatcher(context.Background(), Options{
		TenantService:       &fakeTenantService{},
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			n := active.Add(1)
			for {
				old := maxActive.Load()
				if n <= old || maxActive.CompareAndSwap(old, n) {
					break
				}
			}
			<-release
			active.Add(-1)
			return nil, nil
		},
	})
	defer d.stop(context.Background())
	for i := 0; i < defaultAgentTaskNextStepsWorkers; i++ {
		if !d.enqueue(agentTaskNextStepsJob{task: mysqlstore.AgentTask{ID: uint64(i + 1)}, source: "webui-agent", response: "reply"}) {
			t.Fatalf("enqueue %d rejected unexpectedly", i)
		}
	}
	deadline := time.After(time.Second)
	for maxActive.Load() < defaultAgentTaskNextStepsWorkers {
		select {
		case <-deadline:
			t.Fatalf("default workers did not start concurrently; max=%d", maxActive.Load())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
}

func TestAgentTaskNextStepsDispatcherRejectsFullQueueWithoutBlocking(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(nil, func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
		close(started)
		<-release
		return nil, nil
	}), 1, 1)
	defer d.stop(context.Background())
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("expected worker slot to accept first job")
	}
	<-started
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("expected queue slot to accept second job")
	}
	done := make(chan bool, 1)
	go func() {
		done <- d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"})
	}()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("full queue accepted a third job")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("enqueue blocked on a full queue")
	}
	close(release)
}

func TestAgentTaskNextStepsDispatcherCapsJobContext(t *testing.T) {
	deadlineSeen := make(chan time.Time, 1)
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(nil, func(ctx context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("job context has no deadline")
		} else {
			deadlineSeen <- deadline
		}
		return nil, nil
	}), 1, 1)
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("enqueue rejected")
	}
	select {
	case deadline := <-deadlineSeen:
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > agentTaskNextStepsTimeout {
			t.Fatalf("job deadline remaining=%s, want <=%s", remaining, agentTaskNextStepsTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("job did not execute")
	}
	d.stop(context.Background())
}

func TestAgentTaskNextStepsDispatcherStopCancelsRunningAndQueuedWork(t *testing.T) {
	started := make(chan struct{})
	var canceled atomic.Int32
	fake := &fakeTenantService{}
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(fake, func(ctx context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		canceled.Add(1)
		return nil, ctx.Err()
	}), 1, 1)
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("expected running job to be accepted")
	}
	<-started
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("expected queued job to be accepted")
	}
	stopDone := make(chan struct{})
	go func() { d.stop(context.Background()); close(stopDone) }()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("stop did not wait for workers to exit")
	}
	if canceled.Load() != 1 {
		t.Fatalf("canceled running jobs=%d, want 1; queued work should be discarded", canceled.Load())
	}
	if got := nextStepsPayloads(t, fake); len(got) != 0 {
		t.Fatalf("emitted %d next_steps events after shutdown cancellation, want 0", len(got))
	}
	// stop is lifecycle-safe and idempotent, including after workers exited.
	d.stop(context.Background())
}

// Shutdown cancels the worker context, but an injected provider may ignore
// cancellation and return suggestions anyway. That late append is best-effort
// and must not mutate the already-completed task lifecycle.
func TestAgentTaskNextStepsShutdownTreatsLateAppendAsBestEffort(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	canceled := make(chan struct{})
	fake := &fakeTenantService{}
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(fake, func(ctx context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
		close(started)
		go func() {
			<-ctx.Done()
			close(canceled)
		}()
		<-release
		return []string{"late"}, nil
	}), 1, 1)
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("expected job to be accepted")
	}
	<-started
	stopDone := make(chan struct{})
	go func() { d.stop(context.Background()); close(stopDone) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the running job context")
	}
	close(release)
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wait for the cancellation-ignoring worker")
	}
	if got := nextStepsPayloads(t, fake); len(got) != 1 {
		t.Fatalf("emitted %d late next_steps events, want one best-effort append", len(got))
	}
}

func TestAgentTaskNextStepsDispatcherRecoversJobPanic(t *testing.T) {
	var calls atomic.Int32
	first := make(chan struct{})
	second := make(chan struct{})
	d := newAgentTaskNextStepsDispatcherWithLimits(context.Background(), dispatcherTestOptions(nil, func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
		if calls.Add(1) == 1 {
			close(first)
			panic("suggestion panic")
		}
		close(second)
		return nil, nil
	}), 1, 1)
	defer d.stop(context.Background())
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("expected first job to be accepted")
	}
	<-first
	if !d.enqueue(agentTaskNextStepsJob{task: webAgentTask(), source: "webui-agent", response: "reply"}) {
		t.Fatal("expected second job to be accepted")
	}
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("worker did not continue after a panicking job")
	}
}

type nextStepsPayload struct {
	Source      string   `json:"source"`
	Suggestions []string `json:"suggestions"`
}

func nextStepsPayloads(t *testing.T, fake *fakeTenantService) []nextStepsPayload {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	out := make([]nextStepsPayload, 0)
	for _, event := range fake.agentTaskEvents {
		if event.EventType != agenttasks.EventNextSteps {
			continue
		}
		var payload nextStepsPayload
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("next_steps payload json: %v (raw=%s)", err, event.PayloadJSON)
		}
		out = append(out, payload)
	}
	return out
}

func webAgentTask() mysqlstore.AgentTask {
	return mysqlstore.AgentTask{ID: 7, Model: "claude-sonnet-4-6"}
}

type nextStepsReservationErrorService struct {
	*fakeTenantService
	appendCompletedErr error
	finishCompletedErr error
	panicNextSteps     bool
}

func (s *nextStepsReservationErrorService) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	if input.EventType == agenttasks.EventNextSteps && s.panicNextSteps {
		panic("secret dropped append panic")
	}
	if input.EventType == agenttasks.EventCompleted && s.appendCompletedErr != nil {
		return 0, s.appendCompletedErr
	}
	return s.fakeTenantService.AppendAgentTaskEvent(ctx, input)
}

func TestAgentTaskPostFinishDroppedAppendPanicPreservesCompleted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := &nextStepsReservationErrorService{fakeTenantService: &fakeTenantService{}, panicNextSteps: true}
	opts := Options{
		TenantService:       svc,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
			return []string{"next"}, nil
		},
	}
	opts.nextStepsDispatcher = &agentTaskNextStepsDispatcher{
		reserve:         func() bool { return true },
		enqueueReserved: func(agentTaskNextStepsJob) bool { return false },
	}
	_, err := runAgentTaskMessage(context.Background(), opts, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "reply"}, nil
	}, mysqlstore.AgentTask{ID: 7, Model: "claude-test", MetadataJSON: `{"cwd":"` + t.TempDir() + `","source":"webui-agent"}`}, agenttasks.MessageInput{Content: "prompt"})
	if err != nil {
		t.Fatalf("runAgentTaskMessage error: %v", err)
	}
	status, _ := svc.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusCompleted {
		t.Fatalf("status=%q after dropped append panic, want completed", status)
	}
}

func TestAgentTaskPostFinishTelemetryPanicPreservesCompleted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	emitter := telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		if event.Name == "agent.next_steps.queued" || event.Name == "agent.run.finished" {
			panic("secret telemetry panic")
		}
		return nil
	}))
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
			return []string{"next"}, nil
		},
	}
	opts.nextStepsDispatcher = &agentTaskNextStepsDispatcher{
		reserve:         func() bool { return true },
		enqueueReserved: func(agentTaskNextStepsJob) bool { return true },
	}
	ctx := telemetry.WithEmitter(context.Background(), emitter)
	_, err := runAgentTaskMessage(ctx, opts, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "reply"}, nil
	}, mysqlstore.AgentTask{ID: 7, Model: "claude-test", MetadataJSON: `{"cwd":"` + t.TempDir() + `","source":"webui-agent"}`}, agenttasks.MessageInput{Content: "prompt"})
	if err != nil {
		t.Fatalf("runAgentTaskMessage error: %v", err)
	}
	status, _ := fake.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusCompleted {
		t.Fatalf("status=%q after telemetry panic, want completed", status)
	}
}

func (s *nextStepsReservationErrorService) FinishAgentTask(ctx context.Context, taskID uint64, status, resultJSON string) error {
	if status == agenttasks.StatusCompleted && s.finishCompletedErr != nil {
		return s.finishCompletedErr
	}
	return s.fakeTenantService.FinishAgentTask(ctx, taskID, status, resultJSON)
}

func TestAgentTaskNextStepsReservationRollsBackOnCompletedAppendError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := &nextStepsReservationErrorService{fakeTenantService: &fakeTenantService{}, appendCompletedErr: errors.New("append failed")}
	opts := Options{TenantService: svc, NextStepsWebEnabled: true, NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
		return []string{"next"}, nil
	}}
	opts.nextStepsDispatcher = newAgentTaskNextStepsDispatcherWithLimits(context.Background(), opts, 1, 1)
	defer opts.nextStepsDispatcher.stop(context.Background())
	_, err := runAgentTaskMessage(context.Background(), opts, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "reply"}, nil
	}, mysqlstore.AgentTask{ID: 7, Model: "claude-test", MetadataJSON: `{"cwd":"` + t.TempDir() + `","source":"webui-agent"}`}, agenttasks.MessageInput{Content: "prompt"})
	if err == nil {
		t.Fatal("runAgentTaskMessage succeeded, want completed-event append error")
	}
	if !opts.nextStepsDispatcher.reserve() {
		t.Fatal("reservation leaked after completed-event append error")
	}
	opts.nextStepsDispatcher.releaseReservation()
}

func TestAgentTaskNextStepsReservationRollsBackOnFinishError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := &nextStepsReservationErrorService{fakeTenantService: &fakeTenantService{}, finishCompletedErr: errors.New("finish failed")}
	opts := Options{TenantService: svc, NextStepsWebEnabled: true, NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
		return []string{"next"}, nil
	}}
	opts.nextStepsDispatcher = newAgentTaskNextStepsDispatcherWithLimits(context.Background(), opts, 1, 1)
	defer opts.nextStepsDispatcher.stop(context.Background())
	_, err := runAgentTaskMessage(context.Background(), opts, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "reply"}, nil
	}, mysqlstore.AgentTask{ID: 7, Model: "claude-test", MetadataJSON: `{"cwd":"` + t.TempDir() + `","source":"webui-agent"}`}, agenttasks.MessageInput{Content: "prompt"})
	if err == nil {
		t.Fatal("runAgentTaskMessage succeeded, want FinishAgentTask error")
	}
	if !opts.nextStepsDispatcher.reserve() {
		t.Fatal("reservation leaked after FinishAgentTask error")
	}
	opts.nextStepsDispatcher.releaseReservation()
}

func TestAgentTaskNextStepsMalformedDispatcherOmitsPendingAndReleasesReservation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	var released atomic.Int32
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
			return []string{"next"}, nil
		},
	}
	opts.nextStepsDispatcher = &agentTaskNextStepsDispatcher{
		reserve:            func() bool { return true },
		releaseReservation: func() { released.Add(1) },
	}
	_, err := runAgentTaskMessage(context.Background(), opts, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{Response: "reply"}, nil
	}, mysqlstore.AgentTask{ID: 7, Model: "claude-test", MetadataJSON: `{"cwd":"` + t.TempDir() + `","source":"webui-agent"}`}, agenttasks.MessageInput{Content: "prompt"})
	if err != nil {
		t.Fatalf("runAgentTaskMessage error: %v", err)
	}
	_, resultJSON := fake.lastFinishedAgentSnapshot()
	if strings.Contains(resultJSON, `"next_steps_status":"pending"`) {
		t.Fatalf("malformed dispatcher advertised pending status: %s", resultJSON)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released reservations=%d, want 1", got)
	}
}

func TestAppendAgentTaskNextStepsTelemetryIsSanitized(t *testing.T) {
	fake := &fakeTenantService{}
	var events []telemetry.Event
	emitter := telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	}))
	ctx := telemetry.WithEmitter(context.Background(), emitter)
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
			return nil, errors.New("provider secret prompt response")
		},
	}
	appendAgentTaskNextSteps(ctx, opts, webAgentTask(), t.TempDir(), "webui-agent", "trace-sanitized", "prompt secret", "response secret", "claude-test", "", nil)
	var finished *telemetry.Event
	for i := range events {
		if events[i].Name == "agent.next_steps.finished" {
			finished = &events[i]
			break
		}
	}
	if finished == nil {
		t.Fatalf("missing next_steps finished telemetry: %+v", events)
	}
	if finished.Error != "" {
		t.Fatalf("telemetry error leaked raw text: %q", finished.Error)
	}
	if got := finished.Properties["error_class"]; got != "provider_error" {
		t.Fatalf("error_class=%v, want provider_error", got)
	}
	if finished.DurationMS < 0 {
		t.Fatalf("duration_ms=%d, want non-negative", finished.DurationMS)
	}
}

func TestAppendAgentTaskNextStepsPanicTelemetryIsSanitized(t *testing.T) {
	var events []telemetry.Event
	emitter := telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	}))
	opts := Options{
		TenantService:       &fakeTenantService{},
		NextStepsWebEnabled: true,
		NextStepsFunc: func(context.Context, string, string, []string, string, string, int) ([]string, error) {
			panic("secret panic payload")
		},
	}
	appendAgentTaskNextSteps(telemetry.WithEmitter(context.Background(), emitter), opts, webAgentTask(), t.TempDir(), "webui-agent", "trace-panic", "prompt", "response", "claude-test", "", nil)
	for _, event := range events {
		if event.Name != "agent.next_steps.finished" {
			continue
		}
		if event.Error != "" || event.Properties["error_class"] != "panic" {
			t.Fatalf("panic telemetry leaked details: %+v", event)
		}
		return
	}
	t.Fatalf("missing panic finished telemetry: %+v", events)
}

func TestAppendAgentTaskNextStepsEmitsSuggestions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	var gotToolNames []string
	var gotCount int
	var gotModel, gotProvider string
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, toolNames []string, model, provider string, count int) ([]string, error) {
			gotToolNames = toolNames
			gotCount = count
			gotModel = model
			gotProvider = provider
			return []string{"跑一遍测试", "补单元测试"}, nil
		},
	}
	appendAgentTaskNextSteps(context.Background(), opts, webAgentTask(), t.TempDir(), "webui-agent", "trace-1", "改代码", "改好了", "claude-sonnet-4-6", "anthropic-secondary", []string{"Edit", "Bash"})
	got := nextStepsPayloads(t, fake)
	if len(got) != 1 {
		t.Fatalf("emitted %d next_steps events, want 1", len(got))
	}
	if got[0].Source != "runner" {
		t.Fatalf("source = %q, want %q", got[0].Source, "runner")
	}
	if len(got[0].Suggestions) != 2 || got[0].Suggestions[0] != "跑一遍测试" {
		t.Fatalf("suggestions = %q, want the two generated items", got[0].Suggestions)
	}
	// cfg.Count comes from nextsteps.ConfigFromSettings' defaulting (no
	// settings.json present in the temp cwd), not from a value this test made
	// up -- pinning it catches the runner silently forwarding the wrong count.
	if gotCount != 3 {
		t.Fatalf("count forwarded to NextStepsFunc = %d, want the resolved default (3)", gotCount)
	}
	if len(gotToolNames) != 2 || gotToolNames[0] != "Edit" || gotToolNames[1] != "Bash" {
		t.Fatalf("toolNames forwarded to NextStepsFunc = %q, want [Edit Bash]", gotToolNames)
	}
	// Without an explicit tier mapping, retain the parent model and provider.
	if gotModel != "claude-sonnet-4-6" {
		t.Fatalf("model forwarded to NextStepsFunc = %q, want the parent model", gotModel)
	}
	if gotProvider != "anthropic-secondary" {
		t.Fatalf("provider forwarded to NextStepsFunc = %q, want %q", gotProvider, "anthropic-secondary")
	}
}

// The model used for tier resolution must be the turn's own resolved model
// (the `model` parameter, sourced from runAgentTaskMessage's local that falls
// back to metadata["model"]) and not task.Model, which can be stale or empty.
// task.Model here is deliberately a different, non-Anthropic-looking value so
// a regression that resolves tiering from task.Model instead would resolve to
// a different (wrong) tier model and fail this assertion.
func TestAppendAgentTaskNextStepsResolvesTierFromPassedModelNotTaskModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(cwd, ".niuma-legacy"))
	if err := os.MkdirAll(filepath.Join(cwd, ".niuma-legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".niuma-legacy", "settings.json"), []byte(`{"subagentModelTiers":{"haiku":"tier-configured-haiku-model"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTenantService{}
	var gotModel, gotProvider string
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, model, provider string, _ int) ([]string, error) {
			gotModel = model
			gotProvider = provider
			return []string{"跑一遍测试"}, nil
		},
	}
	task := mysqlstore.AgentTask{ID: 7, Model: "claude-opus-stale"}
	appendAgentTaskNextSteps(context.Background(), opts, task, cwd, "webui-agent", "trace-1", "改代码", "改好了", "gpt-4o-mini", "openai-secondary", nil)
	if gotModel != "tier-configured-haiku-model" {
		t.Fatalf("model forwarded to NextStepsFunc = %q, want the tier resolved from the passed model %q (task.Model must not be used)", gotModel, "tier-configured-haiku-model")
	}
	if gotProvider != "openai-secondary" {
		t.Fatalf("provider forwarded to NextStepsFunc = %q, want %q", gotProvider, "openai-secondary")
	}
}

// Options.NextStepsWebEnabled defaults to false (zero value): web generation
// is opt-in even when NextStepsFunc/TenantService are wired and every other
// condition for emitting is met. Embedders whose clients drop the SSE stream
// at terminal status would pay for suggestions nobody can receive; only the
// CLI, whose bundled web UI waits out the server-side drain, flips this on.
func TestAppendAgentTaskNextStepsSkipsWhenWebDisabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	called := false
	opts := Options{
		TenantService: fake,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			called = true
			return []string{"跑一遍测试"}, nil
		},
	}
	appendAgentTaskNextSteps(context.Background(), opts, webAgentTask(), t.TempDir(), "webui-agent", "trace-1", "改代码", "改好了", "claude-sonnet-4-6", "", nil)
	if called {
		t.Error("NextStepsFunc was called with NextStepsWebEnabled unset, want gated off by default")
	}
	if got := nextStepsPayloads(t, fake); len(got) != 0 {
		t.Errorf("emitted %d events while NextStepsWebEnabled is false, want 0", len(got))
	}
}

// 只有 web UI 有输入框可引导；子代理运行与 REST 调用方不该被计费。
func TestAppendAgentTaskNextStepsSkipsNonWebSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, source := range []string{"", "api", "runner", "subagent"} {
		fake := &fakeTenantService{}
		called := false
		opts := Options{
			TenantService:       fake,
			NextStepsWebEnabled: true,
			NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
				called = true
				return []string{"跑一遍测试"}, nil
			},
		}
		appendAgentTaskNextSteps(context.Background(), opts, webAgentTask(), t.TempDir(), source, "trace-1", "改代码", "改好了", "claude-test", "", nil)
		if called {
			t.Errorf("source %q: generated suggestions, want skipped", source)
		}
		if got := nextStepsPayloads(t, fake); len(got) != 0 {
			t.Errorf("source %q: emitted %d events, want 0", source, len(got))
		}
	}
}

// 失败、空结果、未接线：都必须静默,不得阻断 turn 收尾。
func TestAppendAgentTaskNextStepsStaysSilentOnFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name string
		fn   NextStepsFunc
	}{
		{"error", func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			return nil, errors.New("provider down")
		}},
		{"empty", func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			return nil, nil
		}},
		{"not wired", nil},
	}
	for _, tc := range cases {
		fake := &fakeTenantService{}
		opts := Options{TenantService: fake, NextStepsWebEnabled: true, NextStepsFunc: tc.fn}
		appendAgentTaskNextSteps(context.Background(), opts, webAgentTask(), t.TempDir(), "webui-agent", "trace-1", "改代码", "改好了", "claude-test", "", nil)
		if got := nextStepsPayloads(t, fake); len(got) != 0 {
			t.Errorf("%s: emitted %d events, want 0", tc.name, len(got))
		}
	}
}

// 没有 assistant 回复就无从推断下一步。
func TestAppendAgentTaskNextStepsSkipsEmptyResponse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	called := false
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			called = true
			return []string{"跑一遍测试"}, nil
		},
	}
	appendAgentTaskNextSteps(context.Background(), opts, webAgentTask(), t.TempDir(), "webui-agent", "trace-1", "改代码", "   ", "claude-test", "", nil)
	if called {
		t.Error("generated suggestions without an assistant response, want skipped")
	}
}

// settings.json 里的 nextSteps.enabled=false 是用户对这个功能的显式退出——
// 一个会为每轮 web turn 花 provider token 的功能，必须能被关掉，而且关掉后
// 一定不能再调用 NextStepsFunc。
func TestAppendAgentTaskNextStepsSkipsWhenDisabledByConfig(t *testing.T) {
	// HOME 也要隔离：否则这个断言证明的可能是「全局层碰巧没配」，而不是
	// 「项目层真的生效了」。
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(cwd, ".niuma-legacy"))
	if err := os.MkdirAll(filepath.Join(cwd, ".niuma-legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".niuma-legacy", "settings.json"), []byte(`{"nextSteps":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTenantService{}
	called := false
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			called = true
			return []string{"跑一遍测试"}, nil
		},
	}
	appendAgentTaskNextSteps(context.Background(), opts, webAgentTask(), cwd, "webui-agent", "trace-1", "改代码", "改好了", "claude-test", "", nil)
	if called {
		t.Error("generated suggestions while nextSteps.enabled=false, want skipped")
	}
	if got := nextStepsPayloads(t, fake); len(got) != 0 {
		t.Errorf("emitted %d events while disabled, want 0", len(got))
	}
}

// 一个注入的 NextStepsFunc panic 绝不能变成「已完成的 turn 被判成 failed」——
// EventCompleted 已经落库、response 已经流给浏览器，这里必须原地吞掉 panic
// 并像任何其它失败模式一样保持静默。
func TestAppendAgentTaskNextStepsRecoversFromPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{}
	opts := Options{
		TenantService:       fake,
		NextStepsWebEnabled: true,
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			panic("boom")
		},
	}
	appendAgentTaskNextSteps(context.Background(), opts, webAgentTask(), t.TempDir(), "webui-agent", "trace-1", "改代码", "改好了", "claude-test", "", nil)
	if got := nextStepsPayloads(t, fake); len(got) != 0 {
		t.Errorf("emitted %d events after a panicking generator, want 0", len(got))
	}
}

// End-to-end proof that completion is committed before optional suggestions
// are generated. The HTTP path must remain usable even while the async
// generator is blocked, and the completed payload advertises the pending
// readback state for clients that support late events.
func TestAgentTaskCompletionPrecedesAsyncNextSteps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{{
			ID:           41,
			AgentName:    "web-agent",
			Status:       agenttasks.StatusReady,
			Model:        "claude-test",
			MetadataJSON: `{"cwd":"/workspace","source":"webui-agent"}`,
		}},
	}
	started := make(chan struct{})
	release := make(chan struct{})
	opts := Options{
		AuthToken:           "token",
		TenantService:       fake,
		NextStepsWebEnabled: true,
		StreamQueryFunc: func(_ context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			if _, err := sink.Write([]byte("streamed reply")); err != nil {
				return query.Result{}, err
			}
			return query.Result{Model: req.Model, Turns: 1}, nil
		},
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			close(started)
			<-release
			return []string{"跑一遍测试"}, nil
		},
	}
	opts.nextStepsDispatcher = newAgentTaskNextStepsDispatcherWithLimits(context.Background(), opts, 1, 1)
	defer opts.nextStepsDispatcher.stop(context.Background())
	handler := NewHandler(opts, nil)

	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/message", strings.NewReader(`{"from_agent":"webui","content":"hello","trace_id":"trace-order"}`))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	waitForTestCondition(t, func() bool {
		finishedStatus, _ := fake.lastFinishedAgentSnapshot()
		return finishedStatus == agenttasks.StatusCompleted
	})
	if fake.finishSawNextStepsEventSnapshot() {
		t.Fatal("FinishAgentTask observed next_steps before async generation completed")
	}
	_, finishedResult := fake.lastFinishedAgentSnapshot()
	if !strings.Contains(finishedResult, `"next_steps_status":"pending"`) {
		t.Fatalf("completed result = %s, want next_steps_status=pending", finishedResult)
	}
	if got := nextStepsPayloads(t, fake); len(got) != 0 {
		t.Fatalf("emitted %d next_steps events before generator release, want 0", len(got))
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("next-step generator did not start")
	}
	close(release)
	waitForTestCondition(t, func() bool { return len(nextStepsPayloads(t, fake)) == 1 })
	events, err := fake.ListAgentTaskEvents(context.Background(), 41, 20)
	if err != nil {
		t.Fatal(err)
	}
	completedIndex, nextStepsIndex := -1, -1
	for i, event := range events {
		switch event.EventType {
		case agenttasks.EventCompleted:
			completedIndex = i
		case agenttasks.EventNextSteps:
			nextStepsIndex = i
		}
	}
	if completedIndex < 0 || nextStepsIndex < 0 || completedIndex >= nextStepsIndex {
		t.Fatalf("event order = %+v, want completed before next_steps", events)
	}
}

// Clients that do not implement late-event readback remain compatible: the
// completed turn succeeds before they consume the optional suggestion event.
func TestAgentTaskNextStepsOldClientCanIgnoreLateSuggestions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{
		ID: 41, Status: agenttasks.StatusReady,
		MetadataJSON: `{"cwd":"/workspace","source":"webui-agent"}`,
	}}}
	started := make(chan struct{})
	release := make(chan struct{})
	opts := Options{
		AuthToken:           "token",
		TenantService:       fake,
		NextStepsWebEnabled: true,
		StreamQueryFunc: func(_ context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			_, _ = sink.Write([]byte("reply"))
			return query.Result{Model: req.Model, Turns: 1}, nil
		},
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			close(started)
			<-release
			return []string{"late"}, nil
		},
	}
	opts.nextStepsDispatcher = newAgentTaskNextStepsDispatcherWithLimits(context.Background(), opts, 1, 1)
	defer opts.nextStepsDispatcher.stop(context.Background())
	handler := NewHandler(opts, nil)
	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/message", strings.NewReader(`{"from_agent":"webui","content":"hello","trace_id":"trace-old-client"}`))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	waitForTestCondition(t, func() bool {
		status, _ := fake.lastFinishedAgentSnapshot()
		return status == agenttasks.StatusCompleted
	})
	if got := nextStepsPayloads(t, fake); len(got) != 0 {
		t.Fatalf("emitted %d next_steps events before old client readback, want 0", len(got))
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("next-step generator did not start")
	}
	_, result := fake.lastFinishedAgentSnapshot()
	if !strings.Contains(result, `"next_steps_status":"pending"`) {
		t.Fatalf("completed result = %s, want pending marker for optional suggestion", result)
	}
	// The old client stops here and never performs REST readback. The optional
	// event may still be generated without affecting the completed turn.
	close(release)
	waitForTestCondition(t, func() bool { return len(nextStepsPayloads(t, fake)) == 1 })
	status, _ := fake.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusCompleted {
		t.Fatalf("status = %q after late suggestion, want completed", status)
	}
}

// A panicking NextStepsFunc must not flip an already-completed turn to
// failed. Before the recover in appendAgentTaskNextSteps, this panic would
// propagate out of runAgentTaskMessage and get caught by the goroutine's
// recoverAgentTaskRun instead, which finishes the task as StatusFailed even
// though EventCompleted was already persisted.
func TestAppendAgentTaskNextStepsPanicDoesNotFailCompletedTurn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{{
			ID:           41,
			AgentName:    "web-agent",
			Status:       agenttasks.StatusReady,
			Model:        "claude-test",
			MetadataJSON: `{"cwd":"/workspace","source":"webui-agent"}`,
		}},
	}
	handler := NewHandler(Options{
		AuthToken:           "token",
		TenantService:       fake,
		NextStepsWebEnabled: true,
		StreamQueryFunc: func(_ context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			if _, err := sink.Write([]byte("streamed reply")); err != nil {
				return query.Result{}, err
			}
			return query.Result{Model: req.Model, Turns: 1}, nil
		},
		NextStepsFunc: func(_ context.Context, _, _ string, _ []string, _, _ string, _ int) ([]string, error) {
			panic("boom")
		},
	}, nil)

	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/message", strings.NewReader(`{"from_agent":"webui","content":"hello","trace_id":"trace-panic"}`))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	waitForTestCondition(t, func() bool {
		finishedStatus, _ := fake.lastFinishedAgentSnapshot()
		return finishedStatus != ""
	})
	finishedStatus, finishedResult := fake.lastFinishedAgentSnapshot()
	if finishedStatus != agenttasks.StatusCompleted {
		t.Fatalf("finished status = %q, want %q (a panicking suggestion generator must not fail an already-completed turn); result=%s", finishedStatus, agenttasks.StatusCompleted, finishedResult)
	}
	if !strings.Contains(finishedResult, "streamed reply") {
		t.Fatalf("finished result = %s, want it to still contain the real response", finishedResult)
	}
}
