package session

import (
	"os"
	"path/filepath"
	"testing"
)

// AUDIT-P1-36: the cache multipliers are Anthropic's. Grouping them by provider
// kind keeps a non-Anthropic backend from being priced with Anthropic's discount.
func TestCacheMultipliersGroupedByProviderKind(t *testing.T) {
	for _, kind := range []string{"", "anthropic", "anthropic-compatible"} {
		got, ok := CacheMultipliersForProviderKind(kind)
		if !ok {
			t.Fatalf("CacheMultipliersForProviderKind(%q) reported unknown; Anthropic's are published", kind)
		}
		if got != AnthropicCacheMultipliers() {
			t.Fatalf("kind %q multipliers = %+v, want %+v", kind, got, AnthropicCacheMultipliers())
		}
	}
	// "OpenAI-compatible" is a wire protocol, not a vendor: DeepSeek, GLM, Kimi
	// and OpenAI itself all speak it with different cache discounts, so there is
	// no defensible default to fall back on.
	for _, kind := range []string{"custom", "openai", "openai-compatible", "openai-chat-completions", "bedrock"} {
		if got, ok := CacheMultipliersForProviderKind(kind); ok {
			t.Fatalf("CacheMultipliersForProviderKind(%q) = %+v, want unknown", kind, got)
		}
	}
}

// A rate with no known cache multipliers must not silently borrow Anthropic's.
func TestEstimateCostReportsUnknownWhenCacheDiscountIsUnknown(t *testing.T) {
	rates := map[string]Rate{"glm-5.1": {InputPerMTok: 10, OutputPerMTok: 20}}
	for name, usage := range map[string]TokenUsage{
		"cache read": {CacheReadTokens: 1_000_000},
		"5m write":   {CacheWrite5mTokens: 1_000_000},
		"1h write":   {CacheWrite1hTokens: 1_000_000},
		"mixed turn": {InputTokens: 1_000, CacheReadTokens: 500},
	} {
		cost, ok := EstimateCost("glm-5.1", usage, rates)
		if ok {
			t.Fatalf("%s: EstimateCost = %v with ok=true, want unknown pricing", name, cost)
		}
		if cost != 0 {
			t.Fatalf("%s: unknown cost = %v, want 0", name, cost)
		}
	}
}

// Unknown cache multipliers must not poison turns that used no cache at all.
func TestEstimateCostStillPricesCacheFreeTurnsWithUnknownDiscount(t *testing.T) {
	rates := map[string]Rate{"glm-5.1": {InputPerMTok: 10, OutputPerMTok: 20}}
	got, ok := EstimateCost("glm-5.1", TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}, rates)
	if !ok {
		t.Fatal("a turn with zero cache tokens needs no cache price to be known")
	}
	closeTo(t, got, 30, "cache-free cost")
}

// An explicit cache price is the escape hatch: it must work even when the
// provider kind has no default multipliers.
func TestExplicitCacheRatesPriceAnUnknownProvider(t *testing.T) {
	rates := map[string]Rate{"deepseek-v4-flash": {
		InputPerMTok:        0.14,
		OutputPerMTok:       0.28,
		CacheReadPerMTok:    0.0028,
		CacheWrite5mPerMTok: 0.14,
		CacheWrite1hPerMTok: 0.2,
	}}
	usage := TokenUsage{CacheReadTokens: 1_000_000, CacheWrite5mTokens: 1_000_000, CacheWrite1hTokens: 1_000_000}
	got, ok := EstimateCost("deepseek-v4-flash", usage, rates)
	if !ok {
		t.Fatal("explicit cache rates must count as known pricing")
	}
	closeTo(t, got, 0.0028+0.14+0.2, "explicit cache cost")

	// Each tier is resolved on its own: configuring only the read price must not
	// make the write tiers fall back to a borrowed multiplier.
	partial := map[string]Rate{"deepseek-v4-flash": {
		InputPerMTok:     0.14,
		OutputPerMTok:    0.28,
		CacheReadPerMTok: 0.0028,
	}}
	if cost, ok := EstimateCost("deepseek-v4-flash", TokenUsage{CacheWrite5mTokens: 1_000_000}, partial); ok {
		t.Fatalf("5m write cost = %v with ok=true, want unknown when only cacheRead is configured", cost)
	}
	readOnly, ok := EstimateCost("deepseek-v4-flash", TokenUsage{CacheReadTokens: 1_000_000}, partial)
	if !ok {
		t.Fatal("the configured read price must still price a read-only turn")
	}
	closeTo(t, readOnly, 0.0028, "partial explicit cache read cost")
}

