package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/agentbudget"
	"github.com/konglong87/super-niuma/internal/agents"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agentworktree"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/capabilityloop"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/gitcontext"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/loopguard"
	"github.com/konglong87/super-niuma/internal/mcp"
	"github.com/konglong87/super-niuma/internal/memory"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/promptcache"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/skills"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/toolpolicy"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
	computerusetool "github.com/konglong87/super-niuma/internal/tools/computeruse"
)

type MessageStreamer interface {
	StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error)
}

// ClientResolver creates a provider-specific client for one sub-agent run and
// returns a cleanup callback for any per-client resources.
type ClientResolver func(context.Context, string, string) (MessageStreamer, func(), error)

type Runtime struct {
	Client         MessageStreamer
	ClientResolver ClientResolver
	// ProviderPreflight runs before provider client creation. It is optional
	// for backwards compatibility; deployments that expose named providers
	// should use it to reject missing credentials, unsupported models, or
	// unhealthy providers before task state is created.
	ProviderPreflight       func(context.Context, string, string) error
	Registry                *tools.Registry
	Model                   string
	MaxTurns                int
	MaxTokens               int
	ToolResultLimit         int
	ToolResultMessageBudget int
	ToolResultHistoryBudget int
	RecorderStore           session.Store
	Hooks                   hooks.Runner
	TaskStore               agenttasks.Store
	Controller              *agenttasks.Controller
	LoadMCPTools            func(context.Context, map[string]config.MCPServerConfig) ([]tools.Tool, func())
	// AutoCompact overrides the compaction config for this sub-agent run. Left
	// zero, Run loads it from the settings for req.CWD (same pattern as
	// SubagentModelTiers / ModelPricing), so sub-agents get context-overflow
	// protection without every call site having to thread it through.
	AutoCompact *compact.Config

	requestCacheTracker promptcache.RequestTracker
}

var localProgressTaskSeq atomic.Uint64

type Request struct {
	Description                   string
	Prompt                        string
	InitialMessages               []anthropic.MessageParam
	InitialToolResultReplacements []toolresult.ReplacementRecord
	SubagentType                  string
	Provider                      string
	Model                         string
	Effort                        string
	MaxTurns                      int
	MaxTokens                     int
	AgentMode                     string
	TimeoutMS                     int
	CWD                           string
	TenantID                      uint64
	UserID                        uint64
	ParentSessionID               uint64
	TraceID                       string
	Override                      AgentExecutionOverride
	WorktreePath                  string
	WorktreeBranch                string
	WorktreeHeadCommit            string
	WorktreeGitRoot               string
	WorktreeHookBased             bool
}

type Result struct {
	Content             string          `json:"content"`
	PersistenceDegraded bool            `json:"persistence_degraded,omitempty"`
	PersistenceError    string          `json:"persistence_error,omitempty"`
	CapabilityLoop      *CapabilityLoop `json:"capability_loop,omitempty"`
	AgentName           string          `json:"agent_name,omitempty"`
	Model               string          `json:"model,omitempty"`
	Provider            string          `json:"provider,omitempty"`
	Status              string          `json:"status,omitempty"`
	Background          bool            `json:"background,omitempty"`
	AgentMode           string          `json:"agent_mode,omitempty"`
	Effort              string          `json:"effort,omitempty"`
	MaxOutputTokens     int             `json:"max_output_tokens,omitempty"`
	MaxTurns            int             `json:"max_turns,omitempty"`
	TimeoutMS           int             `json:"timeout_ms,omitempty"`
	OverrideReasons     []string        `json:"override_reason_codes,omitempty"`
	PermissionMode      string          `json:"permission_mode,omitempty"`
	SessionID           string          `json:"session_id,omitempty"`
	TranscriptPath      string          `json:"transcript_path,omitempty"`
	OutputFile          string          `json:"output_file,omitempty"`
	WorktreePath        string          `json:"worktree_path,omitempty"`
	WorktreeBranch      string          `json:"worktree_branch,omitempty"`
	WorktreeHookBased   bool            `json:"worktree_hook_based,omitempty"`
	Turns               int             `json:"turns,omitempty"`
	ToolCalls           []ToolTrace     `json:"tool_calls,omitempty"`
	TaskID              uint64          `json:"task_id,omitempty"`
	Usage               Usage           `json:"usage,omitempty"`
	CostUSD             float64         `json:"cost_usd,omitempty"`
	// CostKnown is false when no price is known for the model, so CostUSD is 0
	// because it is unknown rather than because the run was free (AUDIT-P0-15).
	CostKnown bool `json:"cost_known,omitempty"`
}

type CapabilityLoop struct {
	Evidence              []string `json:"evidence,omitempty"`
	Assumptions           []string `json:"assumptions,omitempty"`
	Unknowns              []string `json:"unknowns,omitempty"`
	Verification          []string `json:"verification,omitempty"`
	Risks                 []string `json:"risks,omitempty"`
	NextAction            string   `json:"next_action,omitempty"`
	ResolvedFollowUp      string   `json:"resolved_follow_up,omitempty"`
	SupersedesEvidenceID  string   `json:"supersedes_evidence_id,omitempty"`
	SupersedesEvidenceIDs []string `json:"supersedes_evidence_ids,omitempty"`
}

type ToolTrace struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Input           string `json:"input,omitempty"`
	Output          string `json:"output"`
	IsError         bool   `json:"is_error,omitempty"`
	contextMessages []anthropic.MessageParam
}

type persistenceState struct {
	firstErr error
}

func (s *persistenceState) record(operation string, err error) {
	if s == nil || err == nil || s.firstErr != nil {
		return
	}
	s.firstErr = fmt.Errorf("%s: %w", operation, err)
}

func (s *persistenceState) apply(result *Result) {
	if s == nil || result == nil || s.firstErr == nil {
		return
	}
	result.PersistenceDegraded = true
	result.PersistenceError = s.firstErr.Error()
}

type Usage struct {
	InputTokens                         int    `json:"input_tokens,omitempty"`
	OutputTokens                        int    `json:"output_tokens,omitempty"`
	CacheCreationInputTokens            int    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int    `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int    `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	ServiceTier                         string `json:"service_tier,omitempty"`
	InferenceGeo                        string `json:"inference_geo,omitempty"`
	Speed                               string `json:"speed,omitempty"`
}

func (r Runtime) RunBackground(ctx context.Context, req Request, toolContext tools.Context) (Result, error) {
	if r.TaskStore == nil {
		return Result{}, fmt.Errorf("background sub-agent requires task store")
	}
	// Reject before any state is created so a refused request leaves nothing
	// behind: no task row, no goroutine, no detached context.
	releaseSlot, ok := backgroundAgents.acquire()
	if !ok {
		return Result{}, backgroundAgents.atCapacityError()
	}
	started := make(chan backgroundStart, 1)
	r.TaskStore = backgroundStartStore{base: r.TaskStore, started: started}
	baseCtx := context.WithoutCancel(ctx)
	var runCtx context.Context
	var cancel context.CancelFunc
	if req.TimeoutMS > 0 {
		runCtx, cancel = context.WithTimeout(baseCtx, time.Duration(req.TimeoutMS)*time.Millisecond)
	} else {
		runCtx, cancel = context.WithCancel(baseCtx)
	}
	go func() {
		// A panic in a detached sub-agent would otherwise take the whole
		// process down (the same class of defect as AUDIT-P0-10, which left
		// this goroutine to AUDIT-P0-14). Recover, release the slot, and let
		// the caller see it as a start failure.
		defer func() {
			releaseSlot()
			cancel()
			if recovered := recover(); recovered != nil {
				select {
				case started <- backgroundStart{err: fmt.Errorf("background sub-agent panicked: %v", recovered)}:
				default:
				}
			}
		}()
		_, err := r.Run(runCtx, req, toolContext)
		if err != nil {
			select {
			case started <- backgroundStart{err: err}:
			default:
			}
		}
	}()
	select {
	case start := <-started:
		if start.err != nil {
			cancel()
			return Result{}, start.err
		}
		return Result{
			AgentName:         start.agentName,
			Model:             start.model,
			Status:            agenttasks.StatusRunning,
			Background:        true,
			AgentMode:         normalizeAgentMode(req.AgentMode),
			SessionID:         start.sessionID,
			OutputFile:        start.outputFile,
			WorktreePath:      start.worktreePath,
			WorktreeBranch:    start.worktreeBranch,
			WorktreeHookBased: req.WorktreeHookBased,
			TaskID:            start.taskID,
			PermissionMode:    start.permissionMode,
		}, nil
	case <-ctx.Done():
		cancel()
		return Result{}, ctx.Err()
	}
}

func AgentBackground(cwd, name string) (bool, error) {
	agent, ok, err := loadAgent(cwd, name)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(name) != "" && !ok {
		return false, unknownSubagentTypeError(cwd, name)
	}
	return agent.Background, nil
}

