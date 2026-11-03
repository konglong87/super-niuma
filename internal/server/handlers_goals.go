package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "github.com/konglong87/super-niuma/docs"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func tenantGoalsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goals", "server.tenantGoalsHandler", "handle tenant goals")
		switch r.Method {
		case http.MethodGet:
			filter, err := tenantGoalListFilter(r)
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			items, err := opts.TenantService.ListGoals(r.Context(), filter, parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req goalCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			input, err := req.toCreateInput()
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			item, err := opts.TenantService.CreateGoal(r.Context(), input)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			event, err := goal.NewEvent(goal.EventInput{GoalID: item.ID, Type: goal.EventGoalStarted, Message: "goal started", SessionID: item.SessionID, Status: item.Status})
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := opts.TenantService.AppendGoalEvent(r.Context(), event); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			broadcastTenantGoalEvent(r, opts, event)
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.goal.create", "goal", item.ID)
			writeJSON(w, item)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantGoalHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goal", "server.tenantGoalHandler", "handle tenant goal")
		goalID, ok := tenantGoalIDFromPath(r.URL.Path, "")
		if !ok {
			writeTenantError(w, http.StatusBadRequest, errMsgGoalIDRequired)
			return
		}
		switch r.Method {
		case http.MethodGet:
			item, err := opts.TenantService.GetGoal(r.Context(), goalID)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodPatch:
			current, err := opts.TenantService.GetGoal(r.Context(), goalID)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			var req goalUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			updated, changed, err := req.apply(current)
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !changed {
				writeTenantError(w, http.StatusBadRequest, "at least one goal field is required")
				return
			}
			if err := opts.TenantService.UpdateGoal(r.Context(), updated); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			if updated.Status != current.Status {
				event, err := goal.NewEvent(goal.EventInput{GoalID: updated.ID, Type: goal.EventStatusChanged, Message: "goal status changed", SessionID: updated.SessionID, Status: updated.Status, Reason: updated.LastReason, NextAction: updated.LastNextAction})
				if err != nil {
					writeTenantError(w, http.StatusBadRequest, err.Error())
					return
				}
				if err := opts.TenantService.AppendGoalEvent(r.Context(), event); err != nil {
					writeTenantServiceError(w, err)
					return
				}
				broadcastTenantGoalEvent(r, opts, event)
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.goal.update", "goal", updated.ID)
			writeJSON(w, updated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantGoalEventsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goal_events", "server.tenantGoalEventsHandler", "handle tenant goal events")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		goalID, ok := tenantGoalIDFromPath(r.URL.Path, "events")
		if !ok {
			writeTenantError(w, http.StatusBadRequest, errMsgGoalIDRequired)
			return
		}
		items, err := opts.TenantService.ListGoalEvents(r.Context(), goalID, parseLimit(r.URL.Query().Get(paramLimit)))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func tenantGoalPlanHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goal_plan", "server.tenantGoalPlanHandler", "handle tenant goal plan")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		goalID, ok := tenantGoalIDFromPath(r.URL.Path, "plan")
		if !ok {
			writeTenantError(w, http.StatusBadRequest, errMsgGoalIDRequired)
			return
		}
		plan, exists, err := opts.TenantService.GetPlan(r.Context(), goalID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if !exists {
			writeTenantError(w, http.StatusNotFound, "goal plan not found")
			return
		}
		writeJSON(w, plan)
	})
}

func tenantGoalEvidenceHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goal_evidence", "server.tenantGoalEvidenceHandler", "handle tenant goal evidence")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		goalID, ok := tenantGoalIDFromPath(r.URL.Path, "evidence")
		if !ok {
			writeTenantError(w, http.StatusBadRequest, errMsgGoalIDRequired)
			return
		}
		items, err := opts.TenantService.ListEvidence(r.Context(), goalID, parseLimit(r.URL.Query().Get(paramLimit)))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": items})
	})
}

func tenantGoalStopHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goal_stop", "server.tenantGoalStopHandler", "stop tenant goal")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		goalID, ok := tenantGoalIDFromPath(r.URL.Path, "stop")
		if !ok {
			writeTenantError(w, http.StatusBadRequest, errMsgGoalIDRequired)
			return
		}
		current, err := opts.TenantService.GetGoal(r.Context(), goalID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		current.Status = goal.StatusStopped
		current.LastReason = "stopped by user"
		current.UpdatedAt = time.Now().UTC()
		if err := opts.TenantService.UpdateGoal(r.Context(), current); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		event, err := goal.NewEvent(goal.EventInput{GoalID: current.ID, Type: goal.EventGoalStopped, Message: "goal stopped", SessionID: current.SessionID, Status: current.Status, Reason: current.LastReason})
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := opts.TenantService.AppendGoalEvent(r.Context(), event); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		broadcastTenantGoalEvent(r, opts, event)
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.goal.stop", "goal", current.ID)
		writeJSON(w, current)
	})
}

func tenantGoalResumeHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goal_resume", "server.tenantGoalResumeHandler", "resume tenant goal")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		goalID, ok := tenantGoalIDFromPath(r.URL.Path, "resume")
		if !ok {
			writeTenantError(w, http.StatusBadRequest, errMsgGoalIDRequired)
			return
		}
		current, err := opts.TenantService.GetGoal(r.Context(), goalID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		force := parseBool(r.URL.Query().Get("force"))
		if current.Status == goal.StatusComplete {
			writeTenantError(w, http.StatusConflict, "goal is complete and cannot be resumed")
			return
		}
		if current.Status == goal.StatusFailed && !force {
			writeTenantError(w, http.StatusConflict, "goal is failed; use force=true to resume")
			return
		}
		current.Status = goal.StatusActive
		current.Error = ""
		current.LastReason = "resumed by user"
		current.UpdatedAt = time.Now().UTC()
		if err := opts.TenantService.UpdateGoal(r.Context(), current); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		event, err := goal.NewEvent(goal.EventInput{GoalID: current.ID, Type: goal.EventGoalResumed, Message: "goal resumed", SessionID: current.SessionID, Status: current.Status, Reason: current.LastReason})
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := opts.TenantService.AppendGoalEvent(r.Context(), event); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		broadcastTenantGoalEvent(r, opts, event)
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.goal.resume", "goal", current.ID)
		writeJSON(w, current)
	})
}

func tenantGoalRunHandler(opts Options, queryFn QueryFunc) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_goal_run", "server.tenantGoalRunHandler", "run tenant goal once")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		goalID, ok := tenantGoalIDFromPath(r.URL.Path, "run")
		if !ok {
			writeTenantError(w, http.StatusBadRequest, errMsgGoalIDRequired)
			return
		}
		if opts.StreamQueryFunc == nil && queryFn == nil {
			writeTenantError(w, http.StatusServiceUnavailable, errMsgGoalQueryRunnerNotConfig)
			return
		}
		runner := goal.Runner{
			Store:     tenantGoalStore{svc: opts.TenantService, streams: opts.MobileStreams, claims: requestMobileClaims(r)},
			Query:     tenantGoalQueryRunner{opts: opts, queryFn: queryFn},
			Evaluator: tenantGoalEvaluator(opts, queryFn, r),
		}
		result, err := runTenantGoal(r.Context(), runner, goalID, r)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.goal.run", "goal", result.Goal.ID)
		writeJSON(w, result)
	})
}

const (
	defaultTenantGoalContinuousMaxTurns  = 5
	absoluteTenantGoalContinuousMaxTurns = 20
)

