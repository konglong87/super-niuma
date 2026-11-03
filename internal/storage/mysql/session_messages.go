package mysql

import (
	"context"
	"fmt"
	"slices"
)

// messageSelectColumns 是 scanMessages 期望的列顺序，本文件的定点读法共用一份。
// 手抄一遍就多一个「列顺序和 scanMessages 错开」的静默扫描 bug 的机会。
const messageSelectColumns = "id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at"

// ListAllMessages 返回一个会话的全部消息，或者报错 —— 绝不返回被静默截断的切片。
//
// 这是 ListMessages 的反面。那条走 normalizeLimit：limit 超过 500 会被静默夹到
// 500，用来分页是对的，但用来「把整个会话复制一份」就是静默丢数据。fork 曾经
// 拿 ListMessages(…, 1000) 当"全部"用，于是超过 500 条的会话被复制成一个悄悄
// 缺了尾巴的分支（TODO-115）。
//
// 多取一条来判断「还有更多」：超过 maxRows 就返回 ErrTooManyMessages，让调用方
// 响亮地失败，而不是拿着一份不完整的列表继续走。
func (r *GormRepository) ListAllMessages(ctx context.Context, tenantID, userID, sessionID uint64, maxRows int) ([]Message, error) {
	r.log(ctx, "message.list_all", "mysql.GormRepository.ListAllMessages", "list every tenant session message")
	if maxRows <= 0 {
		return nil, fmt.Errorf("%w: maxRows must be positive", ErrInvalidInput)
	}
	rows, err := r.with(ctx).Table("tenant_session_messages").
		Select("id, session_id, turn_index, role, content, content_json, tool_id, tool_name, is_error, model, input_tokens, output_tokens, trace_id, created_at").
		Where("tenant_id = ? AND user_id = ? AND session_id = ?", tenantID, userID, sessionID).
		Order("turn_index ASC").
		Limit(maxRows + 1).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(messages) > maxRows {
		return nil, fmt.Errorf("%w: session %d has more than %d messages", ErrTooManyMessages, sessionID, maxRows)
	}
	return messages, nil
}

// MaxMessageTurn 返回会话里最大的 turn_index（空会话为 0）。
//
// 必须问数据库。原来 mobileNextTurns 靠扫一个 normalizeLimit 夹到 500 条的列表来
// 找最大轮次：超过 500 条的会话里它会算出一个**已经存在**的轮次，而消息表的唯一键
// 是 (session_id, turn_index) 且写入走 ON DUPLICATE KEY UPDATE —— 新消息会直接
// 覆盖掉一条已有消息（TODO-116）。
func (r *GormRepository) MaxMessageTurn(ctx context.Context, tenantID, userID, sessionID uint64) (uint, error) {
	r.log(ctx, "message.max_turn", "mysql.GormRepository.MaxMessageTurn", "read the highest tenant session message turn")
	var maxTurn uint
	err := r.with(ctx).Table("tenant_session_messages").
		Select("COALESCE(MAX(turn_index), 0)").
		Where("tenant_id = ? AND user_id = ? AND session_id = ?", tenantID, userID, sessionID).
		Scan(&maxTurn).Error
	if err != nil {
		return 0, err
	}
	return maxTurn, nil
}

