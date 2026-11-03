package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestProviderPreflightRejectsUnknownModelAndMissingCredential(t *testing.T) {
	check := NewProviderPreflight([]config.ProviderConfig{{Name: "primary", Model: "model-a", APIKey: ""}})
	for _, tc := range []struct {
		name, provider, model, want string
	}{
		{"unknown provider", "missing", "model-a", "provider_not_configured"},
		{"unsupported model", "primary", "model-b", "model_not_supported"},
		{"missing credential", "primary", "model-a", "credential_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := check(context.Background(), tc.provider, tc.model)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %s", err, tc.want)
			}
		})
	}
}
