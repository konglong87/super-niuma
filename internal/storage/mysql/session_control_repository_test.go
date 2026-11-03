package mysql

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"gorm.io/gorm"
)

func TestResolveSessionMonitorTargetRequiresOwnedSessionAndActiveFeishuBinding(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM tenant_sessions AS sessions JOIN channel_conversations AS conversations.*JOIN channel_accounts AS accounts.*sessions.tenant_id = \\?.*sessions.user_id = \\?.*sessions.session_key = \\?.*conversations.status = \\?.*accounts.provider = \\?.*accounts.enabled = \\?.*accounts.status = \\?.*LIMIT \\?").
		WithArgs(uint64(7), uint64(11), "target", ChannelConversationStatusActive, ChannelProviderFeishu, true, ChannelAccountStatusReady, 2).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "session_key", "account_id", "account_key", "conversation_id", "external_chat_id", "external_thread_id"}).
			AddRow(41, "target", 9, "primary", 17, "chat-1", "thread-1"))

	target, err := repo.ResolveSessionMonitorTarget(testContext(), 7, 11, "target", ChannelProviderFeishu)
	if err != nil || target.SessionID != 41 || target.ConversationID != 17 || target.AccountID != 9 {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	assertExpectations(t, mock)
}

func TestCommitSessionMonitorObservationCASAndOutboxAreOneTransaction(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	oldMetadata := `{"version":1,"schedule_id":"sched-1","last_observed_cursor":"old"}`
	newMetadata := `{"version":1,"schedule_id":"sched-1","last_observed_cursor":"new"}`
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tenant_session_links` WHERE tenant_id = \\? AND user_id = \\? AND id = \\?.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(71), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "status", "metadata_json", "created_by_user_id", "created_at", "updated_at"}).
			AddRow(71, 7, 11, 41, "tenant", "source", nil, "observed", "active", oldMetadata, 11, now, now))
	mock.ExpectExec("UPDATE `tenant_session_links` SET .*metadata_json.* WHERE tenant_id = \\? AND user_id = \\? AND id = \\?").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `channel_messages`").WillReturnResult(sqlmock.NewResult(81, 1))
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnResult(sqlmock.NewResult(91, 1))
	mock.ExpectCommit()

	result, err := repo.CommitSessionMonitorObservation(testContext(), SessionMonitorObservationInput{
		TenantID: 7, UserID: 11, LinkID: 71, ExpectedMetadataJSON: oldMetadata, MetadataJSON: newMetadata,
		AccountID: 9, ConversationID: 17, IdempotencyKey: "monitor:71:new", PayloadJSON: `{"kind":"text","text":"bounded"}`, Deliver: true,
	})
	if err != nil || !result.Committed || result.MessageID != 81 || result.OutboxID != 91 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestApplySessionMonitorConfigurationLocksAndPreservesObservation(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	oldKeyHash := strings.Repeat("a", 64)
	oldFingerprint := strings.Repeat("b", 64)
	newKeyHash := strings.Repeat("c", 64)
	newFingerprint := strings.Repeat("d", 64)
	storedMetadata := `{"configuration_fingerprint":"` + oldKeyHash + `.` + oldFingerprint + `","schedule_id":"session-monitor-71","last_observed_cursor":"fresh-cursor","last_observed_status":"tenant:source=running"}`
	inputMetadata := `{"configuration_fingerprint":"` + newKeyHash + `.` + newFingerprint + `"}`
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id` FROM `tenant_sessions` WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND archived_at IS NULL.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(41), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_session_links` WHERE tenant_id = \\? AND user_id = \\? AND target_session_id = \\? AND source_kind = \\? AND source_session_key = \\? AND relation_type = \\?.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(41), "tenant", "source", SessionMonitorRelationObserved, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "status", "metadata_json", "created_by_user_id", "created_at", "updated_at"}).
			AddRow(71, 7, 11, 41, "tenant", "source", nil, SessionMonitorRelationObserved, "active", storedMetadata, 11, now, now))
	mock.ExpectExec("UPDATE `tenant_session_links` SET .* WHERE id = \\?").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(71)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.ApplySessionMonitorConfiguration(testContext(), SessionMonitorConfigurationInput{
		SessionMonitorLinkInput: SessionMonitorLinkInput{TenantID: 7, UserID: 11, TargetSessionID: 41, SourceKind: "tenant", SourceSessionKey: "source", RelationType: SessionMonitorRelationObserved, MetadataJSON: inputMetadata, CreatedByUserID: 11},
		ConfigurationKeyHash:    newKeyHash, ConfigurationFingerprint: newFingerprint,
	})
	if err != nil || result.Conflict || result.Replayed || result.Link.ID != 71 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(result.Link.MetadataJSON), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["schedule_id"] != "session-monitor-71" || metadata["last_observed_cursor"] != "fresh-cursor" || metadata["last_observed_status"] != "tenant:source=running" {
		t.Fatalf("metadata=%#v", metadata)
	}
	assertExpectations(t, mock)
}

func TestApplySessionMonitorConfigurationLocksTargetBeforeCreatingAbsentLink(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	keyHash := strings.Repeat("a", 64)
	fingerprint := strings.Repeat("b", 64)
	metadata := `{"configuration_fingerprint":"` + keyHash + `.` + fingerprint + `"}`

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id` FROM `tenant_sessions` WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND archived_at IS NULL.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(41), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_session_links` WHERE tenant_id = \\? AND user_id = \\? AND target_session_id = \\? AND source_kind = \\? AND source_session_key = \\? AND relation_type = \\?.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(41), "tenant", "source", SessionMonitorRelationObserved, 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `tenant_session_links`").WillReturnResult(sqlmock.NewResult(71, 1))
	mock.ExpectExec("UPDATE `tenant_session_links` SET .*metadata_json.* WHERE id = \\?").
		WithArgs(sqlmock.AnyArg(), uint64(71)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.ApplySessionMonitorConfiguration(testContext(), SessionMonitorConfigurationInput{
		SessionMonitorLinkInput: SessionMonitorLinkInput{TenantID: 7, UserID: 11, TargetSessionID: 41, SourceKind: "tenant", SourceSessionKey: "source", RelationType: SessionMonitorRelationObserved, MetadataJSON: metadata, CreatedByUserID: 11},
		ConfigurationKeyHash:    keyHash, ConfigurationFingerprint: fingerprint,
	})
	if err != nil || result.Link.ID != 71 || result.Link.MetadataJSON == metadata || !strings.Contains(result.Link.MetadataJSON, `"schedule_id":"session-monitor-71"`) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestApplySessionMonitorConfigurationReturnsConflictWithoutOverwrite(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	keyHash := strings.Repeat("a", 64)
	storedFingerprint := strings.Repeat("b", 64)
	requestedFingerprint := strings.Repeat("c", 64)
	storedMetadata := `{"configuration_fingerprint":"` + keyHash + `.` + storedFingerprint + `","schedule_id":"session-monitor-71"}`
	inputMetadata := `{"configuration_fingerprint":"` + keyHash + `.` + requestedFingerprint + `"}`
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id` FROM `tenant_sessions` WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND archived_at IS NULL.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(41), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_session_links` WHERE tenant_id = \\? AND user_id = \\? AND target_session_id = \\? AND source_kind = \\? AND source_session_key = \\? AND relation_type = \\?.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(41), "tenant", "source", SessionMonitorRelationObserved, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "status", "metadata_json", "created_by_user_id", "created_at", "updated_at"}).
			AddRow(71, 7, 11, 41, "tenant", "source", nil, SessionMonitorRelationObserved, "active", storedMetadata, 11, now, now))
	mock.ExpectCommit()

	result, err := repo.ApplySessionMonitorConfiguration(testContext(), SessionMonitorConfigurationInput{
		SessionMonitorLinkInput: SessionMonitorLinkInput{TenantID: 7, UserID: 11, TargetSessionID: 41, SourceKind: "tenant", SourceSessionKey: "source", RelationType: SessionMonitorRelationObserved, MetadataJSON: inputMetadata, CreatedByUserID: 11},
		ConfigurationKeyHash:    keyHash, ConfigurationFingerprint: requestedFingerprint,
	})
	if err != nil || !result.Conflict || result.Link.MetadataJSON != storedMetadata {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestResolveSessionMonitorByScheduleIDRestoresTrustedIdentity(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	metadata := `{"version":1,"schedule_id":"session-monitor-71"}`
	mock.ExpectQuery("SELECT .* FROM tenant_session_links AS links JOIN tenants.*JOIN tenant_users AS users.*JOIN tenant_sessions AS sessions.*JOIN channel_conversations AS conversations.*JOIN channel_accounts AS accounts.*links.id = \\?.*LIMIT \\?").
		WithArgs(ChannelConversationStatusActive, ChannelProviderFeishu, true, ChannelAccountStatusReady, uint64(71), SessionMonitorRelationObserved, "active", 2).
		WillReturnRows(sqlmock.NewRows([]string{"link_id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "link_status", "metadata_json", "created_by_user_id", "link_created_at", "link_updated_at", "tenant_key", "user_key", "target_session_key", "account_id", "account_key", "conversation_id", "external_chat_id", "external_thread_id"}).
			AddRow(71, 7, 11, 41, "tenant", "source", nil, SessionMonitorRelationObserved, "active", metadata, 11, now, now, "tenant-a", "user-a", "target", 9, "primary", 17, "chat-1", "thread-1"))

	record, err := repo.ResolveSessionMonitorByScheduleID(testContext(), "session-monitor-71")
	if err != nil || record.Link.ID != 71 || record.Link.TenantID != 7 || record.Link.UserID != 11 || record.TenantKey != "tenant-a" || record.UserKey != "user-a" || record.TargetSessionKey != "target" || record.ConversationID != 17 {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	assertExpectations(t, mock)
}

func TestResolveSessionMonitorByScheduleIDFailsClosedWhenMetadataMismatches(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	metadata := `{"version":1,"schedule_id":"session-monitor-72"}`
	mock.ExpectQuery("SELECT .* FROM tenant_session_links AS links JOIN tenants.*JOIN tenant_users AS users.*JOIN tenant_sessions AS sessions.*JOIN channel_conversations AS conversations.*JOIN channel_accounts AS accounts.*links.id = \\?.*LIMIT \\?").
		WithArgs(ChannelConversationStatusActive, ChannelProviderFeishu, true, ChannelAccountStatusReady, uint64(71), SessionMonitorRelationObserved, "active", 2).
		WillReturnRows(sqlmock.NewRows([]string{"link_id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "link_status", "metadata_json", "created_by_user_id", "link_created_at", "link_updated_at", "tenant_key", "user_key", "target_session_key", "account_id", "account_key", "conversation_id", "external_chat_id", "external_thread_id"}).
			AddRow(71, 7, 11, 41, "tenant", "source", nil, SessionMonitorRelationObserved, "active", metadata, 11, now, now, "tenant-a", "user-a", "target", 9, "primary", 17, "chat-1", "thread-1"))

	if _, err := repo.ResolveSessionMonitorByScheduleID(testContext(), "session-monitor-71"); err != ErrInvalidState {
		t.Fatalf("ResolveSessionMonitorByScheduleID error = %v", err)
	}
	assertExpectations(t, mock)
}

func TestResolveSessionMonitorByScheduleIDRejectsNonCanonicalIDBeforeSQL(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	for _, scheduleID := range []string{"", "sched-1", "session-monitor-0", "session-monitor-01", "session-monitor-not-a-number", "session-monitor-18446744073709551616"} {
		if _, err := repo.ResolveSessionMonitorByScheduleID(testContext(), scheduleID); err != ErrInvalidInput {
			t.Fatalf("ResolveSessionMonitorByScheduleID(%q) error = %v", scheduleID, err)
		}
	}
	assertExpectations(t, mock)
}

func TestSessionMonitorRepositoryRejectsIncompleteScopeBeforeSQL(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	if _, err := repo.ResolveSessionMonitorTarget(testContext(), 0, 11, "target", ChannelProviderFeishu); err != ErrInvalidInput {
		t.Fatalf("ResolveSessionMonitorTarget error = %v", err)
	}
	if _, err := repo.CommitSessionMonitorObservation(testContext(), SessionMonitorObservationInput{}); err != ErrInvalidInput {
		t.Fatalf("CommitSessionMonitorObservation error = %v", err)
	}
	assertExpectations(t, mock)
}

func TestSessionControlSessionReadIsTenantUserScopedAndIncludesMetadata(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, session_key, title, status, model, cwd, COALESCE(metadata_json, ''), started_at, last_message_at FROM `tenant_sessions` WHERE tenant_id = ? AND user_id = ? AND session_key = ? AND archived_at IS NULL LIMIT ?")).
		WithArgs(uint64(7), uint64(11), "session-a", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "metadata_json", "started_at", "last_message_at"}).
			AddRow(41, "session-a", "Session A", "idle", "model-a", "/repo", `{"session_control_operation":{}}`, now, now))

	item, err := repo.GetSessionControlSessionByKey(testContext(), 7, 11, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != 41 || item.MetadataJSON == "" || item.SessionKey != "session-a" {
		t.Fatalf("session = %#v", item)
	}
	assertExpectations(t, mock)
}

func TestCreateSessionControlSessionDoesNotOverwriteExistingRow(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*metadata_json.* FROM `tenant_sessions` .*tenant_id = \\? AND user_id = \\? AND session_key = \\?.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), "session-a", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "metadata_json", "started_at", "last_message_at"}).
			AddRow(41, "session-a", "Original", "idle", "model-a", "/repo", `{"session_control_operation":{}}`, now, now))
	mock.ExpectCommit()

	result, err := repo.CreateSessionControlSession(testContext(), SessionControlCreateInput{Session: SessionInput{TenantID: 7, UserID: 11, SessionKey: "session-a", Title: "Changed", Status: "idle", MetadataJSON: `{"session_control_operation":{"schema":"v1"}}`}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.Session.Title != "Original" || result.Session.ID != 41 {
		t.Fatalf("result = %#v", result)
	}
	assertExpectations(t, mock)
}

func TestCancelAgentTaskForSessionControlPersistsStatusAndEventAtomically(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*status.* FROM `tenant_agent_tasks` .*tenant_id = \\? AND user_id = \\? AND id = \\?.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(42, agenttasks.StatusRunning))
	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .* WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").
		WillReturnResult(sqlmock.NewResult(81, 1))
	mock.ExpectCommit()

	result, err := repo.CancelAgentTaskForSessionControl(testContext(), SessionControlStopInput{TenantID: 7, UserID: 11, TaskID: 42, ResultJSON: `{"cancelled":true}`, EventPayloadJSON: `{"operation":{"schema":"v1"}}`, TraceID: "trace-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cancelled || result.EventID != 81 {
		t.Fatalf("result = %#v", result)
	}
	assertExpectations(t, mock)
}

func TestCreateSessionControlRunPersistsReadyTaskAndMessageAtomically(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*id.* FROM `tenant_sessions` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(41), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*status IN.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(41), agenttasks.AgentNameWeb, agenttasks.StatusReady, agenttasks.StatusRunning, 1).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*idempotency_key.*FOR UPDATE").WithArgs(uint64(7), uint64(11), "hash", 1).WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `tenant_agent_tasks`").WillReturnResult(sqlmock.NewResult(51, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").WillReturnResult(sqlmock.NewResult(61, 1))
	mock.ExpectCommit()

	result, err := repo.CreateSessionControlRun(testContext(), SessionControlRunInput{
		Task:    agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusReady, MetadataJSON: `{"session_control_operation":{}}`, IdempotencyKey: "hash"},
		Message: agenttasks.MessageInput{Content: "go"},
	})
	if err != nil || !result.Created || result.Task.ID != 51 || result.EventID != 61 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestCreateSessionControlRunReturnsWinningActiveTaskForFIFO(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*id.* FROM `tenant_sessions` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(41), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*status IN.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(41), agenttasks.AgentNameWeb, agenttasks.StatusReady, agenttasks.StatusRunning, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "idempotency_key", "started_at", "finished_at"}).AddRow(51, 7, 11, 41, "alpha", agenttasks.AgentNameWeb, "", agenttasks.StatusReady, "", "", `{"session_control_operation":{}}`, "trace", "first-key", now, nil))
	mock.ExpectCommit()
	result, err := repo.CreateSessionControlRun(testContext(), SessionControlRunInput{Task: agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusReady, MetadataJSON: `{"session_control_operation":{"schema":"new"}}`, IdempotencyKey: "second-key"}, Message: agenttasks.MessageInput{Content: "second"}})
	if err != nil || !result.Busy || result.Task.ID != 51 || result.Created {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestSessionControlPreparedReplayAcceptsMySQLJSONFormatting(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*id.* FROM `tenant_sessions` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*status IN.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "status", "metadata_json", "idempotency_key"}).AddRow(51, 7, 11, 41, agenttasks.StatusReady, `{"provider": "provider-a", "model": "model-a"}`, "hash"))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events`").WillReturnRows(sqlmock.NewRows([]string{"id", "payload_json"}).AddRow(61, `{"content": "next", "task_id": 51}`))
	mock.ExpectCommit()
	result, err := repo.CreateSessionControlRun(testContext(), SessionControlRunInput{Task: agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady, IdempotencyKey: "hash", MetadataJSON: `{"model":"model-a","provider":"provider-a"}`}, Message: agenttasks.MessageInput{Content: "next"}})
	if err != nil || result.Task.ID != 51 || result.EventID != 61 || result.Created {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertExpectations(t, mock)
	if sessionControlJSONEqual(`{"id":9007199254740992}`, `{"id":9007199254740993}`) {
		t.Fatal("large IDs lost precision")
	}
}

