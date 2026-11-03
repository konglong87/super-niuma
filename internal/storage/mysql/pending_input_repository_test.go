package mysql

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/konglong87/super-niuma/internal/pendinginput"
)

func TestFindPendingInputByClientIDIsTenantUserGlobalAndIncludesTerminal(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_pending_inputs` WHERE tenant_id = \\? AND user_id = \\? AND client_input_id = \\?.*LIMIT \\?").
		WithArgs(uint64(7), uint64(11), "key", 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id", "session_id", "base_task_id", "client_input_id", "content", "direction", "attachments_json", "attempt", "sequence_no", "status", "dispatched_task_id", "error_code", "error_message", "claimed_at", "created_at", "updated_at"}).
			AddRow(5, 7, 11, "41", 51, "key", "content", nil, `[]`, 0, 1, pendinginput.StatusSent, 77, nil, nil, now, now, now))
	item, ok, err := repo.FindByClientInputID(testContext(), 7, 11, "key")
	if err != nil || !ok || item.Status != pendinginput.StatusSent || item.DispatchedTaskID != 77 || item.SessionID != "41" {
		t.Fatalf("item=%+v ok=%v err=%v", item, ok, err)
	}
	assertExpectations(t, mock)
}

func TestListActivePendingSessionStatesUsesOneTenantScopedQuery(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM `tenant_agent_task_pending_inputs` WHERE tenant_id = \\? AND user_id = \\? AND session_id IN \\(\\?,\\?\\) AND status NOT IN \\(\\?, ?\\?\\)").
		WithArgs(uint64(7), uint64(11), "41", "42", pendinginput.StatusSent, pendinginput.StatusCancelled).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "base_task_id", "base_count", "count"}).AddRow("41", 51, 1, 2).AddRow("42", 61, 1, 1))
	states, err := repo.ListActiveSessionStates(testContext(), 7, 11, []string{"41", "42"})
	if err != nil || states["41"].Count != 2 || states["41"].BaseTaskID != 51 || states["42"].Count != 1 {
		t.Fatalf("states=%+v err=%v", states, err)
	}
	assertExpectations(t, mock)
}
