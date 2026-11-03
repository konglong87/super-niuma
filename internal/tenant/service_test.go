package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func TestServiceResolveContext(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 1, TenantKey: "yutang", Name: "Yutang", Status: "active"}, userID: 12}
	svc := NewService(repo, nil)
	resolved, err := svc.ResolveContext(testTenantContext())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TenantID != 1 || resolved.UserID != 12 || resolved.UserKey != "user-1" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if repo.lastTenantKey != "yutang" || repo.lastEnsureUserKey != "user-1" || repo.lastEnsureTenantID != 1 {
		t.Fatalf("repo = %+v", repo)
	}
}

func TestServiceResolveContextOnceBindsIdentityToSameService(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 1, TenantKey: "yutang"}, userID: 12}
	svc := NewService(repo, nil)
	bound, resolved, err := svc.ResolveContextOnce(testTenantContext())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TenantID != 1 || resolved.UserID != 12 {
		t.Fatalf("resolved = %+v", resolved)
	}
	if _, err := svc.ResolveContext(bound); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveContext(bound); err != nil {
		t.Fatal(err)
	}
	if repo.getTenantCalls != 1 || repo.ensureUserCalls != 1 {
		t.Fatalf("identity repo calls = tenant:%d user:%d, want 1/1", repo.getTenantCalls, repo.ensureUserCalls)
	}
}

func TestServiceResolveContextOnceMarkerCannotCrossServices(t *testing.T) {
	firstRepo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 1, TenantKey: "yutang"}, userID: 12}
	secondRepo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 2, TenantKey: "yutang"}, userID: 22}
	first := NewService(firstRepo, nil)
	second := NewService(secondRepo, nil)
	bound, _, err := first.ResolveContextOnce(testTenantContext())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := second.ResolveContext(bound)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TenantID != 2 || resolved.UserID != 22 || secondRepo.getTenantCalls != 1 || secondRepo.ensureUserCalls != 1 {
		t.Fatalf("cross-service resolve = %+v calls=%d/%d", resolved, secondRepo.getTenantCalls, secondRepo.ensureUserCalls)
	}
}

func TestServiceResolveContextIgnoresArbitraryContextValues(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 3, TenantKey: "yutang"}, userID: 33}
	svc := NewService(repo, nil)
	forged := context.WithValue(testTenantContext(), struct{ name string }{"tenant-resolved"}, Context{TenantID: 999, UserID: 999})
	resolved, err := svc.ResolveContext(forged)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.TenantID != 3 || resolved.UserID != 33 || repo.getTenantCalls != 1 || repo.ensureUserCalls != 1 {
		t.Fatalf("forged resolve = %+v calls=%d/%d", resolved, repo.getTenantCalls, repo.ensureUserCalls)
	}
}

func TestServiceCreateHandoffLinkAndEventResolvesIdentity(t *testing.T) {
	repo := &fakeRepository{
		tenant:        mysqlstore.Tenant{ID: 7, TenantKey: "yutang"},
		userID:        11,
		handoffResult: agenttasks.HandoffLinkAndEventResult{LinkID: 101, EventID: 202},
	}
	svc := NewService(repo, nil)
	result, err := svc.CreateHandoffLinkAndEvent(testTenantContext(), agenttasks.HandoffLinkAndEventInput{
		TenantID: 99, UserID: 88, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", PayloadJSON: `{"schema":"v1"}`, HandoffIdentity: "sha", CreatedByUserID: 77,
	})
	if err != nil {
		t.Fatalf("CreateHandoffLinkAndEvent() error = %v", err)
	}
	if result.LinkID != 101 || result.EventID != 202 {
		t.Fatalf("result = %#v", result)
	}
	if repo.lastHandoffInput.TenantID != 7 || repo.lastHandoffInput.UserID != 11 || repo.lastHandoffInput.CreatedByUserID != 11 {
		t.Fatalf("handoff input = %#v, want authenticated identity", repo.lastHandoffInput)
	}
}

func TestServiceApplySessionMonitorConfigurationResolvesIdentity(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 7, TenantKey: "yutang"}, userID: 11}
	svc := NewService(repo, nil)
	result, err := svc.ApplySessionMonitorConfiguration(testTenantContext(), mysqlstore.SessionMonitorConfigurationInput{
		SessionMonitorLinkInput: mysqlstore.SessionMonitorLinkInput{TenantID: 99, UserID: 88, TargetSessionID: 41, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "observed", MetadataJSON: `{"configuration_fingerprint":"x"}`, CreatedByUserID: 77},
		ConfigurationKeyHash:    "key", ConfigurationFingerprint: "fingerprint",
	})
	if err != nil || result.Link.ID != 71 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	input := repo.lastSessionMonitorConfigurationInput
	if input.TenantID != 7 || input.UserID != 11 || input.CreatedByUserID != 77 || input.TargetSessionID != 41 {
		t.Fatalf("forwarded input=%#v", input)
	}
}

func TestServiceGetAndSaveCurrentUser(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 1, TenantKey: "yutang", Name: "Yutang", Status: "active"},
		userID: 12,
		user:   mysqlstore.User{ID: 12, TenantID: 1, UserKey: "user-1", Email: "u@example.com", DisplayName: "User One", Role: "admin", Status: "active"},
	}
	svc := NewService(repo, nil)
	user, err := svc.GetCurrentUser(testTenantContext())
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != 12 || repo.lastGetUserID != 12 {
		t.Fatalf("user=%+v repo=%+v", user, repo)
	}
	id, err := svc.SaveCurrentUser(testTenantContext(), UserRequest{UserInfoJSON: `{"lang":"go"}`})
	if err != nil {
		t.Fatal(err)
	}
	if id != 12 {
		t.Fatalf("id = %d", id)
	}
	if repo.lastUserInput.Email != "u@example.com" || repo.lastUserInput.Role != "admin" || repo.lastUserInput.UserInfoJSON != `{"lang":"go"}` {
		t.Fatalf("user input = %+v", repo.lastUserInput)
	}
}

