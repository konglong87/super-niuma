package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/sessioncontrol/handoff"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestAgentTaskHandoffInjectsOnlyValidatedSnapshotInSourceOrder(t *testing.T) {
	workspace := handoffContextWorkspace(t, 20_000)
	olderPayload, olderTokens := handoffEventFixture(t, 43, "tenant:source-z", "task_event:9", "older immutable objective", "DO_NOT_RENDER_EVIDENCE_CLAIM")
	newerPayload, newerTokens := handoffEventFixture(t, 43, "tenant:source-a", "message:7", "newer immutable objective", "DO_NOT_RENDER_EVIDENCE_CLAIM")
	if olderTokens+newerTokens >= 3_000 {
		t.Fatalf("fixture unexpectedly exceeds configured aggregate budget: %d", olderTokens+newerTokens)
	}
	svc := &handoffEventTenantService{
		fakeTenantService: &fakeTenantService{},
		events: []mysqlstore.AgentTaskEvent{
			{ID: 9, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: olderPayload, CreatedAt: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: 7, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: newerPayload, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: 10, TaskID: 43, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"raw current task message"}`},
		},
	}
	task := handoffTargetTask(workspace)
	var got QueryRequest
	result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		got = req
		return query.Result{Response: "done"}, nil
	}, task, agenttasks.MessageInput{TaskID: task.ID, FromAgent: "webui", Content: "continue", TraceID: "trace-handoff"})
	if err != nil || result.Status != agenttasks.StatusCompleted {
		t.Fatalf("runAgentTaskMessage() = %+v, %v", result, err)
	}
	if svc.listTaskID != task.ID || svc.listLimit <= 0 {
		t.Fatalf("ListAgentTaskEvents scope = task %d limit %d", svc.listTaskID, svc.listLimit)
	}
	if len(got.InitialMessages) != 1 || got.InitialMessages[0].Role != "user" || len(got.InitialMessages[0].Content) != 1 {
		t.Fatalf("InitialMessages = %#v, want one handoff context message", got.InitialMessages)
	}
	text := got.InitialMessages[0].Content[0].Text
	for _, want := range []string{
		"<session_handoff_context schema=\"niuma.session-handoff-context.v1\">",
		"</session_handoff_context>",
		`"objective": "newer immutable objective"`,
		`"objective": "older immutable objective"`,
		`"constraints": [`,
		`"stage_summary": "validated stage"`,
		`"completed": [`,
		`"open_items": [`,
		`"risks": [`,
		`"next_actions": [`,
		`"ref": "tenant:source-a#message:7"`,
		`"verification": "reported"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("handoff context missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "tenant:source-a") > strings.Index(text, "tenant:source-z") {
		t.Fatalf("handoff sources are not deterministically sorted:\n%s", text)
	}
	for _, forbidden := range []string{"DO_NOT_RENDER_EVIDENCE_CLAIM", "raw current task message", "captured_at", "budget", "transcript", "messages"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("handoff context leaked forbidden field/content %q:\n%s", forbidden, text)
		}
	}
}

func TestAgentTaskHandoffAggregateBudgetUsesTargetContextWindow(t *testing.T) {
	claim := strings.Repeat("unrendered-claim ", 28)
	firstPayload, firstPackageTokens := handoffEventFixture(t, 43, "tenant:source-a", "message:1", "first objective", claim)
	secondPayload, secondPackageTokens := handoffEventFixture(t, 43, "tenant:source-b", "message:2", "second objective", claim)
	events := []mysqlstore.AgentTaskEvent{
		{ID: 1, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: firstPayload},
		{ID: 2, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: secondPayload},
	}
	largeWorkspace := handoffContextWorkspace(t, 200_000)
	largeSvc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: events}
	var renderedText string
	result, err := runAgentTaskMessage(context.Background(), Options{Workspace: largeWorkspace, TenantService: largeSvc}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		renderedText = req.InitialMessages[0].Content[0].Text
		return query.Result{Response: "done"}, nil
	}, handoffTargetTask(largeWorkspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-measure"})
	if err != nil || result.Status != agenttasks.StatusCompleted || renderedText == "" {
		t.Fatalf("measure rendered context run = %+v, %v, text=%q", result, err, renderedText)
	}
	renderedTokens := compact.EstimateTextTokens(renderedText)
	if renderedTokens >= firstPackageTokens+secondPackageTokens {
		t.Fatalf("fixture must prove omitted claim prose is not charged: rendered=%d package estimates=%d", renderedTokens, firstPackageTokens+secondPackageTokens)
	}

	t.Run("exact aggregate cap", func(t *testing.T) {
		workspace := handoffContextWorkspace(t, contextWindowForHandoffLimit(t, renderedTokens))
		svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: events}
		calls := 0
		result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(_ context.Context, req QueryRequest) (query.Result, error) {
			calls++
			if got := compact.EstimateTextTokens(req.InitialMessages[0].Content[0].Text); got != renderedTokens {
				t.Fatalf("rendered tokens = %d, want %d", got, renderedTokens)
			}
			return query.Result{Response: "done"}, nil
		}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-exact"})
		if err != nil || result.Status != agenttasks.StatusCompleted || calls != 1 {
			t.Fatalf("exact-cap run = %+v, %v, model calls=%d", result, err, calls)
		}
	})

	t.Run("aggregate cap exceeded", func(t *testing.T) {
		workspace := handoffContextWorkspace(t, contextWindowForHandoffLimit(t, renderedTokens-1))
		svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: events}
		calls := 0
		result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(_ context.Context, _ QueryRequest) (query.Result, error) {
			calls++
			return query.Result{Response: "must not run"}, nil
		}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-over"})
		if err != nil || result.Status != agenttasks.StatusFailed || calls != 0 {
			t.Fatalf("over-cap run = %+v, %v, model calls=%d", result, err, calls)
		}
		assertHandoffFailureCode(t, svc.fakeTenantService, "session_handoff_budget_exceeded")
	})
}

func TestAgentTaskHandoffFailsBeforeModelForInvalidOrForeignEvents(t *testing.T) {
	valid, _ := handoffEventFixture(t, 43, "tenant:source", "message:1", "immutable objective", "claim")
	foreignTarget, _ := handoffEventFixture(t, 44, "tenant:source", "message:1", "foreign objective", "claim")
	invalidCases := []struct {
		name        string
		events      []mysqlstore.AgentTaskEvent
		listErr     error
		contextSize int
		wantCode    string
	}{
		{name: "list failure", listErr: errors.New("storage unavailable"), contextSize: 20_000, wantCode: "session_handoff_list_failed"},
		{name: "malformed event", events: []mysqlstore.AgentTaskEvent{{ID: 1, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: `{`}}, contextSize: 20_000, wantCode: "session_handoff_invalid"},
		{name: "invalid package sha", events: []mysqlstore.AgentTaskEvent{{ID: 2, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: strings.Replace(valid, `"objective":"immutable objective"`, `"objective":"tampered objective"`, 1)}}, contextSize: 20_000, wantCode: "session_handoff_invalid"},
		{name: "invalid schema", events: []mysqlstore.AgentTaskEvent{{ID: 3, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: strings.Replace(valid, agenttasks.SessionHandoffEventSchema, "unknown-event-schema", 1)}}, contextSize: 20_000, wantCode: "session_handoff_invalid"},
		{name: "transcript shaped field", events: []mysqlstore.AgentTaskEvent{{ID: 4, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: strings.TrimSuffix(valid, "}") + `,"transcript":[{"content":"secret"}]}`}}, contextSize: 20_000, wantCode: "session_handoff_invalid"},
		{name: "foreign event row", events: []mysqlstore.AgentTaskEvent{{ID: 5, TaskID: 99, EventType: agenttasks.EventSessionHandoff, PayloadJSON: valid}}, contextSize: 20_000, wantCode: "session_handoff_scope_mismatch"},
		{name: "foreign target envelope", events: []mysqlstore.AgentTaskEvent{{ID: 6, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: foreignTarget}}, contextSize: 20_000, wantCode: "session_handoff_scope_mismatch"},
	}
	for _, tt := range invalidCases {
		t.Run(tt.name, func(t *testing.T) {
			workspace := handoffContextWorkspace(t, tt.contextSize)
			svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: tt.events, err: tt.listErr}
			calls := 0
			result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(_ context.Context, _ QueryRequest) (query.Result, error) {
				calls++
				return query.Result{Response: "must not run"}, nil
			}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-invalid"})
			if err != nil || result.Status != agenttasks.StatusFailed || calls != 0 {
				t.Fatalf("invalid handoff run = %+v, %v, model calls=%d", result, err, calls)
			}
			assertHandoffFailureCode(t, svc.fakeTenantService, tt.wantCode)
			_, resultJSON := svc.lastFinishedAgentSnapshot()
			if strings.Contains(resultJSON, "immutable objective") || strings.Contains(resultJSON, "secret") || strings.Contains(resultJSON, "storage unavailable") {
				t.Fatalf("failure result leaked payload or dependency details: %s", resultJSON)
			}
		})
	}
}

func TestAgentTaskHandoffUsesRuntimeContextDefaultAndModelMatching(t *testing.T) {
	payload, _ := handoffEventFixture(t, 43, "tenant:source", "message:1", "default context objective", "claim")
	events := []mysqlstore.AgentTaskEvent{{ID: 1, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: payload}}

	t.Run("default context", func(t *testing.T) {
		workspace := t.TempDir()
		svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: events}
		calls := 0
		result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(context.Context, QueryRequest) (query.Result, error) {
			calls++
			return query.Result{Response: "done"}, nil
		}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-default-context"})
		if err != nil || result.Status != agenttasks.StatusCompleted || calls != 1 {
			t.Fatalf("default-context run = %+v, %v, model calls=%d", result, err, calls)
		}
	})

	t.Run("normalized model match", func(t *testing.T) {
		workspace := handoffSettingsWorkspace(t, `{"autoCompact":{"modelContext":{"claude":1000}}}`)
		svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: events}
		calls := 0
		result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(context.Context, QueryRequest) (query.Result, error) {
			calls++
			return query.Result{Response: "must not run"}, nil
		}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-model-context"})
		if err != nil || result.Status != agenttasks.StatusFailed || calls != 0 {
			t.Fatalf("model-context run = %+v, %v, model calls=%d", result, err, calls)
		}
		assertHandoffFailureCode(t, svc.fakeTenantService, "session_handoff_budget_exceeded")
	})
}

