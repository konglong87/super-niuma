package compact

import "github.com/konglong87/super-niuma/internal/config"

func ConfigFromSettings(settings config.Settings, model string) Config {
	var cfg Config
	// Auto-compact is on unless explicitly disabled. Opt-in was the wrong
	// default: an unconfigured long session had no context-overflow protection
	// at all, and the failure mode is a hard API error that ends the session.
	cfg.Enabled = true
	if settings.AutoCompact != nil {
		if settings.AutoCompact.Enabled != nil {
			cfg.Enabled = *settings.AutoCompact.Enabled
		}
		cfg.DefaultThresholdRatio = float64Value(settings.AutoCompact.DefaultThresholdRatio)
		cfg.PreserveRecentRounds = intValue(settings.AutoCompact.PreserveRecentRounds)
		cfg.SummaryModel = stringValue(settings.AutoCompact.SummaryModel)
		cfg.MaxSummaryTokens = intValue(settings.AutoCompact.MaxSummaryTokens)
		cfg.CooldownTurns = intValue(settings.AutoCompact.CooldownTurns)
		cfg.MaxFailures = intValue(settings.AutoCompact.MaxFailures)
		cfg.ModelContext = cloneIntMap(settings.AutoCompact.ModelContext)
		cfg.ModelThresholdRatio = cloneFloatMap(settings.AutoCompact.ModelThresholdRatio)
	}
	if cfg.ModelContext == nil {
		cfg.ModelContext = map[string]int{}
	}
	if settings.ContextLength > 0 && model != "" {
		cfg.ModelContext[model] = settings.ContextLength
	}
	return cfg.WithDefaults()
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func float64Value(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cloneIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]int, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func cloneFloatMap(values map[string]float64) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]float64, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}
