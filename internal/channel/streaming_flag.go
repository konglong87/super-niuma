package channel

import (
	"fmt"
	"strings"
)

type StreamingGlobalMode string

const (
	StreamingGlobalOff       StreamingGlobalMode = "off"
	StreamingGlobalAllowlist StreamingGlobalMode = "allowlist"
	StreamingGlobalOn        StreamingGlobalMode = "on"
)

type StreamingAccountMode string

const (
	StreamingAccountInherit  StreamingAccountMode = "inherit"
	StreamingAccountEnabled  StreamingAccountMode = "enabled"
	StreamingAccountDisabled StreamingAccountMode = "disabled"
)

type StreamingDecisionReason string

const (
	StreamingReasonEnabled         StreamingDecisionReason = "enabled"
	StreamingReasonGlobalOff       StreamingDecisionReason = "global_off"
	StreamingReasonNotAllowlisted  StreamingDecisionReason = "not_allowlisted"
	StreamingReasonAccountDisabled StreamingDecisionReason = "account_disabled"
	StreamingReasonUnsupported     StreamingDecisionReason = "provider_unsupported"
)

type StreamingDecision struct {
	Enabled bool
	Reason  StreamingDecisionReason
}

func ParseStreamingGlobalMode(value string) (StreamingGlobalMode, error) {
	switch mode := StreamingGlobalMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return StreamingGlobalOff, nil
	case StreamingGlobalOff, StreamingGlobalAllowlist, StreamingGlobalOn:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid streaming card global mode %q", value)
	}
}

func ParseStreamingAccountMode(value string) (StreamingAccountMode, error) {
	switch mode := StreamingAccountMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return StreamingAccountInherit, nil
	case StreamingAccountInherit, StreamingAccountEnabled, StreamingAccountDisabled:
		return mode, nil
	default:
		return StreamingAccountDisabled, fmt.Errorf("invalid streaming card account mode %q", value)
	}
}

// EvaluateStreamingCard decides whether incremental streaming updates may be
// sent. The final result card is governed by its own lifecycle and is never
// disabled by this evaluator.
func EvaluateStreamingCard(global StreamingGlobalMode, account StreamingAccountMode, supported bool) StreamingDecision {
	if global == "" {
		global = StreamingGlobalOff
	}
	switch global {
	case StreamingGlobalOff:
		return StreamingDecision{Reason: StreamingReasonGlobalOff}
	case StreamingGlobalAllowlist, StreamingGlobalOn:
		// Continue with the capability and account gates below.
	default:
		return StreamingDecision{Reason: StreamingReasonGlobalOff}
	}

	if !supported {
		return StreamingDecision{Reason: StreamingReasonUnsupported}
	}
	if account == "" {
		account = StreamingAccountInherit
	}
	switch account {
	case StreamingAccountInherit, StreamingAccountEnabled, StreamingAccountDisabled:
		// Continue with the account policy below.
	default:
		account = StreamingAccountDisabled
	}
	if account == StreamingAccountDisabled {
		return StreamingDecision{Reason: StreamingReasonAccountDisabled}
	}
	if global == StreamingGlobalAllowlist && account != StreamingAccountEnabled {
		return StreamingDecision{Reason: StreamingReasonNotAllowlisted}
	}
	return StreamingDecision{Enabled: true, Reason: StreamingReasonEnabled}
}
