package server

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

// StaleAgentTaskStore 是 stale task reaper 需要的最小存储能力。
// 列表返回的每一行都带 tenant_id / user_id 归属，写回按 (tenant_id, user_id, id)
// 精确匹配，reaper 因此永远只改自己读到的那一行，不可能跨租户。
type StaleAgentTaskStore interface {
	// ListStaleRunningAgentTasks 返回 started_at 早于 startedBefore 且仍停在
	// running 的任务，跨租户扫描但按行携带归属。
	ListStaleRunningAgentTasks(ctx context.Context, startedBefore time.Time, limit int) ([]mysqlstore.AgentTask, error)
	// FailStaleAgentTask 仅在任务仍为 running 时置 failed；已被真正的 runner 收尾
	// 的任务返回 mysqlstore.ErrNotFound，避免覆盖正常结果。
	FailStaleAgentTask(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) error
	AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error)
}

type staleAgentTaskEventStore interface {
	AppendAgentTaskEventForTask(context.Context, mysqlstore.AgentTask, agenttasks.EventInput) (uint64, error)
}

const (
	// agentTaskReaperBatch 单轮最多回收多少条，避免一次扫描打满 DB。
	agentTaskReaperBatch = 200
	// agentTaskReaperMinInterval / MaxInterval 约束由 run timeout 推导出的巡检周期。
	agentTaskReaperMinInterval = time.Minute
	agentTaskReaperMaxInterval = 10 * time.Minute
)

type agentTaskReaper struct {
	store    StaleAgentTaskStore
	timeout  time.Duration
	interval time.Duration
	batch    int
	now      func() time.Time
}

func newAgentTaskReaper(opts Options) *agentTaskReaper {
	if opts.AgentTaskReaperStore == nil {
		return nil
	}
	store := opts.AgentTaskReaperStore
	if opts.SessionEvents != nil {
		store = routedStaleAgentTaskStore{base: store, events: opts.SessionEvents}
	}
	timeout := agentTaskRunTimeout(opts)
	return &agentTaskReaper{
		store:    store,
		timeout:  timeout,
		interval: agentTaskReaperInterval(timeout),
		batch:    agentTaskReaperBatch,
		now:      time.Now,
	}
}

func agentTaskReaperInterval(timeout time.Duration) time.Duration {
	interval := timeout / 4
	if interval < agentTaskReaperMinInterval {
		return agentTaskReaperMinInterval
	}
	if interval > agentTaskReaperMaxInterval {
		return agentTaskReaperMaxInterval
	}
	return interval
}

