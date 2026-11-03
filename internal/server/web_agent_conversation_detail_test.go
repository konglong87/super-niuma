package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type scopedWebConversationTenantFake struct {
	*fakeTenantService
	tasks        []mysqlstore.AgentTask
	err          error
	gotSessionID uint64
	gotLimit     int
	calls        int
	listCalls    int
}

func (s *scopedWebConversationTenantFake) ListSessions(ctx context.Context, limit int) ([]mysqlstore.Session, error) {
	s.listCalls++
	return s.fakeTenantService.ListSessions(ctx, limit)
}

func (s *scopedWebConversationTenantFake) ListWebAgentConversationTasks(_ context.Context, sessionID uint64, limit int) ([]mysqlstore.AgentTask, error) {
	s.gotSessionID, s.gotLimit, s.calls = sessionID, limit, s.calls+1
	return s.tasks, s.err
}

func TestWebAgentConversationDetailReadsOwnedSessionBeforeApplyingTaskLimit(t *testing.T) {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tenant := &scopedWebConversationTenantFake{
		fakeTenantService: &fakeTenantService{
			sessions:   []mysqlstore.Session{{ID: 23, SessionKey: "old-session", Title: "old conversation"}},
			agentTasks: []mysqlstore.AgentTask{{ID: 999, ParentSessionID: 999, StartedAt: started.Add(time.Hour)}},
		},
		tasks: []mysqlstore.AgentTask{
			{ID: 13, ParentSessionID: 23, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusCompleted, StartedAt: started, Model: "old-model", ResultJSON: `{"total_tokens":18}`, MetadataJSON: `{"provider":"old-provider","prompt_mode":"chat"}`},
			{ID: 14, ParentSessionID: 23, AgentName: "reviewer", Status: agenttasks.StatusCompleted, StartedAt: started.Add(time.Second)},
			{ID: 15, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusCompleted, StartedAt: started.Add(2 * time.Second), MetadataJSON: `{"web_agent_session_id":"23"}`},
		},
	}
	rec := webConversationDetailRequest(tenant, "/tenant/web-agent/conversations/session%3A23?limit=3&event_limit=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var detail webAgentConversationDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.SessionID != 23 || detail.SessionKey != "old-session" || len(detail.Tasks) != 3 || detail.Tasks[0].Model != "old-model" || detail.Usage.TotalTokens != 18 {
		t.Fatalf("detail=%+v", detail)
	}
	if tenant.lastSessionID != 23 || tenant.gotSessionID != 23 || tenant.gotLimit != 3 || tenant.calls != 1 || tenant.listCalls != 0 || tenant.lastAgentTaskLimit != 0 {
		t.Fatalf("unexpected query scope or global scan: session=%d scoped=%d limit=%d calls=%d globalLists=%d globalTaskLimit=%d", tenant.lastSessionID, tenant.gotSessionID, tenant.gotLimit, tenant.calls, tenant.listCalls, tenant.lastAgentTaskLimit)
	}
}

func TestWebAgentConversationDetailRejectsUnavailableSessionBeforeTaskRead(t *testing.T) {
	for _, id := range []string{"session:23", "session:0", "session:invalid"} {
		t.Run(id, func(t *testing.T) {
			tenant := &scopedWebConversationTenantFake{fakeTenantService: &fakeTenantService{}, tasks: []mysqlstore.AgentTask{{ID: 13, ParentSessionID: 23}}}
			rec := webConversationDetailRequest(tenant, "/tenant/web-agent/conversations/"+id+"?limit=100")
			if rec.Code != http.StatusNotFound || tenant.calls != 0 || tenant.lastAgentTaskLimit != 0 {
				t.Fatalf("status=%d body=%s calls=%d", rec.Code, rec.Body.String(), tenant.calls)
			}
		})
	}
}

func TestWebAgentConversationDetailDoesNotFallbackAfterScopedReadFailure(t *testing.T) {
	tenant := &scopedWebConversationTenantFake{
		fakeTenantService: &fakeTenantService{sessions: []mysqlstore.Session{{ID: 23}}},
		err:               errors.New("database unavailable"),
	}
	rec := webConversationDetailRequest(tenant, "/tenant/web-agent/conversations/session:23?limit=100")
	if rec.Code != http.StatusInternalServerError || tenant.calls != 1 || tenant.lastAgentTaskLimit != 0 {
		t.Fatalf("status=%d body=%s calls=%d", rec.Code, rec.Body.String(), tenant.calls)
	}
}

func TestWebAgentConversationDetailRequiresAuthenticationBeforeReadingSession(t *testing.T) {
	tenant := &scopedWebConversationTenantFake{fakeTenantService: &fakeTenantService{sessions: []mysqlstore.Session{{ID: 23}}}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: tenant}, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tenant/web-agent/conversations/session:23", nil))
	if rec.Code != http.StatusUnauthorized || tenant.calls != 0 || tenant.lastSessionID != 0 {
		t.Fatalf("status=%d calls=%d session=%d", rec.Code, tenant.calls, tenant.lastSessionID)
	}
}

func TestWebAgentConversationDetailPreservesLegacyContinuationScan(t *testing.T) {
	tenant := &scopedWebConversationTenantFake{fakeTenantService: &fakeTenantService{
		agentTasks: []mysqlstore.AgentTask{
			{ID: 13, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusCompleted},
			{ID: 14, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusCompleted, MetadataJSON: `{"continuation_of_task_id":13}`},
		},
	}}
	rec := webConversationDetailRequest(tenant, "/tenant/web-agent/conversations/legacy:13?limit=2")
	var detail webAgentConversationDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || detail.ID != "legacy:13" || len(detail.Tasks) != 2 || tenant.calls != 0 || tenant.lastSessionID != 0 || tenant.lastAgentTaskLimit != 2 || tenant.listCalls != 1 {
		t.Fatalf("status=%d detail=%+v tenant=%+v", rec.Code, detail, tenant)
	}
}

func webConversationDetailRequest(tenant TenantService, path string) *httptest.ResponseRecorder {
	handler := NewHandler(Options{AuthToken: "token", TenantService: tenant}, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionControlRequest(http.MethodGet, path, ""))
	return rec
}
