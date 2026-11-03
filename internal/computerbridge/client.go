package computerbridge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	commandURL     = "http://localhost" + CommandPath
	maxTokenBytes  = 4096
	maxHeaderBytes = 16 << 10
)

// Config is immutable after construction. A zero RequestTimeout selects the
// default; a negative value is invalid. SocketPath must be an absolute path.
type Config struct {
	SocketPath     string
	Token          string
	RequestTimeout time.Duration
}

// Client is safe for concurrent use. It has no session/owner cache, approval
// authority, background work, reconnect loop, redirects or automatic retries.
// The host owns session lifecycle and serializes input independently of Stop.
type Client struct {
	http       *http.Client
	token      string
	socketPath string
}

// remoteOperationError keeps a stable, public host error code across the IPC
// boundary without exposing host error text. It unwraps to ErrRemote so legacy
// callers retain their existing transport classification.
type remoteOperationError struct {
	code string
}

func (e remoteOperationError) Error() string { return "remote computer operation failed" }
func (e remoteOperationError) Unwrap() error { return ErrRemote }
func (e remoteOperationError) Code() string  { return e.code }

func newRemoteOperationError(code string) error {
	return remoteOperationError{code: cu.PublicErrorCode(code)}
}

var _ Service = (*Client)(nil)
var _ SessionLookup = (*Client)(nil)

func NewClient(cfg Config) (*Client, error) {
	if !filepath.IsAbs(cfg.SocketPath) || strings.ContainsRune(cfg.SocketPath, 0) || !validToken(cfg.Token) || cfg.RequestTimeout < 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	dialer := &net.Dialer{Timeout: cfg.RequestTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", cfg.SocketPath)
		},
		// A fresh connection per command prevents replay on a stale pooled socket.
		// No proxy, redirects, HTTP compression, or HTTP/2 are used for this local RPC.
		DisableKeepAlives:      true,
		DisableCompression:     true,
		ResponseHeaderTimeout:  cfg.RequestTimeout,
		MaxResponseHeaderBytes: maxHeaderBytes,
	}
	return &Client{token: cfg.Token, socketPath: cfg.SocketPath, http: &http.Client{
		Transport:     transport,
		Timeout:       cfg.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// SocketPath is a read-only local endpoint identity. It contains no token and
// is used only by the desktop owner for bounded acceptance/fallback cleanup.
func (c *Client) SocketPath() string {
	if c == nil {
		return ""
	}
	return c.socketPath
}

func validToken(token string) bool {
	if len(token) == 0 || len(token) > maxTokenBytes {
		return false
	}
	for _, c := range token {
		if c <= ' ' || c >= 127 {
			return false
		}
	}
	return true
}

// call returns attempted=true once Do is entered, even if dispatch cannot be
// proven. Callers must treat any subsequent execute failure as ambiguous.
func (c *Client) call(ctx context.Context, in Request) (Response, bool, error) {
	if c == nil || c.http == nil || ctx == nil {
		return Response{}, false, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Response{}, false, err
	}
	if !validRequest(in) {
		return Response{}, false, ErrInvalidRequest
	}
	body, err := encodeRequest(in)
	if err != nil {
		return Response{}, false, err
	}
	// NopCloser deliberately prevents NewRequest from installing GetBody.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, commandURL, io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return Response{}, false, ErrInvalidRequest
	}
	req.ContentLength = int64(len(body))
	req.Header.Set(AuthorizationHeader, BearerPrefix+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, true, transportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Response{}, true, ErrRemote
	}
	if resp.Header.Get("Content-Encoding") != "" {
		return Response{}, true, ErrInvalidResponse
	}
	if resp.ContentLength > MaxResponseBytes {
		return Response{}, true, ErrResponseTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return Response{}, true, transportError(ctx, err)
	}
	if len(data) > MaxResponseBytes {
		return Response{}, true, ErrResponseTooLarge
	}
	var out Response
	if err := decodeStrict(data, &out); err != nil {
		return Response{}, true, err
	}
	if out.Error != "" {
		return out, true, newRemoteOperationError(out.ErrorCode)
	}
	if len(out.Data) == 0 || string(out.Data) == "null" {
		return Response{}, true, ErrInvalidResponse
	}
	return out, true, nil
}

func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return errors.Join(ErrTransport, ctx.Err())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrTransport, context.DeadlineExceeded)
	}
	return ErrTransport
}
