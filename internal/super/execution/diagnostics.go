package execution

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"strings"
)

type ErrorCode string
type MediaType string
type UsageState string
type BodyState string
type FailureStage string
type CompletionKind string
type ContentKind string

const (
	BodyUnobserved        BodyState      = "unobserved"
	BodyPartial           BodyState      = "partial"
	BodyComplete          BodyState      = "complete"
	BodySizeLimit         BodyState      = "size_limit_hit"
	StageNone             FailureStage   = "none"
	StageUnknown          FailureStage   = "unknown"
	StageRequest          FailureStage   = "request"
	StageAuthority        FailureStage   = "authority"
	StageBudget           FailureStage   = "budget"
	StageNetwork          FailureStage   = "network"
	StageHTTP             FailureStage   = "http"
	StageMedia            FailureStage   = "media_type"
	StageBody             FailureStage   = "body"
	StageJSON             FailureStage   = "json"
	StageUsage            FailureStage   = "usage"
	StageOutput           FailureStage   = "output"
	StageCallback         FailureStage   = "callback"
	CompletionUnknown     CompletionKind = "unknown"
	CompletionComplete    CompletionKind = "complete"
	CompletionToolCall    CompletionKind = "tool_call"
	CompletionTruncated   CompletionKind = "truncated"
	CompletionRefused     CompletionKind = "refused"
	ContentUnknown        ContentKind    = "unknown"
	ContentEmpty          ContentKind    = "empty"
	ContentText           ContentKind    = "text"
	ContentToolCall       ContentKind    = "tool_call"
	ContentReasoning      ContentKind    = "reasoning"
	ContentMixed          ContentKind    = "mixed"
	ContentUnsupported    ContentKind    = "unsupported"
	CodeOK                ErrorCode      = "execution_ok"
	CodeUnknown           ErrorCode      = "execution_unknown"
	CodeInvalidRequest    ErrorCode      = "execution_invalid_request"
	CodeUnavailable       ErrorCode      = "execution_unavailable"
	CodeAuthorityRevoked  ErrorCode      = "execution_authority_revoked"
	CodeBudgetDenied      ErrorCode      = "execution_budget_denied"
	CodeBudgetExceeded    ErrorCode      = "execution_budget_exceeded"
	CodeBudgetSettlement  ErrorCode      = "execution_budget_settlement_failed"
	CodeCancelled         ErrorCode      = "execution_cancelled"
	CodeTimeout           ErrorCode      = "execution_timeout"
	CodeNetwork           ErrorCode      = "execution_network_failed"
	CodeHTTPUnauthorized  ErrorCode      = "execution_http_unauthorized"
	CodeHTTPForbidden     ErrorCode      = "execution_http_forbidden"
	CodeHTTPRateLimited   ErrorCode      = "execution_http_rate_limited"
	CodeHTTPFailure       ErrorCode      = "execution_http_failed"
	CodeBodyRead          ErrorCode      = "execution_body_read_failed"
	CodeBodyTooLarge      ErrorCode      = "execution_body_too_large"
	CodeMediaType         ErrorCode      = "execution_media_type_unsupported"
	CodeJSONInvalid       ErrorCode      = "execution_json_invalid"
	CodeUsageMissing      ErrorCode      = "execution_usage_missing"
	CodeUsageInvalid      ErrorCode      = "execution_usage_invalid"
	CodeOutputTruncated   ErrorCode      = "execution_output_truncated"
	CodeOutputEmpty       ErrorCode      = "execution_output_empty"
	CodeOutputUnsupported ErrorCode      = "execution_output_unsupported"
	CodeOutputInvalid     ErrorCode      = "execution_output_invalid"
	CodeOutputTooLarge    ErrorCode      = "execution_output_too_large"
	CodeOutputRefused     ErrorCode      = "execution_output_refused"

	MediaUnknown     MediaType = "unknown"
	MediaMissing     MediaType = "missing"
	MediaJSON        MediaType = "json"
	MediaEventStream MediaType = "event_stream"
	MediaText        MediaType = "text"
	MediaHTML        MediaType = "html"
	MediaOther       MediaType = "other"
	MediaInvalid     MediaType = "invalid"

	UsageNotObserved UsageState = "not_observed"
	UsageKnown       UsageState = "known"
	UsageMissing     UsageState = "missing"
	UsagePartial     UsageState = "partial"
	UsageInvalid     UsageState = "invalid"
)

