package agentteam

import (
	"context"
	"testing"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type fakeTeamMailboxStore struct {
	rows []mysqlstore.AgentTeamMailbox
	last uint64
}

func (f *fakeTeamMailboxStore) AppendAgentTeamMailbox(_ context.Context, input mysqlstore.AgentTeamMailboxInput) (mysqlstore.AgentTeamMailbox, error) {
	row := mysqlstore.AgentTeamMailbox{ID: uint64(len(f.rows) + 1), TenantID: input.TenantID, TeamRunID: input.TeamRunID, FromMemberKey: input.FromMemberKey, ToMemberKey: input.ToMemberKey, MessageKind: input.MessageKind, SequenceNo: input.SequenceNo, IdempotencyKey: input.IdempotencyKey, PayloadRef: input.PayloadRef, EvidenceRef: input.EvidenceRef, Status: input.Status}
	f.rows = append(f.rows, row)
	return row, nil
}

func (f *fakeTeamMailboxStore) ListAgentTeamMailbox(_ context.Context, tenantID uint64, runID string, _ int) ([]mysqlstore.AgentTeamMailbox, error) {
	result := make([]mysqlstore.AgentTeamMailbox, 0)
	for _, row := range f.rows {
		if row.TenantID == tenantID && row.TeamRunID == runID {
			result = append(result, row)
		}
	}
	return result, nil
}

func (f *fakeTeamMailboxStore) MarkAgentTeamMailboxConsumed(_ context.Context, tenantID, mailboxID uint64) error {
	f.last = mailboxID
	for i := range f.rows {
		if f.rows[i].TenantID == tenantID && f.rows[i].ID == mailboxID {
			f.rows[i].Status = "consumed"
			return nil
		}
	}
	return mysqlstore.ErrNotFound
}

func TestMySQLMailboxMapsDurableRows(t *testing.T) {
	store := &fakeTeamMailboxStore{}
	mailbox := NewMySQLMailbox(store)
	if err := mailbox.Append(context.Background(), Message{TenantID: 7, RunID: "run-1", FromMember: "researcher", ToMember: "editor", Kind: "result", Sequence: 1, IdempotencyKey: "m-1", Content: "stored"}); err != nil {
		t.Fatal(err)
	}
	items, err := mailbox.List(context.Background(), 7, "run-1")
	if err != nil || len(items) != 1 || items[0].Content != "stored" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err := mailbox.Consume(context.Background(), 7, "run-1", "m-1"); err != nil || store.last != 1 {
		t.Fatalf("consume err=%v last=%d", err, store.last)
	}
}