func (r Runtime) Run(ctx context.Context, req Request, toolContext tools.Context) (returned Result, returnedErr error) {
	runStarted := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var result Result
	var taskID uint64
	var recorder *session.Recorder
	var persistence persistenceState
	var finalStatus = agenttasks.StatusFailed
	var finalErr error
	taskCreated := false
	taskFinished := false
	recorderClosed := false
	closeRecorder := func() {
		if recorder == nil || recorderClosed {
			return
		}
		recorderClosed = true
		persistence.record("close transcript", recorder.Close())
	}
	finishRunTask := func(status string, current Result) Result {
		if taskFinished {
			return current
		}
		taskFinished = true
		closeRecorder()
		persistence.apply(&current)
		return r.finishTask(ctx, req, taskID, status, current)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := fmt.Errorf("sub-agent runtime panicked: %v", recovered)
			observability.Error(ctx, nil, "agent.run.panic", "agentruntime.Runtime.Run", "sub-agent runtime panicked",
				"task_id", taskID,
				"agent", result.AgentName,
				"panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()),
			)
			if taskCreated && taskID != 0 && !taskFinished {
				recordFailureContent(&result, panicErr)
				result = finishRunTask(agenttasks.StatusFailed, result)
				r.emitEvent(context.WithoutCancel(ctx), req, taskID, agenttasks.EventFailed, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"error": panicErr.Error(), "reason": "runtime_panic"}), toolContext.TaskProgress)
			}
			finalStatus = agenttasks.StatusFailed
			finalErr = panicErr
			closeRecorder()
			persistence.apply(&result)
			returned = result
			returnedErr = panicErr
		}
		if taskCreated {
			r.runSubagentStopHook(ctx, req, result, finalStatus, finalErr, runStarted)
			r.emitAgentRunFinishedTelemetry(context.WithoutCancel(ctx), req, result, taskID, finalStatus, finalErr, runStarted)
		}
	}()
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Result{}, fmt.Errorf("prompt is required")
	}
	override := ResolveExecutionOverride(AgentExecutionOverride{Provider: req.Provider, Model: req.Model, Effort: req.Effort, MaxOutputTokens: req.MaxTokens, MaxTurns: req.MaxTurns, TimeoutMS: req.TimeoutMS}, req.Override)
	effectiveProvider := override.Provider
	if effectiveProvider != "" {
		if r.ProviderPreflight != nil {
			if err := r.ProviderPreflight(ctx, effectiveProvider, override.Model); err != nil {
				return Result{}, fmt.Errorf("provider preflight %q: %w", effectiveProvider, err)
			}
		}
		if r.ClientResolver == nil {
			return Result{}, fmt.Errorf("provider override %q requires a client resolver", effectiveProvider)
		}
		client, cleanup, err := r.ClientResolver(ctx, effectiveProvider, firstNonEmpty(override.Model, strings.TrimSpace(r.Model)))
		if err != nil {
			return Result{}, fmt.Errorf("resolve provider %q: %w", effectiveProvider, err)
		}
		if client == nil {
			if cleanup != nil {
				cleanup()
			}
			return Result{}, fmt.Errorf("provider resolver returned nil client for %q", effectiveProvider)
		}
		defer func() {
			if cleanup != nil {
				cleanup()
			}
		}()
		r.Client = client
	}
	if r.Client == nil {
		return Result{}, fmt.Errorf("sub-agent client is not configured")
	}
	// Depth and budget are checked before anything is created (no task row, no
	// recorder, no MCP servers) so a rejected run leaves no residue. The depth
	// is stamped back onto toolContext so runTool's childContext carries it to
	// whatever this sub-agent delegates next.
	depth := toolContext.SubagentDepth + 1
	if maxDepth := MaxSubagentDepth(); depth > maxDepth {
		return Result{}, depthLimitError(depth, maxDepth)
	}
	toolContext.SubagentDepth = depth
	toolContext.ComputerUse = nil
	toolContext.ComputerUseImageSupported = false
	if toolContext.AgentBudget == nil {
		// No session-level budget was wired in (server and eval paths). Fall
		// back to a budget scoped to this sub-agent tree: the pointer travels
		// down through childContext, so nested sub-agents still share it.
		toolContext.AgentBudget = agentbudget.NewDefault()
	}
	budget := toolContext.AgentBudget
	if err := budget.Check(); err != nil {
		return Result{}, err
	}
	maxTokens := r.MaxTokens
	if override.MaxOutputTokens > 0 {
		maxTokens = override.MaxOutputTokens
	}
	if maxTokens <= 0 {
		maxTokens = defaults.CodeMaxTokens
	}
	toolResultLimit := r.ToolResultLimit
	if toolResultLimit <= 0 {
		toolResultLimit = toolresult.DefaultLimit
	}
	toolResultMessageBudget := r.ToolResultMessageBudget
	if toolResultMessageBudget <= 0 {
		toolResultMessageBudget = toolresult.DefaultMessageBudget
	}
	toolResultHistoryBudget := r.ToolResultHistoryBudget
	if toolResultHistoryBudget <= 0 {
		toolResultHistoryBudget = toolresult.DefaultHistoryBudget
	}
	agent, ok, err := loadAgent(req.CWD, req.SubagentType)
	if err != nil {
		return Result{}, err
	}
	if req.SubagentType != "" && !ok {
		return Result{}, unknownSubagentTypeError(req.CWD, req.SubagentType)
	}
	if r.RecorderStore.IsZero() {
		r.RecorderStore = session.DefaultStore()
	}
	if strings.TrimSpace(req.CWD) != "" {
		var recorderErr error
		recorder, recorderErr = r.RecorderStore.NewRecorder(req.CWD)
		if recorderErr != nil {
			return Result{}, fmt.Errorf("create transcript recorder: %w", recorderErr)
		}
	}
	maxTurns := r.MaxTurns
	if override.MaxTurns > 0 {
		maxTurns = override.MaxTurns
	} else if agent.MaxTurns > 0 {
		maxTurns = agent.MaxTurns
	}
	if maxTurns <= 0 {
		maxTurns = defaults.MaxTurns
	}
	model := resolveSubagentModel(firstNonEmpty(override.Model, req.Model), agent.Model, r.Model, config.SubagentModelTiers(req.CWD))
	costRates := subagentCostRates(req.CWD)
	system := subagentBaseSystemPrompt()
	if agent.Prompt != "" {
		system += "\n\nAgent instructions:\n" + agent.Prompt
	}
	system += "\n\n" + subagentEnvDetails(req.CWD, model, subagentEnvOptions{OmitGitStatus: agent.OmitGitStatus})
	if agent.CriticalSystemReminderExperimental != "" {
		system += "\n\nCritical reminder:\n" + agent.CriticalSystemReminderExperimental
	}
	if skillsPrompt, err := agentSkillsPrompt(req.CWD, agent.Skills); err != nil {
		return Result{}, err
	} else if skillsPrompt != "" {
		system += "\n\n" + skillsPrompt
	}
	if memoryPrompt, err := agentMemoryPrompt(req.CWD, firstNonEmpty(agent.Name, req.SubagentType), agent.Memory); err != nil {
		return Result{}, err
	} else if memoryPrompt != "" {
		system += "\n\n" + memoryPrompt
	}
	registry := r.Registry
	cleanupMCP := func() {}
	mcpNames := agentMCPNames(agent.MCPServers)
	if configs := agentMCPConfigs(agent.MCPServers); len(configs) > 0 {
		registry = registry.Clone()
		loader := r.LoadMCPTools
		if loader == nil {
			loader = mcp.LoadTools
		}
		mcpTools, cleanup := loader(ctx, configs)
		if cleanup != nil {
			cleanupMCP = cleanup
		}
		for _, tool := range mcpTools {
			registry.Register(tool)
		}
		if mcpPrompt := agentMCPPrompt(mcpNames, configs, mcpTools); mcpPrompt != "" {
			system += "\n\n" + mcpPrompt
		}
	} else if mcpPrompt := agentMCPPrompt(mcpNames, nil, nil); mcpPrompt != "" {
		system += "\n\n" + mcpPrompt
	}
	defer cleanupMCP()
	allowedTools := append([]string(nil), agent.Tools...)
	deniedTools := append([]string(nil), agent.DisallowedTools...)
	// SendMessage is a thin wrapper that resolves a recipient and then calls
	// AgentMessage, so denying only SendMessage left the A->B channel open to
	// any sub-agent (AUDIT-P1-20). Deny both.
	deniedTools = appendToolNameUnique(deniedTools, "SendMessage")
	deniedTools = appendToolNameUnique(deniedTools, "AgentMessage")
	// Desktop authority is never delegable, even with an explicit agent allowlist.
	deniedTools = appendToolNameUnique(deniedTools, computerusetool.ToolName)
	if registry != nil {
		registry = registry.FilterPolicy(allowedTools, deniedTools)
	}
	compactor := r.newCompactor(req.CWD, model)
	messages := initialSubagentMessages(req.InitialMessages, prompt)
	toolResultReplacements := toolResultReplacementMap(req.InitialToolResultReplacements)
	toolResultSeenIDs := toolresult.ToolResultIDs(req.InitialMessages)
	result = Result{
		AgentName:         strings.TrimSpace(firstNonEmpty(agent.Name, req.SubagentType)),
		Model:             model,
		Provider:          effectiveProvider,
		Effort:            firstNonEmpty(override.Effort, strings.TrimSpace(agent.Effort), defaults.Effort),
		MaxOutputTokens:   maxTokens,
		MaxTurns:          maxTurns,
		TimeoutMS:         override.TimeoutMS,
		OverrideReasons:   append([]string(nil), override.ReasonCodes...),
		PermissionMode:    strings.TrimSpace(agent.PermissionMode),
		AgentMode:         normalizeAgentMode(req.AgentMode),
		WorktreePath:      strings.TrimSpace(req.WorktreePath),
		WorktreeBranch:    strings.TrimSpace(req.WorktreeBranch),
		WorktreeHookBased: req.WorktreeHookBased,
	}
	if recorder != nil {
		result.SessionID = recorder.SessionID
		result.TranscriptPath = recorder.Path
		result.OutputFile = agentOutputFilePath(recorder.Path, recorder.SessionID)
		if err := recorder.Append(session.Entry{Type: "message", Role: "user", Content: prompt}); err != nil {
			closeRecorder()
			return Result{}, fmt.Errorf("persist initial prompt: %w", err)
		}
	}
	taskID = r.createTask(ctx, req, result, recorder)
	taskCreated = true
	progressTaskID := taskID
	if progressTaskID == 0 && r.TaskStore == nil && toolContext.TaskProgress != nil {
		progressTaskID = localProgressTaskSeq.Add(1)
	}
	result.TaskID = taskID
	if r.Controller != nil && taskID != 0 {
		r.Controller.Register(taskID, cancel)
		defer r.Controller.Unregister(taskID)
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "agent.run.started",
		Category:     telemetry.CategoryAgent,
		Source:       "agentruntime.Runtime.Run",
		Status:       telemetry.StatusStarted,
		TraceID:      req.TraceID,
		TenantID:     req.TenantID,
		UserID:       req.UserID,
		SessionID:    req.ParentSessionID,
		ResourceType: "agent_task",
		ResourceID:   fmt.Sprintf("%d", taskID),
		Model:        model,
		Properties: map[string]any{
			"agent_name":   result.AgentName,
			"agent_mode":   result.AgentMode,
			"session_id":   result.SessionID,
			"subagent_key": req.SubagentType,
			"max_turns":    maxTurns,
		},
	})
	subagentStartContext, err := r.runSubagentStartHook(ctx, req, result, runStarted)
	if err != nil {
		recordFailureContent(&result, err)
		result = finishRunTask(agenttasks.StatusFailed, result)
		finalStatus = agenttasks.StatusFailed
		finalErr = err
		return result, err
	}
	if subagentStartContext != "" {
		messages = append(messages, subagentStartHookContextMessage(subagentStartContext))
	}
	emitEvent := func(eventType string, payload map[string]any) {
		r.emitEvent(ctx, req, progressTaskID, eventType, payload, toolContext.TaskProgress)
	}
	// The started event carries only safe task metadata; keep the prompt trimmed
	// to a short preview so TUI observability does not leak full delegated context.
	emitEvent(agenttasks.EventStarted, map[string]any{
		"agent_name":      result.AgentName,
		"model":           model,
		"description":     req.Description,
		"subagent_type":   req.SubagentType,
		"prompt_preview":  truncateEventText(prompt, 160),
		"started_at":      runStarted.UTC().Format(time.RFC3339Nano),
		"max_turns":       maxTurns,
		"max_tokens":      maxTokens,
		"effort":          result.Effort,
		"permission_mode": result.PermissionMode,
	})
	// Overflow recovery is allowed once per sub-agent run; see compactAfterOverflow.
	overflowCompacted := false
	// Loop guard: the same "no new information" detector as the main loop (see
	// docs/loop_guard.md). A sub-agent previously had only MaxTurns, and the
	// shared token budget cannot cover this either — a text-cheap spin barely
	// spends anything while still burning every turn of the delegated task.
	var loopTracker loopguard.Tracker
	activeAgent := &tools.SkillRuntime{Name: result.AgentName, AllowedTools: allowedTools, Model: model, Context: "subagent", Effort: result.Effort}
	agentPolicy := &tools.AgentPolicy{Name: result.AgentName, AllowedTools: allowedTools, DeniedTools: deniedTools, PermissionMode: result.PermissionMode}
	observability.Info(ctx, nil, "agent.run.start", "agentruntime.Runtime.Run", "sub-agent run start",
		"agent", result.AgentName,
		"model", model,
		"prompt_bytes", len(prompt),
	)
	for turn := 1; turn <= maxTurns; turn++ {
		if r.isCancelled(ctx, taskID) {
			err := fmt.Errorf("sub-agent task cancelled")
			recordCancellationContent(&result, err)
			result = finishRunTask(agenttasks.StatusCancelled, result)
			emitEvent(agenttasks.EventCancelled, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"turn": turn, "source": "store"}))
			finalStatus = agenttasks.StatusCancelled
			finalErr = err
			return result, err
		}
		// Stop as soon as the shared budget is spent, before paying for another
		// request. Checked after cancellation so an explicit stop still reports
		// as cancelled rather than as a budget failure.
		if err := budget.Check(); err != nil {
			recordFailureContent(&result, err)
			result = finishRunTask(agenttasks.StatusFailed, result)
			emitEvent(agenttasks.EventFailed, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"error": err.Error(), "turn": turn, "reason": "budget_exhausted"}))
			finalStatus = agenttasks.StatusFailed
			finalErr = err
			return result, err
		}
		if toolContext.AgentMessages != nil {
			for _, pending := range toolContext.AgentMessages(taskID) {
				text := strings.TrimSpace(pending.Content)
				if text == "" {
					continue
				}
				messages = append(messages, anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Agent message from " + firstNonEmpty(pending.FromAgent, "parent") + ":\n" + text}}})
				emitEvent(agenttasks.EventMessage, map[string]any{"turn": turn, "from_agent": pending.FromAgent, "content_preview": truncateEventText(text, 160)})
			}
		}
		messages = toolresult.ApplyMessageBudget(messages, toolresult.BudgetOptions{
			Limit:          toolResultMessageBudget,
			Session:        toolResultSessionRef(recorder),
			SkipToolNames:  registry.ToolResultBudgetSkipNames(),
			Replacements:   toolResultReplacements,
			SeenToolUseIDs: toolResultSeenIDs,
			OnReplacement:  recordContentReplacement(recorder, toolResultReplacements, &persistence),
		})
		messages = toolresult.ApplyHistoryBudget(messages, toolresult.BudgetOptions{
			Limit:          toolResultHistoryBudget,
			Session:        toolResultSessionRef(recorder),
			SkipToolNames:  registry.ToolResultBudgetSkipNames(),
			Replacements:   toolResultReplacements,
			SeenToolUseIDs: toolResultSeenIDs,
			OnReplacement:  recordContentReplacement(recorder, toolResultReplacements, &persistence),
		})
		// Compaction runs after tool-result externalization for the same reason
		// as in the main loop: externalization is free, compaction costs an LLM
		// round. Sub-agents previously had no compaction at all, so a long
		// delegated task simply died on a context-overflow API error.
		messages = r.maybeCompact(ctx, compactor, model, system, defsForCompact(registry), messages, turn, emitEvent)
		requestMessages := withSubagentEvidenceContractMessage(messages)
		requestMessages = withSubagentRuntimeStatusMessages(requestMessages, subagentRuntimeStatusRequest{
			Turn:       turn,
			MaxTurns:   maxTurns,
			LoopStreak: loopTracker.Streak(),
			LoopCall:   loopTracker.Label(),
		})
		result.Turns = turn
		emitEvent(agenttasks.EventTurnStart, map[string]any{"turn": turn, "messages": len(messages)})
		defs := []anthropic.ToolDefinition(nil)
		if registry != nil {
			defs = registry.Definitions()
		}
		if shouldDisableSubagentToolsForFinalTurn(turn, maxTurns, messages) {
			defs = nil
		}
		thinkingConfig := anthropic.ThinkingConfigFromEffort(result.Effort, maxTokens)
		request := anthropic.MessagesRequest{
			Model:     model,
			MaxTokens: maxTokens,
			System:    system,
			Messages:  requestMessages,
			Tools:     defs,
			Thinking:  thinkingConfig,
		}
		if err := r.dumpPromptRequest(req, result, taskID, turn, request, toolResultLimit, toolResultMessageBudget, toolResultHistoryBudget); err != nil {
			recordFailureContent(&result, err)
			result = finishRunTask(agenttasks.StatusFailed, result)
			emitEvent(agenttasks.EventFailed, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"error": err.Error(), "turn": turn}))
			finalStatus = agenttasks.StatusFailed
			finalErr = err
			return result, err
		}
		r.observeRequestCache(ctx, req, taskID, turn, request, thinkingConfig, emitEvent)
		modelStart := time.Now()
		telemetry.Emit(ctx, telemetry.Event{
			Name:         "agent.model.request.started",
			Category:     telemetry.CategoryModel,
			Source:       "agentruntime.Runtime.Run",
			Status:       telemetry.StatusStarted,
			TraceID:      req.TraceID,
			TenantID:     req.TenantID,
			UserID:       req.UserID,
			SessionID:    req.ParentSessionID,
			ResourceType: "agent_task",
			ResourceID:   fmt.Sprintf("%d", taskID),
			Model:        model,
			Properties: map[string]any{
				"agent_name": result.AgentName,
				"turn":       turn,
				"tools":      len(defs),
				"messages":   len(messages),
			},
		})
		stream, err := r.Client.StreamMessages(ctx, request, anthropic.StreamCallbacks{OnText: func(text string) error {
			result.Content += text
			emitEvent(agenttasks.EventTextDelta, map[string]any{"turn": turn, "text": text})
			return nil
		}})
		if err != nil {
			// Reactive overflow fallback, mirroring the main loop: the estimator
			// can undershoot, so force one compaction and retry the turn rather
			// than failing the delegated task outright.
			if next, ok := r.compactAfterOverflow(ctx, compactor, err, model, system, defsForCompact(registry), messages, turn, &overflowCompacted, emitEvent); ok {
				messages = next
				continue
			}
			status := agenttasks.StatusFailed
			switch ctx.Err() {
			case context.DeadlineExceeded:
				status = agenttasks.StatusTimeout
			case context.Canceled:
				status = agenttasks.StatusCancelled
			}
			if status == agenttasks.StatusFailed {
				recordFailureContent(&result, err)
			} else {
				recordStoppedContent(&result, status, err)
			}
			telemetry.Emit(ctx, telemetry.Event{
				Name:         "agent.model.request.finished",
				Category:     telemetry.CategoryModel,
				Source:       "agentruntime.Runtime.Run",
				Status:       telemetry.StatusError,
				TraceID:      req.TraceID,
				TenantID:     req.TenantID,
				UserID:       req.UserID,
				SessionID:    req.ParentSessionID,
				ResourceType: "agent_task",
				ResourceID:   fmt.Sprintf("%d", taskID),
				Model:        model,
				DurationMS:   time.Since(modelStart).Milliseconds(),
				Error:        err.Error(),
				Properties:   map[string]any{"agent_name": result.AgentName, "turn": turn},
			})
			result = finishRunTask(status, result)
			eventType := agenttasks.EventFailed
			switch status {
			case agenttasks.StatusCancelled:
				eventType = agenttasks.EventCancelled
			case agenttasks.StatusTimeout:
				eventType = agenttasks.EventTimeout
			}
			emitEvent(eventType, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"error": err.Error(), "turn": turn}))
			finalStatus = status
			finalErr = err
			return result, err
		}
		turnUsage := usageFromAnthropic(stream.Usage)
		// Calibrate the estimator against the provider's exact prompt size.
		if compactor != nil {
			compactor.ObservePromptTokens(turnUsage.InputTokens)
		}
		result.Usage.add(turnUsage)
		result.CostUSD, result.CostKnown = session.EstimateCost(model, result.Usage.tiers(), costRates)
		turnCostUSD, turnCostKnown := session.EstimateCost(model, turnUsage.tiers(), costRates)
		if !turnCostKnown {
			warnUnpricedModel(ctx, model)
		}
		// The token figure stays the full prompt size on purpose: a token budget
		// measures how much work a fan-out is doing, and cache reads still consume
		// the context window. Only the cost figure is tiered, and it is now the
		// real billable number rather than the ~10x over-estimate.
		budget.Add(turnUsage.InputTokens, turnUsage.OutputTokens, turnCostUSD)
		telemetry.Emit(ctx, telemetry.Event{
			Name:                                "agent.model.request.finished",
			Category:                            telemetry.CategoryModel,
			Source:                              "agentruntime.Runtime.Run",
			Status:                              telemetry.StatusOK,
			TraceID:                             req.TraceID,
			TenantID:                            req.TenantID,
			UserID:                              req.UserID,
			SessionID:                           req.ParentSessionID,
			ResourceType:                        "agent_task",
			ResourceID:                          fmt.Sprintf("%d", taskID),
			Model:                               model,
			DurationMS:                          time.Since(modelStart).Milliseconds(),
			InputTokens:                         turnUsage.InputTokens,
			OutputTokens:                        turnUsage.OutputTokens,
			CacheCreationInputTokens:            turnUsage.CacheCreationInputTokens,
			CacheReadInputTokens:                turnUsage.CacheReadInputTokens,
			CacheCreationEphemeral1hInputTokens: turnUsage.CacheCreationEphemeral1hInputTokens,
			CacheCreationEphemeral5mInputTokens: turnUsage.CacheCreationEphemeral5mInputTokens,
			Properties: map[string]any{
				"agent_name":    result.AgentName,
				"turn":          turn,
				"stop_reason":   stream.StopReason,
				"cost_usd":      turnCostUSD,
				"service_tier":  turnUsage.ServiceTier,
				"inference_geo": turnUsage.InferenceGeo,
				"speed":         turnUsage.Speed,
			},
		})
		if turnUsage.hasTokens() {
			emitEvent(agenttasks.EventUsage, map[string]any{
				"turn":                        turn,
				"model":                       model,
				"input_tokens":                turnUsage.InputTokens,
				"output_tokens":               turnUsage.OutputTokens,
				"cache_creation_input_tokens": turnUsage.CacheCreationInputTokens,
				"cache_read_input_tokens":     turnUsage.CacheReadInputTokens,
				"cache_creation_ephemeral_1h_input_tokens": turnUsage.CacheCreationEphemeral1hInputTokens,
				"cache_creation_ephemeral_5m_input_tokens": turnUsage.CacheCreationEphemeral5mInputTokens,
				"cost_usd":      turnCostUSD,
				"service_tier":  turnUsage.ServiceTier,
				"inference_geo": turnUsage.InferenceGeo,
				"speed":         turnUsage.Speed,
			})
		}
		messages = append(messages, stream.Message)
		persistence.record("append assistant transcript", recordAssistant(recorder, stream.Message.Content))
		for _, block := range stream.Message.Content {
			if block.Type == "text" && result.Content == "" {
				result.Content += block.Text
			}
		}
		toolUses := collectToolUses(stream.Message.Content)
		if len(toolUses) == 0 {
			observability.Info(ctx, nil, "agent.run.finish", "agentruntime.Runtime.Run", "sub-agent run finish",
				"agent", result.AgentName,
				"turns", result.Turns,
				"tool_calls", len(result.ToolCalls),
			)
			result = finishRunTask(agenttasks.StatusCompleted, result)
			emitEvent(agenttasks.EventCompleted, agentTerminalEventPayload(result, time.Since(runStarted), nil))
			finalStatus = agenttasks.StatusCompleted
			return result, nil
		}
		if registry == nil {
			err := fmt.Errorf("sub-agent requested tools but no registry is configured")
			recordFailureContent(&result, err)
			result = finishRunTask(agenttasks.StatusFailed, result)
			emitEvent(agenttasks.EventFailed, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"error": err.Error(), "turn": turn}))
			finalStatus = agenttasks.StatusFailed
			finalErr = err
			return result, err
		}
		toolResults := make([]anthropic.ContentBlock, 0, len(toolUses))
		var toolContextMessages []anthropic.MessageParam
		for _, block := range toolUses {
			if r.isCancelled(ctx, taskID) {
				err := fmt.Errorf("sub-agent task cancelled")
				recordCancellationContent(&result, err)
				result = finishRunTask(agenttasks.StatusCancelled, result)
				emitEvent(agenttasks.EventCancelled, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"turn": turn, "source": "store"}))
				finalStatus = agenttasks.StatusCancelled
				finalErr = err
				return result, err
			}
			// Tool payloads expose names and IDs for progress, but omit full input/output.
			emitEvent(agenttasks.EventToolCall, map[string]any{
				"turn":      turn,
				"tool_id":   block.ID,
				"tool_name": block.Name,
			})
			trace := r.runTool(ctx, registry, block, toolContext, activeAgent, agentPolicy, req, taskID, result.AgentName)
			effectiveToolResultLimit := toolResultLimit
			if tool, ok := registry.Get(block.Name); ok {
				effectiveToolResultLimit = tools.EffectiveResultLimit(tool, toolResultLimit)
			}
			trace.Output = toolresult.Process(trace.Output, toolresult.ProcessOptions{
				ToolName:  block.Name,
				ToolUseID: block.ID,
				Limit:     effectiveToolResultLimit,
				Session:   toolResultSessionRef(recorder),
			})
			result.ToolCalls = append(result.ToolCalls, trace)
			persistence.record("append tool transcript", recordTool(recorder, trace))
			payload := map[string]any{
				"turn":      turn,
				"tool_id":   trace.ID,
				"tool_name": trace.Name,
				"is_error":  trace.IsError,
				"preview":   truncateEventText(trace.Output, 160),
			}
			if tools.IsComputerUseTool(trace.Name) {
				if identity := computerUseIdentity(trace.Output); identity != nil {
					payload["output"] = identity
				}
			}
			emitEvent(agenttasks.EventToolResult, payload)
			toolResults = append(toolResults, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   trace.Output,
				IsError:   trace.IsError,
			})
			if !trace.IsError && len(trace.contextMessages) > 0 {
				toolContextMessages = append(toolContextMessages, trace.contextMessages...)
			}
		}
		messages = append(messages, anthropic.MessageParam{Role: "user", Content: toolResults})
		for _, message := range toolContextMessages {
			messages = append(messages, message)
			persistence.record("append context transcript", recordMessage(recorder, message))
		}
		// Loop guard, evaluated after execution once results are known. Including
		// the results is what keeps a legitimate poll (same input, changing output)
		// from being mistaken for a stuck loop.
		streak := loopTracker.Observe(loopguard.TurnFingerprint(toolUses, toolResults), loopguard.Describe(toolUses))
		if loopTracker.Tripped() {
			err := fmt.Errorf("sub-agent loop guard: no progress for %d turns (the same tool calls and results kept recurring)", streak)
			observability.Error(ctx, nil, "agent.loop_guard_abort", "agentruntime.Runtime.Run", "sub-agent aborted by loop guard",
				"agent", result.AgentName,
				"turns", result.Turns,
				"repeat", streak,
				"error", err,
			)
			recordFailureContent(&result, err)
			result = finishRunTask(agenttasks.StatusFailed, result)
			emitEvent(agenttasks.EventFailed, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"error": err.Error(), "turn": turn, "reason": "loop_guard"}))
			finalStatus = agenttasks.StatusFailed
			finalErr = err
			return result, err
		}
		if loopTracker.TakeWarning() {
			observability.Info(ctx, nil, "agent.loop_guard_warn", "agentruntime.Runtime.Run", "sub-agent making no progress",
				"agent", result.AgentName,
				"turn", turn,
				"repeat", streak,
				"repeated_call", loopTracker.Label(),
			)
		}
	}
	err = fmt.Errorf("sub-agent max turns reached (%d)", maxTurns)
	recordFailureContent(&result, err)
	result = finishRunTask(agenttasks.StatusFailed, result)
	emitEvent(agenttasks.EventFailed, agentTerminalEventPayload(result, time.Since(runStarted), map[string]any{"error": err.Error()}))
	finalStatus = agenttasks.StatusFailed
	finalErr = err
	return result, err
}

