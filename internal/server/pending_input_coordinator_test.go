package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestPendingInputCoordinatorDrainsInCurrentOrder(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	b, _ := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "b", Content: "B"})
	_, _ = queue.MoveUp(context.Background(), b.ID)
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, ParentSessionID: 5, Status: agenttasks.StatusCompleted, AgentName: "web-agent", Model: "model-a"}}}
	var mu sync.Mutex
	var prompts []string
	coordinator := newPendingInputCoordinator(context.Background(), Options{TenantService: fake, PendingInputQueue: queue}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		mu.Lock()
		prompts = append(prompts, req.Prompt)
		mu.Unlock()
		return query.Result{Response: "ok", Model: req.Model, Turns: 1}, nil
	})
	coordinator.trigger(context.Background(), scope)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(prompts) == 2
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 || prompts[0] != "B" || prompts[1] != "A" {
		t.Fatalf("prompts = %#v; want B,A", prompts)
	}
	events, err := fake.ListAgentTaskEvents(context.Background(), 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sawMessage bool
	for _, event := range events {
		if event.EventType == agenttasks.EventMessage && strings.Contains(event.PayloadJSON, `"content":"B"`) && strings.Contains(event.PayloadJSON, `"from_agent":"webui"`) {
			sawMessage = true
		}
	}
	if !sawMessage {
		t.Fatalf("first continuation missing message event: %+v", events)
	}
}

