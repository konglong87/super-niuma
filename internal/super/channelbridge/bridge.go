// Package channelbridge binds authenticated transports to the Super router.
// It never starts legacy channel/runtime or interactive Session execution.
package channelbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	ch "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	MaxAccounts     = 128
	MaxBodyBytes    = 64 << 10
	MaxNoticeBytes  = 8192
	DefaultTimeout  = 15 * time.Second
	SignatureWindow = 5 * time.Minute
	WebhookPrefix   = "/super/webhook/"
	ProtocolVersion = "v1"
	HeaderVersion   = "X-Super-Version"
	HeaderAccount   = "X-Super-Account"
	HeaderKeyID     = "X-Super-Key-Id"
	HeaderTimestamp = "X-Super-Timestamp"
	HeaderNonce     = "X-Super-Nonce"
	HeaderSignature = "X-Super-Signature"
)

var ErrConfiguration = errors.New("invalid Super channel configuration")
var ErrClosed = errors.New("Super channel bridge closed")

type Account struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Enabled      bool   `json:"enabled"`
	SecretEnv    string `json:"secret_env"`
	AppIDEnv     string `json:"app_id_env"`
	AppSecretEnv string `json:"app_secret_env"`
	BotIDEnv     string `json:"bot_id_env"`
	WebhookURL   string `json:"webhook_url"`
}
type InboundHandler interface {
	Handle(context.Context, router.Envelope) (router.Decision, error)
}
type Options struct {
	Accounts       func() []Account
	Handler        InboundHandler
	Logger         *slog.Logger
	Getenv         func(string) string
	Now            func() time.Time
	AdapterFactory func(Account) (ch.Adapter, error)
	// Required for every Send. The control plane must match the exact active
	// grant target (not merely the account) and current authorization revision.
	AuthorizeTarget func(context.Context, control.Target) error
	// Trusted injection for offline tests. Production's default transport pins
	// resolved public addresses, disables proxying and rejects redirects.
	HTTPClient    *http.Client
	AllowLoopback bool
}
type Bridge struct {
	admissionCodes  map[string]string
	options         Options
	client          *http.Client
	life            sync.Mutex
	mu              sync.RWMutex
	adapters        map[string]ch.Adapter
	activeConfig    map[string]string
	cancel          context.CancelFunc
	started, closed bool
}

var _ control.Sender = (*Bridge)(nil)

func New(options Options) (*Bridge, error) {
	if options.Accounts == nil || options.Handler == nil {
		return nil, ErrConfiguration
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	b := &Bridge{admissionCodes: map[string]string{}, options: options, adapters: map[string]ch.Adapter{}}
	if _, err := b.accounts(); err != nil {
		return nil, err
	}
	b.client = b.httpClient(options.HTTPClient)
	return b, nil
}
func (b *Bridge) accounts() (map[string]Account, error) {
	entries := b.options.Accounts()
	if len(entries) > MaxAccounts {
		return nil, ErrConfiguration
	}
	result := make(map[string]Account, len(entries))
	for _, a := range entries {
		if !control.ValidID(a.ID) || len(a.ID) > 96 || result[a.ID].ID != "" || (a.Provider != control.ProviderWebhook && a.Provider != control.ProviderFeishu) {
			return nil, ErrConfiguration
		}
		for _, ref := range []string{a.SecretEnv, a.AppIDEnv, a.AppSecretEnv, a.BotIDEnv} {
			if ref != "" && !validEnvRef(ref) {
				return nil, ErrConfiguration
			}
		}
		if a.Enabled && a.Provider == control.ProviderWebhook {
			if a.SecretEnv == "" {
				return nil, ErrConfiguration
			}
			if a.WebhookURL != "" {
				if _, err := b.authorizedURL(a.WebhookURL); err != nil {
					return nil, ErrConfiguration
				}
			}
		}
		result[a.ID] = a
	}
	return result, nil
}
func validEnvRef(ref string) bool {
	if len(ref) == 0 || len(ref) > 128 {
		return false
	}
	for i, c := range ref {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func (b *Bridge) account(id, provider string) (Account, error) {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return Account{}, ErrClosed
	}
	accounts, err := b.accounts()
	if err != nil {
		return Account{}, err
	}
	a, ok := accounts[id]
	if !ok || !a.Enabled || a.Provider != provider {
		return Account{}, control.ErrDenied
	}
	return a, nil
}
func (b *Bridge) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrConfiguration
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.life.Lock()
	defer b.life.Unlock()
	b.mu.RLock()
	closed, started := b.closed, b.started
	b.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	if started {
		return nil
	}
	accounts, err := b.accounts()
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	startedAdapters := map[string]ch.Adapter{}
	activeConfig := map[string]string{}
	for _, a := range accounts {
		if !a.Enabled || a.Provider != control.ProviderFeishu {
			continue
		}
		var adapter ch.Adapter
		if b.options.AdapterFactory != nil {
			adapter, err = b.options.AdapterFactory(a)
		} else {
			adapter, err = b.newFeishu(a)
		}
		if err != nil || adapter == nil || string(adapter.Provider()) != a.Provider || adapter.AccountID() != a.ID {
			cancel()
			stopAdapters(startedAdapters)
			return ErrConfiguration
		}
		startedAdapters[a.ID] = adapter
		activeConfig[a.ID] = b.configurationFingerprint(a)
		if err = adapter.Start(runCtx, b.feishuInbound(a, adapter)); err != nil {
			cancel()
			stopAdapters(startedAdapters)
			return ErrConfiguration
		}
	}
	b.mu.Lock()
	b.adapters = startedAdapters
	b.activeConfig = activeConfig
	b.cancel = cancel
	b.started = true
	b.mu.Unlock()
	b.options.Logger.InfoContext(ctx, "Super channel transports started", "account_count", len(startedAdapters))
	return nil
}
func stopAdapters(adapters map[string]ch.Adapter) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	for _, adapter := range adapters {
		_ = adapter.Stop(ctx)
	}
}
func (b *Bridge) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrConfiguration
	}
	b.life.Lock()
	defer b.life.Unlock()
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	cancel := b.cancel
	adapters := b.adapters
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var failed bool
	for _, adapter := range adapters {
		if adapter.Stop(ctx) != nil {
			failed = true
		}
	}
	b.client.CloseIdleConnections()
	if failed {
		return ErrClosed
	}
	return nil
}
func (b *Bridge) WebhookHandler() http.Handler { return http.HandlerFunc(b.webhook) }

func (b *Bridge) configurationFingerprint(a Account) string {
	data, _ := json.Marshal(struct {
		Account                 Account
		AppID, AppSecret, BotID string
	}{a, b.options.Getenv(a.AppIDEnv), b.options.Getenv(a.AppSecretEnv), b.options.Getenv(a.BotIDEnv)})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
