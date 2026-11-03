package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

const routeFingerprintVersion = "super-execution-route-v1"

// RouteFingerprint binds an immutable grant to the selected PUBLIC route.
// It deliberately excludes credentials, their references, fallback routes and
// unrelated settings, so ordinary credential rotation does not expand authority.
// Callers must compare this fingerprint at each authorization boundary.
func (r *Runner) RouteFingerprint(ctx context.Context, provider, model string) (string, error) {
	if ctx == nil {
		return "", ErrInvalid
	}
	cfg, err := r.configuration(ctx, provider, model)
	if err != nil {
		return "", err
	}
	resolved, err := config.ResolveProviderProtocol(cfg.Provider, cfg.ProviderProtocol, cfg.Responses)
	if err != nil {
		return "", ErrUnavailable
	}
	var responses *config.ResponsesProviderSettings
	if resolved.Protocol == config.ProviderProtocolOpenAIResponses {
		responses = &resolved.ResponsesProviderSettings
	}
	public := struct {
		Version   string                            `json:"version"`
		Provider  string                            `json:"provider"`
		Route     string                            `json:"route"`
		Protocol  config.ProviderProtocol           `json:"protocol"`
		BaseURL   string                            `json:"base_url"`
		Model     string                            `json:"model"`
		Responses *config.ResponsesProviderSettings `json:"responses,omitempty"`
	}{routeFingerprintVersion, config.NormalizeProviderKind(cfg.Provider), cfg.SelectedProvider,
		resolved.Protocol, strings.TrimRight(cfg.BaseURL, "/"), model, responses}
	data, err := json.Marshal(public)
	if err != nil {
		return "", ErrUnavailable
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