func TestPendingInputHandlerStartsCoordinatorForIdleTask(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusCompleted, AgentName: "web-agent", Model: "model-a"}}}
	ran := make(chan string, 1)
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, PendingInputQueue: queue}, func(ctx context.Context, req QueryRequest) (query.Result, error) {
		if got := observability.TenantKey(ctx); got != "yutang" {
			t.Errorf("tenant key=%q want yutang", got)
		}
		if got := observability.UserID(ctx); got != "user-test" {
			t.Errorf("user id=%q want user-test", got)
		}
		ran <- req.Prompt
		return query.Result{Response: "ok", Model: req.Model, Turns: 1}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs", strings.NewReader(`{"client_input_id":"a","content":"run me"}`))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case prompt := <-ran:
		if prompt != "run me" {
			t.Fatalf("prompt=%q", prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle pending input was not consumed by server coordinator")
	}
}

func TestPendingInputCoordinatorDoesNotDrainWhenQueueDisabled(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
	if _, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "a", Content: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetQueueEnabled(context.Background(), scope, false); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusCompleted}}}
	ran := make(chan struct{}, 1)
	coordinator := newPendingInputCoordinator(context.Background(), Options{TenantService: fake, PendingInputQueue: queue}, func(context.Context, QueryRequest) (query.Result, error) {
		ran <- struct{}{}
		return query.Result{}, nil
	})
	coordinator.trigger(context.Background(), scope)
	select {
	case <-ran:
		t.Fatal("disabled queue was drained")
	case <-time.After(100 * time.Millisecond):
	}
	items, err := queue.List(context.Background(), scope)
	if err != nil || len(items) != 1 || items[0].Status != pendinginput.StatusQueued {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestPendingInputRetryAndReenableWakeIdleCoordinator(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(context.Context, *pendinginput.MemoryQueue, pendinginput.Scope) string
		path    func(string) string
		body    string
	}{
		{
			name: "retry",
			prepare: func(ctx context.Context, queue *pendinginput.MemoryQueue, scope pendinginput.Scope) string {
				item, _ := queue.Add(ctx, pendinginput.NewInput{Scope: scope, ClientInputID: "retry", Content: "retry me"})
				_, _, _ = queue.ClaimNext(ctx, scope)
				_ = queue.MarkFailed(ctx, item.ID, "failed")
				return item.ID
			},
			path: func(id string) string { return "/tenant/agent-tasks/41/pending-inputs/" + id + "/retry" },
		},
		{
			name: "reenable",
			prepare: func(ctx context.Context, queue *pendinginput.MemoryQueue, scope pendinginput.Scope) string {
				_, _ = queue.Add(ctx, pendinginput.NewInput{Scope: scope, ClientInputID: "reenable", Content: "resume me"})
				_ = queue.SetQueueEnabled(ctx, scope, false)
				return ""
			},
			path: func(string) string { return "/tenant/agent-tasks/41/pending-input-settings" },
			body: `{"enabled":true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			queue := pendinginput.NewMemoryQueue()
			scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
			inputID := tt.prepare(ctx, queue, scope)
			fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusCompleted, AgentName: "web-agent"}}}
			ran := make(chan string, 1)
			handler := NewHandler(Options{AuthToken: "token", TenantService: fake, PendingInputQueue: queue}, func(_ context.Context, req QueryRequest) (query.Result, error) {
				ran <- req.Prompt
				return query.Result{Response: "done"}, nil
			})
			req := httptest.NewRequest(http.MethodPost, tt.path(inputID), strings.NewReader(tt.body))
			if tt.body != "" {
				req.Method = http.MethodPatch
			}
			req.Header.Set("Authorization", "Bearer token")
			req.Header.Set("X-Tenant-Key", "yutang")
			req.Header.Set("X-User-Id", "user-test")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			select {
			case <-ran:
			case <-time.After(time.Second):
				t.Fatal("idle coordinator was not woken")
			}
		})
	}
}

func TestPendingInputCoordinatorMarksFailedRunRetryable(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
	item, _ := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "b", Content: "B"})
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusCompleted, AgentName: "web-agent"}}}
	var runs atomic.Int32
	coordinator := newPendingInputCoordinator(context.Background(), Options{TenantService: fake, PendingInputQueue: queue}, func(context.Context, QueryRequest) (query.Result, error) {
		runs.Add(1)
		return query.Result{}, errors.New("runner failed")
	})
	coordinator.trigger(context.Background(), scope)
	waitForTestCondition(t, func() bool {
		items, _ := queue.List(context.Background(), scope)
		return len(items) == 2 && items[0].Status == pendinginput.StatusFailed
	})
	time.Sleep(100 * time.Millisecond)
	items, _ := queue.List(context.Background(), scope)
	if items[0].ID != item.ID || items[0].ErrorCode != agenttasks.StatusFailed {
		t.Fatalf("failed item=%+v", items[0])
	}
	events, _ := fake.ListAgentTaskEvents(context.Background(), 41, 20)
	var sawFailed bool
	for _, event := range events {
		if event.EventType == agenttasks.EventInputFailed && strings.Contains(event.PayloadJSON, `"input_id":"`+item.ID+`"`) && !strings.Contains(event.PayloadJSON, "runner failed") {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Fatalf("missing sanitized input_failed event: %+v", events)
	}
	if runs.Load() != 1 || items[1].Status != pendinginput.StatusQueued {
		t.Fatalf("failed run did not pause queue: runs=%d items=%+v", runs.Load(), items)
	}
}

func TestPendingInputCoordinatorRecoversPanickedRun(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
	item, _ := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "panic", Content: "panic"})
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusCompleted, AgentName: "web-agent"}}}
	coordinator := newPendingInputCoordinator(context.Background(), Options{TenantService: fake, PendingInputQueue: queue}, func(context.Context, QueryRequest) (query.Result, error) {
		panic("runner panic")
	})
	coordinator.trigger(context.Background(), scope)
	waitForTestCondition(t, func() bool {
		items, _ := queue.List(context.Background(), scope)
		return len(items) == 1 && items[0].Status == pendinginput.StatusFailed
	})
	items, _ := queue.List(context.Background(), scope)
	if items[0].ID != item.ID || items[0].ErrorCode != "continuation_run_panic" {
		t.Fatalf("panicked item=%+v", items[0])
	}
	status, result := fake.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusFailed || !strings.Contains(result, "runner panic") {
		t.Fatalf("child status=%q result=%s", status, result)
	}
}

func TestPendingInputCoordinatorsSerializeAcrossInstances(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "b", Content: "B"})
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusCompleted, AgentName: "web-agent"}}}
	block := make(chan struct{})
	started := make(chan struct{}, 2)
	var active atomic.Int32
	var maxActive atomic.Int32
	run := func(context.Context, QueryRequest) (query.Result, error) {
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-block
		active.Add(-1)
		return query.Result{Response: "done"}, nil
	}
	first := newPendingInputCoordinator(context.Background(), Options{TenantService: fake, PendingInputQueue: queue}, run)
	second := newPendingInputCoordinator(context.Background(), Options{TenantService: fake, PendingInputQueue: queue}, run)
	first.trigger(context.Background(), scope)
	second.trigger(context.Background(), scope)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no pending input started")
	}
	select {
	case <-started:
		close(block)
		t.Fatal("two coordinator instances ran the same session concurrently")
	case <-time.After(100 * time.Millisecond):
	}
	close(block)
	waitForTestCondition(t, func() bool {
		items, _ := queue.List(context.Background(), scope)
		return len(items) == 0
	})
	if maxActive.Load() != 1 {
		t.Fatalf("max active=%d want 1", maxActive.Load())
	}
}

func TestRunAgentTaskMessageDrainsPendingInputsAfterEveryTerminalStatus(t *testing.T) {
	tests := []struct {
		name       string
		prepareCtx func() context.Context
		run        QueryFunc
		wantStatus string
	}{
		{
			name:       "completed",
			prepareCtx: context.Background,
			run:        func(context.Context, QueryRequest) (query.Result, error) { return query.Result{Response: "done"}, nil },
			wantStatus: agenttasks.StatusCompleted,
		},
		{
			name:       "failed",
			prepareCtx: context.Background,
			run: func(context.Context, QueryRequest) (query.Result, error) {
				return query.Result{}, errors.New("runner failed")
			},
			wantStatus: agenttasks.StatusFailed,
		},
		{
			name:       "timeout",
			prepareCtx: context.Background,
			run: func(context.Context, QueryRequest) (query.Result, error) {
				return query.Result{}, context.DeadlineExceeded
			},
			wantStatus: agenttasks.StatusTimeout,
		},
		{
			name: "cancelled",
			prepareCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			run:        func(ctx context.Context, _ QueryRequest) (query.Result, error) { return query.Result{}, ctx.Err() },
			wantStatus: agenttasks.StatusCancelled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := pendinginput.NewMemoryQueue()
			task := mysqlstore.AgentTask{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusRunning, AgentName: "web-agent", Model: "model-a", MetadataJSON: `{"cwd":"/repo"}`}
			fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{task}}
			scope := pendingInputScopeForTask(task)
			if _, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "queued", Content: "run after terminal"}); err != nil {
				t.Fatalf("add pending input: %v", err)
			}
			drained := make(chan string, 1)
			opts := Options{TenantService: fake, PendingInputQueue: queue}
			opts.pendingInputCoordinator = newPendingInputCoordinator(context.Background(), opts, func(_ context.Context, req QueryRequest) (query.Result, error) {
				drained <- req.Prompt
				return query.Result{Response: "queued done"}, nil
			})

			result, err := runAgentTaskMessage(tt.prepareCtx(), opts, tt.run, task, agenttasks.MessageInput{TaskID: task.ID, Content: "initial"})
			if err != nil {
				t.Fatalf("run agent task: %v", err)
			}
			if result.Status != tt.wantStatus {
				t.Fatalf("status=%q want %q", result.Status, tt.wantStatus)
			}
			select {
			case prompt := <-drained:
				if prompt != "run after terminal" {
					t.Fatalf("drained prompt=%q", prompt)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatalf("pending input was not drained after %s", tt.wantStatus)
			}
		})
	}
}

func TestPendingInputSideChatIncludesCopiedCandidateInFirstRunContext(t *testing.T) {
	fake := &fakeTenantService{
		agentTasks:      []mysqlstore.AgentTask{{ID: 41, TenantID: 1, UserID: 2, ParentSessionID: 5, Status: agenttasks.StatusRunning, AgentName: "web-agent", MetadataJSON: `{"source":"pending-input-side-chat","cwd":"/repo"}`}},
		agentTaskEvents: []mysqlstore.AgentTaskEvent{{ID: 1, TaskID: 41, EventType: agenttasks.EventMessage, PayloadJSON: `{"task_id":41,"from_agent":"webui","content":"copied candidate"}`}},
	}
	var captured []anthropic.MessageParam
	_, err := runAgentTaskMessage(context.Background(), Options{TenantService: fake}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		captured = req.InitialMessages
		return query.Result{Response: "done"}, nil
	}, fake.agentTasks[0], agenttasks.MessageInput{TaskID: 41, FromAgent: "webui", Content: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0].Role != "user" || len(captured[0].Content) != 1 || captured[0].Content[0].Text != "copied candidate" {
		t.Fatalf("initial messages=%+v", captured)
	}
}
