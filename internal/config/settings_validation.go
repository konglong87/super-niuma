package config

import (
	"fmt"
	"strings"
)

// SettingsIssue identifies a locally verifiable problem without contacting a provider.
type SettingsIssue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	SettingsIssueRoute = "invalid_route"
	SettingsIssueValue = "invalid_value"
)

// ValidateSettings checks route contracts; credentials and remote model availability
// are deliberately not required so the settings editor can repair an incomplete
// document. Runtime route validation is performed by Config.ValidateProviderRoute.
func ValidateSettings(settings Settings) []SettingsIssue {
	issues := []SettingsIssue{}
	add := func(field, code, message string) { issues = append(issues, SettingsIssue{field, code, message}) }
	validateRoute := func(field, kind string, protocol ProviderProtocol, responses *ResponsesProviderSettings) {
		if !ProviderKindAnthropic(kind) && !ProviderKindOpenAI(kind) {
			add(field, SettingsIssueRoute, "unsupported provider type")
			return
		}
		if responses != nil && normalizeProviderProtocol(protocol) != ProviderProtocolOpenAIResponses {
			add(field, SettingsIssueRoute, "responses settings require openai-responses protocol")
			return
		}
		if _, err := ResolveProviderProtocol(kind, protocol, responses); err != nil {
			add(field, SettingsIssueRoute, err.Error())
		}
	}
	kind := settings.Provider
	validateRoute("providerProtocol", kind, settings.ProviderProtocol, settings.Responses)
	if settings.Model != "" && strings.TrimSpace(settings.Model) == "" {
		add("model", SettingsIssueValue, "model must not be whitespace")
	}
	if settings.ContextLength < 0 {
		add("contextLength", SettingsIssueValue, "contextLength must not be negative")
	}
	if settings.MaxToolResultBytes < 0 {
		add("maxToolResultBytes", SettingsIssueValue, "maxToolResultBytes must not be negative")
	}
	if settings.Fallback != nil {
		names := map[string]bool{}
		for i, provider := range settings.Fallback.Providers {
			path := fmt.Sprintf("fallback.providers.%d", i)
			validateRoute(path+".protocol", provider.Type, provider.Protocol, provider.Responses)
			name := strings.TrimSpace(provider.Name)
			if name != "" && names[name] {
				add(path+".name", SettingsIssueValue, "provider name must be unique")
			}
			names[name] = true
			if provider.Model != "" && strings.TrimSpace(provider.Model) == "" {
				add(path+".model", SettingsIssueValue, "model must not be whitespace")
			}
		}
	}
	return issues
}

// InspectSettings uses the runtime's search order and merge rules, tracking only
// routing fields whose attribution can be established exactly during the merge.
// These are file sources, before environment, CLI, profile, or per-run overrides.
func InspectSettings(cwd string) (LoadedSettings, map[string]string) {
	var loaded LoadedSettings
	sources := map[string]string{}
	for _, path := range settingsSearchPaths(cwd) {
		settings, ok := readSettings(path)
		if !ok {
			continue
		}
		if loaded.Provider != "" && settings.Provider != "" && NormalizeProviderKind(loaded.Provider) != NormalizeProviderKind(settings.Provider) {
			for _, field := range []string{"model", "providerProtocol", "responses.stateMode", "responses.store"} {
				delete(sources, field)
			}
			for _, key := range providerRouteKeys {
				delete(sources, key)
			}
		}
		if settings.ProviderProtocol != "" && normalizeProviderProtocol(settings.ProviderProtocol) != normalizeProviderProtocol(loaded.ProviderProtocol) {
			delete(sources, "responses.stateMode")
			delete(sources, "responses.store")
		}
		for field, value := range map[string]string{"model": settings.Model, "provider": settings.Provider, "providerProtocol": string(settings.ProviderProtocol)} {
			if value != "" {
				sources[field] = path
			}
		}
		for _, key := range providerRouteKeys {
			if value := providerRouteValue(settings, key); value != "" {
				sources[key] = path
			}
		}
		if settings.Responses != nil {
			if settings.Responses.StateMode != "" {
				sources["responses.stateMode"] = path
			}
			if settings.Responses.Store != nil {
				sources["responses.store"] = path
			}
		}
		annotatePermissionSources(&settings, permissionSourceForPath(cwd, path))
		loaded.Settings = mergeSettings(loaded.Settings, settings)
		loaded.Sources = append(loaded.Sources, path)
	}
	return loaded, sources
}

func providerRouteValue(settings Settings, key string) string {
	switch key {
	case "baseURL":
		return settings.BaseURL
	case "apiKey":
		return settings.APIKey
	case "authToken":
		return settings.AuthToken
	default:
		return ""
	}
}
