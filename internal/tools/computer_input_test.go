package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/permissions"
)

const computerInputTestSecret = "computer-input-secret"

func assertNoComputerInputSecret(t *testing.T, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), computerInputTestSecret) {
		t.Fatalf("secret leaked: %s", data)
	}
}
func TestRedactToolInputWhitelistAndIdempotence(t *testing.T) {
	input := json.RawMessage(`{"action":"type","text":"秘密ab","key":"computer-input-secret","keys":["computer-input-secret"],"unknown":"computer-input-secret","session_id":"computer-input-secret"}`)
	result := RedactToolInput("ComputerUse", input)
	var summary computerInputSummary
	if err := json.Unmarshal(result, &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.Redacted || summary.Action != "type" || summary.TextLength != 4 || summary.KeysCount != 1 || summary.InputBytes != len(input) || summary.KeyLength != len(computerInputTestSecret) {
		t.Fatalf("summary=%+v", summary)
	}
	assertNoComputerInputSecret(t, json.RawMessage(result))
	if !bytes.Equal(result, RedactToolInput("ComputerUse", result)) {
		t.Fatal("redaction is not idempotent")
	}
	if !bytes.Equal(RedactToolInput("Echo", input), input) {
		t.Fatal("ordinary tool changed")
	}
	if !strings.Contains(string(input), computerInputTestSecret) {
		t.Fatal("redaction mutated execution input")
	}
}
func TestRedactToolInputMalformedUnknownAndForgedSummary(t *testing.T) {
	for _, input := range []string{
		`{"action":"computer-input-secret","text":"computer-input-secret"}`,
		`{"action":"type","text":"computer-input-secret"`,
		`"computer-input-secret"`,
		`{"action":"type","text":"computer-input-secret"} {"leak":"computer-input-secret"}`,
		`{"redacted":true,"action":"computer-input-secret","input_bytes":-5,"text_length":-9,"keys_count":-3,"hidden":"computer-input-secret"}`,
		`{"action":"resume","text":"computer-input-secret"}`,
	} {
		t.Run(input, func(t *testing.T) {
			redacted := RedactToolInput("ComputerUse", json.RawMessage(input))
			assertNoComputerInputSecret(t, json.RawMessage(redacted))
			var summary computerInputSummary
			if err := json.Unmarshal(redacted, &summary); err != nil {
				t.Fatal(err)
			}
			if summary.Action != computerInputUnknownAction || summary.InputBytes < 0 || summary.TextLength < 0 || summary.KeysCount < 0 {
				t.Fatalf("unsafe summary: %s", redacted)
			}
		})
	}
}

type computerPermissionSpy struct {
	input        json.RawMessage
	test         *testing.T
	directUpdate bool
}

func (*computerPermissionSpy) Name() string        { return "ComputerUse" }
func (*computerPermissionSpy) Description() string { return "test permission boundary" }
func (*computerPermissionSpy) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (s *computerPermissionSpy) Run(_ context.Context, input json.RawMessage, tc Context) Result {
	s.input = append(json.RawMessage(nil), input...)
	if s.directUpdate && tc.PermissionUpdate != nil {
		err := tc.PermissionUpdate(PermissionUpdate{ToolName: s.Name(), Input: input, Request: computerInputTestSecret, Rule: computerInputTestSecret, Reason: computerInputTestSecret, Destination: "project", Decision: "allow"})
		if err != nil {
			return Result{Content: err.Error(), IsError: true}
		}
	}
	return Result{Content: "executed"}
}
func TestComputerGuardPermissionPayloadsDoNotExposeInput(t *testing.T) {
	input := json.RawMessage(`{"action":"type","text":"computer-input-secret","name":"computer-input-secret"}`)
	spy := &computerPermissionSpy{test: t, directUpdate: true}
	auditCount, promptCount, updateCount := 0, 0, 0
	guarded := Guard(spy, permissions.Policy{AlwaysAsk: []string{"ComputerUse"}})
	result := guarded.Run(context.Background(), input, Context{
		PermissionAudit: func(audit PermissionAudit) { auditCount++; assertNoComputerInputSecret(t, audit) },
		PermissionPrompt: func(_ context.Context, request PermissionPromptRequest) PermissionPromptResponse {
			promptCount++
			assertNoComputerInputSecret(t, request)
			if !request.OneShot {
				t.Fatal("computer permission can persist a raw rule")
			}
			return PermissionPromptResponse{Allowed: true, Reason: computerInputTestSecret, Rule: computerInputTestSecret, Destination: "project", Payload: json.RawMessage(`{"text":"computer-input-secret"}`)}
		},
		PermissionUpdate: func(update PermissionUpdate) error {
			updateCount++
			assertNoComputerInputSecret(t, update)
			if update.Destination != "once" || update.Rule != "" {
				t.Fatalf("unsafe persistent permission: %+v", update)
			}
			return nil
		},
	})
	if result.IsError || !bytes.Equal(spy.input, input) || auditCount != 2 || promptCount != 1 || updateCount != 1 {
		t.Fatalf("result=%+v counts=%d/%d/%d input=%s", result, auditCount, promptCount, updateCount, spy.input)
	}
}
func TestComputerPermissionDenialAndUpdateErrorAreSafe(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny", true: "update error"}[allowed], func(t *testing.T) {
			spy := &computerPermissionSpy{directUpdate: true}
			result := Guard(spy, permissions.Policy{AlwaysAsk: []string{"ComputerUse"}}).Run(context.Background(), json.RawMessage(`{"action":"type","text":"computer-input-secret"}`), Context{
				PermissionPrompt: func(context.Context, PermissionPromptRequest) PermissionPromptResponse {
					return PermissionPromptResponse{Allowed: allowed, Reason: computerInputTestSecret}
				},
				PermissionUpdate: func(PermissionUpdate) error { return errors.New(computerInputTestSecret) },
			})
			if !result.IsError {
				t.Fatal("expected denial/error")
			}
			assertNoComputerInputSecret(t, result)
			if !allowed && spy.input != nil {
				t.Fatal("denied input executed")
			}
		})
	}
}
func TestPermissionRedactionPreservesOtherTools(t *testing.T) {
	req := PermissionPromptRequest{ToolName: "Echo", Input: json.RawMessage(`{"text":"computer-input-secret"}`), Request: computerInputTestSecret, Rule: computerInputTestSecret}
	if got := RedactPermissionPromptRequest(req); !bytes.Equal(got.Input, req.Input) || got.Request != req.Request || got.Rule != req.Rule {
		t.Fatal("ordinary request changed")
	}
	response := PermissionPromptResponse{Allowed: true, Destination: "project", Reason: computerInputTestSecret}
	if got := RedactPermissionPromptResponse("Echo", response); got.Reason != response.Reason || got.Destination != response.Destination {
		t.Fatal("ordinary response changed")
	}
	audit := PermissionAudit{ToolName: "Echo", Request: computerInputTestSecret, Reason: computerInputTestSecret}
	if got := RedactPermissionAudit(audit); got != audit {
		t.Fatal("ordinary audit changed")
	}
	update := PermissionUpdate{ToolName: "Echo", Rule: computerInputTestSecret, Destination: "project"}
	if got := RedactPermissionUpdate(update); got.Rule != update.Rule || got.Destination != update.Destination {
		t.Fatal("ordinary update changed")
	}
}
