package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func newAgentTaskMessageHTTPRequest(taskID uint64, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/tenant/agent-tasks/%d/message", taskID), strings.NewReader(body))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	return req
}

// TestTenantAgentTaskMessagePanicDoesNotKillProcess 覆盖 AUDIT-P0-10 ①：
// detached runner 里的 panic 必须被 recover 掉（测试进程活着本身就是断言），
// 记成遥测事件，并把任务显式置 failed，而不是永久停在 running。
func TestTenantAgentTaskMessagePanicDoesNotKillProcess(t *testing.T) {
	fake := &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{{
			ID:           41,
			AgentName:    "web-agent",
			Status:       agenttasks.StatusReady,
			Model:        "claude-test",
			TraceID:      "trace-panic",
			MetadataJSON: fmt.Sprintf(`{"cwd":%q,"prompt_mode":"code"}`, t.TempDir()),
		}},
	}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake}, func(context.Context, QueryRequest) (query.Result, error) {
		panic("runner exploded")
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAgentTaskMessageHTTPRequest(41, `{"from_agent":"webui","content":"boom","trace_id":"trace-panic"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	waitForTestCondition(t, func() bool {
		status, _ := fake.lastFinishedAgentSnapshot()
		return status == agenttasks.StatusFailed
	})
	status, result := fake.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusFailed {
		t.Fatalf("finished status = %q, want failed", status)
	}
	if !strings.Contains(result, "runner exploded") || !strings.Contains(result, `"panic":true`) {
		t.Fatalf("finish payload should name the panic: %s", result)
	}

	events, err := fake.ListAgentTaskEvents(context.Background(), 41, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sawFailed bool
	for _, event := range events {
		if event.EventType == agenttasks.EventFailed {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Fatalf("expected a failed event for the panicked run: %+v", events)
	}

	waitForTestCondition(t, func() bool {
		for _, event := range fake.telemetryRecordsSnapshot() {
			if event.Name == "server.background.panic" {
				return true
			}
		}
		return false
	})
	var panicEvent *telemetry.Event
	for _, event := range fake.telemetryRecordsSnapshot() {
		if event.Name == "server.background.panic" {
			panicEvent = &event
			break
		}
	}
	if panicEvent == nil {
		t.Fatal("missing server.background.panic telemetry")
	}
	if panicEvent.Status != telemetry.StatusError || !strings.Contains(panicEvent.Error, "runner exploded") {
		t.Fatalf("unexpected panic telemetry: %+v", panicEvent)
	}
	if panicEvent.Properties["goroutine"] != "server.runAgentTaskMessage" {
		t.Fatalf("panic telemetry should name the goroutine: %+v", panicEvent.Properties)
	}
	if stack, _ := panicEvent.Properties["stack"].(string); !strings.Contains(stack, "runtime/debug.Stack") {
		t.Fatalf("panic telemetry should carry a stack: %+v", panicEvent.Properties)
	}
	// 脱敏约定：prompt 正文与 token 不得进 properties。
	for key := range panicEvent.Properties {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "prompt") || strings.Contains(lower, "token") || strings.Contains(lower, "content") {
			t.Fatalf("panic telemetry leaked a sensitive property %q: %+v", key, panicEvent.Properties)
		}
	}
}

// TestTenantAgentTaskMessageRejectsBeyondConcurrencyLimit 覆盖 AUDIT-P0-10 ③：
// 超过并发上限必须拒绝并返回清晰错误，且不能把被拒的任务改成 running。
func TestTenantAgentTaskMessageRejectsBeyondConcurrencyLimit(t *testing.T) {
	fake := &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{
			{ID: 41, AgentName: "web-agent", Status: agenttasks.StatusReady, TraceID: "trace-slot", MetadataJSON: fmt.Sprintf(`{"cwd":%q}`, t.TempDir())},
			{ID: 42, AgentName: "web-agent", Status: agenttasks.StatusReady, TraceID: "trace-reject", MetadataJSON: fmt.Sprintf(`{"cwd":%q}`, t.TempDir())},
		},
	}
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, AgentTaskMaxConcurrentRuns: 1}, func(context.Context, QueryRequest) (query.Result, error) {
		started <- struct{}{}
		<-block
		return query.Result{Response: "done"}, nil
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAgentTaskMessageHTTPRequest(41, `{"from_agent":"webui","content":"hold the slot"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("first run status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(block)
		t.Fatal("first run never started")
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newAgentTaskMessageHTTPRequest(42, `{"from_agent":"webui","content":"should be rejected"}`))
	if rec.Code != http.StatusServiceUnavailable {
		close(block)
		t.Fatalf("second run status=%d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "at capacity (1 concurrent runs)") {
		close(block)
		t.Fatalf("rejection should state the limit: %s", rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		close(block)
		t.Fatalf("Retry-After = %q, want 5", got)
	}
	// 被拒的任务必须原样留在 ready，否则会留下一个没人跑的 running 任务。
	rejected, err := fake.GetAgentTask(context.Background(), 42)
	if err != nil {
		close(block)
		t.Fatal(err)
	}
	if rejected.Status != agenttasks.StatusReady {
		close(block)
		t.Fatalf("rejected task status = %q, want ready", rejected.Status)
	}
	waitForTestCondition(t, func() bool {
		for _, event := range fake.telemetryRecordsSnapshot() {
			if event.Name == "agent.run.rejected" && event.ResourceID == "42" {
				return true
			}
		}
		return false
	})

	// 槽位释放后必须能重新受理。
	close(block)
	waitForTestCondition(t, func() bool {
		status, _ := fake.lastFinishedAgentSnapshot()
		return status == agenttasks.StatusCompleted
	})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newAgentTaskMessageHTTPRequest(42, `{"from_agent":"webui","content":"retry now"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("retry after release status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("retried run never started after the slot was released")
	}
}

func TestAgentTaskRunLimiterReleaseIsIdempotent(t *testing.T) {
	limiter := newAgentTaskRunLimiter(2)
	first, ok := limiter.acquire()
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	second, ok := limiter.acquire()
	if !ok {
		t.Fatal("second acquire should succeed")
	}
	if _, ok := limiter.acquire(); ok {
		t.Fatal("third acquire should be rejected at limit 2")
	}
	first()
	first()
	if got := limiter.inFlight(); got != 1 {
		t.Fatalf("inFlight = %d after a doubled release, want 1", got)
	}
	second()
	if got := limiter.inFlight(); got != 0 {
		t.Fatalf("inFlight = %d, want 0", got)
	}
	if newAgentTaskRunLimiter(0).limit() != defaultAgentTaskMaxConcurrentRuns {
		t.Fatal("non-positive limit should fall back to the default")
	}
}

// fakeStaleAgentTaskStore 复刻真实 SQL 的租户隔离与 compare-and-set 语义：
// 写回按 (tenant_id, user_id, id, status=running) 精确匹配，任何一项不符都返回
// ErrNotFound。reaper 若拿错租户上下文，这里就会拒绝，测试随之失败。
type fakeStaleAgentTaskStore struct {
	mu       sync.Mutex
	tasks    []mysqlstore.AgentTask
	failed   map[uint64]string
	events   []agenttasks.EventInput
	listErr  error
	failErr  map[uint64]error
	failArgs [][3]uint64
}

func (s *fakeStaleAgentTaskStore) ListStaleRunningAgentTasks(_ context.Context, startedBefore time.Time, limit int) ([]mysqlstore.AgentTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []mysqlstore.AgentTask
	for _, task := range s.tasks {
		if task.Status != agenttasks.StatusRunning || !task.StartedAt.Before(startedBefore) {
			continue
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, task)
	}
	return out, nil
}

func (s *fakeStaleAgentTaskStore) FailStaleAgentTask(_ context.Context, tenantID, userID, taskID uint64, resultJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failArgs = append(s.failArgs, [3]uint64{tenantID, userID, taskID})
	if err := s.failErr[taskID]; err != nil {
		return err
	}
	for i := range s.tasks {
		task := &s.tasks[i]
		if task.ID != taskID || task.TenantID != tenantID || task.UserID != userID || task.Status != agenttasks.StatusRunning {
			continue
		}
		task.Status = agenttasks.StatusFailed
		task.ResultJSON = resultJSON
		if s.failed == nil {
			s.failed = map[uint64]string{}
		}
		s.failed[taskID] = resultJSON
		return nil
	}
	return mysqlstore.ErrNotFound
}

func (s *fakeStaleAgentTaskStore) AppendAgentTaskEvent(_ context.Context, input agenttasks.EventInput) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, input)
	return uint64(len(s.events)), nil
}

func (s *fakeStaleAgentTaskStore) snapshot() ([]mysqlstore.AgentTask, map[uint64]string, []agenttasks.EventInput, [][3]uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mysqlstore.AgentTask(nil), s.tasks...),
		s.failed,
		append([]agenttasks.EventInput(nil), s.events...),
		append([][3]uint64(nil), s.failArgs...)
}

// TestAgentTaskReaperFailsStaleTasksWithinTenantBoundary 覆盖 AUDIT-P0-10 ②：
// 超时仍停在 running 的任务被置 failed、写明是进程重启/超时，且严格按行归属操作。
func TestAgentTaskReaperFailsStaleTasksWithinTenantBoundary(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	timeout := 20 * time.Minute
	store := &fakeStaleAgentTaskStore{
		tasks: []mysqlstore.AgentTask{
			// 两个不同租户的孤儿任务。
			{ID: 1, TenantID: 10, UserID: 100, AgentName: "web-agent", Status: agenttasks.StatusRunning, TraceID: "trace-a", StartedAt: now.Add(-90 * time.Minute)},
			{ID: 2, TenantID: 20, UserID: 200, AgentName: "web-agent", Status: agenttasks.StatusRunning, TraceID: "trace-b", StartedAt: now.Add(-30 * time.Minute)},
			// 仍在超时窗口内，不该被动。
			{ID: 3, TenantID: 10, UserID: 100, Status: agenttasks.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
			// 已经是终态，不该被动。
			{ID: 4, TenantID: 10, UserID: 100, Status: agenttasks.StatusCompleted, StartedAt: now.Add(-90 * time.Minute), ResultJSON: `{"response":"real answer"}`},
		},
		failErr: map[uint64]error{},
	}
	reaper := &agentTaskReaper{store: store, timeout: timeout, interval: time.Hour, batch: 50, now: func() time.Time { return now }}

	if reaped := reaper.sweep(context.Background()); reaped != 2 {
		t.Fatalf("reaped = %d, want 2", reaped)
	}
	tasks, failed, events, failArgs := store.snapshot()

	for _, task := range tasks {
		switch task.ID {
		case 1, 2:
			if task.Status != agenttasks.StatusFailed {
				t.Fatalf("stale task %d status = %q, want failed", task.ID, task.Status)
			}
		case 3:
			if task.Status != agenttasks.StatusRunning {
				t.Fatalf("fresh running task %d was reaped (status=%q)", task.ID, task.Status)
			}
		case 4:
			if task.Status != agenttasks.StatusCompleted || task.ResultJSON != `{"response":"real answer"}` {
				t.Fatalf("terminal task %d was rewritten: %+v", task.ID, task)
			}
		}
	}

	// 失败原因必须诚实说明是失联，而不是伪装成正常完成。
	for _, id := range []uint64{1, 2} {
		payload := failed[id]
		if !strings.Contains(payload, `"status":"failed"`) || !strings.Contains(payload, `"stale":true`) {
			t.Fatalf("task %d payload should be an explicit stale failure: %s", id, payload)
		}
		if !strings.Contains(payload, "process restart or exceeded run timeout") {
			t.Fatalf("task %d payload should name the cause: %s", id, payload)
		}
		if !strings.Contains(payload, `"source":"stale_task_reaper"`) || !strings.Contains(payload, `"run_timeout":"20m0s"`) {
			t.Fatalf("task %d payload missing reaper provenance: %s", id, payload)
		}
	}

	// 每次写回都必须带读到的那一行的 tenant/user，绝不跨租户。
	wantArgs := map[uint64][2]uint64{1: {10, 100}, 2: {20, 200}}
	if len(failArgs) != 2 {
		t.Fatalf("fail calls = %v, want exactly 2", failArgs)
	}
	for _, args := range failArgs {
		want, ok := wantArgs[args[2]]
		if !ok {
			t.Fatalf("reaper touched unexpected task %d", args[2])
		}
		if args[0] != want[0] || args[1] != want[1] {
			t.Fatalf("task %d written with tenant/user %d/%d, want %d/%d", args[2], args[0], args[1], want[0], want[1])
		}
	}

	// 事件同样带归属，且落在正确的任务上。
	if len(events) != 2 {
		t.Fatalf("events = %+v, want 2", events)
	}
	for _, event := range events {
		want := wantArgs[event.TaskID]
		if event.EventType != agenttasks.EventFailed || event.TenantID != want[0] || event.UserID != want[1] {
			t.Fatalf("unexpected reaper event: %+v", event)
		}
	}
}

// 真正的 runner 抢先收尾时，reaper 必须让它的结果说话。
func TestAgentTaskReaperSkipsTasksFinishedByTheRunner(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	store := &fakeStaleAgentTaskStore{
		tasks:   []mysqlstore.AgentTask{{ID: 7, TenantID: 10, UserID: 100, Status: agenttasks.StatusRunning, StartedAt: now.Add(-time.Hour)}},
		failErr: map[uint64]error{7: mysqlstore.ErrNotFound},
	}
	reaper := &agentTaskReaper{store: store, timeout: 20 * time.Minute, interval: time.Hour, batch: 50, now: func() time.Time { return now }}
	if reaped := reaper.sweep(context.Background()); reaped != 0 {
		t.Fatalf("reaped = %d, want 0 when the runner already finished the task", reaped)
	}
	if _, _, events, _ := store.snapshot(); len(events) != 0 {
		t.Fatalf("no event should be appended for a task the runner finished: %+v", events)
	}
}

func TestAgentTaskReaperSurvivesListErrors(t *testing.T) {
	store := &fakeStaleAgentTaskStore{listErr: errors.New("db down")}
	reaper := &agentTaskReaper{store: store, timeout: time.Minute, interval: time.Hour, batch: 10, now: time.Now}
	if reaped := reaper.sweep(context.Background()); reaped != 0 {
		t.Fatalf("reaped = %d, want 0", reaped)
	}
}

// reaper 必须能被 context 取消并干净收尾，不引入新的「进程退不掉」来源。
func TestStartAgentTaskReaperStopsOnContextCancel(t *testing.T) {
	if stop := startAgentTaskReaper(context.Background(), Options{}); stop == nil {
		t.Fatal("stop must never be nil")
	} else {
		stop() // 无 store 时是 no-op，不能阻塞。
	}

	store := &fakeStaleAgentTaskStore{}
	ctx, cancel := context.WithCancel(context.Background())
	stop := startAgentTaskReaper(ctx, Options{AgentTaskReaperStore: store, AgentTaskRunTimeout: time.Minute})
	cancel()
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reaper did not stop after context cancel")
	}
}

func TestAgentTaskReaperIntervalClamped(t *testing.T) {
	for _, tc := range []struct {
		timeout time.Duration
		want    time.Duration
	}{
		{timeout: 20 * time.Minute, want: 5 * time.Minute},
		{timeout: time.Minute, want: agentTaskReaperMinInterval},
		{timeout: 10 * time.Hour, want: agentTaskReaperMaxInterval},
	} {
		if got := agentTaskReaperInterval(tc.timeout); got != tc.want {
			t.Fatalf("interval(%s) = %s, want %s", tc.timeout, got, tc.want)
		}
	}
}

// goSafe 是所有脱离请求的后台 goroutine 的公共兜底。
func TestGoSafeRecoversAndRecordsPanic(t *testing.T) {
	var captured []telemetry.Event
	var mu sync.Mutex
	emitter := telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, event)
		return nil
	}))
	ctx := telemetry.WithEmitter(context.Background(), emitter)

	ran := make(chan struct{})
	goSafe(ctx, "server.testGoroutine", map[string]any{"session_id": uint64(9)}, func() {
		defer close(ran)
		panic("background boom")
	})
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never ran")
	}

	waitForTestCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(captured) > 0
	})
	mu.Lock()
	defer mu.Unlock()
	event := captured[0]
	if event.Name != "server.background.panic" || event.Source != "server.testGoroutine" || event.Status != telemetry.StatusError {
		t.Fatalf("unexpected event: %+v", event)
	}
	if !strings.Contains(event.Error, "background boom") {
		t.Fatalf("event error = %q, want the panic value", event.Error)
	}
	if event.Properties["session_id"] != uint64(9) {
		t.Fatalf("caller properties should be preserved: %+v", event.Properties)
	}
}

// stop 必须在父 ctx 仍然存活时也能返回。Run 里 reaper 是 `defer start(...)()`，
// 而 Run 有不经过 ctx 取消的返回路径（Serve 因 listener 错误返回时 ctx 仍然活着），
// 那条路径上一个只等 ctx.Done() 的 stop 会让 Run 永久挂死。
func TestStartAgentTaskReaperStopReturnsWithoutParentCancel(t *testing.T) {
	store := &fakeStaleAgentTaskStore{}
	stop := startAgentTaskReaper(context.Background(), Options{AgentTaskReaperStore: store, AgentTaskRunTimeout: time.Minute})
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop blocked while the parent context was still live")
	}
}

// Run 必须真的启动 stale task reaper。这条 wiring 在 server.go 里只是一行 defer，
// 而 reaper 的其它测试都直接调 startAgentTaskReaper —— 删掉那一行不会让任何测试变红。
// AUDIT-P0-09 的 graceful shutdown 要重写紧挨着的那几行，本测试把「Run 启动了 reaper」
// 固化成不变量，让那次重构不可能把它悄悄弄丢。
// （go test 下 executable 以 .test 结尾，schedulerDaemonDisabled 为真，不会 fork daemon。）
func TestRunStartsAgentTaskReaper(t *testing.T) {
	store := &fakeStaleAgentTaskStore{
		tasks: []mysqlstore.AgentTask{
			{ID: 1, TenantID: 10, UserID: 100, Status: agenttasks.StatusRunning, StartedAt: time.Now().Add(-2 * time.Hour)},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{
			Host:                 "127.0.0.1",
			Port:                 0,
			AgentTaskReaperStore: store,
			AgentTaskRunTimeout:  time.Minute,
		}, nil)
	}()

	deadline := time.After(5 * time.Second)
	for {
		if _, _, _, failArgs := store.snapshot(); len(failArgs) > 0 {
			break
		}
		select {
		case err := <-errCh:
			t.Fatalf("Run returned before the reaper swept: %v", err)
		case <-deadline:
			t.Fatal("Run did not start the stale task reaper")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}
