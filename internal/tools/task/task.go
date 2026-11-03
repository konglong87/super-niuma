package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/agentruntime"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/tools"
)

type MessageStreamer interface {
	StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error)
}

type Tool struct {
	runtime agentruntime.Runtime
}

type taskParam struct {
	Description     string `json:"description"`
	Prompt          string `json:"prompt"`
	SubagentType    string `json:"subagent_type"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	Effort          string `json:"effort,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	MaxTurns        int    `json:"max_turns,omitempty"`
	Priority        int    `json:"priority,omitempty"`
	RetryAttempts   int    `json:"retry_attempts,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
}

const (
	minMultiTaskBatchTimeoutMS = 30000
	minDeepAuditBatchTimeoutMS = 180000

	defaultBatchConcurrency = 4

	// maxBatchConcurrency caps max_concurrency. Each batch slot is a full
	// sub-agent conversation with its own tool execution, so an unbounded value
	// (the schema advertises a plain integer, and max_concurrency: 500 used to be
	// honoured verbatim) multiplies with recursion depth into an exponential
	// fan-out (AUDIT-P0-14). Requests above the cap are clamped rather than
	// rejected — the batch is still exactly the work the caller asked for, only
	// paced — and the clamp is reported in the batch summary so it is never
	// silent. Override with EnvMaxBatchConcurrency.
	defaultMaxBatchConcurrency = 16

	// EnvMaxBatchConcurrency overrides defaultMaxBatchConcurrency.
	EnvMaxBatchConcurrency = "NIUMA_MAX_BATCH_CONCURRENCY"
)

// maxBatchConcurrency resolves the ceiling for max_concurrency.
func maxBatchConcurrency() int {
	raw := strings.TrimSpace(os.Getenv(EnvMaxBatchConcurrency))
	if raw == "" {
		return defaultMaxBatchConcurrency
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return defaultMaxBatchConcurrency
	}
	return value
}

type Option func(*agentruntime.Runtime)

func WithRegistry(registry *tools.Registry) Option {
	return func(runtime *agentruntime.Runtime) {
		runtime.Registry = registry
	}
}

func WithMaxTurns(maxTurns int) Option {
	return func(runtime *agentruntime.Runtime) {
		runtime.MaxTurns = maxTurns
	}
}

func WithMaxTokens(maxTokens int) Option {
	return func(runtime *agentruntime.Runtime) {
		runtime.MaxTokens = maxTokens
	}
}

func WithHooks(hookRunner hooks.Runner) Option {
	return func(runtime *agentruntime.Runtime) {
		runtime.Hooks = hookRunner
	}
}

func WithTaskStore(store agenttasks.Store) Option {
	return func(runtime *agentruntime.Runtime) {
		runtime.TaskStore = store
	}
}

func WithController(controller *agenttasks.Controller) Option {
	return func(runtime *agentruntime.Runtime) {
		runtime.Controller = controller
	}
}

func New(client MessageStreamer, model string, opts ...Option) Tool {
	runtime := agentruntime.Runtime{Client: client, Model: model}
	for _, opt := range opts {
		opt(&runtime)
	}
	return Tool{runtime: runtime}
}

func (t Tool) Name() string { return "Task" }

func (t Tool) MaxResultSizeChars() int { return tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars }

func (t Tool) Description() string {
	return `Run a focused sub-agent for a specific task and return its answer.

	Use Task for synchronous sub-agent work — the main agent waits for the result.
	For simple file search/count tasks, prefer direct Glob/Grep/LS calls instead of sub-agents.
	For long-running parallel work, use AgentCreate instead.

	Each sub-agent starts as a fresh conversation with only the delegated prompt
	plus configured agent instructions/memory/skills; it does not automatically see
	the parent conversation or previous tool results. Write the prompt as a complete
	brief: goal, why it matters, facts already known, paths/functions/line numbers,
	what was already ruled out, exact scope, and expected output format.

	Never delegate understanding with vague prompts like "based on your findings,
	fix the bug" or "research this and implement it". The parent agent must synthesize
	the sub-agent result and explain the final answer to the user.

	For a single synchronous Task, omit timeout_ms unless the user explicitly asks
	for a deadline. timeout_ms is an explicit hard deadline; setting it too low can
	cancel normal model/tool latency before the sub-agent finishes. 0 or omitted
	means no Task-level timeout.
	When auto-background is enabled, a long single Task may return a background
	task handle instead of a final answer; use AgentGet to check completion and
	do not invent the result while it is still running.

	The sub-agent inherits the parent's tools and permissions. Use the tasks parameter
	to run multiple sub-agent tasks concurrently (batch mode). Batch tasks get separate
	conversations but share ONE working tree with no file locking, so never put two
	tasks that write the same files in the same batch.

	Optional model override: default to omitting the model parameter — the sub-agent
	inherits the session model, which is almost always correct. Only set a lower tier
	(haiku) when highly confident the sub-task is mechanical (bulk search, format
	conversion, extraction); keep the default for anything requiring judgement. Under a
	non-Anthropic provider a tier maps to settings.subagentModelTiers when configured,
	otherwise the sub-agent inherits the session model. The optional effort parameter
	(low/medium/high/max) tunes the sub-agent's thinking budget the same way — omit to
	inherit, raise only for genuinely hard sub-tasks.
	Batch timeout_ms should be at least 30000ms due to model startup overhead.
	For repo-wide audit, scan, docs alignment, or cross-reference work, omit
	timeout_ms or use at least 180000ms; timeout is reported as timeout, not cancelled.

	Expected output contract: ask the sub-agent to return Summary, Evidence,
	Assumptions, Unknowns, Verification, Risks, and Next action. The parent
	agent can structure and reuse these fields in later planning, so do not ask
	for a vague final answer when evidence will drive the next step. Ask it to
	use those exact headings, include one concise bullet under each heading, and
	write "None observed" or an explicit unverified reason instead of omitting a
	heading.`
}

func (t Tool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type": "object",
	  "properties": {
	    "description": {"type": "string", "description": "Short task label. Required in single-task form; ignored when tasks[] is present."},
	    "prompt": {"type": "string", "description": "Required in single-task form; ignored when tasks[] is present. Complete brief for the fresh sub-agent: goal, known facts, ruled-out paths, relevant files/functions/line numbers, exact scope, and expected output. Ask for exact headings Summary, Evidence, Assumptions, Unknowns, Verification, Risks, and Next action when the result will drive parent planning; require one concise bullet under each heading, using 'None observed' or an explicit unverified reason instead of omitting headings. Do not delegate understanding with vague prompts like 'based on your findings, fix it'."},
	    "subagent_type": {"type": "string", "description": "Optional specialized agent type: general-purpose, Explore, Plan, or an agent name from .claude/agents. Omit for the default general-purpose agent; do not pass 'default'."},
	    "provider": {"type": "string", "description": "Optional named provider override. Requires a configured provider resolver; omitted means inherit the parent provider."},
	    "model": {"type": "string", "enum": ["sonnet", "opus", "haiku"], "description": "Optional model tier override for this sub-agent. Takes precedence over the agent definition's model. Default to omitting it — the sub-agent inherits the session model, which is almost always correct. Only set it when highly confident a lower tier suffices (mechanical search, format conversion, bulk extraction). Under a non-Anthropic provider the tier maps to settings.subagentModelTiers if configured, otherwise inherits the session model."},
	    "effort": {"type": "string", "enum": ["low", "medium", "high", "max"], "description": "Optional reasoning effort (thinking budget) for this sub-agent. Takes precedence over the agent definition's effort. Default to omitting it — the sub-agent inherits the agent's configured effort. Raise it only for genuinely hard sub-tasks (architecture review, adversarial verification); lower it for mechanical work."},
	    "max_output_tokens": {"type": "integer", "minimum": 1, "description": "Optional per-sub-agent output token cap; omitted means inherit the runtime cap."},
	    "max_turns": {"type": "integer", "minimum": 1, "description": "Optional per-sub-agent turn cap; omitted means inherit the runtime cap."},
	    "tasks": {"type": "array", "description": "Batch form: run several sub-agent tasks concurrently, instead of the single-task description+prompt form (the two are alternatives; pass exactly one). Each task gets its own fresh conversation, but all tasks run concurrently in the SAME working tree with no file locking \u2014 do not put two tasks that write the same files in one batch. For simple file search/count tasks prefer direct Glob/Grep/LS from the parent agent instead of sub-agents.", "items": {"type":"object","properties":{"description":{"type":"string"},"prompt":{"type":"string"},"subagent_type":{"type":"string","description":"Optional specialized agent type: general-purpose, Explore, Plan, or an agent name from .claude/agents. Omit for the default general-purpose agent; do not pass 'default'."},"model":{"type":"string","enum":["sonnet","opus","haiku"],"description":"Optional per-task model tier override. Omit to inherit the session model; set a lower tier only for mechanical/bulk items you are confident a smaller model handles."},"effort":{"type":"string","enum":["low","medium","high","max"],"description":"Optional per-task reasoning effort. Omit to inherit; raise only for genuinely hard items, lower for mechanical ones."},"priority":{"type":"integer","description":"Higher priority starts earlier when schedule_strategy=priority."},"retry_attempts":{"type":"integer","description":"Per-task retry attempts after the first failed attempt."},"timeout_ms":{"type":"integer","description":"Explicit hard deadline in milliseconds; omit or set 0 to use the batch default. Multi-task sub-agent batches should use at least 30000; repo-wide/deep-audit batches should omit timeout_ms or use at least 180000."}},"required":["description","prompt"],"additionalProperties":false}},
	    "max_concurrency": {"type": "integer", "minimum": 1, "maximum": 16, "description": "Maximum concurrent batch tasks. Default 4, capped at 16 unless the deployment raises NIUMA_MAX_BATCH_CONCURRENCY; larger values are clamped and the batch summary reports the clamp."},
	    "schedule_strategy": {"type": "string", "enum": ["fifo", "priority"], "description": "Batch scheduling strategy. fifo preserves input order; priority starts higher priority tasks first."},
	    "retry_attempts": {"type": "integer", "description": "Retry attempts after the first failed attempt, for the single task or for each batch task. Capped at 10. Cancellation is never retried; a per-attempt timeout is. Default 0."},
	    "retry_backoff_ms": {"type": "integer", "description": "Delay between retry attempts, capped at 30000. Default 0."},
	    "timeout_ms": {"type": "integer", "description": "Explicit hard deadline in milliseconds for single and batch tasks. For a single synchronous Task, omit this unless the user explicitly requested a deadline; 0 or omitted means no Task-level timeout. Multi-task batches should use at least 30000ms; repo-wide/deep-audit batches should omit timeout_ms or use at least 180000ms. Use direct local tools for simple file searches/counts."}
	  },
	  "additionalProperties": false
	}`)
}

