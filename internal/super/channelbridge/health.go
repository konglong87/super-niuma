package channelbridge

import (
	"context"
	ch "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/super/control"
	"sort"
)

const (
	HealthConfigured = "configured"
	HealthStarting   = "starting"
	HealthReady      = "ready"
	HealthDegraded   = "degraded"
	HealthFailed     = "failed"
	HealthDisabled   = "disabled"
	HealthClosed     = "closed"
)

// Health is a public redacted DTO. It never contains Account configuration,
// secret references/values, raw SDK errors, external message text or senders.
type Health struct {
	AccountID string `json:"account_id"`
	Provider  string `json:"provider"`
	Status    string `json:"status"`
	Code      string `json:"code"`
}

// Health returns a bounded, sorted snapshot. A configured webhook is not proof
// that its HTTP handler is mounted or an external credential acceptance passed.
func (b *Bridge) Health() []Health {
	if b == nil {
		return []Health{}
	}
	accounts, err := b.accounts()
	if err != nil {
		return []Health{{Status: HealthDegraded, Code: "configuration_invalid"}}
	}
	b.mu.RLock()
	closed := b.closed
	adapters := make(map[string]ch.Adapter, len(b.adapters))
	configs := make(map[string]string, len(b.activeConfig))
	codes := make(map[string]string, len(b.admissionCodes))
	for id, a := range b.adapters {
		adapters[id] = a
	}
	for id, v := range b.activeConfig {
		configs[id] = v
	}
	for id, v := range b.admissionCodes {
		codes[id] = v
	}
	b.mu.RUnlock()
	out := make([]Health, 0, len(accounts))
	for _, a := range accounts {
		h := Health{AccountID: a.ID, Provider: a.Provider, Status: HealthStarting, Code: "not_started"}
		switch {
		case closed:
			h.Status, h.Code = HealthClosed, "closed"
		case !a.Enabled:
			h.Status, h.Code = HealthDisabled, "disabled"
		case a.Provider == control.ProviderWebhook:
			h.Status, h.Code = HealthConfigured, "handler_mount_unverified"
			if len(b.options.Getenv(a.SecretEnv)) < 32 {
				h.Status, h.Code = HealthDegraded, "credentials_unavailable"
			}
			if a.BotIDEnv != "" && !safeToken(b.options.Getenv(a.BotIDEnv), 256) {
				h.Status, h.Code = HealthDegraded, "bot_identity_unknown"
			}
		case adapters[a.ID] != nil:
			state := adapters[a.ID].Health(context.Background())
			switch state.Status {
			case ch.HealthReady:
				h.Status, h.Code = HealthReady, "transport_ready"
			case ch.HealthStarting:
				h.Status, h.Code = HealthStarting, "transport_starting"
			case ch.HealthFailed:
				h.Status, h.Code = HealthFailed, "transport_failed"
			default:
				h.Status, h.Code = HealthDegraded, "transport_degraded"
			}
			if codes[a.ID] != "" {
				h.Status, h.Code = HealthDegraded, codes[a.ID]
			}
			if configs[a.ID] != b.configurationFingerprint(a) {
				h.Status, h.Code = HealthDegraded, "configuration_changed"
			}
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out
}
func (b *Bridge) setAdmissionCode(id, code string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.admissionCodes[id] = code
}
