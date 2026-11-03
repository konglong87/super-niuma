package session

import (
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

// Cost estimation, tier by tier.
//
// Cache tokens are not billed at the input rate. Before AUDIT-P0-15 this package
// summed every input tier and charged the full input rate for the lot, which
// overstates a cache-heavy session by close to 10x (a turn that is 99% cache read
// pays ~10x its real price).
//
// These multipliers are Anthropic's, and only Anthropic's — AUDIT-P1-36. Verified
// 2026-07-25 against Anthropic's published pricing, which states them verbatim
// ("5-minute cache write tokens are 1.25 times the base input tokens price,
// 1-hour cache write tokens are 2 times, cache read tokens are 0.1 times"):
//   - https://platform.claude.com/docs/en/build-with-claude/prompt-caching (Pricing)
//   - https://platform.claude.com/docs/en/about-claude/pricing (Prompt caching)
//
// Other vendors are nowhere near these ratios, which is why they are not a global
// default: DeepSeek's published V4 rate card puts cache-hit input around 0.02x
// (Flash, $0.0028 vs $0.14) and under 0.01x (Pro), and has no cache-write tier at
// all. CacheMultipliersForProviderKind is the grouping.
const (
	AnthropicCacheWrite5mMultiplier = 1.25
	AnthropicCacheWrite1hMultiplier = 2.0
	AnthropicCacheReadMultiplier    = 0.1
)

// CacheMultipliers derives the three cache-tier prices from a base input rate.
// The zero value means "unknown": this runtime will not guess a provider's cache
// discount, because guessing is indistinguishable from being wrong.
type CacheMultipliers struct {
	Write5m float64
	Write1h float64
	Read    float64
}

func (m CacheMultipliers) known() bool { return m != CacheMultipliers{} }

// AnthropicCacheMultipliers returns Anthropic's published cache pricing ratios.
func AnthropicCacheMultipliers() CacheMultipliers {
	return CacheMultipliers{
		Write5m: AnthropicCacheWrite5mMultiplier,
		Write1h: AnthropicCacheWrite1hMultiplier,
		Read:    AnthropicCacheReadMultiplier,
	}
}

// CacheMultipliersForProviderKind groups the default cache-price ratios by
// provider kind. Only Anthropic-shaped providers have a published, uniform ratio;
// "OpenAI-compatible" is a wire protocol rather than a vendor, so DeepSeek, GLM,
// Kimi and OpenAI itself all arrive under it with different discounts and no
// single default is defensible. Those fall back to unknown, and the operator
// supplies the real numbers via settings.modelPricing's cache fields.
func CacheMultipliersForProviderKind(kind string) (CacheMultipliers, bool) {
	if config.ProviderKindAnthropic(kind) {
		return AnthropicCacheMultipliers(), true
	}
	return CacheMultipliers{}, false
}

// TokenUsage is a per-tier token count with disjoint tiers: InputTokens counts
// uncached prompt input only.
type TokenUsage struct {
	InputTokens        int
	CacheWrite5mTokens int
	CacheWrite1hTokens int
	CacheReadTokens    int
	OutputTokens       int
}

// ReportedUsage mirrors the counters as this codebase records them, where
// InputTokens is the *whole* prompt (uncached + cache creation + cache read).
// That sum is deliberate: the context-window estimator needs the full prompt
// size. Billing needs the tiers apart, which is what Tiers does.
type ReportedUsage struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	CacheCreation5mTokens    int
	CacheCreation1hTokens    int
	OutputTokens             int
}

