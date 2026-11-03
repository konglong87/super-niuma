package runtimecompose

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

// safeWriter never emits raw chunks. Even a fixed lookbehind cannot protect an
// unbounded credential shape (or a private key spanning multiple lines). The
// guardedStreamer releases only a fully sanitized final result; this sink adds
// an independent byte cap without retaining another copy.
type safeWriter struct {
	ctx   context.Context
	bytes int
}

func (w *safeWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > workflow.MaxOutputBytes-w.bytes {
		return 0, failure(CodeBudget, "Streamed text exceeds the output byte budget.", false)
	}
	w.bytes += len(p)
	return len(p), nil
}

// guardedStreamer is the boundary before query can log, persist, recover a
// PartialStreamError as success, or execute a hallucinated tool call.
type guardedStreamer struct {
	inner      query.MessageStreamer
	redact     func(string) string
	usage      workflow.Usage
	usageKnown bool
	err        error
}

func (s *guardedStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if err := ctx.Err(); err != nil {
		s.err = s.safeError(ctx, err)
		return nil, s.err
	}
	if len(req.Tools) != 0 {
		s.err = failure(CodePolicy, "Text model requests must not contain tools.", false)
		return nil, s.err
	}
	bytes := 0
	res, err := s.inner.StreamMessages(ctx, req, anthropic.StreamCallbacks{
		OnText: func(text string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(text) > workflow.MaxOutputBytes-bytes {
				s.err = failure(CodeBudget, "Streamed text exceeds the output byte budget.", false)
				return s.err
			}
			bytes += len(text)
			// No data leaves the adapter via callbacks; query receives final sanitized
			// text below. This also avoids retaining hidden content in query buffers.
			return nil
		},
		// Thinking/signatures/citations/connector payloads are intentionally dropped.
	})
	reported := res
	if reported == nil {
		var partial *anthropic.PartialStreamError
		if errors.As(err, &partial) {
			reported = partial.Partial
		}
	}
	if reported != nil {
		var usageErr *ExecutionError
		s.usage, s.usageKnown, usageErr = readUsage(reported.Usage)
		if usageErr != nil {
			s.err = usageErr
			return nil, usageErr
		}
	}
	if err != nil {
		s.err = s.safeError(ctx, err)
		return nil, s.err
	}
	if ctx.Err() != nil {
		s.err = s.safeError(ctx, ctx.Err())
		return nil, s.err
	}
	if s.err != nil {
		return nil, s.err
	}
	if res == nil {
		s.err = failure(CodeProvider, "Provider returned no text response.", false)
		return nil, s.err
	}
	var output strings.Builder
	for _, block := range res.Message.Content {
		switch block.Type {
		case "text":
			if len(block.Text) > workflow.MaxOutputBytes-output.Len() {
				s.err = failure(CodeBudget, "Text response exceeds the output byte budget.", false)
				return nil, s.err
			}
			output.WriteString(block.Text)
		case "thinking", "redacted_thinking": // discard, never hand to query
		default:
			s.err = failure(CodeUnsupported, "Model returned an unsupported non-text or tool content block.", false)
			return nil, s.err
		}
	}
	if err := validCompletion(res.StopReason, s.usage, output.String()); err != nil {
		s.err = err
		return nil, err
	}
	text := s.redact(output.String())
	if len(text) > workflow.MaxOutputBytes {
		s.err = failure(CodeBudget, "Sanitized response exceeds the output byte budget.", false)
		return nil, s.err
	}
	safe := &anthropic.StreamResult{StopReason: res.StopReason, Usage: res.Usage, Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}}}
	if cb.OnText != nil {
		if err := cb.OnText(text); err != nil {
			s.err = s.safeError(ctx, err)
			return nil, s.err
		}
	}
	return safe, nil
}

// Sanitize before query can log errors, not only at the workflow boundary.
func (s *guardedStreamer) safeError(ctx context.Context, err error) *ExecutionError {
	typed := classify(ctx, err)
	return &ExecutionError{Code: typed.Code, Message: s.redact(typed.Message), Retryable: typed.Retryable}
}

func validCompletion(stop string, u workflow.Usage, text string) *ExecutionError {
	switch stop {
	case "end_turn", "stop_sequence", "stop":
	case "tool_use", "tool_calls", "function_call":
		return failure(CodeUnsupported, "Model tool calls are unsupported in text execution.", false)
	case "refusal", "content_filter":
		return failure(CodePolicy, "Provider policy rejected the text response.", false)
	default:
		return failure(CodeStopReason, "Provider did not return a complete text stop reason.", false)
	}
	if err := validateUsage(u); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return failure(CodeEmptyOutput, "Provider returned empty text output.", false)
	}
	if !utf8.ValidString(text) {
		return failure(CodeProvider, "Provider returned invalid UTF-8 text.", false)
	}
	return nil
}

type routeObserver struct {
	mu              sync.Mutex
	provider, model string
}

func (r *routeObserver) Emit(_ context.Context, event telemetry.Event) error {
	// Client emits phase spans with actual provider.name and the post-fallback
	// request model. Neither error bodies nor arbitrary properties are retained.
	if event.Status != telemetry.StatusOK {
		return nil
	}
	phase, _ := event.Properties["phase"].(string)
	if !strings.HasPrefix(phase, "stream.") {
		return nil
	}
	name, _ := event.Properties["provider_name"].(string)
	if name == "" || event.Model == "" {
		return nil
	}
	r.mu.Lock()
	r.provider, r.model = name, event.Model
	r.mu.Unlock()
	return nil
}
func (r *routeObserver) identity() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.provider, r.model
}