// startAgentTaskReaper 启动 reaper 并返回一个「等它干净退出」的函数。
// 进程重启会把 detached runner 腰斩，DB 里的任务永久停在 running；启动时先扫一轮
// 把这些孤儿收掉，之后按周期兜住运行期超时。返回的 stop 必须被调用（配合 graceful
// shutdown），否则 Run 返回后 goroutine 还在跑。
//
// reaper 跑在自己派生的 context 上，stop 先 cancel 再等。不能只等父 ctx：Run 里
// 这是 `defer startAgentTaskReaper(ctx, opts)()`，而 Run 有不经过 ctx 取消的返回
// 路径（Serve 因 listener 错误返回时父 ctx 仍然活着），那条路径上一个只等
// ctx.Done() 的 stop 会让 Run 永久挂死。
func startAgentTaskReaper(ctx context.Context, opts Options) (stop func()) {
	reaper := newAgentTaskReaper(opts)
	if reaper == nil {
		return func() {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	goSafe(runCtx, "server.agentTaskReaper", nil, func() {
		defer close(done)
		reaper.run(runCtx)
	})
	return func() {
		cancel()
		<-done
	}
}

// run 立刻扫一轮（回收上次进程留下的孤儿），随后按 interval 巡检，直到 ctx 取消。
func (rp *agentTaskReaper) run(ctx context.Context) {
	rp.sweep(ctx)
	ticker := time.NewTicker(rp.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rp.sweep(ctx)
		}
	}
}

// sweep 扫一轮并返回实际回收条数。
func (rp *agentTaskReaper) sweep(ctx context.Context) int {
	if ctx.Err() != nil {
		return 0
	}
	cutoff := rp.now().UTC().Add(-rp.timeout)
	tasks, err := rp.store.ListStaleRunningAgentTasks(ctx, cutoff, rp.batch)
	if err != nil {
		observability.Error(ctx, nil, "agent_task.reaper_error", "server.agentTaskReaper.sweep", "list stale agent tasks failed", "error", err)
		return 0
	}
	reaped := 0
	for _, task := range tasks {
		if ctx.Err() != nil {
			return reaped
		}
		if rp.reap(ctx, task) {
			reaped++
		}
	}
	if reaped > 0 {
		observability.Info(ctx, nil, "agent_task.reaped", "server.agentTaskReaper.sweep", "failed stale running agent tasks",
			"count", reaped, "cutoff", cutoff.Format(time.RFC3339), "run_timeout", rp.timeout.String())
	}
	return reaped
}

func (rp *agentTaskReaper) reap(ctx context.Context, task mysqlstore.AgentTask) bool {
	payload := staleAgentTaskResultJSON(task, rp.timeout, rp.now().UTC())
	// 归属直接来自读到的那一行，reaper 自己不构造 tenant/user。
	if err := rp.store.FailStaleAgentTask(ctx, task.TenantID, task.UserID, task.ID, payload); err != nil {
		if errors.Is(err, mysqlstore.ErrNotFound) {
			// 真正的 runner 已经收尾，让它的结果说话。
			return false
		}
		observability.Error(ctx, nil, "agent_task.reaper_error", "server.agentTaskReaper.reap", "fail stale agent task failed",
			"agent_task_id", task.ID, "error", err)
		return false
	}
	input := agenttasks.EventInput{
		TenantID:    task.TenantID,
		UserID:      task.UserID,
		TaskID:      task.ID,
		EventType:   agenttasks.EventFailed,
		PayloadJSON: payload,
		TraceID:     task.TraceID,
	}
	var appendErr error
	if routed, ok := rp.store.(staleAgentTaskEventStore); ok {
		_, appendErr = routed.AppendAgentTaskEventForTask(ctx, task, input)
	} else {
		_, appendErr = rp.store.AppendAgentTaskEvent(ctx, input)
	}
	if appendErr != nil {
		observability.Error(ctx, nil, "agent_task.reaper_error", "server.agentTaskReaper.reap", "append stale agent task event failed",
			"agent_task_id", task.ID, "error", appendErr)
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "agent.run.reaped",
		Category:     telemetry.CategoryAgent,
		Source:       "server.agentTaskReaper.reap",
		Status:       telemetry.StatusError,
		TraceID:      task.TraceID,
		TenantID:     task.TenantID,
		UserID:       task.UserID,
		Model:        task.Model,
		ResourceType: "agent_task",
		ResourceID:   strconv.FormatUint(task.ID, 10),
		Error:        staleAgentTaskReason,
		Properties: map[string]any{
			"agent_name":  task.AgentName,
			"run_timeout": rp.timeout.String(),
			"started_at":  task.StartedAt.UTC().Format(time.RFC3339),
		},
	})
	return true
}

// staleAgentTaskReason 是写进 DB 和遥测的失败原因。措辞必须明说是进程重启/超时
// 导致的失联，不能伪装成正常完成，否则调用方会把空结果当成答案。
const staleAgentTaskReason = "agent task runner was lost (process restart or exceeded run timeout); status reclaimed by stale task reaper"

func staleAgentTaskResultJSON(task mysqlstore.AgentTask, timeout time.Duration, reapedAt time.Time) string {
	content := "Failed: " + staleAgentTaskReason
	payload := map[string]any{
		"source":      "stale_task_reaper",
		"status":      agenttasks.StatusFailed,
		"error":       staleAgentTaskReason,
		"reason":      "runner_lost",
		"stale":       true,
		"reaped_at":   reapedAt.Format(time.RFC3339),
		"run_timeout": timeout.String(),
		"content":     content,
	}
	if !task.StartedAt.IsZero() {
		payload["started_at"] = task.StartedAt.UTC().Format(time.RFC3339)
		payload["duration_ms"] = reapedAt.Sub(task.StartedAt.UTC()).Milliseconds()
	}
	if task.TraceID != "" {
		payload["trace_id"] = task.TraceID
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return `{"source":"stale_task_reaper","status":"failed","error":"` + staleAgentTaskReason + `"}`
	}
	return string(data)
}