// Tiers splits ReportedUsage into disjoint billing tiers.
func (r ReportedUsage) Tiers() TokenUsage {
	write5m := r.CacheCreation5mTokens
	write1h := r.CacheCreation1hTokens
	// Responses that report only a cache-creation total get it all attributed to
	// the 5-minute tier: that is the default TTL, so it is the correct guess as
	// well as the cheaper one.
	if write5m == 0 && write1h == 0 {
		write5m = r.CacheCreationInputTokens
	}
	uncached := r.InputTokens - r.CacheCreationInputTokens - r.CacheReadInputTokens
	// A provider that already excludes the cache tiers from input_tokens would
	// drive this negative; charge zero rather than a credit.
	if uncached < 0 {
		uncached = 0
	}
	return TokenUsage{
		InputTokens:        uncached,
		CacheWrite5mTokens: write5m,
		CacheWrite1hTokens: write1h,
		CacheReadTokens:    r.CacheReadInputTokens,
		OutputTokens:       r.OutputTokens,
	}
}

// Rate is a per-million-token price. The three cache fields are optional: a zero
// one means "derive it from InputPerMTok via CacheMultipliers". CacheMultipliers
// itself may be unknown, in which case a turn that actually used that cache tier
// has no knowable price and EstimateCost reports it as unpriced rather than
// inventing a ratio (AUDIT-P1-36).
type Rate struct {
	InputPerMTok        float64
	OutputPerMTok       float64
	CacheWrite5mPerMTok float64
	CacheWrite1hPerMTok float64
	CacheReadPerMTok    float64
	CacheMultipliers    CacheMultipliers
}

func (r Rate) priced() bool { return r.InputPerMTok > 0 || r.OutputPerMTok > 0 }

// derived resolves one cache tier: an explicit price wins, otherwise the tier is
// derived from the input rate, and failing that it is unknown.
func (r Rate) derived(explicitPerMTok, multiplier float64) (float64, bool) {
	if explicitPerMTok > 0 {
		return explicitPerMTok, true
	}
	if multiplier > 0 {
		return r.InputPerMTok * multiplier, true
	}
	return 0, false
}

func (r Rate) cacheWrite5m() (float64, bool) {
	return r.derived(r.CacheWrite5mPerMTok, r.CacheMultipliers.Write5m)
}

func (r Rate) cacheWrite1h() (float64, bool) {
	return r.derived(r.CacheWrite1hPerMTok, r.CacheMultipliers.Write1h)
}

func (r Rate) cacheRead() (float64, bool) {
	return r.derived(r.CacheReadPerMTok, r.CacheMultipliers.Read)
}

// cacheCost prices the cache tiers that this usage actually touched. A tier with
// no tokens needs no price, so an unknown cache discount only makes a turn
// unpriceable when that turn really used the cache.
func (r Rate) cacheCost(usage TokenUsage) (float64, bool) {
	tiers := []struct {
		tokens int
		price  func() (float64, bool)
	}{
		{usage.CacheWrite5mTokens, r.cacheWrite5m},
		{usage.CacheWrite1hTokens, r.cacheWrite1h},
		{usage.CacheReadTokens, r.cacheRead},
	}
	total := 0.0
	for _, tier := range tiers {
		if tier.tokens <= 0 {
			continue
		}
		price, known := tier.price()
		if !known {
			return 0, false
		}
		total += perMTok(tier.tokens, price)
	}
	return total, true
}

// EstimateCost prices usage tier by tier. ok is false when no rate is known for
// the model, or when the turn used a cache tier whose price cannot be derived;
// callers must surface that rather than reporting a $0 cost, which is
// indistinguishable from a free session (AUDIT-P0-15).
func EstimateCost(model string, usage TokenUsage, rates map[string]Rate) (cost float64, ok bool) {
	rate, ok := ResolveRate(model, rates)
	if !ok {
		return 0, false
	}
	cacheCost, ok := rate.cacheCost(usage)
	if !ok {
		return 0, false
	}
	return perMTok(usage.InputTokens, rate.InputPerMTok) +
		perMTok(usage.OutputTokens, rate.OutputPerMTok) +
		cacheCost, true
}

