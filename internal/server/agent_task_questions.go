package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tools"
)

const (
	agentTaskQuestionTimeout        = 10 * time.Minute
	agentTaskQuestionPersistTimeout = 5 * time.Second
	maxAgentTaskAnswerBytes         = 16 * 1024
	questionStatusAnswered          = agenttasks.UserQuestionAnswered
	questionStatusCancelled         = agenttasks.UserQuestionCancelled
	questionStatusExpired           = agenttasks.UserQuestionExpired
)

var errQuestionNotPending = errors.New("question is no longer pending; refresh the conversation")

// Live waiters follow the existing detached Run lifetime. Request and answer
// events are durable; the registry never treats another task's ID as authority.
type AgentTaskQuestionRegistry struct {
	mu      sync.Mutex
	pending map[string]*agentTaskQuestion
}

type agentTaskQuestion struct {
	mu                       sync.Mutex
	ctx                      context.Context
	taskID, tenantID, userID uint64
	response                 chan tools.UserQuestionResponse
	status, answer           string
}

func NewAgentTaskQuestionRegistry() *AgentTaskQuestionRegistry {
	return &AgentTaskQuestionRegistry{pending: make(map[string]*agentTaskQuestion)}
}

func (r *AgentTaskQuestionRegistry) register(ctx context.Context, task mysqlstore.AgentTask, id string) *agentTaskQuestion {
	entry := &agentTaskQuestion{ctx: ctx, taskID: task.ID, tenantID: task.TenantID, userID: task.UserID, response: make(chan tools.UserQuestionResponse, 1)}
	r.mu.Lock()
	r.pending[id] = entry
	r.mu.Unlock()
	return entry
}

func (r *AgentTaskQuestionRegistry) get(id string) *agentTaskQuestion {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[id]
}

func (r *AgentTaskQuestionRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.pending, id)
	r.mu.Unlock()
}

func (s *agentTaskTextSink) OnUserQuestion(ctx context.Context, req tools.UserQuestionRequest) (tools.UserQuestionResponse, error) {
	if s.questions == nil {
		return tools.UserQuestionResponse{}, errors.New("user question registry is not configured")
	}
	task := s.task
	if task.ID == 0 {
		task.ID = s.taskID
	}
	if task.TenantID == 0 && task.UserID == 0 && s.svc != nil {
		loaded, loadErr := s.svc.GetAgentTask(ctx, s.taskID)
		if loadErr != nil {
			return tools.UserQuestionResponse{}, loadErr
		}
		task = loaded
	}
	waitCtx, cancel := context.WithTimeout(ctx, agentTaskQuestionTimeout)
	defer cancel()
	requestID := "question-" + uuid.NewString()
	entry := s.questions.register(waitCtx, task, requestID)
	defer s.questions.remove(requestID)
	s.setQuestionWaiting(1)
	defer s.setQuestionWaiting(-1)
	expiresAt, _ := waitCtx.Deadline()
	if err := s.appendEvent(ctx, agenttasks.EventUserQuestionRequest, map[string]any{
		"request_id": requestID, "tool_id": req.ToolUseID, "question": req.Question, "choices": req.Choices, "expires_at": expiresAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return tools.UserQuestionResponse{}, err
	}
	select {
	case response := <-entry.response:
		return response, nil
	case <-waitCtx.Done():
		return s.closeQuestion(entry, requestID, waitCtx.Err())
	}
}

func (s *agentTaskTextSink) setQuestionWaiting(delta int) {
	s.watchdogMu.Lock()
	s.waitingUserQuestions += delta
	s.lastWrite = time.Now()
	s.watchdogMu.Unlock()
}

func (s *agentTaskTextSink) closeQuestion(entry *agentTaskQuestion, id string, cause error) (tools.UserQuestionResponse, error) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	// An answer committed before cancellation wins the acknowledgement race.
	if entry.status == questionStatusAnswered {
		return tools.UserQuestionResponse{Answered: true, Answer: entry.answer}, nil
	}
	entry.status = questionStatusCancelled
	if errors.Is(cause, context.DeadlineExceeded) {
		entry.status = questionStatusExpired
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(entry.ctx), agentTaskQuestionPersistTimeout)
	defer cancel()
	err := s.appendEvent(persistCtx, agenttasks.EventUserQuestionResolved, map[string]any{"request_id": id, "status": entry.status})
	return tools.UserQuestionResponse{}, errors.Join(cause, err)
}

type agentTaskQuestionAnswer struct {
	Answer string `json:"answer"`
}

func tenantAgentTaskQuestionHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		taskID, requestID, ok := agentTaskQuestionPath(r.URL.Path)
		if !ok {
			writeTenantError(w, http.StatusBadRequest, "agent task question id is required")
			return
		}
		task, err := ensureTenantOwnsAgentTask(r.Context(), opts.TenantService, taskID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		answer, err := decodeQuestionAnswer(w, r)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, "a non-empty answer of at most 16384 bytes is required")
			return
		}
		entry := opts.AgentTaskQuestions.get(requestID)
		if entry == nil {
			err = replayQuestionAnswerWithOptions(r.Context(), opts, taskID, requestID, answer)
		} else {
			err = resolveQuestionAnswerWithOptions(r.Context(), opts, entry, task, requestID, answer)
		}
		if err != nil {
			if errors.Is(err, errQuestionNotPending) {
				writeTenantError(w, http.StatusConflict, errQuestionNotPending.Error())
			} else {
				writeTenantServiceError(w, err)
			}
			return
		}
		writeJSON(w, map[string]any{"id": taskID, "request_id": requestID, "status": questionStatusAnswered, "answer": answer})
	})
}

func decodeQuestionAnswer(w http.ResponseWriter, r *http.Request) (string, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentTaskAnswerBytes*6+128))
	decoder.DisallowUnknownFields()
	var input agentTaskQuestionAnswer
	if err := decoder.Decode(&input); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errors.New("one JSON object is required")
	}
	answer := strings.TrimSpace(input.Answer)
	if answer == "" || len(answer) > maxAgentTaskAnswerBytes {
		return "", errors.New("invalid answer length")
	}
	return answer, nil
}

func resolveQuestionAnswer(ctx context.Context, svc TenantService, entry *agentTaskQuestion, task mysqlstore.AgentTask, id, answer string) error {
	return resolveQuestionAnswerWithOptions(ctx, Options{TenantService: svc}, entry, task, id, answer)
}

func resolveQuestionAnswerWithOptions(ctx context.Context, opts Options, entry *agentTaskQuestion, task mysqlstore.AgentTask, id, answer string) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.taskID != task.ID || entry.tenantID != task.TenantID || entry.userID != task.UserID {
		return errQuestionNotPending
	}
	if entry.status == questionStatusAnswered {
		if entry.answer == answer {
			return nil
		}
		return errQuestionNotPending
	}
	if entry.status != "" || entry.ctx.Err() != nil || task.Status != agenttasks.StatusRunning {
		return errQuestionNotPending
	}
	// Persist before waking the original tool. A write failure leaves the answer
	// retryable, and the per-question lock prevents two tabs from answering twice.
	if _, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{TaskID: task.ID, EventType: agenttasks.EventUserQuestionResolved,
		PayloadJSON: agentTaskEventPayload(map[string]any{"request_id": id, "status": questionStatusAnswered, "answer": answer}), TraceID: task.TraceID}); err != nil {
		return err
	}
	entry.status, entry.answer = questionStatusAnswered, answer
	recordTenantAudit(ctx, opts.TenantService, "tenant.agent_task.question.answer", "agent_task", task.ID)
	entry.response <- tools.UserQuestionResponse{Answered: true, Answer: answer}
	return nil
}

func replayQuestionAnswer(ctx context.Context, svc TenantService, taskID uint64, id, answer string) error {
	return replayQuestionAnswerWithOptions(ctx, Options{TenantService: svc}, taskID, id, answer)
}

func replayQuestionAnswerWithOptions(ctx context.Context, opts Options, taskID uint64, id, answer string) error {
	// Only retries after the live waiter is removed need a history read. Normal
	// answers use the scoped registry; this adds no background polling or query.
	events, err := listCompleteAgentTaskEventsWithOptions(ctx, opts, taskID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.TaskID != taskID || event.EventType != agenttasks.EventUserQuestionResolved {
			continue
		}
		var payload struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
			Answer    string `json:"answer"`
		}
		if json.Unmarshal([]byte(event.PayloadJSON), &payload) != nil || payload.RequestID != id {
			continue
		}
		if payload.Status == questionStatusAnswered && payload.Answer == answer {
			return nil
		}
		return errQuestionNotPending
	}
	return errQuestionNotPending
}

func agentTaskQuestionPath(path string) (uint64, string, bool) {
	const prefix = "/tenant/agent-tasks/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 3 || parts[1] != "questions" || strings.TrimSpace(parts[2]) == "" {
		return 0, "", false
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	return id, parts[2], err == nil && id > 0
}
