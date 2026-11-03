package agentteam

import (
	"context"
	"errors"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type TeamMailboxStore interface {
	AppendAgentTeamMailbox(context.Context, mysqlstore.AgentTeamMailboxInput) (mysqlstore.AgentTeamMailbox, error)
	ListAgentTeamMailbox(context.Context, uint64, string, int) ([]mysqlstore.AgentTeamMailbox, error)
	MarkAgentTeamMailboxConsumed(context.Context, uint64, uint64) error
}

type MySQLMailbox struct {
	store TeamMailboxStore
}

func NewMySQLMailbox(store TeamMailboxStore) *MySQLMailbox {
	return &MySQLMailbox{store: store}
}

func (m *MySQLMailbox) Append(ctx context.Context, message Message) error {
	if m == nil || m.store == nil || message.TenantID == 0 || message.RunID == "" || message.IdempotencyKey == "" {
		return ErrMessageNotFound
	}
	_, err := m.store.AppendAgentTeamMailbox(ctx, mysqlstore.AgentTeamMailboxInput{
		TenantID: message.TenantID, TeamRunID: message.RunID, FromMemberKey: message.FromMember,
		ToMemberKey: message.ToMember, MessageKind: message.Kind, SequenceNo: message.Sequence,
		IdempotencyKey: message.IdempotencyKey, EvidenceRef: message.EvidenceRef, Status: message.Status,
		PayloadRef: message.Content,
	})
	return err
}

func (m *MySQLMailbox) List(ctx context.Context, tenantID uint64, runID string) ([]Message, error) {
	if m == nil || m.store == nil || tenantID == 0 || runID == "" {
		return nil, ErrMessageNotFound
	}
	rows, err := m.store.ListAgentTeamMailbox(ctx, tenantID, runID, 500)
	if err != nil {
		return nil, err
	}
	result := make([]Message, 0, len(rows))
	for _, row := range rows {
		result = append(result, Message{TenantID: row.TenantID, RunID: row.TeamRunID, FromMember: row.FromMemberKey, ToMember: row.ToMemberKey, Kind: row.MessageKind, Sequence: row.SequenceNo, IdempotencyKey: row.IdempotencyKey, Content: row.PayloadRef, EvidenceRef: row.EvidenceRef, Status: row.Status})
	}
	return result, nil
}

func (m *MySQLMailbox) Consume(ctx context.Context, tenantID uint64, runID, idempotencyKey string) error {
	if m == nil || m.store == nil || tenantID == 0 || runID == "" || idempotencyKey == "" {
		return ErrMessageNotFound
	}
	rows, err := m.store.ListAgentTeamMailbox(ctx, tenantID, runID, 500)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.IdempotencyKey == idempotencyKey {
			return m.store.MarkAgentTeamMailboxConsumed(ctx, tenantID, row.ID)
		}
	}
	return errors.Join(ErrMessageNotFound, mysqlstore.ErrNotFound)
}