// FailureCode never calls Error() or returns a provider-supplied string. The
// original sentinel remains available via errors.Is on a diagnosed failure.
func FailureCode(err error) string {
	if err == nil {
		return ""
	}
	var failure *diagnosedFailure
	if errors.As(err, &failure) && failure != nil {
		code := safeCode(failure.diagnostic.Code)
		if code == CodeOK {
			code = CodeUnknown
		}
		return string(code)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return string(CodeTimeout)
	case errors.Is(err, context.Canceled):
		return string(CodeCancelled)
	case errors.Is(err, ErrAuthority):
		return string(CodeAuthorityRevoked)
	case errors.Is(err, ErrBudget):
		return string(CodeBudgetDenied)
	case errors.Is(err, ErrInvalid):
		return string(CodeInvalidRequest)
	case errors.Is(err, ErrUnavailable):
		return string(CodeUnavailable)
	default:
		return string(CodeUnknown)
	}
}

type diagnosedFailure struct {
	diagnostic    Diagnostic
	cause         error
	reportedUsage *Usage
}

func (e *diagnosedFailure) Error() string { return string(safeCode(e.diagnostic.Code)) }
func (e *diagnosedFailure) Unwrap() error { return e.cause }
func diagnosticFailure(d Diagnostic, code ErrorCode, cause error) error {
	if code == CodeOK {
		code = CodeUnknown
	}
	d.Code = code
	// Never retain an arbitrary original error, even as an unwrap cause.
	switch cause {
	case ErrAuthority, ErrBudget, ErrInvalid, ErrUnavailable, context.Canceled, context.DeadlineExceeded:
	default:
		cause = ErrFailed
	}
	return &diagnosedFailure{diagnostic: d.safe(), cause: cause}
}
func codedFailure(code ErrorCode) error { return diagnosticFailure(Diagnostic{}, code, ErrFailed) }
func failureDiagnostic(err error) (Diagnostic, bool) {
	var failure *diagnosedFailure
	if errors.As(err, &failure) && failure != nil {
		return failure.diagnostic.safe(), true
	}
	return Diagnostic{}, false
}
func safeCode(code ErrorCode) ErrorCode {
	switch code {
	case CodeOK, CodeUnknown, CodeInvalidRequest, CodeUnavailable, CodeAuthorityRevoked, CodeBudgetDenied, CodeBudgetExceeded, CodeBudgetSettlement, CodeCancelled, CodeTimeout, CodeNetwork, CodeHTTPUnauthorized, CodeHTTPForbidden, CodeHTTPRateLimited, CodeHTTPFailure, CodeBodyRead, CodeBodyTooLarge, CodeMediaType, CodeJSONInvalid, CodeUsageMissing, CodeUsageInvalid, CodeOutputTruncated, CodeOutputEmpty, CodeOutputUnsupported, CodeOutputInvalid, CodeOutputTooLarge, CodeOutputRefused:
		return code
	default:
		return CodeUnknown
	}
}
func (d Diagnostic) safe() Diagnostic {
	d.Code = safeCode(d.Code)
	switch d.BodyState {
	case BodyUnobserved, BodyPartial, BodyComplete, BodySizeLimit:
	default:
		d.BodyState = BodyUnobserved
	}
	switch d.FailureStage {
	case StageNone, StageUnknown, StageRequest, StageAuthority, StageBudget, StageNetwork, StageHTTP, StageMedia, StageBody, StageJSON, StageUsage, StageOutput, StageCallback:
	default:
		d.FailureStage = StageUnknown
	}
	switch d.CompletionKind {
	case CompletionUnknown, CompletionComplete, CompletionToolCall, CompletionTruncated, CompletionRefused:
	default:
		d.CompletionKind = CompletionUnknown
	}
	switch d.ContentKind {
	case ContentUnknown, ContentEmpty, ContentText, ContentToolCall, ContentReasoning, ContentMixed, ContentUnsupported:
	default:
		d.ContentKind = ContentUnknown
	}
	switch d.MediaType {
	case MediaUnknown, MediaMissing, MediaJSON, MediaEventStream, MediaText, MediaHTML, MediaOther, MediaInvalid:
	default:
		d.MediaType = MediaUnknown
	}
	switch d.UsageState {
	case UsageNotObserved, UsageKnown, UsageMissing, UsagePartial, UsageInvalid:
	default:
		d.UsageState = UsageNotObserved
	}
	if d.HTTPStatus < 100 || d.HTTPStatus > 599 {
		d.HTTPStatus = 0
	}
	if d.ResponseBytes < 0 {
		d.ResponseBytes = 0
	}
	if d.ResponseBytes > maxResponseBytes+1 {
		d.ResponseBytes = maxResponseBytes + 1
	}
	if d.RequestIndex < 0 || d.RequestIndex > MaxTurns {
		d.RequestIndex = 0
	}
	return d
}

