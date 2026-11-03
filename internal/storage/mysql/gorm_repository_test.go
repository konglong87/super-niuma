package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/goal"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestGormRepositoryGetTenantByKey(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_key, name, status, COALESCE(settings_json, '') AS settings_json FROM `tenants` WHERE tenant_key = ? AND deleted_at IS NULL LIMIT ?")).
		WithArgs("yutang", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_key", "name", "status", "settings_json"}).AddRow(1, "yutang", "Yutang", "active", `{"tier":"pro"}`))

	tenant, err := repo.GetTenantByKey(testContext(), "yutang")
	if err != nil {
		t.Fatal(err)
	}
	if tenant.ID != 1 || tenant.TenantKey != "yutang" || tenant.Status != "active" || tenant.SettingsJSON == "" {
		t.Fatalf("tenant = %+v", tenant)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryGetTenantByKeyNotFound(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_key, name, status, COALESCE(settings_json, '') AS settings_json FROM `tenants` WHERE tenant_key = ? AND deleted_at IS NULL LIMIT ?")).
		WithArgs("missing", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := repo.GetTenantByKey(testContext(), "missing")
	if err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryTenantAdminLifecycle(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("INSERT INTO `tenants` .*ON DUPLICATE KEY UPDATE").
		WithArgs("acme", "Acme", "active", `{"region":"us"}`, nil, nil, "Acme", `{"region":"us"}`, "active").
		WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_key, name, status, COALESCE(settings_json, '') AS settings_json FROM `tenants` WHERE tenant_key = ? AND deleted_at IS NULL LIMIT ?")).
		WithArgs("acme", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_key", "name", "status", "settings_json"}).AddRow(11, "acme", "Acme", "active", `{"region":"us"}`))
	id, err := repo.UpsertTenant(testContext(), TenantInput{TenantKey: "acme", Name: "Acme", SettingsJSON: `{"region":"us"}`})
	if err != nil {
		t.Fatal(err)
	}
	if id != 11 {
		t.Fatalf("id = %d", id)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_key, name, status, COALESCE(settings_json, '') AS settings_json FROM `tenants` WHERE deleted_at IS NULL ORDER BY tenant_key ASC LIMIT ?")).
		WithArgs(2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_key", "name", "status", "settings_json"}).AddRow(11, "acme", "Acme", "active", `{"region":"us"}`))
	tenants, err := repo.ListTenants(testContext(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 1 || tenants[0].TenantKey != "acme" {
		t.Fatalf("tenants = %+v", tenants)
	}

	mock.ExpectExec("UPDATE `tenants` SET .*`deleted_at`=.*`status`=.* WHERE tenant_key = \\? AND deleted_at IS NULL").
		WithArgs(sqlmock.AnyArg(), "archived", "acme").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.ArchiveTenant(testContext(), "acme"); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryUpsertMessageUsesContextTraceID(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, session_key, title, status, model, cwd, started_at, last_message_at FROM `tenant_sessions` WHERE tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(3), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}).
			AddRow(3, "session-3", "Title", "active", "claude", "/workspace", now, now))
	mock.ExpectExec("INSERT INTO `tenant_session_messages` .*ON DUPLICATE KEY UPDATE .*LAST_INSERT_ID").
		WithArgs(uint64(1), uint64(2), uint64(3), uint(4), "assistant", "hello", nil, nil, nil, false, "claude", uint(11), uint(22), "trace-1").
		WillReturnResult(sqlmock.NewResult(99, 1))

	id, err := repo.UpsertMessage(testContext(), MessageInput{
		TenantID:    1,
		UserID:      2,
		SessionID:   3,
		TurnIndex:   4,
		Role:        "assistant",
		Content:     "hello",
		Model:       "claude",
		InputTokens: 11,
		OutputToken: 22,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 99 {
		t.Fatalf("id = %d", id)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryUpsertMessageRejectsForeignSession(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, session_key, title, status, model, cwd, started_at, last_message_at FROM `tenant_sessions` WHERE tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(3), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}))

	_, err := repo.UpsertMessage(testContext(), MessageInput{
		TenantID:  1,
		UserID:    2,
		SessionID: 3,
		TurnIndex: 4,
		Role:      "assistant",
		Content:   "hello",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryArchiveUserNotFound(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `tenant_users` SET `deleted_at`=CURRENT_TIMESTAMP(6),`status`=? WHERE tenant_id = ? AND user_key = ? AND deleted_at IS NULL")).
		WithArgs("archived", uint64(1), "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.ArchiveUser(testContext(), 1, "missing")
	if err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryEnsureUserFallsBackToSelectWhenIDNotReturned(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("INSERT INTO `tenant_users` .*ON DUPLICATE KEY UPDATE .*LAST_INSERT_ID").
		WithArgs(uint64(1), "user-1", sqlmock.AnyArg(), sqlmock.AnyArg(), nil, nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `id` FROM `tenant_users` WHERE tenant_id = ? AND user_key = ? AND deleted_at IS NULL LIMIT ?")).
		WithArgs(uint64(1), "user-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))

	id, err := repo.EnsureUser(testContext(), 1, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id = %d", id)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryUpsertsFallBackToSelectWhenIDNotReturned(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectExec("INSERT INTO `tenant_user_memories` .*ON DUPLICATE KEY UPDATE .*LAST_INSERT_ID").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_user_memories` WHERE tenant_id = ? AND user_id = ? AND memory_key = ? AND deleted_at IS NULL LIMIT ?")).
		WithArgs(uint64(1), uint64(2), "webui.validation.preference", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(70))
	memoryID, err := repo.UpsertMemory(testContext(), MemoryInput{TenantID: 1, UserID: 2, MemoryKey: "webui.validation.preference", Category: "webui", Content: "content", Importance: 8, Source: "webui-scenario"})
	if err != nil {
		t.Fatal(err)
	}
	if memoryID != 70 {
		t.Fatalf("memoryID = %d", memoryID)
	}

	mock.ExpectExec("INSERT INTO `tenant_skills` .*ON DUPLICATE KEY UPDATE .*LAST_INSERT_ID").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_skills` WHERE tenant_id = ? AND skill_key = ? AND version = ? AND deleted_at IS NULL LIMIT ?")).
		WithArgs(uint64(1), "webui-validation", uint(1), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(71))
	skillID, err := repo.UpsertSkill(testContext(), SkillInput{TenantID: 1, SkillKey: "webui-validation", Name: "WebUI Validation", ContentMD: "# Skill", Version: 1, Enabled: true, CreatedByUserID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if skillID != 71 {
		t.Fatalf("skillID = %d", skillID)
	}

	mock.ExpectExec("INSERT INTO `tenant_user_skill_overrides` .*ON DUPLICATE KEY UPDATE .*LAST_INSERT_ID").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_user_skill_overrides` WHERE tenant_id = ? AND user_id = ? AND skill_id = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(71), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(72))
	overrideID, err := repo.UpsertSkillOverride(testContext(), SkillOverrideInput{TenantID: 1, UserID: 2, SkillID: 71, Enabled: true, ConfigJSON: `{"source":"webui-scenario"}`})
	if err != nil {
		t.Fatal(err)
	}
	if overrideID != 72 {
		t.Fatalf("overrideID = %d", overrideID)
	}

	assertExpectations(t, mock)
}

func TestGormRepositorySessionLifecycle(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, session_key, title, status, model, cwd, started_at, last_message_at FROM `tenant_sessions` WHERE tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(5), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}).
			AddRow(5, "session-1", "Session One", "active", "claude", "/tmp/work", now, now))
	item, err := repo.GetSession(testContext(), 1, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != 5 || item.SessionKey != "session-1" {
		t.Fatalf("item = %+v", item)
	}

	mock.ExpectExec("UPDATE `tenant_sessions` SET .* WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND archived_at IS NULL").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(1), uint64(2), uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateSession(testContext(), SessionInput{ID: 5, TenantID: 1, UserID: 2, Title: "Renamed", Status: "paused"}); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("UPDATE `tenant_sessions` SET .* WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND archived_at IS NULL").
		WithArgs(sqlmock.AnyArg(), "archived", uint64(1), uint64(2), uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.ArchiveSession(testContext(), 1, 2, 5); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryListMessages(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at FROM `tenant_session_messages` WHERE tenant_id = ? AND user_id = ? AND session_id = ? ORDER BY turn_index ASC LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(5), 10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "turn_index", "role", "content", "content_json", "tool_id", "tool_name", "is_error", "model", "input_tokens", "output_tokens", "trace_id", "created_at"}).
			AddRow(90, 5, 1, "user", "hello", nil, nil, nil, false, "claude", 3, 0, "trace-1", now))

	items, err := repo.ListMessages(testContext(), 1, 2, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Role != "user" || items[0].Content != "hello" || items[0].TraceID != "trace-1" {
		t.Fatalf("items = %+v", items)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryAgentTaskLifecycle(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectExec("INSERT INTO `tenant_agent_tasks`").
		WithArgs(uint64(1), uint64(2), uint64(5), "sub-session", "reviewer", "review", "do it", agenttasks.StatusRunning, "claude", nil, nil, "trace-1", nil, nil).
		WillReturnResult(sqlmock.NewResult(41, 1))
	taskID, err := repo.CreateAgentTask(testContext(), agenttasks.TaskInput{
		TenantID:           1,
		UserID:             2,
		ParentSessionID:    5,
		SubagentSessionKey: "sub-session",
		AgentName:          "reviewer",
		Description:        "review",
		Prompt:             "do it",
		Model:              "claude",
		TraceID:            "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if taskID != 41 {
		t.Fatalf("taskID = %d", taskID)
	}

	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .* WHERE id = \\?").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(41)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.FinishAgentTask(testContext(), 41, agenttasks.StatusCompleted, `{"ok":true}`); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_id, user_id, COALESCE(parent_session_id, 0), COALESCE(subagent_session_key, ''), COALESCE(agent_name, ''), COALESCE(description, ''), status, COALESCE(model, ''), COALESCE(result_json, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), started_at, finished_at FROM `tenant_agent_tasks` WHERE tenant_id = ? AND user_id = ? AND id = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(41), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "started_at", "finished_at"}).
			AddRow(41, 1, 2, 5, "sub-session", "reviewer", "review", agenttasks.StatusRunning, "claude", "", `{"phase":"run"}`, "trace-1", now, nil))
	task, err := repo.GetAgentTask(testContext(), 1, 2, 41)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != 41 || task.MetadataJSON != `{"phase":"run"}` {
		t.Fatalf("task = %+v", task)
	}

	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .* WHERE tenant_id = \\? AND user_id = \\? AND id = \\?").
		WithArgs(sqlmock.AnyArg(), agenttasks.StatusCompleted, uint64(1), uint64(2), uint64(41)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateAgentTask(testContext(), 41, agenttasks.TaskUpdate{TenantID: 1, UserID: 2, Status: agenttasks.StatusCompleted}); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").
		WithArgs(uint64(1), uint64(2), uint64(41), agenttasks.EventStarted, `{"turn":1}`, "trace-1").
		WillReturnResult(sqlmock.NewResult(90, 1))
	eventID, err := repo.AppendAgentTaskEvent(testContext(), agenttasks.EventInput{TenantID: 1, UserID: 2, TaskID: 41, EventType: agenttasks.EventStarted, PayloadJSON: `{"turn":1}`, TraceID: "trace-1"})
	if err != nil {
		t.Fatal(err)
	}
	if eventID != 90 {
		t.Fatalf("eventID = %d", eventID)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_id, user_id, COALESCE(parent_session_id, 0), COALESCE(subagent_session_key, ''), COALESCE(agent_name, ''), COALESCE(description, ''), status, COALESCE(model, ''), COALESCE(result_json, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), started_at, finished_at FROM `tenant_agent_tasks` WHERE tenant_id = ? AND user_id = ? ORDER BY started_at DESC LIMIT ?")).
		WithArgs(uint64(1), uint64(2), 10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "started_at", "finished_at"}).
			AddRow(41, 1, 2, 5, "sub-session", "reviewer", "review", agenttasks.StatusCompleted, "claude", `{"ok":true}`, "", "trace-1", now, now))
	tasks, err := repo.ListAgentTasks(testContext(), 1, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != 41 || tasks[0].Status != agenttasks.StatusCompleted {
		t.Fatalf("tasks = %+v", tasks)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, task_id, event_type, COALESCE(payload_json, ''), COALESCE(trace_id, ''), created_at FROM `tenant_agent_task_events` WHERE tenant_id = ? AND user_id = ? AND task_id = ? AND id > ? ORDER BY id ASC LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(41), uint64(0), 10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "event_type", "payload_json", "trace_id", "created_at"}).
			AddRow(90, 41, agenttasks.EventStarted, `{"turn":1}`, "trace-1", now))
	events, err := repo.ListAgentTaskEvents(testContext(), 1, 2, 41, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != agenttasks.EventStarted {
		t.Fatalf("events = %+v", events)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, task_id, event_type, COALESCE(payload_json, ''), COALESCE(trace_id, ''), created_at FROM `tenant_agent_task_events` WHERE tenant_id = ? AND user_id = ? AND task_id = ? AND id > ? ORDER BY id ASC LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(41), uint64(90), 10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "event_type", "payload_json", "trace_id", "created_at"}).
			AddRow(91, 41, agenttasks.EventCompleted, `{"ok":true}`, "trace-1", now))
	events, err = repo.ListAgentTaskEventsAfter(testContext(), 1, 2, 41, 90, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != 91 || events[0].EventType != agenttasks.EventCompleted {
		t.Fatalf("events after = %+v", events)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryGetAgentTaskEventIsTenantUserScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	query := regexp.QuoteMeta("SELECT id, task_id, event_type, COALESCE(payload_json, ''), COALESCE(trace_id, ''), created_at FROM `tenant_agent_task_events` WHERE tenant_id = ? AND user_id = ? AND id = ? LIMIT ?")
	mock.ExpectQuery(query).
		WithArgs(uint64(7), uint64(11), uint64(90), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "event_type", "payload_json", "trace_id", "created_at"}).
			AddRow(90, 41, agenttasks.EventToolResult, `{"tool_name":"go test"}`, "trace-1", now))

	event, err := repo.GetAgentTaskEvent(testContext(), 7, 11, 90)
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != 90 || event.TaskID != 41 || event.EventType != agenttasks.EventToolResult {
		t.Fatalf("event = %+v", event)
	}

	mock.ExpectQuery(query).
		WithArgs(uint64(7), uint64(12), uint64(90), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "event_type", "payload_json", "trace_id", "created_at"}))
	if _, err := repo.GetAgentTaskEvent(testContext(), 7, 12, 90); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign user lookup error = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryGetSessionByKeyIsTenantUserScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	oldStartedAt := time.Date(2022, time.January, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, session_key, title, status, model, cwd, started_at, last_message_at FROM `tenant_sessions` WHERE tenant_id = ? AND user_id = ? AND session_key = ? AND archived_at IS NULL LIMIT ?")).
		WithArgs(uint64(7), uint64(11), "old-session", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}).
			AddRow(31, "old-session", "Old session", "idle", "model-a", "/repo", oldStartedAt, oldStartedAt))

	session, err := repo.GetSessionByKey(testContext(), 7, 11, "old-session")
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != 31 || session.SessionKey != "old-session" || !session.StartedAt.Equal(oldStartedAt) {
		t.Fatalf("session = %+v", session)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryListLatestAgentTasksForSessionsIsDeterministicAndScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT .*ROW_NUMBER\\(\\) OVER \\(PARTITION BY parent_session_id ORDER BY CASE WHEN agent_name = \\? THEN 0 ELSE 1 END, started_at DESC, id DESC\\).*AS row_rank.*FROM tenant_agent_tasks.*tenant_id = \\? AND user_id = \\? AND parent_session_id IN \\(\\?,\\?\\).*ranked_tasks.*row_rank = 1.*ORDER BY parent_session_id ASC").
		WithArgs(agenttasks.AgentNameWeb, uint64(7), uint64(11), uint64(5), uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "started_at", "finished_at"}).
			AddRow(42, 7, 11, 5, "", "review", "latest", agenttasks.StatusRunning, "model-a", "", "", "trace-1", now, nil).
			AddRow(88, 7, 11, 9, "", "build", "latest", agenttasks.StatusCompleted, "model-a", "", "", "trace-2", now, now))

	tasks, err := repo.ListLatestAgentTasksForSessions(testContext(), 7, 11, []uint64{5, 9})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != 42 || tasks[1].ID != 88 {
		t.Fatalf("tasks = %+v", tasks)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCancelAgentTaskIfRunningDoesNotOverwriteTerminalRace(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND status = \\?").
		WithArgs(sqlmock.AnyArg(), `{"source":"session_control"}`, sqlmock.AnyArg(), uint64(7), uint64(11), uint64(41), agenttasks.StatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 1))
	cancelled, err := repo.CancelAgentTaskIfRunning(testContext(), 7, 11, 41, `{"source":"session_control"}`)
	if err != nil || !cancelled {
		t.Fatalf("cancelled=%t err=%v", cancelled, err)
	}

	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .*WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND status = \\?").
		WithArgs(sqlmock.AnyArg(), `{"source":"session_control"}`, sqlmock.AnyArg(), uint64(7), uint64(11), uint64(41), agenttasks.StatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 0))
	cancelled, err = repo.CancelAgentTaskIfRunning(testContext(), 7, 11, 41, `{"source":"session_control"}`)
	if err != nil || cancelled {
		t.Fatalf("cancelled=%t err=%v", cancelled, err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryListAgentTaskEventsForTasksCompleteFailsRatherThanSilentlyTruncating(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	rows := sqlmock.NewRows([]string{"id", "task_id", "event_type", "payload_json", "trace_id", "created_at"})
	for id := 1; id <= maxCompleteAgentTaskEventBatch+1; id++ {
		eventType := agenttasks.EventStarted
		if id == maxCompleteAgentTaskEventBatch+1 {
			eventType = "permission_resolved"
		}
		rows.AddRow(id, 41, eventType, "{}", "trace-1", time.Now().UTC())
	}
	mock.ExpectQuery("SELECT id, task_id, event_type, COALESCE\\(payload_json, ''\\), COALESCE\\(trace_id, ''\\), created_at FROM `tenant_agent_task_events` WHERE tenant_id = \\? AND user_id = \\? AND task_id IN \\(\\?,\\?\\) ORDER BY id ASC LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(41), uint64(42), maxCompleteAgentTaskEventBatch+1).
		WillReturnRows(rows)

	events, err := repo.ListAgentTaskEventsForTasksComplete(testContext(), 7, 11, []uint64{41, 42})
	if !errors.Is(err, ErrTooManyAgentTaskEvents) {
		t.Fatalf("err = %v, want ErrTooManyAgentTaskEvents", err)
	}
	if events != nil {
		t.Fatalf("events = %+v, want nil on incomplete permission window", events)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateAgentTaskLegacyEmptyIdempotencyIsNull(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("INSERT INTO `tenant_agent_tasks`").
		WithArgs(uint64(1), uint64(2), nil, nil, nil, nil, nil, agenttasks.StatusRunning, nil, nil, nil, "trace-legacy", nil, nil).
		WillReturnResult(sqlmock.NewResult(51, 1))
	taskID, err := repo.CreateAgentTask(testContext(), agenttasks.TaskInput{TenantID: 1, UserID: 2, TraceID: "trace-legacy"})
	if err != nil || taskID != 51 {
		t.Fatalf("taskID=%d err=%v", taskID, err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryGetAgentTaskByIdempotencyKeyIsScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` WHERE tenant_id = \\? AND user_id = \\? AND idempotency_key = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(11), "create-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "idempotency_key", "started_at", "finished_at"}).
			AddRow(41, 7, 11, nil, nil, "reviewer", "review", agenttasks.StatusRunning, "claude", nil, nil, "trace-1", "create-1", time.Now().UTC(), nil))
	task, err := repo.GetAgentTaskByIdempotencyKey(testContext(), 7, 11, "create-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != 41 || task.TenantID != 7 || task.UserID != 11 || task.IdempotencyKey != "create-1" {
		t.Fatalf("task=%+v", task)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryGetAgentTaskByIdempotencyKeyMiss(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` WHERE tenant_id = \\? AND user_id = \\? AND idempotency_key = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(11), "missing", 1).
		WillReturnError(gorm.ErrRecordNotFound)
	_, err := repo.GetAgentTaskByIdempotencyKey(testContext(), 7, 11, "missing")
	if err != ErrNotFound {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositorySessionLinkPersistenceIsScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("INSERT INTO `tenant_session_links`").
		WillReturnResult(sqlmock.NewResult(73, 1))
	linkID, err := repo.UpsertSessionLink(testContext(), SessionLinkInput{
		TenantID: 7, UserID: 11, TargetSessionID: 99, SourceKind: "tenant", SourceSessionKey: "source-1", RelationType: "handoff", MetadataJSON: `{"reason":"test"}`, CreatedByUserID: 11,
	})
	if err != nil || linkID != 73 {
		t.Fatalf("linkID=%d err=%v", linkID, err)
	}

	mock.ExpectQuery("SELECT .* FROM `tenant_session_links` WHERE tenant_id = \\? AND user_id = \\? AND target_session_id = \\? AND source_kind = \\? AND source_session_key = \\? AND relation_type = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(99), "tenant", "source-1", "handoff", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "status", "metadata_json", "created_by_user_id", "created_at", "updated_at"}).
			AddRow(73, 7, 11, 99, "tenant", "source-1", nil, "handoff", "active", `{"reason":"test"}`, 11, time.Now().UTC(), time.Now().UTC()))
	link, err := repo.GetSessionLink(testContext(), 7, 11, 99, "tenant", "source-1", "handoff")
	if err != nil || link.ID != 73 || link.MetadataJSON == "" {
		t.Fatalf("link=%+v err=%v", link, err)
	}

	mock.ExpectQuery("SELECT .* FROM `tenant_session_links` WHERE tenant_id = \\? AND user_id = \\? AND target_session_id = \\? ORDER BY created_at ASC, id ASC LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(99), 20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "status", "metadata_json", "created_by_user_id", "created_at", "updated_at"}).
			AddRow(73, 7, 11, 99, "tenant", "source-1", nil, "handoff", "active", `{"reason":"test"}`, 11, time.Now().UTC(), time.Now().UTC()))
	links, err := repo.ListSessionLinks(testContext(), 7, 11, 99, 20)
	if err != nil || len(links) != 1 || links[0].ID != 73 {
		t.Fatalf("links=%+v err=%v", links, err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositorySessionLinkReplayUpdatesMetadata(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := SessionLinkInput{TenantID: 7, UserID: 11, TargetSessionID: 99, SourceKind: "tenant", SourceSessionKey: "source-1", RelationType: "handoff", Status: "active", MetadataJSON: `{"attempt":1}`, CreatedByUserID: 11}
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE .*metadata_json.*status.*updated_at").
		WillReturnResult(sqlmock.NewResult(73, 1))
	firstID, err := repo.UpsertSessionLink(testContext(), input)
	if err != nil || firstID != 73 {
		t.Fatalf("first id=%d err=%v", firstID, err)
	}
	input.Status = "resolved"
	input.MetadataJSON = `{"attempt":2,"resolved":true}`
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE .*metadata_json.*status.*updated_at").
		WithArgs(uint64(7), uint64(11), uint64(99), "tenant", "source-1", nil, "handoff", "resolved", `{"attempt":2,"resolved":true}`, uint64(11), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(73, 2))
	replayedID, err := repo.UpsertSessionLink(testContext(), input)
	if err != nil || replayedID != 73 {
		t.Fatalf("replayed id=%d err=%v", replayedID, err)
	}
	mock.ExpectQuery("SELECT .* FROM `tenant_session_links` WHERE tenant_id = \\? AND user_id = \\? AND target_session_id = \\? AND source_kind = \\? AND source_session_key = \\? AND relation_type = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(99), "tenant", "source-1", "handoff", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "target_session_id", "source_kind", "source_session_key", "source_session_id", "relation_type", "status", "metadata_json", "created_by_user_id", "created_at", "updated_at"}).
			AddRow(73, 7, 11, 99, "tenant", "source-1", nil, "handoff", "resolved", `{"attempt":2,"resolved":true}`, 11, time.Now().UTC(), time.Now().UTC()))
	readback, err := repo.GetSessionLink(testContext(), 7, 11, 99, "tenant", "source-1", "handoff")
	if err != nil || readback.Status != "resolved" || readback.MetadataJSON != `{"attempt":2,"resolved":true}` {
		t.Fatalf("readback=%+v err=%v", readback, err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffLinkAndEventIsAtomic(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := agenttasks.HandoffLinkAndEventInput{
		TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42,
		SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", LinkStatus: "active", LinkMetadataJSON: `{"transcript":"must be ignored"}`,
		CreatedByUserID: 11, PayloadJSON: testSessionHandoffEvent(t, 42), HandoffIdentity: "forged-caller-identity", TraceID: "trace-1",
	}
	decoded, err := agenttasks.DecodeSessionHandoffEvent(input.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, agenttasks.StatusReady))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events` .*JSON_EXTRACT.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, decoded.PackageSHA256, decoded.PackageID, 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(41), "tenant", "source", nil, "handoff", "active", fmt.Sprintf(`{"schema":"%s","package_id":"%s","package_sha256":"%s","target_task_id":42}`, decoded.Schema, decoded.PackageID, decoded.PackageSHA256), uint64(11), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").
		WillReturnResult(sqlmock.NewResult(202, 1))
	mock.ExpectCommit()

	result, err := repo.CreateHandoffLinkAndEvent(testContext(), input)
	if err != nil {
		t.Fatalf("CreateHandoffLinkAndEvent() error = %v", err)
	}
	if result.LinkID != 101 || result.EventID != 202 || result.Replayed {
		t.Fatalf("result = %#v", result)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffBatchRollsBackEverySourceWhenLaterEventFails(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	identity := strings.Repeat("a", 64)
	first := testSessionHandoffEventForOperation(t, 42, "tenant:source-a", "task_event:11", identity)
	second := testSessionHandoffEventForOperation(t, 42, "local:source-b", "entry:one", identity)
	input := agenttasks.HandoffBatchInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, OperationIdentity: identity, CreatedByUserID: 11, Items: []agenttasks.HandoffLinkAndEventInput{
		{SourceKind: "tenant", SourceSessionKey: "source-a", RelationType: "handoff", LinkStatus: "active", PayloadJSON: first},
		{SourceKind: "local", SourceSessionKey: "source-b", RelationType: "handoff", LinkStatus: "active", PayloadJSON: second},
	}}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(42), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, agenttasks.StatusReady))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events` .*operation_identity.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, identity).WillReturnRows(sqlmock.NewRows([]string{"id", "payload_json"}))
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").WillReturnResult(sqlmock.NewResult(201, 1))
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(102, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").WillReturnError(errors.New("second event failed"))
	mock.ExpectRollback()
	if _, err := repo.CreateHandoffBatch(testContext(), input); err == nil {
		t.Fatal("CreateHandoffBatch() error = nil, want rollback")
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffBatchReplaysWholeOperationWithoutNewEvents(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	identity := strings.Repeat("b", 64)
	first := testSessionHandoffEventForOperation(t, 42, "tenant:source-a", "task_event:11", identity)
	second := testSessionHandoffEventForOperation(t, 42, "local:source-b", "entry:one", identity)
	input := agenttasks.HandoffBatchInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, OperationIdentity: identity, Items: []agenttasks.HandoffLinkAndEventInput{{SourceKind: "tenant", SourceSessionKey: "source-a", RelationType: "handoff", PayloadJSON: first}, {SourceKind: "local", SourceSessionKey: "source-b", RelationType: "handoff", PayloadJSON: second}}}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(42), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, agenttasks.StatusRunning))
	mock.ExpectQuery("SELECT .*operation_identity.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, identity).WillReturnRows(sqlmock.NewRows([]string{"id", "payload_json"}).AddRow(201, first).AddRow(202, second))
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(102, 1))
	mock.ExpectCommit()
	result, err := repo.CreateHandoffBatch(testContext(), input)
	if err != nil || !result.Replayed || len(result.Items) != 2 || result.Items[0].EventID != 201 || result.Items[1].EventID != 202 {
		t.Fatalf("CreateHandoffBatch() result=%#v error=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffBatchRejectsPartialOperationReplay(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	identity := strings.Repeat("c", 64)
	first := testSessionHandoffEventForOperation(t, 42, "tenant:source-a", "task_event:11", identity)
	second := testSessionHandoffEventForOperation(t, 42, "local:source-b", "entry:one", identity)
	input := agenttasks.HandoffBatchInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, OperationIdentity: identity, Items: []agenttasks.HandoffLinkAndEventInput{{SourceKind: "tenant", SourceSessionKey: "source-a", RelationType: "handoff", PayloadJSON: first}, {SourceKind: "local", SourceSessionKey: "source-b", RelationType: "handoff", PayloadJSON: second}}}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(42), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, agenttasks.StatusReady))
	mock.ExpectQuery("SELECT .*operation_identity.*FOR UPDATE").WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, identity).WillReturnRows(sqlmock.NewRows([]string{"id", "payload_json"}).AddRow(201, first))
	mock.ExpectRollback()
	if _, err := repo.CreateHandoffBatch(testContext(), input); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("CreateHandoffBatch() error=%v, want ErrInvalidState", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryRecoverHandoffBatchAllowsRunningTarget(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	identity := strings.Repeat("d", 64)
	metadata := testSessionControlOperationMetadata(t, "attach", identity, strings.Repeat("f", 64))
	payload := testSessionHandoffEventWithOperationMetadata(t, 42, "tenant:source", "task_event:11", identity, metadata)
	decoded, _ := agenttasks.DecodeSessionHandoffEvent(payload)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.* FROM `tenant_agent_tasks`").WithArgs(uint64(7), uint64(11), uint64(42), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id"}).AddRow(42, 41))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events` .*operation_identity").WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, identity).WillReturnRows(sqlmock.NewRows([]string{"id", "payload_json"}).AddRow(202, payload))
	mock.ExpectQuery("SELECT .*id.* FROM `tenant_session_links`").WithArgs(uint64(7), uint64(11), uint64(41), "tenant", "source", agenttasks.SessionHandoffRelationType, 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(101))
	mock.ExpectCommit()
	result, err := repo.RecoverHandoffBatch(testContext(), agenttasks.HandoffRecoveryInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, OperationIdentity: identity, ExpectedSources: []string{"tenant:source"}})
	if err != nil || !result.Found || result.OperationMetadataJSON != metadata || len(result.Items) != 1 || result.Items[0].PackageSHA256 != decoded.PackageSHA256 || result.Items[0].LinkID != 101 || result.Items[0].EventID != 202 {
		t.Fatalf("RecoverHandoffBatch() result=%#v error=%v", result, err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryRecoverHandoffBatchRejectsPartialStoredOperation(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	identity := strings.Repeat("e", 64)
	payload := testSessionHandoffEventForOperation(t, 42, "tenant:source", "task_event:11", identity)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.* FROM `tenant_agent_tasks`").WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id"}).AddRow(42, 41))
	mock.ExpectQuery("SELECT .*operation_identity").WillReturnRows(sqlmock.NewRows([]string{"id", "payload_json"}).AddRow(202, payload))
	mock.ExpectRollback()
	_, err := repo.RecoverHandoffBatch(testContext(), agenttasks.HandoffRecoveryInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, OperationIdentity: identity, ExpectedSources: []string{"tenant:source", "local:other"}})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("RecoverHandoffBatch() error=%v", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffLinkAndEventRollsBackEventFailure(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := agenttasks.HandoffLinkAndEventInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", LinkStatus: "active", CreatedByUserID: 11, PayloadJSON: testSessionHandoffEvent(t, 42)}
	decoded, err := agenttasks.DecodeSessionHandoffEvent(input.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, agenttasks.StatusReady))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events` .*JSON_EXTRACT.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, decoded.PackageSHA256, decoded.PackageID, 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").
		WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectExec("INSERT INTO `tenant_agent_task_events`").
		WillReturnError(errors.New("event write failed"))
	mock.ExpectRollback()

	if _, err := repo.CreateHandoffLinkAndEvent(testContext(), input); err == nil {
		t.Fatal("CreateHandoffLinkAndEvent() error = nil, want rollback")
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffLinkAndEventReplaysExistingIdentity(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := agenttasks.HandoffLinkAndEventInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", LinkStatus: "active", CreatedByUserID: 11, PayloadJSON: testSessionHandoffEvent(t, 42)}
	decoded, err := agenttasks.DecodeSessionHandoffEvent(input.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, agenttasks.StatusReady))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events` .*JSON_EXTRACT.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, decoded.PackageSHA256, decoded.PackageID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(202))
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").
		WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectCommit()

	result, err := repo.CreateHandoffLinkAndEvent(testContext(), input)
	if err != nil {
		t.Fatalf("CreateHandoffLinkAndEvent() error = %v", err)
	}
	if result.LinkID != 101 || result.EventID != 202 || !result.Replayed {
		t.Fatalf("result = %#v", result)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffLinkAndEventRejectsMissingTask(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := agenttasks.HandoffLinkAndEventInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", PayloadJSON: testSessionHandoffEvent(t, 42)}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectRollback()

	if _, err := repo.CreateHandoffLinkAndEvent(testContext(), input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateHandoffLinkAndEvent() error = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffLinkAndEventRejectsTaskFromAnotherSession(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := agenttasks.HandoffLinkAndEventInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", PayloadJSON: testSessionHandoffEvent(t, 42)}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 99, agenttasks.StatusReady))
	mock.ExpectRollback()
	if _, err := repo.CreateHandoffLinkAndEvent(testContext(), input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateHandoffLinkAndEvent() error = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffLinkAndEventRollsBackLinkFailure(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := agenttasks.HandoffLinkAndEventInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", PayloadJSON: testSessionHandoffEvent(t, 42)}
	decoded, err := agenttasks.DecodeSessionHandoffEvent(input.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, agenttasks.StatusReady))
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_events` .*JSON_EXTRACT.*FOR UPDATE").
		WithArgs(uint64(7), uint64(11), uint64(42), agenttasks.EventSessionHandoff, decoded.PackageSHA256, decoded.PackageID, 1).
		WillReturnError(gorm.ErrRecordNotFound)
	mock.ExpectExec("INSERT INTO `tenant_session_links` .*ON DUPLICATE KEY UPDATE").
		WillReturnError(errors.New("link write failed"))
	mock.ExpectRollback()

	if _, err := repo.CreateHandoffLinkAndEvent(testContext(), input); err == nil {
		t.Fatal("CreateHandoffLinkAndEvent() error = nil, want rollback")
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryCreateHandoffLinkAndEventRejectsInvalidPayloadBeforeTransaction(t *testing.T) {
	for name, payload := range map[string]string{
		"malformed":          `{`,
		"transcript":         strings.TrimSuffix(testSessionHandoffEvent(t, 42), "}") + `,"transcript":[{"content":"secret"}]}`,
		"package transcript": strings.Replace(testSessionHandoffEvent(t, 42), `"objective":"continue"`, `"objective":"continue","messages":[{"content":"secret"}]`, 1),
		"target":             testSessionHandoffEvent(t, 99),
	} {
		t.Run(name, func(t *testing.T) {
			repo, mock, closeDB := newMockGormRepository(t)
			defer closeDB()
			input := agenttasks.HandoffLinkAndEventInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", PayloadJSON: payload}
			if _, err := repo.CreateHandoffLinkAndEvent(testContext(), input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateHandoffLinkAndEvent() error = %v, want ErrInvalidInput", err)
			}
			assertExpectations(t, mock)
		})
	}
}

func TestGormRepositoryCreateHandoffLinkAndEventRejectsOwnedNonReadyTask(t *testing.T) {
	for _, status := range []string{agenttasks.StatusRunning, agenttasks.StatusCompleted, agenttasks.StatusFailed} {
		t.Run(status, func(t *testing.T) {
			repo, mock, closeDB := newMockGormRepository(t)
			defer closeDB()
			input := agenttasks.HandoffLinkAndEventInput{TenantID: 7, UserID: 11, TargetSessionID: 41, TargetTaskID: 42, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", PayloadJSON: testSessionHandoffEvent(t, 42)}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT .*parent_session_id.*status.* FROM `tenant_agent_tasks` .*FOR UPDATE").
				WithArgs(uint64(7), uint64(11), uint64(42), 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", "parent_session_id", "status"}).AddRow(42, 41, status))
			mock.ExpectRollback()
			if _, err := repo.CreateHandoffLinkAndEvent(testContext(), input); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("CreateHandoffLinkAndEvent() error = %v, want ErrInvalidState", err)
			}
			assertExpectations(t, mock)
		})
	}
}

func TestMySQLE2EConcurrentSessionHandoffIdentityCreatesOneEvent(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run the real InnoDB handoff concurrency contract")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	var engine string
	if err := repo.db.Raw("SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", "tenant_agent_task_events").Scan(&engine).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(engine, "InnoDB") {
		t.Fatalf("tenant_agent_task_events engine = %q, want InnoDB", engine)
	}

	key := fmt.Sprintf("handoff-e2e-%d", time.Now().UnixNano())
	var tenantID, userID, sessionID, taskID uint64
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = repo.with(cleanupCtx).Transaction(func(tx *gorm.DB) error {
			if taskID != 0 {
				tx.Exec("DELETE FROM tenant_agent_task_events WHERE tenant_id = ? AND user_id = ? AND task_id = ?", tenantID, userID, taskID)
			}
			if sessionID != 0 {
				tx.Exec("DELETE FROM tenant_session_links WHERE tenant_id = ? AND user_id = ? AND target_session_id = ?", tenantID, userID, sessionID)
			}
			if taskID != 0 {
				tx.Exec("DELETE FROM tenant_agent_tasks WHERE tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, taskID)
			}
			if sessionID != 0 {
				tx.Exec("DELETE FROM tenant_sessions WHERE tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, sessionID)
			}
			if userID != 0 {
				tx.Exec("DELETE FROM tenant_users WHERE tenant_id = ? AND id = ?", tenantID, userID)
			}
			if tenantID != 0 {
				tx.Exec("DELETE FROM tenants WHERE id = ?", tenantID)
			}
			return nil
		})
	})
	tenantID, err = repo.UpsertTenant(ctx, TenantInput{TenantKey: key, Name: key, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err = repo.EnsureUser(ctx, tenantID, key)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err = repo.UpsertSession(ctx, SessionInput{TenantID: tenantID, UserID: userID, SessionKey: key, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err = repo.CreateAgentTask(ctx, agenttasks.TaskInput{TenantID: tenantID, UserID: userID, ParentSessionID: sessionID, Status: agenttasks.StatusReady})
	if err != nil {
		t.Fatal(err)
	}
	payload := testSessionHandoffEvent(t, taskID)
	input := agenttasks.HandoffLinkAndEventInput{TenantID: tenantID, UserID: userID, TargetSessionID: sessionID, TargetTaskID: taskID, SourceKind: "tenant", SourceSessionKey: "source", RelationType: "handoff", LinkStatus: "active", CreatedByUserID: userID, PayloadJSON: payload}

	start := make(chan struct{})
	results := make([]agenttasks.HandoffLinkAndEventResult, 2)
	errorsByCall := make([]error, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errorsByCall[index] = repo.CreateHandoffLinkAndEvent(ctx, input)
		}(index)
	}
	close(start)
	wg.Wait()
	for index, callErr := range errorsByCall {
		if callErr != nil {
			t.Fatalf("concurrent call %d error = %v", index, callErr)
		}
	}
	if results[0].EventID == 0 || results[0].EventID != results[1].EventID || results[0].Replayed == results[1].Replayed {
		t.Fatalf("results = %#v, want one insert and one replay of the same event", results)
	}
	decoded, err := agenttasks.DecodeSessionHandoffEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	var eventCount int64
	if err := repo.db.Table("tenant_agent_task_events").Where("tenant_id = ? AND user_id = ? AND task_id = ? AND event_type = ? AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.package_sha256')) = ?", tenantID, userID, taskID, agenttasks.EventSessionHandoff, decoded.PackageSHA256).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("durable event count = %d, want 1", eventCount)
	}
	var linkCount int64
	if err := repo.db.Table("tenant_session_links").Where("tenant_id = ? AND user_id = ? AND target_session_id = ? AND source_kind = ? AND source_session_key = ? AND relation_type = ?", tenantID, userID, sessionID, "tenant", "source", agenttasks.SessionHandoffRelationType).Count(&linkCount).Error; err != nil {
		t.Fatal(err)
	}
	if linkCount != 1 {
		t.Fatalf("durable link count = %d, want 1", linkCount)
	}
}

func testSessionHandoffEvent(t *testing.T, targetTaskID uint64) string {
	t.Helper()
	packageJSON := testSessionHandoffPackageJSON(t)
	payload, err := agenttasks.EncodeSessionHandoffEvent(targetTaskID, packageJSON)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func testSessionHandoffEventForOperation(t *testing.T, targetTaskID uint64, sourceRef, cursor, operationIdentity string) string {
	t.Helper()
	payload, err := agenttasks.EncodeSessionHandoffEventForOperation(targetTaskID, testSessionHandoffPackageJSONForSource(t, sourceRef, cursor), "", 0, operationIdentity)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func testSessionHandoffEventWithOperationMetadata(t *testing.T, targetTaskID uint64, sourceRef, cursor, operationIdentity, operationMetadataJSON string) string {
	t.Helper()
	payload, err := agenttasks.EncodeSessionHandoffEventWithOperationMetadata(targetTaskID, testSessionHandoffPackageJSONForSource(t, sourceRef, cursor), "", 0, operationIdentity, operationMetadataJSON)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func testSessionControlOperationMetadata(t *testing.T, operation, keyHash, fingerprint string) string {
	t.Helper()
	operationPayload := struct {
		Schema      string `json:"schema"`
		Operation   string `json:"operation"`
		KeyHash     string `json:"key_hash"`
		Fingerprint string `json:"fingerprint"`
	}{"niuma.session-control-operation.v1", operation, keyHash, fingerprint}
	operationID := testJSONSHA256(t, operationPayload)
	encoded, err := json.Marshal(struct {
		Schema      string `json:"schema"`
		Operation   string `json:"operation"`
		OperationID string `json:"operation_id"`
		KeyHash     string `json:"key_hash"`
		Fingerprint string `json:"fingerprint"`
	}{"niuma.session-control-operation.v1", operation, operationID, keyHash, fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func testSessionHandoffPackageJSON(t *testing.T) string {
	return testSessionHandoffPackageJSONForSource(t, "tenant:source", "task_event:11")
}

func testSessionHandoffPackageJSONForSource(t *testing.T, sourceRef, cursor string) string {
	t.Helper()
	const schema = "niuma.session-handoff.v1"
	type source struct {
		Ref           string `json:"ref"`
		Cursor        string `json:"cursor"`
		ContentSHA256 string `json:"content_sha256,omitempty"`
	}
	canonicalSource := struct {
		Schema      string   `json:"schema"`
		Source      source   `json:"source"`
		Objective   string   `json:"objective"`
		Constraints []string `json:"constraints"`
		Stage       string   `json:"stage_summary"`
		Completed   []string `json:"completed"`
		OpenItems   []string `json:"open_items"`
		Risks       []string `json:"risks"`
		NextActions []string `json:"next_actions"`
		Evidence    []any    `json:"evidence"`
	}{schema, source{Ref: sourceRef, Cursor: cursor}, "continue", []string{}, "", []string{}, []string{}, []string{}, []string{}, []any{}}
	contentSHA := testJSONSHA256(t, canonicalSource)
	packageID := "handoff:" + sourceRef + ":" + cursor + ":" + contentSHA
	packageBody := struct {
		Schema      string         `json:"schema"`
		PackageID   string         `json:"package_id"`
		Source      source         `json:"source"`
		Target      map[string]any `json:"target"`
		Objective   string         `json:"objective"`
		Constraints []string       `json:"constraints"`
		Stage       string         `json:"stage_summary"`
		Completed   []string       `json:"completed"`
		OpenItems   []string       `json:"open_items"`
		Risks       []string       `json:"risks"`
		NextActions []string       `json:"next_actions"`
		Evidence    []any          `json:"evidence"`
		Budget      map[string]int `json:"budget"`
	}{schema, packageID, source{Ref: sourceRef, Cursor: cursor, ContentSHA256: contentSHA}, map[string]any{"ref": "tenant:target"}, "continue", []string{}, "", []string{}, []string{}, []string{}, []string{}, nil, map[string]int{"estimated_tokens": 2048, "limit_tokens": 2048}}
	packageSHA := testJSONSHA256(t, packageBody)
	wire := struct {
		Schema        string         `json:"schema"`
		PackageID     string         `json:"package_id"`
		PackageSHA256 string         `json:"package_sha256"`
		Source        map[string]any `json:"source"`
		Target        map[string]any `json:"target"`
		Objective     string         `json:"objective"`
		Constraints   []string       `json:"constraints"`
		Stage         string         `json:"stage_summary"`
		Completed     []string       `json:"completed"`
		OpenItems     []string       `json:"open_items"`
		Risks         []string       `json:"risks"`
		NextActions   []string       `json:"next_actions"`
		Evidence      []any          `json:"evidence"`
		Budget        map[string]int `json:"budget"`
	}{schema, packageID, packageSHA, map[string]any{"ref": sourceRef, "cursor": cursor, "content_sha256": contentSHA, "captured_at": "0001-01-01T00:00:00Z"}, map[string]any{"ref": "tenant:target"}, "continue", []string{}, "", []string{}, []string{}, []string{}, []string{}, nil, map[string]int{"estimated_tokens": 2048, "limit_tokens": 2048}}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func testJSONSHA256(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func TestGormRepositoryGoalLifecycle(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)

	mock.ExpectExec("INSERT INTO `tenant_goals`").
		WithArgs(uint64(1), uint64(2), sqlmock.AnyArg(), "ship feature", string(goal.StatusActive), "session-1", "/tmp/project", "claude", 3, 1000, 0, 0, 0, nil, 0, nil, nil, nil, nil, now, now).
		WillReturnResult(sqlmock.NewResult(10, 1))
	created, err := repo.CreateGoal(testContext(), 1, 2, goal.CreateInput{
		Objective:   "ship feature",
		SessionID:   "session-1",
		CWD:         "/tmp/project",
		Model:       "claude",
		TurnBudget:  3,
		TokenBudget: 1000,
		Now:         now,
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, goal_key, objective, status, COALESCE(session_id, ''), COALESCE(cwd, ''), COALESCE(model, ''), turn_budget, token_budget, turns_used, input_tokens, output_tokens, COALESCE(last_blocker, ''), repeated_blocker_count, COALESCE(last_checkpoint, ''), COALESCE(last_reason, ''), COALESCE(last_next_action, ''), COALESCE(error_message, ''), created_at, updated_at FROM `tenant_goals` WHERE tenant_id = ? AND user_id = ? AND goal_key = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), created.ID, 1).
		WillReturnRows(goalRows().AddRow(10, created.ID, "ship feature", goal.StatusBlocked, "session-1", "/tmp/project", "claude", 3, 1000, 2, 11, 22, "needs api", 3, "checkpoint-1", "blocked", "retry", "", now, now))
	got, err := repo.GetGoal(testContext(), 1, 2, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Status != goal.StatusBlocked {
		t.Fatalf("got = %+v", got)
	}

	got.Status = goal.StatusComplete
	got.UpdatedAt = now.Add(time.Minute)
	mock.ExpectExec("UPDATE `tenant_goals` SET .* WHERE tenant_id = \\? AND user_id = \\? AND goal_key = \\?").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(1), uint64(2), created.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateGoal(testContext(), 1, 2, got); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, goal_key, objective, status, COALESCE(session_id, ''), COALESCE(cwd, ''), COALESCE(model, ''), turn_budget, token_budget, turns_used, input_tokens, output_tokens, COALESCE(last_blocker, ''), repeated_blocker_count, COALESCE(last_checkpoint, ''), COALESCE(last_reason, ''), COALESCE(last_next_action, ''), COALESCE(error_message, ''), created_at, updated_at FROM `tenant_goals` WHERE (tenant_id = ? AND user_id = ?) AND status = ? ORDER BY updated_at DESC LIMIT ?")).
		WithArgs(uint64(1), uint64(2), string(goal.StatusComplete), 5).
		WillReturnRows(goalRows().AddRow(10, created.ID, "ship feature", goal.StatusComplete, "session-1", "/tmp/project", "claude", 3, 1000, 3, 11, 22, "", 0, "", "", "", "", now, got.UpdatedAt))
	list, err := repo.ListGoals(testContext(), 1, 2, goal.ListFilter{Status: goal.StatusComplete}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != goal.StatusComplete {
		t.Fatalf("list = %+v", list)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_goals` WHERE tenant_id = ? AND user_id = ? AND goal_key = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), created.ID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	mock.ExpectExec("INSERT INTO `tenant_goal_events`").
		WithArgs(uint64(1), uint64(2), uint64(10), "evt_test", string(goal.EventTurnFinished), "turn done", "session-1", 3, 11, 22, int64(123), "checkpoint-1", string(goal.StatusComplete), "done", "stop", nil, nil, now).
		WillReturnResult(sqlmock.NewResult(99, 1))
	event := goal.Event{ID: "evt_test", GoalID: created.ID, Type: goal.EventTurnFinished, Message: "turn done", SessionID: "session-1", Turn: 3, InputTokens: 11, OutputTokens: 22, DurationMS: 123, Checkpoint: "checkpoint-1", Status: goal.StatusComplete, Reason: "done", NextAction: "stop", CreatedAt: now}
	if err := repo.AppendGoalEvent(testContext(), 1, 2, event); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_goals` WHERE tenant_id = ? AND user_id = ? AND goal_key = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), created.ID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT event_key, event_type, COALESCE(message, ''), COALESCE(session_id, ''), turn_index, input_tokens, output_tokens, duration_ms, COALESCE(checkpoint, ''), COALESCE(status, ''), COALESCE(reason, ''), COALESCE(next_action, ''), COALESCE(blocker_key, ''), COALESCE(error_message, ''), created_at FROM `tenant_goal_events` WHERE tenant_id = ? AND user_id = ? AND goal_id = ? ORDER BY created_at ASC LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(10), 10).
		WillReturnRows(goalEventRows().AddRow("evt_test", goal.EventTurnFinished, "turn done", "session-1", 3, 11, 22, 123, "checkpoint-1", goal.StatusComplete, "done", "stop", "", "", now))
	events, err := repo.ListGoalEvents(testContext(), 1, 2, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].GoalID != created.ID || events[0].Type != goal.EventTurnFinished {
		t.Fatalf("events = %+v", events)
	}

	plan := goal.GoalPlan{
		GoalID:        created.ID,
		Version:       1,
		Summary:       "storage plan",
		CurrentStepID: "step_1",
		Steps: []goal.GoalStep{{
			ID:     "step_1",
			Title:  "Implement storage",
			Status: goal.StepStatusActive,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_goals` WHERE tenant_id = ? AND user_id = ? AND goal_key = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), created.ID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	mock.ExpectExec("INSERT INTO `tenant_goal_plans` .*ON DUPLICATE KEY UPDATE").
		WithArgs(uint64(1), uint64(2), uint64(10), 1, "storage plan", "step_1", sqlmock.AnyArg(), now, now).
		WillReturnResult(sqlmock.NewResult(50, 1))
	if err := repo.SaveGoalPlan(testContext(), 1, 2, plan); err != nil {
		t.Fatal(err)
	}

	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT g.goal_key, p.plan_json FROM tenant_goal_plans AS p INNER JOIN tenant_goals g ON g.id = p.goal_id WHERE p.tenant_id = ? AND p.user_id = ? AND g.goal_key = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), created.ID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"goal_key", "plan_json"}).AddRow(created.ID, string(planJSON)))
	gotPlan, ok, err := repo.GetGoalPlan(testContext(), 1, 2, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetGoalPlan ok=%v err=%v", ok, err)
	}
	if gotPlan.GoalID != created.ID || gotPlan.CurrentStepID != "step_1" || len(gotPlan.Steps) != 1 {
		t.Fatalf("plan = %+v", gotPlan)
	}

	exitCode := 0
	evidence := goal.GoalEvidence{ID: "ev_test", GoalID: created.ID, Type: goal.EvidenceTypeTest, Summary: "tests passed", Command: "go test ./internal/goal -count=1", ExitCode: &exitCode, Passed: true, Payload: []byte(`{"package":"internal/goal"}`), CreatedAt: now}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_goals` WHERE tenant_id = ? AND user_id = ? AND goal_key = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), created.ID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	mock.ExpectExec("INSERT INTO `tenant_goal_evidence`").
		WithArgs(uint64(1), uint64(2), uint64(10), "ev_test", string(goal.EvidenceTypeTest), "tests passed", "go test ./internal/goal -count=1", &exitCode, true, `{"package":"internal/goal"}`, now).
		WillReturnResult(sqlmock.NewResult(60, 1))
	if err := repo.AppendGoalEvidence(testContext(), 1, 2, evidence); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM `tenant_goals` WHERE tenant_id = ? AND user_id = ? AND goal_key = ? LIMIT ?")).
		WithArgs(uint64(1), uint64(2), created.ID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT evidence_key, evidence_type, summary, COALESCE(command, ''), exit_code, passed, COALESCE(payload_json, ''), created_at FROM `tenant_goal_evidence` WHERE tenant_id = ? AND user_id = ? AND goal_id = ? ORDER BY created_at ASC LIMIT ?")).
		WithArgs(uint64(1), uint64(2), uint64(10), 10).
		WillReturnRows(goalEvidenceRows().AddRow("ev_test", goal.EvidenceTypeTest, "tests passed", "go test ./internal/goal -count=1", 0, true, `{"package":"internal/goal"}`, now))
	evidenceList, err := repo.ListGoalEvidence(testContext(), 1, 2, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidenceList) != 1 || evidenceList[0].GoalID != created.ID || evidenceList[0].ExitCode == nil || *evidenceList[0].ExitCode != 0 {
		t.Fatalf("evidence = %+v", evidenceList)
	}
	assertExpectations(t, mock)
}

func newMockGormRepository(t *testing.T) (*GormRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return NewGormRepository(gormDB, nil), mock, func() { _ = db.Close() }
}

func TestGormRepositoryGetUserNotFound(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_id, user_key, email, display_name, role, status, user_info_json, metadata_json, updated_at FROM `tenant_users` WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL LIMIT ?")).
		WithArgs(uint64(1), uint64(2), 1).
		WillReturnError(sql.ErrNoRows)

	_, err := repo.GetUser(testContext(), 1, 2)
	if err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	assertExpectations(t, mock)
}

// stale reaper 的两条查询必须自带租户边界：列表跨租户扫，但写回按
// (tenant_id, user_id, id, status=running) 精确匹配。
func TestGormRepositoryListStaleRunningAgentTasks(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	cutoff := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	startedAt := cutoff.Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE status = ? AND started_at < ? ORDER BY started_at ASC LIMIT ?")).
		WithArgs(agenttasks.StatusRunning, cutoff, 50).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name",
			"description", "status", "model", "result_json", "metadata_json", "trace_id", "started_at", "finished_at",
		}).AddRow(7, 10, 100, 0, "", "web-agent", "", agenttasks.StatusRunning, "claude-test", "", "", "trace-a", startedAt, nil))

	tasks, err := repo.ListStaleRunningAgentTasks(testContext(), cutoff, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != 7 || tasks[0].TenantID != 10 || tasks[0].UserID != 100 {
		t.Fatalf("tasks = %+v, want the row to carry its tenant/user ownership", tasks)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryFailStaleAgentTaskIsTenantScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .* WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND status = \\?").
		WithArgs(sqlmock.AnyArg(), `{"stale":true}`, agenttasks.StatusFailed, uint64(10), uint64(100), uint64(7), agenttasks.StatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.FailStaleAgentTask(testContext(), 10, 100, 7, `{"stale":true}`); err != nil {
		t.Fatal(err)
	}

	// 任务已被真正的 runner 收尾（status 不再是 running）→ ErrNotFound，不覆盖结果。
	mock.ExpectExec("UPDATE `tenant_agent_tasks` SET .* WHERE tenant_id = \\? AND user_id = \\? AND id = \\? AND status = \\?").
		WithArgs(sqlmock.AnyArg(), `{"stale":true}`, agenttasks.StatusFailed, uint64(10), uint64(100), uint64(7), agenttasks.StatusRunning).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.FailStaleAgentTask(testContext(), 10, 100, 7, `{"stale":true}`); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}