func resolveSubagentModel(requestModel, agentModel, parentModel string, tierModels map[string]string) string {
	parentModel = strings.TrimSpace(parentModel)
	if model := resolveSubagentModelSpec(requestModel, parentModel, tierModels); model != "" {
		return model
	}
	if model := resolveSubagentModelSpec(agentModel, parentModel, tierModels); model != "" {
		return model
	}
	if parentModel != "" {
		return parentModel
	}
	return config.DefaultModel()
}

func resolveSubagentModelSpec(spec, parentModel string, tierModels map[string]string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if strings.EqualFold(spec, "inherit") {
		return strings.TrimSpace(parentModel)
	}
	switch strings.ToLower(spec) {
	case "sonnet", "opus", "haiku":
		tier := strings.ToLower(spec)
		// Legacy tier names are aliases, not a vendor model catalog. Explicit
		// mappings apply to every provider; otherwise inherit the session model.
		if configured := strings.TrimSpace(tierModels[tier]); configured != "" {
			return configured
		}
		return strings.TrimSpace(parentModel)
	default:
		return spec
	}
}

// subagentCostRates converts configured per-model pricing into session.Rate so
// cost estimation is meaningful under non-Anthropic providers. Returns nil when
// nothing is configured (callers then fall back to the built-in Anthropic table).
func subagentCostRates(cwd string) map[string]session.Rate {
	return session.ConfiguredRates(cwd)
}