func (t Tool) Run(ctx context.Context, input json.RawMessage, toolContext tools.Context) tools.Result {
	var params struct {
		Description     string      `json:"description"`
		Prompt          string      `json:"prompt"`
		SubagentType    string      `json:"subagent_type"`
		Provider        string      `json:"provider"`
		Model           string      `json:"model"`
		Effort          string      `json:"effort"`
		MaxOutputTokens int         `json:"max_output_tokens"`
		MaxTurns        int         `json:"max_turns"`
		Tasks           []taskParam `json:"tasks"`
		MaxConcurrency  int         `json:"max_concurrency"`
		Schedule        string      `json:"schedule_strategy"`
		RetryAttempts   int         `json:"retry_attempts"`
		RetryBackoffMS  int         `json:"retry_backoff_ms"`
		TimeoutMS       int         `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	// 单任务与 batch 是二选一，但 schema 表达不了「二选一」而不用根级 anyOf ——
	// 根级 anyOf 在各 provider 的 tool schema 校验里兼容性不一（AUDIT-P1-19），所以
	// 这里在代码里校验，并给模型一句能照着改的错误。既有的 Agent 工具（agent.go）
	// 就是这么做的：schema 声明 + 代码校验各做一半。
	if err := validateTaskForm(params.Description, params.Prompt, len(params.Tasks)); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if len(params.Tasks) > 0 {
		return t.runBatch(ctx, batchOptions{
			Items:          params.Tasks,
			MaxConcurrency: params.MaxConcurrency,
			Schedule:       params.Schedule,
			RetryAttempts:  params.RetryAttempts,
			RetryBackoffMS: params.RetryBackoffMS,
			TimeoutMS:      params.TimeoutMS,
			ToolContext:    toolContext,
		})
	}
	if params.SubagentType == "" && toolContext.ActiveSkill != nil {
		params.SubagentType = toolContext.ActiveSkill.Agent
	}
	runtime := t.runtime
	if runtime.TaskStore == nil {
		runtime.TaskStore = toolContext.TaskStore
	}
	background, err := agentruntime.AgentBackground(toolContext.CWD, params.SubagentType)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if background {
		if runtime.TaskStore == nil {
			return tools.Result{Content: "background sub-agent requires task store", IsError: true}
		}
		result, err := runtime.RunBackground(ctx, agentruntime.Request{
			Description:     params.Description,
			Prompt:          params.Prompt,
			SubagentType:    params.SubagentType,
			Provider:        params.Provider,
			Model:           params.Model,
			Effort:          params.Effort,
			MaxTokens:       params.MaxOutputTokens,
			MaxTurns:        params.MaxTurns,
			TimeoutMS:       params.TimeoutMS,
			CWD:             toolContext.CWD,
			TenantID:        toolContext.TenantID,
			UserID:          toolContext.UserID,
			ParentSessionID: toolContext.SessionID,
			TraceID:         toolContext.TraceID,
		}, toolContext)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		data, err := json.Marshal(backgroundResponse{
			Status:         result.Status,
			Background:     result.Background,
			TaskID:         result.TaskID,
			AgentName:      result.AgentName,
			Model:          result.Model,
			SessionID:      result.SessionID,
			PermissionMode: result.PermissionMode,
		})
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		return tools.Result{Content: string(data)}
	}
	request := agentruntime.Request{
		Description:     params.Description,
		Prompt:          params.Prompt,
		SubagentType:    params.SubagentType,
		Provider:        params.Provider,
		Model:           params.Model,
		Effort:          params.Effort,
		MaxTokens:       params.MaxOutputTokens,
		MaxTurns:        params.MaxTurns,
		CWD:             toolContext.CWD,
		TenantID:        toolContext.TenantID,
		UserID:          toolContext.UserID,
		ParentSessionID: toolContext.SessionID,
		TraceID:         toolContext.TraceID,
	}
	if autoAfter := autoBackgroundAfter(); autoAfter > 0 && runtime.TaskStore != nil {
		// 这条路径不重试：一次尝试可能已经翻成后台任务并返回了句柄，再起一次就变成
		// 两个子代理在跑同一件事。auto-background 靠 env 显式开启，默认关闭。
		return t.runSingleWithAutoBackground(ctx, runtime, request, params.TimeoutMS, autoAfter, toolContext)
	}
	return t.runSingle(ctx, runtime, request, params.TimeoutMS, params.RetryAttempts,
		time.Duration(clamp(params.RetryBackoffMS, 0, 30000))*time.Millisecond, toolContext)
}

// validateTaskForm 守住「单任务 或 batch，二选一」这条 schema 表达不了的约束。
//
// 之前 `{}` 和 `{"description":"x"}` 都会被接受然后拿一个空 prompt 去起子代理，
// 而 `{"description":"a","prompt":"b","tasks":[...]}` 会静默忽略顶层那两个字段。
func validateTaskForm(description, prompt string, batchSize int) error {
	hasSingle := strings.TrimSpace(description) != "" || strings.TrimSpace(prompt) != ""
	if batchSize > 0 {
		if hasSingle {
			return errors.New("Task takes either the single-task form (description + prompt) or the batch form (tasks), not both: drop the top-level description/prompt, or drop tasks")
		}
		return nil
	}
	if strings.TrimSpace(description) == "" || strings.TrimSpace(prompt) == "" {
		return errors.New("Task requires either description and prompt (single task) or a non-empty tasks array (batch)")
	}
	return nil
}

// worthRetrying 判断一次失败之后还该不该再试一次。
//
// 单 Task 与 batch 共用这一个判断，两条路径才不会各自漂移（AUDIT-P1-20）。
// 取消不重试 —— 父 ctx 已经死了，后续每次尝试都会同样失败，还会覆盖这次已经拿到的
// partial evidence。deadline 仍然重试：单次尝试超时不代表下一次也超时。
func worthRetrying(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && ctx.Err() == nil
}

// runSingle 跑单个子代理，并按 retry_attempts 重试。
//
// 重试语义与 batch 完全一致（同一个 worthRetrying、同一个退避、同一个 0..10 clamp）。
// 以前单 Task 是一次不重试的，同一个 retry_attempts 参数在两种形式下含义不同，
// 而这个差别在描述里也没写（AUDIT-P1-20）。
func (t Tool) runSingle(ctx context.Context, runtime agentruntime.Runtime, request agentruntime.Request, timeoutMS, retryAttempts int, retryBackoff time.Duration, toolContext tools.Context) tools.Result {
	totalAttempts := clamp(retryAttempts, 0, 10) + 1
	var result agentruntime.Result
	var err error
	for attempt := 1; attempt <= totalAttempts; attempt++ {
		result, err = t.runSingleAttempt(ctx, runtime, request, timeoutMS, toolContext)
		if err == nil || attempt == totalAttempts || !worthRetrying(ctx, err) {
			break
		}
		if retryBackoff > 0 {
			select {
			case <-time.After(retryBackoff):
			case <-ctx.Done():
				return singleTaskRunResult(result, err)
			}
		}
	}
	return singleTaskRunResult(result, err)
}

func (t Tool) runSingleAttempt(ctx context.Context, runtime agentruntime.Runtime, request agentruntime.Request, timeoutMS int, toolContext tools.Context) (agentruntime.Result, error) {
	runCtx, cancel := contextWithTimeout(ctx, timeoutMS)
	defer cancel()
	result, err := runtime.Run(runCtx, request, toolContext)
	if isDeadlineExceeded(runCtx, err) {
		err = timeoutError()
	}
	return result, err
}

func singleTaskRunResult(result agentruntime.Result, err error) tools.Result {
	if err != nil {
		if content, ok := partialTaskResultContent(result, err); ok {
			return tools.Result{Content: content}
		}
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: agentruntime.ResultWithCapabilityLoopDecisionContext(result)}
}

func (t Tool) runSingleWithAutoBackground(ctx context.Context, runtime agentruntime.Runtime, request agentruntime.Request, timeoutMS int, autoAfter time.Duration, toolContext tools.Context) tools.Result {
	started := make(chan autoBackgroundStart, 1)
	resultCh := make(chan singleTaskOutcome, 1)
	runtime.TaskStore = autoBackgroundStartStore{base: runtime.TaskStore, started: started}
	baseCtx := context.WithoutCancel(ctx)
	runCtx, cancel := contextWithTimeout(baseCtx, timeoutMS)
	go func() {
		result, err := runtime.Run(runCtx, request, toolContext)
		if isDeadlineExceeded(runCtx, err) {
			err = timeoutError()
		}
		resultCh <- singleTaskOutcome{result: result, err: err}
		cancel()
	}()

	timer := time.NewTimer(autoAfter)
	defer timer.Stop()
	timerC := timer.C
	autoExpired := false
	var start *autoBackgroundStart
	for {
		select {
		case outcome := <-resultCh:
			return singleTaskRunResult(outcome.result, outcome.err)
		case s := <-started:
			if s.err != nil {
				cancel()
				return tools.Result{Content: s.err.Error(), IsError: true}
			}
			start = &s
			if autoExpired {
				return autoBackgroundTaskResult(*start)
			}
		case <-timerC:
			autoExpired = true
			timerC = nil
			if start != nil {
				return autoBackgroundTaskResult(*start)
			}
		case <-ctx.Done():
			cancel()
			return tools.Result{Content: ctx.Err().Error(), IsError: true}
		}
	}
}

type singleTaskOutcome struct {
	result agentruntime.Result
	err    error
}

type autoBackgroundStart struct {
	taskID         uint64
	agentName      string
	model          string
	sessionID      string
	permissionMode string
	err            error
}

type autoBackgroundStartStore struct {
	base    agenttasks.Store
	started chan<- autoBackgroundStart
}

func (s autoBackgroundStartStore) CreateAgentTask(ctx context.Context, input agenttasks.TaskInput) (uint64, error) {
	id, err := s.base.CreateAgentTask(ctx, input)
	start := autoBackgroundStart{
		taskID:    id,
		agentName: input.AgentName,
		model:     input.Model,
		sessionID: input.SubagentSessionKey,
		err:       err,
	}
	if input.MetadataJSON != "" {
		var metadata struct {
			PermissionMode string `json:"permission_mode"`
		}
		if json.Unmarshal([]byte(input.MetadataJSON), &metadata) == nil {
			start.permissionMode = metadata.PermissionMode
		}
	}
	select {
	case s.started <- start:
	default:
	}
	return id, err
}

func (s autoBackgroundStartStore) FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error {
	return s.base.FinishAgentTask(ctx, taskID, status, resultJSON)
}

func (s autoBackgroundStartStore) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	return s.base.AppendAgentTaskEvent(ctx, input)
}

func (s autoBackgroundStartStore) IsAgentTaskCancelled(ctx context.Context, taskID uint64) (bool, error) {
	checker, ok := s.base.(agenttasks.CancellationChecker)
	if !ok {
		return false, nil
	}
	return checker.IsAgentTaskCancelled(ctx, taskID)
}

func autoBackgroundTaskResult(start autoBackgroundStart) tools.Result {
	data, err := json.Marshal(backgroundResponse{
		Status:         agenttasks.StatusRunning,
		Background:     true,
		TaskID:         start.taskID,
		AgentName:      start.agentName,
		Model:          start.model,
		SessionID:      start.sessionID,
		PermissionMode: start.permissionMode,
		Instructions:   "The sub-agent is still running in the background. Do not invent its result; use AgentGet with task_id to check completion before synthesizing findings.",
	})
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	return tools.Result{Content: string(data)}
}

type batchResult struct {
	Index          int                          `json:"index"`
	Description    string                       `json:"description,omitempty"`
	Status         string                       `json:"status"`
	Background     bool                         `json:"background,omitempty"`
	Priority       int                          `json:"priority,omitempty"`
	Attempts       int                          `json:"attempts"`
	StartedAt      string                       `json:"started_at,omitempty"`
	FinishedAt     string                       `json:"finished_at,omitempty"`
	DurationMS     int64                        `json:"duration_ms,omitempty"`
	Content        string                       `json:"content,omitempty"`
	CapabilityLoop *agentruntime.CapabilityLoop `json:"capability_loop,omitempty"`
	IsError        bool                         `json:"is_error,omitempty"`
	Error          string                       `json:"error,omitempty"`
	TaskID         uint64                       `json:"task_id,omitempty"`
	SessionID      string                       `json:"session_id,omitempty"`
	TranscriptPath string                       `json:"transcript_path,omitempty"`
	Model          string                       `json:"model,omitempty"`
	InputTokens    int                          `json:"input_tokens,omitempty"`
	OutputTokens   int                          `json:"output_tokens,omitempty"`
	CostUSD        float64                      `json:"cost_usd,omitempty"`
}

type batchSummary struct {
	Total          int `json:"total"`
	Completed      int `json:"completed"`
	Running        int `json:"running,omitempty"`
	Timeout        int `json:"timeout,omitempty"`
	Failed         int `json:"failed"`
	Retries        int `json:"retries"`
	MaxConcurrency int `json:"max_concurrency"`
	// ConcurrencyClampNote is set only when max_concurrency was above the
	// configured ceiling. Clamping silently would read as "your 500 workers ran",
	// so the batch reports what it actually did.
	ConcurrencyClampNote string  `json:"concurrency_clamp_note,omitempty"`
	ScheduleStrategy     string  `json:"schedule_strategy"`
	StartedAt            string  `json:"started_at"`
	FinishedAt           string  `json:"finished_at"`
	DurationMS           int64   `json:"duration_ms"`
	TotalInputTokens     int     `json:"total_input_tokens,omitempty"`
	TotalOutputTokens    int     `json:"total_output_tokens,omitempty"`
	TotalCostUSD         float64 `json:"total_cost_usd,omitempty"`
}

type batchResponse struct {
	Summary batchSummary  `json:"summary"`
	Tasks   []batchResult `json:"tasks"`
}

type backgroundResponse struct {
	Status         string `json:"status"`
	Background     bool   `json:"background"`
	TaskID         uint64 `json:"task_id"`
	AgentName      string `json:"agent_name,omitempty"`
	Model          string `json:"model,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Instructions   string `json:"instructions,omitempty"`
}

type batchOptions struct {
	Items          []taskParam
	MaxConcurrency int
	Schedule       string
	RetryAttempts  int
	RetryBackoffMS int
	TimeoutMS      int
	ToolContext    tools.Context
}

func (t Tool) runBatch(ctx context.Context, opts batchOptions) tools.Result {
	items := opts.Items
	maxConcurrency := opts.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = defaultBatchConcurrency
	}
	requestedConcurrency := maxConcurrency
	if ceiling := maxBatchConcurrency(); maxConcurrency > ceiling {
		maxConcurrency = ceiling
	}
	if maxConcurrency > len(items) {
		maxConcurrency = len(items)
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	schedule := normalizeSchedule(opts.Schedule)
	retryAttempts := clamp(opts.RetryAttempts, 0, 10)
	retryBackoff := time.Duration(clamp(opts.RetryBackoffMS, 0, 30000)) * time.Millisecond
	if invalid, minTimeout, ok := invalidMultiTaskBatchTimeout(items, opts.TimeoutMS); ok {
		return invalidBatchTimeoutResult(items, maxConcurrency, schedule, invalid, minTimeout)
	}
	batchStarted := time.Now()
	results := make([]batchResult, len(items))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < maxConcurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				item := items[i]
				if err := ctx.Err(); err != nil {
					results[i] = batchResult{
						Index:       i,
						Description: item.Description,
						Status:      "cancelled",
						Priority:    item.Priority,
						IsError:     true,
						Error:       err.Error(),
					}
					continue
				}
				results[i] = t.runBatchItem(ctx, item, i, maxRetryAttempts(retryAttempts, item.RetryAttempts), retryBackoff, effectiveTimeout(opts.TimeoutMS, item.TimeoutMS), opts.ToolContext)
			}
		}()
	}
	for _, i := range scheduleOrder(items, schedule) {
		select {
		case jobs <- i:
		case <-ctx.Done():
			item := items[i]
			results[i] = batchResult{
				Index:       i,
				Description: item.Description,
				Status:      "cancelled",
				Priority:    item.Priority,
				IsError:     true,
				Error:       ctx.Err().Error(),
			}
		}
	}
	close(jobs)
	wg.Wait()
	batchFinished := time.Now()
	response := batchResponse{
		Summary: batchSummary{
			Total:                len(results),
			MaxConcurrency:       maxConcurrency,
			ConcurrencyClampNote: concurrencyClampNote(requestedConcurrency, maxConcurrency),
			ScheduleStrategy:     schedule,
			StartedAt:            batchStarted.UTC().Format(time.RFC3339Nano),
			FinishedAt:           batchFinished.UTC().Format(time.RFC3339Nano),
			DurationMS:           batchFinished.Sub(batchStarted).Milliseconds(),
		},
		Tasks: results,
	}
	for _, result := range results {
		switch {
		case result.Status == "running":
			response.Summary.Running++
		case result.Status == agenttasks.StatusTimeout:
			response.Summary.Timeout++
			response.Summary.Failed++
		case result.IsError:
			response.Summary.Failed++
		default:
			response.Summary.Completed++
		}
		if result.Attempts > 1 {
			response.Summary.Retries += result.Attempts - 1
		}
		response.Summary.TotalInputTokens += result.InputTokens
		response.Summary.TotalOutputTokens += result.OutputTokens
		response.Summary.TotalCostUSD += result.CostUSD
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	if response.Summary.Failed > 0 {
		return tools.Result{Content: string(data), IsError: true}
	}
	return tools.Result{Content: string(data)}
}