// ConfiguredRates converts settings.modelPricing into billing rates, resolving
// each model's provider kind so its cache tiers derive from the right ratios (or
// from none at all). Returns nil when nothing is configured, so callers fall back
// to the built-in Anthropic table.
func ConfiguredRates(cwd string) map[string]Rate {
	pricing := config.ModelPricing(cwd)
	if len(pricing) == 0 {
		return nil
	}
	rates := make(map[string]Rate, len(pricing))
	for model, price := range pricing {
		rate := Rate{
			InputPerMTok:        price.Input,
			OutputPerMTok:       price.Output,
			CacheWrite5mPerMTok: price.CacheWrite5m,
			CacheWrite1hPerMTok: price.CacheWrite1h,
			CacheReadPerMTok:    price.CacheRead,
		}
		if kind, found := config.ProviderKindForModel(cwd, model); found {
			rate.CacheMultipliers, _ = CacheMultipliersForProviderKind(kind)
		}
		rates[model] = rate
	}
	return rates
}

func perMTok(tokens int, ratePerMTok float64) float64 {
	if tokens <= 0 {
		return 0
	}
	return float64(tokens) / 1_000_000 * ratePerMTok
}

// ResolveRate returns the price for a model. Configured pricing wins over the
// built-in table so an operator can always override, including for a model whose
// id looks Anthropic-shaped.
func ResolveRate(model string, rates map[string]Rate) (Rate, bool) {
	if rate, found := rates[model]; found && rate.priced() {
		return rate, true
	}
	return builtinAnthropicRate(model)
}

// builtinAnthropicRate prices genuine Anthropic model ids only. This used to be
// a strings.Contains scan, which handed Anthropic's official price to any
// third-party gateway whose model name happened to contain "sonnet-4". Because the
// id shape alone establishes the vendor here, these rates always carry Anthropic's
// cache multipliers.
func builtinAnthropicRate(model string) (Rate, bool) {
	rate, ok := builtinAnthropicBaseRate(model)
	if !ok {
		return Rate{}, false
	}
	rate.CacheMultipliers = AnthropicCacheMultipliers()
	return rate, true
}

func builtinAnthropicBaseRate(model string) (Rate, bool) {
	family, ok := anthropicModelFamily(model)
	if !ok {
		return Rate{}, false
	}
	switch {
	case hasAnyPrefix(family, "opus-4-8", "opus-4-7", "opus-4-6", "opus-4-5"):
		return Rate{InputPerMTok: 5, OutputPerMTok: 25}, true
	case hasAnyPrefix(family, "opus-4-1", "opus-4"):
		return Rate{InputPerMTok: 15, OutputPerMTok: 75}, true
	case hasAnyPrefix(family, "sonnet-4-6", "sonnet-4-5", "sonnet-4"):
		return Rate{InputPerMTok: 3, OutputPerMTok: 15}, true
	case hasAnyPrefix(family, "haiku-4-5"):
		return Rate{InputPerMTok: 1, OutputPerMTok: 5}, true
	// Legacy ids put the version before the family: claude-3-5-haiku-20241022.
	case hasAnyPrefix(family, "haiku-3-5", "3-5-haiku"):
		return Rate{InputPerMTok: 0.80, OutputPerMTok: 4}, true
	case hasAnyPrefix(family, "haiku-3", "3-haiku"):
		return Rate{InputPerMTok: 0.25, OutputPerMTok: 1.25}, true
	default:
		return Rate{}, false
	}
}

// anthropicModelFamily strips the vendor decoration Bedrock and Vertex add plus
// the mandatory "claude-" prefix, e.g.
// "us.anthropic.claude-sonnet-4-5-v1:0" -> "sonnet-4-5-v1:0". It fails for any
// id that is not an Anthropic model id, which is the point.
func anthropicModelFamily(model string) (string, bool) {
	id := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"us.anthropic.", "eu.anthropic.", "apac.anthropic.", "anthropic.", "anthropic/"} {
		if strings.HasPrefix(id, prefix) {
			id = strings.TrimPrefix(id, prefix)
			break
		}
	}
	if !strings.HasPrefix(id, "claude-") {
		return "", false
	}
	return strings.TrimPrefix(id, "claude-"), true
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
