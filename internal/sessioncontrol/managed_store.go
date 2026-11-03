package sessioncontrol

import (
	"context"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

// TenantManagedService is the narrow tenant-service surface used to persist
// managed sessions. Keeping this boundary small lets session control remain
// independent from the tenant service's broader control-plane API.
type TenantManagedService interface {
	ResolveContext(context.Context) (tenant.Context, error)
	ResolveContextOnce(context.Context) (context.Context, tenant.Context, error)
	UpsertSession(context.Context, tenant.SessionRequest) (uint64, error)
	CreateSessionControlSession(context.Context, tenant.SessionControlSessionRequest) (mysql.SessionControlCreateResult, error)
	ListSessionControlSessions(context.Context, int) ([]mysql.SessionControlSession, error)
	GetSessionControlSessionByKey(context.Context, string) (mysql.SessionControlSession, error)
	RecoverSessionControlStop(context.Context, uint64, string) (mysql.SessionControlStopRecovery, error)
	GetMessage(context.Context, uint64, uint64) (mysql.Message, error)
	ListRecentMessages(context.Context, uint64, int) ([]mysql.Message, error)
	GetAgentTask(context.Context, uint64) (mysql.AgentTask, error)
	GetAgentTaskEvent(context.Context, uint64) (mysql.AgentTaskEvent, error)
	CreateHandoffBatch(context.Context, agenttasks.HandoffBatchInput) (agenttasks.HandoffBatchResult, error)
	ListLatestAgentTasksForSessions(context.Context, []uint64) ([]mysql.AgentTask, error)
	CreateAgentTask(context.Context, agenttasks.TaskInput) (uint64, error)
	CancelAgentTaskIfRunning(context.Context, uint64, string) (bool, error)
	CancelAgentTaskForSessionControl(context.Context, tenant.SessionControlStopRequest) (mysql.SessionControlStopResult, error)
	ListAgentTaskEventsForTasksComplete(context.Context, []uint64) ([]mysql.AgentTaskEvent, error)
	ListSessionLinks(context.Context, uint64, int) ([]mysql.SessionLink, error)
	GetSessionLink(context.Context, uint64, string, string, string) (mysql.SessionLink, error)
}

// TenantManagedStore validates the transport-provided scope against the
// authenticated tenant context before delegating to tenant.Service.
type TenantManagedStore struct {
	service TenantManagedService
}

var _ ManagedStore = (*TenantManagedStore)(nil)
var _ TenantManagedService = (*tenant.Service)(nil)

func NewTenantManagedStore(service TenantManagedService) *TenantManagedStore {
	return &TenantManagedStore{service: service}
}

func (s *TenantManagedStore) ResolveContextOnce(ctx context.Context) (context.Context, tenant.Context, error) {
	return s.service.ResolveContextOnce(ctx)
}

func (s *TenantManagedStore) UpsertSession(ctx context.Context, scope RequestContext, input mysql.SessionInput) (uint64, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return 0, err
	}
	return s.service.UpsertSession(ctx, tenant.SessionRequest{
		SessionKey:   input.SessionKey,
		Title:        input.Title,
		Status:       input.Status,
		Model:        input.Model,
		CWD:          input.CWD,
		MetadataJSON: input.MetadataJSON,
	})
}

func (s *TenantManagedStore) CreateSessionControlSession(ctx context.Context, scope RequestContext, input mysql.SessionControlCreateInput) (mysql.SessionControlCreateResult, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.SessionControlCreateResult{}, err
	}
	item := input.Session
	return s.service.CreateSessionControlSession(ctx, tenant.SessionControlSessionRequest{
		SessionKey: item.SessionKey, Title: item.Title, Status: item.Status,
		Model: item.Model, CWD: item.CWD, MetadataJSON: item.MetadataJSON,
	})
}

func (s *TenantManagedStore) ListSessions(ctx context.Context, scope RequestContext, limit int) ([]mysql.SessionControlSession, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return nil, err
	}
	return s.service.ListSessionControlSessions(ctx, limit)
}

