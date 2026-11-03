package agentteam

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type TeamRunStore interface {
	CreateAgentTeamRun(context.Context, mysqlstore.AgentTeamRunInput) (mysqlstore.AgentTeamRun, error)
	UpdateAgentTeamRun(context.Context, mysqlstore.AgentTeamRunUpdate) error
}

type TeamRunEventStore interface {
	AppendAgentTeamRunEvent(context.Context, mysqlstore.AgentTeamRunEventInput) (mysqlstore.AgentTeamRunEvent, error)
}

type MySQLRunRecorder struct {
	store      TeamRunStore
	eventStore TeamRunEventStore
}

func NewMySQLRunRecorder(store TeamRunStore, eventStore ...TeamRunEventStore) *MySQLRunRecorder {
	recorder := &MySQLRunRecorder{store: store}
	if len(eventStore) > 0 {
		recorder.eventStore = eventStore[0]
	}
	return recorder
}

func (r *MySQLRunRecorder) Start(ctx context.Context, record RunRecord) error {
	if r == nil || r.store == nil || record.TenantID == 0 || record.TeamID == 0 || record.InboxEventID == 0 || record.SourceAccountID == 0 || strings.TrimSpace(record.RunID) == "" {
		return mysqlstore.ErrInvalidInput
	}
	var startedAt *time.Time
	if !record.StartedAt.IsZero() {
		value := record.StartedAt
		startedAt = &value
	}
	_, err := r.store.CreateAgentTeamRun(ctx, mysqlstore.AgentTeamRunInput{ID: record.RunID, TenantID: record.TenantID, TeamID: record.TeamID, InboxEventID: record.InboxEventID, SourceAccountID: record.SourceAccountID, ConversationID: record.ConversationID, CoordinatorMemberKey: record.CoordinatorMember, Status: string(record.Status), TeamEffectiveHash: record.TeamEffectiveHash, MemberCount: record.MemberCount, MaxRounds: record.MaxRounds, MaxParallelMembers: record.MaxParallelMembers, MaxTotalTokens: record.MaxTotalTokens, StartedAt: startedAt})
	return err
}

func (r *MySQLRunRecorder) Finish(ctx context.Context, record RunRecord) error {
	if r == nil || r.store == nil || record.TenantID == 0 || strings.TrimSpace(record.RunID) == "" || record.Status == "" {
		return mysqlstore.ErrInvalidInput
	}
	var finishedAt *time.Time
	if !record.FinishedAt.IsZero() {
		value := record.FinishedAt
		finishedAt = &value
	}
	resultJSON, err := json.Marshal(map[string]string{"content": record.Result})
	if err != nil {
		return err
	}
	return r.store.UpdateAgentTeamRun(ctx, mysqlstore.AgentTeamRunUpdate{TenantID: record.TenantID, RunID: record.RunID, Status: string(record.Status), UsedTokens: record.UsedTokens, UsedTurns: record.UsedTurns, FinishedAt: finishedAt, ResultJSON: string(resultJSON), ErrorMessage: record.Error})
}

func (r *MySQLRunRecorder) Append(ctx context.Context, event RunEvent) error {
	if r == nil || r.eventStore == nil || event.TenantID == 0 || strings.TrimSpace(event.RunID) == "" || event.Sequence == 0 || strings.TrimSpace(event.EventType) == "" {
		return mysqlstore.ErrInvalidInput
	}
	_, err := r.eventStore.AppendAgentTeamRunEvent(ctx, mysqlstore.AgentTeamRunEventInput{
		TenantID: event.TenantID, TeamRunID: event.RunID, SequenceNo: event.Sequence, EventType: event.EventType,
		MemberKey: event.MemberKey, FromMemberKey: event.FromMember, ToMemberKey: event.ToMember,
		Status: string(event.Status), Summary: event.Summary, PayloadJSON: event.PayloadJSON,
		ArtifactRef: event.ArtifactRef, CreatedAt: event.CreatedAt,
	})
	return err
}
