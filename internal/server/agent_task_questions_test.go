package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/query"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tools/askuserquestion"
)

func questionHTTP(t *testing.T, handler http.Handler, taskID uint64, requestID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/tenant/agent-tasks/%d/questions/%s", taskID, requestID), strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "yutang")
	req.Header.Set("X-User-Id", "user-test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAgentTaskQuestionAnswerPersistsBeforeResumingAndReplays(t *testing.T) {
	task := mysqlstore.AgentTask{ID: 44, TenantID: 7, UserID: 11, Status: agenttasks.StatusRunning}
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{task}}
	registry := NewAgentTaskQuestionRegistry()
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, AgentTaskQuestions: registry}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &agentTaskTextSink{ctx: ctx, svc: fake, taskID: task.ID, questions: registry}
	done := make(chan tools.UserQuestionResponse, 1)
	go func() {
		answer, _ := sink.OnUserQuestion(ctx, tools.UserQuestionRequest{Question: "Which diagram?", Choices: []string{"Architecture", "Sequence"}})
		done <- answer
	}()
	waitForTestCondition(t, func() bool {
		return fake.lastAgentTaskEventInputSnapshot().EventType == agenttasks.EventUserQuestionRequest
	})
	var question struct {
		RequestID string   `json:"request_id"`
		Choices   []string `json:"choices"`
	}
	if err := json.Unmarshal([]byte(fake.lastAgentTaskEventInputSnapshot().PayloadJSON), &question); err != nil {
		t.Fatal(err)
	}
	if question.RequestID == "" || len(question.Choices) != 2 {
		t.Fatal("question not persisted for SSE/history")
	}
	rec := questionHTTP(t, handler, task.ID, question.RequestID, `{"answer":"Architecture"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case answer := <-done:
		if !answer.Answered || answer.Answer != "Architecture" {
			t.Fatalf("answer=%+v", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("original question did not resume")
	}
	event := fake.lastAgentTaskEventInputSnapshot()
	if event.EventType != agenttasks.EventUserQuestionResolved || !strings.Contains(event.PayloadJSON, `"answer":"Architecture"`) {
		t.Fatal("answer was not persisted")
	}
	for _, tc := range []struct {
		answer string
		status int
	}{{"Architecture", 200}, {"Sequence", 409}} {
		rec = questionHTTP(t, handler, task.ID, question.RequestID, fmt.Sprintf(`{"answer":%q}`, tc.answer))
		if rec.Code != tc.status {
			t.Fatalf("replay %s: status=%d body=%s", tc.answer, rec.Code, rec.Body.String())
		}
	}
	events, _ := fake.ListAgentTaskEvents(ctx, task.ID, 100)
	if len(events) != 2 {
		t.Fatalf("duplicate answer appended events: %d", len(events))
	}
}

func TestAgentTaskQuestionRejectsOtherTaskEmptyAnswerAndCancelledRun(t *testing.T) {
	task := mysqlstore.AgentTask{ID: 44, TenantID: 7, UserID: 11, Status: agenttasks.StatusRunning}
	other := task
	other.ID = 45
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{task, other}}
	registry := NewAgentTaskQuestionRegistry()
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, AgentTaskQuestions: registry}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	entry := registry.register(ctx, task, "question-test")
	for _, tc := range []struct {
		taskID uint64
		body   string
		status int
	}{{45, `{"answer":"a"}`, 409}, {99, `{"answer":"a"}`, 404}, {44, `{"answer":"  "}`, 400}, {44, `{"answer":"a","unexpected":true}`, 400}} {
		rec := questionHTTP(t, handler, tc.taskID, "question-test", tc.body)
		if rec.Code != tc.status {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
		}
	}
	cancel()
	rec := questionHTTP(t, handler, task.ID, "question-test", `{"answer":"a"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cancelled question status=%d", rec.Code)
	}
	select {
	case <-entry.response:
		t.Fatal("invalid answer resumed run")
	default:
	}
}

type questionWriteFailureService struct{ *fakeTenantService }

func (s *questionWriteFailureService) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	if input.EventType == agenttasks.EventUserQuestionResolved {
		return 0, errors.New("simulated database failure")
	}
	return s.fakeTenantService.AppendAgentTaskEvent(ctx, input)
}