func (s *TenantManagedStore) ListSessionChannels(ctx context.Context, scope RequestContext, sessionIDs []uint64) (map[uint64]mysql.SessionChannel, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return nil, err
	}
	lister, ok := s.service.(interface {
		ListSessionChannels(context.Context, uint64, uint64, []uint64) (map[uint64]mysql.SessionChannel, error)
	})
	if !ok {
		return map[uint64]mysql.SessionChannel{}, nil
	}
	return lister.ListSessionChannels(ctx, scope.TenantID, scope.UserID, sessionIDs)
}

func (s *TenantManagedStore) GetSessionControlSessionByKey(ctx context.Context, scope RequestContext, key string) (mysql.SessionControlSession, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.SessionControlSession{}, err
	}
	return s.service.GetSessionControlSessionByKey(ctx, key)
}

func (s *TenantManagedStore) GetSessionByKey(ctx context.Context, scope RequestContext, key string) (mysql.Session, error) {
	item, err := s.GetSessionControlSessionByKey(ctx, scope, key)
	return item.Session, err
}

func (s *TenantManagedStore) RecoverSessionControlStop(ctx context.Context, sessionID uint64, keyHash string) (mysql.SessionControlStopRecovery, error) {
	return s.service.RecoverSessionControlStop(ctx, sessionID, keyHash)
}

func (s *TenantManagedStore) GetSessionLink(ctx context.Context, scope RequestContext, targetSessionID uint64, source SessionRef, relationType string) (mysql.SessionLink, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.SessionLink{}, err
	}
	return s.service.GetSessionLink(ctx, targetSessionID, string(source.Source), source.Key, relationType)
}

func (s *TenantManagedStore) GetAgentTask(ctx context.Context, scope RequestContext, taskID uint64) (mysql.AgentTask, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.AgentTask{}, err
	}
	return s.service.GetAgentTask(ctx, taskID)
}

func (s *TenantManagedStore) GetMessage(ctx context.Context, scope RequestContext, sessionID, messageID uint64) (mysql.Message, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.Message{}, err
	}
	message, err := s.service.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return mysql.Message{}, err
	}
	if message.ID != messageID || message.SessionID != sessionID {
		return mysql.Message{}, mysql.ErrNotFound
	}
	return message, nil
}

func (s *TenantManagedStore) GetTaskEvent(ctx context.Context, scope RequestContext, sessionID, eventID uint64) (mysql.AgentTaskEvent, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.AgentTaskEvent{}, err
	}
	return s.getTaskEventAuthorized(ctx, sessionID, eventID)
}

func (s *TenantManagedStore) getTaskEventAuthorized(ctx context.Context, sessionID, eventID uint64) (mysql.AgentTaskEvent, error) {
	event, err := s.service.GetAgentTaskEvent(ctx, eventID)
	if err != nil {
		return mysql.AgentTaskEvent{}, err
	}
	task, err := s.service.GetAgentTask(ctx, event.TaskID)
	if err != nil {
		return mysql.AgentTaskEvent{}, err
	}
	if event.ID != eventID || task.ID != event.TaskID || task.ParentSessionID != sessionID {
		return mysql.AgentTaskEvent{}, mysql.ErrNotFound
	}
	return event, nil
}

func (s *TenantManagedStore) GetToolTraceRecord(ctx context.Context, scope RequestContext, sessionID, eventID uint64, ordinal int) (mysql.AgentTaskEvent, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.AgentTaskEvent{}, err
	}
	if ordinal != 0 {
		return mysql.AgentTaskEvent{}, mysql.ErrNotFound
	}
	event, err := s.getTaskEventAuthorized(ctx, sessionID, eventID)
	if err != nil {
		return mysql.AgentTaskEvent{}, err
	}
	if event.EventType != agenttasks.EventToolResult {
		return mysql.AgentTaskEvent{}, mysql.ErrNotFound
	}
	return event, nil
}

