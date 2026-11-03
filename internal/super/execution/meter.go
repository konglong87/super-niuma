package execution

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/query"
)

// One state per attempt; query's parallel tools are deliberately disabled.
type attemptState struct {
	request    Request
	cancel     context.CancelFunc
	logger     *slog.Logger
	err        error
	toolCalls  int
	writes     []WriteReceipt
	diagnostic *Diagnostic
}

func (s *attemptState) fail(err error) error {
	if s.err == nil {
		s.err = err
		d := Diagnostic{FailureStage: StageUnknown}
		if s.diagnostic != nil {
			d = *s.diagnostic
		}
		if reported, ok := failureDiagnostic(err); ok {
			// Sparse local failures (e.g. a bounded callback) must not erase the
			// already observed response metadata. New requests clear it upfront.
			if reported.HTTPStatus != 0 {
				index := d.RequestIndex
				d = reported
				d.RequestIndex = index
			}
			d.FailureStage = reported.FailureStage
		} else {
			d.FailureStage = StageUnknown
		}
		d.Code = ErrorCode(FailureCode(err))
		if d.FailureStage == StageUnknown || d.FailureStage == StageNone || d.FailureStage == "" {
			d.FailureStage = stageForCode(d.Code)
		}
		s.diagnose(d)
		s.cancel()
	}
	return s.err
}
func (s *attemptState) authorize(ctx context.Context) error {
	if s.err != nil {
		return s.err
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if s.request.CheckAuthority(ctx) != nil {
		return s.fail(safeError(ctx, ErrAuthority))
	}
	return nil
}
func (s *attemptState) diagnose(d Diagnostic) { copy := d.safe(); s.diagnostic = &copy }
func (s *attemptState) log(action, code string) {
	reference := sha256.Sum256([]byte(s.request.AttemptID))
	attrs := []any{"action", action, "run_id", s.request.RunID, "job_id", s.request.JobID, "attempt_ref", fmt.Sprintf("%x", reference[:8]), "code", code}
	if s.diagnostic != nil {
		d := s.diagnostic.safe()
		attrs = append(attrs, "diagnostic_code", d.Code, "http_status", d.HTTPStatus, "response_bytes", d.ResponseBytes, "media_type", d.MediaType, "usage_state", d.UsageState, "request_index", d.RequestIndex, "body_state", d.BodyState, "failure_stage", d.FailureStage, "completion_kind", d.CompletionKind, "content_kind", d.ContentKind)
	}
	s.logger.Info("super_execution", attrs...)
}

type boundedWriter struct {
	state *attemptState
	bytes int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if len(p) > MaxResultBytes-w.bytes {
		return 0, w.state.fail(codedFailure(CodeOutputTooLarge))
	}
	w.bytes += len(p)
	return len(p), nil
}

type requestSizer interface {
	requestBytes(anthropic.MessagesRequest) (int, error)
}
type meteredStreamer struct {
	inner      query.MessageStreamer
	state      *attemptState
	calls      int
	usage      Usage
	usageKnown bool
}

type responseDiagnoser interface{ responseDiagnostic() Diagnostic }

func (m *meteredStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	m.state.diagnose(Diagnostic{Code: CodeOK, RequestIndex: m.calls + 1, FailureStage: StageAuthority})
	if err := m.state.authorize(ctx); err != nil {
		return nil, err
	}
	if m.calls >= m.state.request.MaxTurns || req.Model != m.state.request.Model || req.MaxTokens != m.state.request.MaxOutputTokens || req.Thinking != nil {
		return nil, m.state.fail(ErrInvalid)
	}
	m.state.diagnostic.FailureStage = StageRequest
	encoded, err := json.Marshal(req)
	if err != nil || len(encoded) > maxRequestBytes {
		return nil, m.state.fail(ErrBudget)
	}
	size := len(encoded)
	if sizer, ok := m.inner.(requestSizer); ok {
		n, err := sizer.requestBytes(req)
		if err != nil {
			return nil, m.state.fail(diagnosticFailure(*m.state.diagnostic, ErrorCode(FailureCode(err)), ErrFailed))
		}
		if n > size {
			size = n
		}
	}
	if size > maxRequestBytes {
		return nil, m.state.fail(ErrBudget)
	}
	input := size + framingReserve + len(req.Messages)*perMessageReserve + len(req.Tools)*perToolReserve
	m.calls++
	id := fmt.Sprintf("%s.model.%d", m.state.request.AttemptID, m.calls)
	m.state.diagnostic.FailureStage = StageBudget
	m.state.log(actionModelReserve, codeStart)
	if err := m.state.request.Budget.Reserve(ctx, id, input, req.MaxTokens); err != nil {
		m.usageKnown = false
		return nil, m.state.fail(safeError(ctx, ErrBudget))
	}
	return m.reservedRequest(ctx, id, input, req, cb)
}

func (m *meteredStreamer) reservedRequest(ctx context.Context, id string, input int, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (stream *anthropic.StreamResult, retErr error) {
	usage, known := Usage{}, false
	defer func() {
		// This bounded bookkeeping may outlive cancellation. It never performs a
		// model request. Known cost is settled even when execution was rejected;
		// incomplete/ambiguous reports retain the entire reservation.
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := m.state.request.Budget.Settle(settleCtx, id, usage, known); err != nil {
			if retErr == nil {
				d := *m.state.diagnostic
				d.FailureStage = StageBudget
				stream, retErr = nil, m.state.fail(diagnosticFailure(d, CodeBudgetSettlement, ErrBudget))
			} else {
				m.state.log(actionModelSettle, string(CodeBudgetSettlement))
			}
		}
		m.usageKnown = m.usageKnown && known
		m.usage.InputTokens += usage.InputTokens
		m.usage.OutputTokens += usage.OutputTokens
		m.state.log(actionModelSettle, errorCode(retErr))
	}()
	if err := m.state.authorize(ctx); err != nil {
		return nil, err
	}
	m.state.diagnostic.FailureStage = StageNetwork
	m.state.log(actionModelRequest, codeStart)
	raw, requestErr := m.inner.StreamMessages(ctx, req, cb)
	d := Diagnostic{Code: CodeOK, RequestIndex: m.calls, FailureStage: StageUnknown}
	if metadata, ok := m.inner.(responseDiagnoser); ok {
		d = metadata.responseDiagnostic()
	}
	if failure, ok := failureDiagnostic(requestErr); ok {
		d = failure
	}
	d.RequestIndex = m.calls
	m.state.diagnose(d)
	if requestErr != nil {
		usage, known = rejectedUsage(requestErr)
		if known && (usage.InputTokens > input || usage.OutputTokens > req.MaxTokens) {
			return nil, m.budgetExceeded()
		}
		if _, typed := failureDiagnostic(requestErr); typed {
			return nil, m.state.fail(requestErr)
		}
		// Never pass arbitrary errors or PartialStreamError to query: they can
		// contain secrets or activate recovery of a rejected partial response.
		return nil, m.state.fail(diagnosticFailure(d, ErrorCode(FailureCode(safeError(ctx, requestErr))), safeError(ctx, ErrFailed)))
	}
	if raw == nil {
		return nil, m.state.fail(diagnosticFailure(d, CodeOutputInvalid, ErrFailed))
	}
	var usageErr error
	usage, usageErr = observedUsage(raw.Usage)
	if usageErr != nil {
		m.state.diagnostic.UsageState = UsageInvalid
		m.state.diagnostic.FailureStage = StageUsage
		return nil, m.state.fail(diagnosticFailure(*m.state.diagnostic, CodeUsageInvalid, ErrFailed))
	}
	known = true
	m.state.diagnostic.UsageState = UsageKnown
	if usage.InputTokens > input || usage.OutputTokens > req.MaxTokens {
		return nil, m.budgetExceeded()
	}
	if err := m.state.authorize(ctx); err != nil {
		return nil, err
	}
	m.state.diagnostic.FailureStage = StageNone
	return raw, nil
}
func (m *meteredStreamer) budgetExceeded() error {
	d := *m.state.diagnostic
	d.UsageState = UsageKnown
	d.FailureStage = StageBudget
	return m.state.fail(diagnosticFailure(d, CodeBudgetExceeded, ErrBudget))
}

// A later successful write supersedes an older receipt for that same path.
// Verification sees the current candidate, not an obsolete intermediate hash.
func (s *attemptState) recordWrite(receipt WriteReceipt) {
	for i := range s.writes {
		if s.writes[i].Path == receipt.Path {
			s.writes[i] = receipt
			return
		}
	}
	s.writes = append(s.writes, receipt)
}
