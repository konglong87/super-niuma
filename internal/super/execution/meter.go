package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/query"
)

// One state per attempt; query's parallel tools are deliberately disabled.
type attemptState struct {
	request   Request
	cancel    context.CancelFunc
	logger    *slog.Logger
	err       error
	toolCalls int
	writes    []WriteReceipt
}

func (s *attemptState) fail(err error) error {
	if s.err == nil {
		s.err = err
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
func (s *attemptState) log(action, code string) {
	s.logger.Info("super_execution", "action", action, "run_id", s.request.RunID,
		"job_id", s.request.JobID, "attempt_id", s.request.AttemptID, "code", code)
}

type boundedWriter struct {
	state *attemptState
	bytes int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if len(p) > MaxResultBytes-w.bytes {
		return 0, w.state.fail(ErrFailed)
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

func (m *meteredStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if err := m.state.authorize(ctx); err != nil {
		return nil, err
	}
	if m.calls >= m.state.request.MaxTurns || req.Model != m.state.request.Model ||
		req.MaxTokens != m.state.request.MaxOutputTokens || req.Thinking != nil {
		return nil, m.state.fail(ErrInvalid)
	}
	encoded, err := json.Marshal(req)
	if err != nil || len(encoded) > maxRequestBytes {
		return nil, m.state.fail(ErrBudget)
	}
	size := len(encoded)
	if sizer, ok := m.inner.(requestSizer); ok {
		n, err := sizer.requestBytes(req)
		if err != nil {
			return nil, m.state.fail(ErrFailed)
		}
		if n > size {
			size = n
		}
	}
	if size > maxRequestBytes {
		return nil, m.state.fail(ErrBudget)
	}
	// One byte per possible token, plus explicit message/tool framing. Never
	// estimate bytes/4: JSON escaping and non-ASCII text invalidate that bound.
	input := size + framingReserve + len(req.Messages)*perMessageReserve + len(req.Tools)*perToolReserve
	m.calls++
	id := fmt.Sprintf("%s/model/%d", m.state.request.AttemptID, m.calls)
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
		// Settlement is bounded cleanup only, never another model request. On lost
		// authority/cancellation/unknown usage the reservation is NOT refunded.
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := m.state.request.Budget.Settle(settleCtx, id, usage, known); err != nil {
			stream, retErr = nil, m.state.fail(ErrBudget)
		}
		m.usageKnown = m.usageKnown && known
		m.usage.InputTokens += usage.InputTokens
		m.usage.OutputTokens += usage.OutputTokens
		m.state.log(actionModelSettle, errorCode(retErr))
	}()
	if err := m.state.authorize(ctx); err != nil {
		return nil, err
	}
	m.state.log(actionModelRequest, codeStart)
	raw, err := m.inner.StreamMessages(ctx, req, cb)
	if err != nil {
		return nil, m.state.fail(safeError(ctx, ErrFailed))
	}
	if raw == nil {
		return nil, m.state.fail(ErrFailed)
	}
	u := raw.Usage
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadInputTokens < 0 || u.CacheCreationInputTokens < 0 {
		return nil, m.state.fail(ErrBudget)
	}
	// OpenAI input already includes cached tokens; Anthropic's does not.
	for _, n := range []int{u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens, u.CacheCreationEphemeral1hInputTokens, u.CacheCreationEphemeral5mInputTokens, u.CacheCreation.Ephemeral1hInputTokens, u.CacheCreation.Ephemeral5mInputTokens} {
		if n < 0 || n > maxRequestBytes {
			return nil, m.state.fail(ErrBudget)
		}
	}
	creation := max(u.CacheCreationInputTokens, u.CacheCreationEphemeral1hInputTokens+u.CacheCreationEphemeral5mInputTokens, u.CacheCreation.Ephemeral1hInputTokens+u.CacheCreation.Ephemeral5mInputTokens)
	total := int64(u.InputTokens) + int64(creation)
	if !u.InputTokensIncludeCacheRead {
		total += int64(u.CacheReadInputTokens)
	}
	if total > int64(maxRequestBytes)*2 || int64(u.OutputTokens) > int64(maxResponseBytes)*2 {
		return nil, m.state.fail(ErrBudget)
	}
	usage = Usage{int(total), u.OutputTokens}
	known = total > 0
	if known && (usage.InputTokens > input || usage.OutputTokens > req.MaxTokens) {
		return nil, m.state.fail(ErrBudget)
	}
	if err := m.state.authorize(ctx); err != nil {
		known = false
		return nil, err
	}
	return raw, nil
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