func runTenantGoal(ctx context.Context, runner goal.Runner, goalID string, r *http.Request) (goal.RunResult, error) {
	if !parseBool(r.URL.Query().Get("continuous")) {
		return runner.RunOnce(ctx, goalID)
	}
	maxTurns, err := tenantGoalContinuousMaxTurns(r)
	if err != nil {
		return goal.RunResult{}, err
	}
	var last goal.RunResult
	for i := 0; i < maxTurns; i++ {
		result, err := runner.RunOnce(ctx, goalID)
		if err != nil {
			return last, err
		}
		last = result
		if result.Goal.Status != goal.StatusActive {
			return last, nil
		}
		if err := ctx.Err(); err != nil {
			return last, err
		}
	}
	if last.Goal.ID == "" {
		return last, fmt.Errorf("goal %s did not run", goalID)
	}
	decision := goal.Decision{
		Status:     goal.DecisionBlocked,
		Reason:     fmt.Sprintf("continuous run reached max_turns=%d", maxTurns),
		NextAction: "resume with another controlled run if the goal still needs work",
		BlockerKey: "continuous_max_turns_reached",
		Confidence: 1,
	}
	last.Goal = goal.ApplyDecision(last.Goal, decision)
	last.Goal.UpdatedAt = time.Now().UTC()
	if err := runner.Store.Update(ctx, last.Goal); err != nil {
		return last, err
	}
	event, err := goal.NewEvent(goal.EventInput{
		GoalID:     last.Goal.ID,
		Type:       goal.EventStatusChanged,
		Message:    "goal blocked by continuous run max_turns",
		SessionID:  last.Goal.SessionID,
		Status:     last.Goal.Status,
		Reason:     decision.Reason,
		NextAction: decision.NextAction,
		BlockerKey: decision.BlockerKey,
		Now:        last.Goal.UpdatedAt,
	})
	if err != nil {
		return last, err
	}
	if err := runner.Store.AppendEvent(ctx, event); err != nil {
		return last, err
	}
	last.Decision = decision
	last.Event = event
	return last, nil
}

func tenantGoalContinuousMaxTurns(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("max_turns"))
	if raw == "" {
		return defaultTenantGoalContinuousMaxTurns, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("max_turns must be a positive integer")
	}
	if n > absoluteTenantGoalContinuousMaxTurns {
		return 0, fmt.Errorf("max_turns must be <= %d", absoluteTenantGoalContinuousMaxTurns)
	}
	return n, nil
}

func broadcastTenantGoalEvent(r *http.Request, opts Options, event goal.Event) {
	if opts.MobileStreams == nil {
		return
	}
	opts.MobileStreams.BroadcastGoalFor(requestMobileClaims(r), event)
}

type tenantGoalStore struct {
	svc     TenantService
	streams *MobileStreamRegistry
	claims  mobileClaims
}

func (s tenantGoalStore) Create(ctx context.Context, input goal.CreateInput) (goal.Goal, error) {
	return s.svc.CreateGoal(ctx, input)
}

func (s tenantGoalStore) Get(ctx context.Context, id string) (goal.Goal, error) {
	return s.svc.GetGoal(ctx, id)
}

func (s tenantGoalStore) List(ctx context.Context, filter goal.ListFilter) ([]goal.Goal, error) {
	return s.svc.ListGoals(ctx, filter, 0)
}

func (s tenantGoalStore) Update(ctx context.Context, item goal.Goal) error {
	return s.svc.UpdateGoal(ctx, item)
}

func (s tenantGoalStore) AppendEvent(ctx context.Context, event goal.Event) error {
	if err := s.svc.AppendGoalEvent(ctx, event); err != nil {
		return err
	}
	if s.streams != nil {
		s.streams.BroadcastGoalFor(s.claims, event)
	}
	return nil
}

func (s tenantGoalStore) ListEvents(ctx context.Context, goalID string, limit int) ([]goal.Event, error) {
	return s.svc.ListGoalEvents(ctx, goalID, limit)
}

func (s tenantGoalStore) SavePlan(ctx context.Context, plan goal.GoalPlan) error {
	if err := s.svc.SavePlan(ctx, plan); err != nil {
		return err
	}
	if s.streams != nil {
		s.streams.BroadcastGoalPlanFor(s.claims, plan)
	}
	return nil
}

func (s tenantGoalStore) GetPlan(ctx context.Context, goalID string) (goal.GoalPlan, bool, error) {
	return s.svc.GetPlan(ctx, goalID)
}

func (s tenantGoalStore) AppendEvidence(ctx context.Context, evidence goal.GoalEvidence) error {
	if err := s.svc.AppendEvidence(ctx, evidence); err != nil {
		return err
	}
	if s.streams != nil {
		s.streams.BroadcastGoalEvidenceFor(s.claims, evidence)
	}
	return nil
}

