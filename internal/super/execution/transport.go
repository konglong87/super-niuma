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
func (s *httpStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	data, suffix, err := encodeRequest(s.protocol, req)
	if err != nil || len(data) > maxRequestBytes {
		return nil, ErrFailed
	}
	endpoint := strings.TrimRight(s.cfg.BaseURL, "/")
	if s.protocol == config.ProviderProtocolAnthropicMessages && !strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/v1"
	}
	// Body is intentionally non-replayable. Along with fresh HTTP/1 connections
	// (no keepalive), this removes net/http's idempotent connection retry path.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+suffix, io.NopCloser(bytes.NewReader(data)))
	if err != nil {
		return nil, ErrUnavailable
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.authorize != nil {
		if err := s.authorize(ctx); err != nil {
			return nil, err
		}
	}
	response, err := s.client.Do(httpReq)
	if err != nil {
		return nil, ErrFailed
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrFailed
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes || !utf8.Valid(body) {
		return nil, ErrFailed
	}
	result, err := decodeResponse(s.protocol, body)
	if err != nil {
		return nil, ErrFailed
	}
	if err := validateResponse(result); err != nil {
		return nil, err
	}
	for _, b := range result.Message.Content {
		if b.Type == contentText && cb.OnText != nil {
			if err := cb.OnText(b.Text); err != nil {
				return nil, ErrFailed
			}
		}
	}
	return result, nil
}

func validateResponse(r *anthropic.StreamResult) error {
	if r == nil || r.Message.Role != roleAssistant || len(r.Message.Content) == 0 {
		return ErrFailed
	}
	textBytes, calls := 0, 0
	seen := map[string]bool{}
	for _, b := range r.Message.Content {
		switch b.Type {
		case contentText:
			textBytes += len(b.Text)
			if !utf8.ValidString(b.Text) || textBytes > MaxResultBytes {
				return ErrFailed
			}
		case contentToolUse:
			calls++
			if calls > maxCallsPerResponse || !validID(b.ID) || seen[b.ID] || (b.Name != ToolRead && b.Name != ToolWrite) || !json.Valid(b.Input) || len(b.Input) > maxResponseBytes {
				return ErrFailed
			}
			seen[b.ID] = true
		default:
			return ErrFailed
		}
	}
	if calls > 0 && r.StopReason != stopTools || calls == 0 && r.StopReason != stopComplete {
		return ErrFailed
	}
	return nil
}