func recordFailureContent(result *Result, err error) {
	if result == nil || err == nil {
		return
	}
	message := "Error: " + err.Error()
	if strings.TrimSpace(result.Content) == "" {
		result.Content = message
		return
	}
	if !strings.Contains(result.Content, err.Error()) {
		result.Content = strings.TrimRight(result.Content, "\n") + "\n\n" + message
	}
}

func recordCancellationContent(result *Result, err error) {
	recordStoppedContent(result, agenttasks.StatusCancelled, err)
}

func recordStoppedContent(result *Result, status string, err error) {
	if result == nil {
		return
	}
	if strings.TrimSpace(result.Content) == "" {
		return
	}
	label := "Cancelled"
	defaultReason := "sub-agent task cancelled"
	if strings.TrimSpace(strings.ToLower(status)) == agenttasks.StatusTimeout {
		label = "Timeout"
		defaultReason = "sub-agent task timed out"
	}
	message := label + ": " + defaultReason
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = label + ": " + err.Error()
	}
	if !strings.Contains(result.Content, message) {
		result.Content = strings.TrimRight(result.Content, "\n") + "\n\n" + message
	}
}

// EnsureCapabilityLoop normalizes terminal sub-agent results that bypass Runtime.finishTask.
func EnsureCapabilityLoop(result Result, status string) Result {
	return ensureCapabilityLoop(result, status)
}

// ResultWithCapabilityLoopDecisionContext appends structured sub-agent decision
// context when the child produced actionable evidence. Plain one-line answers
// stay unchanged so simple Task/Agent calls remain concise.
func ResultWithCapabilityLoopDecisionContext(result Result) string {
	content := strings.TrimSpace(result.Content)
	context := CapabilityLoopDecisionContext(result)
	if context == "" {
		return result.Content
	}
	if content == "" {
		return context
	}
	return strings.TrimRight(result.Content, "\n") + "\n\n" + context
}

func CapabilityLoopDecisionContext(result Result) string {
	if !CapabilityLoopHasActionableEvidence(result.CapabilityLoop) {
		return ""
	}
	payload := map[string]any{
		"capability_loop": result.CapabilityLoop,
	}
	if status := strings.TrimSpace(result.Status); status != "" {
		payload["status"] = status
	}
	if result.TaskID != 0 {
		payload["task_id"] = result.TaskID
	}
	if sessionID := strings.TrimSpace(result.SessionID); sessionID != "" {
		payload["session_id"] = sessionID
	}
	if outputFile := strings.TrimSpace(result.OutputFile); outputFile != "" {
		payload["output_file"] = outputFile
	}
	if transcriptPath := strings.TrimSpace(result.TranscriptPath); transcriptPath != "" {
		payload["transcript_path"] = transcriptPath
	}
	if worktreePath := strings.TrimSpace(result.WorktreePath); worktreePath != "" {
		payload["worktree_path"] = worktreePath
	}
	if worktreeBranch := strings.TrimSpace(result.WorktreeBranch); worktreeBranch != "" {
		payload["worktree_branch"] = worktreeBranch
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ""
	}
	return "<capability_loop>\n" + string(data) + "\n</capability_loop>\nThese structured fields are parent decision context; carry evidence, assumptions, unknowns, verification, risks, and next_action forward before finalizing, retrying, or recovering."
}

func CapabilityLoopHasActionableEvidence(loop *CapabilityLoop) bool {
	if loop == nil {
		return false
	}
	for _, value := range loop.Evidence {
		value = strings.TrimSpace(value)
		if !isCapabilityLoopPlaceholder(value) && !strings.Contains(value, "No structured evidence reported by sub-agent") {
			return true
		}
	}
	for _, value := range loop.Assumptions {
		value = strings.TrimSpace(value)
		if !isCapabilityLoopPlaceholder(value) && !strings.EqualFold(value, "No explicit assumptions reported.") {
			return true
		}
	}
	for _, value := range loop.Unknowns {
		value = strings.TrimSpace(value)
		if !isCapabilityLoopPlaceholder(value) && !strings.EqualFold(value, "No explicit unknowns reported.") {
			return true
		}
	}
	for _, value := range loop.Verification {
		value = strings.TrimSpace(value)
		if !isCapabilityLoopPlaceholder(value) && !strings.EqualFold(value, "No explicit verification reported.") {
			return true
		}
	}
	if len(loop.Risks) > 0 {
		for _, value := range loop.Risks {
			if !isCapabilityLoopPlaceholder(value) {
				return true
			}
		}
	}
	nextAction := strings.TrimSpace(loop.NextAction)
	if isCapabilityLoopPlaceholder(nextAction) {
		return false
	}
	// The failure-path next_actions capabilityLoopFromContent injects when a
	// sub-agent reported none of its own. A next_action the runtime wrote itself
	// is not sub-agent signal, so it alone cannot make the loop actionable.
	//
	// This list must stay in lockstep with the switch in capabilityLoopFromContent
	// below: it used to omit the timeout branch, which inverted the verdict for
	// exactly that status — an entirely empty timeout was judged actionable on the
	// strength of boilerplate the runtime had just written, while an identically
	// empty completed sub-agent was correctly judged not (was TODO-100).
	//
	// The completed branch is deliberately absent: it is a placeholder for the
	// whole protocol, so isCapabilityLoopPlaceholder above already caught it.
	defaultNextActions := []string{
		"Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation.",
		"Parent agent should use any partial evidence cautiously and decide whether a replacement sub-agent is needed.",
		"Parent agent should preserve partial evidence, inspect transcript_path or output_file, then retry with a narrower scope or longer profile only if needed.",
		"Parent agent should decide the next verification step.",
	}
	for _, value := range defaultNextActions {
		if strings.EqualFold(nextAction, value) {
			return false
		}
	}
	return true
}

// isCapabilityLoopPlaceholder defers the placeholder set to
// capabilityloop.IsPlaceholder, which is the one such set for the whole protocol
// (was TODO-101 ②). The private copy this replaced omitted the machine-injected
// "completed" default next_action, so that boilerplate counted as real sub-agent
// signal here while capabilityloop, compact and the TUI all filtered it — enough
// on its own to attach an otherwise contentless decision context to the parent's
// turn.
func isCapabilityLoopPlaceholder(value string) bool {
	return capabilityloop.IsPlaceholder(value)
}

func ensureCapabilityLoop(result Result, status string) Result {
	if result.CapabilityLoop == nil {
		loop := capabilityLoopFromContent(result.Content, status)
		result.CapabilityLoop = &loop
	}
	return result
}

func capabilityLoopFromContent(content, status string) CapabilityLoop {
	loop := parseCapabilityLoop(content)
	if len(loop.Evidence) == 0 {
		if partial := partialEvidenceFromUnstructuredContent(content, status); partial != "" {
			loop.Evidence = append(loop.Evidence, partial)
		} else {
			loop.Evidence = append(loop.Evidence, "No structured evidence reported by sub-agent; parent must verify before relying on findings.")
		}
	}
	if len(loop.Assumptions) == 0 {
		loop.Assumptions = append(loop.Assumptions, "No explicit assumptions reported.")
	}
	if len(loop.Unknowns) == 0 {
		loop.Unknowns = append(loop.Unknowns, "No explicit unknowns reported.")
	}
	if len(loop.Verification) == 0 {
		loop.Verification = append(loop.Verification, "No explicit verification reported.")
	}
	if strings.TrimSpace(loop.NextAction) == "" {
		switch strings.TrimSpace(strings.ToLower(status)) {
		case agenttasks.StatusCompleted:
			loop.NextAction = "Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing."
		case agenttasks.StatusFailed:
			loop.NextAction = "Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation."
		case agenttasks.StatusCancelled:
			loop.NextAction = "Parent agent should use any partial evidence cautiously and decide whether a replacement sub-agent is needed."
		case agenttasks.StatusTimeout:
			loop.NextAction = "Parent agent should preserve partial evidence, inspect transcript_path or output_file, then retry with a narrower scope or longer profile only if needed."
		default:
			loop.NextAction = "Parent agent should decide the next verification step."
		}
	}
	return loop
}

func partialEvidenceFromUnstructuredContent(content, status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
	default:
		return ""
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	status = strings.TrimSpace(strings.ToLower(status))
	return "Partial sub-agent content before " + status + ": " + trimCapabilityLoopText(content, 200)
}