func concurrencyClampNote(requested, effective int) string {
	if ceiling := maxBatchConcurrency(); requested <= ceiling {
		return ""
	}
	return fmt.Sprintf("max_concurrency=%d exceeds the %d-slot ceiling; the batch ran with %d concurrent sub-agents (raise %s to change the ceiling)",
		requested, maxBatchConcurrency(), effective, EnvMaxBatchConcurrency)
}

func (t Tool) runBatchItem(ctx context.Context, item taskParam, index, retryAttempts int, retryBackoff time.Duration, timeoutMS int, toolContext tools.Context) batchResult {
	started := time.Now()
	out := batchResult{
		Index:       index,
		Description: item.Description,
		Status:      "running",
		Priority:    item.Priority,
		StartedAt:   started.UTC().Format(time.RFC3339Nano),
	}
	totalAttempts := retryAttempts + 1
	var lastResult agentruntime.Result
	var lastErr error
	for attempt := 1; attempt <= totalAttempts; attempt++ {
		out.Attempts = attempt
		if item.SubagentType == "" && toolContext.ActiveSkill != nil {
			item.SubagentType = toolContext.ActiveSkill.Agent
		}
		runtime := t.runtime
		if runtime.TaskStore == nil {
			runtime.TaskStore = toolContext.TaskStore
		}
		background, err := agentruntime.AgentBackground(toolContext.CWD, item.SubagentType)
		if err != nil {
			lastErr = err
			break
		}
		if background {
			if runtime.TaskStore == nil {
				lastErr = errors.New("background sub-agent requires task store")
				break
			}
			lastResult, lastErr = runtime.RunBackground(ctx, agentruntime.Request{
				Description:     item.Description,
				Prompt:          item.Prompt,
				SubagentType:    item.SubagentType,
				Provider:        item.Provider,
				Model:           item.Model,
				Effort:          item.Effort,
				MaxTokens:       item.MaxOutputTokens,
				MaxTurns:        item.MaxTurns,
				TimeoutMS:       effectiveTimeout(timeoutMS, item.TimeoutMS),
				CWD:             toolContext.CWD,
				TenantID:        toolContext.TenantID,
				UserID:          toolContext.UserID,
				ParentSessionID: toolContext.SessionID,
				TraceID:         toolContext.TraceID,
			}, toolContext)
			break
		}
		runCtx, cancel := contextWithTimeout(ctx, timeoutMS)
		lastResult, lastErr = runtime.Run(runCtx, agentruntime.Request{
			Description:     item.Description,
			Prompt:          item.Prompt,
			SubagentType:    item.SubagentType,
			Provider:        item.Provider,
			Model:           item.Model,
			Effort:          item.Effort,
			MaxTokens:       item.MaxOutputTokens,
			MaxTurns:        item.MaxTurns,
			CWD:             toolContext.CWD,
			TenantID:        toolContext.TenantID,
			UserID:          toolContext.UserID,
			ParentSessionID: toolContext.SessionID,
			TraceID:         toolContext.TraceID,
		}, toolContext)
		if isDeadlineExceeded(runCtx, lastErr) {
			lastErr = timeoutError()
		}
		cancel()
		if lastErr == nil || attempt == totalAttempts {
			break
		}
		if !worthRetrying(ctx, lastErr) {
			break
		}
		if retryBackoff > 0 {
			select {
			case <-time.After(retryBackoff):
			case <-ctx.Done():
				lastErr = ctx.Err()
				attempt = totalAttempts
			}
		}
	}
	finished := time.Now()
	out.Status = "completed"
	if lastResult.Background {
		out.Status = agenttasks.StatusRunning
		out.Background = true
	}
	out.Content = lastResult.Content
	if agentruntime.CapabilityLoopHasActionableEvidence(lastResult.CapabilityLoop) {
		out.CapabilityLoop = lastResult.CapabilityLoop
	}
	out.TaskID = lastResult.TaskID
	out.SessionID = lastResult.SessionID
	out.TranscriptPath = lastResult.TranscriptPath
	out.Model = lastResult.Model
	out.InputTokens = lastResult.Usage.InputTokens
	out.OutputTokens = lastResult.Usage.OutputTokens
	out.CostUSD = lastResult.CostUSD
	out.FinishedAt = finished.UTC().Format(time.RFC3339Nano)
	out.DurationMS = finished.Sub(started).Milliseconds()
	if lastResult.Background {
		out.Content = ""
		out.FinishedAt = ""
	}
	if lastErr != nil {
		out.Status = "failed"
		out.IsError = true
		out.Error = lastErr.Error()
		if errors.Is(lastErr, context.DeadlineExceeded) {
			out.Status = "timeout"
		}
	}
	return out
}

