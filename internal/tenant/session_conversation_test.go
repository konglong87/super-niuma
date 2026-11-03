package tenant

import (
	"context"
	"testing"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type conversationRepositoryFake struct {
	*fakeRepository
	scope  [3]uint64
	cursor uint64
	calls  int
	limit  int
}

func (r *conversationRepositoryFake) ListWebAgentConversationTasks(_ context.Context, tenantID, userID, sessionID uint64, limit int) ([]mysqlstore.AgentTask, error) {
	r.scope, r.limit, r.calls = [3]uint64{tenantID, userID, sessionID}, limit, r.calls+1
	return []mysqlstore.AgentTask{{ID: 42, ParentSessionID: sessionID}}, nil
}

func (r *conversationRepositoryFake) ListSessionConversationEvents(_ context.Context, tenantID, userID, sessionID, cursor uint64, _ int) ([]mysqlstore.AgentTaskEvent, error) {
	r.scope, r.cursor, r.calls = [3]uint64{tenantID, userID, sessionID}, cursor, r.calls+1
	return nil, nil
}

func (r *conversationRepositoryFake) ListSessionConversationTasks(_ context.Context, tenantID, userID, sessionID uint64, _ int) ([]mysqlstore.AgentTask, error) {
	r.scope, r.calls = [3]uint64{tenantID, userID, sessionID}, r.calls+1
	return nil, nil
}

func TestConversationReadsUseResolvedTenantAndUser(t *testing.T) {
	repo := &conversationRepositoryFake{fakeRepository: &fakeRepository{tenant: mysqlstore.Tenant{ID: 7, TenantKey: "yutang"}, userID: 11}}
	svc := NewService(repo, nil)
	if _, err := svc.ListSessionConversationEvents(testTenantContext(), 41, 50, 200); err != nil {
		t.Fatal(err)
	}
	if repo.scope != [3]uint64{7, 11, 41} || repo.cursor != 50 {
		t.Fatalf("scope=%v cursor=%d", repo.scope, repo.cursor)
	}
	if _, err := svc.ListSessionConversationTasks(testTenantContext(), 42, 200); err != nil {
		t.Fatal(err)
	}
	if repo.scope != [3]uint64{7, 11, 42} {
		t.Fatalf("scope=%v", repo.scope)
	}
	if _, err := svc.ListSessionConversationEvents(context.Background(), 41, 0, 200); err == nil || repo.calls != 2 {
		t.Fatalf("missing identity reached repository: calls=%d err=%v", repo.calls, err)
	}
}

func TestWebConversationTasksUseResolvedTenantAndUser(t *testing.T) {
	repo := &conversationRepositoryFake{fakeRepository: &fakeRepository{tenant: mysqlstore.Tenant{ID: 7, TenantKey: "yutang"}, userID: 11}}
	svc := NewService(repo, nil)
	tasks, err := svc.ListWebAgentConversationTasks(testTenantContext(), 41, 3)
	if err != nil || len(tasks) != 1 || tasks[0].ParentSessionID != 41 || repo.scope != [3]uint64{7, 11, 41} || repo.limit != 3 {
		t.Fatalf("tasks=%+v scope=%v limit=%d err=%v", tasks, repo.scope, repo.limit, err)
	}
	if _, err := svc.ListWebAgentConversationTasks(context.Background(), 41, 3); err == nil || repo.calls != 1 {
		t.Fatalf("missing identity reached repository: calls=%d err=%v", repo.calls, err)
	}
	withoutCapability := NewService(repo.fakeRepository, nil)
	if _, err := withoutCapability.ListWebAgentConversationTasks(testTenantContext(), 41, 3); err == nil {
		t.Fatal("missing scoped repository capability succeeded")
	}
}
