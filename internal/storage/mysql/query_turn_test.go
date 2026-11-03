package mysql

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	driver "github.com/go-sql-driver/mysql"
)

const (
	insertSessionSQL = "INSERT INTO `tenant_sessions` .*ON DUPLICATE KEY UPDATE .*LAST_INSERT_ID"
	insertMessageSQL = "INSERT INTO `tenant_session_messages` .*ON DUPLICATE KEY UPDATE .*LAST_INSERT_ID"
	selectSessionSQL = "SELECT id, session_key, title, status, model, cwd, started_at, last_message_at FROM `tenant_sessions` WHERE tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL LIMIT ?"
)

func testQueryTurnInput() QueryTurnInput {
	return QueryTurnInput{
		Session: SessionInput{
			TenantID:   1,
			UserID:     2,
			SessionKey: "session-a",
			Title:      "Title",
			Model:      "claude",
		},
		User: MessageInput{
			TurnIndex: 1,
			Role:      "user",
			Content:   "hello",
			Model:     "claude",
		},
		Assistant: MessageInput{
			TurnIndex: 2,
			Role:      "assistant",
			Content:   "hi",
			Model:     "claude",
		},
	}
}

func expectSessionOwnershipCheck(mock sqlmock.Sqlmock) {
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta(selectSessionSQL)).
		WithArgs(uint64(1), uint64(2), uint64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}).
			AddRow(7, "session-a", "Title", "active", "claude", "/workspace", now, now))
}

func TestGormRepositorySaveQueryTurnRollsBackWhenUserMessageFails(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(7, 1))
	expectSessionOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).WillReturnError(errors.New("user message insert failed"))
	mock.ExpectRollback()

	if _, err := repo.SaveQueryTurn(testContext(), testQueryTurnInput()); err == nil {
		t.Fatal("expected error")
	}
	// ExpectationsWereMet 只有在驱动真的发过 ROLLBACK 时才通过：
	// 会话行随事务一起回滚，不会留下半写会话。
	assertExpectations(t, mock)
}

func TestGormRepositorySaveQueryTurnRollsBackWhenAssistantMessageFails(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(7, 1))
	expectSessionOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectExec(insertMessageSQL).WillReturnError(errors.New("assistant message insert failed"))
	mock.ExpectRollback()

	if _, err := repo.SaveQueryTurn(testContext(), testQueryTurnInput()); err == nil {
		t.Fatal("expected error")
	}
	assertExpectations(t, mock)
}

func TestGormRepositorySaveQueryTurnRejectsForeignSession(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	// 归属校验失败（会话不属于本租户本用户）同样要整体回滚，
	// 事务化不得把 2026-06-26 数据隔离 review 加的这道校验绕过去。
	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectQuery(regexp.QuoteMeta(selectSessionSQL)).
		WithArgs(uint64(1), uint64(2), uint64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}))
	mock.ExpectRollback()

	if _, err := repo.SaveQueryTurn(testContext(), testQueryTurnInput()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositorySaveQueryTurnCommitsAllThreeRows(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(7, 1))
	expectSessionOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).
		WithArgs(uint64(1), uint64(2), uint64(7), uint(1), "user", "hello", nil, nil, nil, false, "claude", uint(0), uint(0), "trace-1").
		WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectExec(insertMessageSQL).
		WithArgs(uint64(1), uint64(2), uint64(7), uint(2), "assistant", "hi", nil, nil, nil, false, "claude", uint(0), uint(0), "trace-1").
		WillReturnResult(sqlmock.NewResult(12, 1))
	mock.ExpectCommit()

	out, err := repo.SaveQueryTurn(testContext(), testQueryTurnInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != 7 || out.UserMessageID != 11 || out.AssistantMessageID != 12 {
		t.Fatalf("out = %+v", out)
	}
	assertExpectations(t, mock)
}

func TestGormRepositorySaveQueryTurnRetriesWholeTransactionOnDeadlock(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	// 死锁重试必须重跑整个事务，而不是事务内的单条语句：
	// 第二次尝试要重新 BEGIN 并重新写会话行，否则消息会挂在一个已回滚的会话上。
	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(7, 1))
	expectSessionOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).WillReturnError(&driver.MySQLError{Number: 1213, Message: "deadlock found"})
	mock.ExpectRollback()

	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(7, 1))
	expectSessionOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(12, 1))
	mock.ExpectCommit()

	out, err := repo.SaveQueryTurn(testContext(), testQueryTurnInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != 7 || out.UserMessageID != 11 || out.AssistantMessageID != 12 {
		t.Fatalf("out = %+v", out)
	}
	assertExpectations(t, mock)
}