func contextWithTimeout(ctx context.Context, timeoutMS int) (context.Context, context.CancelFunc) {
	timeoutMS = clamp(timeoutMS, 0, 24*60*60*1000)
	if timeoutMS <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
}

func autoBackgroundAfter() time.Duration {
	if !truthyEnv("CLAUDE_AUTO_BACKGROUND_TASKS") && !truthyEnv("NIUMA_AUTO_BACKGROUND_TASKS") {
		return 0
	}
	if raw := strings.TrimSpace(os.Getenv("NIUMA_AUTO_BACKGROUND_MS")); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err == nil && ms > 0 {
			return time.Duration(clamp(ms, 1, 24*60*60*1000)) * time.Millisecond
		}
	}
	return 120 * time.Second
}

func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func effectiveTimeout(global, item int) int {
	if item > 0 {
		return item
	}
	return global
}

func invalidMultiTaskBatchTimeout(items []taskParam, globalTimeoutMS int) (int, int, bool) {
	if len(items) < 2 {
		return 0, 0, false
	}
	minTimeout := minMultiTaskBatchTimeoutMS
	if batchLooksLikeDeepAudit(items) {
		minTimeout = minDeepAuditBatchTimeoutMS
	}
	for _, item := range items {
		timeout := effectiveTimeout(globalTimeoutMS, item.TimeoutMS)
		if timeout > 0 && timeout < minTimeout {
			return timeout, minTimeout, true
		}
	}
	return 0, 0, false
}

