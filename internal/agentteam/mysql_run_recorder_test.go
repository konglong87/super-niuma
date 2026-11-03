package agentteam

import (
	"context"
	"encoding/json"
	"testing"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type fakeTeamRunStore struct {
	updated mysqlstore.AgentTeamRunUpdate
	events  []mysqlstore.AgentTeamRunEventInput
}

func (f *fakeTeamRunStore) CreateAgentTeamRun(_ context.Context, input mysqlstore.AgentTeamRunInput) (mysqlstore.AgentTeamRun, error) {
	return mysqlstore.AgentTeamRun{ID: input.ID}, nil
}

func (f *fakeTeamRunStore) UpdateAgentTeamRun(_ context.Context, input mysqlstore.AgentTeamRunUpdate) error {
	f.updated = input
	return nil
}

func (f *fakeTeamRunStore) AppendAgentTeamRunEvent(_ context.Context, input mysqlstore.AgentTeamRunEventInput) (mysqlstore.AgentTeamRunEvent, error) {
	f.events = append(f.events, input)
	return mysqlstore.AgentTeamRunEvent{ID: uint64(len(f.events)), TeamRunID: input.TeamRunID, SequenceNo: input.SequenceNo, EventType: input.EventType}, nil
}

func TestMySQLRunRecorderEncodesMarkdownResultAsJSON(t *testing.T) {
	store := &fakeTeamRunStore{}
	recorder := NewMySQLRunRecorder(store)
	const result = "# 标题\n\n正文 **加粗**"
	if err := recorder.Finish(context.Background(), RunRecord{TenantID: 1, RunID: "team-run-1", Status: StatusCompleted, Result: result}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(store.updated.ResultJSON)) {
		t.Fatalf("result_json=%q is not valid JSON", store.updated.ResultJSON)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(store.updated.ResultJSON), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["content"] != result {
		t.Fatalf("decoded result=%q, want %q", decoded["content"], result)
	}
}

func TestMySQLRunRecorderPersistsRunEvents(t *testing.T) {
	store := &fakeTeamRunStore{}
	recorder := NewMySQLRunRecorder(store, store)
	if err := recorder.Append(context.Background(), RunEvent{TenantID: 1, RunID: "team-run-1", Sequence: 2, EventType: RunEventMemberCompleted, MemberKey: "reviewer", Status: StatusCompleted, Summary: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 || store.events[0].MemberKey != "reviewer" || store.events[0].EventType != RunEventMemberCompleted {
		t.Fatalf("events=%+v", store.events)
	}
}
