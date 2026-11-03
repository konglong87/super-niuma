package mysql

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	selectMessagesSQL = "SELECT id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at FROM `tenant_session_messages` WHERE tenant_id = ? AND user_id = ? AND session_id = ? ORDER BY turn_index ASC LIMIT ?"
	selectMaxTurnSQL  = "SELECT COALESCE(MAX(turn_index), 0) FROM `tenant_session_messages` WHERE tenant_id = ? AND user_id = ? AND session_id = ?"

	// 定点读法生成的 SQL。这些断言的意义是「查询形状对不对」：GetMessage 确实按 id
	// 定点查而不是拉一页回来筛，倒序读法确实是 ORDER BY turn_index DESC LIMIT n，
	// 而且三条的 WHERE 里都还带着 tenant_id / user_id —— 归属校验没有随着换查询丢掉。
	//
	// 边界：sqlmock 只回放我们给它的语句、不校验语法，也没有 MySQL 语义。
	// 所以这里能证明「我们发出的 SQL 是这个形状」，证明不了「这条 SQL 在 MySQL 上跑得通」。
	selectMessageByIDSQL      = "SELECT id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at FROM `tenant_session_messages` WHERE tenant_id = ? AND user_id = ? AND session_id = ? AND id = ? LIMIT ?"
	selectPreviousUserMessage = "SELECT id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at FROM `tenant_session_messages` WHERE tenant_id = ? AND user_id = ? AND session_id = ? AND role = ? AND turn_index < ? ORDER BY turn_index DESC LIMIT ?"
	selectRecentMessagesSQL   = "SELECT id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at FROM `tenant_session_messages` WHERE tenant_id = ? AND user_id = ? AND session_id = ? ORDER BY turn_index DESC LIMIT ?"

	// message_key 的定点查询比的是 000010 那个生成列，不是 JSON 路径表达式 ——
	// 后者用不上索引，每次幂等检查都要扫完整个会话再逐行解 JSON。
	// role 是必须的判别条件：同一个 message_key 会同时落在一条 user 和一条 assistant 上。
	selectMessageByKeySQL = "SELECT id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at FROM `tenant_session_messages` WHERE tenant_id = ? AND user_id = ? AND session_id = ? AND role = ? AND mobile_message_key = ? LIMIT ?"
)

// messageRowsAtTurns 按给定顺序造行，用来模拟数据库真按 ORDER BY 返回的顺序。
func messageRowsAtTurns(role string, turns ...int) *sqlmock.Rows {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "session_id", "turn_index", "role", "content", "content_json", "tool_id", "tool_name", "is_error", "model", "input_tokens", "output_tokens", "trace_id", "created_at"})
	for _, turn := range turns {
		rows.AddRow(turn, 5, turn, role, "msg", nil, nil, nil, false, "claude", 0, 0, nil, now)
	}
	return rows
}

func messageRowsForList(count int) *sqlmock.Rows {
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "session_id", "turn_index", "role", "content", "content_json", "tool_id", "tool_name", "is_error", "model", "input_tokens", "output_tokens", "trace_id", "created_at"})
	for i := 0; i < count; i++ {
		rows.AddRow(i+1, 5, i+1, "user", "msg", nil, nil, nil, false, "claude", 0, 0, nil, now)
	}
	return rows
}

// ListAllMessages 要么给出全部消息，要么报错 —— 绝不返回一个被静默截断的切片。
// 这是 ListMessages 的反面：那条走 normalizeLimit，超过 500 会静默夹掉尾巴。
func TestGormRepositoryListAllMessagesReturnsEveryRow(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	// 多取一条用来判断「还有更多」，所以 LIMIT 是 cap+1。
	mock.ExpectQuery(regexp.QuoteMeta(selectMessagesSQL)).
		WithArgs(uint64(1), uint64(2), uint64(5), 621).
		WillReturnRows(messageRowsForList(620))

	messages, err := repo.ListAllMessages(testContext(), 1, 2, 5, 620)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 620 {
		t.Fatalf("got %d messages, want 620", len(messages))
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryListAllMessagesRejectsMoreThanTheCap(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectMessagesSQL)).
		WithArgs(uint64(1), uint64(2), uint64(5), 11).
		WillReturnRows(messageRowsForList(11))

	if _, err := repo.ListAllMessages(testContext(), 1, 2, 5, 10); !errors.Is(err, ErrTooManyMessages) {
		t.Fatalf("err = %v, want ErrTooManyMessages", err)
	}
	assertExpectations(t, mock)
}

// 最大轮次必须问数据库，不能靠扫一个长度受限的列表 —— 后者在超过 500 条的会话里
// 会算出一个已经存在的轮次，而写入按 (session_id, turn_index) 唯一键 upsert，
// 于是新消息覆盖旧消息。
func TestGormRepositoryMaxMessageTurnAsksTheDatabase(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectMaxTurnSQL)).
		WithArgs(uint64(1), uint64(2), uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"COALESCE(MAX(turn_index), 0)"}).AddRow(620))

	turn, err := repo.MaxMessageTurn(testContext(), 1, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if turn != 620 {
		t.Fatalf("turn = %d, want 620", turn)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryMaxMessageTurnIsZeroForAnEmptySession(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectMaxTurnSQL)).
		WithArgs(uint64(1), uint64(2), uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"COALESCE(MAX(turn_index), 0)"}).AddRow(0))

	turn, err := repo.MaxMessageTurn(testContext(), 1, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if turn != 0 {
		t.Fatalf("turn = %d, want 0", turn)
	}
	assertExpectations(t, mock)
}