func parseCapabilityLoop(content string) CapabilityLoop {
	var loop CapabilityLoop
	current := ""
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if heading, item, ok := capabilityLoopInlineSection(line); ok {
			switch heading {
			case "evidence":
				loop.Evidence = appendLimitedUnique(loop.Evidence, item, 6)
			case "assumptions":
				loop.Assumptions = appendLimitedUnique(loop.Assumptions, item, 5)
			case "unknowns":
				loop.Unknowns = appendLimitedUnique(loop.Unknowns, item, 5)
			case "verification":
				loop.Verification = appendLimitedUnique(loop.Verification, item, 5)
			case "risks":
				loop.Risks = appendLimitedUnique(loop.Risks, item, 5)
			case "next_action":
				if strings.TrimSpace(loop.NextAction) == "" {
					loop.NextAction = trimCapabilityLoopText(item, 240)
				}
			case "resolved_follow_up":
				if strings.TrimSpace(loop.ResolvedFollowUp) == "" {
					loop.ResolvedFollowUp = trimCapabilityLoopText(item, 240)
				}
			case "supersedes_evidence_id":
				addSupersedesEvidenceID(&loop, item)
			}
			current = heading
			continue
		}
		if heading, ok := capabilityLoopHeading(line); ok {
			current = heading
			continue
		}
		item := capabilityLoopItem(line)
		if item == "" || current == "" {
			continue
		}
		switch current {
		case "evidence":
			loop.Evidence = appendLimitedUnique(loop.Evidence, item, 6)
		case "assumptions":
			loop.Assumptions = appendLimitedUnique(loop.Assumptions, item, 5)
		case "unknowns":
			loop.Unknowns = appendLimitedUnique(loop.Unknowns, item, 5)
		case "verification":
			loop.Verification = appendLimitedUnique(loop.Verification, item, 5)
		case "risks":
			loop.Risks = appendLimitedUnique(loop.Risks, item, 5)
		case "next_action":
			if strings.TrimSpace(loop.NextAction) == "" {
				loop.NextAction = trimCapabilityLoopText(item, 240)
			}
		case "resolved_follow_up":
			if strings.TrimSpace(loop.ResolvedFollowUp) == "" {
				loop.ResolvedFollowUp = trimCapabilityLoopText(item, 240)
			}
		case "supersedes_evidence_id":
			addSupersedesEvidenceID(&loop, item)
		}
	}
	return loop
}

func addSupersedesEvidenceID(loop *CapabilityLoop, id string) {
	id = trimCapabilityLoopText(id, 160)
	if id == "" {
		return
	}
	if strings.TrimSpace(loop.SupersedesEvidenceID) == "" {
		loop.SupersedesEvidenceID = id
		return
	}
	if loop.SupersedesEvidenceID == id {
		return
	}
	for _, existing := range loop.SupersedesEvidenceIDs {
		if strings.TrimSpace(existing) == id {
			return
		}
	}
	loop.SupersedesEvidenceIDs = append(loop.SupersedesEvidenceIDs, id)
}

func capabilityLoopInlineSection(line string) (string, string, bool) {
	candidate := strings.TrimSpace(line)
	candidate = strings.TrimLeft(candidate, "-*• \t")
	candidate = strings.TrimSpace(candidate)
	for i := 0; i < len(candidate); i++ {
		if candidate[i] < '0' || candidate[i] > '9' {
			if i > 0 && (candidate[i] == '.' || candidate[i] == ')') {
				candidate = strings.TrimSpace(candidate[i+1:])
			}
			break
		}
	}
	colonIndex, colonWidth := firstCapabilityLoopColon(candidate)
	if colonIndex <= 0 {
		return "", "", false
	}
	heading, ok := capabilityLoopHeading(candidate[:colonIndex])
	if !ok {
		return "", "", false
	}
	item := capabilityLoopItem(candidate[colonIndex+colonWidth:])
	if item == "" {
		return "", "", false
	}
	return heading, item, true
}

func firstCapabilityLoopColon(line string) (int, int) {
	asciiIndex := strings.Index(line, ":")
	fullWidthIndex := strings.Index(line, "：")
	switch {
	case asciiIndex == -1 && fullWidthIndex == -1:
		return -1, 0
	case asciiIndex == -1:
		return fullWidthIndex, len("：")
	case fullWidthIndex == -1 || asciiIndex < fullWidthIndex:
		return asciiIndex, len(":")
	default:
		return fullWidthIndex, len("：")
	}
}

func capabilityLoopHeading(line string) (string, bool) {
	normalized := strings.ToLower(strings.Trim(line, "#*:： \t"))
	normalized = strings.ReplaceAll(normalized, "_", " ")
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "evidence":
		return "evidence", true
	case "assumption", "assumptions":
		return "assumptions", true
	case "unknown", "unknowns", "assumptions unknowns", "assumptions / unknowns", "assumptions and unknowns":
		return "unknowns", true
	case "verification", "verification checks", "tests", "checks":
		return "verification", true
	case "risk", "risks", "risks next actions", "risks / next actions", "risk next action":
		return "risks", true
	case "next action", "next actions", "recommended next action":
		return "next_action", true
	case "resolved follow up", "resolved followup", "follow up resolution", "followup resolution":
		return "resolved_follow_up", true
	case "supersedes evidence id", "supersede evidence id", "supersedes follow up id", "supersede follow up id", "supersedes id":
		return "supersedes_evidence_id", true
	default:
		return "", false
	}
}

func capabilityLoopItem(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*• \t")
	line = strings.TrimSpace(line)
	for i := 0; i < len(line); i++ {
		if line[i] < '0' || line[i] > '9' {
			if i > 0 && (line[i] == '.' || line[i] == ')') {
				line = strings.TrimSpace(line[i+1:])
			}
			break
		}
	}
	return trimCapabilityLoopText(line, 240)
}

func appendLimitedUnique(values []string, value string, limit int) []string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 || len(values) >= limit {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func trimCapabilityLoopText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func agentTerminalEventPayload(result Result, duration time.Duration, extra map[string]any) map[string]any {
	payload := map[string]any{
		"turns":       result.Turns,
		"tool_calls":  len(result.ToolCalls),
		"duration_ms": duration.Milliseconds(),
		"session_id":  result.SessionID,
	}
	if result.TranscriptPath != "" {
		payload["transcript_path"] = result.TranscriptPath
	}
	if result.OutputFile != "" {
		payload["output_file"] = result.OutputFile
	}
	if result.WorktreePath != "" {
		payload["worktree_path"] = result.WorktreePath
	}
	if result.WorktreeBranch != "" {
		payload["worktree_branch"] = result.WorktreeBranch
	}
	if result.WorktreeHookBased {
		payload["worktree_hook_based"] = result.WorktreeHookBased
	}
	if result.CapabilityLoop != nil {
		payload["capability_loop"] = result.CapabilityLoop
	}
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}

func initialSubagentMessages(initial []anthropic.MessageParam, prompt string) []anthropic.MessageParam {
	messages := append([]anthropic.MessageParam(nil), initial...)
	messages = append(messages, anthropic.MessageParam{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: prompt}},
	})
	return messages
}

func subagentStartHookContextMessage(contextText string) anthropic.MessageParam {
	text := strings.TrimSpace(contextText)
	if text == "" {
		return anthropic.MessageParam{}
	}
	return anthropic.MessageParam{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: "<system-reminder>\nSubagentStart hook additional context: " + text + "\n</system-reminder>"}},
	}
}

func withSubagentEvidenceContractMessage(messages []anthropic.MessageParam) []anthropic.MessageParam {
	if subagentMessagesContainText(messages, "Sub-agent evidence output contract") {
		return messages
	}
	out := make([]anthropic.MessageParam, 0, len(messages)+1)
	out = append(out, messages...)
	out = append(out, anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type: "text",
			Text: `<system-reminder>
Sub-agent evidence output contract:
- Unless the delegated prompt explicitly requires an exact output format, structure your final result with these exact top-level headings: Summary, Evidence, Assumptions, Unknowns, Verification, Risks, and Next action.
- Evidence should cite concrete files, functions, line numbers, commands, tests, transcripts, request dumps, or artifacts where available.
- Put one concise bullet under each heading. If a field is not available, say "None observed" or explain why it remains unverified; do not omit the heading.
- The parent agent extracts these headings into capability_loop context. Missing headings weaken parent planning, compact/resume recovery, and failure handling.
- If the task is failed, cancelled, blocked, or partial, still return usable partial evidence plus remaining unknowns, risks, verification, and the recommended next action.
</system-reminder>`,
		}},
	})
	return out
}

func subagentMessagesContainText(messages []anthropic.MessageParam, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, message := range messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, needle) {
				return true
			}
		}
	}
	return false
}

func toolResultReplacementMap(records []toolresult.ReplacementRecord) map[string]string {
	out := map[string]string{}
	for _, record := range records {
		if strings.TrimSpace(record.Kind) != "tool-result" {
			continue
		}
		toolUseID := strings.TrimSpace(record.ToolUseID)
		if toolUseID != "" && record.Replacement != "" {
			out[toolUseID] = record.Replacement
		}
	}
	return out
}

func recordContentReplacement(recorder *session.Recorder, replacements map[string]string, persistence *persistenceState) func(toolresult.ReplacementRecord) {
	return func(record toolresult.ReplacementRecord) {
		if strings.TrimSpace(record.Kind) != "tool-result" || strings.TrimSpace(record.ToolUseID) == "" || record.Replacement == "" {
			return
		}
		if replacements != nil {
			if existing, ok := replacements[record.ToolUseID]; ok && existing == record.Replacement {
				return
			}
			replacements[record.ToolUseID] = record.Replacement
		}
		if recorder == nil {
			return
		}
		persistence.record("append content replacement transcript", recorder.Append(session.Entry{
			Type: "content_replacement",
			Replacements: []session.ReplacementRecord{{
				Kind:        record.Kind,
				ToolUseID:   record.ToolUseID,
				Replacement: record.Replacement,
			}},
		}))
	}
}

type subagentRuntimeStatusRequest struct {
	Turn       int
	MaxTurns   int
	LoopStreak int
	LoopCall   string
}

func withSubagentRuntimeStatusMessages(messages []anthropic.MessageParam, request subagentRuntimeStatusRequest) []anthropic.MessageParam {
	text := subagentRuntimeStatusText(messages, request)
	if strings.TrimSpace(text) == "" {
		return messages
	}
	out := make([]anthropic.MessageParam, 0, len(messages)+1)
	out = append(out, messages...)
	out = append(out, anthropic.MessageParam{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: text}},
	})
	return out
}

func subagentRuntimeStatusText(messages []anthropic.MessageParam, request subagentRuntimeStatusRequest) string {
	var sections []string
	if section := subagentTurnStatus(messages, request); section != "" {
		sections = append(sections, section)
	}
	// Escalating loop warning, so the sub-agent gets a chance to change course
	// before the hard abort rather than being killed without notice.
	if section := loopguard.Awareness(request.LoopStreak, request.LoopCall); section != "" {
		sections = append(sections, section)
	}
	if section := subagentToolPlanningStatus(messages); section != "" {
		sections = append(sections, section)
	}
	if len(sections) == 0 {
		return ""
	}
	return "<system-reminder>\nCurrent sub-agent runtime status. Use it to finish the delegated task efficiently; do not mention it unless relevant.\n\n" + strings.Join(sections, "\n\n") + "\n</system-reminder>"
}

func subagentTurnStatus(messages []anthropic.MessageParam, request subagentRuntimeStatusRequest) string {
	if request.Turn <= 0 || request.MaxTurns <= 0 || request.Turn < request.MaxTurns {
		return ""
	}
	toolUses, toolResults := countSubagentToolBlocks(messages)
	if toolUses == 0 && toolResults == 0 {
		return ""
	}
	return fmt.Sprintf(`## Sub-agent turn budget reminder
- This is model turn %d of %d, the last turn allowed by the current sub-agent max-turns budget.
- Tools are disabled on this final sub-agent request because prior tool evidence is already present.
- produce the concise final sub-agent answer now using the delegated evidence-first output contract.`, request.Turn, request.MaxTurns)
}