func TestCreateSessionControlRunAdoptsLegacySideChatInMessageTransaction(t *testing.T) {
	for _, failMessage := range []bool{false, true} {
		t.Run(fmt.Sprint(failMessage), func(t *testing.T) {
			repo, mock, closeDB := newMockGormRepository(t)
			defer closeDB()
			metadata := `{"source":"pending-input-side-chat","provider":"provider-a"}`
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT .*id.* FROM `tenant_sessions` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(41), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
			mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*status IN.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(41), agenttasks.AgentNameWeb, agenttasks.StatusReady, agenttasks.StatusRunning, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "status", "metadata_json"}).AddRow(51, 7, 11, 41, agenttasks.StatusReady, metadata))
			mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*idempotency_key.*metadata_json.*model.*WHERE tenant_id = .*user_id = .*id = .*status = .*idempotency_key IS NULL").WithArgs("key", metadata, "model-a", uint64(7), uint64(11), uint64(51), agenttasks.StatusReady).WillReturnResult(sqlmock.NewResult(0, 1))
			message := mock.ExpectExec("INSERT INTO `tenant_agent_task_events`")
			if failMessage {
				message.WillReturnError(errors.New("event insert failed"))
				mock.ExpectRollback()
			} else {
				message.WillReturnResult(sqlmock.NewResult(62, 1))
				mock.ExpectCommit()
			}
			result, err := repo.CreateSessionControlRun(testContext(), SessionControlRunInput{AdoptTaskID: 51, Task: agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady, IdempotencyKey: "key", Model: "model-a", MetadataJSON: metadata}, Message: agenttasks.MessageInput{Content: "followup"}})
			if failMessage {
				if err == nil {
					t.Fatal("partial adoption committed")
				}
			} else if err != nil || result.Task.ID != 51 || result.Task.IdempotencyKey != "key" || result.EventID != 62 || !result.Created {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			assertExpectations(t, mock)
		})
	}
}