func (s tenantGoalStore) ListEvidence(ctx context.Context, goalID string, limit int) ([]goal.GoalEvidence, error) {
	return s.svc.ListEvidence(ctx, goalID, limit)
}

type tenantGoalQueryRunner struct {
	opts    Options
	queryFn QueryFunc
}

func (r tenantGoalQueryRunner) RunGoalTurn(ctx context.Context, item goal.Goal, prompt string, checkpointName string) (goal.TurnResult, error) {
	queryReq := QueryRequest{
		Prompt:                   prompt,
		Model:                    item.Model,
		CWD:                      item.CWD,
		SessionKey:               item.SessionID,
		PromptMode:               "chat",
		DisableTenantPersistence: true,
	}
	checkpoint, checkpointCreated, err := createTenantGoalCheckpoint(item, checkpointName)
	if err != nil {
		return goal.TurnResult{Error: err}, err
	}
	var text strings.Builder
	var result query.Result
	switch {
	case r.opts.StreamQueryFunc != nil:
		result, err = r.opts.StreamQueryFunc(ctx, queryReq, &text)
		if result.Response == "" {
			result.Response = text.String()
		}
	case r.queryFn != nil:
		result, err = r.queryFn(ctx, queryReq)
	default:
		return goal.TurnResult{Error: errors.New(errMsgGoalQueryRunnerNotConfig)}, errors.New(errMsgGoalQueryRunnerNotConfig)
	}
	turnResult := goal.TurnResult{
		Response:     result.Response,
		StopReason:   result.StopReason,
		Turns:        result.Turns,
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		ToolErrors:   countServerToolErrors(result.ToolCalls),
		Evidence:     goal.EvidenceFromToolTraces(item.ID, tenantGoalToolEvidenceTraces(result.ToolCalls), time.Now().UTC()),
		Error:        err,
		ContextDone:  errors.Is(err, context.Canceled),
	}
	if checkpointCreated {
		turnResult.CheckpointName = checkpoint.Name
		turnResult.CheckpointCreated = true
	}
	return turnResult, err
}

func tenantGoalToolEvidenceTraces(calls []query.ToolTrace) []goal.ToolEvidenceTrace {
	if len(calls) == 0 {
		return nil
	}
	traces := make([]goal.ToolEvidenceTrace, 0, len(calls))
	for _, call := range calls {
		traces = append(traces, goal.ToolEvidenceTrace{
			ID:      call.ID,
			Name:    call.Name,
			Input:   call.Input,
			Output:  call.Output,
			IsError: call.IsError,
		})
	}
	return traces
}

func createTenantGoalCheckpoint(item goal.Goal, checkpointName string) (session.Entry, bool, error) {
	if strings.TrimSpace(item.SessionID) == "" || strings.TrimSpace(checkpointName) == "" {
		return session.Entry{}, false, nil
	}
	if !session.IsValidID(item.SessionID) {
		return session.Entry{}, false, nil
	}
	recorder, err := session.DefaultStore().NewRecorderWithID(item.CWD, item.SessionID)
	if err != nil {
		return session.Entry{}, false, err
	}
	defer func() {
		_ = recorder.Close()
	}()
	checkpoint, err := recorder.Checkpoint(checkpointName, "goal turn checkpoint")
	if err != nil {
		return session.Entry{}, false, err
	}
	return checkpoint, true, nil
}

type tenantGoalClassifier struct {
	opts    Options
	queryFn QueryFunc
}

func (c tenantGoalClassifier) ClassifyGoal(ctx context.Context, item goal.Goal, prompt string) (string, error) {
	queryReq := QueryRequest{
		Prompt:                   prompt,
		Model:                    item.Model,
		CWD:                      item.CWD,
		PromptMode:               "chat",
		MaxTokens:                256,
		DisableTenantPersistence: true,
	}
	var text strings.Builder
	var result query.Result
	var err error
	switch {
	case c.opts.StreamQueryFunc != nil:
		result, err = c.opts.StreamQueryFunc(ctx, queryReq, &text)
		if result.Response == "" {
			result.Response = text.String()
		}
	case c.queryFn != nil:
		result, err = c.queryFn(ctx, queryReq)
	default:
		return "", errors.New("goal classifier query runner is not configured")
	}
	return result.Response, err
}

