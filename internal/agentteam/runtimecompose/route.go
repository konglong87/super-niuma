package runtimecompose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

const (
	publicRouteHashVersion = 1
	routeChangedMessage    = "Provider route changed; start new run to use the updated configuration."
)

// These structs are an explicit allowlist, not a serialization of Config. Never
// add credentials, credential hashes, redacted/secret-derived replacements,
// arbitrary settings, URL userinfo, query parameters or fragments here.
type publicEndpoint struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Path   string `json:"path"`
}
type publicProviderRoute struct {
	Name               string                    `json:"name"`
	Provider           string                    `json:"provider"`
	Model              string                    `json:"model"`
	ModelPin           string                    `json:"model_pin,omitempty"`
	Protocol           config.ProviderProtocol   `json:"protocol"`
	ExplicitProtocol   bool                      `json:"explicit_protocol"`
	ResponsesStateMode config.ResponsesStateMode `json:"responses_state_mode,omitempty"`
	ResponsesStore     bool                      `json:"responses_store"`
	Endpoint           publicEndpoint            `json:"endpoint"`
}
type publicRouteConfiguration struct {
	Version  int                   `json:"version"`
	Primary  publicProviderRoute   `json:"primary"`
	Fallback []publicProviderRoute `json:"fallback"`
}

func publicRouteHash(cfg config.Config, model string) (string, error) {
	primary, err := projectPublicRoute(first(cfg.SelectedProvider, "primary"), cfg.Provider, model, cfg.SelectedProviderModel, cfg.ProviderProtocol, cfg.Responses, cfg.BaseURL)
	if err != nil {
		return "", err
	}
	public := publicRouteConfiguration{Version: publicRouteHashVersion, Primary: primary, Fallback: make([]publicProviderRoute, 0, len(cfg.FallbackProviders))}
	// Preserve order: changing fallback priority changes where context may go.
	for i, p := range cfg.FallbackProviders {
		route, err := projectPublicRoute(p.FallbackRouteName(i), p.Type, first(p.Model, model), p.Model, p.Protocol, p.Responses, p.BaseURL)
		if err != nil {
			return "", err
		}
		public.Fallback = append(public.Fallback, route)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		return "", failure(CodeConfig, "Public text route configuration could not be encoded.", false)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func projectPublicRoute(name, kind, model, pin string, protocol config.ProviderProtocol, responses *config.ResponsesProviderSettings, baseURL string) (publicProviderRoute, error) {
	resolved, err := config.ResolveProviderProtocol(kind, protocol, responses)
	if err != nil {
		return publicProviderRoute{}, failure(CodeConfig, "Public text route protocol is invalid.", false)
	}
	endpoint, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return publicProviderRoute{}, failure(CodeConfig, "Public text route endpoint must be an absolute HTTP(S) URL.", false)
	}
	scheme := strings.ToLower(endpoint.Scheme)
	if endpoint.Host == "" || (scheme != "http" && scheme != "https") {
		// Do not wrap url.Error: it includes the original, possibly secret URL.
		return publicProviderRoute{}, failure(CodeConfig, "Public text route endpoint must be an absolute HTTP(S) URL.", false)
	}
	return publicProviderRoute{
		Name: strings.TrimSpace(name), Provider: config.NormalizeProviderKind(kind), Model: strings.TrimSpace(model), ModelPin: strings.TrimSpace(pin),
		Protocol: resolved.Protocol, ExplicitProtocol: resolved.Explicit, ResponsesStateMode: resolved.StateMode, ResponsesStore: resolved.Store != nil && *resolved.Store,
		// Preserve the escaped path exactly; path normalization could merge distinct
		// gateway routes. Include scheme/port to fence transport or origin changes.
		Endpoint: publicEndpoint{Scheme: scheme, Host: strings.ToLower(endpoint.Host), Path: endpoint.EscapedPath()},
	}, nil
}
