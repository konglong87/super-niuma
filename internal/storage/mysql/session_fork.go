package mysql

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/konglong87/super-niuma/internal/observability"
)

// MaxForkedMessages 是一次 fork 能复制的消息条数上限，读侧和写侧共用同一个数。
//
// 上限必须显式，而不是从读路径的夹取里"继承"一个：原先 fork 拿
// ListMessages(…, 1000) 当"全部消息"用，而 normalizeLimit 会把它静默夹到 500，
// 于是超过 500 条的会话被复制成一个悄悄缺了尾巴的分支，还照样返回 200 和
// copied_messages: 500（TODO-115）。现在读侧走 ListAllMessages(…, MaxForkedMessages)，
// 超限时报 ErrTooManyMessages，端点响亮地失败。
//
// 为什么是 5000 而不是继续用 500：500 是列表分页的读上限，跟"一次 fork 能有多大"
// 没有关系，沿用它会让超过 500 条的会话直接不能 fork。5000 比常见移动端会话高两个
// 数量级，同时仍然给单个事务封了顶 —— 持锁时间随条数线性增长，还要顾及
// max_allowed_packet 和 innodb_log_file_size。
//
// 为什么不分批：分批写会重新打开「半写会话」这个正要修掉的窗口（TODO-106）。
// 真正能去掉这道上限的做法是把复制下推成库内的一条 INSERT ... SELECT，本仓暂不做
// —— 本机没有 MySQL 也没有能跑 MySQL 方言的进程内引擎，sqlmock 只回放我们发出的
// 语句、不校验语法，手写的 INSERT ... SELECT 在提交前无法验证，而它一旦有错就是
// 整个 fork 端点不可用。
const MaxForkedMessages = 5000

// ForkSessionInput 是一次会话 fork 要落库的整批行：新建的分支会话 + 逐条复制的消息。
// 消息的 TenantID/UserID/SessionID 由仓储在写入时统一填成分支会话那一行的值，调用方不填。
type ForkSessionInput struct {
	Session  SessionInput
	Messages []MessageInput
}

type ForkSessionResult struct {
	SessionID      uint64
	CopiedMessages int
}

// ForkSession 把「建分支会话 + 复制 N 条消息」收进一个显式事务：要么整条分支都在，
// 要么一行都不留，不会留下只复制了一半消息的分支会话（TODO-106）。
//
// 与 SaveQueryTurn 同构，包括三处刻意的选择：
//   - 仓储全局设了 SkipDefaultTransaction，那是刻意的性能选择；这里只给这一条
//     需要原子性的路径开事务，不动全局默认值。
//   - 死锁重试包在事务外层：重试的是整个事务，而不是事务内的单条语句 —— 回滚后
//     再单独重发一条消息，会把它挂到一个已经不存在的分支会话上。
//   - 归属校验走事务内的 getSessionTx，不得因为事务化而被绕过。
func (r *GormRepository) ForkSession(ctx context.Context, input ForkSessionInput) (ForkSessionResult, error) {
	r.log(ctx, "session.fork", "mysql.GormRepository.ForkSession", "fork tenant session")
	if len(input.Messages) > MaxForkedMessages {
		return ForkSessionResult{}, fmt.Errorf("%w: cannot fork %d messages in one transaction (limit %d)", ErrTooManyMessages, len(input.Messages), MaxForkedMessages)
	}
	traceID := observability.TraceID(ctx)
	var out ForkSessionResult
	err := withRetryableTransaction(ctx, func() error {
		out = ForkSessionResult{}
		return r.with(ctx).Transaction(func(tx *gorm.DB) error {
			sessionID, err := upsertSessionTx(tx, input.Session)
			if err != nil {
				return err
			}
			// 分支会话行刚在本事务里按 (tenant_id, user_id, session_key) 唯一键写过，
			// 这里在事务内复查一次，既覆盖归档会话，也保证复制来的消息只挂在本租户
			// 本用户的会话下（参考 TestGormRepositoryForkSessionRejectsForeignSession）。
			if _, err := getSessionTx(tx, input.Session.TenantID, input.Session.UserID, sessionID); err != nil {
				return err
			}
			for _, message := range input.Messages {
				if message.TraceID == "" {
					message.TraceID = traceID
				}
				if _, err := upsertMessageRowTx(tx, sessionScopedMessage(message, input.Session, sessionID)); err != nil {
					return err
				}
			}
			out = ForkSessionResult{SessionID: sessionID, CopiedMessages: len(input.Messages)}
			return nil
		})
	})
	if err != nil {
		return ForkSessionResult{}, err
	}
	return out, nil
}
