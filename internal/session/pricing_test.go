package session

import (
	"math"
	"testing"
)

func closeTo(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// The gold standard for AUDIT-P0-15: a realistic cache-heavy turn (a small
// uncached tail on top of a large cache read) must cost roughly a tenth of what
// the old flat-rate arithmetic charged, because a cache read bills at 0.1x the
// input rate rather than 1x.
func TestEstimateCostPricesCacheHeavyTurnAtATenthOfFlatRate(t *testing.T) {
	const (
		uncachedInput = 1_000
		cacheRead     = 200_000
		// claude-haiku-4-5 input rate, per million tokens.
		inputPerMTok = 1.0
	)
	usage := TokenUsage{InputTokens: uncachedInput, CacheReadTokens: cacheRead}

	tiered, ok := EstimateCost("claude-haiku-4-5", usage, nil)
	if !ok {
		t.Fatal("EstimateCost reported unknown pricing for claude-haiku-4-5")
	}

	// What the code did before this fix: sum every input tier, charge full rate.
	flat := float64(uncachedInput+cacheRead) / 1_000_000 * inputPerMTok
	want := float64(uncachedInput)/1_000_000*inputPerMTok +
		float64(cacheRead)/1_000_000*inputPerMTok*AnthropicCacheReadMultiplier
	closeTo(t, tiered, want, "tiered cost")

	ratio := flat / tiered
	if ratio < 9 || ratio > 10 {
		t.Fatalf("flat/tiered = %v (flat=%v tiered=%v), want the ~10x overestimate to be gone", ratio, flat, tiered)
	}
}

func TestEstimateCostChargesEveryTierAtItsOwnMultiple(t *testing.T) {
	// One million tokens in each tier makes each tier's multiplier readable
	// straight off the total. claude-haiku-4-5: input 1, output 5 per MTok.
	usage := TokenUsage{
		InputTokens:        1_000_000,
		CacheWrite5mTokens: 1_000_000,
		CacheWrite1hTokens: 1_000_000,
		CacheReadTokens:    1_000_000,
		OutputTokens:       1_000_000,
	}
	got, ok := EstimateCost("claude-haiku-4-5", usage, nil)
	if !ok {
		t.Fatal("EstimateCost reported unknown pricing for claude-haiku-4-5")
	}
	want := 1.0 + 1.0*AnthropicCacheWrite5mMultiplier + 1.0*AnthropicCacheWrite1hMultiplier + 1.0*AnthropicCacheReadMultiplier + 5.0
	closeTo(t, got, want, "all-tier cost")
}

func TestAnthropicCacheMultipliersMatchPublishedPricing(t *testing.T) {
	if AnthropicCacheWrite5mMultiplier != 1.25 {
		t.Fatalf("AnthropicCacheWrite5mMultiplier = %v, want 1.25", AnthropicCacheWrite5mMultiplier)
	}
	if AnthropicCacheWrite1hMultiplier != 2 {
		t.Fatalf("AnthropicCacheWrite1hMultiplier = %v, want 2", AnthropicCacheWrite1hMultiplier)
	}
	if AnthropicCacheReadMultiplier != 0.1 {
		t.Fatalf("AnthropicCacheReadMultiplier = %v, want 0.1", AnthropicCacheReadMultiplier)
	}
}

// Before this fix modelRates used strings.Contains, so any model id containing
// "sonnet-4" inherited Anthropic's official price.
func TestEstimateCostRejectsThirdPartyGatewayNamesThatMerelyContainAFamily(t *testing.T) {
	for _, model := range []string{"my-sonnet-4-proxy", "sonnet-4-clone", "glm-opus-4-8-lookalike", "gpt-5.5"} {
		if cost, ok := EstimateCost(model, TokenUsage{InputTokens: 1_000_000}, nil); ok {
			t.Fatalf("EstimateCost(%q) claimed to know the price (%v); only genuine Anthropic ids may hit the built-in table", model, cost)
		}
	}
}

func TestEstimateCostMatchesGenuineAnthropicModelIDs(t *testing.T) {
	cases := map[string]float64{
		// Plain ids.
		"claude-haiku-4-5":           1,
		"claude-sonnet-4-5-20250929": 3,
		"claude-opus-4-8":            5,
		"claude-opus-4":              15,
		// Bedrock and Vertex decorate the same id.
		"anthropic.claude-haiku-4-5-v1:0":     1,
		"us.anthropic.claude-sonnet-4-5-v1:0": 3,
		"claude-sonnet-4-5@20250929":          3,
	}
	for model, wantInputRate := range cases {
		got, ok := EstimateCost(model, TokenUsage{InputTokens: 1_000_000}, nil)
		if !ok {
			t.Fatalf("EstimateCost(%q) reported unknown pricing", model)
		}
		closeTo(t, got, wantInputRate, "input cost for "+model)
	}
}

func TestEstimateCostPrefersConfiguredRateOverBuiltinTable(t *testing.T) {
	// A gateway serving something under an Anthropic-looking id must be priced
	// by config, not by the built-in table.
	rates := map[string]Rate{"claude-haiku-4-5": {InputPerMTok: 10, OutputPerMTok: 20}}
	got, ok := EstimateCost("claude-haiku-4-5", TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}, rates)
	if !ok {
		t.Fatal("configured rate reported unknown pricing")
	}
	closeTo(t, got, 30, "configured cost")
}

