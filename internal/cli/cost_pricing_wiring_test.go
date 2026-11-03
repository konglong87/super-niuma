package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/session"
)

// `cost` must apply settings.modelPricing, otherwise a non-Anthropic provider
// reports every session as unpriced (AUDIT-P0-15).
func TestCostCommandAppliesConfiguredModelPricing(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	if err := os.MkdirAll(filepath.Join(project, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config", "settings.json"),
		[]byte(`{"modelPricing":{"glm-5.1":{"input":10,"output":20}}}`), 0600); err != nil {
		t.Fatal(err)
	}

	store := session.Store{Root: filepath.Join(home, "sessions")}
	recorder, err := store.NewRecorder(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Type: "usage", Model: "glm-5.1", InputTokens: 1_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	rates := configuredCostRates(project)
	if got, ok := rates["glm-5.1"]; !ok || got.InputPerMTok != 10 || got.OutputPerMTok != 20 {
		t.Fatalf("configuredCostRates = %+v, want glm-5.1 at 10/20", rates)
	}

	usage, err := store.UsageWithRates(rates)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.UnknownPricingModels) != 0 {
		t.Fatalf("UnknownPricingModels = %+v, want none once modelPricing is configured", usage.UnknownPricingModels)
	}
	if usage.CostUSD != 10 {
		t.Fatalf("CostUSD = %v, want 10", usage.CostUSD)
	}
}
