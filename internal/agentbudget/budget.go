// Package agentbudget bounds the cumulative token and cost usage of sub-agent
// runs so one bad delegation cannot burn an unbounded amount of the session.
//
// Scope note: this is a circuit breaker for the sub-agent fan-out path only
// (agentruntime). The parent/main conversation loop is deliberately not metered
// here — tripping the breaker mid-conversation would be a worse failure than
// the runaway it prevents, and the runaway documented in AUDIT-P0-14 lives in
// the sub-agent path (recursion x batch concurrency x turns x max_tokens).
//
// Accounting (settled by AUDIT-P0-15). The two figures fed in here now have
// deliberately different meanings:
//
//   - Tokens are the full prompt size, cache reads included. That is not a
//     billing number and is not meant to be one: a token ceiling bounds how much
//     work a fan-out is doing, and a cache read still consumes context window.
//     This is unchanged by AUDIT-P0-15, so the token breaker trips exactly as
//     before.
//   - Cost is now tiered (cache read 0.1x, 5m write 1.25x, 1h write 2x) and is
//     the real billable figure, where it used to be inflated by up to ~10x on a
//     cache-heavy run. A cost ceiling therefore trips *later* than it did — but
//     it now trips on money actually spent instead of on a number that was
//     simply wrong, which is the only useful behaviour for a currency limit.
//
// DefaultMaxCostUSD stays 0 (disabled), so this changes no default behaviour:
// the only breaker armed out of the box is the token one, and that one did not
// move. Operators who set a cost ceiling were previously tripping it ~10x early.
package agentbudget

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	// EnvMaxTotalTokens / EnvMaxCostUSD override the defaults below. 0 disables
	// the corresponding limit.
	EnvMaxTotalTokens = "NIUMA_SUBAGENT_TOKEN_BUDGET"
	EnvMaxCostUSD     = "NIUMA_SUBAGENT_COST_BUDGET_USD"

	// DefaultMaxTotalTokens is deliberately far above any legitimate session so
	// normal work never notices it, while still bounding a runaway fan-out to
	// something recoverable. With the depth and concurrency caps in place the
	// worst case fan-out reaches this within seconds of misbehaving.
	DefaultMaxTotalTokens = 20_000_000

	// DefaultMaxCostUSD is 0 (disabled): a currency limit only means something
	// once per-model pricing is configured, and an unpriced model contributes $0
	// no matter how much it spends. The token ceiling above is what actually
	// guards the default install. Operators who want a hard money ceiling set
	// EnvMaxCostUSD explicitly, and since AUDIT-P0-15 it is measured against real
	// tiered cost rather than an inflated estimate.
	DefaultMaxCostUSD = 0.0
)

// ErrExhausted marks every budget rejection so callers can classify it without
// string matching.
var ErrExhausted = errors.New("sub-agent budget exhausted")

// Limits are the ceilings for one budget. A zero value in either field disables
// that ceiling.
type Limits struct {
	MaxTotalTokens int
	MaxCostUSD     float64
}

// DefaultLimits resolves the process defaults, honouring the env overrides.
func DefaultLimits() Limits {
	return Limits{
		MaxTotalTokens: envInt(EnvMaxTotalTokens, DefaultMaxTotalTokens),
		MaxCostUSD:     envFloat(EnvMaxCostUSD, DefaultMaxCostUSD),
	}
}

// Snapshot is a point-in-time read of a budget, for reporting.
type Snapshot struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	CostUSD      float64
	Limits       Limits
}

// Budget accumulates sub-agent usage across every run that shares it. A nil
// *Budget is a valid unlimited budget so call sites need no nil checks.
type Budget struct {
	mu           sync.Mutex
	limits       Limits
	inputTokens  int
	outputTokens int
	costUSD      float64
}

// New returns a budget with the given limits.
func New(limits Limits) *Budget {
	if limits.MaxTotalTokens < 0 {
		limits.MaxTotalTokens = 0
	}
	if limits.MaxCostUSD < 0 {
		limits.MaxCostUSD = 0
	}
	return &Budget{limits: limits}
}

// NewDefault returns a budget using DefaultLimits.
func NewDefault() *Budget { return New(DefaultLimits()) }

// Check reports why the budget is spent, or nil while it still has room. It is
// safe to call on a nil budget.
func (b *Budget) Check() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	total := b.inputTokens + b.outputTokens
	if b.limits.MaxTotalTokens > 0 && total >= b.limits.MaxTotalTokens {
		return fmt.Errorf("%w: sub-agents have used %d tokens, reaching the %d-token session budget; raise %s or split the work into a new session",
			ErrExhausted, total, b.limits.MaxTotalTokens, EnvMaxTotalTokens)
	}
	if b.limits.MaxCostUSD > 0 && b.costUSD >= b.limits.MaxCostUSD {
		return fmt.Errorf("%w: sub-agents have used an estimated $%.4f, reaching the $%.4f session budget; raise %s or split the work into a new session",
			ErrExhausted, b.costUSD, b.limits.MaxCostUSD, EnvMaxCostUSD)
	}
	return nil
}

// Add records one sub-agent turn's usage. Safe on a nil budget.
func (b *Budget) Add(inputTokens, outputTokens int, costUSD float64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if inputTokens > 0 {
		b.inputTokens += inputTokens
	}
	if outputTokens > 0 {
		b.outputTokens += outputTokens
	}
	if costUSD > 0 {
		b.costUSD += costUSD
	}
}

// Snapshot reads the current totals. Safe on a nil budget.
func (b *Budget) Snapshot() Snapshot {
	if b == nil {
		return Snapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return Snapshot{
		InputTokens:  b.inputTokens,
		OutputTokens: b.outputTokens,
		TotalTokens:  b.inputTokens + b.outputTokens,
		CostUSD:      b.costUSD,
		Limits:       b.limits,
	}
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
