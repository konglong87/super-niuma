package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/agentteam"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type agentProfileHandlerStub struct {
	*fakeTenantService
	profile mysqlstore.AgentProfile
	team    mysqlstore.AgentTeam
}

func (s *agentProfileHandlerStub) ListAgentProfiles(context.Context, string, int) ([]mysqlstore.AgentProfile, error) {
	return []mysqlstore.AgentProfile{s.profile}, nil
}
func (s *agentProfileHandlerStub) GetAgentProfile(context.Context, string, uint) (mysqlstore.AgentProfile, error) {
	return s.profile, nil
}
func (s *agentProfileHandlerStub) SaveAgentProfile(context.Context, mysqlstore.AgentProfileInput) (mysqlstore.AgentProfile, error) {
	return s.profile, nil
}
func (s *agentProfileHandlerStub) ValidateAgentProfile(context.Context, mysqlstore.AgentProfileInput) (agentprofile.ValidationReport, error) {
	return agentprofile.ValidationReport{Valid: true}, nil
}
func (s *agentProfileHandlerStub) ResolveAgentProfile(context.Context, agentprofile.ResolveRequest) (agentprofile.EffectiveProfile, error) {
	return agentprofile.EffectiveProfile{ProfileKey: s.profile.ProfileKey, ProfileVersion: s.profile.ProfileVersion}, nil
}
func (s *agentProfileHandlerStub) PublishAgentProfile(context.Context, string, uint) error {
	return nil
}
func (s *agentProfileHandlerStub) ArchiveAgentProfile(context.Context, string, uint) error {
	return nil
}
func (s *agentProfileHandlerStub) RollbackAgentProfile(context.Context, string, uint) (mysqlstore.AgentProfile, error) {
	return s.profile, nil
}
func (s *agentProfileHandlerStub) GetAgentProfileChannelBinding(context.Context, string, uint) (mysqlstore.AgentProfileChannelBinding, error) {
	return mysqlstore.AgentProfileChannelBinding{}, nil
}
func (s *agentProfileHandlerStub) UpsertAgentProfileChannelBinding(context.Context, string, uint, mysqlstore.AgentProfileChannelBindingInput) (mysqlstore.AgentProfileChannelBinding, error) {
	return mysqlstore.AgentProfileChannelBinding{ID: 3}, nil
}
func (s *agentProfileHandlerStub) ArchiveAgentProfileChannelBinding(context.Context, string, uint) error {
	return nil
}
func (s *agentProfileHandlerStub) GetAgentProfileAssignment(context.Context, string) (mysqlstore.AgentProfileAssignment, error) {
	return mysqlstore.AgentProfileAssignment{ProfileID: s.profile.ID}, nil
}
func (s *agentProfileHandlerStub) UpsertAgentProfileAssignment(context.Context, mysqlstore.AgentProfileAssignmentInput) (mysqlstore.AgentProfileAssignment, error) {
	return mysqlstore.AgentProfileAssignment{ID: 4}, nil
}
func (s *agentProfileHandlerStub) ListAgentProfileConversations(context.Context, string, uint, int) (mysqlstore.AgentProfileConversationCatalog, error) {
	return mysqlstore.AgentProfileConversationCatalog{Profile: s.profile}, nil
}

