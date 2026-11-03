package mysql

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/konglong87/super-niuma/internal/agenttasks"
)

func TestProjectSessionMessagesCarriesMessageRoles(t *testing.T) {
	events := projectSessionMessages([]Message{
		{ID: 7, Role: "user", Content: "question"},
		{ID: 8, Role: "assistant", Content: "answer"},
	})
	if len(events) != 4 {
		t.Fatalf("projected event count = %d, want 4", len(events))
	}
	var userPayload, assistantPayload map[string]any
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &userPayload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(events[2].PayloadJSON), &assistantPayload); err != nil {
		t.Fatal(err)
	}
	if userPayload["role"] != "user" || assistantPayload["role"] != "assistant" {
		t.Fatalf("projected roles = %#v, %#v", userPayload["role"], assistantPayload["role"])
	}
	if events[0].EventType != agenttasks.EventMessage || events[2].EventType != agenttasks.EventTextDelta {
		t.Fatalf("projected event types = %q, %q", events[0].EventType, events[2].EventType)
	}
}

func TestWebConversationTasksScopeLegacyMembershipBeforeApplyingLimit(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	where := "WHERE (tenant_id = ? AND user_id = ?) AND (parent_session_id = ? OR ((parent_session_id IS NULL OR parent_session_id = 0) AND JSON_UNQUOTE(JSON_EXTRACT(metadata_json, '$.web_agent_session_id')) = ?)) ORDER BY started_at DESC, id DESC LIMIT ?"
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` "+regexp.QuoteMeta(where)).
		WithArgs(uint64(7), uint64(11), uint64(41), "41", 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "started_at", "finished_at"}).
			AddRow(44, 7, 11, 0, "", agenttasks.AgentNameWeb, "", agenttasks.StatusCompleted, "model", "{}", `{"web_agent_session_id":"41"}`, "", time.Now(), time.Now()).
			AddRow(43, 7, 11, 41, "", "reviewer", "", agenttasks.StatusCompleted, "model", "{}", "{}", "", time.Now(), time.Now()).
			AddRow(42, 7, 11, 41, "", agenttasks.AgentNameWeb, "", agenttasks.StatusCompleted, "model", "{}", "{}", "", time.Now(), time.Now()))
	tasks, err := repo.ListWebAgentConversationTasks(testContext(), 7, 11, 41, 3)
	if err != nil || len(tasks) != 3 || tasks[0].ID != 44 || tasks[1].AgentName != "reviewer" {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	assertExpectations(t, mock)
}

func TestConversationEventsRequireOwnedUnarchivedSessionAndCursor(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(`SELECT .* FROM tenant_agent_task_events AS e JOIN tenant_agent_tasks AS t ON t.id = e.task_id AND t.tenant_id = e.tenant_id AND t.user_id = e.user_id JOIN tenant_sessions AS s ON s.id = t.parent_session_id AND s.tenant_id = t.tenant_id AND s.user_id = t.user_id WHERE e.tenant_id = \? AND e.user_id = \? AND s.id = \? AND s.archived_at IS NULL AND t.agent_name = \? AND e.id > \? ORDER BY e.id ASC LIMIT \?`).
		WithArgs(uint64(7), uint64(11), uint64(41), agenttasks.AgentNameWeb, uint64(50), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "event_type", "payload_json", "trace_id", "created_at"}).AddRow(51, 42, agenttasks.EventFailed, `{"error":"test failure"}`, "trace", time.Now()))
	events, err := repo.ListSessionConversationEvents(testContext(), 7, 11, 41, 50, 200)
	if err != nil || len(events) != 1 || events[0].ID != 51 || events[0].EventType != agenttasks.EventFailed {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	assertExpectations(t, mock)
}

func TestConversationHistoryTasksAreScopedBeforeApplyingLimit(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_tasks` WHERE tenant_id = \\? AND user_id = \\? AND parent_session_id = \\? AND agent_name = \\? ORDER BY started_at DESC, id DESC LIMIT \\?").
		WithArgs(uint64(7), uint64(11), uint64(41), agenttasks.AgentNameWeb, 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "parent_session_id", "subagent_session_key", "agent_name", "description", "status", "model", "result_json", "metadata_json", "trace_id", "started_at", "finished_at"}).AddRow(42, 7, 11, 41, "", agenttasks.AgentNameWeb, "", agenttasks.StatusCompleted, "model", "{}", "{}", "trace", time.Now(), time.Now()))
	tasks, err := repo.ListSessionConversationTasks(testContext(), 7, 11, 41, 200)
	if err != nil || len(tasks) != 1 || tasks[0].ParentSessionID != 41 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	assertExpectations(t, mock)
}

func TestConversationQueriesRejectMissingScopeBeforeDatabase(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	for _, scope := range [][3]uint64{{0, 11, 41}, {7, 0, 41}, {7, 11, 0}} {
		_, err := repo.ListSessionConversationEvents(testContext(), scope[0], scope[1], scope[2], 0, 200)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("events scope=%v err=%v", scope, err)
		}
		_, err = repo.ListSessionConversationTasks(testContext(), scope[0], scope[1], scope[2], 200)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("tasks scope=%v err=%v", scope, err)
		}
		_, err = repo.ListWebAgentConversationTasks(testContext(), scope[0], scope[1], scope[2], 200)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("web tasks scope=%v err=%v", scope, err)
		}
	}
	assertExpectations(t, mock)
}