func TestAgentTaskQuestionPersistenceFailureDoesNotConsumeAnswer(t *testing.T) {
	task := mysqlstore.AgentTask{ID: 44, TenantID: 7, UserID: 11, Status: agenttasks.StatusRunning}
	fake := &questionWriteFailureService{&fakeTenantService{agentTasks: []mysqlstore.AgentTask{task}}}
	registry := NewAgentTaskQuestionRegistry()
	entry := registry.register(context.Background(), task, "question-test")
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, AgentTaskQuestions: registry}, nil)
	rec := questionHTTP(t, handler, task.ID, "question-test", `{"answer":"a"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-entry.response:
		t.Fatal("unpersisted answer resumed run")
	default:
	}
}

func TestAgentTaskQuestionWaitSurvivesIdleTimeoutAndClosesOnCancel(t *testing.T) {
	task := mysqlstore.AgentTask{ID: 44, TenantID: 7, UserID: 11, Status: agenttasks.StatusRunning}
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{task}}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	sink := &agentTaskTextSink{ctx: ctx, svc: fake, taskID: task.ID, questions: NewAgentTaskQuestionRegistry(), idleTimeout: 25 * time.Millisecond, idleCancel: cancel}
	sink.startIdleWatchdog()
	defer sink.stopIdleWatchdog()
	done := make(chan error, 1)
	go func() {
		_, err := sink.OnUserQuestion(ctx, tools.UserQuestionRequest{Question: "Continue?"})
		done <- err
	}()
	waitForTestCondition(t, func() bool {
		return fake.lastAgentTaskEventInputSnapshot().EventType == agenttasks.EventUserQuestionRequest
	})
	select {
	case err := <-done:
		t.Fatalf("question timed out as idle: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel(context.Canceled)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("question waiter leaked after stop")
	}
	event := fake.lastAgentTaskEventInputSnapshot()
	if event.EventType != agenttasks.EventUserQuestionResolved || !strings.Contains(event.PayloadJSON, `"status":"cancelled"`) {
		t.Fatalf("unclosed question: %+v", event)
	}
}

func TestAgentTaskQuestionHTTPContinuesOriginalDetachedRun(t *testing.T) {
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 44, TenantID: 7, UserID: 11, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusReady}}}
	opts := Options{AuthToken: "token", TenantService: fake, StreamQueryFunc: func(ctx context.Context, _ QueryRequest, sink io.Writer) (query.Result, error) {
		questions := sink.(AgentTaskUserQuestionSink)
		result := askuserquestion.New().Run(ctx, json.RawMessage(`{"question":"Which diagram?","choices":["Architecture","Sequence"]}`), tools.Context{
			UserQuestion: func(ctx context.Context, req tools.UserQuestionRequest) tools.UserQuestionResponse {
				response, err := questions.OnUserQuestion(ctx, req)
				if err != nil {
					return tools.UserQuestionResponse{Error: err.Error()}
				}
				return response
			},
		})
		if result.IsError {
			return query.Result{}, errors.New(result.Content)
		}
		return query.Result{Response: result.Content}, nil
	}}
	handler := NewHandler(opts, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAgentTaskMessageHTTPRequest(44, `{"content":"Draw a diagram"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("send=%d %s", rec.Code, rec.Body.String())
	}
	waitForTestCondition(t, func() bool {
		return fake.lastAgentTaskEventInputSnapshot().EventType == agenttasks.EventUserQuestionRequest
	})
	status, _ := fake.lastFinishedAgentSnapshot()
	if status != "" {
		t.Fatalf("run completed before user answer: %s", status)
	}
	var payload struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal([]byte(fake.lastAgentTaskEventInputSnapshot().PayloadJSON), &payload)
	rec = questionHTTP(t, handler, 44, payload.RequestID, `{"answer":"Architecture"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer=%d %s", rec.Code, rec.Body.String())
	}
	waitForTestCondition(t, func() bool {
		status, _ := fake.lastFinishedAgentSnapshot()
		return status == agenttasks.StatusCompleted
	})
	_, result := fake.lastFinishedAgentSnapshot()
	if !strings.Contains(result, "User answered: Architecture") {
		t.Fatalf("answer lost during continuation: %s", result)
	}
	events, _ := fake.ListAgentTaskEvents(context.Background(), 44, 200)
	for _, event := range events {
		if event.TaskID != 44 {
			t.Fatalf("answer started another run: %d", event.TaskID)
		}
	}
}

func TestAgentTaskQuestionRegistryRejectsForgedScopeAndDuplicateConcurrentAnswers(t *testing.T) {
	task := mysqlstore.AgentTask{ID: 44, TenantID: 7, UserID: 11, Status: agenttasks.StatusRunning}
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{task}}
	registry := NewAgentTaskQuestionRegistry()
	entry := registry.register(context.Background(), task, "question-test")
	for _, foreign := range []mysqlstore.AgentTask{{ID: 44, TenantID: 8, UserID: 11, Status: agenttasks.StatusRunning}, {ID: 44, TenantID: 7, UserID: 12, Status: agenttasks.StatusRunning}} {
		if err := resolveQuestionAnswer(context.Background(), fake, entry, foreign, "question-test", "a"); !errors.Is(err, errQuestionNotPending) {
			t.Fatalf("foreign answer accepted: %v", err)
		}
	}
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- resolveQuestionAnswer(context.Background(), fake, entry, task, "question-test", "a") }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	events, _ := fake.ListAgentTaskEvents(context.Background(), task.ID, 100)
	if len(events) != 1 {
		t.Fatalf("duplicate resolved events: %d", len(events))
	}
}