func shouldDisableSubagentToolsForFinalTurn(turn, maxTurns int, messages []anthropic.MessageParam) bool {
	if turn <= 0 || maxTurns <= 0 || turn < maxTurns {
		return false
	}
	toolUses, toolResults := countSubagentToolBlocks(messages)
	return toolUses > 0 || toolResults > 0
}

func subagentToolPlanningStatus(messages []anthropic.MessageParam) string {
	toolUses, toolResults := countSubagentToolBlocks(messages)
	if toolResults == 0 && toolUses == 0 {
		return ""
	}
	return fmt.Sprintf(`## Sub-agent tool planning reminder
- You have already gathered %d tool results from %d tool calls in this sub-agent context.
- If you have enough evidence for the delegated task, stop exploring and synthesize the concise sub-agent result now.
- Before calling Read, use paths returned by LS, Glob, or Grep; do not invent file paths.
- For narrow read-only diagnostics, prefer scoped Grep with output_mode:"content" and line context before broad Read loops.
- Grep patterns use RE2 regular expressions; for several symbol/function names, prefer multiple simple Grep calls or plain alternation over complex unbalanced groups.
- When reading code after Grep/LS, pass offset and limit for the smallest useful line range instead of reading whole large files.`, toolResults, toolUses)
}

func countSubagentToolBlocks(messages []anthropic.MessageParam) (int, int) {
	toolUses := 0
	toolResults := 0
	for _, message := range messages {
		for _, block := range message.Content {
			switch block.Type {
			case "tool_use":
				toolUses++
			case "tool_result":
				toolResults++
			}
		}
	}
	return toolUses, toolResults
}

func (r Runtime) runSubagentStartHook(ctx context.Context, req Request, result Result, started time.Time) (string, error) {
	payload := hooks.Payload{
		TaskID:              result.TaskID,
		AgentID:             agentHookID(result),
		AgentType:           firstNonEmpty(req.SubagentType, result.AgentName),
		AgentName:           result.AgentName,
		SessionID:           result.SessionID,
		AgentTranscriptPath: result.TranscriptPath,
		Status:              agenttasks.StatusRunning,
		DurationMS:          time.Since(started).Milliseconds(),
	}
	hookResult, err := runHookWithRecovery(ctx, hooks.SubagentStart, func() (hooks.Result, error) {
		return r.Hooks.RunWithPayload(ctx, hooks.SubagentStart, req.CWD, payload)
	})
	if err != nil {
		observability.Error(ctx, nil, "hook.subagent_start.error", "agentruntime.Runtime.runSubagentStartHook", "sub-agent start hook failed",
			"task_id", result.TaskID,
			"agent", result.AgentName,
			"error", err,
		)
		return "", err
	}
	return strings.TrimSpace(firstNonEmpty(hookResult.AdditionalContext, hookResult.Message)), nil
}

func (r Runtime) runSubagentStopHook(ctx context.Context, req Request, result Result, status string, runErr error, started time.Time) {
	payload := hooks.Payload{
		TaskID:               result.TaskID,
		AgentID:              agentHookID(result),
		AgentType:            firstNonEmpty(req.SubagentType, result.AgentName),
		AgentName:            result.AgentName,
		SessionID:            result.SessionID,
		AgentTranscriptPath:  result.TranscriptPath,
		Status:               status,
		Result:               result.Content,
		LastAssistantMessage: truncateEventText(result.Content, 2000),
		IsError:              runErr != nil,
		DurationMS:           time.Since(started).Milliseconds(),
	}
	if runErr != nil {
		payload.Error = runErr.Error()
	}
	if _, err := runHookWithRecovery(context.WithoutCancel(ctx), hooks.SubagentStop, func() (hooks.Result, error) {
		return r.Hooks.RunWithPayload(context.WithoutCancel(ctx), hooks.SubagentStop, req.CWD, payload)
	}); err != nil {
		observability.Error(ctx, nil, "hook.subagent_stop.error", "agentruntime.Runtime.runSubagentStopHook", "sub-agent stop hook failed",
			"task_id", result.TaskID,
			"agent", result.AgentName,
			"status", status,
			"error", err,
		)
	}
}

func (r *Runtime) observeRequestCache(ctx context.Context, req Request, taskID uint64, turn int, request anthropic.MessagesRequest, thinkingConfig *anthropic.ThinkingConfig, emitEvent func(string, map[string]any)) {
	report := r.requestCacheTracker.Observe(request, thinkingConfig)
	payload := map[string]any{
		"turn":                turn,
		"signature_hash":      report.SignatureHash,
		"model_hash":          report.ModelHash,
		"system_hash":         report.SystemHash,
		"cache_control_hash":  report.CacheControlHash,
		"tools_hash":          report.ToolsHash,
		"message_prefix_hash": report.MessagePrefixHash,
		"thinking_hash":       report.ThinkingHash,
		"initialized":         report.Initialized,
		"breaks_cache":        report.BreaksCache,
		"model_changed":       report.ModelChanged,
		"system_changed":      report.SystemChanged,
		"cache_changed":       report.CacheChanged,
		"tools_changed":       report.ToolsChanged,
		"message_changed":     report.MessageChanged,
		"thinking_changed":    report.ThinkingChanged,
	}
	if report.BreaksCache {
		observability.Info(ctx, nil, "agent.prompt_cache.request_break", "agentruntime.Runtime.observeRequestCache", "sub-agent request cache-safe signature changed",
			"task_id", taskID,
			"agent", req.SubagentType,
			"turn", turn,
			"signature_hash", report.SignatureHash,
			"model_changed", report.ModelChanged,
			"system_changed", report.SystemChanged,
			"cache_changed", report.CacheChanged,
			"tools_changed", report.ToolsChanged,
			"message_changed", report.MessageChanged,
			"thinking_changed", report.ThinkingChanged,
		)
	} else {
		observability.Debug(ctx, nil, "agent.prompt_cache.request_stable", "agentruntime.Runtime.observeRequestCache", "sub-agent request cache-safe signature stable",
			"task_id", taskID,
			"agent", req.SubagentType,
			"turn", turn,
			"signature_hash", report.SignatureHash,
			"initialized", report.Initialized,
		)
	}
	emitEvent(agenttasks.EventCacheState, payload)
}

func (r Runtime) emitAgentRunFinishedTelemetry(ctx context.Context, req Request, result Result, taskID uint64, status string, runErr error, started time.Time) {
	telemetryStatus := telemetry.StatusOK
	if runErr != nil || status == agenttasks.StatusFailed || status == agenttasks.StatusCancelled || status == agenttasks.StatusTimeout {
		telemetryStatus = telemetry.StatusError
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:                                "agent.run.finished",
		Category:                            telemetry.CategoryAgent,
		Source:                              "agentruntime.Runtime.Run",
		Status:                              telemetryStatus,
		TraceID:                             req.TraceID,
		TenantID:                            req.TenantID,
		UserID:                              req.UserID,
		SessionID:                           req.ParentSessionID,
		ResourceType:                        "agent_task",
		ResourceID:                          fmt.Sprintf("%d", taskID),
		Model:                               result.Model,
		DurationMS:                          time.Since(started).Milliseconds(),
		InputTokens:                         result.Usage.InputTokens,
		OutputTokens:                        result.Usage.OutputTokens,
		CacheCreationInputTokens:            result.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:                result.Usage.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: result.Usage.CacheCreationEphemeral1hInputTokens,
		CacheCreationEphemeral5mInputTokens: result.Usage.CacheCreationEphemeral5mInputTokens,
		Error:                               errorFromRun(runErr),
		Properties: map[string]any{
			"agent_name":      result.AgentName,
			"agent_mode":      result.AgentMode,
			"agent_status":    status,
			"cost_usd":        result.CostUSD,
			"session_id":      result.SessionID,
			"turns":           result.Turns,
			"tool_calls":      len(result.ToolCalls),
			"permission_mode": result.PermissionMode,
		},
	})
}

type backgroundStart struct {
	taskID         uint64
	agentName      string
	model          string
	sessionID      string
	outputFile     string
	worktreePath   string
	worktreeBranch string
	permissionMode string
	err            error
}

type backgroundStartStore struct {
	base    agenttasks.Store
	started chan<- backgroundStart
}

func (s backgroundStartStore) CreateAgentTask(ctx context.Context, input agenttasks.TaskInput) (uint64, error) {
	id, err := s.base.CreateAgentTask(ctx, input)
	start := backgroundStart{
		taskID:    id,
		agentName: input.AgentName,
		model:     input.Model,
		err:       err,
	}
	if input.MetadataJSON != "" {
		var metadata struct {
			PermissionMode string `json:"permission_mode"`
			OutputFile     string `json:"output_file"`
			WorktreePath   string `json:"worktree_path"`
			WorktreeBranch string `json:"worktree_branch"`
		}
		_ = json.Unmarshal([]byte(input.MetadataJSON), &metadata)
		start.permissionMode = metadata.PermissionMode
		start.outputFile = metadata.OutputFile
		start.worktreePath = metadata.WorktreePath
		start.worktreeBranch = metadata.WorktreeBranch
	}
	start.sessionID = input.SubagentSessionKey
	select {
	case s.started <- start:
	default:
	}
	return id, err
}

func (s backgroundStartStore) FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error {
	return s.base.FinishAgentTask(ctx, taskID, status, resultJSON)
}

func (s backgroundStartStore) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	return s.base.AppendAgentTaskEvent(ctx, input)
}

func (s backgroundStartStore) IsAgentTaskCancelled(ctx context.Context, taskID uint64) (bool, error) {
	checker, ok := s.base.(agenttasks.CancellationChecker)
	if !ok {
		return false, nil
	}
	return checker.IsAgentTaskCancelled(ctx, taskID)
}

func (r Runtime) isCancelled(ctx context.Context, taskID uint64) bool {
	if taskID == 0 || r.TaskStore == nil {
		return false
	}
	checker, ok := r.TaskStore.(agenttasks.CancellationChecker)
	if !ok {
		return false
	}
	cancelled, err := checker.IsAgentTaskCancelled(context.WithoutCancel(ctx), taskID)
	if err != nil {
		observability.Error(ctx, nil, "agent.task.cancel_check_error", "agentruntime.Runtime.isCancelled", "check sub-agent cancellation failed", "error", err)
		return false
	}
	return cancelled
}

func (r Runtime) createTask(ctx context.Context, req Request, result Result, recorder *session.Recorder) uint64 {
	if r.TaskStore == nil {
		return 0
	}
	subagentSessionKey := ""
	if recorder != nil {
		subagentSessionKey = recorder.SessionID
	}
	id, err := r.TaskStore.CreateAgentTask(ctx, agenttasks.TaskInput{
		TenantID:           req.TenantID,
		UserID:             req.UserID,
		ParentSessionID:    req.ParentSessionID,
		SubagentSessionKey: subagentSessionKey,
		AgentName:          result.AgentName,
		Description:        req.Description,
		Prompt:             req.Prompt,
		Status:             agenttasks.StatusRunning,
		Model:              result.Model,
		MetadataJSON:       taskMetadataJSON(result),
		TraceID:            req.TraceID,
	})
	if err != nil {
		observability.Error(ctx, nil, "agent.task.create_error", "agentruntime.Runtime.createTask", "create sub-agent task failed", "error", err)
		return 0
	}
	return id
}

func taskMetadataJSON(result Result) string {
	payload := map[string]any{}
	if result.AgentMode != "" {
		payload["agent_mode"] = result.AgentMode
	}
	if result.Effort != "" {
		payload["effort"] = result.Effort
	}
	if result.PermissionMode != "" {
		payload["permission_mode"] = result.PermissionMode
	}
	if result.OutputFile != "" {
		payload["output_file"] = result.OutputFile
	}
	if result.WorktreePath != "" {
		payload["worktree_path"] = result.WorktreePath
	}
	if result.WorktreeBranch != "" {
		payload["worktree_branch"] = result.WorktreeBranch
	}
	if result.WorktreeHookBased {
		payload["worktree_hook_based"] = result.WorktreeHookBased
	}
	if result.Background {
		payload["background"] = result.Background
	}
	if len(payload) == 0 {
		return ""
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeAgentMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "teammate", "in_process", "in-process", "inprocess":
		return "teammate"
	default:
		return "subagent"
	}
}