// GetMessage 必须是按 id 的定点查询，不是「拉一页回来再筛」。cancel/regenerate 只要
// 一条消息，而 ListMessages 的 limit 会被 normalizeLimit 静默夹到 500，于是超过
// 500 条的会话里对一条真实存在的消息报 404（TODO-117）。
func TestGormRepositoryGetMessageQueriesByIDWithinTheOwner(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectMessageByIDSQL)).
		WithArgs(uint64(1), uint64(2), uint64(5), uint64(600), 1).
		WillReturnRows(messageRowsAtTurns("assistant", 600))

	message, err := repo.GetMessage(testContext(), 1, 2, 5, 600)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != 600 || message.TurnIndex != 600 {
		t.Fatalf("message = %+v", message)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryGetMessageReportsNotFound(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectMessageByIDSQL)).
		WithArgs(uint64(1), uint64(2), uint64(5), uint64(600), 1).
		WillReturnRows(messageRowsAtTurns("assistant"))

	if _, err := repo.GetMessage(testContext(), 1, 2, 5, 600); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

// regenerate 还要目标 assistant 前面那条 user 消息。数据库按 turn_index DESC 返回，
// 取第一条就是「600 之前最后一条」。
func TestGormRepositoryPreviousUserMessageTakesTheNearestEarlierTurn(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectPreviousUserMessage)).
		WithArgs(uint64(1), uint64(2), uint64(5), "user", uint(600), 1).
		WillReturnRows(messageRowsAtTurns("user", 599))

	message, err := repo.PreviousUserMessage(testContext(), 1, 2, 5, 600)
	if err != nil {
		t.Fatal(err)
	}
	if message.TurnIndex != 599 {
		t.Fatalf("message = %+v", message)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryPreviousUserMessageReportsNotFound(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectPreviousUserMessage)).
		WithArgs(uint64(1), uint64(2), uint64(5), "user", uint(1), 1).
		WillReturnRows(messageRowsAtTurns("user"))

	if _, err := repo.PreviousUserMessage(testContext(), 1, 2, 5, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	assertExpectations(t, mock)
}

// TODO-119：求「最新 recap」要倒着读。ListMessages 是 ORDER BY turn_index ASC 且被
// 夹到 500，求出来的是「最旧 500 条里的最新」。
func TestGormRepositoryListRecentMessagesReadsTheNewestDescending(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	// 数据库按 DESC 返回最近三条。
	mock.ExpectQuery(regexp.QuoteMeta(selectRecentMessagesSQL)).
		WithArgs(uint64(1), uint64(2), uint64(5), 3).
		WillReturnRows(messageRowsAtTurns("user", 620, 619, 618))

	messages, err := repo.ListRecentMessages(testContext(), 1, 2, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 返回前翻回升序：本仓储的 []Message 一律按 turn_index 升序，
	// mobileLatestRecapFromMessages 这类从尾部往前找的代码依赖这个不变量。
	if len(messages) != 3 || messages[0].TurnIndex != 618 || messages[2].TurnIndex != 620 {
		t.Fatalf("messages = %+v", messages)
	}
	assertExpectations(t, mock)
}

// TODO-118：message_key 的幂等重放必须是定点查询。原先是 ListMessages(…, 1000) 再
// 在返回切片里线性查找 message_key，而 normalizeLimit 把 limit 静默夹到 500 —— 于是
// 长会话里客户端重试**找不到**已有记录，重新跑一次查询、重新写一条消息、重新计一次量。
//
// 边界与本文件其他 sqlmock 断言相同：这里证明的是「我们发出的 SQL 是这个形状」，
// 证明不了这条 SQL 在 MySQL 上跑得通、更证明不了它真的走了索引 —— 生成列和索引都在
// 000010 那个**没有真跑过**的 migration 里。
func TestGormRepositoryMessageByKeyQueriesTheGeneratedColumn(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectMessageByKeySQL)).
		WithArgs(uint64(1), uint64(2), uint64(5), "assistant", "replay-1", 1).
		WillReturnRows(messageRowsAtTurns("assistant", 622))

	message, found, err := repo.MessageByKey(testContext(), 1, 2, 5, "assistant", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	if !found || message.TurnIndex != 622 {
		t.Fatalf("found=%v message=%+v", found, message)
	}
	assertExpectations(t, mock)
}

// 查不到不是错误：第一次请求本来就不是重放。所以这条返回 found=false 而不是
// ErrNotFound —— 调用方要区分「没有重放」和「查询失败」，后者绝不能被当成前者
// 放过去（那就等于在数据库出问题时静默重复执行一次）。
func TestGormRepositoryMessageByKeyReportsMissAsNotFound(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	mock.ExpectQuery(regexp.QuoteMeta(selectMessageByKeySQL)).
		WithArgs(uint64(1), uint64(2), uint64(5), "assistant", "never-seen", 1).
		WillReturnRows(messageRowsAtTurns("assistant"))

	message, found, err := repo.MessageByKey(testContext(), 1, 2, 5, "assistant", "never-seen")
	if err != nil {
		t.Fatal(err)
	}
	if found || message.ID != 0 {
		t.Fatalf("found=%v message=%+v", found, message)
	}
	assertExpectations(t, mock)
}

// 空 key 不许查：生成列在没有 $.mobile.message_key 的行上是 NULL，但
// mobileMetadataJSON 在 message_key 为空时写的是空字符串，空 key 一查就会撞上它们。
func TestGormRepositoryMessageByKeyRejectsAnEmptyKey(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	if _, _, err := repo.MessageByKey(testContext(), 1, 2, 5, "assistant", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	assertExpectations(t, mock)
}

func TestGormRepositoryListRecentMessagesRejectsNonPositiveLimit(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()

	if _, err := repo.ListRecentMessages(testContext(), 1, 2, 5, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	assertExpectations(t, mock)
}
