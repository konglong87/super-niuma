package control

import (
	"context"
	"errors"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/execution"
)

func TestFailedExecutionRetainsSafeDiagnosticAndUsageNotOutput(t *testing.T) {
	e := testEngine()
	diag := &execution.Diagnostic{Code: execution.CodeOutputTruncated, HTTPStatus: 200, BodyState: execution.BodyComplete, CompletionKind: execution.CompletionTruncated, UsageState: execution.UsageKnown}
	e.Executor = fakeExecutor{fn: func(context.Context, execution.Request) (execution.Result, error) {
		return execution.Result{Text: "untrusted failed output must not become dependency input", Diagnostic: diag, Usage: execution.Usage{InputTokens: 7, OutputTokens: 9}, UsageKnown: true}, errors.New("provider-specific raw message must not be persisted")
	}}
	r, _, err := e.Start(context.Background(), Principal{Scope: LocalScope, Owner: LocalOwner}, "diagnostic", "objective", "", testGrant(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Step(context.Background(), "worker"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Store.GetRun(context.Background(), r.Scope, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	j := got.Jobs[0]
	if got.Status != Failed || j.Result.Diagnostic == nil || j.Result.Diagnostic.Code != execution.CodeOutputTruncated || !j.Result.UsageKnown || j.Result.Usage.InputTokens != 7 || j.Result.Usage.OutputTokens != 9 || j.Result.Text != "" || j.SafeCode != execution.FailureCode(errors.New("unknown")) {
		t.Fatalf("failure facts missing or raw output persisted: status=%s job=%+v", got.Status, j)
	}
}