func TestSessionControlAdoptedSideChatReplayChecksFollowupEvent(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	metadata := `{"source":"pending-input-side-chat"}`
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*id.* FROM `tenant_sessions` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*status IN.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "status", "metadata_json", "idempotency_key"}).AddRow(51, 7, 11, 41, agenttasks.StatusReady, metadata, "key"))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events` .*ORDER BY id DESC").WithArgs(uint64(7), uint64(11), uint64(51), agenttasks.EventMessage, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "payload_json"}).AddRow(62, `{"task_id":51,"content":"followup"}`))
	mock.ExpectCommit()
	result, err := repo.CreateSessionControlRun(testContext(), SessionControlRunInput{Task: agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady, IdempotencyKey: "key", MetadataJSON: metadata}, Message: agenttasks.MessageInput{Content: "followup"}})
	if err != nil || result.Task.ID != 51 || result.EventID != 62 || result.Created {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestListSessionControlSessionsReadsScopedPrivateConfigInOneQuery(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .*COALESCE\\(metadata_json, ''\\).* FROM `tenant_sessions` WHERE tenant_id = \\? AND user_id = \\? AND archived_at IS NULL ORDER BY last_message_at DESC, started_at DESC LIMIT \\?").WithArgs(uint64(7), uint64(11), 12).WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "metadata_json", "started_at", "last_message_at"}).AddRow(41, "alpha", "Alpha", "idle", "model-a", "/repo", `{"provider":"provider-a"}`, time.Now(), nil))
	items, err := repo.ListSessionControlSessions(testContext(), 7, 11, 12)
	if err != nil || len(items) != 1 || items[0].MetadataJSON != `{"provider":"provider-a"}` {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), "provider-a") {
		t.Fatal("private metadata leaked through public Session JSON")
	}
	assertExpectations(t, mock)
}

func TestListSessionChannelsReturnsLatestChannelPerSession(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .*conversations\\.session_id.*accounts\\.provider.*accounts\\.account_key.*FROM .*channel_conversations AS conversations.*JOIN channel_accounts AS accounts.*JOIN tenant_sessions AS sessions.*WHERE .*sessions\\.tenant_id = \\?.*sessions\\.user_id = \\?.*conversations\\.tenant_id = \\?.*conversations\\.session_id IN \\(\\?,\\?\\).*ORDER BY conversations\\.updated_at DESC, conversations\\.id DESC").
		WithArgs(uint64(7), uint64(11), uint64(7), uint64(41), uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "provider", "account_key"}).
			AddRow(41, ChannelProviderFeishu, "feishu-primary").
			AddRow(42, "dingtalk", "dingtalk-primary").
			AddRow(41, "feishu", "feishu-older"))

	channels, err := repo.ListSessionChannels(testContext(), 7, 11, []uint64{41, 42})
	if err != nil {
		t.Fatal(err)
	}
	if channels[41].Provider != ChannelProviderFeishu || channels[41].AccountKey != "feishu-primary" {
		t.Fatalf("session 41 channel = %+v", channels[41])
	}
	if channels[42].Provider != "dingtalk" || channels[42].AccountKey != "dingtalk-primary" {
		t.Fatalf("session 42 channel = %+v", channels[42])
	}
	assertExpectations(t, mock)
}

func TestStartSessionControlRunClaimsReadyTaskOnce(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM `tenant_sessions` .*parent_session_id.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(7), uint64(11), uint64(51), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(51), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "idempotency_key", "started_at", "finished_at"}).AddRow(51, 7, 11, 41, "alpha", "", "", agenttasks.StatusReady, "", "", `{}`, "trace", "hash", time.Now(), nil))
	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*status.* WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND status = \\?").WithArgs(agenttasks.StatusRunning, uint64(7), uint64(11), uint64(51), agenttasks.StatusReady).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := repo.StartSessionControlRun(testContext(), 7, 11, 51)
	if err != nil || !result.Started || result.Task.ID != 51 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestFailSessionControlRunCommitsReadyFailureAndEventAtomically(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	payload := `{"source":"session_control_prelaunch","error_code":"budget_exceeded"}`
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*finished_at.*result_json.*status.*WHERE tenant_id = .*user_id = .*id = .*status = .*").
		WithArgs(sqlmock.AnyArg(), payload, agenttasks.StatusFailed, uint64(7), uint64(11), uint64(51), agenttasks.StatusReady).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").WillReturnResult(sqlmock.NewResult(61, 1))
	mock.ExpectCommit()

	result, err := repo.FailSessionControlRun(testContext(), SessionControlRunFailureInput{TenantID: 7, UserID: 11, TaskID: 51, ResultJSON: payload, EventPayloadJSON: payload, TraceID: "trace-1"})
	if err != nil || !result.Failed || result.EventID != 61 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestFailSessionControlRunDoesNotOverwriteStartedRace(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	payload := `{"source":"session_control_prelaunch","error_code":"internal_error"}`
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*WHERE tenant_id = .*user_id = .*id = .*status = .*").
		WithArgs(sqlmock.AnyArg(), payload, agenttasks.StatusFailed, uint64(7), uint64(11), uint64(51), agenttasks.StatusReady).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	result, err := repo.FailSessionControlRun(testContext(), SessionControlRunFailureInput{TenantID: 7, UserID: 11, TaskID: 51, ResultJSON: payload, EventPayloadJSON: payload})
	if err != nil || result.Failed || result.EventID != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestFailSessionControlRunRollsBackStatusWhenEventInsertFails(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	payload := `{"source":"session_control_prelaunch","error_code":"internal_error"}`
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*WHERE tenant_id = .*user_id = .*id = .*status = .*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").WillReturnError(errors.New("event insert failed"))
	mock.ExpectRollback()

	result, err := repo.FailSessionControlRun(testContext(), SessionControlRunFailureInput{TenantID: 7, UserID: 11, TaskID: 51, ResultJSON: payload, EventPayloadJSON: payload})
	if err == nil || result.Failed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestStartSessionControlRunCommitsConfigWithClaimOrRollsBack(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(failWrite), func(t *testing.T) {
			repo, mock, closeDB := newMockGormRepository(t)
			defer closeDB()
			metadata := `{"model":"model-b","provider":"provider-b","permission_mode":"ask","effort":"low","prompt_mode":"chat","session_control_operation":{"key_hash":"run-key"}}`
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT id FROM `tenant_sessions` .*parent_session_id.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(7), uint64(11), uint64(51), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
			mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(51), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "status", "model", "metadata_json"}).AddRow(51, 7, 11, 41, agenttasks.StatusReady, "model-b", metadata))
			mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*status.*").WithArgs(agenttasks.StatusRunning, uint64(7), uint64(11), uint64(51), agenttasks.StatusReady).WillReturnResult(sqlmock.NewResult(0, 1))
			write := mock.ExpectExec("UPDATE `tenant_sessions` SET `metadata_json`=JSON_SET.*`model`=.*tenant_id = \\? AND user_id = \\? AND id = \\? AND archived_at IS NULL").WithArgs("$.model", "model-b", "$.provider", "provider-b", "$.permission_mode", "ask", "$.effort", "low", "$.prompt_mode", "chat", "model-b", uint64(7), uint64(11), uint64(41))
			if failWrite {
				write.WillReturnError(errors.New("write rejected"))
				mock.ExpectRollback()
			} else {
				write.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			result, err := repo.StartSessionControlRun(testContext(), 7, 11, 51)
			if failWrite {
				if err == nil || result.Started {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if err != nil || !result.Started || result.Task.MetadataJSON != metadata {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			assertExpectations(t, mock)
		})
	}
}

func TestCreateSessionControlRunRejectsStaleDefaultsBeforeTaskWrite(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*id.* FROM `tenant_sessions` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(41), 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*status IN.*FOR UPDATE").WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` .*idempotency_key.*FOR UPDATE").WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectQuery("SELECT model, metadata_json FROM `tenant_sessions` .*tenant_id = \\? AND user_id = \\? AND id = \\?").WithArgs(uint64(7), uint64(11), uint64(41), 1).WillReturnRows(sqlmock.NewRows([]string{"model", "metadata_json"}).AddRow("model-new", `{"provider":"provider-new"}`))
	mock.ExpectRollback()
	_, err := repo.CreateSessionControlRun(testContext(), SessionControlRunInput{Task: agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady, IdempotencyKey: "key", MetadataJSON: `{}`}, Message: agenttasks.MessageInput{Content: "next"}, ExpectedRuntimeConfigJSON: `{"model":"model-old","provider":"provider-old"}`})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("error=%v", err)
	}
	assertExpectations(t, mock)
}