// GetMessage 按 id 取一条消息，取不到报 ErrNotFound。
//
// cancel 和 regenerate 要的一直是「按 id 取这一条」，但它们原先的写法是
// ListMessages(…, 1000) 再在返回的切片里线性查找。normalizeLimit 把 limit 静默夹
// 到 500，于是在超过 500 条的会话里，对一条**真实存在**的消息做取消或重新生成会
// 得到 404 message not found（TODO-117）。
//
// 修法是定点查询，而不是改走 ListAllMessages：这两条路径只要一条消息，把 5000 条
// 消息连 content 一起拉进内存纯属浪费。
//
// tenant_id / user_id 仍然写在 WHERE 里。id 是全表唯一的，光按 id 查也能查到，
// 但那样就等于把越权读打开了 —— 归属校验必须留在查询里，不能因为换了查询而丢掉。
func (r *GormRepository) GetMessage(ctx context.Context, tenantID, userID, sessionID, messageID uint64) (Message, error) {
	r.log(ctx, "message.get", "mysql.GormRepository.GetMessage", "read one tenant session message by id")
	rows, err := r.with(ctx).Table("tenant_session_messages").
		Select(messageSelectColumns).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND id = ?", tenantID, userID, sessionID, messageID).
		Limit(1).
		Rows()
	if err != nil {
		return Message{}, err
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 0 {
		return Message{}, fmt.Errorf("%w: message %d in session %d", ErrNotFound, messageID, sessionID)
	}
	return messages[0], nil
}

// PreviousUserMessage 取 beforeTurn 之前最后一条 user 消息，取不到报 ErrNotFound。
//
// regenerate 除了目标 assistant 消息，还要它对应的那条 user 消息当提示。原先这一步
// 也是在被夹取的列表里线性查找，所以在 620 条消息的会话里重新生成第 600 条，即使
// 目标查到了，第 599 条那条 user 消息仍然在 500 之外 —— 会改报 400 previous user
// message not found。两处都得是定点查询才算修完（TODO-117）。
func (r *GormRepository) PreviousUserMessage(ctx context.Context, tenantID, userID, sessionID uint64, beforeTurn uint) (Message, error) {
	r.log(ctx, "message.previous_user", "mysql.GormRepository.PreviousUserMessage", "read the user message before a turn")
	rows, err := r.with(ctx).Table("tenant_session_messages").
		Select(messageSelectColumns).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND role = ? AND turn_index < ?", tenantID, userID, sessionID, "user", beforeTurn).
		Order("turn_index DESC").
		Limit(1).
		Rows()
	if err != nil {
		return Message{}, err
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 0 {
		return Message{}, fmt.Errorf("%w: no user message before turn %d in session %d", ErrNotFound, beforeTurn, sessionID)
	}
	return messages[0], nil
}

// MessageByKey 按 (role, message_key) 取一条消息，查不到返回 found=false。
//
// message_key 是移动端的幂等键。原先判断「这条 message_key 是否已经处理过」的写法是
// ListMessages(…, 1000) 再在返回切片里线性查找，而 normalizeLimit 把 limit 静默夹到
// 500 —— 长会话里客户端重试找不到已有记录，于是重新跑一次查询、重新写一条消息、
// 重新计一次量。这一族夹取 bug 里只有这一处**花钱且有副作用**（TODO-118）。
//
// 比的是 000010 加的 mobile_message_key 生成列（从 content_json 的
// $.mobile.message_key 抽出，STORED，带前缀索引），不是 JSON 路径表达式：
// 后者用不上索引，等于把线性扫描从 Go 搬进 MySQL。
//
// role 是必须的判别条件，不是可选过滤：同一个 message_key 会同时落在一条 user 和
// 一条 assistant 上（user 是请求本身，assistant 是它的回答），两个调用点要的是不同
// 的那一条。
//
// 返回 (Message, bool, error) 而不是 ErrNotFound：这里「查不到」是**预期的常态** ——
// 第一次请求本来就不是重放。用错误表达它会逼调用方在正常路径上做 errors.Is，
// 更要紧的是查询真的失败时容易被同一条分支吞掉，那就等于在数据库出问题时静默重复
// 执行一次。
func (r *GormRepository) MessageByKey(ctx context.Context, tenantID, userID, sessionID uint64, role, messageKey string) (Message, bool, error) {
	r.log(ctx, "message.by_key", "mysql.GormRepository.MessageByKey", "read one tenant session message by message key")
	// 空 key 不许查：mobileMetadataJSON 在 message_key 为空时写的是空字符串而不是省略
	// 该字段，所以空 key 一查就会撞上那些行。
	if messageKey == "" {
		return Message{}, false, fmt.Errorf("%w: messageKey must not be empty", ErrInvalidInput)
	}
	rows, err := r.with(ctx).Table("tenant_session_messages").
		Select(messageSelectColumns).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND role = ? AND mobile_message_key = ?", tenantID, userID, sessionID, role, messageKey).
		Limit(1).
		Rows()
	if err != nil {
		return Message{}, false, err
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	if err != nil {
		return Message{}, false, err
	}
	if len(messages) == 0 {
		return Message{}, false, nil
	}
	return messages[0], true, nil
}

// ListRecentMessages 取一个会话最近的 limit 条消息，按 turn_index 升序返回。
//
// ListMessages 是 ORDER BY turn_index ASC 且被 normalizeLimit 夹到 500，所以拿它求
// 「最新的什么」求出来的是「最旧 500 条里的最新」。会话详情的 latest_recap 就栽在
// 这里：长会话里新生成的 recap 根本不出现（TODO-119）。倒序读一个窗口即可，不需要
// 把整个会话拉回来。
//
// 返回前翻回升序：本仓储的 []Message 一律是按 turn_index 升序的，调用方（以及
// mobileLatestRecapFromMessages 这种从尾部往前找的代码）都依赖这个不变量。倒序只
// 是「取哪一段」的手段，不是返回契约。
func (r *GormRepository) ListRecentMessages(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]Message, error) {
	r.log(ctx, "message.list_recent", "mysql.GormRepository.ListRecentMessages", "list the most recent tenant session messages")
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be positive", ErrInvalidInput)
	}
	rows, err := r.with(ctx).Table("tenant_session_messages").
		Select(messageSelectColumns).
		Where("tenant_id = ? AND user_id = ? AND session_id = ?", tenantID, userID, sessionID).
		Order("turn_index DESC").
		Limit(limit).
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	slices.Reverse(messages)
	return messages, nil
}