// A configured rate whose provider does follow Anthropic's cache pricing derives
// its cache tiers from the input rate. This asserted the same thing before
// AUDIT-P1-36 but with no CacheMultipliers set, which made it a guarantee that
// *every* configured model gets Anthropic's ratios — see
// TestEstimateCostReportsUnknownWhenCacheDiscountIsUnknown for the new contract.
func TestConfiguredRateDerivesCacheTiersFromInputRate(t *testing.T) {
	rates := map[string]Rate{"claude-relay": {
		InputPerMTok:     10,
		OutputPerMTok:    20,
		CacheMultipliers: AnthropicCacheMultipliers(),
	}}
	usage := TokenUsage{CacheReadTokens: 1_000_000, CacheWrite1hTokens: 1_000_000}
	got, ok := EstimateCost("claude-relay", usage, rates)
	if !ok {
		t.Fatal("configured rate reported unknown pricing")
	}
	closeTo(t, got, 10*AnthropicCacheReadMultiplier+10*AnthropicCacheWrite1hMultiplier, "derived cache cost")
}

func TestConfiguredRateHonoursExplicitCacheRates(t *testing.T) {
	rates := map[string]Rate{"glm-5.1": {
		InputPerMTok:     10,
		OutputPerMTok:    20,
		CacheReadPerMTok: 4,
	}}
	got, ok := EstimateCost("glm-5.1", TokenUsage{CacheReadTokens: 1_000_000}, rates)
	if !ok {
		t.Fatal("configured rate reported unknown pricing")
	}
	closeTo(t, got, 4, "explicit cache read cost")
}

// An unpriced model must be reported as unknown, not silently valued at $0.
func TestEstimateCostReportsUnknownModelInsteadOfZero(t *testing.T) {
	cost, ok := EstimateCost("glm-5.1", TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}, nil)
	if ok {
		t.Fatalf("EstimateCost claimed to price an unconfigured model, cost=%v", cost)
	}
	if cost != 0 {
		t.Fatalf("unknown-model cost = %v, want 0 alongside ok=false", cost)
	}
}

func TestEstimateCostTreatsBlankConfiguredRateAsUnknown(t *testing.T) {
	rates := map[string]Rate{"glm-5.1": {}}
	if _, ok := EstimateCost("glm-5.1", TokenUsage{InputTokens: 1_000_000}, rates); ok {
		t.Fatal("an all-zero configured rate must not count as known pricing")
	}
}

func TestReportedUsageTiersSubtractsCacheTiersFromTotalInput(t *testing.T) {
	// InputTokens as recorded here is the whole prompt: uncached + creation +
	// read. Billing needs them apart.
	reported := ReportedUsage{
		InputTokens:              201_000,
		CacheCreationInputTokens: 50_000,
		CacheReadInputTokens:     150_000,
		CacheCreation5mTokens:    20_000,
		CacheCreation1hTokens:    30_000,
		OutputTokens:             500,
	}
	tiers := reported.Tiers()
	if tiers.InputTokens != 1_000 {
		t.Fatalf("uncached input = %d, want 1000", tiers.InputTokens)
	}
	if tiers.CacheReadTokens != 150_000 {
		t.Fatalf("cache read = %d, want 150000", tiers.CacheReadTokens)
	}
	if tiers.CacheWrite5mTokens != 20_000 || tiers.CacheWrite1hTokens != 30_000 {
		t.Fatalf("cache writes = 5m:%d 1h:%d, want 20000/30000", tiers.CacheWrite5mTokens, tiers.CacheWrite1hTokens)
	}
	if tiers.OutputTokens != 500 {
		t.Fatalf("output = %d, want 500", tiers.OutputTokens)
	}
}

