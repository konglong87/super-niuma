package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// fakeAgentTaskStreamer 按调用序号交出事件批次，并数清每种查询打了多少次。
type fakeAgentTaskStreamer struct {
	batches   map[int][]mysqlstore.AgentTaskEvent
	status    string
	statusAt  int
	listCalls int
	getCalls  int
	listErr   error
	getErr    error
}

func (f *fakeAgentTaskStreamer) ListAgentTaskEventsAfter(_ context.Context, _ uint64, _ uint64, _ int) ([]mysqlstore.AgentTaskEvent, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	batch := f.batches[f.listCalls]
	f.listCalls++
	return batch, nil
}

func (f *fakeAgentTaskStreamer) GetAgentTask(_ context.Context, _ uint64) (mysqlstore.AgentTask, error) {
	if f.getErr != nil {
		return mysqlstore.AgentTask{}, f.getErr
	}
	f.getCalls++
	status := agenttasks.StatusRunning
	if f.status != "" && f.getCalls >= f.statusAt {
		status = f.status
	}
	return mysqlstore.AgentTask{Status: status}, nil
}

// newTestStream 用一个不真睡的 wait 驱动轮询循环：记录每次要睡多久，累计到
// budget 之后就当客户端断开。测试因此完全确定，也不占用真实时间。
func newTestStream(svc agentTaskEventStreamer, budget time.Duration) (*agentTaskStream, *[]time.Duration) {
	var waits []time.Duration
	var simulated time.Duration
	stream := &agentTaskStream{
		svc:   svc,
		write: func(string, any) {},
		flush: func() {},
		limit: 200,
		policy: agentTaskStreamPolicy{
			minInterval: minAgentTaskPollInterval,
			maxInterval: maxAgentTaskPollInterval,
			wait: func(_ context.Context, d time.Duration) bool {
				waits = append(waits, d)
				simulated += d
				return simulated < budget
			},
		},
	}
	return stream, &waits
}

// 原来是固定 250ms 且每 tick 两条查询 —— 10 秒空转 80 次查询，100 条并发流常驻
// 800 QPS(AUDIT-P0-12)。退避之后必须有个明确的上界。
func TestIdleAgentTaskStreamQueryRateHasUpperBound(t *testing.T) {
	svc := &fakeAgentTaskStreamer{}
	stream, waits := newTestStream(svc, 10*time.Second)
	stream.run(context.Background())

	queries := svc.listCalls + svc.getCalls
	if queries > 20 {
		t.Fatalf("%d queries in 10s of idle streaming (waits %v); the old fixed 250ms ticker cost 80", queries, *waits)
	}
	for _, wait := range *waits {
		if wait > maxAgentTaskPollInterval {
			t.Fatalf("wait %s exceeded the %s ceiling", wait, maxAgentTaskPollInterval)
		}
	}
}

func TestAgentTaskStreamBacksOffWhenIdleAndResetsOnEvents(t *testing.T) {
	svc := &fakeAgentTaskStreamer{batches: map[int][]mysqlstore.AgentTaskEvent{
		0: {{ID: 1}},
		4: {{ID: 2}},
	}}
	stream, waits := newTestStream(svc, 5*time.Second)
	stream.run(context.Background())

	want := []time.Duration{
		250 * time.Millisecond,  // 有事件，保持最快节奏
		500 * time.Millisecond,  // 空转开始退避
		1000 * time.Millisecond, //
		2000 * time.Millisecond, // 触顶
		250 * time.Millisecond,  // 又有事件，立刻回到最快
	}
	if len(*waits) < len(want) {
		t.Fatalf("waits = %v, want at least %v", *waits, want)
	}
	for i, expected := range want {
		if (*waits)[i] != expected {
			t.Fatalf("wait[%d] = %s, want %s (full sequence %v)", i, (*waits)[i], expected, *waits)
		}
	}
}

// 事件还在流说明任务显然还活着，那一条状态查询是白花的。
func TestAgentTaskStreamSkipsStatusQueryWhileEventsFlow(t *testing.T) {
	svc := &fakeAgentTaskStreamer{batches: map[int][]mysqlstore.AgentTaskEvent{
		0: {{ID: 1}},
		1: {{ID: 2}},
		2: {{ID: 3}},
	}}
	stream, _ := newTestStream(svc, 750*time.Millisecond)
	stream.run(context.Background())

	if svc.listCalls != 3 {
		t.Fatalf("listCalls = %d, want 3", svc.listCalls)
	}
	if svc.getCalls != 0 {
		t.Fatalf("getCalls = %d; status must not be polled on ticks that produced events", svc.getCalls)
	}
}

