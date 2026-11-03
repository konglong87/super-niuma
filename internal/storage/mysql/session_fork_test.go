package mysql

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	driver "github.com/go-sql-driver/mysql"
)

func testForkSessionInput(messages int) ForkSessionInput {
	input := ForkSessionInput{
		Session: SessionInput{
			TenantID:   1,
			UserID:     2,
			SessionKey: "branch-a",
			Title:      "Source branch",
			Model:      "claude",
		},
	}
	for i := 0; i < messages; i++ {
		input.Messages = append(input.Messages, MessageInput{
			TurnIndex: uint(i + 1),
			Role:      "user",
			Content:   "message",
			Model:     "claude",
		})
	}
	return input
}

func expectForkOwnershipCheck(mock sqlmock.Sqlmock) {
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta(selectSessionSQL)).
		WithArgs(uint64(1), uint64(2), uint64(9), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}).
			AddRow(9, "branch-a", "Source branch", "active", "claude", "/workspace", now, now))
}

// 分支会话的整批复制必须原子：中途一条消息失败，新建的会话行也不能留下。
// 这是 TODO-106 的核心断言 —— 半写分支会话比 /query 那条更糟，因为条数不定。
func TestGormRepositoryForkSessionRollsBackWhenAMessageCopyFails(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	expectForkOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(21, 1))
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(22, 1))
	mock.ExpectExec(insertMessageSQL).WillReturnError(errors.New("third message insert failed"))
	mock.ExpectRollback()

	if _, err := repo.ForkSession(testContext(), testForkSessionInput(4)); err == nil {
		t.Fatal("expected error")
	}
	// ExpectationsWereMet 只有在驱动真的发过 ROLLBACK 时才通过：
	// 前两条消息和会话行一起回滚，不会留下只复制了一半的分支会话。
	assertExpectations(t, mock)
}

func TestGormRepositoryForkSessionCommitsSessionAndEveryMessage(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	expectForkOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).
		WithArgs(uint64(1), uint64(2), uint64(9), uint(1), "user", "message", nil, nil, nil, false, "claude", uint(0), uint(0), "trace-1").
		WillReturnResult(sqlmock.NewResult(21, 1))
	mock.ExpectExec(insertMessageSQL).
		WithArgs(uint64(1), uint64(2), uint64(9), uint(2), "user", "message", nil, nil, nil, false, "claude", uint(0), uint(0), "trace-1").
		WillReturnResult(sqlmock.NewResult(22, 1))
	mock.ExpectCommit()

	out, err := repo.ForkSession(testContext(), testForkSessionInput(2))
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != 9 || out.CopiedMessages != 2 {
		t.Fatalf("out = %+v", out)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryForkSessionRejectsForeignSession(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	// 归属校验失败同样要整体回滚：事务化不得把 2026-06-26 数据隔离 review 加的
	// 这道校验绕过去（与 TestGormRepositorySaveQueryTurnRejectsForeignSession 同构）。
	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectQuery(regexp.QuoteMeta(selectSessionSQL)).
		WithArgs(uint64(1), uint64(2), uint64(9), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_key", "title", "status", "model", "cwd", "started_at", "last_message_at"}))
	mock.ExpectRollback()

	if _, err := repo.ForkSession(testContext(), testForkSessionInput(2)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryForkSessionRetriesWholeTransactionOnDeadlock(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	// 死锁重试必须重跑整个事务：回滚后单独重发一条消息，会把它挂到一个已经不
	// 存在的分支会话上。
	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	expectForkOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(21, 1))
	mock.ExpectExec(insertMessageSQL).WillReturnError(&driver.MySQLError{Number: 1213, Message: "deadlock found"})
	mock.ExpectRollback()

	mock.ExpectBegin()
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	expectForkOwnershipCheck(mock)
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(21, 1))
	mock.ExpectExec(insertMessageSQL).WillReturnResult(sqlmock.NewResult(22, 1))
	mock.ExpectCommit()

	out, err := repo.ForkSession(testContext(), testForkSessionInput(2))
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionID != 9 || out.CopiedMessages != 2 {
		t.Fatalf("out = %+v", out)
	}
	assertExpectations(t, mock)
}

// 条数上限是显式的、读写共用的一个数，而不是从 normalizeLimit 的 500 夹取里继承来
// 的巧合。超限报 ErrTooManyMessages（而不是含糊的 error），端点才能把它映射成一个
// 明确的 4xx，而不是让调用方以为 fork 成功了。
func TestGormRepositoryForkSessionRejectsTooManyMessages(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	// 超限在开事务之前就拒掉，所以一条 SQL 都不该发出去。
	if _, err := repo.ForkSession(testContext(), testForkSessionInput(MaxForkedMessages+1)); !errors.Is(err, ErrTooManyMessages) {
		t.Fatalf("err = %v, want ErrTooManyMessages", err)
	}
	assertExpectations(t, mock)
}
