package server

import (
	"context"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type conversationTenantFake struct {
	*fakeTenantService
	gotSession, gotCursor uint64
	calls                 int
}

func (s *conversationTenantFake) ListSessionConversationEvents(_ context.Context, sessionID, afterID uint64, _ int) ([]mysqlstore.AgentTaskEvent, error) {
	s.gotSession, s.gotCursor = sessionID, afterID
	s.calls++
	return []mysqlstore.AgentTaskEvent{{ID: afterID + 1, TaskID: 14, EventType: "failed", PayloadJSON: `{"error":"provider route conflict"}`}}, nil
}

func TestSessionConversationUsesAuthorizedNumericIDAndExposesFailure(t *testing.T) {
	tenant := &conversationTenantFake{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11}}
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{ID: 7, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "sc-long-key"}, Status: sessioncontrol.StatusFailed}}
	handler := tenantSessionConversationHandler(Options{AuthToken: "token", TenantService: tenant, SessionControl: service})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/sc-long-key/conversation?cursor=9", ""))
	if rec.Code != 200 || tenant.gotSession != 7 || tenant.gotCursor != 9 || !strings.Contains(rec.Body.String(), "provider route conflict") {
		t.Fatalf("status=%d body=%s session=%d cursor=%d", rec.Code, rec.Body.String(), tenant.gotSession, tenant.gotCursor)
	}
}

func TestSessionConversationRejectsForeignAndLocalBeforeReadingContent(t *testing.T) {
	for _, ref := range []string{"tenant:foreign", "local:private"} {
		t.Run(ref, func(t *testing.T) {
			tenant := &conversationTenantFake{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11}}
			service := &sessionControlServiceFake{err: &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "forbidden"}}
			parsed, _ := sessioncontrol.ParseRef(ref)
			_, err := readSessionConversation(context.Background(), Options{TenantService: tenant, SessionControl: service}, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, parsed, 0)
			if err == nil || tenant.calls != 0 {
				t.Fatal("unauthorized conversation was read")
			}
		})
	}
}

func TestConversationSubscribeChecksEveryRefBeforeSSEHeaders(t *testing.T) {
	tenant := &conversationTenantFake{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11}}
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{ID: 7}}
	handler := tenantSessionConversationsStreamHandler(Options{AuthToken: "token", TenantService: tenant, SessionControl: service})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionControlRequest(http.MethodPost, "/tenant/session-control/conversations/stream", `{"sessions":[{"ref":"tenant:mine","cursor":"3"},{"ref":"local:private","cursor":"0"}]}`))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
}
