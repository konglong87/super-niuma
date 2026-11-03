package agentruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

// NewProviderPreflight builds a deterministic startup gate from configured
// providers. It intentionally performs no network call; deployments can layer
// a health probe through Runtime.ProviderPreflight when live checks are needed.
func NewProviderPreflight(providers []config.ProviderConfig) func(context.Context, string, string) error {
	byName := make(map[string]config.ProviderConfig, len(providers))
	for _, provider := range providers {
		name := strings.TrimSpace(provider.Name)
		if name != "" {
			byName[name] = provider
		}
	}
	return func(ctx context.Context, name, model string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		name = strings.TrimSpace(name)
		provider, ok := byName[name]
		if !ok {
			return fmt.Errorf("provider_not_configured: %s", name)
		}
		configuredModel := strings.TrimSpace(provider.Model)
		model = strings.TrimSpace(model)
		if configuredModel != "" && model != "" && configuredModel != model {
			return fmt.Errorf("model_not_supported: provider %s serves %s", name, configuredModel)
		}
		if strings.TrimSpace(provider.APIKey) == "" && strings.TrimSpace(provider.AuthToken) == "" {
			return fmt.Errorf("credential_missing: provider %s", name)
		}
		return nil
	}
}