func agentHookID(result Result) string {
	if result.TaskID > 0 {
		return fmt.Sprintf("agent-%d", result.TaskID)
	}
	if result.SessionID != "" {
		return result.SessionID
	}
	return result.AgentName
}

func (r Runtime) appendEvent(ctx context.Context, req Request, taskID uint64, eventType string, payload map[string]any) {
	r.emitEvent(ctx, req, taskID, eventType, payload, nil)
}

func (r Runtime) emitEvent(ctx context.Context, req Request, taskID uint64, eventType string, payload map[string]any, progress func(agenttasks.EventInput)) {
	if r.TaskStore == nil || taskID == 0 {
		if progress != nil {
			progress(agenttasks.EventInput{
				TenantID:    req.TenantID,
				UserID:      req.UserID,
				TaskID:      taskID,
				EventType:   eventType,
				PayloadJSON: marshalEventPayload(payload),
				TraceID:     req.TraceID,
			})
		}
		return
	}
	input := agenttasks.EventInput{
		TenantID:    req.TenantID,
		UserID:      req.UserID,
		TaskID:      taskID,
		EventType:   eventType,
		PayloadJSON: marshalEventPayload(payload),
		TraceID:     req.TraceID,
	}
	if input.PayloadJSON == "" && len(payload) > 0 {
		return
	}
	if progress != nil {
		progress(input)
	}
	storeCtx := context.WithoutCancel(ctx)
	if _, err := r.TaskStore.AppendAgentTaskEvent(storeCtx, input); err != nil {
		observability.Error(ctx, nil, "agent.task.event_error", "agentruntime.Runtime.appendEvent", "append sub-agent event failed", "error", err)
	}
}

func marshalEventPayload(payload map[string]any) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

// computerUseIdentity extracts stable identity fields from a ComputerUse tool
// result output so they are available in persisted tool_result events without
// retaining the full (potentially large, screenshot-bearing) output. The
// acceptance harness and observability tools read window_id, bundle_id,
// target_id, outcome, and error_code from the event payload.
func computerUseIdentity(output string) map[string]any {
	return computeruse.ResultMetadata(output)
}

func truncateEventText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func (r Runtime) finishTask(ctx context.Context, req Request, taskID uint64, status string, result Result) Result {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		status = agenttasks.StatusTimeout
	case context.Canceled:
		status = agenttasks.StatusCancelled
	}
	result.Status = status
	result = ensureCapabilityLoop(result, status)
	result = cleanupWorktreeIfUnchanged(ctx, req, result)
	if r.TaskStore == nil || taskID == 0 {
		return result
	}
	data, err := json.Marshal(result)
	if err != nil {
		observability.Error(ctx, nil, "agent.task.result_marshal_error", "agentruntime.Runtime.finishTask", "marshal sub-agent result failed", "error", err)
		return result
	}
	var outputPersistenceErrors []string
	stateWritten := false
	if err := writeAgentOutputStateFile(result.OutputFile, status, data); err != nil {
		observability.Error(ctx, nil, "agent.task.output_state_file_error", "agentruntime.Runtime.finishTask", "write sub-agent output state file failed", "path", result.OutputFile, "error", err)
		outputPersistenceErrors = append(outputPersistenceErrors, "write output state: "+err.Error())
	} else {
		stateWritten = true
	}
	if err := writeAgentOutputFile(result.OutputFile, result.Content); err != nil {
		observability.Error(ctx, nil, "agent.task.output_file_error", "agentruntime.Runtime.finishTask", "write sub-agent output file failed", "path", result.OutputFile, "error", err)
		outputPersistenceErrors = append(outputPersistenceErrors, "write output: "+err.Error())
	}
	if len(outputPersistenceErrors) > 0 {
		result.PersistenceDegraded = true
		result.PersistenceError = strings.Join(outputPersistenceErrors, "; ")
		if degradedData, marshalErr := json.Marshal(result); marshalErr == nil {
			data = degradedData
			if stateWritten {
				_ = writeAgentOutputStateFile(result.OutputFile, status, data)
			}
		}
	}
	storeCtx := context.WithoutCancel(ctx)
	if err := r.TaskStore.FinishAgentTask(storeCtx, taskID, status, string(data)); err != nil {
		observability.Error(ctx, nil, "agent.task.finish_error", "agentruntime.Runtime.finishTask", "finish sub-agent task failed", "error", err)
	}
	return result
}

func cleanupWorktreeIfUnchanged(ctx context.Context, req Request, result Result) Result {
	info := agentworktree.Info{
		Path:       firstNonEmpty(result.WorktreePath, req.WorktreePath),
		Branch:     firstNonEmpty(result.WorktreeBranch, req.WorktreeBranch),
		HeadCommit: req.WorktreeHeadCommit,
		GitRoot:    req.WorktreeGitRoot,
		HookBased:  req.WorktreeHookBased,
	}
	kept, err := agentworktree.CleanupIfUnchanged(context.WithoutCancel(ctx), info)
	if err != nil {
		observability.Error(ctx, nil, "agent.worktree.cleanup_error", "agentruntime.cleanupWorktreeIfUnchanged", "cleanup unchanged sub-agent worktree failed", "path", info.Path, "error", err)
	}
	result.WorktreePath = kept.Path
	result.WorktreeBranch = kept.Branch
	return result
}

func agentOutputFilePath(transcriptPath, sessionID string) string {
	transcriptPath = strings.TrimSpace(transcriptPath)
	sessionID = strings.TrimSpace(sessionID)
	if transcriptPath == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(transcriptPath), sessionID+".output")
}

func writeAgentOutputFile(path, content string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return atomicWriteFile(path, []byte(content), 0600)
}

func writeAgentOutputStateFile(outputFile, status string, resultJSON []byte) error {
	outputFile = strings.TrimSpace(outputFile)
	if outputFile == "" {
		return nil
	}
	payload := map[string]any{
		"status": strings.TrimSpace(status),
	}
	if len(resultJSON) > 0 && json.Valid(resultJSON) {
		payload["result"] = json.RawMessage(resultJSON)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return atomicWriteFile(outputFile+".state.json", data, 0600)
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func loadAgent(cwd, name string) (agents.Agent, bool, error) {
	normalized := strings.TrimSpace(name)
	if normalized == "" {
		agent, ok := agents.BuiltIn("general-purpose")
		return agent, ok, nil
	}
	agent, ok, err := agents.Load(cwd, name)
	// Models sometimes hallucinate "default" as a sentinel for the default
	// agent; alias it to general-purpose unless a real agent claims the name.
	if err == nil && !ok && strings.EqualFold(normalized, "default") {
		agent, ok = agents.BuiltIn("general-purpose")
	}
	return agent, ok, err
}

func unknownSubagentTypeError(cwd, name string) error {
	names := []string{"Explore", "Plan", "general-purpose"}
	if list, err := agents.List(cwd); err == nil && len(list) > 0 {
		names = names[:0]
		for _, agent := range list {
			if trimmed := strings.TrimSpace(agent.Name); trimmed != "" {
				names = append(names, trimmed)
			}
		}
	}
	return fmt.Errorf("unknown subagent_type: %s (available: %s; omit subagent_type for the default general-purpose agent)", name, strings.Join(names, ", "))
}

func appendToolNameUnique(names []string, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return names
	}
	for _, existing := range names {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return names
		}
	}
	return append(names, name)
}

func subagentBaseSystemPrompt() string {
	return `You are a focused niuma sub-agent. Complete only the delegated task and return a concise result.

Treat the delegated prompt as your full scope. Do not silently infer missing background from the parent conversation; if needed context is absent, say what is unknown instead of guessing.

Return an evidence-first result for the parent agent to synthesize. Use these exact section labels:
- Summary: the direct answer or current status.
- Evidence: concrete file paths, function/type names, line numbers, commands, tests, or transcript/request-dump references when available.
- For implementation-level claims, observe the relevant code body with Read or Grep output_mode:"content"; a file list alone is not implementation evidence.
- Assumptions: assumptions you made while working.
- Unknowns: anything you could not prove from the delegated context and tools.
- Verification: tests or checks run, or why none were run.
- Risks: only risks the parent agent must consider before answering the user.
- Next action: the single next action the parent should take.

Do not omit these headings. Put one concise bullet under each heading, using "None observed" or an explicit reason when a field is unavailable. The parent runtime extracts these headings into capability_loop context for follow-up planning, compact/resume recovery, and failure handling.

Do not fabricate evidence or claim a file/function/test was checked unless you actually observed it. The parent agent is responsible for the final user-facing synthesis.`
}

type subagentEnvOptions struct {
	OmitGitStatus bool
}

func subagentEnvDetails(cwd, model string, options subagentEnvOptions) string {
	notes := strings.Join([]string{
		"Notes:",
		"- Agent threads may run shell commands from a reset working directory; use absolute file paths for file and shell operations.",
		"- In your final response, share file paths as absolute paths when they are relevant to the delegated task.",
		"- Do not use emojis.",
		"- Do not use a colon before tool calls.",
	}, "\n")
	parts := []string{
		notes,
		subagentEnvInfoSection(cwd, model),
	}
	if !options.OmitGitStatus {
		snapshot := gitcontext.Snapshot(context.Background(), cwd)
		if snapshot != "" {
			parts = append(parts, snapshot)
		}
	}
	return strings.Join(parts, "\n\n")
}

func subagentEnvInfoSection(cwd, model string) string {
	var lines []string
	lines = append(lines, "# Environment")
	if strings.TrimSpace(cwd) != "" {
		lines = append(lines, "Current working directory: "+strings.TrimSpace(cwd))
	}
	lines = append(lines, "Date: "+time.Now().Format("2006-01-02"))
	if strings.TrimSpace(model) != "" {
		lines = append(lines, "Model: "+strings.TrimSpace(model))
	}
	return strings.Join(lines, "\n")
}

func agentMCPConfigs(specs []agents.MCPServerSpec) map[string]config.MCPServerConfig {
	if len(specs) == 0 {
		return nil
	}
	out := map[string]config.MCPServerConfig{}
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			name = stringFromMap(spec.Config, "name")
		}
		if name == "" {
			continue
		}
		cfg := mcpServerConfigFromMap(spec.Config)
		if cfg.Command == "" && cfg.URL == "" {
			continue
		}
		out[name] = cfg
	}
	return out
}

func agentMCPNames(specs []agents.MCPServerSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			name = stringFromMap(spec.Config, "name")
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func mcpServerConfigFromMap(raw map[string]any) config.MCPServerConfig {
	if len(raw) == 0 {
		return config.MCPServerConfig{}
	}
	return config.MCPServerConfig{
		Type:    stringFromMap(raw, "type"),
		Command: stringFromMap(raw, "command"),
		Args:    stringSliceFromMap(raw, "args"),
		Env:     stringMapFromMap(raw, "env"),
		URL:     stringFromMap(raw, "url"),
		Headers: stringMapFromMap(raw, "headers"),
	}
}

func agentMCPPrompt(names []string, configs map[string]config.MCPServerConfig, loaded []tools.Tool) string {
	if len(names) == 0 {
		return ""
	}
	byServer := map[string][]string{}
	for _, tool := range loaded {
		parts := strings.Split(tool.Name(), "__")
		if len(parts) >= 3 && parts[0] == "mcp" {
			byServer[parts[1]] = append(byServer[parts[1]], tool.Name())
		}
	}
	var lines []string
	lines = append(lines, "# Agent MCP Servers")
	lines = append(lines, "The following MCP servers are scoped to this sub-agent run. Treat their tools as agent-local capabilities.")
	for _, name := range names {
		toolNames := byServer[safeMCPName(name)]
		sort.Strings(toolNames)
		if len(toolNames) == 0 {
			if _, ok := configs[name]; ok {
				lines = append(lines, "- "+name+": configured; no tools were loaded")
			} else {
				lines = append(lines, "- "+name+": referenced from inherited MCP configuration")
			}
			continue
		}
		lines = append(lines, "- "+name+": "+strings.Join(toolNames, ", "))
	}
	return strings.Join(lines, "\n")
}

func agentSkillsPrompt(cwd string, names []string) (string, error) {
	if len(names) == 0 {
		return "", nil
	}
	var parts []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		skill, ok, err := skills.Load(cwd, name)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("agent skill %q not found", name)
		}
		content := strings.TrimSpace(skill.Content)
		if content == "" {
			continue
		}
		parts = append(parts, "## "+skill.Name+"\n"+content)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "# Preloaded Skills\nThe following skills are preloaded for this sub-agent only. Follow them when relevant to the delegated task.\n\n" + strings.Join(parts, "\n\n"), nil
}

