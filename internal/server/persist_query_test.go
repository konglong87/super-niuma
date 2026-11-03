package server

import (
	"context"
	"errors"
	"testing"

	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

// tenantTurnStore 只关心「一次查询到底往哪几张表落了行」。
// 它按事务语义记账：行先进暂存区，只有整批成功才可见；
// failAt 让第 N 次写入返回错误，用来复现半写会话。
//
// 夹具能不能做到原子，本身就是被测性质：三次独立调用无论如何都凑不出
// 原子性，只有当服务端把整个 turn 交给存储层一次写完时它才可能成立。
type tenantTurnStore struct {
	*fakeTenantService

	failAt int
	writes int

	sessions []tenantservice.SessionRequest
	messages []tenantservice.MessageRequest
}

func newTenantTurnStore(failAt int) *tenantTurnStore {
	return &tenantTurnStore{fakeTenantService: &fakeTenantService{}, failAt: failAt}
}

var errTurnWriteFailed = errors.New("write failed")

func (s *tenantTurnStore) nextWriteFails() bool {
	s.writes++
	return s.writes == s.failAt
}

func (s *tenantTurnStore) UpsertSession(ctx context.Context, req tenantservice.SessionRequest) (uint64, error) {
	if s.nextWriteFails() {
		return 0, errTurnWriteFailed
	}
	s.sessions = append(s.sessions, req)
	return 7, nil
}

func (s *tenantTurnStore) UpsertMessage(ctx context.Context, req tenantservice.MessageRequest) (uint64, error) {
	if s.nextWriteFails() {
		return 0, errTurnWriteFailed
	}
	s.messages = append(s.messages, req)
	return uint64(len(s.messages)), nil
}

// SaveQueryTurn 按事务语义记账：三行先在暂存区成形，任一步失败就整体丢弃。
// 上面那两个逐张写的方法留着当回归闸门 —— 一旦 persistTenantQuery 退回
// 三次独立写入，它们就会被打到，半写会话立刻重新变红。
func (s *tenantTurnStore) SaveQueryTurn(ctx context.Context, req tenantservice.QueryTurnRequest) (tenantservice.QueryTurnResult, error) {
	const sessionID = 7
	if s.nextWriteFails() {
		return tenantservice.QueryTurnResult{}, errTurnWriteFailed
	}
	pending := make([]tenantservice.MessageRequest, 0, 2)
	for _, msg := range []tenantservice.MessageRequest{req.User, req.Assistant} {
		if s.nextWriteFails() {
			return tenantservice.QueryTurnResult{}, errTurnWriteFailed
		}
		msg.SessionID = sessionID
		pending = append(pending, msg)
	}
	s.sessions = append(s.sessions, req.Session)
	s.messages = append(s.messages, pending...)
	return tenantservice.QueryTurnResult{
		SessionID:          sessionID,
		UserMessageID:      1,
		AssistantMessageID: 2,
	}, nil
}

func testPersistContext() context.Context {
	return observability.WithRequestValues(context.Background(), "trace-1", "user-1", "yutang")
}

func TestPersistTenantQueryLeavesNoRowsWhenALaterWriteFails(t *testing.T) {
	// failAt=2 是用户消息失败，failAt=3 是助手消息失败；
	// 两种情况下会话行都不许留下来。
	for _, failAt := range []int{2, 3} {
		store := newTenantTurnStore(failAt)
		persistTenantQuery(testPersistContext(), store, nil, QueryRequest{
			SessionKey: "session-a",
			Prompt:     "hello",
			Model:      "claude-sonnet-5",
		}, query.Result{Response: "hi"})

		if len(store.sessions) != 0 {
			t.Fatalf("failAt=%d: session rows = %d, want 0 (half-written session left behind)", failAt, len(store.sessions))
		}
		if len(store.messages) != 0 {
			t.Fatalf("failAt=%d: message rows = %d, want 0", failAt, len(store.messages))
		}
	}
}

func TestPersistTenantQueryWritesAllThreeRowsOnSuccess(t *testing.T) {
	store := newTenantTurnStore(0)
	persistTenantQuery(testPersistContext(), store, nil, QueryRequest{
		SessionKey: "session-a",
		Prompt:     "hello",
		Model:      "claude-sonnet-5",
	}, query.Result{Response: "hi"})

	if len(store.sessions) != 1 {
		t.Fatalf("session rows = %d, want 1", len(store.sessions))
	}
	if len(store.messages) != 2 {
		t.Fatalf("message rows = %d, want 2", len(store.messages))
	}
	if store.messages[0].Role != "user" || store.messages[1].Role != "assistant" {
		t.Fatalf("roles = %q/%q", store.messages[0].Role, store.messages[1].Role)
	}
}