func TestCreateSessionControlRunRejectsPendingConfigChange(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `tenant_agent_task_pending_input_settings`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_pending_input_settings` .*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "user_id", "session_id", "enabled"}).AddRow(7, 11, "41", true))
	mock.ExpectQuery("SELECT count.* FROM `tenant_agent_task_pending_inputs` .*status NOT IN").WithArgs(uint64(7), uint64(11), "41", pendinginput.StatusSent, pendinginput.StatusCancelled).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()
	_, err := repo.CreateSessionControlRun(testContext(), SessionControlRunInput{Task: agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady, IdempotencyKey: "key", MetadataJSON: `{}`}, Message: agenttasks.MessageInput{Content: "next"}, RuntimeConfigChanged: true})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("error=%v", err)
	}
	assertExpectations(t, mock)
}

func TestRecoverSessionControlStopFindsOriginalRunAfterNewerRun(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	payload := `{"schema":"niuma.session-control-stop.v1","operation":{"key_hash":"` + strings.Repeat("a", 64) + `"}}`
	mock.ExpectQuery("SELECT .*event_id.*task_id.*payload_json.* FROM tenant_agent_task_events AS events JOIN tenant_agent_tasks AS tasks.*parent_session_id = \\?.*operation.key_hash.*LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(41), agenttasks.EventSessionControlStop, strings.Repeat("a", 64), 1).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "task_id", "payload_json"}).AddRow(81, 42, payload))

	result, err := repo.RecoverSessionControlStop(testContext(), 7, 11, 41, strings.Repeat("a", 64))
	if err != nil || !result.Found || result.TaskID != 42 || result.EventID != 81 || result.EventPayloadJSON != payload {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	assertExpectations(t, mock)
}

func TestInsertSessionControlAuditSerializesOnTenantAndReplays(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM `tenants` WHERE id = \\?.*FOR UPDATE").WithArgs(uint64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery("SELECT .* FROM `tenant_audit_logs` WHERE tenant_id = \\? AND actor_user_id = \\? AND action = \\? AND resource_type = \\? AND resource_id = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(11), "session_control.send", "session", "operation-id", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "actor_user_id", "action", "resource_type", "resource_id", "metadata_json", "trace_id", "created_at"}).
			AddRow(91, 7, 11, "session_control.send", "session", "operation-id", `{"safe":true}`, "trace-1", now))
	mock.ExpectCommit()

	result, err := repo.InsertSessionControlAudit(testContext(), SessionControlAuditInput{AuditLogInput: AuditLogInput{TenantID: 7, ActorUserID: 11, Action: "session_control.send", ResourceType: "session", ResourceID: "operation-id", MetadataJSON: `{"safe":true}`, TraceID: "trace-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.Audit.ID != 91 {
		t.Fatalf("result = %#v", result)
	}
	assertExpectations(t, mock)
}

func TestSessionControlRepositoryRejectsIncompleteScopeBeforeSQL(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	if _, err := repo.GetSessionControlSessionByKey(testContext(), 0, 11, "session-a"); err != ErrInvalidInput {
		t.Fatalf("GetSessionControlSessionByKey error = %v", err)
	}
	if _, err := repo.CreateSessionControlSession(testContext(), SessionControlCreateInput{}); err != ErrInvalidInput {
		t.Fatalf("CreateSessionControlSession error = %v", err)
	}
	if _, err := repo.CancelAgentTaskForSessionControl(testContext(), SessionControlStopInput{}); err != ErrInvalidInput {
		t.Fatalf("CancelAgentTaskForSessionControl error = %v", err)
	}
	if _, err := repo.InsertSessionControlAudit(testContext(), SessionControlAuditInput{}); err != ErrInvalidInput {
		t.Fatalf("InsertSessionControlAudit error = %v", err)
	}
	assertExpectations(t, mock)
}