func TestAgentTaskStreamStopsOnTerminalStatus(t *testing.T) {
	svc := &fakeAgentTaskStreamer{status: agenttasks.StatusCompleted, statusAt: 1}
	stream, waits := newTestStream(svc, time.Hour)
	stream.run(context.Background())

	if svc.listCalls != 1 || svc.getCalls != 1 {
		t.Fatalf("listCalls = %d getCalls = %d, want 1 and 1", svc.listCalls, svc.getCalls)
	}
	if len(*waits) != 0 {
		t.Fatalf("waits = %v, want the stream to return immediately", *waits)
	}
}

// 收尾时还有积压事件：先把它们推完，再让状态查询终止流。
func TestAgentTaskStreamDrainsTrailingEventsBeforeStopping(t *testing.T) {
	svc := &fakeAgentTaskStreamer{
		batches:  map[int][]mysqlstore.AgentTaskEvent{0: {{ID: 1}}, 1: {{ID: 2}}},
		status:   agenttasks.StatusCompleted,
		statusAt: 1,
	}
	var pushed int
	stream, _ := newTestStream(svc, time.Hour)
	stream.write = func(event string, _ any) {
		if event == "agent_task_event" {
			pushed++
		}
	}
	stream.run(context.Background())

	if pushed != 2 {
		t.Fatalf("pushed %d trailing events, want 2", pushed)
	}
}

func TestAgentTaskStreamStopsWhenClientDisconnects(t *testing.T) {
	svc := &fakeAgentTaskStreamer{}
	stream := &agentTaskStream{
		svc:    svc,
		write:  func(string, any) {},
		flush:  func() {},
		limit:  200,
		policy: agentTaskStreamPolicy{minInterval: time.Millisecond, maxInterval: 2 * time.Millisecond, wait: waitAgentTaskPoll},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream.run(ctx)

	if svc.listCalls != 1 {
		t.Fatalf("listCalls = %d, want the loop to stop after the first wait", svc.listCalls)
	}
}

func TestAgentTaskStreamReportsQueryErrors(t *testing.T) {
	svc := &fakeAgentTaskStreamer{listErr: errors.New("mysql is down")}
	var events []string
	stream, _ := newTestStream(svc, time.Hour)
	stream.write = func(event string, _ any) { events = append(events, event) }
	stream.run(context.Background())

	if len(events) != 1 || events[0] != "error" {
		t.Fatalf("events = %v, want a single error event", events)
	}
}

// A terminal SSE response may close as soon as completed is observed. Late
// optional suggestions remain recoverable through the unchanged after_id REST
// cursor, without replaying the completed event.
func TestAgentTaskStreamCompletedFirstThenRESTReadsLateNextSteps(t *testing.T) {
	firstList := make(chan struct{})
	fake := &terminalStreamTenantService{
		fakeTenantService: &fakeTenantService{
			agentTasks: []mysqlstore.AgentTask{{ID: 41, Status: agenttasks.StatusCompleted}},
			agentTaskEvents: []mysqlstore.AgentTaskEvent{{
				ID: 7, TaskID: 41, EventType: agenttasks.EventCompleted,
				PayloadJSON: `{"response":"reply","next_steps_status":"pending"}`,
			}},
		}, firstList: firstList}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake}, nil)

	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-tasks/41/events/stream?after_id=0&limit=20", nil)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	returned := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(returned)
	}()
	select {
	case <-firstList:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not read the completed event")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not close after completed")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: connected") || !strings.Contains(body, `"event_type":"completed"`) {
		t.Fatalf("body=%s, want connected and completed events", body)
	}
	if strings.Contains(body, `"event_type":"next_steps"`) {
		t.Fatalf("body=%s, late next_steps must not be required in terminal SSE", body)
	}

	fake.mu.Lock()
	fake.agentTaskEvents = append(fake.agentTaskEvents, mysqlstore.AgentTaskEvent{
		ID: 8, TaskID: 41, EventType: agenttasks.EventNextSteps,
		PayloadJSON: `{"source":"runner","suggestions":["late"]}`,
	})
	fake.mu.Unlock()

	req = httptest.NewRequest(http.MethodGet, "/tenant/agent-tasks/41/events?after_id=7&limit=20", nil)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("REST status=%d body=%s", rec.Code, rec.Body.String())
	}
	restBody := rec.Body.String()
	if !strings.Contains(restBody, `"event_type":"next_steps"`) || strings.Contains(restBody, `"event_type":"completed"`) {
		t.Fatalf("REST body=%s, want only the late event after cursor 7", restBody)
	}
}

type terminalStreamTenantService struct {
	*fakeTenantService
	firstList chan struct{}
}

func (s *terminalStreamTenantService) ListAgentTaskEventsAfter(ctx context.Context, taskID, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	items, err := s.fakeTenantService.ListAgentTaskEventsAfter(ctx, taskID, afterID, limit)
	select {
	case <-s.firstList:
	default:
		close(s.firstList)
	}
	return items, err
}
