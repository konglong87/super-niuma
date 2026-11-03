package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type pendingInputCoordinator struct {
	ctx     context.Context
	opts    Options
	queryFn QueryFunc
	owner   string
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
}

func newPendingInputCoordinator(ctx context.Context, opts Options, queryFn QueryFunc) *pendingInputCoordinator {
	if ctx == nil {
		ctx = context.Background()
	}
	return &pendingInputCoordinator{ctx: ctx, opts: opts, queryFn: queryFn, owner: newWebAgentTraceID(), locks: make(map[string]*sync.Mutex)}
}

func (c *pendingInputCoordinator) trigger(valueCtx context.Context, scope pendinginput.Scope) {
	if c == nil || c.opts.PendingInputQueue == nil || c.opts.TenantService == nil || !pendingInputScopeValid(scope) {
		return
	}
	key := fmt.Sprintf("%d:%d:%s", scope.TenantID, scope.UserID, scope.SessionID)
	c.mu.Lock()
	lock := c.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		c.locks[key] = lock
	}
	c.mu.Unlock()
	runCtx, cancel := pendingInputRunContext(c.ctx, valueCtx)
	goSafe(runCtx, "server.pendingInputCoordinator.trigger", map[string]any{"tenant_id": scope.TenantID, "user_id": scope.UserID, "session_id": scope.SessionID}, func() {
		defer cancel()
		lock.Lock()
		defer lock.Unlock()
		leaseQueue, ok := c.opts.PendingInputQueue.(pendinginput.ConsumerLeaseQueue)
		if ok {
			acquired, err := leaseQueue.AcquireConsumerLease(runCtx, scope, c.owner, agentTaskRunTimeout(c.opts)+time.Minute)
			if err != nil || !acquired {
				return
			}
			defer leaseQueue.ReleaseConsumerLease(context.WithoutCancel(runCtx), scope, c.owner)
		}
		c.drain(runCtx, scope)
	})
}