func agentMemoryPrompt(cwd, agentName, scope string) (string, error) {
	docs, err := memory.LoadAgent(cwd, agentName, scope)
	if err != nil {
		return "", err
	}
	if len(docs) == 0 {
		return "", nil
	}
	return "# Agent Memory\nThe following memory files are scoped to this sub-agent. Follow them as durable agent guidance unless they conflict with higher-priority instructions.\n\n" + memory.SystemAddendumFromDocuments(docs), nil
}

func stringFromMap(raw map[string]any, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func stringSliceFromMap(raw map[string]any, key string) []string {
	value, ok := raw[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	default:
		return nil
	}
}

func stringMapFromMap(raw map[string]any, key string) map[string]string {
	value, ok := raw[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			if strings.TrimSpace(key) != "" {
				out[key] = value
			}
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			if strings.TrimSpace(key) != "" {
				out[key] = fmt.Sprint(value)
			}
		}
		return out
	default:
		return nil
	}
}

func safeMCPName(name string) string {
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, name)
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	name = strings.Trim(name, "_")
	if name == "" {
		return "unnamed"
	}
	return name
}

func collectToolUses(blocks []anthropic.ContentBlock) []anthropic.ContentBlock {
	var out []anthropic.ContentBlock
	for _, block := range blocks {
		if block.Type == "tool_use" {
			out = append(out, block)
		}
	}
	return out
}

func (r Runtime) runTool(ctx context.Context, registry *tools.Registry, block anthropic.ContentBlock, parent tools.Context, activeAgent *tools.SkillRuntime, agentPolicy *tools.AgentPolicy, req Request, taskID uint64, agentName string) (trace ToolTrace) {
	trace = ToolTrace{ID: block.ID, Name: block.Name, Input: string(block.Input)}
	start := time.Now()
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "agent.tool.execution.started",
		Category:     telemetry.CategoryTool,
		Source:       "agentruntime.Runtime.runTool",
		Status:       telemetry.StatusStarted,
		TraceID:      req.TraceID,
		TenantID:     req.TenantID,
		UserID:       req.UserID,
		SessionID:    req.ParentSessionID,
		ResourceType: "agent_task",
		ResourceID:   fmt.Sprintf("%d", taskID),
		ToolName:     block.Name,
		Properties:   map[string]any{"agent_name": agentName, "tool_id": block.ID},
	})
	defer func() {
		if recovered := recover(); recovered != nil {
			trace.Output = fmt.Sprintf("tool %q panicked: %v", block.Name, recovered)
			trace.IsError = true
			trace.contextMessages = nil
			observability.Error(ctx, nil, "agent.tool.panic", "agentruntime.Runtime.runTool", "tool execution panicked",
				"tool", block.Name,
				"tool_id", block.ID,
				"task_id", taskID,
				"agent", agentName,
				"panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()),
			)
		}
		status := telemetry.StatusOK
		if trace.IsError {
			status = telemetry.StatusError
		}
		telemetry.Emit(ctx, telemetry.Event{
			Name:         "agent.tool.execution.finished",
			Category:     telemetry.CategoryTool,
			Source:       "agentruntime.Runtime.runTool",
			Status:       status,
			TraceID:      req.TraceID,
			TenantID:     req.TenantID,
			UserID:       req.UserID,
			SessionID:    req.ParentSessionID,
			ResourceType: "agent_task",
			ResourceID:   fmt.Sprintf("%d", taskID),
			ToolName:     block.Name,
			DurationMS:   time.Since(start).Milliseconds(),
			Error:        errorTextForTelemetry(trace.IsError, trace.Output),
			Properties:   map[string]any{"agent_name": agentName, "tool_id": block.ID},
		})
	}()
	tool, ok := registry.Get(block.Name)
	if !ok {
		trace.Output = "unknown tool: " + block.Name
		trace.IsError = true
		return trace
	}
	input := block.Input
	hookResult, err := runHookWithRecovery(ctx, hooks.PreToolUse, func() (hooks.Result, error) {
		return r.Hooks.RunWithPayload(ctx, hooks.PreToolUse, req.CWD, hooks.Payload{ToolName: block.Name, Input: block.Input})
	})
	if err != nil {
		trace.Output = err.Error()
		trace.IsError = true
		return trace
	}
	if strings.EqualFold(hookResult.PermissionDecision, "deny") {
		trace.Output = firstNonEmpty(hookResult.PermissionDecisionReason, hookResult.Message, "tool denied by PreToolUse hook")
		trace.IsError = true
		return trace
	}
	if len(hookResult.UpdatedInput) > 0 {
		input = hookResult.UpdatedInput
		trace.Input = string(input)
	}
	if decision := toolpolicy.Check(block.Name, input, req.CWD, parent.SharedStateAuthorization); decision != nil {
		trace.Output = decision.Message
		trace.IsError = true
		return trace
	}
	childContext := parent
	childContext.ComputerUse = nil
	childContext.ComputerUseImageSupported = false
	if strings.TrimSpace(req.CWD) != "" {
		childContext.CWD = req.CWD
	}
	childContext.ActiveSkill = activeAgent
	childContext.AgentPolicy = agentPolicy
	res := tool.Run(ctx, input, childContext)
	event := hooks.PostToolUse
	if res.IsError {
		event = hooks.PostToolUseFailure
	}
	if _, err := runHookWithRecovery(ctx, event, func() (hooks.Result, error) {
		return r.Hooks.RunWithPayload(ctx, event, req.CWD, hooks.Payload{ToolName: block.Name, Input: input, Result: res.Content, IsError: res.IsError})
	}); err != nil && !res.IsError {
		res = tools.Result{Content: err.Error(), IsError: true}
	}
	trace.Output = res.Content
	trace.IsError = res.IsError
	trace.contextMessages = append([]anthropic.MessageParam(nil), res.ContextMessages...)
	return trace
}

func runHookWithRecovery(ctx context.Context, event string, run func() (hooks.Result, error)) (result hooks.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s hook panicked: %v", event, recovered)
			observability.Error(ctx, nil, "hook.panic", "agentruntime.runHookWithRecovery", "hook execution panicked",
				"event", event,
				"panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()),
			)
		}
	}()
	return run()
}

func recordAssistant(recorder *session.Recorder, blocks []anthropic.ContentBlock) error {
	if recorder == nil {
		return nil
	}
	var firstErr error
	for _, block := range blocks {
		var err error
		switch block.Type {
		case "text":
			if block.Text != "" {
				err = recorder.Append(session.Entry{Type: "message", Role: "assistant", Content: block.Text})
			}
		case "tool_use":
			err = recorder.Append(session.Entry{Type: "tool_call", ToolID: block.ID, ToolName: block.Name, Content: string(block.Input)})
		}
		if firstErr == nil && err != nil {
			firstErr = err
		}
	}
	return firstErr
}

func recordTool(recorder *session.Recorder, trace ToolTrace) error {
	if recorder == nil {
		return nil
	}
	return recorder.Append(session.Entry{Type: "tool_result", ToolID: trace.ID, ToolName: trace.Name, Content: trace.Output, IsError: trace.IsError})
}

func recordMessage(recorder *session.Recorder, message anthropic.MessageParam) error {
	if recorder == nil {
		return nil
	}
	if message.Role != "user" {
		return nil
	}
	var firstErr error
	for _, block := range message.Content {
		var err error
		switch block.Type {
		case "text":
			if block.Text != "" {
				err = recorder.Append(session.Entry{Type: "message", Role: "user", Content: block.Text})
			}
		case "tool_result":
			err = recorder.Append(session.Entry{Type: "tool_result", ToolID: block.ToolUseID, Content: block.Content, IsError: block.IsError})
		}
		if firstErr == nil && err != nil {
			firstErr = err
		}
	}
	return firstErr
}

func toolResultSessionRef(recorder *session.Recorder) toolresult.SessionRef {
	if recorder == nil {
		return toolresult.SessionRef{}
	}
	return toolresult.SessionRef{
		SessionID:      recorder.SessionID,
		TranscriptPath: recorder.Path,
	}
}

// usageFromAnthropic sums every input tier into InputTokens on purpose: it is
// the full prompt size, which ObservePromptTokens calibrates the context
// estimator against. Cost is priced from the tiers via Usage.tiers, not from
// this sum (AUDIT-P0-15).
func usageFromAnthropic(usage anthropic.Usage) Usage {
	return Usage{
		InputTokens:                         usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens,
		OutputTokens:                        usage.OutputTokens,
		CacheCreationInputTokens:            usage.CacheCreationInputTokens,
		CacheReadInputTokens:                usage.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: firstNonZero(usage.CacheCreationEphemeral1hInputTokens, usage.CacheCreation.Ephemeral1hInputTokens),
		CacheCreationEphemeral5mInputTokens: firstNonZero(usage.CacheCreationEphemeral5mInputTokens, usage.CacheCreation.Ephemeral5mInputTokens),
		ServiceTier:                         usage.ServiceTier,
		InferenceGeo:                        usage.InferenceGeo,
		Speed:                               usage.Speed,
	}
}

// tiers converts the recorded counters into disjoint billing tiers. InputTokens
// here is the whole prompt, so the cache tiers have to be subtracted back out.
func (u Usage) tiers() session.TokenUsage {
	return session.ReportedUsage{
		InputTokens:              u.InputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		CacheCreation5mTokens:    u.CacheCreationEphemeral5mInputTokens,
		CacheCreation1hTokens:    u.CacheCreationEphemeral1hInputTokens,
		OutputTokens:             u.OutputTokens,
	}.Tiers()
}

// warnUnpricedModel reports once per model that cost observability is blind for
// it. Silently reporting $0 makes an unpriced provider look free (AUDIT-P0-15).
func warnUnpricedModel(ctx context.Context, model string) {
	if _, seen := unpricedModelsWarned.LoadOrStore(model, true); seen {
		return
	}
	observability.Warn(ctx, nil, "agent.cost.model_unpriced", "agentruntime.Runtime.Run",
		"no pricing known for model; sub-agent cost is reported as unknown, not zero",
		"model", model, "hint", "set settings.modelPricing["+model+"]")
}

var unpricedModelsWarned sync.Map

func (u *Usage) add(next Usage) {
	u.InputTokens += next.InputTokens
	u.OutputTokens += next.OutputTokens
	u.CacheCreationInputTokens += next.CacheCreationInputTokens
	u.CacheReadInputTokens += next.CacheReadInputTokens
	u.CacheCreationEphemeral1hInputTokens += next.CacheCreationEphemeral1hInputTokens
	u.CacheCreationEphemeral5mInputTokens += next.CacheCreationEphemeral5mInputTokens
	u.ServiceTier = firstNonEmpty(next.ServiceTier, u.ServiceTier)
	u.InferenceGeo = firstNonEmpty(next.InferenceGeo, u.InferenceGeo)
	u.Speed = firstNonEmpty(next.Speed, u.Speed)
}

func (u Usage) hasTokens() bool {
	return u.InputTokens != 0 || u.OutputTokens != 0 || u.CacheCreationInputTokens != 0 || u.CacheReadInputTokens != 0
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func errorFromRun(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errorTextForTelemetry(isError bool, output string) string {
	if !isError {
		return ""
	}
	return truncateEventText(output, 1024)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func MarshalResult(result Result) string {
	data, err := json.Marshal(result)
	if err != nil {
		return result.Content
	}
	return string(data)
}