func batchLooksLikeDeepAudit(items []taskParam) bool {
	keywords := []string{
		"audit", "scan", "cross-reference", "cross reference", "alignment", "docs", "vitepress", "repo-wide", "repository-wide", "full repo", "entire repo",
		"所有文件", "全量", "审计", "扫描", "对齐", "文档", "全仓库", "整个仓库",
	}
	for _, item := range items {
		text := strings.ToLower(strings.Join([]string{item.Description, item.Prompt}, "\n"))
		for _, keyword := range keywords {
			if strings.Contains(text, keyword) {
				return true
			}
		}
	}
	return false
}

func invalidBatchTimeoutResult(items []taskParam, maxConcurrency int, schedule string, timeoutMS, minTimeoutMS int) tools.Result {
	if minTimeoutMS <= 0 {
		minTimeoutMS = minMultiTaskBatchTimeoutMS
	}
	message := fmt.Sprintf("multi-task sub-agent batch timeout_ms=%d is too small; use >=%d or omit timeout_ms. For repo-wide/deep-audit work, omit timeout_ms or use a longer lifecycle profile; for simple file search/count tasks, call local Glob/Grep/LS directly from the parent agent.", timeoutMS, minTimeoutMS)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	response := batchResponse{
		Summary: batchSummary{
			Total:            len(items),
			Failed:           len(items),
			MaxConcurrency:   maxConcurrency,
			ScheduleStrategy: schedule,
			StartedAt:        now,
			FinishedAt:       now,
		},
		Tasks: make([]batchResult, len(items)),
	}
	for i, item := range items {
		response.Tasks[i] = batchResult{
			Index:       i,
			Description: item.Description,
			Status:      "invalid_timeout",
			Priority:    item.Priority,
			Attempts:    0,
			IsError:     true,
			Error:       message,
		}
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return tools.Result{Content: message, IsError: true}
	}
	return tools.Result{Content: string(data), IsError: true}
}

