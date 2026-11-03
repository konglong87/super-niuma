package config

import (
	"fmt"
	"strings"
)

var providerRouteKeys = []string{"baseURL", "apiKey", "authToken"}

type ProviderProtocol string

const (
	ProviderProtocolAnthropicMessages     ProviderProtocol = "anthropic-messages"
	ProviderProtocolOpenAIChatCompletions ProviderProtocol = "openai-chat-completions"
	ProviderProtocolOpenAIResponses       ProviderProtocol = "openai-responses"
)

type ResponsesStateMode string

const (
	ResponsesStateModeStateless          ResponsesStateMode = "stateless"
	ResponsesStateModePreviousResponseID ResponsesStateMode = "previous-response-id"
)

type ResponsesProviderSettings struct {
	StateMode ResponsesStateMode `json:"stateMode,omitempty" yaml:"stateMode,omitempty"`
	Store     *bool              `json:"store,omitempty" yaml:"store,omitempty"`
}

type ResolvedProviderProtocol struct {
	Protocol ProviderProtocol
	Explicit bool
	ResponsesProviderSettings
}

func ResolveProviderProtocol(kind string, configured ProviderProtocol, responses *ResponsesProviderSettings) (ResolvedProviderProtocol, error) {
	kind = NormalizeProviderKind(kind)
	protocol := normalizeProviderProtocol(configured)
	if protocol == "" {
		return ResolvedProviderProtocol{Protocol: legacyProviderProtocol(kind)}, nil
	}

	resolved := ResolvedProviderProtocol{Protocol: protocol, Explicit: true}
	switch protocol {
	case ProviderProtocolAnthropicMessages:
		if !ProviderKindAnthropic(kind) {
			return ResolvedProviderProtocol{}, providerProtocolConflict(kind, protocol)
		}
	case ProviderProtocolOpenAIChatCompletions:
		if !ProviderKindOpenAI(kind) {
			return ResolvedProviderProtocol{}, providerProtocolConflict(kind, protocol)
		}
	case ProviderProtocolOpenAIResponses:
		if kind != "custom" && kind != "openai" && kind != "openai-compatible" {
			return ResolvedProviderProtocol{}, providerProtocolConflict(kind, protocol)
		}
		settings, err := resolveResponsesProviderSettings(responses)
		if err != nil {
			return ResolvedProviderProtocol{}, err
		}
		resolved.ResponsesProviderSettings = settings
	default:
		return ResolvedProviderProtocol{}, fmt.Errorf("unsupported provider protocol %q", protocol)
	}
	if protocol != ProviderProtocolOpenAIResponses && responses != nil {
		return ResolvedProviderProtocol{}, fmt.Errorf("responses settings require provider protocol %q", ProviderProtocolOpenAIResponses)
	}
	return resolved, nil
}

func legacyProviderProtocol(kind string) ProviderProtocol {
	if ProviderKindOpenAI(kind) {
		return ProviderProtocolOpenAIChatCompletions
	}
	if ProviderKindAnthropic(kind) {
		return ProviderProtocolAnthropicMessages
	}
	return ""
}

func resolveResponsesProviderSettings(settings *ResponsesProviderSettings) (ResponsesProviderSettings, error) {
	resolved := ResponsesProviderSettings{StateMode: ResponsesStateModeStateless, Store: boolPointer(false)}
	if settings == nil {
		return resolved, nil
	}
	stateMode := ResponsesStateMode(strings.ToLower(strings.TrimSpace(string(settings.StateMode))))
	if stateMode != "" {
		resolved.StateMode = stateMode
	}
	if settings.Store != nil {
		resolved.Store = boolPointer(*settings.Store)
	}

	switch resolved.StateMode {
	case ResponsesStateModeStateless:
	case ResponsesStateModePreviousResponseID:
		return ResponsesProviderSettings{}, fmt.Errorf("responses state mode %q is not implemented; use %q", resolved.StateMode, ResponsesStateModeStateless)
	default:
		return ResponsesProviderSettings{}, fmt.Errorf("unsupported responses state mode %q", resolved.StateMode)
	}
	if resolved.Store != nil && *resolved.Store {
		return ResponsesProviderSettings{}, fmt.Errorf("responses store=true is not implemented; use store=false")
	}
	return resolved, nil
}

func providerProtocolConflict(kind string, protocol ProviderProtocol) error {
	return fmt.Errorf("provider type %q is incompatible with protocol %q", kind, protocol)
}

func normalizeProviderProtocol(protocol ProviderProtocol) ProviderProtocol {
	return ProviderProtocol(strings.ToLower(strings.TrimSpace(string(protocol))))
}

func mergeResponsesProviderSettings(base, override *ResponsesProviderSettings) *ResponsesProviderSettings {
	if override == nil {
		return cloneResponsesProviderSettings(base)
	}
	merged := ResponsesProviderSettings{}
	if base != nil {
		merged = *base
	}
	if override.StateMode != "" {
		merged.StateMode = ResponsesStateMode(strings.ToLower(strings.TrimSpace(string(override.StateMode))))
	}
	if override.Store != nil {
		merged.Store = boolPointer(*override.Store)
	}
	return &merged
}

func cloneResponsesProviderSettings(settings *ResponsesProviderSettings) *ResponsesProviderSettings {
	if settings == nil {
		return nil
	}
	cloned := *settings
	if settings.Store != nil {
		cloned.Store = boolPointer(*settings.Store)
	}
	return &cloned
}

func boolPointer(value bool) *bool {
	return &value
}
