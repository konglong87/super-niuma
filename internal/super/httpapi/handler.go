// Package httpapi exposes a loopback-only, explicit intake API. It has no
// execution adapters and does not mount or change the traditional WebUI.
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const (
	APIPrefix                   = "/api/super/v1"
	RequestHeader               = "X-Super-Niuma-Request"
	RequestHeaderValue          = "1"
	IdempotencyHeader           = "Idempotency-Key"
	MaxBodyBytes          int64 = 64 << 10
	MaxConcurrentRequests       = 32
	statusPath                  = APIPrefix + "/status"
	collectionPath              = APIPrefix + "/work-orders"
	applicationName             = "super-niuma"
	intakeMode                  = "intake_only"
)

type Config struct {
	Service     *workorder.Service
	AllowedHost string
	Logger      *slog.Logger
}

type handler struct {
	service *workorder.Service
	host    string
	logger  *slog.Logger
	valid   bool
	active  chan struct{}
}

// NewHandler fails closed on invalid configuration because its fixed integration
// contract returns only http.Handler. AllowedHost is the actual bound authority,
// not a hostname, wildcard, reverse-proxy header, or another loopback alias.
func NewHandler(config Config) http.Handler {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &handler{service: config.Service, host: config.AllowedHost,
		logger: logger.With("component", "super.httpapi"),
		valid:  config.Service != nil && validAuthority(config.AllowedHost),
		active: make(chan struct{}, MaxConcurrentRequests)}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.valid {
		h.write(w, r, failure(codeUnavailable))
		return
	}
	if !h.authorized(r) {
		h.write(w, r, failure(codeForbidden))
		return
	}
	select {
	case h.active <- struct{}{}:
		defer func() { <-h.active }()
	default:
		h.write(w, r, failure(codeUnavailable))
		return
	}
	h.write(w, r, h.dispatch(w, r))
}
