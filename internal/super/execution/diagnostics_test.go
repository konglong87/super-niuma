package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/query"
)

// No sockets or user configuration: only the HTTP RoundTripper is replaced.
// The production transport, meter, fresh query session and registry all run.
func diagnosticRunner(t *testing.T, protocol config.ProviderProtocol, trip roundTripFunc, logs *bytes.Buffer) *Runner {
	t.Helper()
	cfg := fixtureConfig()
	cfg.ProviderProtocol = protocol
	if protocol != config.ProviderProtocolAnthropicMessages {
		cfg.Provider = "openai"
	}
	cfg.BaseURL = "https://fixture.invalid/v1"
	r := New(func(context.Context) (config.Config, error) { return cfg, nil }, slog.New(slog.NewJSONHandler(logs, nil)))
	r.newClient = func(c config.Config) (query.MessageStreamer, error) {
		inner, err := newHTTPStreamer(c)
		if err != nil {
			return nil, err
		}
		inner.(*httpStreamer).client.Transport = trip
		return inner, nil
	}
	return r
}
func fixtureHTTP(status int, media, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(body))}
}
func chatFixture(finish, message, usage string) string {
	return `{"choices":[{"finish_reason":"` + finish + `","message":` + message + `}],"usage":` + usage + `}`
}

const diagnosticUsage = `{"prompt_tokens":7,"completion_tokens":9}`
const diagnosticWrite = `{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"super_write","arguments":"{\"path\":\"artifacts/rejected.txt\",\"content\":\"must-not-write\"}"}}]}`

func TestRejectedOutcomePreservesKnownUsageThroughRealQuery(t *testing.T) {
	cases := []struct {
		name, body string
		code       ErrorCode
		completion CompletionKind
		content    ContentKind
	}{
		{"length_null", chatFixture("length", `{"role":"assistant","content":null}`, diagnosticUsage), CodeOutputTruncated, CompletionTruncated, ContentEmpty},
		{"length_text", chatFixture("length", `{"role":"assistant","content":"private-partial"}`, diagnosticUsage), CodeOutputTruncated, CompletionTruncated, ContentText},
		{"null_content", chatFixture("stop", `{"role":"assistant","content":null}`, diagnosticUsage), CodeOutputEmpty, CompletionComplete, ContentEmpty},
		{"empty_content", chatFixture("stop", `{"role":"assistant","content":""}`, diagnosticUsage), CodeOutputEmpty, CompletionComplete, ContentEmpty},
		{"reasoning_only", chatFixture("stop", `{"role":"assistant","content":null,"reasoning_content":"private-reasoning"}`, diagnosticUsage), CodeOutputEmpty, CompletionComplete, ContentReasoning},
		{"length_with_write", chatFixture("length", diagnosticWrite, diagnosticUsage), CodeOutputTruncated, CompletionTruncated, ContentToolCall},
		{"unknown_server_tool", chatFixture("tool_calls", `{"role":"assistant","tool_calls":[{"type":"web_search","id":"call-1"}]}`, diagnosticUsage), CodeOutputUnsupported, CompletionToolCall, ContentToolCall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := fixtureRequest(t)
			var logs bytes.Buffer
			calls := 0
			runner := diagnosticRunner(t, config.ProviderProtocolOpenAIChatCompletions, func(*http.Request) (*http.Response, error) {
				calls++
				return fixtureHTTP(http.StatusOK, "application/json", tc.body), nil
			}, &logs)
			got, err := runner.Execute(context.Background(), req)
			if !errors.Is(err, ErrFailed) || FailureCode(err) != string(tc.code) {
				t.Fatalf("code=%s err=%v", FailureCode(err), err)
			}
			if got.Text != "" || got.ToolCalls != 0 || len(got.Writes) != 0 || calls != 1 {
				t.Fatalf("rejected outcome escaped: %+v calls=%d", got, calls)
			}
			if got.Usage != (Usage{7, 9}) || !got.UsageKnown {
				t.Fatalf("known usage erased: %+v", got)
			}
			b := req.Budget.(*fakeBudget)
			if len(b.reserved) != 1 || len(b.settled) != 1 || !b.settled[0].known || b.settled[0].usage != (Usage{7, 9}) {
				t.Fatalf("bad settlement: %+v", b)
			}
			d := got.Diagnostic
			if d == nil || d.Code != tc.code || d.HTTPStatus != 200 || d.ResponseBytes != int64(len(tc.body)) || d.MediaType != MediaJSON || d.BodyState != BodyComplete || d.UsageState != UsageKnown || d.FailureStage != StageOutput || d.CompletionKind != tc.completion || d.ContentKind != tc.content || d.RequestIndex != 1 {
				t.Fatalf("diagnostic=%+v", d)
			}
			if _, err := os.Stat(filepath.Join(req.Workspace, "artifacts/rejected.txt")); !os.IsNotExist(err) {
				t.Fatal("rejected tool wrote file")
			}
			serialized, _ := json.Marshal(got)
			for _, secret := range []string{"private-partial", "private-reasoning", "fixture.invalid", "secret-api-key", "must-not-write", req.Objective} {
				if strings.Contains(logs.String()+string(serialized)+err.Error(), secret) {
					t.Fatal("unsafe diagnostic")
				}
			}
		})
	}
}