func (s *agentProfileHandlerStub) ListAgentTeams(context.Context, string, int) ([]mysqlstore.AgentTeam, error) {
	return []mysqlstore.AgentTeam{s.team}, nil
}
func (s *agentProfileHandlerStub) GetAgentTeam(context.Context, string, uint) (mysqlstore.AgentTeam, error) {
	return s.team, nil
}
func (s *agentProfileHandlerStub) SaveAgentTeam(context.Context, mysqlstore.AgentTeamInput) (mysqlstore.AgentTeam, error) {
	return s.team, nil
}
func (s *agentProfileHandlerStub) ValidateAgentTeam(context.Context, mysqlstore.AgentTeamInput, []mysqlstore.AgentTeamMemberInput, []mysqlstore.AgentTeamBindingInput) (agentteam.ValidationReport, error) {
	return agentteam.ValidationReport{Valid: true}, nil
}
func (s *agentProfileHandlerStub) PublishAgentTeam(context.Context, string, uint) error { return nil }
func (s *agentProfileHandlerStub) ArchiveAgentTeam(context.Context, string, uint) error { return nil }
func (s *agentProfileHandlerStub) RollbackAgentTeam(context.Context, string, uint) (mysqlstore.AgentTeam, error) {
	return s.team, nil
}
func (s *agentProfileHandlerStub) ReplaceAgentTeamMembers(context.Context, string, uint, []mysqlstore.AgentTeamMemberInput) ([]mysqlstore.AgentTeamMember, error) {
	return nil, nil
}
func (s *agentProfileHandlerStub) ListAgentTeamMembers(context.Context, string, uint, int) ([]mysqlstore.AgentTeamMember, error) {
	return nil, nil
}
func (s *agentProfileHandlerStub) ReplaceAgentTeamBindings(context.Context, string, uint, []mysqlstore.AgentTeamBindingInput) ([]mysqlstore.AgentTeamBinding, error) {
	return nil, nil
}
func (s *agentProfileHandlerStub) ListAgentTeamBindings(context.Context, string, uint, int) ([]mysqlstore.AgentTeamBinding, error) {
	return nil, nil
}
func (s *agentProfileHandlerStub) ListAgentTeamRuns(context.Context, string, uint, int) ([]mysqlstore.AgentTeamRun, error) {
	return nil, nil
}
func (s *agentProfileHandlerStub) GetAgentTeamRun(context.Context, string) (mysqlstore.AgentTeamRun, error) {
	return mysqlstore.AgentTeamRun{}, nil
}
func (s *agentProfileHandlerStub) ListAgentTeamRunEvents(context.Context, string, uint, string, int) ([]mysqlstore.AgentTeamRunEvent, error) {
	return nil, nil
}
func (s *agentProfileHandlerStub) ListAgentTeamMailbox(context.Context, string, uint, string, int) ([]mysqlstore.AgentTeamMailbox, error) {
	return nil, nil
}
func (s *agentProfileHandlerStub) CancelAgentTeamRun(context.Context, string) error { return nil }
func (s *agentProfileHandlerStub) ListChannelAccounts(context.Context, int) ([]mysqlstore.ChannelAccount, error) {
	return nil, nil
}

func TestAgentProfileRoutesRequireBearerAuth(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token"}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-profiles", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestAgentProfileListAndInvalidDraft(t *testing.T) {
	stub := &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11, currentUser: mysqlstore.User{Role: "owner", Status: "active"}}, profile: mysqlstore.AgentProfile{ID: 41, TenantID: 7, ProfileKey: "copywriter", ProfileVersion: 1, Status: "published"}, team: mysqlstore.AgentTeam{ID: 9, TenantID: 7, TeamKey: "launch", TeamVersion: 1, Status: "published"}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: stub}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-profiles?limit=10", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "tenant")
	req.Header.Set("X-User-Id", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "copywriter") {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodPost, "/tenant/agent-profiles", strings.NewReader(`{"profile_key":"copywriter","display_name":"Copywriter","config":{"schema_version":1}`))
	invalid.Header.Set("Authorization", "Bearer token")
	invalid.Header.Set("X-Tenant-Key", "tenant")
	invalid.Header.Set("X-User-Id", "user")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid draft status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentTeamListRequiresConfiguredService(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token", TenantService: &fakeTenantService{}}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-teams", nil)
	req.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentTeamRunPathRequiresPinnedTeamVersion(t *testing.T) {
	stub := &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11, currentUser: mysqlstore.User{Role: "owner", Status: "active"}}, team: mysqlstore.AgentTeam{ID: 9, TenantID: 7, TeamKey: "launch", TeamVersion: 1, Status: "published"}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: stub}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-teams/launch/runs/run-1", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "tenant")
	req.Header.Set("X-User-Id", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "team version") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentProfileEffectivePreviewReturnsResolvedMetadata(t *testing.T) {
	stub := &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11, currentUser: mysqlstore.User{Role: "owner", Status: "active"}}, profile: mysqlstore.AgentProfile{ID: 41, TenantID: 7, ProfileKey: "copywriter", ProfileVersion: 1, Status: "published"}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: stub}, nil)
	req := httptest.NewRequest(http.MethodGet, "/tenant/agent-profiles/copywriter/effective?surface=web_chat&max_tokens=512", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "tenant")
	req.Header.Set("X-User-Id", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "copywriter") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentTeamValidateAcceptsPolicyWithoutDisplayName(t *testing.T) {
	stub := &agentProfileHandlerStub{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11, currentUser: mysqlstore.User{Role: "owner", Status: "active"}}, team: mysqlstore.AgentTeam{ID: 9, TenantID: 7, TeamKey: "launch", TeamVersion: 1, Status: "published"}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: stub}, nil)
	body := `{"policy":{"orchestration":{"mode":"coordinator","coordinator_member":"editor","max_rounds":2,"max_parallel_members":1,"max_total_tokens":1000},"authorization":{"require_tenant_member":true},"output":{"phase_updates":"coordinator_only","post_member_cards":false}},"members":[],"bindings":[]}`
	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-teams/launch/validate?version=1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "tenant")
	req.Header.Set("X-User-Id", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
