package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestDiagnosticContractAndSafeFailureCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""}, {errors.New("private-unknown"), string(CodeUnknown)}, {ErrFailed, string(CodeUnknown)},
		{fmt.Errorf("private-wrapper: %w", ErrBudget), string(CodeBudgetDenied)},
		{context.Canceled, string(CodeCancelled)}, {context.DeadlineExceeded, string(CodeTimeout)},
		{ErrAuthority, string(CodeAuthorityRevoked)}, {ErrInvalid, string(CodeInvalidRequest)}, {ErrUnavailable, string(CodeUnavailable)},
		{diagnosticFailure(Diagnostic{}, CodeHTTPUnauthorized, errors.New("private-cause")), string(CodeHTTPUnauthorized)},
	} {
		if got := FailureCode(tc.err); got != tc.want {
			t.Fatalf("got=%s want=%s", got, tc.want)
		}
	}
	failure := diagnosticFailure(Diagnostic{}, CodeOutputTruncated, errors.New("private-cause"))
	if !errors.Is(failure, ErrFailed) || errors.Unwrap(failure) != ErrFailed {
		t.Fatal("unsafe unwrap")
	}
	// Optional field leaves legacy result encoding unchanged.
	b, err := json.Marshal(Result{})
	if err != nil || strings.Contains(string(b), "diagnostic") {
		t.Fatalf("optional field=%s err=%v", b, err)
	}
	var legacy Result
	if json.Unmarshal([]byte(`{"text":"legacy","usage":{"input_tokens":7,"output_tokens":9},"usage_known":true}`), &legacy) != nil || legacy.Diagnostic != nil || legacy.Text != "legacy" {
		t.Fatal("legacy result incompatible")
	}
	forged := Diagnostic{Code: "private-code", BodyState: "private-body", FailureStage: "private-stage", CompletionKind: "private-completion", ContentKind: "private-content", MediaType: "private-media", UsageState: "private-usage", HTTPStatus: 999, ResponseBytes: 1 << 60, RequestIndex: 1000}
	b, err = json.Marshal(forged)
	if err != nil || strings.Contains(string(b), "private-") {
		t.Fatalf("enum escaped %s", b)
	}
	var safe Diagnostic
	if json.Unmarshal(b, &safe) != nil || safe.Code != CodeUnknown || safe.HTTPStatus != 0 || safe.ResponseBytes != maxResponseBytes+1 || safe.RequestIndex != 0 {
		t.Fatalf("bounds=%s", b)
	}
	for _, u := range []Usage{{-1, 9}, {7, -1}, {0, 0}, {1 << 60, 9}} {
		e := rejectedOutcome(Diagnostic{UsageState: UsageKnown}, CodeOutputEmpty, ErrFailed, &u)
		if _, known := rejectedUsage(e); known {
			t.Fatal("unvalidated numeric usage trusted")
		}
	}
}

func TestStrictUsageValidationIndependentOfOutput(t *testing.T) {
	cases := []struct {
		name, raw string
		state     UsageState
	}{
		{"complete", diagnosticUsage, UsageKnown},
		{"cache_reasoning", `{"prompt_tokens":7,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":9}}`, UsageKnown},
		{"missing", `null`, UsageMissing}, {"empty", `{}`, UsageMissing}, {"partial", `{"prompt_tokens":7}`, UsagePartial},
		{"array", `[]`, UsageInvalid}, {"string", `{"prompt_tokens":"7","completion_tokens":9}`, UsageInvalid},
		{"fraction", `{"prompt_tokens":7,"completion_tokens":9.5}`, UsageInvalid},
		{"negative", `{"prompt_tokens":7,"completion_tokens":-1}`, UsageInvalid},
		{"null_counter", `{"prompt_tokens":null,"completion_tokens":9}`, UsageInvalid},
		{"zero", `{"prompt_tokens":0,"completion_tokens":0}`, UsageInvalid},
		{"overflow", `{"prompt_tokens":9999999999999999999999999999999,"completion_tokens":9}`, UsageInvalid},
		{"duplicate", `{"prompt_tokens":7,"prompt_tokens":8,"completion_tokens":9}`, UsageInvalid},
		{"cache_exceeds", `{"prompt_tokens":7,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":8}}`, UsageInvalid},
		{"reasoning_exceeds", `{"prompt_tokens":7,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":10}}`, UsageInvalid},
		{"duplicate_detail", `{"prompt_tokens":7,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":1,"reasoning_tokens":2}}`, UsageInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := chatFixture("length", `{"role":"assistant","content":null}`, tc.raw)
			u, state, err := strictUsage(config.ProviderProtocolOpenAIChatCompletions, []byte(body))
			if state != tc.state || (err == nil) != (tc.state == UsageKnown) {
				t.Fatalf("state=%s err=%v", state, err)
			}
			if state == UsageKnown {
				amount, err := observedUsage(u)
				if err != nil || amount != (Usage{7, 9}) {
					t.Fatalf("usage=%+v err=%v", amount, err)
				}
			}
		})
	}
	for _, body := range []string{
		`{"usage":` + diagnosticUsage + `,"usage":` + diagnosticUsage + `}`,
		`{"usage":` + diagnosticUsage + `}{}`,
		`{"usage":` + diagnosticUsage,
		`null`,
	} {
		_, state, err := strictUsage(config.ProviderProtocolOpenAIChatCompletions, []byte(body))
		if state != UsageNotObserved || FailureCode(err) != string(CodeJSONInvalid) {
			t.Fatal("ambiguous/incomplete envelope trusted")
		}
	}
	for _, tc := range []struct {
		raw   string
		want  int
		known bool
	}{
		{`{"input_tokens":7,"output_tokens":9,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}`, 15, true},
		{`{"input_tokens":7,"output_tokens":9,"cache_creation":{"ephemeral_1h_input_tokens":2,"ephemeral_5m_input_tokens":3}}`, 12, true},
		{`{"input_tokens":7,"output_tokens":9,"cache_creation_input_tokens":1,"cache_creation":{"ephemeral_1h_input_tokens":2}}`, 0, false},
	} {
		u, state, err := strictUsage(config.ProviderProtocolAnthropicMessages, []byte(`{"usage":`+tc.raw+`}`))
		if (state == UsageKnown) != tc.known || (err == nil) != tc.known {
			t.Fatalf("cache=%s state=%s err=%v", tc.raw, state, err)
		}
		if tc.known {
			amount, _ := observedUsage(u)
			if amount.InputTokens != tc.want {
				t.Fatal("cache undercount")
			}
		}
	}
}