func TestServiceListTenantUsers(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 1, TenantKey: "yutang", Name: "Yutang", Status: "active"},
		userID: 12,
		users:  []mysqlstore.User{{ID: 12, TenantID: 1, UserKey: "user-1"}, {ID: 13, TenantID: 1, UserKey: "user-2"}},
	}
	svc := NewService(repo, nil)
	users, err := svc.ListTenantUsers(testTenantContext(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || repo.lastListUsersTenantID != 1 || repo.lastListUsersLimit != 5 {
		t.Fatalf("users=%+v repo=%+v", users, repo)
	}
}

func TestServiceAdminUserCRUDRequiresRole(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID: 10,
		user:   mysqlstore.User{ID: 10, Role: "admin", Status: "active"},
	}
	svc := NewService(repo, nil)
	id, err := svc.SaveTenantUser(testTenantContext(), UserRequest{UserKey: "user-two", DisplayName: "User Two", Role: "member", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 10 || repo.lastUserInput.UserKey != "user-two" || repo.lastUserInput.TenantID != 4 {
		t.Fatalf("user input = %+v id=%d", repo.lastUserInput, id)
	}
	if err := svc.ArchiveTenantUser(testTenantContext(), "user-two"); err != nil {
		t.Fatal(err)
	}
	if repo.lastArchivedUserKey != "user-two" {
		t.Fatalf("archived user = %q", repo.lastArchivedUserKey)
	}
	repo.user.Role = "member"
	if _, err := svc.SaveTenantUser(testTenantContext(), UserRequest{UserKey: "user-three"}); err != ErrForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceTenantAdminCRUDRequiresRole(t *testing.T) {
	repo := &fakeRepository{
		tenant:  mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:  10,
		user:    mysqlstore.User{ID: 10, Role: "owner", Status: "active"},
		tenants: []mysqlstore.Tenant{{ID: 4, TenantKey: "yutang"}, {ID: 5, TenantKey: "acme"}},
	}
	svc := NewService(repo, nil)
	id, err := svc.SaveTenant(testTenantContext(), TenantRequest{TenantKey: "acme", Name: "Acme", Status: "active", SettingsJSON: `{"region":"us"}`})
	if err != nil {
		t.Fatal(err)
	}
	if id != 5 || repo.lastTenantInput.TenantKey != "acme" || repo.lastTenantInput.SettingsJSON == "" {
		t.Fatalf("tenant input = %+v id=%d", repo.lastTenantInput, id)
	}
	tenants, err := svc.ListTenants(testTenantContext(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 2 || repo.lastTenantListLimit != 7 {
		t.Fatalf("tenants=%+v repo=%+v", tenants, repo)
	}
	if err := svc.ArchiveTenant(testTenantContext(), "acme"); err != nil {
		t.Fatal(err)
	}
	if repo.lastArchivedTenantKey != "acme" {
		t.Fatalf("archived tenant = %q", repo.lastArchivedTenantKey)
	}
	repo.user.Role = "member"
	if _, err := svc.SaveTenant(testTenantContext(), TenantRequest{TenantKey: "blocked"}); err != ErrForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceTenantAdminPagedLists(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID: 10,
		user:   mysqlstore.User{ID: 10, Role: "owner", Status: "active"},
		tenants: []mysqlstore.Tenant{
			{ID: 1, TenantKey: "alpha"},
			{ID: 2, TenantKey: "bravo"},
			{ID: 3, TenantKey: "charlie"},
		},
		users: []mysqlstore.User{
			{ID: 11, TenantID: 4, UserKey: "user-a"},
			{ID: 12, TenantID: 4, UserKey: "user-b"},
			{ID: 13, TenantID: 4, UserKey: "user-c"},
		},
		auditLogs: []mysqlstore.AuditLog{
			{ID: 21, Action: "tenant.user.save"},
			{ID: 22, Action: "tenant.user.archive"},
			{ID: 23, Action: "tenant.memory.upsert"},
		},
		telemetryEvents: []mysqlstore.TelemetryEvent{
			{Event: telemetry.Event{ID: 31, Name: "api.request.finished"}},
			{Event: telemetry.Event{ID: 32, Name: "model.request.finished"}},
			{Event: telemetry.Event{ID: 33, Name: "tool.execution.finished"}},
		},
	}
	svc := NewService(repo, nil)
	tenants, err := svc.ListTenantsPage(testTenantContext(), ListOptions{Limit: 2, Cursor: 5, Search: "ta"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants.Data) != 2 || !tenants.HasMore || tenants.NextCursor != "7" || repo.lastTenantListOptions.Search != "ta" || repo.lastTenantListOptions.Limit != 3 || repo.lastTenantListOptions.Cursor != 5 {
		t.Fatalf("tenants=%+v opts=%+v", tenants, repo.lastTenantListOptions)
	}
	users, err := svc.ListTenantUsersPage(testTenantContext(), ListOptions{Limit: 2, Search: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if len(users.Data) != 2 || !users.HasMore || users.NextCursor != "2" || repo.lastListUsersTenantID != 4 || repo.lastListUsersOptions.Limit != 3 {
		t.Fatalf("users=%+v opts=%+v", users, repo.lastListUsersOptions)
	}
	audit, err := svc.ListAuditLogsPage(testTenantContext(), ListOptions{Limit: 2, Search: "archive"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Data) != 2 || !audit.HasMore || audit.NextCursor != "2" || repo.lastAuditOptions.Search != "archive" || repo.lastAuditOptions.Limit != 3 {
		t.Fatalf("audit=%+v opts=%+v", audit, repo.lastAuditOptions)
	}
	events, err := svc.ListTelemetryEventsPage(testTenantContext(), ListOptions{Limit: 2, Search: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Data) != 2 || !events.HasMore || events.NextCursor != "2" || repo.lastTelemetryOptions.Search != "request" || repo.lastTelemetryOptions.Limit != 3 {
		t.Fatalf("telemetry=%+v opts=%+v", events, repo.lastTelemetryOptions)
	}
}

func TestServiceRequiresTenantAndUserHeaders(t *testing.T) {
	svc := NewService(&fakeRepository{}, nil)
	if _, err := svc.ResolveContext(context.Background()); !errors.Is(err, ErrMissingTenantKey) {
		t.Fatalf("missing tenant err = %v", err)
	}
	ctx := observability.WithRequestValues(context.Background(), "trace", "user-1", "")
	if _, err := svc.ResolveContext(ctx); !errors.Is(err, ErrMissingTenantKey) {
		t.Fatalf("default tenant err = %v", err)
	}
	ctx = observability.WithRequestValues(context.Background(), "trace", "", "yutang")
	if _, err := svc.ResolveContext(ctx); !errors.Is(err, ErrMissingUserID) {
		t.Fatalf("missing user err = %v", err)
	}
}

func TestServiceUpsertMemoryUsesResolvedIDs(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 3, TenantKey: "aihe"}, userID: 9, memoryID: 44}
	svc := NewService(repo, nil)
	id, err := svc.UpsertMemory(testTenantContext(), MemoryRequest{
		MemoryKey:  "pref.editor",
		Category:   "profile",
		Content:    "vim",
		Importance: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 44 {
		t.Fatalf("id = %d", id)
	}
	if repo.lastMemoryInput.TenantID != 3 || repo.lastMemoryInput.UserID != 9 || repo.lastMemoryInput.MemoryKey != "pref.editor" {
		t.Fatalf("memory input = %+v", repo.lastMemoryInput)
	}
}

func TestServiceReviewAutoMemoryApprovesAndRejectsCandidates(t *testing.T) {
	repo := &fakeRepository{
		tenant:   mysqlstore.Tenant{ID: 3, TenantKey: "aihe"},
		userID:   9,
		memoryID: 44,
		memories: []mysqlstore.Memory{{
			ID:           12,
			MemoryKey:    "auto.pending.auto.preference.abc",
			Category:     "automem_pending",
			Content:      "我偏好简洁中文回答",
			MetadataJSON: `{"category":"preference","confidence":0.72}`,
			Importance:   5,
			UpdatedAt:    time.Now().UTC(),
		}},
	}
	svc := NewService(repo, nil)
	id, err := svc.ReviewAutoMemory(testTenantContext(), AutoMemoryReviewRequest{MemoryKey: "auto.pending.auto.preference.abc", Action: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 44 || len(repo.memoryInputs) != 2 {
		t.Fatalf("approved writes = %+v id=%d", repo.memoryInputs, id)
	}
	if repo.memoryInputs[0].Category != "preference" || repo.memoryInputs[0].Source != "automem-approved" || !strings.Contains(repo.memoryInputs[0].MetadataJSON, `"review_status":"approved"`) {
		t.Fatalf("approved target memory = %+v", repo.memoryInputs[0])
	}
	if repo.memoryInputs[1].Category != "automem_approved" || repo.memoryInputs[1].MemoryKey != "auto.pending.auto.preference.abc" {
		t.Fatalf("approved candidate marker = %+v", repo.memoryInputs[1])
	}

	repo.memoryInputs = nil
	id, err = svc.ReviewAutoMemory(testTenantContext(), AutoMemoryReviewRequest{MemoryKey: "auto.pending.auto.preference.abc", Action: "reject"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 44 || repo.lastMemoryInput.Category != "automem_rejected" || repo.lastMemoryInput.Source != "automem-rejected" {
		t.Fatalf("rejected memory = %+v id=%d", repo.lastMemoryInput, id)
	}
}

func TestServiceReviewExplicitRememberApprovesAndRejectsCandidates(t *testing.T) {
	repo := &fakeRepository{
		tenant:   mysqlstore.Tenant{ID: 3, TenantKey: "aihe"},
		userID:   9,
		memoryID: 45,
		memories: []mysqlstore.Memory{{
			ID:           13,
			MemoryKey:    "explicit.pending.preference.def",
			Category:     "explicit_pending",
			Content:      "我喜欢简洁中文回答",
			MetadataJSON: `{"category":"preference","candidate_type":"explicit_remember"}`,
			Importance:   8,
			UpdatedAt:    time.Now().UTC(),
		}},
	}
	svc := NewService(repo, nil)
	id, err := svc.ReviewMemoryCandidate(testTenantContext(), MemoryReviewRequest{MemoryKey: "explicit.pending.preference.def", Action: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 45 || len(repo.memoryInputs) != 2 {
		t.Fatalf("approved writes = %+v id=%d", repo.memoryInputs, id)
	}
	if repo.memoryInputs[0].MemoryKey != "preference.def" || repo.memoryInputs[0].Category != "preference" || repo.memoryInputs[0].Source != "explicit-user-remember-approved" {
		t.Fatalf("approved target memory = %+v", repo.memoryInputs[0])
	}
	if repo.memoryInputs[1].Category != "explicit_approved" || repo.memoryInputs[1].MemoryKey != "explicit.pending.preference.def" {
		t.Fatalf("approved candidate marker = %+v", repo.memoryInputs[1])
	}

	repo.memoryInputs = nil
	id, err = svc.ReviewMemoryCandidate(testTenantContext(), MemoryReviewRequest{MemoryKey: "explicit.pending.preference.def", Action: "reject"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 45 || repo.lastMemoryInput.Category != "explicit_rejected" || repo.lastMemoryInput.Source != "explicit-remember-rejected" {
		t.Fatalf("rejected memory = %+v id=%d", repo.lastMemoryInput, id)
	}
}

func TestServiceMemoryReviewListsExplicitBeforeAutoMem(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 3, TenantKey: "aihe"},
		userID: 9,
		memories: []mysqlstore.Memory{{
			ID:        1,
			MemoryKey: "auto.pending.auto.preference.old",
			Category:  "automem_pending",
			UpdatedAt: time.Now().UTC().Add(time.Hour),
		}, {
			ID:        2,
			MemoryKey: "explicit.pending.preference.new",
			Category:  "explicit_pending",
			UpdatedAt: time.Now().UTC().Add(-time.Hour),
		}},
	}
	svc := NewService(repo, nil)
	items, err := svc.ListMemoryReviewCandidates(testTenantContext(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Category != "explicit_pending" {
		t.Fatalf("explicit candidate should not be hidden by automem limit: %+v", items)
	}
}

func TestServiceMemoryReviewFiltersByTypeRiskAndSourceSession(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 3, TenantKey: "aihe"},
		userID: 9,
		memories: []mysqlstore.Memory{{
			ID:           1,
			MemoryKey:    "auto.pending.auto.preference.old",
			Category:     "automem_pending",
			MetadataJSON: `{"candidate_type":"automem_candidate","risk_status":"low_risk","source_session_id":42}`,
			UpdatedAt:    time.Now().UTC(),
		}, {
			ID:           2,
			MemoryKey:    "explicit.pending.preference.new",
			Category:     "explicit_pending",
			MetadataJSON: `{"candidate_type":"explicit_remember","risk_status":"low_risk","source_session_id":42}`,
			UpdatedAt:    time.Now().UTC().Add(-time.Minute),
		}, {
			ID:           3,
			MemoryKey:    "explicit.pending.preference.other",
			Category:     "explicit_pending",
			MetadataJSON: `{"candidate_type":"explicit_remember","risk_status":"needs_review","source_session_id":99}`,
			UpdatedAt:    time.Now().UTC().Add(time.Minute),
		}},
	}
	svc := NewService(repo, nil)
	items, err := svc.ListMemoryReviewCandidatesFiltered(testTenantContext(), MemoryReviewListOptions{
		Limit:           10,
		CandidateType:   "explicit_remember",
		RiskStatus:      "low_risk",
		SourceSessionID: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MemoryKey != "explicit.pending.preference.new" {
		t.Fatalf("filtered candidates = %+v", items)
	}
}

func TestServiceMemoryReviewArchiveTelemetryUsesArchivedEvent(t *testing.T) {
	repo := &fakeRepository{
		tenant:   mysqlstore.Tenant{ID: 3, TenantKey: "aihe"},
		userID:   9,
		memoryID: 46,
		memories: []mysqlstore.Memory{{
			ID:        13,
			MemoryKey: "explicit.pending.preference.def",
			Category:  "explicit_pending",
			Content:   "我喜欢简洁中文回答",
		}},
	}
	sink := telemetry.NewMemorySink()
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(sink))
	defer restore()

	svc := NewService(repo, nil)
	if _, err := svc.ReviewMemoryCandidate(testTenantContext(), MemoryReviewRequest{MemoryKey: "explicit.pending.preference.def", Action: "archive"}); err != nil {
		t.Fatal(err)
	}
	if repo.lastMemoryInput.Category != "explicit_archived" {
		t.Fatalf("archived marker = %+v", repo.lastMemoryInput)
	}
	if !tenantTelemetryEventsContain(sink.Events(), "memory.remember.archived") {
		t.Fatalf("missing archived telemetry: %+v", sink.Events())
	}
	if tenantTelemetryEventsContain(sink.Events(), "memory.remember.rejected") {
		t.Fatalf("archive emitted rejected telemetry: %+v", sink.Events())
	}
}

func TestServiceUpsertSkillUsesResolvedTenantAndCreator(t *testing.T) {
	repo := &fakeRepository{
		tenant:  mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:  10,
		user:    mysqlstore.User{ID: 10, Role: "admin", Status: "active"},
		skillID: 88,
	}
	svc := NewService(repo, nil)
	enabled := false
	id, err := svc.UpsertSkill(testTenantContext(), SkillRequest{
		SkillKey:  "go-review",
		Name:      "Go Review",
		ContentMD: "# Skill",
		Version:   2,
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 88 {
		t.Fatalf("id = %d", id)
	}
	if repo.lastSkillInput.TenantID != 4 || repo.lastSkillInput.CreatedByUserID != 10 || repo.lastSkillInput.Enabled {
		t.Fatalf("skill input = %+v", repo.lastSkillInput)
	}
	repo.user.Role = "member"
	if _, err := svc.UpsertSkill(testTenantContext(), SkillRequest{SkillKey: "blocked", Name: "Blocked", ContentMD: "# Blocked"}); err != ErrForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceListAndGetSkills(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID: 10,
		skills: []mysqlstore.Skill{{ID: 1, SkillKey: "go-review", Name: "Go Review", Enabled: true}},
	}
	svc := NewService(repo, nil)
	items, err := svc.ListSkills(testTenantContext(), true, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SkillKey != "go-review" || !repo.lastSkillEnabledOnly {
		t.Fatalf("items=%+v repo=%+v", items, repo)
	}
	item, err := svc.GetSkill(testTenantContext(), "go-review", 0)
	if err != nil {
		t.Fatal(err)
	}
	if item.SkillKey != "go-review" || repo.lastSkillKey != "go-review" {
		t.Fatalf("item=%+v repo=%+v", item, repo)
	}
}

func TestServiceRollbackSkillCopiesHistoricalVersionToNewLatest(t *testing.T) {
	repo := &fakeRepository{
		tenant:  mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:  10,
		user:    mysqlstore.User{ID: 10, Role: "admin", Status: "active"},
		skillID: 91,
		skills: []mysqlstore.Skill{
			{ID: 11, SkillKey: "go-review", Name: "Go Review", Description: "old", ContentMD: "# Old", ConfigJSON: `{"mode":"old"}`, Version: 1, Enabled: false},
			{ID: 12, SkillKey: "go-review", Name: "Go Review", Description: "new", ContentMD: "# New", ConfigJSON: `{"mode":"new"}`, Version: 2, Enabled: true},
		},
	}
	svc := NewService(repo, nil)
	result, err := svc.RollbackSkill(testTenantContext(), SkillRollbackRequest{SkillKey: "go-review", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != 91 || result.SkillKey != "go-review" || result.FromVersion != 1 || result.Version != 3 {
		t.Fatalf("result = %+v", result)
	}
	if repo.lastSkillRollbackInput.TenantID != 4 || repo.lastSkillRollbackInput.CreatedByUserID != 10 || repo.lastSkillRollbackInput.FromVersion != 1 {
		t.Fatalf("rollback input = %+v", repo.lastSkillRollbackInput)
	}
}

func TestServiceRollbackSkillRejectsStaleTargetVersion(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID: 10,
		user:   mysqlstore.User{ID: 10, Role: "admin", Status: "active"},
		skills: []mysqlstore.Skill{{ID: 12, SkillKey: "go-review", Version: 2}},
	}
	svc := NewService(repo, nil)
	_, err := svc.RollbackSkill(testTenantContext(), SkillRollbackRequest{SkillKey: "go-review", Version: 1, TargetVersion: 2})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceRollbackSkillRequiresAdminRole(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID: 10,
		user:   mysqlstore.User{ID: 10, Role: "member", Status: "active"},
		skills: []mysqlstore.Skill{{ID: 11, SkillKey: "go-review", Version: 1}},
	}
	svc := NewService(repo, nil)
	if _, err := svc.RollbackSkill(testTenantContext(), SkillRollbackRequest{SkillKey: "go-review", Version: 1}); err != ErrForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceUpsertSkillOverrideResolvesSkillAndUser(t *testing.T) {
	repo := &fakeRepository{
		tenant:     mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:     10,
		skillID:    17,
		skills:     []mysqlstore.Skill{{ID: 17, SkillKey: "go-review", Name: "Go Review", Version: 2}},
		overrideID: 99,
	}
	svc := NewService(repo, nil)
	enabled := false
	id, err := svc.UpsertSkillOverride(testTenantContext(), SkillOverrideRequest{
		SkillKey:   "go-review",
		Version:    2,
		Enabled:    &enabled,
		ConfigJSON: `{"level":"quiet"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 99 {
		t.Fatalf("id = %d", id)
	}
	if repo.lastSkillKey != "go-review" || repo.lastSkillVersion != 2 {
		t.Fatalf("skill lookup = %q v%d", repo.lastSkillKey, repo.lastSkillVersion)
	}
	if repo.lastOverrideInput.TenantID != 4 || repo.lastOverrideInput.UserID != 10 || repo.lastOverrideInput.SkillID != 17 || repo.lastOverrideInput.Enabled {
		t.Fatalf("override input = %+v", repo.lastOverrideInput)
	}
}

func TestServiceListAndGetSkillOverrides(t *testing.T) {
	repo := &fakeRepository{
		tenant:    mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:    10,
		overrides: []mysqlstore.SkillOverride{{ID: 1, SkillKey: "go-review", SkillID: 17, Enabled: false}},
	}
	svc := NewService(repo, nil)
	items, err := svc.ListSkillOverrides(testTenantContext(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SkillKey != "go-review" || repo.lastOverrideLimit != 5 {
		t.Fatalf("items=%+v repo=%+v", items, repo)
	}
	item, err := svc.GetSkillOverride(testTenantContext(), "go-review", 0)
	if err != nil {
		t.Fatal(err)
	}
	if item.SkillKey != "go-review" || repo.lastOverrideSkillKey != "go-review" {
		t.Fatalf("item=%+v repo=%+v", item, repo)
	}
}

func TestServiceListAndGetEffectiveSkills(t *testing.T) {
	repo := &fakeRepository{
		tenant:          mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:          10,
		effectiveSkills: []mysqlstore.EffectiveSkill{{Skill: mysqlstore.Skill{ID: 17, SkillKey: "go-review", Enabled: true}, OverrideID: 23}},
	}
	svc := NewService(repo, nil)
	items, err := svc.ListEffectiveSkills(testTenantContext(), true, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].OverrideID != 23 || !repo.lastEffectiveEnabledOnly || repo.lastEffectiveLimit != 5 {
		t.Fatalf("items=%+v repo=%+v", items, repo)
	}
	item, err := svc.GetEffectiveSkill(testTenantContext(), "go-review", 2)
	if err != nil {
		t.Fatal(err)
	}
	if item.SkillKey != "go-review" || repo.lastEffectiveSkillKey != "go-review" || repo.lastEffectiveVersion != 2 {
		t.Fatalf("item=%+v repo=%+v", item, repo)
	}
}

func TestServiceSaveGetAndListDocuments(t *testing.T) {
	repo := &fakeRepository{
		tenant:     mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:     10,
		documentID: 41,
		documents:  []mysqlstore.Document{{ID: 41, DocType: "CLAUDE.md", Version: 2, Active: true}},
	}
	svc := NewService(repo, nil)
	id, err := svc.SaveDocument(testTenantContext(), DocumentRequest{
		DocType:   "CLAUDE.md",
		Title:     "Project Instructions",
		ContentMD: "# Rules",
		Version:   2,
		Active:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 41 || repo.lastDocument.TenantID != 4 || repo.lastDocument.UserID != 10 || repo.lastDocument.DocType != "CLAUDE.md" {
		t.Fatalf("id=%d doc=%+v", id, repo.lastDocument)
	}
	doc, err := svc.GetActiveDocument(testTenantContext(), "CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if doc.DocType != "CLAUDE.md" || repo.lastDocumentType != "CLAUDE.md" {
		t.Fatalf("doc=%+v repo=%+v", doc, repo)
	}
	items, err := svc.ListDocuments(testTenantContext(), "CLAUDE.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || repo.lastDocumentType != "CLAUDE.md" || repo.lastDocumentLimit != 5 {
		t.Fatalf("items=%+v repo=%+v", items, repo)
	}
}

func TestServiceKnowledgeDocumentsAndSearch(t *testing.T) {
	repo := &fakeRepository{
		tenant:      mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:      10,
		knowledgeID: 77,
		knowledgeDocs: []mysqlstore.KnowledgeDocument{{
			ID:         77,
			Title:      "Billing FAQ",
			SourceType: "manual",
			Status:     "active",
		}},
		knowledgeChunks: []mysqlstore.KnowledgeChunk{{
			ID:         88,
			DocumentID: 77,
			Title:      "Billing FAQ",
			Content:    "Refunds are processed within 7 days.",
			Score:      10,
		}},
	}
	svc := NewService(repo, nil)
	id, err := svc.SaveKnowledgeDocument(testTenantContext(), KnowledgeDocumentRequest{
		Title:   "Billing FAQ",
		Content: strings.Repeat("refund policy ", 120) + "\n\nInvoices are monthly.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 77 || repo.lastKnowledgeDocument.TenantID != 4 || repo.lastKnowledgeDocument.UserID != 10 || len(repo.lastKnowledgeChunks) < 2 {
		t.Fatalf("id=%d doc=%+v chunks=%+v", id, repo.lastKnowledgeDocument, repo.lastKnowledgeChunks)
	}
	docs, err := svc.ListKnowledgeDocuments(testTenantContext(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || repo.lastKnowledgeLimit != 5 {
		t.Fatalf("docs=%+v repo=%+v", docs, repo)
	}
	chunks, err := svc.SearchKnowledgeChunks(testTenantContext(), KnowledgeSearchRequest{Query: "refund", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || repo.lastKnowledgeSearch.Query != "refund" || repo.lastKnowledgeSearch.Limit != 3 || repo.lastKnowledgeSearch.UserID != 10 {
		t.Fatalf("chunks=%+v search=%+v", chunks, repo.lastKnowledgeSearch)
	}
}

func TestServiceSaveAndGetProfile(t *testing.T) {
	repo := &fakeRepository{
		tenant:    mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:    10,
		profileID: 31,
		profile:   mysqlstore.Profile{ID: 31, ProfileVersion: 2, ProfileJSON: `{"style":"concise"}`},
	}
	svc := NewService(repo, nil)
	id, err := svc.SaveProfile(testTenantContext(), ProfileRequest{
		ProfileVersion:         2,
		Summary:                "prefers concise answers",
		ProfileJSON:            `{"style":"concise"}`,
		GeneratedFromSessionID: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 31 {
		t.Fatalf("id = %d", id)
	}
	if repo.lastProfileInput.TenantID != 4 || repo.lastProfileInput.UserID != 10 || repo.lastProfileInput.ProfileVersion != 2 {
		t.Fatalf("profile input = %+v", repo.lastProfileInput)
	}
	profile, err := svc.GetProfile(testTenantContext(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != 31 || repo.lastProfileVersion != 2 {
		t.Fatalf("profile=%+v repo=%+v", profile, repo)
	}
}

func TestServiceListSessionsAndMessages(t *testing.T) {
	repo := &fakeRepository{
		tenant:      mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:      10,
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "session-1", Status: "active"}},
		messageRows: []mysqlstore.Message{{ID: 90, SessionID: 5, TurnIndex: 1, Role: "user", Content: "hello"}},
	}
	svc := NewService(repo, nil)
	sessions, err := svc.ListSessions(testTenantContext(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || repo.lastSessionLimit != 7 {
		t.Fatalf("sessions=%+v repo=%+v", sessions, repo)
	}
	messages, err := svc.ListMessages(testTenantContext(), 5, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || repo.lastMessageSessionID != 5 || repo.lastMessageLimit != 8 {
		t.Fatalf("messages=%+v repo=%+v", messages, repo)
	}
}

func TestServiceSessionLifecycleUsesResolvedIDs(t *testing.T) {
	repo := &fakeRepository{
		tenant:   mysqlstore.Tenant{ID: 4, TenantKey: "yutang"},
		userID:   10,
		sessions: []mysqlstore.Session{{ID: 5, SessionKey: "session-1", Status: "active"}},
	}
	svc := NewService(repo, nil)
	item, err := svc.GetSession(testTenantContext(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != 5 || repo.lastSessionID != 5 {
		t.Fatalf("item=%+v repo=%+v", item, repo)
	}
	if err := svc.UpdateSession(testTenantContext(), 5, SessionRequest{Title: "Renamed", Status: "paused"}); err != nil {
		t.Fatal(err)
	}
	if repo.lastSessionInput.ID != 5 || repo.lastSessionInput.TenantID != 4 || repo.lastSessionInput.UserID != 10 || repo.lastSessionInput.Title != "Renamed" {
		t.Fatalf("session input = %+v", repo.lastSessionInput)
	}
	if err := svc.ArchiveSession(testTenantContext(), 5); err != nil {
		t.Fatal(err)
	}
	if repo.archivedSessionID != 5 {
		t.Fatalf("repo = %+v", repo)
	}
}

func TestServiceUpsertMessageUsesResolvedIDs(t *testing.T) {
	repo := &fakeRepository{
		tenant:    mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:    5,
		messageID: 77,
		sessions:  []mysqlstore.Session{{ID: 8, SessionKey: "session-8", Status: "active"}},
	}
	svc := NewService(repo, nil)
	id, err := svc.UpsertMessage(testTenantContext(), MessageRequest{
		SessionID:   8,
		TurnIndex:   1,
		Role:        "assistant",
		Content:     "ok",
		InputTokens: 3,
		OutputToken: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 77 {
		t.Fatalf("id = %d", id)
	}
	if repo.lastMessageInput.TenantID != 2 || repo.lastMessageInput.UserID != 5 || repo.lastMessageInput.SessionID != 8 {
		t.Fatalf("message input = %+v", repo.lastMessageInput)
	}
	if repo.lastSessionID != 8 {
		t.Fatalf("session was not checked before write: repo=%+v", repo)
	}
}

func TestServiceSaveQueryTurnUsesResolvedIDs(t *testing.T) {
	repo := &fakeRepository{
		tenant:    mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:    5,
		sessionID: 8,
		messageID: 77,
	}
	svc := NewService(repo, nil)
	out, err := svc.SaveQueryTurn(testTenantContext(), QueryTurnRequest{
		Session:   SessionRequest{SessionKey: "session-8", Title: "Title", Model: "claude"},
		User:      MessageRequest{TurnIndex: 1, Role: "user", Content: "hello"},
		Assistant: MessageRequest{TurnIndex: 2, Role: "assistant", Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != 8 || out.UserMessageID != 77 || out.AssistantMessageID != 78 {
		t.Fatalf("out = %+v", out)
	}
	// 租户归属只能来自 ResolveContext，绝不能由调用方传进来。
	session := repo.lastQueryTurnInput.Session
	if session.TenantID != 2 || session.UserID != 5 || session.SessionKey != "session-8" {
		t.Fatalf("session input = %+v", session)
	}
	if repo.lastQueryTurnInput.User.Role != "user" || repo.lastQueryTurnInput.Assistant.Role != "assistant" {
		t.Fatalf("turn input = %+v", repo.lastQueryTurnInput)
	}
}

func TestServiceForkSessionUsesResolvedIDs(t *testing.T) {
	repo := &fakeRepository{
		tenant:    mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:    5,
		sessionID: 9,
	}
	svc := NewService(repo, nil)
	out, err := svc.ForkSession(testTenantContext(), ForkSessionRequest{
		Session: SessionRequest{SessionKey: "branch-9", Title: "Branch", Model: "claude"},
		Messages: []MessageRequest{
			{TurnIndex: 1, Role: "user", Content: "hello"},
			{TurnIndex: 2, Role: "assistant", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != 9 || out.CopiedMessages != 2 {
		t.Fatalf("out = %+v", out)
	}
	// 租户归属只能来自 ResolveContext，绝不能由调用方传进来。
	session := repo.lastForkSessionInput.Session
	if session.TenantID != 2 || session.UserID != 5 || session.SessionKey != "branch-9" {
		t.Fatalf("session input = %+v", session)
	}
	if len(repo.lastForkSessionInput.Messages) != 2 || repo.lastForkSessionInput.Messages[1].Role != "assistant" {
		t.Fatalf("fork input = %+v", repo.lastForkSessionInput)
	}
}

func TestServiceListAllMessagesRefusesToTruncate(t *testing.T) {
	repo := &fakeRepository{
		tenant:      mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:      5,
		messageRows: make([]mysqlstore.Message, 11),
	}
	svc := NewService(repo, nil)
	if _, err := svc.ListAllMessages(testTenantContext(), 8, 10); !errors.Is(err, mysqlstore.ErrTooManyMessages) {
		t.Fatalf("err = %v, want ErrTooManyMessages", err)
	}
	if repo.lastMessageMaxRows != 10 {
		t.Fatalf("maxRows = %d, want 10", repo.lastMessageMaxRows)
	}
}

func TestServiceMaxMessageTurnUsesResolvedIDs(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID: 5,
		messageRows: []mysqlstore.Message{
			{TurnIndex: 3}, {TurnIndex: 620}, {TurnIndex: 17},
		},
	}
	svc := NewService(repo, nil)
	turn, err := svc.MaxMessageTurn(testTenantContext(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if turn != 620 {
		t.Fatalf("turn = %d, want 620", turn)
	}
}

// 定点查询必须带上解析出来的 (tenantID, userID)。换查询方式不能把归属校验丢掉 ——
// 消息 id 是全表唯一的，只按 id 查照样查得到，等于把越权读打开。
func TestServiceGetMessagePassesResolvedOwner(t *testing.T) {
	repo := &fakeRepository{
		tenant:      mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:      5,
		messageRows: []mysqlstore.Message{{ID: 600, TurnIndex: 600, Role: "assistant"}},
	}
	svc := NewService(repo, nil)
	message, err := svc.GetMessage(testTenantContext(), 8, 600)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != 600 {
		t.Fatalf("message = %+v", message)
	}
	if repo.lastMessageOwner != [2]uint64{2, 5} || repo.lastMessageSessionID != 8 || repo.lastMessageID != 600 {
		t.Fatalf("owner=%v session=%d id=%d", repo.lastMessageOwner, repo.lastMessageSessionID, repo.lastMessageID)
	}
}

func TestServiceGetMessageReportsNotFound(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"}, userID: 5}
	svc := NewService(repo, nil)
	if _, err := svc.GetMessage(testTenantContext(), 8, 600); !errors.Is(err, mysqlstore.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestServicePreviousUserMessagePassesResolvedOwner(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID: 5,
		messageRows: []mysqlstore.Message{
			{ID: 1, TurnIndex: 1, Role: "user"},
			{ID: 599, TurnIndex: 599, Role: "user"},
			{ID: 601, TurnIndex: 601, Role: "user"},
		},
	}
	svc := NewService(repo, nil)
	message, err := svc.PreviousUserMessage(testTenantContext(), 8, 600)
	if err != nil {
		t.Fatal(err)
	}
	// 要的是 600 之前**最后**一条 user 消息，不是第一条，也不是 600 之后的。
	if message.ID != 599 {
		t.Fatalf("message = %+v", message)
	}
	if repo.lastMessageOwner != [2]uint64{2, 5} || repo.lastMessageBeforeTurn != 600 {
		t.Fatalf("owner=%v beforeTurn=%d", repo.lastMessageOwner, repo.lastMessageBeforeTurn)
	}
}

// MessageByKey 同样要把解析出来的 (tenantID, userID) 传下去 —— 生成列上的索引以
// session_id 起头，归属校验完全靠 WHERE 里那两列，在这一层丢掉就等于开了越权读。
func TestServiceMessageByKeyPassesResolvedOwner(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID: 5,
		messageRows: []mysqlstore.Message{
			{ID: 621, TurnIndex: 621, Role: "user", ContentJSON: `{"mobile":{"message_key":"replay-1"}}`},
			{ID: 622, TurnIndex: 622, Role: "assistant", ContentJSON: `{"mobile":{"message_key":"replay-1"}}`},
		},
	}
	svc := NewService(repo, nil)
	message, found, err := svc.MessageByKey(testTenantContext(), 8, "assistant", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	// 同一个 key 落在两条消息上，role 决定取哪一条。
	if !found || message.ID != 622 {
		t.Fatalf("found=%v message=%+v", found, message)
	}
	if repo.lastMessageOwner != [2]uint64{2, 5} || repo.lastMessageSessionID != 8 || repo.lastMessageKey != "replay-1" || repo.lastMessageRole != "assistant" {
		t.Fatalf("owner=%v session=%d role=%q key=%q", repo.lastMessageOwner, repo.lastMessageSessionID, repo.lastMessageRole, repo.lastMessageKey)
	}
}

func TestServiceMessageByKeyReportsAMissWithoutAnError(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"}, userID: 5}
	svc := NewService(repo, nil)
	_, found, err := svc.MessageByKey(testTenantContext(), 8, "assistant", "never-seen")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("found = true for a key that was never written")
	}
}

// ListRecentMessages 取的是**尾部**那一段，而且 limit 要如实传下去。
func TestServiceListRecentMessagesPassesLimitAndOwner(t *testing.T) {
	rows := make([]mysqlstore.Message, 0, 620)
	for i := 1; i <= 620; i++ {
		rows = append(rows, mysqlstore.Message{ID: uint64(i), TurnIndex: uint(i)})
	}
	repo := &fakeRepository{
		tenant:      mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:      5,
		messageRows: rows,
	}
	svc := NewService(repo, nil)
	messages, err := svc.ListRecentMessages(testTenantContext(), 8, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 200 || messages[0].TurnIndex != 421 || messages[199].TurnIndex != 620 {
		t.Fatalf("got %d messages, first=%d last=%d", len(messages), messages[0].TurnIndex, messages[len(messages)-1].TurnIndex)
	}
	if repo.lastMessageOwner != [2]uint64{2, 5} || repo.lastMessageLimit != 200 {
		t.Fatalf("owner=%v limit=%d", repo.lastMessageOwner, repo.lastMessageLimit)
	}
}

func TestServiceUpsertMessageRejectsForeignSession(t *testing.T) {
	repo := &fakeRepository{
		tenant:    mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:    5,
		messageID: 77,
	}
	svc := NewService(repo, nil)
	_, err := svc.UpsertMessage(testTenantContext(), MessageRequest{
		SessionID: 99,
		TurnIndex: 1,
		Role:      "user",
		Content:   "cross tenant attempt",
	})
	if !errors.Is(err, mysqlstore.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if repo.lastMessageInput.SessionID != 0 {
		t.Fatalf("message was written without session ownership: %+v", repo.lastMessageInput)
	}
}

func TestServiceAuditAndRBAC(t *testing.T) {
	repo := &fakeRepository{
		tenant:          mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID:          5,
		user:            mysqlstore.User{ID: 5, Role: "admin", Status: "active"},
		auditID:         88,
		auditLogs:       []mysqlstore.AuditLog{{ID: 88, Action: "tenant.memory.upsert", ResourceType: "memory"}},
		telemetryID:     89,
		telemetryEvents: []mysqlstore.TelemetryEvent{{Event: telemetry.Event{ID: 89, Name: "api.request.finished", Category: telemetry.CategoryAPI}}},
	}
	svc := NewService(repo, nil)
	id, err := svc.RecordAudit(testTenantContext(), AuditRequest{Action: "tenant.memory.upsert", ResourceType: "memory", ResourceID: "55"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 88 || repo.lastAuditInput.TenantID != 2 || repo.lastAuditInput.ActorUserID != 5 || repo.lastAuditInput.TraceID != "trace-1" {
		t.Fatalf("audit input = %+v id=%d", repo.lastAuditInput, id)
	}
	items, err := svc.ListAuditLogs(testTenantContext(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || repo.lastAuditLimit != 7 {
		t.Fatalf("items=%+v limit=%d", items, repo.lastAuditLimit)
	}
	telemetryID, err := svc.RecordTelemetry(testTenantContext(), telemetry.Event{Name: "api.request.finished", Category: telemetry.CategoryAPI, Status: telemetry.StatusOK, Properties: map[string]any{"token": "redacted", "status": 200}})
	if err != nil {
		t.Fatal(err)
	}
	if telemetryID != 89 || repo.lastTelemetryInput.TenantID != 2 || repo.lastTelemetryInput.UserID != 5 || repo.lastTelemetryInput.Event.TraceID != "trace-1" {
		t.Fatalf("telemetry input = %+v id=%d", repo.lastTelemetryInput, telemetryID)
	}
	telemetryItems, err := svc.ListTelemetryEvents(testTenantContext(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(telemetryItems) != 1 || repo.lastTelemetryLimit != 8 {
		t.Fatalf("telemetry=%+v limit=%d", telemetryItems, repo.lastTelemetryLimit)
	}
	if err := svc.RequireRole(testTenantContext(), "owner", "admin"); err != nil {
		t.Fatalf("require role: %v", err)
	}
	repo.user.Role = "member"
	if err := svc.RequireRole(testTenantContext(), "owner", "admin"); err != ErrForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceListAuditRequiresAdmin(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 2, TenantKey: "gongtong"},
		userID: 5,
		user:   mysqlstore.User{ID: 5, Role: "member", Status: "active"},
	}
	svc := NewService(repo, nil)
	_, err := svc.ListAuditLogs(testTenantContext(), 10)
	if err != ErrForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceQuotaUsageReadAllowsMemberButSaveRequiresAdmin(t *testing.T) {
	repo := &fakeRepository{
		tenant:      mysqlstore.Tenant{ID: 1, TenantKey: "yutang"},
		userID:      2,
		user:        mysqlstore.User{ID: 2, Role: "member", Status: "active"},
		quotaConfig: mysqlstore.QuotaConfig{TenantID: 1, QuotaEnabled: true, Timezone: "UTC", ReserveOutputTokens: 4096, Status: "active"},
		usageDaily:  []mysqlstore.UsageDaily{{TenantID: 1, Source: "query", Model: "claude", TotalTokens: 10}},
		usageLedger: []mysqlstore.UsageLedger{{TenantID: 1, RequestID: "req-1", Source: "query", Model: "claude", TotalTokens: 10}},
		quotaEvents: []mysqlstore.QuotaEvent{{TenantID: 1, EventType: "quota.rejected.qps", Source: "query"}},
	}
	svc := NewService(repo, nil)

	if _, err := svc.GetQuotaConfig(testTenantContext()); err != nil {
		t.Fatalf("GetQuotaConfig err = %v", err)
	}
	if _, err := svc.ListUsageDailyPage(testTenantContext(), ListOptions{Limit: 2, Search: "claude"}); err != nil {
		t.Fatalf("ListUsageDailyPage err = %v", err)
	}
	if repo.lastUsageDailyOptions.Search != "claude" {
		t.Fatalf("usage daily opts = %+v", repo.lastUsageDailyOptions)
	}
	if _, err := svc.ListUsageLedgerPage(testTenantContext(), ListOptions{Limit: 2, Search: "req"}); err != nil {
		t.Fatalf("ListUsageLedgerPage err = %v", err)
	}
	if _, err := svc.ListQuotaEventsPage(testTenantContext(), ListOptions{Limit: 2, Search: "qps"}); err != nil {
		t.Fatalf("ListQuotaEventsPage err = %v", err)
	}
	if repo.lastQuotaEventOptions.Search != "qps" {
		t.Fatalf("quota event opts = %+v", repo.lastQuotaEventOptions)
	}
	if _, err := svc.SaveQuotaConfig(testTenantContext(), QuotaConfigRequest{QuotaEnabled: true}); err != ErrForbidden {
		t.Fatalf("save err = %v", err)
	}
}

func TestServiceAgentTaskLifecycleUsesResolvedIDs(t *testing.T) {
	repo := &fakeRepository{
		tenant:  mysqlstore.Tenant{ID: 7, TenantKey: "yutang"},
		userID:  11,
		taskID:  41,
		eventID: 42,
		agentTasks: []mysqlstore.AgentTask{{
			ID:              41,
			TenantID:        7,
			UserID:          11,
			ParentSessionID: 99,
			AgentName:       "reviewer",
			Status:          agenttasks.StatusRunning,
		}},
		agentTaskEvents: []mysqlstore.AgentTaskEvent{{
			ID:        42,
			TaskID:    41,
			EventType: agenttasks.EventStarted,
		}},
		sessions: []mysqlstore.Session{{
			ID: 99,
		}},
	}
	svc := NewService(repo, nil)

	taskID, err := svc.CreateAgentTask(testTenantContext(), agenttasks.TaskInput{
		ParentSessionID: 99,
		AgentName:       "reviewer",
		Prompt:          "check code",
		Status:          agenttasks.StatusRunning,
		TraceID:         "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if taskID != 41 || repo.lastSessionID != 99 || repo.lastAgentTaskInput.TenantID != 7 || repo.lastAgentTaskInput.UserID != 11 || repo.lastAgentTaskInput.ParentSessionID != 99 {
		t.Fatalf("taskID=%d task input=%+v", taskID, repo.lastAgentTaskInput)
	}

	eventID, err := svc.AppendAgentTaskEvent(testTenantContext(), agenttasks.EventInput{
		TaskID:      41,
		EventType:   agenttasks.EventStarted,
		PayloadJSON: `{"ok":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if eventID != 42 || repo.lastAgentTaskEventInput.TenantID != 7 || repo.lastAgentTaskEventInput.UserID != 11 || repo.lastAgentTaskEventInput.TaskID != 41 {
		t.Fatalf("eventID=%d event input=%+v", eventID, repo.lastAgentTaskEventInput)
	}

	exactEvent, err := svc.GetAgentTaskEvent(testTenantContext(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if exactEvent.ID != 42 || repo.lastAgentTaskEventID != 42 || repo.lastAgentTaskEventTenantID != 7 || repo.lastAgentTaskEventUserID != 11 {
		t.Fatalf("exact event=%+v repo=%+v", exactEvent, repo)
	}

	tasks, err := svc.ListAgentTasks(testTenantContext(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || repo.lastAgentTaskTenantID != 7 || repo.lastAgentTaskUserID != 11 || repo.lastAgentTaskLimit != 20 {
		t.Fatalf("tasks=%+v repo=%+v", tasks, repo)
	}

	task, err := svc.GetAgentTask(testTenantContext(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != 41 || repo.lastAgentTaskTaskID != 41 || repo.lastAgentTaskTenantID != 7 || repo.lastAgentTaskUserID != 11 {
		t.Fatalf("task=%+v repo=%+v", task, repo)
	}

	if err := svc.UpdateAgentTask(testTenantContext(), 41, agenttasks.TaskUpdate{Status: agenttasks.StatusCompleted, ResultJSON: `{"ok":true}`}); err != nil {
		t.Fatal(err)
	}
	if repo.lastUpdatedAgentTaskID != 41 || repo.lastUpdatedAgentTask.TenantID != 7 || repo.lastUpdatedAgentTask.UserID != 11 || repo.lastUpdatedAgentTask.Status != agenttasks.StatusCompleted {
		t.Fatalf("update repo=%+v", repo)
	}

	events, err := svc.ListAgentTaskEvents(testTenantContext(), 41, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || repo.lastAgentTaskEventTaskID != 41 || repo.lastAgentTaskEventLimit != 30 {
		t.Fatalf("events=%+v repo=%+v", events, repo)
	}
	events, err = svc.ListAgentTaskEventsAfter(testTenantContext(), 41, 42, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || repo.lastAgentTaskEventTaskID != 41 || repo.lastAgentTaskEventLimit != 30 {
		t.Fatalf("events after=%+v repo=%+v", events, repo)
	}

	if err := svc.FinishAgentTask(testTenantContext(), 41, agenttasks.StatusCompleted, `{"result":"done"}`); err != nil {
		t.Fatal(err)
	}
	if repo.lastFinishedAgentTaskID != 41 || repo.lastFinishedAgentTaskStatus != agenttasks.StatusCompleted || repo.lastFinishedAgentTaskResult != `{"result":"done"}` {
		t.Fatalf("finish repo=%+v", repo)
	}

	if err := svc.CancelAgentTask(testTenantContext(), 41, `{"source":"test"}`); err != nil {
		t.Fatal(err)
	}
	if repo.lastCancelledAgentTaskID != 41 || repo.lastCancelledAgentTaskTenantID != 7 || repo.lastCancelledAgentTaskUserID != 11 {
		t.Fatalf("cancel repo=%+v", repo)
	}
	if repo.lastCancelledAgentTaskResult != `{"source":"test"}` {
		t.Fatalf("cancel result = %s", repo.lastCancelledAgentTaskResult)
	}
	if err := svc.CancelAgentTask(testTenantContext(), 41, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repo.lastCancelledAgentTaskResult, `"capability_loop"`) || !strings.Contains(repo.lastCancelledAgentTaskResult, `"source":"tenant_service"`) {
		t.Fatalf("default cancel result = %s", repo.lastCancelledAgentTaskResult)
	}
	cancelled, err := svc.IsAgentTaskCancelled(testTenantContext(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if !cancelled || repo.lastAgentTaskStatusID != 41 {
		t.Fatalf("cancelled=%t repo=%+v", cancelled, repo)
	}
}

func TestServiceManagedSessionStoreQueriesUseResolvedTenantUserScope(t *testing.T) {
	repo := &fakeRepository{
		tenant:          mysqlstore.Tenant{ID: 7, TenantKey: "yutang"},
		userID:          11,
		sessions:        []mysqlstore.Session{{ID: 5, SessionKey: "old-session"}},
		agentTasks:      []mysqlstore.AgentTask{{ID: 41, ParentSessionID: 5, Status: agenttasks.StatusRunning}},
		agentTaskEvents: []mysqlstore.AgentTaskEvent{{ID: 1, TaskID: 41, EventType: "permission_request"}, {ID: 2, TaskID: 41, EventType: "permission_resolved"}},
		cancelIfRunning: true,
	}
	svc := NewService(repo, nil)

	session, err := svc.GetSessionByKey(testTenantContext(), "old-session")
	if err != nil || session.ID != 5 || repo.lastSessionKey != "old-session" || repo.lastSessionTenantID != 7 || repo.lastSessionUserID != 11 {
		t.Fatalf("session=%+v err=%v scope=%d/%d key=%q", session, err, repo.lastSessionTenantID, repo.lastSessionUserID, repo.lastSessionKey)
	}

	tasks, err := svc.ListLatestAgentTasksForSessions(testTenantContext(), []uint64{5})
	if err != nil || len(tasks) != 1 || repo.lastLatestAgentTaskTenantID != 7 || repo.lastLatestAgentTaskUserID != 11 || len(repo.lastLatestAgentTaskSessionIDs) != 1 || repo.lastLatestAgentTaskSessionIDs[0] != 5 {
		t.Fatalf("tasks=%+v err=%v repo=%+v", tasks, err, repo)
	}

	cancelled, err := svc.CancelAgentTaskIfRunning(testTenantContext(), 41, `{"source":"session_control"}`)
	if err != nil || !cancelled || repo.lastCancelIfRunningTenantID != 7 || repo.lastCancelIfRunningUserID != 11 || repo.lastCancelIfRunningTaskID != 41 {
		t.Fatalf("cancelled=%t err=%v repo=%+v", cancelled, err, repo)
	}

	events, err := svc.ListAgentTaskEventsForTasksComplete(testTenantContext(), []uint64{41})
	if err != nil || len(events) != 2 || repo.lastCompleteEventTenantID != 7 || repo.lastCompleteEventUserID != 11 || len(repo.lastCompleteEventTaskIDs) != 1 || repo.lastCompleteEventTaskIDs[0] != 41 {
		t.Fatalf("events=%+v err=%v repo=%+v", events, err, repo)
	}
}

func TestServiceSessionControlPersistenceUsesResolvedScope(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 7, TenantKey: "yutang"}, userID: 11,
		sessionControlSession: mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 51, SessionKey: "managed-1"}, MetadataJSON: `{"session_control_operation":{}}`},
		sessionControlAudit:   mysqlstore.SessionControlAuditResult{Audit: mysqlstore.AuditLog{ID: 91}},
		sessionControlStop:    mysqlstore.SessionControlStopResult{Cancelled: true, EventID: 81},
	}
	svc := NewService(repo, nil)

	created, err := svc.CreateSessionControlSession(testTenantContext(), SessionControlSessionRequest{SessionKey: "managed-1", Title: "Managed", Status: "idle", Model: "model-a", CWD: "/repo", MetadataJSON: `{"session_control_operation":{}}`})
	if err != nil || created.Session.ID != 51 || repo.lastSessionControlCreate.Session.TenantID != 7 || repo.lastSessionControlCreate.Session.UserID != 11 {
		t.Fatalf("create=%#v err=%v input=%#v", created, err, repo.lastSessionControlCreate)
	}
	read, err := svc.GetSessionControlSessionByKey(testTenantContext(), "managed-1")
	if err != nil || read.ID != 51 || repo.lastSessionControlOwner != [2]uint64{7, 11} {
		t.Fatalf("read=%#v err=%v owner=%v", read, err, repo.lastSessionControlOwner)
	}
	listed, err := svc.ListSessionControlSessions(testTenantContext(), 12)
	if err != nil || len(listed) != 1 || listed[0].MetadataJSON != read.MetadataJSON || repo.lastSessionControlOwner != [2]uint64{7, 11} || repo.lastSessionLimit != 12 {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	stopped, err := svc.CancelAgentTaskForSessionControl(testTenantContext(), SessionControlStopRequest{TaskID: 42, ResultJSON: `{"cancelled":true}`, EventPayloadJSON: `{"operation":{}}`, TraceID: "trace-1"})
	if err != nil || !stopped.Cancelled || repo.lastSessionControlStop.TenantID != 7 || repo.lastSessionControlStop.UserID != 11 {
		t.Fatalf("stop=%#v err=%v input=%#v", stopped, err, repo.lastSessionControlStop)
	}
	failed, err := svc.FailSessionControlRun(testTenantContext(), mysqlstore.SessionControlRunFailureInput{TenantID: 99, UserID: 100, TaskID: 43, ResultJSON: `{"failed":true}`, EventPayloadJSON: `{"source":"session_control_prelaunch"}`, TraceID: "trace-2"})
	if err != nil || !failed.Failed || repo.lastSessionControlFailure.TenantID != 7 || repo.lastSessionControlFailure.UserID != 11 || repo.lastSessionControlFailure.TaskID != 43 {
		t.Fatalf("failure=%#v err=%v input=%#v", failed, err, repo.lastSessionControlFailure)
	}
	audit, err := svc.RecordSessionControlAudit(testTenantContext(), SessionControlAuditRequest{Action: "session_control.send", ResourceType: "session", ResourceID: "operation", MetadataJSON: `{"safe":true}`, TraceID: "trace-1"})
	if err != nil || audit.Audit.ID != 91 || repo.lastSessionControlAudit.TenantID != 7 || repo.lastSessionControlAudit.ActorUserID != 11 {
		t.Fatalf("audit=%#v err=%v input=%#v", audit, err, repo.lastSessionControlAudit)
	}
}

func TestServiceSessionLinkForwardingUsesResolvedScope(t *testing.T) {
	repo := &fakeRepository{tenant: mysqlstore.Tenant{ID: 7, TenantKey: "yutang"}, userID: 11, taskID: 73}
	svc := NewService(repo, nil)

	linkID, err := svc.UpsertSessionLink(testTenantContext(), mysqlstore.SessionLinkInput{
		TargetSessionID:  99,
		SourceKind:       "tenant",
		SourceSessionKey: "source-1",
		RelationType:     "handoff",
		MetadataJSON:     `{"reason":"test"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if linkID != 73 || repo.lastSessionLinkInput.TenantID != 7 || repo.lastSessionLinkInput.UserID != 11 || repo.lastSessionLinkInput.CreatedByUserID != 11 {
		t.Fatalf("linkID=%d input=%+v", linkID, repo.lastSessionLinkInput)
	}
	linkID, err = svc.UpsertSessionLink(testTenantContext(), mysqlstore.SessionLinkInput{TargetSessionID: 99, SourceKind: "tenant", SourceSessionKey: "source-1", RelationType: "handoff", CreatedByUserID: 42})
	if err != nil || linkID != 73 || repo.lastSessionLinkInput.CreatedByUserID != 42 {
		t.Fatalf("nonzero created_by forwarding: linkID=%d input=%+v err=%v", linkID, repo.lastSessionLinkInput, err)
	}

	if _, err := svc.GetSessionLink(testTenantContext(), 99, "tenant", "source-1", "handoff"); err != mysqlstore.ErrNotFound {
		t.Fatalf("get err=%v", err)
	}
	if repo.lastSessionLinkTenantID != 7 || repo.lastSessionLinkUserID != 11 || repo.lastSessionLinkTargetID != 99 {
		t.Fatalf("get scope=%+v", repo)
	}
	if _, err := svc.ListSessionLinks(testTenantContext(), 99, 20); err != nil {
		t.Fatal(err)
	}
	if repo.lastSessionLinkLimit != 20 || repo.lastSessionLinkTenantID != 7 || repo.lastSessionLinkUserID != 11 {
		t.Fatalf("list scope=%+v", repo)
	}
}

func TestServiceAgentTaskIdempotencyForwardingUsesResolvedScope(t *testing.T) {
	repo := &fakeRepository{
		tenant: mysqlstore.Tenant{ID: 7, TenantKey: "yutang"},
		userID: 11,
		taskID: 41,
		agentTasks: []mysqlstore.AgentTask{{
			ID:             41,
			TenantID:       7,
			UserID:         11,
			IdempotencyKey: "create-1",
		}},
	}
	svc := NewService(repo, nil)
	taskID, err := svc.CreateAgentTask(testTenantContext(), agenttasks.TaskInput{IdempotencyKey: "create-1"})
	if err != nil || taskID != 41 {
		t.Fatalf("taskID=%d err=%v", taskID, err)
	}
	if repo.lastAgentTaskInput.TenantID != 7 || repo.lastAgentTaskInput.UserID != 11 || repo.lastAgentTaskInput.IdempotencyKey != "create-1" {
		t.Fatalf("input=%+v", repo.lastAgentTaskInput)
	}
	task, err := svc.GetAgentTaskByIdempotencyKey(testTenantContext(), "create-1")
	if err != nil || task.ID != 41 {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	if repo.lastAgentTaskTenantID != 7 || repo.lastAgentTaskUserID != 11 || repo.lastAgentTaskIdempotencyKey != "create-1" {
		t.Fatalf("scope tenant=%d user=%d key=%q", repo.lastAgentTaskTenantID, repo.lastAgentTaskUserID, repo.lastAgentTaskIdempotencyKey)
	}
}

func TestServiceGoalStoreUsesResolvedIDs(t *testing.T) {
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	goalItem := goal.Goal{
		ID:          "goal_test",
		Objective:   "ship feature",
		Status:      goal.StatusActive,
		SessionID:   "session-1",
		CWD:         "/tmp/project",
		TurnBudget:  3,
		TokenBudget: 1000,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	event := goal.Event{ID: "evt_test", GoalID: "goal_test", Type: goal.EventGoalStarted, CreatedAt: now}
	repo := &fakeRepository{
		tenant:     mysqlstore.Tenant{ID: 7, TenantKey: "yutang"},
		userID:     11,
		goals:      []goal.Goal{goalItem},
		goalEvents: []goal.Event{event},
	}
	svc := NewService(repo, nil)

	created, err := svc.Create(testTenantContext(), goal.CreateInput{Objective: "ship feature", CWD: "/tmp/project", TurnBudget: 3, TokenBudget: 1000, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "goal_test" || repo.lastGoalTenantID != 7 || repo.lastGoalUserID != 11 {
		t.Fatalf("created=%+v repo=%+v", created, repo)
	}

	got, err := svc.Get(testTenantContext(), "goal_test")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "goal_test" || repo.lastGoalID != "goal_test" {
		t.Fatalf("got=%+v repo=%+v", got, repo)
	}

	list, err := svc.ListGoals(testTenantContext(), goal.ListFilter{Active: true}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || repo.lastGoalFilter.Active != true || repo.lastGoalLimit != 20 {
		t.Fatalf("list=%+v repo=%+v", list, repo)
	}

	goalItem.Status = goal.StatusComplete
	if err := svc.Update(testTenantContext(), goalItem); err != nil {
		t.Fatal(err)
	}
	if repo.lastUpdatedGoal.ID != "goal_test" || repo.lastGoalTenantID != 7 || repo.lastGoalUserID != 11 {
		t.Fatalf("updated repo=%+v", repo)
	}

	if err := svc.AppendEvent(testTenantContext(), event); err != nil {
		t.Fatal(err)
	}
	if repo.lastGoalEvent.ID != "evt_test" || repo.lastGoalTenantID != 7 || repo.lastGoalUserID != 11 {
		t.Fatalf("event repo=%+v", repo)
	}

	events, err := svc.ListEvents(testTenantContext(), "goal_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || repo.lastGoalEventGoalID != "goal_test" || repo.lastGoalEventLimit != 10 {
		t.Fatalf("events=%+v repo=%+v", events, repo)
	}
}

func TestServiceGoalPlanStoreUsesResolvedIDs(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	exitCode := 0
	plan := goal.GoalPlan{
		GoalID:        "goal_test",
		Version:       1,
		Summary:       "ship plan",
		CurrentStepID: "step_1",
		Steps: []goal.GoalStep{{
			ID:     "step_1",
			Title:  "Implement storage",
			Status: goal.StepStatusActive,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	evidence := goal.GoalEvidence{
		ID:        "ev_test",
		GoalID:    "goal_test",
		Type:      goal.EvidenceTypeTest,
		Summary:   "go test passed",
		Command:   "go test ./internal/goal -count=1",
		ExitCode:  &exitCode,
		Passed:    true,
		Payload:   []byte(`{"package":"internal/goal"}`),
		CreatedAt: now,
	}
	repo := &fakeRepository{
		tenant:       mysqlstore.Tenant{ID: 7, TenantKey: "yutang"},
		userID:       11,
		goalPlan:     plan,
		goalEvidence: []goal.GoalEvidence{evidence},
	}
	svc := NewService(repo, nil)

	if err := svc.SavePlan(testTenantContext(), plan); err != nil {
		t.Fatal(err)
	}
	if repo.lastGoalTenantID != 7 || repo.lastGoalUserID != 11 || repo.lastGoalPlan.GoalID != "goal_test" {
		t.Fatalf("saved plan repo=%+v", repo)
	}

	got, ok, err := svc.GetPlan(testTenantContext(), "goal_test")
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if got.GoalID != "goal_test" || repo.lastGoalPlanGoalID != "goal_test" {
		t.Fatalf("plan=%+v repo=%+v", got, repo)
	}

	if err := svc.AppendEvidence(testTenantContext(), evidence); err != nil {
		t.Fatal(err)
	}
	if repo.lastGoalEvidence.ID != "ev_test" || repo.lastGoalTenantID != 7 || repo.lastGoalUserID != 11 {
		t.Fatalf("evidence repo=%+v", repo)
	}

	evidenceList, err := svc.ListEvidence(testTenantContext(), "goal_test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidenceList) != 1 || repo.lastGoalEvidenceGoalID != "goal_test" || repo.lastGoalEvidenceLimit != 5 {
		t.Fatalf("evidence=%+v repo=%+v", evidenceList, repo)
	}
}

func testTenantContext() context.Context {
	return observability.WithRequestValues(context.Background(), "trace-1", "user-1", "yutang")
}

type fakeRepository struct {
	tenant  mysqlstore.Tenant
	tenants []mysqlstore.Tenant
	userID  uint64
	user    mysqlstore.User
	users   []mysqlstore.User

	memoryID        uint64
	sessionID       uint64
	messageID       uint64
	documentID      uint64
	skillID         uint64
	overrideID      uint64
	profileID       uint64
	skills          []mysqlstore.Skill
	overrides       []mysqlstore.SkillOverride
	effectiveSkills []mysqlstore.EffectiveSkill
	documents       []mysqlstore.Document
	memories        []mysqlstore.Memory
	knowledgeID     uint64
	knowledgeDocs   []mysqlstore.KnowledgeDocument
	knowledgeChunks []mysqlstore.KnowledgeChunk
	profile         mysqlstore.Profile
	sessions        []mysqlstore.Session
	messageRows     []mysqlstore.Message
	auditID         uint64
	auditLogs       []mysqlstore.AuditLog
	telemetryID     uint64
	telemetryEvents []mysqlstore.TelemetryEvent
	quotaConfig     mysqlstore.QuotaConfig
	usageDaily      []mysqlstore.UsageDaily
	usageLedger     []mysqlstore.UsageLedger
	quotaEvents     []mysqlstore.QuotaEvent
	taskID          uint64
	eventID         uint64
	agentTasks      []mysqlstore.AgentTask
	agentTaskEvents []mysqlstore.AgentTaskEvent
	goals           []goal.Goal
	goalEvents      []goal.Event
	goalPlan        goal.GoalPlan
	goalEvidence    []goal.GoalEvidence
	getTenantCalls  int
	ensureUserCalls int

	lastTenantKey            string
	lastTenantInput          mysqlstore.TenantInput
	lastTenantListLimit      int
	lastTenantListOptions    mysqlstore.ListOptions
	lastArchivedTenantKey    string
	lastEnsureTenantID       uint64
	lastEnsureUserKey        string
	lastGetUserID            uint64
	lastArchivedUserKey      string
	lastListUsersTenantID    uint64
	lastListUsersLimit       int
	lastListUsersOptions     mysqlstore.ListOptions
	lastUserInput            mysqlstore.UserInput
	lastMemoryInput          mysqlstore.MemoryInput
	memoryInputs             []mysqlstore.MemoryInput
	lastSkillInput           mysqlstore.SkillInput
	lastSkillRollbackInput   mysqlstore.SkillRollbackInput
	lastSkillKey             string
	lastSkillVersion         uint
	lastSkillEnabledOnly     bool
	lastOverrideInput        mysqlstore.SkillOverrideInput
	lastOverrideLimit        int
	lastOverrideSkillKey     string
	lastOverrideVersion      uint
	lastEffectiveEnabledOnly bool
	lastEffectiveLimit       int
	lastEffectiveSkillKey    string
	lastEffectiveVersion     uint
	lastDocument             mysqlstore.DocumentInput
	lastDocumentType         string
	lastDocumentLimit        int
	lastKnowledgeDocument    mysqlstore.KnowledgeDocumentInput
	lastKnowledgeChunks      []mysqlstore.KnowledgeChunkInput
	lastKnowledgeLimit       int
	lastKnowledgeSearch      mysqlstore.KnowledgeSearchOptions
	lastProfileInput         mysqlstore.ProfileInput
	lastProfileVersion       uint
	lastSessionInput         mysqlstore.SessionInput
	lastSessionTenantID      uint64
	lastSessionUserID        uint64
	lastSessionKey           string
	lastSessionID            uint64
	archivedSessionID        uint64
	lastSessionLimit         int
	lastMessageInput         mysqlstore.MessageInput
	lastQueryTurnInput       mysqlstore.QueryTurnInput
	lastForkSessionInput     mysqlstore.ForkSessionInput
	lastMessageMaxRows       int
	lastMessageSessionID     uint64
	lastMessageLimit         int
	// lastMessageOwner 记下定点查询实际收到的 (tenantID, userID)。
	// 换成定点查询不能把归属校验丢掉，所以要断言解析出来的那对 id 真的传下去了。
	lastMessageOwner                     [2]uint64
	lastMessageID                        uint64
	lastMessageBeforeTurn                uint
	lastMessageRole                      string
	lastMessageKey                       string
	lastAgentTaskInput                   agenttasks.TaskInput
	lastAgentTaskTenantID                uint64
	lastAgentTaskUserID                  uint64
	lastAgentTaskTaskID                  uint64
	lastAgentTaskLimit                   int
	lastAgentTaskIdempotencyKey          string
	lastFinishedAgentTaskID              uint64
	lastFinishedAgentTaskStatus          string
	lastFinishedAgentTaskResult          string
	lastUpdatedAgentTaskID               uint64
	lastUpdatedAgentTask                 agenttasks.TaskUpdate
	lastAgentTaskEventInput              agenttasks.EventInput
	lastHandoffInput                     agenttasks.HandoffLinkAndEventInput
	handoffResult                        agenttasks.HandoffLinkAndEventResult
	lastAgentTaskEventTenantID           uint64
	lastAgentTaskEventUserID             uint64
	lastAgentTaskEventTaskID             uint64
	lastAgentTaskEventID                 uint64
	batchedAgentTaskIDs                  []uint64
	lastAgentTaskEventLimit              int
	lastCancelledAgentTaskID             uint64
	lastCancelledAgentTaskTenantID       uint64
	lastCancelledAgentTaskUserID         uint64
	lastCancelledAgentTaskResult         string
	cancelIfRunning                      bool
	lastCancelIfRunningTenantID          uint64
	lastCancelIfRunningUserID            uint64
	lastCancelIfRunningTaskID            uint64
	lastCancelIfRunningResult            string
	lastLatestAgentTaskTenantID          uint64
	lastLatestAgentTaskUserID            uint64
	lastLatestAgentTaskSessionIDs        []uint64
	lastCompleteEventTenantID            uint64
	lastCompleteEventUserID              uint64
	lastCompleteEventTaskIDs             []uint64
	lastAgentTaskStatusID                uint64
	lastSessionLinkInput                 mysqlstore.SessionLinkInput
	lastSessionLinkTenantID              uint64
	lastSessionLinkUserID                uint64
	lastSessionLinkTargetID              uint64
	lastSessionLinkLimit                 int
	lastSessionMonitorConfigurationInput mysqlstore.SessionMonitorConfigurationInput
	lastGoalTenantID                     uint64
	lastGoalUserID                       uint64
	lastGoalID                           string
	lastGoalInput                        goal.CreateInput
	lastGoalFilter                       goal.ListFilter
	lastGoalLimit                        int
	lastUpdatedGoal                      goal.Goal
	lastGoalEvent                        goal.Event
	lastGoalEventGoalID                  string
	lastGoalEventLimit                   int
	lastGoalPlan                         goal.GoalPlan
	lastGoalPlanGoalID                   string
	lastGoalEvidence                     goal.GoalEvidence
	lastGoalEvidenceGoalID               string
	lastGoalEvidenceLimit                int
	lastAuditInput                       mysqlstore.AuditLogInput
	lastAuditLimit                       int
	lastAuditOptions                     mysqlstore.ListOptions
	lastTelemetryInput                   mysqlstore.TelemetryEventInput
	lastTelemetryLimit                   int
	lastTelemetryOptions                 mysqlstore.ListOptions
	lastQuotaConfigInput                 mysqlstore.QuotaConfigInput
	lastUsageLedgerInput                 mysqlstore.UsageLedgerInput
	lastUsageLedgerSettlement            mysqlstore.UsageLedgerInput
	lastUsageDailyDelta                  mysqlstore.UsageDailyDelta
	lastUsageDailyOptions                mysqlstore.ListOptions
	lastUsageLedgerOptions               mysqlstore.ListOptions
	lastQuotaEvent                       mysqlstore.QuotaEvent
	lastQuotaEventOptions                mysqlstore.ListOptions
	sessionControlSession                mysqlstore.SessionControlSession
	sessionControlAudit                  mysqlstore.SessionControlAuditResult
	sessionControlStop                   mysqlstore.SessionControlStopResult
	lastSessionControlCreate             mysqlstore.SessionControlCreateInput
	lastSessionControlOwner              [2]uint64
	lastSessionControlStop               mysqlstore.SessionControlStopInput
	lastSessionControlFailure            mysqlstore.SessionControlRunFailureInput
	lastSessionControlAudit              mysqlstore.AuditLogInput
}

func (f *fakeRepository) GetTenantByKey(ctx context.Context, tenantKey string) (mysqlstore.Tenant, error) {
	f.getTenantCalls++
	f.lastTenantKey = tenantKey
	if f.tenant.ID == 0 {
		return mysqlstore.Tenant{}, mysqlstore.ErrNotFound
	}
	return f.tenant, nil
}

func (f *fakeRepository) UpsertTenant(ctx context.Context, input mysqlstore.TenantInput) (uint64, error) {
	f.lastTenantInput = input
	for _, tenant := range f.tenants {
		if tenant.TenantKey == input.TenantKey {
			return tenant.ID, nil
		}
	}
	return 5, nil
}

func (f *fakeRepository) ArchiveTenant(ctx context.Context, tenantKey string) error {
	f.lastArchivedTenantKey = tenantKey
	return nil
}

func (f *fakeRepository) ListTenants(ctx context.Context, limit int) ([]mysqlstore.Tenant, error) {
	f.lastTenantListLimit = limit
	return f.tenants, nil
}

func (f *fakeRepository) ListTenantsFiltered(ctx context.Context, opts mysqlstore.ListOptions) ([]mysqlstore.Tenant, error) {
	f.lastTenantListOptions = opts
	return f.tenants, nil
}

func (f *fakeRepository) UpsertUser(ctx context.Context, input mysqlstore.UserInput) (uint64, error) {
	f.lastUserInput = input
	return f.userID, nil
}

func (f *fakeRepository) EnsureUser(ctx context.Context, tenantID uint64, userKey string) (uint64, error) {
	f.ensureUserCalls++
	f.lastEnsureTenantID = tenantID
	f.lastEnsureUserKey = userKey
	return f.userID, nil
}

func (f *fakeRepository) ArchiveUser(ctx context.Context, tenantID uint64, userKey string) error {
	f.lastEnsureTenantID = tenantID
	f.lastArchivedUserKey = userKey
	return nil
}

func (f *fakeRepository) GetUser(ctx context.Context, tenantID, userID uint64) (mysqlstore.User, error) {
	f.lastGetUserID = userID
	if f.user.ID == 0 {
		return mysqlstore.User{}, mysqlstore.ErrNotFound
	}
	return f.user, nil
}

func (f *fakeRepository) ListUsers(ctx context.Context, tenantID uint64, limit int) ([]mysqlstore.User, error) {
	f.lastListUsersTenantID = tenantID
	f.lastListUsersLimit = limit
	return f.users, nil
}

func (f *fakeRepository) ListUsersFiltered(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.User, error) {
	f.lastListUsersTenantID = tenantID
	f.lastListUsersOptions = opts
	return f.users, nil
}

func (f *fakeRepository) UpsertMemory(ctx context.Context, input mysqlstore.MemoryInput) (uint64, error) {
	f.lastMemoryInput = input
	f.memoryInputs = append(f.memoryInputs, input)
	return f.memoryID, nil
}

func (f *fakeRepository) ListMemories(ctx context.Context, tenantID, userID uint64, category string, limit int) ([]mysqlstore.Memory, error) {
	var out []mysqlstore.Memory
	for _, item := range f.memories {
		if category == "" || item.Category == category {
			out = append(out, item)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	return []mysqlstore.Memory{{ID: 1, MemoryKey: "k", Category: category, UpdatedAt: time.Now()}}, nil
}

func (f *fakeRepository) UpsertSkill(ctx context.Context, input mysqlstore.SkillInput) (uint64, error) {
	f.lastSkillInput = input
	return f.skillID, nil
}

func (f *fakeRepository) RollbackSkillVersion(ctx context.Context, input mysqlstore.SkillRollbackInput) (mysqlstore.Skill, error) {
	f.lastSkillRollbackInput = input
	source, err := f.GetSkill(ctx, input.TenantID, input.SkillKey, input.FromVersion)
	if err != nil {
		return mysqlstore.Skill{}, err
	}
	latest, err := f.GetSkill(ctx, input.TenantID, input.SkillKey, 0)
	if err != nil {
		return mysqlstore.Skill{}, err
	}
	nextVersion := latest.Version + 1
	if input.TargetVersion > 0 {
		nextVersion = input.TargetVersion
	}
	source.ID = f.skillID
	source.Version = nextVersion
	return source, nil
}

func (f *fakeRepository) ListSkills(ctx context.Context, tenantID uint64, enabledOnly bool, limit int) ([]mysqlstore.Skill, error) {
	f.lastSkillEnabledOnly = enabledOnly
	return f.skills, nil
}

func (f *fakeRepository) GetSkill(ctx context.Context, tenantID uint64, skillKey string, version uint) (mysqlstore.Skill, error) {
	f.lastSkillKey = skillKey
	f.lastSkillVersion = version
	var latest mysqlstore.Skill
	for _, item := range f.skills {
		if item.SkillKey != skillKey {
			continue
		}
		if version > 0 && item.Version == version {
			return item, nil
		}
		if version == 0 && (latest.ID == 0 || item.Version > latest.Version) {
			latest = item
		}
	}
	if version == 0 && latest.ID != 0 {
		return latest, nil
	}
	if len(f.skills) == 1 && f.skills[0].SkillKey == "" {
		return f.skills[0], nil
	}
	return mysqlstore.Skill{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) UpsertSkillOverride(ctx context.Context, input mysqlstore.SkillOverrideInput) (uint64, error) {
	f.lastOverrideInput = input
	return f.overrideID, nil
}

func (f *fakeRepository) ListSkillOverrides(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.SkillOverride, error) {
	f.lastOverrideLimit = limit
	return f.overrides, nil
}

func (f *fakeRepository) GetSkillOverride(ctx context.Context, tenantID, userID uint64, skillKey string, version uint) (mysqlstore.SkillOverride, error) {
	f.lastOverrideSkillKey = skillKey
	f.lastOverrideVersion = version
	if len(f.overrides) == 0 {
		return mysqlstore.SkillOverride{}, mysqlstore.ErrNotFound
	}
	return f.overrides[0], nil
}

func (f *fakeRepository) ListEffectiveSkills(ctx context.Context, tenantID, userID uint64, enabledOnly bool, limit int) ([]mysqlstore.EffectiveSkill, error) {
	f.lastEffectiveEnabledOnly = enabledOnly
	f.lastEffectiveLimit = limit
	return f.effectiveSkills, nil
}

func (f *fakeRepository) GetEffectiveSkill(ctx context.Context, tenantID, userID uint64, skillKey string, version uint) (mysqlstore.EffectiveSkill, error) {
	f.lastEffectiveSkillKey = skillKey
	f.lastEffectiveVersion = version
	if len(f.effectiveSkills) == 0 {
		return mysqlstore.EffectiveSkill{}, mysqlstore.ErrNotFound
	}
	return f.effectiveSkills[0], nil
}

func (f *fakeRepository) SaveDocument(ctx context.Context, input mysqlstore.DocumentInput) (uint64, error) {
	f.lastDocument = input
	return f.documentID, nil
}

func (f *fakeRepository) GetActiveDocument(ctx context.Context, tenantID, userID uint64, docType string) (mysqlstore.Document, error) {
	f.lastDocumentType = docType
	return mysqlstore.Document{ID: 1, DocType: docType, Version: 1}, nil
}

func (f *fakeRepository) ListDocuments(ctx context.Context, tenantID, userID uint64, docType string, limit int) ([]mysqlstore.Document, error) {
	f.lastDocumentType = docType
	f.lastDocumentLimit = limit
	return f.documents, nil
}

func (f *fakeRepository) SaveKnowledgeDocument(ctx context.Context, input mysqlstore.KnowledgeDocumentInput, chunks []mysqlstore.KnowledgeChunkInput) (uint64, error) {
	f.lastKnowledgeDocument = input
	f.lastKnowledgeChunks = chunks
	return f.knowledgeID, nil
}

func (f *fakeRepository) ListKnowledgeDocuments(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.KnowledgeDocument, error) {
	f.lastKnowledgeLimit = limit
	return f.knowledgeDocs, nil
}

func (f *fakeRepository) SearchKnowledgeChunks(ctx context.Context, tenantID uint64, opts mysqlstore.KnowledgeSearchOptions) ([]mysqlstore.KnowledgeChunk, error) {
	f.lastKnowledgeSearch = opts
	return f.knowledgeChunks, nil
}

func (f *fakeRepository) SaveProfile(ctx context.Context, input mysqlstore.ProfileInput) (uint64, error) {
	f.lastProfileInput = input
	return f.profileID, nil
}

func (f *fakeRepository) GetProfile(ctx context.Context, tenantID, userID uint64, version uint) (mysqlstore.Profile, error) {
	f.lastProfileVersion = version
	if f.profile.ID == 0 {
		return mysqlstore.Profile{}, mysqlstore.ErrNotFound
	}
	return f.profile, nil
}

func (f *fakeRepository) UpsertSession(ctx context.Context, input mysqlstore.SessionInput) (uint64, error) {
	f.lastSessionInput = input
	return f.sessionID, nil
}

func (f *fakeRepository) CreateSessionControlSession(_ context.Context, input mysqlstore.SessionControlCreateInput) (mysqlstore.SessionControlCreateResult, error) {
	f.lastSessionControlCreate = input
	return mysqlstore.SessionControlCreateResult{Session: f.sessionControlSession, Created: true}, nil
}

func (f *fakeRepository) GetSessionControlSessionByKey(_ context.Context, tenantID, userID uint64, _ string) (mysqlstore.SessionControlSession, error) {
	f.lastSessionControlOwner = [2]uint64{tenantID, userID}
	return f.sessionControlSession, nil
}

func (f *fakeRepository) ListSessions(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.Session, error) {
	f.lastSessionLimit = limit
	return f.sessions, nil
}

func (f *fakeRepository) ListSessionControlSessions(_ context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.SessionControlSession, error) {
	f.lastSessionControlOwner = [2]uint64{tenantID, userID}
	f.lastSessionLimit = limit
	return []mysqlstore.SessionControlSession{f.sessionControlSession}, nil
}

func (f *fakeRepository) GetSession(ctx context.Context, tenantID, userID, sessionID uint64) (mysqlstore.Session, error) {
	f.lastSessionID = sessionID
	for _, session := range f.sessions {
		if session.ID == sessionID {
			return session, nil
		}
	}
	return mysqlstore.Session{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) GetSessionByKey(_ context.Context, tenantID, userID uint64, sessionKey string) (mysqlstore.Session, error) {
	f.lastSessionTenantID = tenantID
	f.lastSessionUserID = userID
	f.lastSessionKey = sessionKey
	for _, session := range f.sessions {
		if session.SessionKey == sessionKey {
			return session, nil
		}
	}
	return mysqlstore.Session{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) UpdateSession(ctx context.Context, input mysqlstore.SessionInput) error {
	f.lastSessionInput = input
	f.lastSessionID = input.ID
	return nil
}

func (f *fakeRepository) ArchiveSession(ctx context.Context, tenantID, userID, sessionID uint64) error {
	f.archivedSessionID = sessionID
	return nil
}

func (f *fakeRepository) UpsertMessage(ctx context.Context, input mysqlstore.MessageInput) (uint64, error) {
	f.lastMessageInput = input
	return f.messageID, nil
}

func (f *fakeRepository) SaveQueryTurn(ctx context.Context, input mysqlstore.QueryTurnInput) (mysqlstore.QueryTurnResult, error) {
	f.lastQueryTurnInput = input
	return mysqlstore.QueryTurnResult{
		SessionID:          f.sessionID,
		UserMessageID:      f.messageID,
		AssistantMessageID: f.messageID + 1,
	}, nil
}

func (f *fakeRepository) ListAllMessages(ctx context.Context, tenantID, userID, sessionID uint64, maxRows int) ([]mysqlstore.Message, error) {
	f.lastMessageSessionID = sessionID
	f.lastMessageMaxRows = maxRows
	if len(f.messageRows) > maxRows {
		return nil, mysqlstore.ErrTooManyMessages
	}
	return f.messageRows, nil
}

func (f *fakeRepository) MaxMessageTurn(ctx context.Context, tenantID, userID, sessionID uint64) (uint, error) {
	f.lastMessageSessionID = sessionID
	var maxTurn uint
	for _, message := range f.messageRows {
		if message.TurnIndex > maxTurn {
			maxTurn = message.TurnIndex
		}
	}
	return maxTurn, nil
}

func (f *fakeRepository) GetMessage(ctx context.Context, tenantID, userID, sessionID, messageID uint64) (mysqlstore.Message, error) {
	f.lastMessageOwner = [2]uint64{tenantID, userID}
	f.lastMessageSessionID = sessionID
	f.lastMessageID = messageID
	for _, message := range f.messageRows {
		if message.ID == messageID {
			return message, nil
		}
	}
	return mysqlstore.Message{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) PreviousUserMessage(ctx context.Context, tenantID, userID, sessionID uint64, beforeTurn uint) (mysqlstore.Message, error) {
	f.lastMessageOwner = [2]uint64{tenantID, userID}
	f.lastMessageSessionID = sessionID
	f.lastMessageBeforeTurn = beforeTurn
	var selected mysqlstore.Message
	for _, message := range f.messageRows {
		if message.Role != "user" || message.TurnIndex >= beforeTurn {
			continue
		}
		if selected.ID == 0 || message.TurnIndex > selected.TurnIndex {
			selected = message
		}
	}
	if selected.ID == 0 {
		return mysqlstore.Message{}, mysqlstore.ErrNotFound
	}
	return selected, nil
}

// MessageByKey 复刻真查询：role + 生成列相等，取一条。这里从 ContentJSON 里读 key
// 而不是另存一个字段 —— 生成列就是从 content_json 抽出来的，夹具照着抽才算诚实。
func (f *fakeRepository) MessageByKey(ctx context.Context, tenantID, userID, sessionID uint64, role, messageKey string) (mysqlstore.Message, bool, error) {
	f.lastMessageOwner = [2]uint64{tenantID, userID}
	f.lastMessageSessionID = sessionID
	f.lastMessageRole = role
	f.lastMessageKey = messageKey
	if messageKey == "" {
		return mysqlstore.Message{}, false, mysqlstore.ErrInvalidInput
	}
	for _, message := range f.messageRows {
		if message.Role != role {
			continue
		}
		var raw struct {
			Mobile struct {
				MessageKey string `json:"message_key"`
			} `json:"mobile"`
		}
		if err := json.Unmarshal([]byte(message.ContentJSON), &raw); err != nil {
			continue
		}
		if raw.Mobile.MessageKey == messageKey {
			return message, true, nil
		}
	}
	return mysqlstore.Message{}, false, nil
}

func (f *fakeRepository) ListRecentMessages(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]mysqlstore.Message, error) {
	f.lastMessageOwner = [2]uint64{tenantID, userID}
	f.lastMessageSessionID = sessionID
	f.lastMessageLimit = limit
	if len(f.messageRows) > limit {
		return f.messageRows[len(f.messageRows)-limit:], nil
	}
	return f.messageRows, nil
}

func (f *fakeRepository) ForkSession(ctx context.Context, input mysqlstore.ForkSessionInput) (mysqlstore.ForkSessionResult, error) {
	f.lastForkSessionInput = input
	return mysqlstore.ForkSessionResult{
		SessionID:      f.sessionID,
		CopiedMessages: len(input.Messages),
	}, nil
}

func (f *fakeRepository) ListMessages(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]mysqlstore.Message, error) {
	f.lastMessageSessionID = sessionID
	f.lastMessageLimit = limit
	return f.messageRows, nil
}

func (f *fakeRepository) CreateAgentTask(ctx context.Context, input agenttasks.TaskInput) (uint64, error) {
	f.lastAgentTaskInput = input
	return f.taskID, nil
}

func (f *fakeRepository) CreateSessionControlRun(_ context.Context, input mysqlstore.SessionControlRunInput) (mysqlstore.SessionControlRunResult, error) {
	return mysqlstore.SessionControlRunResult{Task: mysqlstore.AgentTask{ID: 1, TenantID: input.Task.TenantID, UserID: input.Task.UserID, ParentSessionID: input.Task.ParentSessionID, Status: agenttasks.StatusReady, MetadataJSON: input.Task.MetadataJSON, IdempotencyKey: input.Task.IdempotencyKey}, EventID: 1, Created: true}, nil
}

func (f *fakeRepository) StartSessionControlRun(_ context.Context, tenantID, userID, taskID uint64) (mysqlstore.SessionControlRunStartResult, error) {
	return mysqlstore.SessionControlRunStartResult{Task: mysqlstore.AgentTask{ID: taskID, TenantID: tenantID, UserID: userID, Status: agenttasks.StatusRunning}, Started: true}, nil
}

func (f *fakeRepository) FailSessionControlRun(_ context.Context, input mysqlstore.SessionControlRunFailureInput) (mysqlstore.SessionControlRunFailureResult, error) {
	f.lastSessionControlFailure = input
	return mysqlstore.SessionControlRunFailureResult{Failed: input.TaskID != 0, EventID: 1}, nil
}

func (f *fakeRepository) GetAgentTaskByIdempotencyKey(ctx context.Context, tenantID, userID uint64, idempotencyKey string) (mysqlstore.AgentTask, error) {
	f.lastAgentTaskTenantID = tenantID
	f.lastAgentTaskUserID = userID
	f.lastAgentTaskIdempotencyKey = idempotencyKey
	for _, task := range f.agentTasks {
		if task.IdempotencyKey == idempotencyKey {
			return task, nil
		}
	}
	return mysqlstore.AgentTask{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) UpsertSessionLink(ctx context.Context, input mysqlstore.SessionLinkInput) (uint64, error) {
	f.lastSessionLinkInput = input
	return f.taskID, nil
}

func (f *fakeRepository) GetSessionLink(ctx context.Context, tenantID, userID, targetSessionID uint64, sourceKind, sourceSessionKey, relationType string) (mysqlstore.SessionLink, error) {
	f.lastSessionLinkTenantID = tenantID
	f.lastSessionLinkUserID = userID
	f.lastSessionLinkTargetID = targetSessionID
	return mysqlstore.SessionLink{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) ResolveSessionMonitorTarget(context.Context, uint64, uint64, string, string) (mysqlstore.SessionMonitorTarget, error) {
	return mysqlstore.SessionMonitorTarget{SessionID: 41}, nil
}

func (f *fakeRepository) GetSessionMonitorLink(context.Context, uint64, uint64, uint64, string, string) (mysqlstore.SessionLink, error) {
	return mysqlstore.SessionLink{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) UpsertSessionMonitorLink(_ context.Context, input mysqlstore.SessionMonitorLinkInput) (mysqlstore.SessionLink, error) {
	return mysqlstore.SessionLink{ID: 71, TenantID: input.TenantID, UserID: input.UserID}, nil
}

func (f *fakeRepository) ApplySessionMonitorConfiguration(_ context.Context, input mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error) {
	f.lastSessionMonitorConfigurationInput = input
	return mysqlstore.SessionMonitorConfigurationResult{Link: mysqlstore.SessionLink{ID: 71, TenantID: input.TenantID, UserID: input.UserID}}, nil
}

func (f *fakeRepository) ListSessionLinks(ctx context.Context, tenantID, userID, targetSessionID uint64, limit int) ([]mysqlstore.SessionLink, error) {
	f.lastSessionLinkTenantID = tenantID
	f.lastSessionLinkUserID = userID
	f.lastSessionLinkTargetID = targetSessionID
	f.lastSessionLinkLimit = limit
	return nil, nil
}

func (f *fakeRepository) FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error {
	f.lastFinishedAgentTaskID = taskID
	f.lastFinishedAgentTaskStatus = status
	f.lastFinishedAgentTaskResult = resultJSON
	return nil
}

func (f *fakeRepository) CancelAgentTask(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) error {
	f.lastCancelledAgentTaskTenantID = tenantID
	f.lastCancelledAgentTaskUserID = userID
	f.lastCancelledAgentTaskID = taskID
	f.lastCancelledAgentTaskResult = resultJSON
	return nil
}

func (f *fakeRepository) CancelAgentTaskIfRunning(_ context.Context, tenantID, userID, taskID uint64, resultJSON string) (bool, error) {
	f.lastCancelIfRunningTenantID = tenantID
	f.lastCancelIfRunningUserID = userID
	f.lastCancelIfRunningTaskID = taskID
	f.lastCancelIfRunningResult = resultJSON
	return f.cancelIfRunning, nil
}

func (f *fakeRepository) CancelAgentTaskForSessionControl(_ context.Context, input mysqlstore.SessionControlStopInput) (mysqlstore.SessionControlStopResult, error) {
	f.lastSessionControlStop = input
	return f.sessionControlStop, nil
}

func (f *fakeRepository) RecoverSessionControlStop(context.Context, uint64, uint64, uint64, string) (mysqlstore.SessionControlStopRecovery, error) {
	return mysqlstore.SessionControlStopRecovery{}, nil
}

func (f *fakeRepository) GetAgentTaskStatus(ctx context.Context, tenantID, userID, taskID uint64) (string, error) {
	f.lastAgentTaskStatusID = taskID
	return agenttasks.StatusCancelled, nil
}

func (f *fakeRepository) GetAgentTask(ctx context.Context, tenantID, userID, taskID uint64) (mysqlstore.AgentTask, error) {
	f.lastAgentTaskTenantID = tenantID
	f.lastAgentTaskUserID = userID
	f.lastAgentTaskTaskID = taskID
	for _, task := range f.agentTasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return mysqlstore.AgentTask{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) UpdateAgentTask(ctx context.Context, taskID uint64, input agenttasks.TaskUpdate) error {
	f.lastUpdatedAgentTaskID = taskID
	f.lastUpdatedAgentTask = input
	return nil
}

func (f *fakeRepository) ListAgentTasks(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.AgentTask, error) {
	f.lastAgentTaskTenantID = tenantID
	f.lastAgentTaskUserID = userID
	f.lastAgentTaskLimit = limit
	return f.agentTasks, nil
}

func (f *fakeRepository) ListLatestAgentTasksForSessions(_ context.Context, tenantID, userID uint64, sessionIDs []uint64) ([]mysqlstore.AgentTask, error) {
	f.lastLatestAgentTaskTenantID = tenantID
	f.lastLatestAgentTaskUserID = userID
	f.lastLatestAgentTaskSessionIDs = append([]uint64(nil), sessionIDs...)
	return f.agentTasks, nil
}

func (f *fakeRepository) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	f.lastAgentTaskEventInput = input
	return f.eventID, nil
}

func (f *fakeRepository) GetAgentTaskEvent(_ context.Context, tenantID, userID, eventID uint64) (mysqlstore.AgentTaskEvent, error) {
	f.lastAgentTaskEventTenantID = tenantID
	f.lastAgentTaskEventUserID = userID
	f.lastAgentTaskEventID = eventID
	for _, event := range f.agentTaskEvents {
		if event.ID == eventID {
			return event, nil
		}
	}
	return mysqlstore.AgentTaskEvent{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) CreateHandoffLinkAndEvent(_ context.Context, input agenttasks.HandoffLinkAndEventInput) (agenttasks.HandoffLinkAndEventResult, error) {
	f.lastHandoffInput = input
	return f.handoffResult, nil
}

func (f *fakeRepository) CreateHandoffBatch(_ context.Context, input agenttasks.HandoffBatchInput) (agenttasks.HandoffBatchResult, error) {
	items := make([]agenttasks.HandoffLinkAndEventResult, len(input.Items))
	return agenttasks.HandoffBatchResult{Items: items}, nil
}

func (f *fakeRepository) RecoverHandoffBatch(context.Context, agenttasks.HandoffRecoveryInput) (agenttasks.HandoffRecoveryResult, error) {
	return agenttasks.HandoffRecoveryResult{}, nil
}

func (f *fakeRepository) ListAgentTaskEvents(ctx context.Context, tenantID, userID, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	return f.ListAgentTaskEventsAfter(ctx, tenantID, userID, taskID, 0, limit)
}

func (f *fakeRepository) ListAgentTaskEventsAfter(ctx context.Context, tenantID, userID, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	f.lastAgentTaskEventTenantID = tenantID
	f.lastAgentTaskEventUserID = userID
	f.lastAgentTaskEventTaskID = taskID
	f.lastAgentTaskEventLimit = limit
	var events []mysqlstore.AgentTaskEvent
	for _, event := range f.agentTaskEvents {
		if event.TaskID == taskID && event.ID > afterID {
			events = append(events, event)
			if limit > 0 && len(events) >= limit {
				break
			}
		}
	}
	return events, nil
}

func (f *fakeRepository) ListAgentTaskEventsForTasks(_ context.Context, tenantID, userID uint64, taskIDs []uint64, limitPerTask int) ([]mysqlstore.AgentTaskEvent, error) {
	f.lastAgentTaskEventTenantID = tenantID
	f.lastAgentTaskEventUserID = userID
	f.batchedAgentTaskIDs = append([]uint64(nil), taskIDs...)
	f.lastAgentTaskEventLimit = limitPerTask
	wanted := make(map[uint64]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		wanted[taskID] = struct{}{}
	}
	var events []mysqlstore.AgentTaskEvent
	for _, event := range f.agentTaskEvents {
		if _, ok := wanted[event.TaskID]; ok {
			events = append(events, event)
		}
	}
	return events, nil
}

func (f *fakeRepository) ListAgentTaskEventsForTasksComplete(_ context.Context, tenantID, userID uint64, taskIDs []uint64) ([]mysqlstore.AgentTaskEvent, error) {
	f.lastCompleteEventTenantID = tenantID
	f.lastCompleteEventUserID = userID
	f.lastCompleteEventTaskIDs = append([]uint64(nil), taskIDs...)
	return f.agentTaskEvents, nil
}

func (f *fakeRepository) CreateGoal(ctx context.Context, tenantID, userID uint64, input goal.CreateInput) (goal.Goal, error) {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalInput = input
	if len(f.goals) > 0 {
		return f.goals[0], nil
	}
	return goal.NewGoal(input)
}

func (f *fakeRepository) GetGoal(ctx context.Context, tenantID, userID uint64, goalID string) (goal.Goal, error) {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalID = goalID
	for _, item := range f.goals {
		if item.ID == goalID {
			return item, nil
		}
	}
	return goal.Goal{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) ListGoals(ctx context.Context, tenantID, userID uint64, filter goal.ListFilter, limit int) ([]goal.Goal, error) {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalFilter = filter
	f.lastGoalLimit = limit
	return f.goals, nil
}

func (f *fakeRepository) UpdateGoal(ctx context.Context, tenantID, userID uint64, item goal.Goal) error {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastUpdatedGoal = item
	return nil
}

func (f *fakeRepository) AppendGoalEvent(ctx context.Context, tenantID, userID uint64, event goal.Event) error {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalEvent = event
	return nil
}

func (f *fakeRepository) ListGoalEvents(ctx context.Context, tenantID, userID uint64, goalID string, limit int) ([]goal.Event, error) {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalEventGoalID = goalID
	f.lastGoalEventLimit = limit
	return f.goalEvents, nil
}

func (f *fakeRepository) SaveGoalPlan(ctx context.Context, tenantID, userID uint64, plan goal.GoalPlan) error {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalPlan = plan
	return nil
}

func (f *fakeRepository) GetGoalPlan(ctx context.Context, tenantID, userID uint64, goalID string) (goal.GoalPlan, bool, error) {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalPlanGoalID = goalID
	if f.goalPlan.GoalID == "" {
		return goal.GoalPlan{}, false, nil
	}
	return f.goalPlan, true, nil
}

func (f *fakeRepository) AppendGoalEvidence(ctx context.Context, tenantID, userID uint64, evidence goal.GoalEvidence) error {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalEvidence = evidence
	return nil
}

func (f *fakeRepository) ListGoalEvidence(ctx context.Context, tenantID, userID uint64, goalID string, limit int) ([]goal.GoalEvidence, error) {
	f.lastGoalTenantID = tenantID
	f.lastGoalUserID = userID
	f.lastGoalEvidenceGoalID = goalID
	f.lastGoalEvidenceLimit = limit
	return f.goalEvidence, nil
}

func (f *fakeRepository) InsertAuditLog(ctx context.Context, input mysqlstore.AuditLogInput) (uint64, error) {
	f.lastAuditInput = input
	return f.auditID, nil
}

func (f *fakeRepository) InsertSessionControlAudit(_ context.Context, input mysqlstore.SessionControlAuditInput) (mysqlstore.SessionControlAuditResult, error) {
	f.lastSessionControlAudit = input.AuditLogInput
	return f.sessionControlAudit, nil
}

func (f *fakeRepository) GetSessionControlAuditByKeyHash(context.Context, uint64, uint64, string, string) (mysqlstore.AuditLog, error) {
	return mysqlstore.AuditLog{}, mysqlstore.ErrNotFound
}

func (f *fakeRepository) ListAuditLogs(ctx context.Context, tenantID uint64, limit int) ([]mysqlstore.AuditLog, error) {
	f.lastAuditLimit = limit
	return f.auditLogs, nil
}

func (f *fakeRepository) ListAuditLogsFiltered(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.AuditLog, error) {
	f.lastAuditOptions = opts
	return f.auditLogs, nil
}

func (f *fakeRepository) InsertTelemetryEvent(ctx context.Context, input mysqlstore.TelemetryEventInput) (uint64, error) {
	f.lastTelemetryInput = input
	return f.telemetryID, nil
}

func (f *fakeRepository) ListTelemetryEvents(ctx context.Context, tenantID uint64, limit int) ([]mysqlstore.TelemetryEvent, error) {
	f.lastTelemetryLimit = limit
	return f.telemetryEvents, nil
}

func (f *fakeRepository) ListTelemetryEventsFiltered(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.TelemetryEvent, error) {
	f.lastTelemetryOptions = opts
	return f.telemetryEvents, nil
}

func (f *fakeRepository) GetTenantQuotaConfig(ctx context.Context, tenantID uint64) (mysqlstore.QuotaConfig, error) {
	if f.quotaConfig.TenantID == 0 {
		return mysqlstore.QuotaConfig{TenantID: tenantID, Timezone: "UTC", ReserveOutputTokens: 4096, Status: "active"}, nil
	}
	return f.quotaConfig, nil
}

func (f *fakeRepository) UpsertTenantQuotaConfig(ctx context.Context, input mysqlstore.QuotaConfigInput) error {
	f.lastQuotaConfigInput = input
	f.quotaConfig = mysqlstore.QuotaConfig{
		TenantID:              input.TenantID,
		QuotaEnabled:          input.QuotaEnabled,
		QPSLimit:              input.QPSLimit,
		DailyTokenLimit:       input.DailyTokenLimit,
		DailyMessageLimit:     input.DailyMessageLimit,
		MaxConcurrentRequests: input.MaxConcurrentRequests,
		Timezone:              input.Timezone,
		ReserveOutputTokens:   input.ReserveOutputTokens,
		Status:                input.Status,
		UpdatedByUserID:       input.UpdatedByUserID,
	}
	if f.quotaConfig.Timezone == "" {
		f.quotaConfig.Timezone = "UTC"
	}
	if f.quotaConfig.ReserveOutputTokens == 0 {
		f.quotaConfig.ReserveOutputTokens = 4096
	}
	if f.quotaConfig.Status == "" {
		f.quotaConfig.Status = "active"
	}
	return nil
}

func (f *fakeRepository) InsertUsageLedger(ctx context.Context, input mysqlstore.UsageLedgerInput) (uint64, error) {
	f.lastUsageLedgerInput = input
	return 1, nil
}

func (f *fakeRepository) UpdateUsageLedgerSettlement(ctx context.Context, requestID string, input mysqlstore.UsageLedgerInput) error {
	input.RequestID = requestID
	f.lastUsageLedgerSettlement = input
	return nil
}

func (f *fakeRepository) UpsertUsageDailyDelta(ctx context.Context, delta mysqlstore.UsageDailyDelta) error {
	f.lastUsageDailyDelta = delta
	return nil
}

func (f *fakeRepository) ListTenantUsageDaily(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.UsageDaily, error) {
	f.lastUsageDailyOptions = opts
	return f.usageDaily, nil
}

func (f *fakeRepository) ListTenantUsageLedger(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.UsageLedger, error) {
	f.lastUsageLedgerOptions = opts
	return f.usageLedger, nil
}

func (f *fakeRepository) InsertQuotaEvent(ctx context.Context, event mysqlstore.QuotaEvent) (uint64, error) {
	f.lastQuotaEvent = event
	return 1, nil
}

func (f *fakeRepository) ListQuotaEvents(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.QuotaEvent, error) {
	f.lastQuotaEventOptions = opts
	return f.quotaEvents, nil
}

func tenantTelemetryEventsContain(events []telemetry.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}
