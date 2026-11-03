package tenant

import (
	"context"
	"fmt"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type sessionConversationRepository interface {
	ListSessionConversationEvents(context.Context, uint64, uint64, uint64, uint64, int) ([]mysqlstore.AgentTaskEvent, error)
	ListSessionConversationTasks(context.Context, uint64, uint64, uint64, int) ([]mysqlstore.AgentTask, error)
}

type webAgentConversationRepository interface {
	ListWebAgentConversationTasks(context.Context, uint64, uint64, uint64, int) ([]mysqlstore.AgentTask, error)
}

func (s *Service) ListWebAgentConversationTasks(ctx context.Context, sessionID uint64, limit int) ([]mysqlstore.AgentTask, error) {
	s.log(ctx, "tenant.web_agent_conversation.tasks", "tenant.Service.ListWebAgentConversationTasks", "list session conversation tasks")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(webAgentConversationRepository)
	if !ok {
		return nil, fmt.Errorf("web agent conversation repository unavailable")
	}
	return repo.ListWebAgentConversationTasks(ctx, resolved.TenantID, resolved.UserID, sessionID, limit)
}

func (s *Service) ListSessionConversationTasks(ctx context.Context, sessionID uint64, limit int) ([]mysqlstore.AgentTask, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(sessionConversationRepository)
	if !ok {
		return nil, fmt.Errorf("session conversation repository unavailable")
	}
	return repo.ListSessionConversationTasks(ctx, resolved.TenantID, resolved.UserID, sessionID, limit)
}

func (s *Service) ListSessionConversationEvents(ctx context.Context, sessionID, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(sessionConversationRepository)
	if !ok {
		return nil, fmt.Errorf("session conversation repository unavailable")
	}
	return repo.ListSessionConversationEvents(ctx, resolved.TenantID, resolved.UserID, sessionID, afterID, limit)
}
