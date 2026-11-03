package execution

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/query"
)

// Do NOT use anthropic.NewClient: its SDK retries, OpenAI compatibility
// retries and provider fallback perform unmetered requests. This deliberately
// small, buffered protocol adapter makes one HTTP POST per StreamMessages.
// Buffering also bounds hostile output and keeps raw provider errors private.
type httpStreamer struct {
	cfg       config.Config
	protocol  config.ProviderProtocol
	client    *http.Client
	authorize func(context.Context) error
	last      Diagnostic
}

func newHTTPStreamer(cfg config.Config) (query.MessageStreamer, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	resolved, _ := config.ResolveProviderProtocol(cfg.Provider, cfg.ProviderProtocol, cfg.Responses)
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &httpStreamer{cfg: cfg, protocol: resolved.Protocol, client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}
func (s *httpStreamer) requestBytes(req anthropic.MessagesRequest) (int, error) {
	data, _, err := encodeRequest(s.protocol, req)
	return len(data), err
}
func (s *httpStreamer) responseDiagnostic() Diagnostic { return s.last.safe() }

func (s *httpStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (stream *anthropic.StreamResult, retErr error) {
	d := Diagnostic{Code: CodeOK, UsageState: UsageNotObserved, FailureStage: StageRequest}
	defer func() {
		if failure, ok := failureDiagnostic(retErr); ok {
			d = failure
		} else if retErr == nil {
			d = s.last
		}
		s.last = d.safe()
	}()
	data, suffix, err := encodeRequest(s.protocol, req)
	if err != nil {
		return nil, diagnosticFailure(d, ErrorCode(FailureCode(err)), ErrFailed)
	}
	if len(data) > maxRequestBytes {
		return nil, diagnosticFailure(d, CodeBudgetDenied, ErrBudget)
	}
	endpoint := strings.TrimRight(s.cfg.BaseURL, "/")
	if s.protocol == config.ProviderProtocolAnthropicMessages && !strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/v1"
	}
	// One non-replayable body on a fresh HTTP/1 connection: no SDK compatibility
	// calls, redirects, connection retries, proxies or fallback routes.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+suffix, io.NopCloser(bytes.NewReader(data)))
	if err != nil {
		return nil, diagnosticFailure(d, CodeUnavailable, ErrUnavailable)
	}
	httpReq.ContentLength = int64(len(data))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if s.protocol == config.ProviderProtocolAnthropicMessages {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		if s.cfg.APIKey != "" {
			httpReq.Header.Set("x-api-key", s.cfg.APIKey)
		}
		if s.cfg.AuthToken != "" {
			httpReq.Header.Set("Authorization", "Bearer "+s.cfg.AuthToken)
		}
	} else {
		key := s.cfg.APIKey
		if key == "" {
			key = s.cfg.AuthToken
		}
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	d.FailureStage = StageAuthority
	if err := ctx.Err(); err != nil {
		return nil, transportFailure(ctx, d, err, CodeUnknown)
	}
	if s.authorize != nil {
		if err := s.authorize(ctx); err != nil {
			return nil, diagnosticFailure(d, ErrorCode(FailureCode(err)), err)
		}
	}
	d.FailureStage = StageNetwork
	response, err := s.client.Do(httpReq)
	if err != nil {
		return nil, transportFailure(ctx, d, err, CodeNetwork)
	}
	defer response.Body.Close()
	d.HTTPStatus = response.StatusCode
	d.MediaType = classifyMedia(response.Header.Get("Content-Type"))
	d.FailureStage = StageHTTP
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := CodeHTTPFailure
		switch response.StatusCode {
		case http.StatusUnauthorized:
			code = CodeHTTPUnauthorized
		case http.StatusForbidden:
			code = CodeHTTPForbidden
		case http.StatusTooManyRequests:
			code = CodeHTTPRateLimited
		}
		return nil, diagnosticFailure(d, code, ErrFailed)
	}
	d.FailureStage = StageMedia
	if d.MediaType != MediaJSON {
		return nil, diagnosticFailure(d, CodeMediaType, ErrFailed)
	}
	d.FailureStage = StageBody
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	d.ResponseBytes = int64(len(body))
	d.BodyState = BodyPartial
	if len(body) > maxResponseBytes {
		d.BodyState = BodySizeLimit
		return nil, diagnosticFailure(d, CodeBodyTooLarge, ErrFailed)
	}
	if err != nil {
		return nil, transportFailure(ctx, d, err, CodeBodyRead)
	}
	d.BodyState = BodyComplete
	return s.decodeOutcome(body, d, cb)
}

func (s *httpStreamer) decodeOutcome(body []byte, d Diagnostic, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	usage, state, usageErr := strictUsage(s.protocol, body)
	d.UsageState = state
	var trusted *Usage
	if state == UsageKnown {
		amount, err := observedUsage(usage)
		if err == nil {
			trusted = &amount
		}
	}
	reject := func(code ErrorCode, stage FailureStage, cause error) (*anthropic.StreamResult, error) {
		d.FailureStage = stage
		return nil, rejectedOutcome(d, code, cause, trusted)
	}
	if ErrorCode(FailureCode(usageErr)) == CodeJSONInvalid {
		return reject(CodeJSONInvalid, StageJSON, ErrFailed)
	}
	d.CompletionKind, d.ContentKind = classifyOutcome(s.protocol, body)
	result, outputErr := decodeResponse(s.protocol, body)
	if outputErr == nil {
		outputErr = validateResponse(result)
	}
	if outputErr != nil {
		return reject(ErrorCode(FailureCode(outputErr)), StageOutput, ErrFailed)
	}
	if usageErr != nil {
		return reject(ErrorCode(FailureCode(usageErr)), StageUsage, ErrFailed)
	}
	result.Usage = usage
	for _, b := range result.Message.Content {
		if b.Type == contentText && cb.OnText != nil {
			if err := cb.OnText(b.Text); err != nil {
				return reject(ErrorCode(FailureCode(err)), StageCallback, err)
			}
		}
	}
	d.Code = CodeOK
	d.FailureStage = StageNone
	s.last = d.safe()
	return result, nil
}

func validateResponse(r *anthropic.StreamResult) error {
	if r == nil || r.Message.Role != roleAssistant {
		return codedFailure(CodeOutputInvalid)
	}
	switch r.StopReason {
	case "max_tokens", "length":
		return codedFailure(CodeOutputTruncated)
	case "refusal", "content_filter":
		return codedFailure(CodeOutputRefused)
	}
	if len(r.Message.Content) == 0 {
		return codedFailure(CodeOutputEmpty)
	}
	textBytes, calls := 0, 0
	hasText := false
	seen := map[string]bool{}
	for _, b := range r.Message.Content {
		switch b.Type {
		case contentText:
			textBytes += len(b.Text)
			hasText = hasText || strings.TrimSpace(b.Text) != ""
			if !utf8.ValidString(b.Text) {
				return codedFailure(CodeOutputInvalid)
			}
			if textBytes > MaxResultBytes {
				return codedFailure(CodeOutputTooLarge)
			}
		case contentToolUse:
			calls++
			if b.Name != ToolRead && b.Name != ToolWrite {
				return codedFailure(CodeOutputUnsupported)
			}
			if calls > maxCallsPerResponse || !validID(b.ID) || seen[b.ID] || !json.Valid(b.Input) || len(b.Input) > maxResponseBytes {
				return codedFailure(CodeOutputInvalid)
			}
			seen[b.ID] = true
		default:
			return codedFailure(CodeOutputUnsupported)
		}
	}
	if calls == 0 && !hasText {
		return codedFailure(CodeOutputEmpty)
	}
	if calls > 0 && r.StopReason != stopTools || calls == 0 && r.StopReason != stopComplete {
		return codedFailure(CodeOutputInvalid)
	}
	return nil
}