func tenantGoalEvaluator(opts Options, queryFn QueryFunc, r *http.Request) goal.Evaluator {
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("evaluator")))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(opts.GoalEvaluator))
	}
	if mode == "model" {
		return goal.ModelClassifiedEvaluator{Classifier: tenantGoalClassifier{opts: opts, queryFn: queryFn}}
	}
	return goal.DeterministicEvaluator{}
}

func countServerToolErrors(calls []query.ToolTrace) int {
	n := 0
	for _, call := range calls {
		if call.IsError {
			n++
		}
	}
	return n
}

func tenantGoalListFilter(r *http.Request) (goal.ListFilter, error) {
	filter := goal.ListFilter{}
	query := r.URL.Query()
	if parseBool(query.Get("active")) {
		filter.Active = true
	}
	status := strings.TrimSpace(query.Get("status"))
	if status != "" {
		filter.Status = goal.Status(status)
		if !filter.Status.Valid() {
			return filter, fmt.Errorf("invalid goal status: %s", status)
		}
	}
	return filter, nil
}

func tenantGoalIDFromPath(path, suffix string) (string, bool) {
	raw := strings.TrimPrefix(path, "/tenant/goals/")
	if raw == "" || raw == path {
		return "", false
	}
	suffix = strings.Trim(suffix, "/")
	if suffix != "" {
		expectedSuffix := "/" + suffix
		if !strings.HasSuffix(raw, expectedSuffix) {
			return "", false
		}
		raw = strings.TrimSuffix(raw, expectedSuffix)
	} else if strings.Contains(raw, "/") {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	return raw, true
}

type goalCreateRequest struct {
	Objective   string `json:"objective"`
	SessionID   string `json:"session_id,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	Model       string `json:"model,omitempty"`
	TurnBudget  int    `json:"turn_budget,omitempty"`
	TokenBudget int    `json:"token_budget,omitempty"`
}

func (r goalCreateRequest) toCreateInput() (goal.CreateInput, error) {
	input := goal.CreateInput{
		Objective:   strings.TrimSpace(r.Objective),
		SessionID:   strings.TrimSpace(r.SessionID),
		CWD:         strings.TrimSpace(r.CWD),
		Model:       strings.TrimSpace(r.Model),
		TurnBudget:  r.TurnBudget,
		TokenBudget: r.TokenBudget,
	}
	_, err := goal.NormalizeCreateInput(input)
	return input, err
}

type goalUpdateRequest struct {
	Objective            *string      `json:"objective,omitempty"`
	Status               *goal.Status `json:"status,omitempty"`
	SessionID            *string      `json:"session_id,omitempty"`
	CWD                  *string      `json:"cwd,omitempty"`
	Model                *string      `json:"model,omitempty"`
	TurnBudget           *int         `json:"turn_budget,omitempty"`
	TokenBudget          *int         `json:"token_budget,omitempty"`
	TurnsUsed            *int         `json:"turns_used,omitempty"`
	InputTokens          *int         `json:"input_tokens,omitempty"`
	OutputTokens         *int         `json:"output_tokens,omitempty"`
	LastBlocker          *string      `json:"last_blocker,omitempty"`
	RepeatedBlockerCount *int         `json:"repeated_blocker_count,omitempty"`
	LastCheckpoint       *string      `json:"last_checkpoint,omitempty"`
	LastReason           *string      `json:"last_reason,omitempty"`
	LastNextAction       *string      `json:"last_next_action,omitempty"`
	Error                *string      `json:"error,omitempty"`
}

func (r goalUpdateRequest) apply(item goal.Goal) (goal.Goal, bool, error) {
	changed := false
	if r.Objective != nil {
		item.Objective = strings.TrimSpace(*r.Objective)
		changed = true
	}
	if r.Status != nil {
		item.Status = *r.Status
		changed = true
	}
	if r.SessionID != nil {
		item.SessionID = strings.TrimSpace(*r.SessionID)
		changed = true
	}
	if r.CWD != nil {
		item.CWD = strings.TrimSpace(*r.CWD)
		changed = true
	}
	if r.Model != nil {
		item.Model = strings.TrimSpace(*r.Model)
		changed = true
	}
	if r.TurnBudget != nil {
		item.TurnBudget = *r.TurnBudget
		changed = true
	}
	if r.TokenBudget != nil {
		item.TokenBudget = *r.TokenBudget
		changed = true
	}
	if r.TurnsUsed != nil {
		item.TurnsUsed = *r.TurnsUsed
		changed = true
	}
	if r.InputTokens != nil {
		item.InputTokens = *r.InputTokens
		changed = true
	}
	if r.OutputTokens != nil {
		item.OutputTokens = *r.OutputTokens
		changed = true
	}
	if r.LastBlocker != nil {
		item.LastBlocker = strings.TrimSpace(*r.LastBlocker)
		changed = true
	}
	if r.RepeatedBlockerCount != nil {
		item.RepeatedBlockerCount = *r.RepeatedBlockerCount
		changed = true
	}
	if r.LastCheckpoint != nil {
		item.LastCheckpoint = strings.TrimSpace(*r.LastCheckpoint)
		changed = true
	}
	if r.LastReason != nil {
		item.LastReason = strings.TrimSpace(*r.LastReason)
		changed = true
	}
	if r.LastNextAction != nil {
		item.LastNextAction = strings.TrimSpace(*r.LastNextAction)
		changed = true
	}
	if r.Error != nil {
		item.Error = strings.TrimSpace(*r.Error)
		changed = true
	}
	item.UpdatedAt = time.Now().UTC()
	if changed {
		if err := item.Validate(); err != nil {
			return goal.Goal{}, false, err
		}
	}
	return item, changed, nil
}

func tenantAuditHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_audit", "server.tenantAuditHandler", "handle tenant audit logs")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireTenantRole(w, r, opts, "owner", "admin") {
			return
		}
		page, err := opts.TenantService.ListAuditLogsPage(r.Context(), tenantListOptions(r))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, page)
	})
}

func tenantTelemetryHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_telemetry", "server.tenantTelemetryHandler", "handle tenant telemetry events")
		switch r.Method {
		case http.MethodGet:
			if !requireTenantRole(w, r, opts, "owner", "admin") {
				return
			}
			page, err := opts.TenantService.ListTelemetryEventsPage(r.Context(), tenantListOptions(r))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, page)
		case http.MethodPost:
			var event telemetry.Event
			if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(event.Name) == "" {
				writeTenantError(w, http.StatusBadRequest, "name is required")
				return
			}
			if err := telemetry.ValidateRuntimeTraceEvent(event); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			id, err := opts.TenantService.RecordTelemetry(r.Context(), event)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantQuotaConfigHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_quota_config", "server.tenantQuotaConfigHandler", "handle tenant quota config")
		switch r.Method {
		case http.MethodGet:
			cfg, err := opts.TenantService.GetQuotaConfig(r.Context())
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, cfg)
		case http.MethodPut, http.MethodPost:
			if !requireTenantRole(w, r, opts, "owner", "admin") {
				return
			}
			var req tenantservice.QuotaConfigRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			cfg, err := opts.TenantService.SaveQuotaConfig(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, cfg)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantUsageDailyHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_usage_daily", "server.tenantUsageDailyHandler", "handle tenant usage daily")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		page, err := opts.TenantService.ListUsageDailyPage(r.Context(), tenantListOptions(r))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, page)
	})
}

func tenantUsageLedgerHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_usage_ledger", "server.tenantUsageLedgerHandler", "handle tenant usage ledger")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		page, err := opts.TenantService.ListUsageLedgerPage(r.Context(), tenantListOptions(r))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, page)
	})
}

func tenantQuotaEventsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_quota_events", "server.tenantQuotaEventsHandler", "handle tenant quota events")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		page, err := opts.TenantService.ListQuotaEventsPage(r.Context(), tenantListOptions(r))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, page)
	})
}