func TestRejectedUsageAcrossProtocolsAndBudgetOverrun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protocol config.ProviderProtocol
		body     string
		code     ErrorCode
		usage    Usage
	}{
		{"anthropic_length", config.ProviderProtocolAnthropicMessages, `{"role":"assistant","content":[],"stop_reason":"max_tokens","usage":{"input_tokens":7,"output_tokens":9}}`, CodeOutputTruncated, Usage{7, 9}},
		{"anthropic_server_tool", config.ProviderProtocolAnthropicMessages, `{"role":"assistant","content":[{"type":"server_tool_use","name":"web_search","id":"x"}],"stop_reason":"tool_use","usage":{"input_tokens":7,"output_tokens":9}}`, CodeOutputUnsupported, Usage{7, 9}},
		{"responses_incomplete", config.ProviderProtocolOpenAIResponses, `{"status":"incomplete","output":[],"usage":{"input_tokens":7,"output_tokens":9}}`, CodeOutputTruncated, Usage{7, 9}},
		{"responses_reasoning", config.ProviderProtocolOpenAIResponses, `{"status":"completed","output":[{"type":"reasoning","summary":[]}],"usage":{"input_tokens":7,"output_tokens":9}}`, CodeOutputUnsupported, Usage{7, 9}},
		{"responses_server_tool", config.ProviderProtocolOpenAIResponses, `{"status":"completed","output":[{"type":"web_search_call"}],"usage":{"input_tokens":7,"output_tokens":9}}`, CodeOutputUnsupported, Usage{7, 9}},
		{"rejected_over_budget", config.ProviderProtocolOpenAIChatCompletions, chatFixture("length", `{"role":"assistant","content":null}`, `{"prompt_tokens":7,"completion_tokens":201}`), CodeBudgetExceeded, Usage{7, 201}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := fixtureRequest(t)
			var logs bytes.Buffer
			calls := 0
			r := diagnosticRunner(t, tc.protocol, func(*http.Request) (*http.Response, error) {
				calls++
				return fixtureHTTP(200, "application/json", tc.body), nil
			}, &logs)
			got, err := r.Execute(context.Background(), req)
			b := req.Budget.(*fakeBudget)
			if FailureCode(err) != string(tc.code) || !got.UsageKnown || got.Usage != tc.usage || got.Text != "" || got.ToolCalls != 0 || len(got.Writes) != 0 || calls != 1 || len(b.settled) != 1 || !b.settled[0].known || b.settled[0].usage != tc.usage {
				t.Fatalf("result=%+v err=%v settled=%+v", got, err, b.settled)
			}
		})
	}
}

func TestKnownUsageSurvivesCancellationRevocationAndSettlementFailure(t *testing.T) {
	for _, phase := range []string{"cancel_in_flight", "cancel_after_response", "revoke_after_response", "settle_failure"} {
		t.Run(phase, func(t *testing.T) {
			req := fixtureRequest(t)
			var logs bytes.Buffer
			calls := 0
			revoked := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req.CheckAuthority = func(context.Context) error {
				if revoked {
					return errors.New("private-authority")
				}
				return nil
			}
			b := req.Budget.(*fakeBudget)
			b.settleErr = phase == "settle_failure"
			r := diagnosticRunner(t, config.ProviderProtocolOpenAIChatCompletions, func(*http.Request) (*http.Response, error) {
				calls++
				if phase == "cancel_in_flight" {
					cancel()
					return nil, errors.New("private-network")
				}
				if phase == "cancel_after_response" {
					cancel()
				}
				if phase == "revoke_after_response" {
					revoked = true
				}
				return fixtureHTTP(200, "application/json", chatFixture("tool_calls", diagnosticWrite, diagnosticUsage)), nil
			}, &logs)
			got, err := r.Execute(ctx, req)
			want := CodeCancelled
			if phase == "revoke_after_response" {
				want = CodeAuthorityRevoked
			}
			if phase == "settle_failure" {
				want = CodeBudgetSettlement
			}
			known := phase != "cancel_in_flight"
			if FailureCode(err) != string(want) || got.Diagnostic == nil || got.Diagnostic.Code != want || got.UsageKnown != known || got.Text != "" || got.ToolCalls != 0 || len(got.Writes) != 0 || calls != 1 || len(b.settled) != 1 || b.settled[0].known != known {
				t.Fatalf("result=%+v err=%v settle=%+v", got, err, b.settled)
			}
			if known && (got.Usage != (Usage{7, 9}) || b.settled[0].usage != (Usage{7, 9}) || got.Diagnostic.HTTPStatus != 200 || got.Diagnostic.UsageState != UsageKnown) {
				t.Fatal("known usage/metadata lost")
			}
			if phase == "revoke_after_response" && got.Diagnostic.FailureStage != StageAuthority {
				t.Fatal("stale failure stage")
			}
		})
	}
}