func TestAgentTaskHandoffPaginatesPastNonHandoffEvents(t *testing.T) {
	const pageSize = 200
	workspace := handoffContextWorkspace(t, 200_000)
	payload, _ := handoffEventFixture(t, 43, "tenant:late-source", "message:206", "late handoff objective", "claim")
	events := make([]mysqlstore.AgentTaskEvent, 0, pageSize+6)
	for id := uint64(1); id <= uint64(pageSize+5); id++ {
		events = append(events, mysqlstore.AgentTaskEvent{ID: id, TaskID: 43, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"ordinary"}`})
	}
	events = append(events, mysqlstore.AgentTaskEvent{ID: uint64(pageSize + 6), TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: payload})
	svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: events}
	var got QueryRequest
	result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		got = req
		return query.Result{Response: "done"}, nil
	}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-pagination"})
	if err != nil || result.Status != agenttasks.StatusCompleted {
		t.Fatalf("paginated handoff run = %+v, %v", result, err)
	}
	if len(got.InitialMessages) != 1 || !strings.Contains(got.InitialMessages[0].Content[0].Text, "late handoff objective") {
		t.Fatalf("late handoff was omitted: %#v", got.InitialMessages)
	}
	if len(svc.listAfterIDs) < 2 || svc.listAfterIDs[0] != 0 || svc.listAfterIDs[1] != uint64(pageSize) {
		t.Fatalf("pagination cursors = %#v", svc.listAfterIDs)
	}
}

func TestAgentTaskHandoffFailsWhenEventSafetyBoundCannotProveExhaustion(t *testing.T) {
	const maxEventRows = 10_000
	workspace := handoffContextWorkspace(t, 200_000)
	events := make([]mysqlstore.AgentTaskEvent, 0, maxEventRows+1)
	for id := uint64(1); id <= uint64(maxEventRows+1); id++ {
		eventType, payload := agenttasks.EventMessage, ""
		if id == uint64(maxEventRows+1) {
			eventType, payload = agenttasks.EventSessionHandoff, `{`
		}
		events = append(events, mysqlstore.AgentTaskEvent{ID: id, TaskID: 43, EventType: eventType, PayloadJSON: payload})
	}
	svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, events: events}
	calls := 0
	result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(context.Context, QueryRequest) (query.Result, error) {
		calls++
		return query.Result{Response: "must not run"}, nil
	}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-bound"})
	if err != nil || result.Status != agenttasks.StatusFailed || calls != 0 {
		t.Fatalf("bounded handoff run = %+v, %v, model calls=%d", result, err, calls)
	}
	assertHandoffFailureCode(t, svc.fakeTenantService, "session_handoff_event_limit_exceeded")
}

func TestAgentTaskHandoffPreservesListCancellationBeforeModel(t *testing.T) {
	workspace := handoffContextWorkspace(t, 200_000)
	svc := &handoffEventTenantService{fakeTenantService: &fakeTenantService{}, err: context.Canceled}
	calls := 0
	result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(context.Context, QueryRequest) (query.Result, error) {
		calls++
		return query.Result{Response: "must not run"}, nil
	}, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-cancel"})
	if err != nil || result.Status != agenttasks.StatusCancelled || calls != 0 {
		t.Fatalf("cancelled list run = %+v, %v, model calls=%d", result, err, calls)
	}
	status, _ := svc.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusCancelled {
		t.Fatalf("finished status = %q, want cancelled", status)
	}
}

func TestAgentTaskHandoffFailsBeforeStreamModelInvocation(t *testing.T) {
	workspace := handoffContextWorkspace(t, 20_000)
	svc := &handoffEventTenantService{
		fakeTenantService: &fakeTenantService{},
		events:            []mysqlstore.AgentTaskEvent{{ID: 1, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: `{`}},
	}
	calls := 0
	result, err := runAgentTaskMessage(context.Background(), Options{
		Workspace: workspace, TenantService: svc,
		StreamQueryFunc: func(context.Context, QueryRequest, io.Writer) (query.Result, error) {
			calls++
			return query.Result{Response: "must not run"}, nil
		},
	}, nil, handoffTargetTask(workspace), agenttasks.MessageInput{Content: "continue", TraceID: "trace-invalid-stream"})
	if err != nil || result.Status != agenttasks.StatusFailed || calls != 0 {
		t.Fatalf("invalid stream handoff run = %+v, %v, model calls=%d", result, err, calls)
	}
	assertHandoffFailureCode(t, svc.fakeTenantService, "session_handoff_invalid")
}

func TestAgentTaskContextWithoutHandoffLeavesQueryRequestByteForByteUnchanged(t *testing.T) {
	workspace := handoffContextWorkspace(t, 20_000)
	svc := &handoffEventTenantService{
		fakeTenantService: &fakeTenantService{},
		events:            []mysqlstore.AgentTaskEvent{{ID: 1, TaskID: 43, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"ordinary event"}`}},
	}
	task := handoffTargetTask(workspace)
	input := agenttasks.MessageInput{TaskID: task.ID, FromAgent: "webui", Content: "ordinary prompt", TraceID: "trace-normal"}
	var got QueryRequest
	result, err := runAgentTaskMessage(context.Background(), Options{Workspace: workspace, TenantService: svc}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		got = req
		return query.Result{Response: "done"}, nil
	}, task, input)
	if err != nil || result.Status != agenttasks.StatusCompleted {
		t.Fatalf("normal run = %+v, %v", result, err)
	}
	want := QueryRequest{
		Prompt:                   "ordinary prompt",
		Model:                    "claude-test",
		Provider:                 "anthropic",
		TenantID:                 7,
		UserID:                   11,
		CWD:                      workspace,
		SessionKey:               "web-agent-task-43",
		PromptMode:               "code",
		TraceID:                  "trace-normal",
		DisableTenantPersistence: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryRequest changed without handoff:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestAgentTaskHandoffEventKeepsUnknownCompatibleSSEEnvelope(t *testing.T) {
	payload, _ := handoffEventFixture(t, 43, "tenant:source", "message:1", "immutable objective", "claim")
	fake := &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{{ID: 43, Status: agenttasks.StatusCompleted}},
		agentTaskEvents: []mysqlstore.AgentTaskEvent{{
			ID: 1, TaskID: 43, EventType: agenttasks.EventSessionHandoff, PayloadJSON: payload,
		}},
	}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-tasks/43/events/stream?limit=8", nil)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("content-type") != "text/event-stream" {
		t.Fatalf("SSE status/content-type = %d/%q, body=%s", rec.Code, rec.Header().Get("content-type"), rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"event: connected", "event: agent_task_event", `"event_type":"session_handoff"`, `"payload_json":`} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE envelope missing %q: %s", want, body)
		}
	}
}

