package channel

import (
	"context"
	"testing"
)

func TestInboundDispositions(t *testing.T) {
	t.Parallel()

	accepted := AcceptInbound()
	if !accepted.Ack || accepted.Retryable || accepted.ErrorCode != "" {
		t.Fatalf("AcceptInbound() = %+v", accepted)
	}

	ignored := IgnoreInbound("policy_denied")
	if !ignored.Ack || ignored.Retryable || ignored.ErrorCode != "policy_denied" {
		t.Fatalf("IgnoreInbound() = %+v", ignored)
	}

	retry := RetryInbound("storage_unavailable")
	if retry.Ack || !retry.Retryable || retry.ErrorCode != "storage_unavailable" {
		t.Fatalf("RetryInbound() = %+v", retry)
	}
}

func TestInboundDispositionValidateRejectsInvalidStates(t *testing.T) {
	t.Parallel()

	longCode := "a"
	for len(longCode) <= 128 {
		longCode += "a"
	}
	tests := []struct {
		name  string
		value InboundDisposition
	}{
		{name: "zero", value: InboundDisposition{}},
		{name: "conflicting flags", value: InboundDisposition{Ack: true, Retryable: true, ErrorCode: "conflict"}},
		{name: "empty retry code", value: InboundDisposition{Retryable: true}},
		{name: "unsafe ack code", value: InboundDisposition{Ack: true, ErrorCode: "provider raw text"}},
		{name: "unsafe retry code", value: InboundDisposition{Retryable: true, ErrorCode: "secret/token"}},
		{name: "too long", value: InboundDisposition{Ack: true, ErrorCode: longCode}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.value.Validate(); err == nil {
				t.Fatalf("Validate() = nil for %+v", tt.value)
			}
		})
	}
}

func TestInboundDispositionValidateAcceptsSafeCodes(t *testing.T) {
	t.Parallel()

	for _, disposition := range []InboundDisposition{
		AcceptInbound(),
		IgnoreInbound("policy_denied.v2"),
		RetryInbound("storage-unavailable_2"),
	} {
		if err := disposition.Validate(); err != nil {
			t.Fatalf("Validate(%+v) = %v", disposition, err)
		}
	}
}

func TestIgnoreInboundDefaultsMissingReason(t *testing.T) {
	t.Parallel()

	ignored := IgnoreInbound("")
	if !ignored.Ack || ignored.Retryable || ignored.ErrorCode != "policy_denied" {
		t.Fatalf("IgnoreInbound(\"\") = %+v, want default policy_denied", ignored)
	}
	if err := ignored.Validate(); err != nil {
		t.Fatalf("IgnoreInbound(\"\").Validate() = %v", err)
	}
}

func TestRunInputUsesTenantSessionID(t *testing.T) {
	t.Parallel()

	input := RunInput{TenantSessionID: 42, ModelProvider: "anthropic"}
	if input.TenantSessionID != 42 || input.ModelProvider != "anthropic" {
		t.Fatalf("RunInput = %+v", input)
	}
}

func TestBoundaryEnumsValidateKnownValuesOnly(t *testing.T) {
	t.Parallel()

	if !ThreadSourceEvent.Valid() || !ThreadSourceRawMessageLookup.Valid() || !ThreadSourceNone.Valid() {
		t.Fatal("known thread sources should be valid")
	}
	if ThreadSource("").Valid() || ThreadSource("unknown").Valid() {
		t.Fatal("empty and unknown thread sources should be invalid")
	}
	if !HealthReady.Valid() || !HealthStarting.Valid() || !HealthDegraded.Valid() || !HealthFailed.Valid() {
		t.Fatal("known health statuses should be valid")
	}
	if HealthStatus("").Valid() || HealthStatus("unknown").Valid() {
		t.Fatal("empty and unknown health statuses should be invalid")
	}
	if !DeltaText.Valid() || !DeltaTool.Valid() || !DeltaStatus.Valid() {
		t.Fatal("known delta kinds should be valid")
	}
	if DeltaKind("").Valid() || DeltaKind("unknown").Valid() {
		t.Fatal("empty and unknown delta kinds should be invalid")
	}
}

var (
	_ Adapter            = (*fakeAdapter)(nil)
	_ ConversationRunner = (*fakeRunner)(nil)
)

type fakeAdapter struct{}

func (*fakeAdapter) Provider() Provider                              { return ProviderFeishu }
func (*fakeAdapter) AccountID() string                               { return "account" }
func (*fakeAdapter) Start(_ context.Context, _ InboundHandler) error { return nil }
func (*fakeAdapter) Stop(_ context.Context) error                    { return nil }
func (*fakeAdapter) Deliver(_ context.Context, _ OutboundOperation) (DeliveryReceipt, error) {
	return DeliveryReceipt{MessageID: "message"}, nil
}
func (*fakeAdapter) ResolveThread(_ context.Context, message InboundMessage) (ThreadResolution, error) {
	return ThreadResolution{ThreadID: message.ExternalThreadID, Source: ThreadSourceEvent}, nil
}
func (*fakeAdapter) Capabilities() Capabilities {
	return Capabilities{FinalCard: true, StreamingCard: true, MessageUpdate: true}
}
func (*fakeAdapter) Health(_ context.Context) Health {
	return Health{Status: HealthReady}
}

type fakeRunner struct{}

func (*fakeRunner) Run(_ context.Context, _ RunInput) (RunResult, error) {
	return RunResult{FinalText: "ok"}, nil
}
func (*fakeRunner) RunStream(_ context.Context, _ RunInput, _ DeltaSink) (RunResult, error) {
	return RunResult{FinalText: "ok"}, nil
}
