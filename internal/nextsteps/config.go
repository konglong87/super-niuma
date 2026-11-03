// Model selection uses only configured aliases or the current session model.

package nextsteps

import "strings"

// Legacy alias for the optional inexpensive model, shared with subagents.
const modelTierHaiku = "haiku"

type Config struct {
	Enabled bool
	Model   string
	Count   int
}

func (c Config) WithDefaults(parentModel string, tierModels map[string]string) Config {
	switch {
	case c.Count <= 0:
		c.Count = DefaultCount
	case c.Count > MaxCount:
		c.Count = MaxCount
	}
	c.Model = resolveModel(c.Model, parentModel, tierModels)
	return c
}

func resolveModel(configured, parentModel string, tierModels map[string]string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	parent := strings.TrimSpace(parentModel)
	if tier := strings.TrimSpace(tierModels[modelTierHaiku]); tier != "" {
		return tier
	}
	return parent
}
