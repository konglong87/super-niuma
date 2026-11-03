package mysql

import (
	"context"

	"gorm.io/gorm"

	"github.com/konglong87/super-niuma/internal/observability"
)

// QueryTurnInput 是一次查询要落库的整批行：会话 + 用户消息 + 助手消息。
// 消息的 TenantID/UserID/SessionID 由仓储在写入时统一填成会话那一行的值，调用方不填。
type QueryTurnInput struct {
	Session   SessionInput
	User      MessageInput
	Assistant MessageInput
}

type QueryTurnResult struct {
	SessionID          uint64
	UserMessageID      uint64
	AssistantMessageID uint64
}

// SaveQueryTurn 把三张表的写入收进一个显式事务：要么全成功，要么全回滚，
// 不会留下「会话行在、消息缺失」或「用户消息在、助手消息缺失」的半写会话。
//
// 仓储全局设了 SkipDefaultTransaction，那是刻意的性能选择；这里只给这一条
// 需要原子性的路径开事务，不动全局默认值。
//
// 死锁重试包在事务外层（与 RollbackSkillVersion 同构）：重试的是整个事务，
// 而不是事务内的单条语句 —— 回滚后再单独重发一条语句会把消息挂到已经不存在的会话上。
func (r *GormRepository) SaveQueryTurn(ctx context.Context, input QueryTurnInput) (QueryTurnResult, error) {
	r.log(ctx, "session.turn.save", "mysql.GormRepository.SaveQueryTurn", "save tenant query turn")
	if input.User.TraceID == "" {
		input.User.TraceID = observability.TraceID(ctx)
	}
	if input.Assistant.TraceID == "" {
		input.Assistant.TraceID = observability.TraceID(ctx)
	}
	var out QueryTurnResult
	err := withRetryableTransaction(ctx, func() error {
		out = QueryTurnResult{}
		return r.with(ctx).Transaction(func(tx *gorm.DB) error {
			sessionID, err := upsertSessionTx(tx, input.Session)
			if err != nil {
				return err
			}
			// 归属校验和单条 UpsertMessage 走同一个 getSessionTx：事务化不得把
			// 2026-06-26 数据隔离 review 加的这道校验绕过去。会话行刚在本事务里
			// 按 (tenant_id, user_id, session_key) 唯一键写过，这里在事务内复查
			// 一次，既覆盖归档会话，也保证消息只挂在本租户本用户的会话下。
			if _, err := getSessionTx(tx, input.Session.TenantID, input.Session.UserID, sessionID); err != nil {
				return err
			}
			userMessageID, err := upsertMessageRowTx(tx, sessionScopedMessage(input.User, input.Session, sessionID))
			if err != nil {
				return err
			}
			assistantMessageID, err := upsertMessageRowTx(tx, sessionScopedMessage(input.Assistant, input.Session, sessionID))
			if err != nil {
				return err
			}
			out = QueryTurnResult{
				SessionID:          sessionID,
				UserMessageID:      userMessageID,
				AssistantMessageID: assistantMessageID,
			}
			return nil
		})
	})
	if err != nil {
		return QueryTurnResult{}, err
	}
	return out, nil
}

func sessionScopedMessage(msg MessageInput, session SessionInput, sessionID uint64) MessageInput {
	msg.TenantID = session.TenantID
	msg.UserID = session.UserID
	msg.SessionID = sessionID
	return msg
}