// Older responses report a cache-creation total with no 5m/1h split. The default
// cache TTL is 5 minutes, so the whole total belongs in that tier — dropping it
// would under-bill, and putting it in the 1h tier would over-bill.
func TestReportedUsageTiersDefaultsUnsplitCacheWritesTo5m(t *testing.T) {
	reported := ReportedUsage{
		InputTokens:              41_000,
		CacheCreationInputTokens: 40_000,
	}
	tiers := reported.Tiers()
	if tiers.CacheWrite5mTokens != 40_000 || tiers.CacheWrite1hTokens != 0 {
		t.Fatalf("cache writes = 5m:%d 1h:%d, want 40000/0", tiers.CacheWrite5mTokens, tiers.CacheWrite1hTokens)
	}
	if tiers.InputTokens != 1_000 {
		t.Fatalf("uncached input = %d, want 1000", tiers.InputTokens)
	}
}

// Some providers report input_tokens already excluding the cache tiers. The
// subtraction must not produce a negative charge.
func TestReportedUsageTiersNeverGoesNegative(t *testing.T) {
	reported := ReportedUsage{
		InputTokens:          1_000,
		CacheReadInputTokens: 150_000,
	}
	if got := reported.Tiers().InputTokens; got != 0 {
		t.Fatalf("uncached input = %d, want 0", got)
	}
}

// Store.Usage must price a cache-heavy session at the tiered rate, not by
// charging the whole prompt at the input rate (AUDIT-P0-15).
func TestStoreUsagePricesCacheReadAtTieredRate(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder("/tmp/pricing")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{
		Type:                 "usage",
		Model:                "claude-haiku-4-5",
		InputTokens:          1_000,
		CacheReadInputTokens: 200_000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	// claude-haiku-4-5: $1/MTok input.
	want := 1_000.0/1_000_000*1.0 + 200_000.0/1_000_000*1.0*AnthropicCacheReadMultiplier
	flat := 201_000.0 / 1_000_000 * 1.0
	closeTo(t, usage.CostUSD, want, "summary cost")
	if flat/usage.CostUSD < 9 {
		t.Fatalf("cost %v is not ~10x below the old flat-rate %v", usage.CostUSD, flat)
	}
	if !usage.Models["claude-haiku-4-5"].CostKnown {
		t.Fatal("CostKnown = false for a priced Anthropic model")
	}
	if len(usage.UnknownPricingModels) != 0 {
		t.Fatalf("UnknownPricingModels = %+v, want none", usage.UnknownPricingModels)
	}
}

// A session on an unpriced model must be named, not folded into a $0 total.
func TestStoreUsageReportsUnknownPricingModels(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder("/tmp/unpriced")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "usage", Model: "glm-5.1", InputTokens: 1_000_000, OutputTokens: 1_000}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.UnknownPricingModels) != 1 || usage.UnknownPricingModels[0] != "glm-5.1" {
		t.Fatalf("UnknownPricingModels = %+v, want [glm-5.1]", usage.UnknownPricingModels)
	}
	if usage.Models["glm-5.1"].CostKnown {
		t.Fatal("CostKnown = true for an unpriced model")
	}

	// Configuring a rate resolves it.
	rates := map[string]Rate{"glm-5.1": {InputPerMTok: 10, OutputPerMTok: 20}}
	priced, err := store.UsageWithRates(rates)
	if err != nil {
		t.Fatal(err)
	}
	if len(priced.UnknownPricingModels) != 0 {
		t.Fatalf("UnknownPricingModels = %+v after configuring a rate, want none", priced.UnknownPricingModels)
	}
	closeTo(t, priced.CostUSD, 10+1_000.0/1_000_000*20, "configured summary cost")
}

func TestStoreUsageExposesTurnUsageReadback(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{Type: "usage", Turn: 2, Provider: "anthropic", UsageSource: "provider", Model: "claude-sonnet-4-6", InputTokens: 100, OutputTokens: 20}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.TurnUsages) != 1 {
		t.Fatalf("turn usages = %+v", usage.TurnUsages)
	}
	turn := usage.TurnUsages[0]
	if turn.SessionID != "44444444-4444-4444-8444-444444444444" || turn.Turn != 2 || turn.Provider != "anthropic" || turn.Source != "provider" || turn.TotalTokens() != 120 {
		t.Fatalf("turn usage = %+v", turn)
	}
}