func isDeadlineExceeded(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() == context.DeadlineExceeded
}

func timeoutError() error {
	return context.DeadlineExceeded
}

func partialTaskResultContent(result agentruntime.Result, runErr error) (string, bool) {
	partial := strings.TrimSpace(result.Content)
	if partial == "" && len(result.ToolCalls) == 0 {
		return "", false
	}
	status := strings.TrimSpace(strings.ToLower(result.Status))
	if status == "" || status == agenttasks.StatusRunning {
		status = agenttasks.StatusFailed
	}
	if errors.Is(runErr, context.DeadlineExceeded) || status == agenttasks.StatusTimeout {
		status = agenttasks.StatusTimeout
	}
	var b strings.Builder
	b.WriteString("Sub-agent stopped before a clean final answer")
	if runErr != nil {
		b.WriteString(": ")
		b.WriteString(runErr.Error())
	}
	b.WriteString(".\n\n")
	if partial != "" {
		b.WriteString("Partial answer:\n")
		b.WriteString(partial)
		b.WriteString("\n")
	}
	if result.Turns > 0 || len(result.ToolCalls) > 0 || result.SessionID != "" || result.TranscriptPath != "" || result.OutputFile != "" || result.WorktreePath != "" || result.WorktreeBranch != "" {
		b.WriteString("\nPartial progress metadata:\n")
		if result.Turns > 0 {
			b.WriteString(fmt.Sprintf("- turns: %d\n", result.Turns))
		}
		if len(result.ToolCalls) > 0 {
			b.WriteString(fmt.Sprintf("- tool_calls: %d\n", len(result.ToolCalls)))
		}
		if result.SessionID != "" {
			b.WriteString("- session_id: ")
			b.WriteString(result.SessionID)
			b.WriteString("\n")
		}
		if result.TranscriptPath != "" {
			b.WriteString("- transcript_path: ")
			b.WriteString(result.TranscriptPath)
			b.WriteString("\n")
		}
		if result.OutputFile != "" {
			b.WriteString("- output_file: ")
			b.WriteString(result.OutputFile)
			b.WriteString("\n")
		}
		if result.WorktreePath != "" {
			b.WriteString("- worktree_path: ")
			b.WriteString(result.WorktreePath)
			b.WriteString("\n")
		}
		if result.WorktreeBranch != "" {
			b.WriteString("- worktree_branch: ")
			b.WriteString(result.WorktreeBranch)
			b.WriteString("\n")
		}
	}
	if len(result.ToolCalls) > 0 {
		b.WriteString("\nCompleted tool calls before failure:\n")
		for _, call := range tailToolCalls(result.ToolCalls, 8) {
			b.WriteString("- ")
			b.WriteString(call.Name)
			if call.IsError {
				b.WriteString(" (error)")
			}
			input := strings.TrimSpace(call.Input)
			if input != "" {
				b.WriteString(" input=")
				b.WriteString(truncateTaskPartialField(input, 240))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nTreat this as an incomplete sub-agent result and continue from the partial evidence if needed.")
	partialResult := agentruntime.EnsureCapabilityLoop(agentruntime.Result{
		Content:        b.String(),
		Status:         status,
		SessionID:      result.SessionID,
		TranscriptPath: result.TranscriptPath,
		OutputFile:     result.OutputFile,
		WorktreePath:   result.WorktreePath,
		WorktreeBranch: result.WorktreeBranch,
		TaskID:         result.TaskID,
		ToolCalls:      result.ToolCalls,
	}, status)
	if partialResult.CapabilityLoop != nil {
		stopWord := "failure"
		if status == agenttasks.StatusTimeout {
			stopWord = "timeout"
		}
		partialResult.CapabilityLoop.Unknowns = replaceDefaultCapabilityLoopField(partialResult.CapabilityLoop.Unknowns, "Whether the partial sub-agent result covered all requested evidence before "+stopWord+".")
		partialResult.CapabilityLoop.Verification = replaceDefaultCapabilityLoopField(partialResult.CapabilityLoop.Verification, "Inspect partial answer, completed tool calls, transcript_path, or rerun a narrower verification before relying on findings.")
		if len(partialResult.CapabilityLoop.Risks) == 0 {
			partialResult.CapabilityLoop.Risks = []string{"Partial sub-agent result may be incomplete, stale, or missing later evidence due to the " + stopWord + "."}
		}
	}
	return agentruntime.ResultWithCapabilityLoopDecisionContext(partialResult), true
}

func replaceDefaultCapabilityLoopField(values []string, replacement string) []string {
	if len(values) == 1 && strings.HasPrefix(values[0], "No explicit ") {
		return []string{replacement}
	}
	if len(values) == 0 {
		return []string{replacement}
	}
	return values
}

func tailToolCalls(calls []agentruntime.ToolTrace, limit int) []agentruntime.ToolTrace {
	if limit <= 0 || len(calls) <= limit {
		return calls
	}
	return calls[len(calls)-limit:]
}

func truncateTaskPartialField(value string, limit int) string {
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func normalizeSchedule(schedule string) string {
	switch schedule {
	case "priority":
		return "priority"
	default:
		return "fifo"
	}
}

func scheduleOrder(items []taskParam, strategy string) []int {
	order := make([]int, len(items))
	for i := range items {
		order[i] = i
	}
	if strategy != "priority" {
		return order
	}
	sort.SliceStable(order, func(i, j int) bool {
		left := items[order[i]]
		right := items[order[j]]
		if left.Priority == right.Priority {
			return order[i] < order[j]
		}
		return left.Priority > right.Priority
	})
	return order
}

func maxRetryAttempts(global, item int) int {
	if item > global {
		return clamp(item, 0, 10)
	}
	return global
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