func (s *TenantManagedStore) ListRecentMessages(ctx context.Context, scope RequestContext, sessionID uint64, limit int) ([]mysql.Message, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return nil, err
	}
	return s.service.ListRecentMessages(ctx, sessionID, limit)
}

func (s *TenantManagedStore) ListLatestAgentTasksForSessions(ctx context.Context, scope RequestContext, sessionIDs []uint64) ([]mysql.AgentTask, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return nil, err
	}
	return s.service.ListLatestAgentTasksForSessions(ctx, sessionIDs)
}

func (s *TenantManagedStore) ListSessionConversationTasks(ctx context.Context, scope RequestContext, sessionID uint64, limit int) ([]mysql.AgentTask, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return nil, err
	}
	reader, ok := s.service.(interface {
		ListSessionConversationTasks(context.Context, uint64, int) ([]mysql.AgentTask, error)
	})
	if !ok {
		return nil, unavailable("tenant conversation store")
	}
	return reader.ListSessionConversationTasks(ctx, sessionID, limit)
}

func (s *TenantManagedStore) CreateAgentTask(ctx context.Context, scope RequestContext, input agenttasks.TaskInput) (uint64, error) {
	resolved, err := s.resolveScope(ctx, scope)
	if err != nil {
		return 0, err
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	return s.service.CreateAgentTask(ctx, input)
}

func (s *TenantManagedStore) CancelAgentTaskIfRunning(ctx context.Context, scope RequestContext, taskID uint64, resultJSON string) (bool, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return false, err
	}
	return s.service.CancelAgentTaskIfRunning(ctx, taskID, resultJSON)
}

func (s *TenantManagedStore) CancelAgentTaskForSessionControl(ctx context.Context, scope RequestContext, input mysql.SessionControlStopInput) (mysql.SessionControlStopResult, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return mysql.SessionControlStopResult{}, err
	}
	return s.service.CancelAgentTaskForSessionControl(ctx, tenant.SessionControlStopRequest{
		TaskID: input.TaskID, ResultJSON: input.ResultJSON,
		EventPayloadJSON: input.EventPayloadJSON, TraceID: input.TraceID,
	})
}

func (s *TenantManagedStore) AppendAgentTaskEvent(ctx context.Context, scope RequestContext, input agenttasks.EventInput) (uint64, error) {
	resolved, err := s.resolveScope(ctx, scope)
	if err != nil {
		return 0, err
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	appender, ok := s.service.(interface {
		AppendAgentTaskEvent(context.Context, agenttasks.EventInput) (uint64, error)
	})
	if !ok {
		return 0, unavailable("tenant task event appender")
	}
	return appender.AppendAgentTaskEvent(ctx, input)
}

func (s *TenantManagedStore) ListAgentTaskEventsForTasksComplete(ctx context.Context, scope RequestContext, taskIDs []uint64) ([]mysql.AgentTaskEvent, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return nil, err
	}
	return s.service.ListAgentTaskEventsForTasksComplete(ctx, taskIDs)
}

func (s *TenantManagedStore) ListSessionLinks(ctx context.Context, scope RequestContext, sessionID uint64, limit int) ([]mysql.SessionLink, error) {
	if _, err := s.resolveScope(ctx, scope); err != nil {
		return nil, err
	}
	return s.service.ListSessionLinks(ctx, sessionID, limit)
}

func (s *TenantManagedStore) resolveScope(ctx context.Context, scope RequestContext) (tenant.Context, error) {
	resolved, err := s.service.ResolveContext(ctx)
	if err != nil {
		return tenant.Context{}, err
	}
	if resolved.TenantID != scope.TenantID || resolved.UserID != scope.UserID {
		return tenant.Context{}, &ServiceError{Code: CodeForbidden, Message: "request scope does not match authenticated tenant context"}
	}
	return resolved, nil
}
