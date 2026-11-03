package compact

import (
	"math"
	"strings"
	"time"
)

const (
	DefaultContextTokens   = 200_000
	DefaultThresholdRatio  = 0.75
	DefaultRecentRounds    = 6
	DefaultMaxSummaryToken = 8_000
	DefaultMaxFailures     = 3
	DefaultCooldownTurns   = 1
	// DefaultFailureResetAfter bounds how long consecutive summary failures keep
	// the compaction circuit open. Without a reset dimension three transient
	// failures disabled compaction for the rest of the session.
	DefaultFailureResetAfter = 10 * time.Minute
)

type Config struct {
	Enabled               bool
	DefaultThresholdRatio float64
	PreserveRecentRounds  int
	SummaryModel          string
	MaxSummaryTokens      int
	CooldownTurns         int
	MaxFailures           int
	FailureResetAfter     time.Duration
	ModelContext          map[string]int
	ModelThresholdRatio   map[string]float64
}

func (c Config) WithDefaults() Config {
	if c.DefaultThresholdRatio <= 0 || c.DefaultThresholdRatio > 1 {
		c.DefaultThresholdRatio = DefaultThresholdRatio
	}
	if c.PreserveRecentRounds <= 0 {
		c.PreserveRecentRounds = DefaultRecentRounds
	}
	if c.MaxSummaryTokens <= 0 {
		c.MaxSummaryTokens = DefaultMaxSummaryToken
	}
	if c.MaxFailures <= 0 {
		c.MaxFailures = DefaultMaxFailures
	}
	if c.CooldownTurns <= 0 {
		c.CooldownTurns = DefaultCooldownTurns
	}
	if c.FailureResetAfter <= 0 {
		c.FailureResetAfter = DefaultFailureResetAfter
	}
	return c
}

func (c Config) ContextTokens(model string) int {
	c = c.WithDefaults()
	if value := lookupModelInt(c.ModelContext, model); value > 0 {
		return value
	}
	return DefaultContextTokens
}

func (c Config) ThresholdRatio(model string) float64 {
	c = c.WithDefaults()
	if value := lookupModelFloat(c.ModelThresholdRatio, model); value > 0 && value <= 1 {
		return value
	}
	return c.DefaultThresholdRatio
}

func (c Config) ThresholdTokens(model string) int {
	contextTokens := c.ContextTokens(model)
	ratio := c.ThresholdRatio(model)
	return int(math.Floor(float64(contextTokens) * ratio))
}

func lookupModelInt(values map[string]int, model string) int {
	if len(values) == 0 {
		return 0
	}
	if value := values[model]; value > 0 {
		return value
	}
	normalized := strings.ToLower(strings.TrimSpace(model))
	for key, value := range values {
		if value <= 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(key))
		if k != "" && (normalized == k || strings.Contains(normalized, k)) {
			return value
		}
	}
	return 0
}

func lookupModelFloat(values map[string]float64, model string) float64 {
	if len(values) == 0 {
		return 0
	}
	if value := values[model]; value > 0 {
		return value
	}
	normalized := strings.ToLower(strings.TrimSpace(model))
	for key, value := range values {
		if value <= 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(key))
		if k != "" && (normalized == k || strings.Contains(normalized, k)) {
			return value
		}
	}
	return 0
}
