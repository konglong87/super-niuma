package mysql

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/quota"
)

func TestSQLiteDesktopRepositorySupportsSessionControlPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.sqlite")
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "webui-local", Name: "Local Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "webui-local-user")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetUserRole(ctx, tenantID, userID, DesktopLocalUserRole); err != nil {
		t.Fatal(err)
	}
	user, err := repo.GetUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != DesktopLocalUserRole {
		t.Fatalf("desktop user role = %q, want %q", user.Role, DesktopLocalUserRole)
	}
	if _, err := repo.GetTenantQuotaConfig(ctx, tenantID); err != nil {
		t.Fatalf("get default quota config: %v", err)
	}
	if _, err := repo.ListTenantUsageDaily(ctx, tenantID, ListOptions{Limit: 10}); err != nil {
		t.Fatalf("list usage daily: %v", err)
	}
	if _, err := repo.ListTenantUsageLedger(ctx, tenantID, ListOptions{Limit: 10}); err != nil {
		t.Fatalf("list usage ledger: %v", err)
	}
	if _, err := repo.ListQuotaEvents(ctx, tenantID, ListOptions{Limit: 10}); err != nil {
		t.Fatalf("list quota events: %v", err)
	}
	sessionID, err := repo.UpsertSession(ctx, SessionInput{
		TenantID: tenantID, UserID: userID, SessionKey: "session-1",
		Title: "SQLite test", Status: "idle", MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertMessage(ctx, MessageInput{
		TenantID: tenantID, UserID: userID, SessionID: sessionID,
		TurnIndex: 1, Role: "user", Content: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	channelEvents, err := repo.ListSessionConversationEvents(ctx, tenantID, userID, sessionID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(channelEvents) != 2 || channelEvents[0].EventType != agenttasks.EventMessage || channelEvents[1].EventType != agenttasks.EventCompleted {
		t.Fatalf("projected channel events = %+v", channelEvents)
	}
	taskID, err := repo.CreateAgentTask(ctx, agenttasks.TaskInput{
		TenantID: tenantID, UserID: userID, ParentSessionID: sessionID,
		AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusReady,
		IdempotencyKey: "request-1", MetadataJSON: `{"model":"test"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := repo.ListWebAgentConversationTasks(ctx, tenantID, userID, sessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID || tasks[0].StartedAt.IsZero() {
		t.Fatalf("new task = %+v", tasks)
	}
	_, err = repo.CreateAgentTask(ctx, agenttasks.TaskInput{
		TenantID: tenantID, UserID: userID, AgentName: agenttasks.AgentNameWeb,
		Status: agenttasks.StatusCompleted, MetadataJSON: `{"web_agent_session_id":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateAgentTask(ctx, agenttasks.TaskInput{
		TenantID: tenantID, UserID: userID, AgentName: agenttasks.AgentNameWeb,
		Status: agenttasks.StatusCompleted, MetadataJSON: `{"web_agent_session_id":"1"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyTasks, err := repo.ListWebAgentConversationTasks(ctx, tenantID, userID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyTasks) != 3 {
		t.Fatalf("legacy tasks = %+v", legacyTasks)
	}
	if _, err := repo.AppendAgentTaskEvent(ctx, agenttasks.EventInput{
		TenantID: tenantID, UserID: userID, TaskID: taskID,
		EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"hello"}`,
	}); err != nil {
		t.Fatal(err)
	}
	sessions, err := repo.ListSessionControlSessions(ctx, tenantID, userID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != sessionID {
		t.Fatalf("sessions = %+v", sessions)
	}

	// Reopening the same database must be safe and preserve the rows.
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	messages, err := reopened.ListMessages(ctx, tenantID, userID, sessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "hello" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestSQLiteDesktopRepositorySupportsImagePersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "desktop.sqlite")
	repo, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "webui-local", Name: "Local Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "webui-local-user")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := repo.UpsertSession(ctx, SessionInput{
		TenantID: tenantID, UserID: userID, SessionKey: "image-session",
		Title: "Image test", Status: "idle", MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, model := range []any{&gormMediaAsset{}, &gormImageGeneration{}, &gormImageGenerationAttempt{}, &gormImageCompletionOutbox{}} {
		if !repo.db.Migrator().HasTable(model) {
			t.Fatalf("sqlite image table for %T is missing", model)
		}
	}

	if err := repo.UpsertMediaAsset(ctx, MediaAssetInput{
		AssetID: "asset-1", TenantID: tenantID, UserID: userID, SessionID: sessionID,
		Kind: "image", MediaType: "image/png", Name: "result.png", SizeBytes: 12,
		SHA256: "abc", State: string(media.StateReady),
		OriginalJSON: `{"path":"tenant/asset-1/original.png"}`,
		AccessJSON:   `{"tenant_id":1,"user_id":1}`,
	}); err != nil {
		t.Fatal(err)
	}
	asset, err := repo.GetMediaAsset(ctx, tenantID, userID, sessionID, "asset-1")
	if err != nil {
		t.Fatal(err)
	}
	if asset.AssetID != "asset-1" || asset.State != string(media.StateReady) || asset.OriginalJSON == "" {
		t.Fatalf("media asset = %+v", asset)
	}

	created, err := repo.CreateImageGeneration(ctx, ImageGenerationInput{
		GenerationID: "gen-1", TenantID: tenantID, UserID: userID, SessionID: sessionID,
		AssetID: "asset-1", Operation: "generate", Status: "completed",
		Prompt: "a small blue house", Provider: "jiuan", Model: "gpt-image-2",
		IdempotencyKey: "desktop-image-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.GenerationID != "gen-1" {
		t.Fatalf("created image generation = %+v", created)
	}
	loaded, err := repo.GetImageGeneration(ctx, tenantID, userID, sessionID, "gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AssetID != "asset-1" || loaded.Model != "gpt-image-2" || loaded.Status != "completed" {
		t.Fatalf("loaded image generation = %+v", loaded)
	}
	list, err := repo.ListImageGenerations(ctx, tenantID, userID, sessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].GenerationID != "gen-1" {
		t.Fatalf("image generations = %+v", list)
	}

	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedAsset, err := reopened.GetMediaAsset(ctx, tenantID, userID, sessionID, "asset-1")
	if err != nil {
		t.Fatal(err)
	}
	reopenedGeneration, err := reopened.GetImageGeneration(ctx, tenantID, userID, sessionID, "gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if reopenedAsset.AssetID != asset.AssetID || reopenedGeneration.GenerationID != created.GenerationID {
		t.Fatalf("reopened image persistence asset=%+v generation=%+v", reopenedAsset, reopenedGeneration)
	}
}

func TestSQLiteDesktopRepositorySupportsProfilesAndSkills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.sqlite")
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "desktop", Name: "Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "desktop-user")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := repo.CreateAgentProfile(ctx, AgentProfileInput{
		TenantID: tenantID, OwnerKey: "desktop-user", ProfileKey: "default",
		Scope: "user_private", DisplayName: "Default", ProfileVersion: 1,
		ConfigJSON:      `{ "prompt": { "mode": "chat" } }`,
		CreatedByUserID: userID, UpdatedByUserID: userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID == 0 {
		t.Fatal("profile id was not assigned")
	}
	if _, err := repo.UpsertAgentProfileAssignment(ctx, AgentProfileAssignmentInput{
		TenantID: tenantID, UserID: userID, Surface: "web_chat",
		ProfileID: profile.ID, AssignedByUserID: userID,
	}); err != nil {
		t.Fatal(err)
	}
	assignment, err := repo.GetAgentProfileAssignment(ctx, tenantID, userID, "web_chat")
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ProfileID != profile.ID {
		t.Fatalf("assignment profile id = %d, want %d", assignment.ProfileID, profile.ID)
	}
	if _, err := repo.UpsertSkill(ctx, SkillInput{
		TenantID: tenantID, SkillKey: "welcome", Name: "Welcome",
		ContentMD: "Say hello.", Version: 1, Enabled: true,
		CreatedByUserID: userID,
	}); err != nil {
		t.Fatal(err)
	}
	skills, err := repo.ListEffectiveSkills(ctx, tenantID, userID, true, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].SkillKey != "welcome" {
		t.Fatalf("effective skills = %+v", skills)
	}
}

func TestSQLiteDesktopRepositorySupportsPersistenceUpsertsAndKnowledgeSearch(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "persistence.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "desktop", Name: "Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "desktop-user")
	if err != nil {
		t.Fatal(err)
	}

	memoryID, err := repo.UpsertMemory(ctx, MemoryInput{
		TenantID: tenantID, UserID: userID, MemoryKey: "preference",
		Category: "general", Content: "first", Importance: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedMemoryID, err := repo.UpsertMemory(ctx, MemoryInput{
		TenantID: tenantID, UserID: userID, MemoryKey: "preference",
		Category: "general", Content: "second", Importance: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updatedMemoryID != memoryID {
		t.Fatalf("memory id changed across upsert: first=%d second=%d", memoryID, updatedMemoryID)
	}
	memories, err := repo.ListMemories(ctx, tenantID, userID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 || memories[0].Content != "second" {
		t.Fatalf("memories = %+v", memories)
	}

	skillID, err := repo.UpsertSkill(ctx, SkillInput{
		TenantID: tenantID, SkillKey: "sqlite-test", Name: "SQLite test",
		ContentMD: "test", Version: 1, Enabled: true, CreatedByUserID: userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	overrideID, err := repo.UpsertSkillOverride(ctx, SkillOverrideInput{
		TenantID: tenantID, UserID: userID, SkillID: skillID,
		Enabled: true, ConfigJSON: `{"mode":"first"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedOverrideID, err := repo.UpsertSkillOverride(ctx, SkillOverrideInput{
		TenantID: tenantID, UserID: userID, SkillID: skillID,
		Enabled: false, ConfigJSON: `{"mode":"second"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updatedOverrideID != overrideID {
		t.Fatalf("skill override id changed across upsert: first=%d second=%d", overrideID, updatedOverrideID)
	}
	overrides, err := repo.ListSkillOverrides(ctx, tenantID, userID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 1 || overrides[0].Enabled || overrides[0].ConfigJSON != `{"mode":"second"}` {
		t.Fatalf("skill overrides = %+v", overrides)
	}

	sessionID, err := repo.UpsertSession(ctx, SessionInput{
		TenantID: tenantID, UserID: userID, SessionKey: "target",
		Title: "Target", Status: "idle", MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	linkID, err := repo.UpsertSessionLink(ctx, SessionLinkInput{
		TenantID: tenantID, UserID: userID, TargetSessionID: sessionID,
		SourceKind: "tenant", SourceSessionKey: "source",
		RelationType: "handoff", Status: "active", MetadataJSON: `{"step":1}`,
		CreatedByUserID: userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedLinkID, err := repo.UpsertSessionLink(ctx, SessionLinkInput{
		TenantID: tenantID, UserID: userID, TargetSessionID: sessionID,
		SourceKind: "tenant", SourceSessionKey: "source",
		RelationType: "handoff", Status: "completed", MetadataJSON: `{"step":2}`,
		CreatedByUserID: userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updatedLinkID != linkID {
		t.Fatalf("session link id changed across upsert: first=%d second=%d", linkID, updatedLinkID)
	}
	link, err := repo.GetSessionLink(ctx, tenantID, userID, sessionID, "tenant", "source", "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if link.Status != "completed" || link.MetadataJSON != `{"step":2}` {
		t.Fatalf("session link = %+v", link)
	}

	qpsLimit := uint64(3)
	if err := repo.UpsertTenantQuotaConfig(ctx, quota.ConfigInput{
		TenantID: tenantID, QuotaEnabled: true, QPSLimit: &qpsLimit,
		Timezone: "UTC", ReserveOutputTokens: 128, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	qpsLimit = 5
	if err := repo.UpsertTenantQuotaConfig(ctx, quota.ConfigInput{
		TenantID: tenantID, QuotaEnabled: true, QPSLimit: &qpsLimit,
		Timezone: "UTC", ReserveOutputTokens: 256, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	config, err := repo.GetTenantQuotaConfig(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if config.QPSLimit == nil || *config.QPSLimit != 5 || config.ReserveOutputTokens != 256 {
		t.Fatalf("quota config = %+v", config)
	}

	usageDate := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	for _, delta := range []quota.UsageDailyDelta{
		{TenantID: tenantID, UsageDate: usageDate, Source: "web", Model: "test", RequestCount: 1, MessageCount: 2, InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
		{TenantID: tenantID, UsageDate: usageDate, Source: "web", Model: "test", RequestCount: 2, MessageCount: 3, InputTokens: 5, OutputTokens: 6, TotalTokens: 11},
	} {
		if err := repo.UpsertUsageDailyDelta(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := repo.ListTenantUsageDaily(ctx, tenantID, ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].RequestCount != 3 || usage[0].MessageCount != 5 || usage[0].TotalTokens != 18 {
		t.Fatalf("usage daily = %+v", usage)
	}

	documentID, err := repo.SaveKnowledgeDocument(ctx, KnowledgeDocumentInput{
		TenantID: tenantID, UserID: userID, Title: "SQLite knowledge",
		SourceType: "manual", Content: "SQLite persistence notes", Status: "active",
	}, []KnowledgeChunkInput{{TenantID: tenantID, ChunkIndex: 0, Content: "SQLite persistence supports knowledge search"}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := repo.SearchKnowledgeChunks(ctx, tenantID, KnowledgeSearchOptions{
		Query: "knowledge search", UserID: userID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].DocumentID != documentID || results[0].SearchMode != "like" {
		t.Fatalf("knowledge results = %+v", results)
	}
}

// The desktop stop button failed on SQLite because the stop idempotency
// recovery query used the MySQL-only JSON_UNQUOTE function. This round trip
// exercises cancel + recovery + audit lookup against a real SQLite database.
func TestSQLiteSessionControlStopRecoveryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.sqlite")
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "webui-local", Name: "Local Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "webui-local-user")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := repo.UpsertSession(ctx, SessionInput{
		TenantID: tenantID, UserID: userID, SessionKey: "stop-session",
		Title: "stop", Status: "running", MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := repo.CreateAgentTask(ctx, agenttasks.TaskInput{
		TenantID: tenantID, UserID: userID, ParentSessionID: sessionID,
		AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusRunning, MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	keyHash := strings.Repeat("ab", 32)
	stopPayload := `{"schema":"niuma.session-control-stop.v1","operation":{"key_hash":"` + keyHash + `"}}`
	cancelled, err := repo.CancelAgentTaskForSessionControl(ctx, SessionControlStopInput{
		TenantID: tenantID, UserID: userID, TaskID: taskID,
		ResultJSON: `{"status":"cancelled"}`, EventPayloadJSON: stopPayload, TraceID: "trace-stop",
	})
	if err != nil || !cancelled.Cancelled {
		t.Fatalf("CancelAgentTaskForSessionControl() = %+v, err = %v", cancelled, err)
	}

	recovered, err := repo.RecoverSessionControlStop(ctx, tenantID, userID, sessionID, keyHash)
	if err != nil {
		t.Fatalf("RecoverSessionControlStop() err = %v", err)
	}
	if !recovered.Found || recovered.TaskID != taskID || recovered.EventID != cancelled.EventID || recovered.EventPayloadJSON != stopPayload {
		t.Fatalf("recovered = %+v, want task %d event %d", recovered, taskID, cancelled.EventID)
	}
	missing, err := repo.RecoverSessionControlStop(ctx, tenantID, userID, sessionID, strings.Repeat("cd", 32))
	if err != nil || missing.Found {
		t.Fatalf("missing recovery = %+v, err = %v", missing, err)
	}

	auditKeyHash := strings.Repeat("ef", 32)
	if _, err := repo.InsertSessionControlAudit(ctx, SessionControlAuditInput{AuditLogInput: AuditLogInput{
		TenantID: tenantID, ActorUserID: userID, Action: "session_control.stop",
		ResourceType: "session", ResourceID: "1",
		MetadataJSON: `{"session_control_operation":{"key_hash":"` + auditKeyHash + `"}}`,
		TraceID:      "trace-audit",
	}}); err != nil {
		t.Fatal(err)
	}
	audit, err := repo.GetSessionControlAuditByKeyHash(ctx, tenantID, userID, "session_control.stop", auditKeyHash)
	if err != nil || audit.Action != "session_control.stop" {
		t.Fatalf("audit = %+v, err = %v", audit, err)
	}
}

// Handoff replay/recovery predicates share the same JSON extraction helper.
// Links and events are staged directly so this test covers the JSON predicate
// dialect without coupling the assertions to the orchestration layer.
func TestSQLiteHandoffRecoveryJSONPredicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.sqlite")
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "webui-local", Name: "Local Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "webui-local-user")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := repo.UpsertSession(ctx, SessionInput{
		TenantID: tenantID, UserID: userID, SessionKey: "handoff-target",
		Title: "handoff", Status: "running", MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := repo.CreateAgentTask(ctx, agenttasks.TaskInput{
		TenantID: tenantID, UserID: userID, ParentSessionID: sessionID,
		AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusRunning, MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	operationIdentity := strings.Repeat("01", 32)
	sources := []string{"tenant:source-a", "tenant:source-b"}
	for index, sourceRef := range sources {
		payload := testSessionHandoffEventForOperation(t, taskID, sourceRef, fmt.Sprintf("task_event:%d", 21+index), operationIdentity)
		if _, err := repo.AppendAgentTaskEvent(ctx, agenttasks.EventInput{
			TenantID: tenantID, UserID: userID, TaskID: taskID,
			EventType: agenttasks.EventSessionHandoff, PayloadJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
		kind, key, ok := strings.Cut(sourceRef, ":")
		if !ok {
			t.Fatalf("source ref %q", sourceRef)
		}
		if err := repo.db.Create(&gormSessionLink{
			TenantID: tenantID, UserID: userID, TargetSessionID: sessionID,
			SourceKind: kind, SourceSessionKey: key, RelationType: agenttasks.SessionHandoffRelationType,
			Status: agenttasks.SessionHandoffLinkStatus, CreatedByUserID: userID,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	recovered, err := repo.RecoverHandoffBatch(ctx, agenttasks.HandoffRecoveryInput{
		TenantID: tenantID, UserID: userID, TargetSessionID: sessionID,
		TargetTaskID: taskID, OperationIdentity: operationIdentity, ExpectedSources: sources,
	})
	if err != nil {
		t.Fatalf("RecoverHandoffBatch() err = %v", err)
	}
	if !recovered.Found || len(recovered.Items) != len(sources) {
		t.Fatalf("recovered = %+v", recovered)
	}
	for index, item := range recovered.Items {
		if item.SourceRef != sources[index] || item.LinkID == 0 || item.EventID == 0 || item.PackageSHA256 == "" {
			t.Fatalf("recovered item %d = %+v", index, item)
		}
	}
}
