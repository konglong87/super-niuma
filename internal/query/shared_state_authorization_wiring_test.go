package query

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/tools"
)

type sharedStateAuthorizationProbe struct {
	authorizations []gitpolicy.Authorization
}

type sharedStateAuthorizationProbeStreamer struct {
	calls int
}

func (s *sharedStateAuthorizationProbeStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type: "tool_use", ID: "toolu_auth_probe", Name: "Task", Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func (p *sharedStateAuthorizationProbe) Name() string        { return "Task" }
func (p *sharedStateAuthorizationProbe) Description() string { return "probe" }
func (p *sharedStateAuthorizationProbe) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (p *sharedStateAuthorizationProbe) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	p.authorizations = append(p.authorizations, toolContext.SharedStateAuthorization)
	return tools.Result{Content: "ok"}
}

func TestSessionHandsCurrentTurnSharedStateAuthorizationToDelegatedTools(t *testing.T) {
	probe := &sharedStateAuthorizationProbe{}
	streamer := &sharedStateAuthorizationProbeStreamer{}
	session := New(streamer, tools.NewRegistry(probe), Options{Model: "test", MaxTurns: 2, CWD: t.TempDir()})
	if _, err := session.Run(context.Background(), "Commit the current changes.", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(probe.authorizations) != 1 || !probe.authorizations[0].Allows(gitpolicy.Effect{Operation: gitpolicy.OperationCommit}) {
		t.Fatalf("current-turn commit authorization was not propagated: %+v", probe.authorizations)
	}
}
