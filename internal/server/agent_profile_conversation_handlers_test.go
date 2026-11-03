package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestAgentProfileConversationsReturnsSafeCatalog(t *testing.T) {
	stub := &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11, currentUser: mysqlstore.User{Role: "owner", Status: "active"}}, profile: mysqlstore.AgentProfile{ID: 41, TenantID: 7, ProfileKey: "copywriter", ProfileVersion: 1, Status: "published"}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: stub}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-profiles/copywriter/conversations?version=1", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "tenant")
	req.Header.Set("X-User-Id", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"profile_key":"copywriter"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