// Genuine Anthropic ids keep Anthropic's multipliers without any configuration.
func TestBuiltinAnthropicRateCarriesAnthropicCacheMultipliers(t *testing.T) {
	rate, ok := ResolveRate("claude-haiku-4-5", nil)
	if !ok {
		t.Fatal("claude-haiku-4-5 must resolve from the built-in table")
	}
	if rate.CacheMultipliers != AnthropicCacheMultipliers() {
		t.Fatalf("built-in multipliers = %+v, want %+v", rate.CacheMultipliers, AnthropicCacheMultipliers())
	}
}

func TestConfiguredRatesDerivesCacheTiersOnlyForAnthropicProviders(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("NIUMA_CONFIG_DIR", home)

	writeSettings(t, project, `{
	  "model": "glm-5.1",
	  "provider": "custom",
	  "fallback": {"enabled": true, "providers": [
	    {"name": "relay", "type": "anthropic-compatible", "model": "claude-haiku-4-5"}
	  ]},
	  "modelPricing": {
	    "glm-5.1": {"input": 10, "output": 20},
	    "claude-haiku-4-5": {"input": 1, "output": 5}
	  }
	}`)

	rates := ConfiguredRates(project)

	// The custom-kind primary has no published cache discount we can trust.
	if got, ok := CacheMultipliersForProviderKind("custom"); ok {
		t.Fatalf("guard: custom kind unexpectedly known (%+v)", got)
	}
	if rates["glm-5.1"].CacheMultipliers != (CacheMultipliers{}) {
		t.Fatalf("glm-5.1 multipliers = %+v, want unknown", rates["glm-5.1"].CacheMultipliers)
	}
	if _, ok := EstimateCost("glm-5.1", TokenUsage{CacheReadTokens: 1_000_000}, rates); ok {
		t.Fatal("a custom-kind provider must not be priced with Anthropic's cache discount")
	}

	// The anthropic-compatible relay does follow Anthropic's cache pricing.
	if rates["claude-haiku-4-5"].CacheMultipliers != AnthropicCacheMultipliers() {
		t.Fatalf("relay multipliers = %+v, want Anthropic's", rates["claude-haiku-4-5"].CacheMultipliers)
	}
	got, ok := EstimateCost("claude-haiku-4-5", TokenUsage{CacheReadTokens: 1_000_000}, rates)
	if !ok {
		t.Fatal("anthropic-compatible relay cache read must be priced")
	}
	closeTo(t, got, 1*AnthropicCacheReadMultiplier, "relay cache read cost")
}

// The configured cache price wins over the derived multiplier even when the
// provider kind does have defaults.
func TestConfiguredRatesPreferExplicitCachePriceOverDerivedMultiplier(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("NIUMA_CONFIG_DIR", home)

	writeSettings(t, project, `{
	  "model": "claude-haiku-4-5",
	  "provider": "anthropic",
	  "modelPricing": {"claude-haiku-4-5": {
	    "input": 10, "output": 20, "cacheRead": 4, "cacheWrite5m": 11, "cacheWrite1h": 12
	  }}
	}`)

	rates := ConfiguredRates(project)
	usage := TokenUsage{CacheReadTokens: 1_000_000, CacheWrite5mTokens: 1_000_000, CacheWrite1hTokens: 1_000_000}
	got, ok := EstimateCost("claude-haiku-4-5", usage, rates)
	if !ok {
		t.Fatal("explicitly configured cache rates reported unknown pricing")
	}
	closeTo(t, got, 4+11+12, "explicit cache cost")
}

// Backward compatibility: a model priced with input/output only, served by an
// Anthropic-kind provider, still derives its cache tiers as it did before.
func TestConfiguredRatesWithoutCacheFieldsStayBackwardCompatible(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("NIUMA_CONFIG_DIR", home)

	writeSettings(t, project, `{
	  "model": "claude-haiku-4-5",
	  "modelPricing": {"claude-haiku-4-5": {"input": 10, "output": 20}}
	}`)

	rates := ConfiguredRates(project)
	got, ok := EstimateCost("claude-haiku-4-5", TokenUsage{CacheWrite5mTokens: 1_000_000}, rates)
	if !ok {
		t.Fatal("an Anthropic-kind provider with no cache fields must still derive them")
	}
	closeTo(t, got, 10*AnthropicCacheWrite5mMultiplier, "derived 5m write cost")
}

func TestConfiguredRatesReturnsNilWhenNothingIsPriced(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("NIUMA_CONFIG_DIR", home)

	if rates := ConfiguredRates(project); rates != nil {
		t.Fatalf("ConfiguredRates = %+v, want nil so callers fall back to the built-in table", rates)
	}
}

func writeSettings(t *testing.T, project, content string) {
	t.Helper()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	path := filepath.Join(project, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
