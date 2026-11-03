package recap

import "github.com/konglong87/super-niuma/internal/config"

func ConfigFromSettings(settings config.Settings, model string) Config {
	cfg := Config{
		IncludeSessionMemory:  true,
		IncludeCompactSummary: true,
	}
	if settings.Recap != nil {
		cfg.Enabled = settings.Recap.Enabled != nil && *settings.Recap.Enabled
		cfg.Mode = settings.Recap.Mode
		cfg.Model = settings.Recap.Model
		cfg.RecentMessageWindow = intValue(settings.Recap.RecentMessageWindow)
		cfg.MaxTokens = intValue(settings.Recap.MaxTokens)
		cfg.AwayDelaySeconds = intValue(settings.Recap.AwayDelaySeconds)
		if settings.Recap.IncludeSessionMemory != nil {
			cfg.IncludeSessionMemory = *settings.Recap.IncludeSessionMemory
		}
		if settings.Recap.IncludeCompactSummary != nil {
			cfg.IncludeCompactSummary = *settings.Recap.IncludeCompactSummary
		}
	}
	return cfg.WithDefaults(model)
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
