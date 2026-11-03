package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type environmentProfileStub struct {
	*agentProfileHandlerStub
	readTenant, readUser string
	writes               int
}

func (s *environmentProfileStub) ListAgentProfiles(ctx context.Context, _ string, _ int) ([]mysqlstore.AgentProfile, error) {
	s.readTenant, s.readUser = observability.TenantKey(ctx), observability.UserID(ctx)
	return []mysqlstore.AgentProfile{s.profile}, nil
}

func (s *environmentProfileStub) SaveAgentProfile(ctx context.Context, _ mysqlstore.AgentProfileInput) (mysqlstore.AgentProfile, error) {
	s.readTenant, s.readUser = observability.TenantKey(ctx), observability.UserID(ctx)
	s.writes++
	return s.profile, nil
}

func environmentRequest(handler http.Handler, method, path, token, tenant, user, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Tenant-Key", tenant)
	r.Header.Set("X-User-Id", user)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestSettingsEnvironmentsIsolateCatalogIdentityAndWrites(t *testing.T) {
	primary := &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{}, profile: mysqlstore.AgentProfile{ID: 1, ProfileKey: "web-profile"}}
	target := &environmentProfileStub{agentProfileHandlerStub: &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{}, profile: mysqlstore.AgentProfile{ID: 1, ProfileKey: "worker-profile"}}}
	options := Options{AuthToken: "test", TenantService: primary, SettingsDatabase: "web_db", SettingsEnvironments: []SettingsEnvironment{{ID: "channel", Label: "Worker", Database: "channel_db", TenantKey: "target-tenant", UserID: "target-user", AllowedTenantKey: "source-tenant", AllowedUserID: "source-user", Service: target}}}
	handler := NewHandler(options, nil)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		return environmentRequest(handler, method, path, "test", "source-tenant", "source-user", body)
	}
	list := request(http.MethodGet, settingsEnvironmentPrefix, "")
	var catalog SettingsEnvironmentsResponse
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &catalog) != nil || len(catalog.Environments) != 2 || !catalog.GlobalSettingsShared {
		t.Fatalf("catalog: %d %s", list.Code, list.Body.String())
	}
	if catalog.Environments[1].Database != "channel_db" || strings.Contains(list.Body.String(), "Allowed") {
		t.Fatal("invalid safe catalog")
	}
	read := request(http.MethodGet, settingsEnvironmentPrefix+"/channel/tenant/agent-profiles?tenant_key=forged&user_id=forged", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "worker-profile") || strings.Contains(read.Body.String(), "web-profile") {
		t.Fatalf("target read: %d %s", read.Code, read.Body.String())
	}
	if target.readTenant != "target-tenant" || target.readUser != "target-user" {
		t.Fatalf("target identity = %s/%s", target.readTenant, target.readUser)
	}
	write := request(http.MethodPost, settingsEnvironmentPrefix+"/channel/tenant/agent-profiles", `{"profile_key":"worker-profile","display_name":"Worker","config":{"schema_version":1}}`)
	if write.Code != http.StatusOK || target.writes != 1 {
		t.Fatalf("target write: %d %s", write.Code, write.Body.String())
	}
	if primary.lastAudit.Action != "settings.environment.write" || target.lastAudit.Action != "tenant.agent_profile.update" {
		t.Fatal("environment write must audit the source operator and target profile")
	}
	target.currentUser.Role = "member"
	denied := request(http.MethodPost, settingsEnvironmentPrefix+"/channel/tenant/agent-profiles", `{"profile_key":"worker-profile","display_name":"Worker","config":{}}`)
	if denied.Code != http.StatusForbidden || target.writes != 1 {
		t.Fatalf("target member write: %d, writes: %d", denied.Code, target.writes)
	}
	read = request(http.MethodGet, "/tenant/agent-profiles", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "web-profile") {
		t.Fatal("primary service was mutated")
	}
}

func TestSettingsEnvironmentsRejectUnauthorizedAndUnregisteredResources(t *testing.T) {
	service := &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{}, profile: mysqlstore.AgentProfile{ProfileKey: "protected"}}
	opts := Options{AuthToken: "test", TenantService: service, SettingsEnvironments: []SettingsEnvironment{{ID: "channel", Label: "Worker", TenantKey: "target", UserID: "target-user", AllowedTenantKey: "source", AllowedUserID: "operator", Service: service}, {ID: "offline", AllowedTenantKey: "source", AllowedUserID: "operator"}}}
	h := NewHandler(opts, nil)
	for _, tc := range []struct {
		path, token, tenant, user, method string
		status                            int
	}{
		{settingsEnvironmentPrefix, "bad", "source", "operator", "GET", 401},
		{settingsEnvironmentPrefix + "/channel/tenant/agent-profiles", "test", "other", "operator", "GET", 404},
		{settingsEnvironmentPrefix + "/channel/tenant/agent-profiles", "test", "source", "other", "GET", 404},
		{settingsEnvironmentPrefix + "/unknown/tenant/agent-profiles", "test", "source", "operator", "GET", 404},
		{settingsEnvironmentPrefix + "/channel/tenant/agent-tasks", "test", "source", "operator", "POST", 404},
		{settingsEnvironmentPrefix + "/channel/tenant/messages", "test", "source", "operator", "POST", 404},
		{settingsEnvironmentPrefix + "/channel/runtime/settings", "test", "source", "operator", "PUT", 404},
		{settingsEnvironmentPrefix + "/channel/tenant/../agent-profiles", "test", "source", "operator", "GET", 400},
		{settingsEnvironmentPrefix + "/offline/tenant/agent-profiles", "test", "source", "operator", "GET", 503},
	} {
		t.Run(tc.path+tc.method+tc.user+tc.token, func(t *testing.T) {
			w := environmentRequest(h, tc.method, tc.path, tc.token, tc.tenant, tc.user, "")
			if w.Code != tc.status {
				t.Fatalf("status %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
	service.currentUser.Role = "member"
	w := environmentRequest(h, "GET", settingsEnvironmentPrefix+"/channel/tenant/agent-profiles", "test", "source", "operator", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("member access: %d", w.Code)
	}
}