// Also constrain serialization of caller-constructed Diagnostic values. Go's
// string-backed enums alone do not prevent arbitrary strings being assigned.
func (d Diagnostic) MarshalJSON() ([]byte, error) {
	type plain Diagnostic
	return json.Marshal(plain(d.safe()))
}
func classifyMedia(value string) MediaType {
	if strings.TrimSpace(value) == "" {
		return MediaMissing
	}
	kind, _, err := mime.ParseMediaType(value)
	if err != nil {
		return MediaInvalid
	}
	switch {
	case kind == "application/json" || strings.HasPrefix(kind, "application/") && strings.HasSuffix(kind, "+json"):
		return MediaJSON
	case kind == "text/event-stream":
		return MediaEventStream
	case kind == "text/html":
		return MediaHTML
	case strings.HasPrefix(kind, "text/"):
		return MediaText
	default:
		return MediaOther
	}
}
func transportFailure(ctx context.Context, d Diagnostic, err error, otherwise ErrorCode) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return diagnosticFailure(d, CodeTimeout, context.DeadlineExceeded)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return diagnosticFailure(d, CodeCancelled, context.Canceled)
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return diagnosticFailure(d, CodeTimeout, context.DeadlineExceeded)
	}
	return diagnosticFailure(d, otherwise, ErrFailed)
}

// A rejected outcome transports ONLY independently validated numeric usage, not
// partial text/tools. query never receives a recoverable PartialStreamError.
func rejectedOutcome(d Diagnostic, code ErrorCode, cause error, usage *Usage) error {
	failure := diagnosticFailure(d, code, cause).(*diagnosedFailure)
	if usage != nil && d.UsageState == UsageKnown && validReportedUsage(*usage) {
		copy := *usage
		failure.reportedUsage = &copy
	}
	return failure
}
func validReportedUsage(u Usage) bool {
	return u.InputTokens > 0 && u.InputTokens <= 5*maxRequestBytes && u.OutputTokens >= 0 && u.OutputTokens <= maxRequestBytes
}
func rejectedUsage(err error) (Usage, bool) {
	var failure *diagnosedFailure
	if errors.As(err, &failure) && failure != nil && failure.reportedUsage != nil && failure.diagnostic.UsageState == UsageKnown && validReportedUsage(*failure.reportedUsage) {
		return *failure.reportedUsage, true
	}
	return Usage{}, false
}

func stageForCode(code ErrorCode) FailureStage {
	switch code {
	case CodeOK:
		return StageNone
	case CodeAuthorityRevoked:
		return StageAuthority
	case CodeInvalidRequest, CodeUnavailable:
		return StageRequest
	case CodeBudgetDenied, CodeBudgetExceeded, CodeBudgetSettlement:
		return StageBudget
	case CodeCancelled, CodeTimeout, CodeNetwork:
		return StageNetwork
	case CodeHTTPUnauthorized, CodeHTTPForbidden, CodeHTTPRateLimited, CodeHTTPFailure:
		return StageHTTP
	case CodeMediaType:
		return StageMedia
	case CodeBodyRead, CodeBodyTooLarge:
		return StageBody
	case CodeJSONInvalid:
		return StageJSON
	case CodeUsageMissing, CodeUsageInvalid:
		return StageUsage
	case CodeOutputTruncated, CodeOutputEmpty, CodeOutputUnsupported, CodeOutputInvalid, CodeOutputTooLarge, CodeOutputRefused:
		return StageOutput
	default:
		return StageUnknown
	}
}
