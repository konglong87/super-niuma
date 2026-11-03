package nextsteps

import "github.com/konglong87/super-niuma/internal/config"

// ConfigFromSettings 读取 settings.nextSteps。未配置即开启：Enabled 是 *bool，
// nil 表示「没配」而非「显式关」。
func ConfigFromSettings(settings config.Settings, model string) Config {
	cfg := Config{Enabled: true}
	if settings.NextSteps != nil {
		if settings.NextSteps.Enabled != nil {
			cfg.Enabled = *settings.NextSteps.Enabled
		}
		cfg.Model = settings.NextSteps.Model
		if settings.NextSteps.Count != nil {
			cfg.Count = *settings.NextSteps.Count
		}
	}
	return cfg.WithDefaults(model, settings.SubagentModelTiers)
}
