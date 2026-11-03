package mysql

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestListAgentProfileConversationSummariesScopesProfileAndBinding(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.id AS conversation_id")).WithArgs(uint64(1), uint64(11), 50).WillReturnRows(sqlmock.NewRows([]string{"conversation_id", "session_id", "account_id", "account_key", "external_chat_id", "external_thread_id", "chat_type", "conversation_status", "title", "model", "last_message_at", "last_inbound_at", "last_outbound_at", "message_count", "run_count", "latest_run_status", "last_message_role", "last_message_preview"}).AddRow(7, 31, 2, "copywriter-feishu", "oc_group", "", "group", "active", "Feishu Team", "glm-5.1", now, now, now, 8, 3, "completed", "assistant", "preview"))
	items, err := repo.ListAgentProfileConversationSummaries(testContext(), 1, 11, 50)
	if err != nil || len(items) != 1 || items[0].AccountKey != "copywriter-feishu" || items[0].MessageCount != 8 || items[0].LatestRunStatus != "completed" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	assertExpectations(t, mock)
}

func TestListAgentProfileTeamLinksScopesProfile(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.id AS team_id")).WithArgs(uint64(1), uint64(11), 50).WillReturnRows(sqlmock.NewRows([]string{"team_id", "team_key", "team_version", "team_display_name", "member_key", "role", "account_id", "account_key", "external_chat_id", "trigger_policy", "status"}).AddRow(4, "launch", 1, "Launch Team", "copywriter", "coordinator", 2, "copywriter-feishu", "oc_group", "mention", "active"))
	items, err := repo.ListAgentProfileTeamLinks(testContext(), 1, 11, 50)
	if err != nil || len(items) != 1 || items[0].TeamKey != "launch" || items[0].Role != "coordinator" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	assertExpectations(t, mock)
}
