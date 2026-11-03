package anthropic

import (
	"context"
	"fmt"

	"github.com/konglong87/super-niuma/internal/config"
)

type protocolSource string

const (
	protocolSourceLegacyDerived protocolSource = "legacy-derived"
	protocolSourceExplicit      protocolSource = "explicit"
)

type providerCapability string

const (
	capabilityFunctionCalling    providerCapability = "function-calling"
	capabilityReasoningSummary   providerCapability = "reasoning-summary"
	capabilityPreviousResponseID providerCapability = "previous-response-id"
)

type providerCapabilities map[providerCapability]bool

type providerBackend interface {
	Protocol() config.ProviderProtocol
	Capabilities() providerCapabilities
	StreamMessages(context.Context, MessagesRequest, StreamCallbacks) (*StreamResult, error)
}

type providerBackendSpec struct {
	name      string
	role      string
	kind      string
	baseURL   string
	endpoint  string
	apiKey    string
	authToken string
}

type backendFactory func(providerBackendSpec) (providerBackend, error)

// backendRegistry is assembled once per Client and is read-only afterwards. It
// deliberately has no mutation API so request-time code cannot change protocol
// dispatch while concurrent streams are active.
type backendRegistry struct {
	factories map[config.ProviderProtocol]backendFactory
}

func newBackendRegistry() backendRegistry {
	return backendRegistry{factories: map[config.ProviderProtocol]backendFactory{
		config.ProviderProtocolOpenAIResponses: newOpenAIResponsesBackend,
	}}
}

func (r backendRegistry) newBackend(protocol config.ProviderProtocol, spec providerBackendSpec) (providerBackend, error) {
	factory, ok := r.factories[protocol]
	if !ok {
		return nil, fmt.Errorf("no backend registered for provider protocol %q", protocol)
	}
	return factory(spec)
}
