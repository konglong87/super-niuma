package config

import "strings"

// RuntimeDefaults is the user-facing, provider-neutral view of the effective
// primary route. It intentionally excludes credentials and protocol details.
type RuntimeDefaults struct {
	Service    string `json:"service,omitempty"`
	Configured bool   `json:"configured"`
	NeedsSetup bool   `json:"needs_setup"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Source     string `json:"source,omitempty"`
}

// ResolveRuntimeDefaults is the single source of truth for desktop/WebUI
// defaults. A model without a valid provider route is not considered ready.
func ResolveRuntimeDefaults(cwd string) RuntimeDefaults {
	cfg := LoadForCWD(cwd)
	model := strings.TrimSpace(cfg.Settings.Model)
	provider := strings.TrimSpace(cfg.Provider)
	configured := model != "" && cfg.ConfigurationError == ""
	source := "none"
	if len(cfg.Sources) > 0 {
		source = "settings"
	}
	return RuntimeDefaults{
		Service:    "ready",
		Configured: configured,
		NeedsSetup: !configured,
		Provider:   provider,
		Model:      model,
		Source:     source,
	}
}