func pendingInputRunContext(lifecycleCtx, valueCtx context.Context) (context.Context, context.CancelFunc) {
	if lifecycleCtx == nil {
		lifecycleCtx = context.Background()
	}
	if valueCtx == nil {
		valueCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(valueCtx))
	stop := context.AfterFunc(lifecycleCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func pendingInputScopeValid(scope pendinginput.Scope) bool {
	return scope.TenantID > 0 && scope.UserID > 0 && strings.TrimSpace(scope.SessionID) != ""
}

func (c *pendingInputCoordinator) drain(ctx context.Context, scope pendinginput.Scope) {
	for {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		if leaseQueue, ok := c.opts.PendingInputQueue.(pendinginput.ConsumerLeaseQueue); ok {
			renewed, err := leaseQueue.AcquireConsumerLease(ctx, scope, c.owner, agentTaskRunTimeout(c.opts)+time.Minute)
			if err != nil || !renewed {
				return
			}
		}
		enabled, err := c.opts.PendingInputQueue.QueueEnabled(ctx, scope)
		if err != nil || !enabled || c.sessionHasRunningTask(ctx, scope) {
			return
		}
		item, ok, err := c.opts.PendingInputQueue.ClaimNext(ctx, scope)
		if err != nil || !ok {
			return
		}
		parent, err := c.opts.TenantService.GetAgentTask(ctx, scope.BaseTaskID)
		if err != nil {
			_ = c.opts.PendingInputQueue.MarkFailed(ctx, item.ID, "parent_task_not_found")
			appendPendingInputEventContextWithOptions(ctx, c.opts, scope.BaseTaskID, agenttasks.EventInputFailed, pendingInputEventValues(item, pendinginput.StatusFailed, 0, "parent_task_not_found"))
			return
		}
		metadata := parseAgentTaskJSONMap(parent.MetadataJSON)
		metadata["continuation_of_task_id"] = parent.ID
		metadata["pending_input_id"] = item.ID
		metadata["pending_input_attempt"] = item.Attempt
		metadata["pending_input_base_task_id"] = scope.BaseTaskID
		traceID := firstNonEmptyString(parent.TraceID, newWebAgentTraceID())
		metadataJSON, _ := json.Marshal(metadata)
		childID, err := c.opts.TenantService.CreateAgentTask(ctx, agenttasks.TaskInput{
			ParentSessionID: parent.ParentSessionID,
			AgentName:       firstNonEmptyString(parent.AgentName, "web-agent"),
			Description:     firstNonEmptyString(parent.Description, "Web Agent continuation"),
			Prompt:          composePendingInputContent(item),
			Status:          agenttasks.StatusRunning,
			Model:           parent.Model,
			MetadataJSON:    string(metadataJSON),
			TraceID:         traceID,
		})
		if err != nil {
			_ = c.opts.PendingInputQueue.MarkFailed(ctx, item.ID, "continuation_create_failed")
			appendPendingInputEventContextWithOptions(ctx, c.opts, scope.BaseTaskID, agenttasks.EventInputFailed, pendingInputEventValues(item, pendinginput.StatusFailed, 0, "continuation_create_failed"))
			return
		}
		child := mysqlstore.AgentTask{ID: childID, TenantID: parent.TenantID, UserID: parent.UserID, ParentSessionID: parent.ParentSessionID, AgentName: parent.AgentName, Description: parent.Description, Status: agenttasks.StatusRunning, Model: parent.Model, MetadataJSON: string(metadataJSON), TraceID: traceID}
		if _, err := appendAgentTaskEvent(ctx, c.opts, child, agenttasks.EventInput{TaskID: childID, EventType: agenttasks.EventStarted, PayloadJSON: agentTaskEventPayload(map[string]any{"source": "pending-input-queue", "pending_input_id": item.ID}), TraceID: traceID}); err != nil {
			_ = c.opts.TenantService.FinishAgentTask(context.WithoutCancel(ctx), childID, agenttasks.StatusFailed, agentTaskEventPayload(map[string]any{"source": "pending-input-queue", "error_code": "continuation_event_failed"}))
			_ = c.opts.PendingInputQueue.MarkFailed(ctx, item.ID, "continuation_event_failed")
			appendPendingInputEventContextWithOptions(ctx, c.opts, scope.BaseTaskID, agenttasks.EventInputFailed, pendingInputEventValues(item, pendinginput.StatusFailed, childID, "continuation_event_failed"))
			return
		}
		message := agenttasks.MessageInput{TaskID: childID, FromAgent: "webui", Content: composePendingInputContent(item), TraceID: traceID, Attachments: item.Attachments}
		messagePayload, err := json.Marshal(message)
		if err != nil {
			_ = c.opts.TenantService.FinishAgentTask(context.WithoutCancel(ctx), childID, agenttasks.StatusFailed, agentTaskEventPayload(map[string]any{"source": "pending-input-queue", "error_code": "continuation_message_failed"}))
			_ = c.opts.PendingInputQueue.MarkFailed(ctx, item.ID, "continuation_message_failed")
			appendPendingInputEventContextWithOptions(ctx, c.opts, scope.BaseTaskID, agenttasks.EventInputFailed, pendingInputEventValues(item, pendinginput.StatusFailed, childID, "continuation_message_failed"))
			return
		}
		if _, err := appendAgentTaskEvent(ctx, c.opts, child, agenttasks.EventInput{TaskID: childID, EventType: agenttasks.EventMessage, PayloadJSON: string(messagePayload), TraceID: traceID}); err != nil {
			_ = c.opts.TenantService.FinishAgentTask(context.WithoutCancel(ctx), childID, agenttasks.StatusFailed, agentTaskEventPayload(map[string]any{"source": "pending-input-queue", "error_code": "continuation_message_failed"}))
			_ = c.opts.PendingInputQueue.MarkFailed(ctx, item.ID, "continuation_message_failed")
			appendPendingInputEventContextWithOptions(ctx, c.opts, scope.BaseTaskID, agenttasks.EventInputFailed, pendingInputEventValues(item, pendinginput.StatusFailed, childID, "continuation_message_failed"))
			return
		}
		appendPendingInputEventContextWithOptions(ctx, c.opts, scope.BaseTaskID, agenttasks.EventInputRunning, pendingInputEventValues(item, pendinginput.StatusRunning, childID, ""))
		taskCtx, cancel := context.WithTimeout(ctx, agentTaskRunTimeout(c.opts))
		result, err := c.runTask(taskCtx, child, message)
		cancel()
		if err != nil || result.Status != agenttasks.StatusCompleted {
			code := strings.TrimSpace(result.Status)
			if code == "" {
				code = "continuation_run_failed"
			}
			_ = c.opts.PendingInputQueue.MarkFailed(context.WithoutCancel(ctx), item.ID, code)
			appendPendingInputEventContextWithOptions(context.WithoutCancel(ctx), c.opts, scope.BaseTaskID, agenttasks.EventInputFailed, pendingInputEventValues(item, pendinginput.StatusFailed, childID, code))
			return
		}
		if err := c.opts.PendingInputQueue.MarkSent(context.WithoutCancel(ctx), item.ID, childID); err != nil {
			return
		}
		appendPendingInputEventContextWithOptions(context.WithoutCancel(ctx), c.opts, scope.BaseTaskID, agenttasks.EventInputSent, pendingInputEventValues(item, pendinginput.StatusSent, childID, ""))
	}
}

func (c *pendingInputCoordinator) runTask(ctx context.Context, task mysqlstore.AgentTask, message agenttasks.MessageInput) (result agentTaskRunResult, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			reason, stack := describeBackgroundPanic(rec)
			observability.Error(ctx, nil, "agent.run.panic", "server.pendingInputCoordinator.runTask", "pending input runner panicked", "agent_task_id", task.ID, "panic", reason, "stack", stack)
			emitBackgroundPanic(ctx, "server.pendingInputCoordinator.runTask", reason, stack, map[string]any{"agent_task_id": task.ID, "trace_id": message.TraceID})
			persistCtx := context.WithoutCancel(ctx)
			payload := agentTaskEventPayload(map[string]any{"source": "pending-input-queue", "trace_id": message.TraceID, "error": reason, "panic": true})
			_, _ = appendAgentTaskEvent(persistCtx, c.opts, task, agenttasks.EventInput{TaskID: task.ID, EventType: agenttasks.EventFailed, PayloadJSON: payload, TraceID: message.TraceID})
			_ = c.opts.TenantService.FinishAgentTask(persistCtx, task.ID, agenttasks.StatusFailed, payload)
			result = agentTaskRunResult{Status: "continuation_run_panic"}
			err = fmt.Errorf("pending input runner panic: %s", reason)
		}
	}()
	return runAgentTaskMessage(ctx, c.opts, c.queryFn, task, message)
}

