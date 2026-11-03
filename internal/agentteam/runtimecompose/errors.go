package runtimecompose

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
)

const (
	CodeConfig      = "text_config"
	CodeAuth        = "text_auth"
	CodePolicy      = "text_policy"
	CodeBudget      = "text_budget"
	CodeUnsupported = "text_unsupported"
	CodeTransient   = "text_transient"
	CodeProvider    = "text_provider"
	CodeEmptyOutput = "text_empty_output"
	CodeUsage       = "text_invalid_usage"
	CodeStopReason  = "text_stop_reason"
	CodeCancelled   = "text_cancelled"
	CodeTimedOut    = "text_timed_out"
)

// ExecutionError is safe to display and persist. It deliberately does NOT wrap
// provider errors, which can contain credentials, thinking or raw HTTP bodies.
type ExecutionError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *ExecutionError) Error() string { return e.Message }

// PublicCode marks this as a classified, display-safe API error. Unknown DB or
// SDK errors must not be exposed simply because they implement error.
func (e *ExecutionError) PublicCode() string { return e.Code }
func (e *ExecutionError) Is(target error) bool {
	return e.Code == CodeCancelled && target == context.Canceled || e.Code == CodeTimedOut && target == context.DeadlineExceeded
}
func failure(code, message string, retryable bool) *ExecutionError {
	return &ExecutionError{Code: code, Message: message, Retryable: retryable}
}

var statusPattern = regexp.MustCompile(`(?:^|[^0-9])([45][0-9]{2})(?:[^0-9]|$)`)

func classify(ctx context.Context, err error) *ExecutionError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return failure(CodeTimedOut, "Text execution deadline exceeded.", false)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return failure(CodeCancelled, "Text execution cancelled.", false)
	}
	var typed *ExecutionError
	if errors.As(err, &typed) {
		return typed
	}
	// Inspect, but never retain, provider text. anthropic.Client aggregates route
	// failures into a string, so HTTP status matching must also support that form.
	message := strings.ToLower(err.Error())
	for _, match := range statusPattern.FindAllStringSubmatch(message, -1) {
		switch match[1] {
		case "401", "403":
			return failure(CodeAuth, "Provider authentication or access was rejected.", false)
		}
	}
	if strings.Contains(message, "invalid_api_key") || strings.Contains(message, "authentication") || strings.Contains(message, "unauthorized") {
		return failure(CodeAuth, "Provider authentication was rejected.", false)
	}
	if strings.Contains(message, "content_filter") || strings.Contains(message, "safety") || strings.Contains(message, "policy") {
		return failure(CodePolicy, "Provider policy rejected text execution.", false)
	}
	for _, match := range statusPattern.FindAllStringSubmatch(message, -1) {
		if match[1][0] == '5' || match[1] == "408" || match[1] == "409" || match[1] == "429" {
			return failure(CodeTransient, "Provider is temporarily unavailable; retry is permitted.", true)
		}
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") || strings.Contains(message, "connection reset") || strings.Contains(message, "connection refused") || strings.Contains(message, "unexpected eof") || strings.Contains(message, "cooling down") {
		return failure(CodeTransient, "Provider connection failed temporarily; retry is permitted.", true)
	}
	if strings.Contains(message, "400") || strings.Contains(message, "404") || strings.Contains(message, "422") || strings.Contains(message, "unsupported") || strings.Contains(message, "credentials") {
		return failure(CodeConfig, "Provider configuration or request is invalid.", false)
	}
	return failure(CodeProvider, "Provider text execution failed.", false)
}