type handoffEventTenantService struct {
	*fakeTenantService
	events       []mysqlstore.AgentTaskEvent
	err          error
	listTaskID   uint64
	listLimit    int
	listAfterIDs []uint64
}

func (s *handoffEventTenantService) ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	return s.ListAgentTaskEventsAfter(ctx, taskID, 0, limit)
}

func (s *handoffEventTenantService) ListAgentTaskEventsAfter(_ context.Context, taskID, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	s.listTaskID = taskID
	s.listLimit = limit
	s.listAfterIDs = append(s.listAfterIDs, afterID)
	if s.err != nil {
		return nil, s.err
	}
	events := append([]mysqlstore.AgentTaskEvent(nil), s.events...)
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
	out := make([]mysqlstore.AgentTaskEvent, 0, limit)
	for _, event := range events {
		if event.ID <= afterID {
			continue
		}
		out = append(out, event)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

func handoffTargetTask(workspace string) mysqlstore.AgentTask {
	metadata, _ := json.Marshal(map[string]any{"cwd": workspace, "provider": "anthropic", "prompt_mode": "code"})
	return mysqlstore.AgentTask{ID: 43, TenantID: 7, UserID: 11, AgentName: "web-agent", Status: agenttasks.StatusReady, Model: "claude-test", MetadataJSON: string(metadata)}
}

func handoffEventFixture(t *testing.T, targetTaskID uint64, sourceRef, cursor, objective, evidenceClaim string) (string, int) {
	t.Helper()
	sha := strings.Repeat("a", 64)
	pkg, err := handoff.BuildPackage(handoff.PackageInput{
		Source:       handoff.Source{Ref: sourceRef, Cursor: cursor, CapturedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		Target:       handoff.Target{Ref: "tenant:target"},
		Objective:    objective,
		Constraints:  []string{"keep tenant isolation"},
		StageSummary: "validated stage",
		Completed:    []string{"captured source snapshot"},
		OpenItems:    []string{"continue target work"},
		Risks:        []string{"source may be historical"},
		NextActions:  []string{"verify target state"},
		Evidence: []handoff.Evidence{{
			Ref: sourceRef + "#" + cursor, Claim: evidenceClaim, Verification: handoff.VerificationReported, SHA256: sha,
		}},
		Budget: handoff.Budget{LimitTokens: handoff.DefaultPackageTokenLimit},
	})
	if err != nil {
		t.Fatalf("BuildPackage() error = %v", err)
	}
	payload, err := handoff.EncodeSessionHandoffEvent(pkg, targetTaskID)
	if err != nil {
		t.Fatalf("EncodeSessionHandoffEvent() error = %v", err)
	}
	return payload, pkg.Budget.EstimatedTokens
}

func handoffContextWorkspace(t *testing.T, contextLength int) string {
	t.Helper()
	workspace := t.TempDir()
	if contextLength <= 0 {
		return workspace
	}
	configDir := filepath.Join(workspace, "config")
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(fmt.Sprintf(`{"contextLength":%d}`, contextLength)), 0o600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func handoffSettingsWorkspace(t *testing.T, settings string) string {
	t.Helper()
	workspace := t.TempDir()
	configDir := filepath.Join(workspace, "config")
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func contextWindowForHandoffLimit(t *testing.T, limit int) int {
	t.Helper()
	if limit <= 0 {
		t.Fatalf("invalid aggregate limit fixture: %d", limit)
	}
	contextWindow := (limit*100 + 14) / 15
	if got := handoff.AggregateBudgetLimit(contextWindow); got != limit {
		t.Fatalf("context window fixture %d gives aggregate limit %d, want %d", contextWindow, got, limit)
	}
	return contextWindow
}

func assertHandoffFailureCode(t *testing.T, svc *fakeTenantService, want string) {
	t.Helper()
	status, resultJSON := svc.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusFailed {
		t.Fatalf("finished status = %q, want failed; result=%s", status, resultJSON)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &payload); err != nil {
		t.Fatalf("invalid failure result JSON: %v; body=%s", err, resultJSON)
	}
	if payload["error_code"] != want {
		t.Fatalf("failure error_code = %#v, want %q; body=%s", payload["error_code"], want, resultJSON)
	}
}