func pendingInputEventValues(item pendinginput.PendingInput, status pendinginput.Status, taskID uint64, errorCode string) map[string]any {
	values := map[string]any{"input_id": item.ID, "status": status, "sequence": item.Sequence, "attempt": item.Attempt}
	if taskID > 0 {
		values["dispatched_task_id"] = taskID
	}
	if strings.TrimSpace(errorCode) != "" {
		values["error_code"] = strings.TrimSpace(errorCode)
	}
	return values
}

func (c *pendingInputCoordinator) sessionHasRunningTask(ctx context.Context, scope pendinginput.Scope) bool {
	if c.opts.TenantService == nil {
		return false
	}
	items, err := c.opts.TenantService.ListAgentTasks(ctx, 200)
	if err != nil {
		return true
	}
	parentID, _ := strconv.ParseUint(strings.TrimSpace(scope.SessionID), 10, 64)
	for _, item := range items {
		if item.Status != agenttasks.StatusRunning && item.Status != agenttasks.StatusReady {
			continue
		}
		if parentID > 0 && item.ParentSessionID == parentID {
			return true
		}
		if parentID == 0 && item.ID == scope.BaseTaskID {
			return true
		}
	}
	return false
}

func pendingInputScopeForTask(task mysqlstore.AgentTask) pendinginput.Scope {
	metadata := parseAgentTaskJSONMap(task.MetadataJSON)
	baseTaskID := task.ID
	if raw, ok := metadata["pending_input_base_task_id"]; ok {
		if value := pendingInputUint64Value(raw); value > 0 {
			baseTaskID = value
		}
	} else if raw, ok := metadata["continuation_of_task_id"]; ok {
		if value := pendingInputUint64Value(raw); value > 0 {
			baseTaskID = value
		}
	}
	sessionID := strconv.FormatUint(task.ParentSessionID, 10)
	if task.ParentSessionID == 0 {
		sessionID = "task:" + strconv.FormatUint(task.ID, 10)
	}
	return pendinginput.Scope{TenantID: task.TenantID, UserID: task.UserID, SessionID: sessionID, BaseTaskID: baseTaskID}
}

func pendingInputUint64Value(value any) uint64 {
	switch typed := value.(type) {
	case float64:
		if typed > 0 && typed == float64(uint64(typed)) {
			return uint64(typed)
		}
	case json.Number:
		parsed, _ := strconv.ParseUint(string(typed), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseUint(strings.TrimSpace(typed), 10, 64)
		return parsed
	}
	return 0
}
