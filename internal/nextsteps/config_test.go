package nextsteps

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

// 未配置即开启：默认开是产品决策，用 *bool 的 nil 表达。
func TestConfigFromSettingsDefaultsToEnabled(t *testing.T) {
	cfg := ConfigFromSettings(config.Settings{}, "claude-sonnet-4-6")
	if !cfg.Enabled {
		t.Error("Enabled = false, want true when nextSteps is unconfigured")
	}
	if cfg.Count != DefaultCount {
		t.Errorf("Count = %d, want %d", cfg.Count, DefaultCount)
	}
}

func TestConfigFromSettingsHonorsExplicitDisable(t *testing.T) {
	disabled := false
	cfg := ConfigFromSettings(config.Settings{
		NextSteps: &config.NextStepsSettings{Enabled: &disabled},
	}, "claude-sonnet-4-6")
	if cfg.Enabled {
		t.Error("Enabled = true, want false when explicitly disabled")
	}
}

func TestConfigClampsCountToRange(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, DefaultCount},
		{-3, DefaultCount},
		{1, 1},
		{MaxCount, MaxCount},
		{MaxCount + 7, MaxCount},
	}
	for _, tc := range cases {
		got := Config{Count: tc.in}.WithDefaults("claude-sonnet-4-6", nil).Count
		if got != tc.want {
			t.Errorf("Count %d -> %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestWithDefaultsInheritsParentWithoutConfiguredTier(t *testing.T) {
	got := Config{}.WithDefaults("claude-sonnet-4-6", nil).Model
	if got != "claude-sonnet-4-6" {
		t.Errorf("Model = %q, want the parent model", got)
	}
}

// 非 Anthropic 主模型 + 配了 tier：用配置值，不能甩 Anthropic id 过去。
func TestWithDefaultsUsesConfiguredTierForNonAnthropicParent(t *testing.T) {
	got := Config{}.WithDefaults("glm-4-plus", map[string]string{"haiku": "glm-4-flash"}).Model
	if got != "glm-4-flash" {
		t.Errorf("Model = %q, want glm-4-flash", got)
	}
}

// 非 Anthropic 主模型 + 没配 tier：继承主模型，而不是跳到 provider 不认识的 id。
func TestWithDefaultsInheritsNonAnthropicParentWithoutTier(t *testing.T) {
	got := Config{}.WithDefaults("glm-4-plus", nil).Model
	if got != "glm-4-plus" {
		t.Errorf("Model = %q, want glm-4-plus", got)
	}
}

// 主模型本来就在便宜档，无需再切。
func TestWithDefaultsKeepsParentAlreadyOnCheapTier(t *testing.T) {
	got := Config{}.WithDefaults("claude-haiku-4-5", nil).Model
	if got != "claude-haiku-4-5" {
		t.Errorf("Model = %q, want claude-haiku-4-5", got)
	}
}

// 显式配置优先于一切推断。
func TestWithDefaultsPrefersExplicitModel(t *testing.T) {
	got := Config{Model: "my-tiny-model"}.WithDefaults("claude-sonnet-4-6", map[string]string{"haiku": "glm-4-flash"}).Model
	if got != "my-tiny-model" {
		t.Errorf("Model = %q, want my-tiny-model", got)
	}
}

// settings 里的 subagentModelTiers 要被 ConfigFromSettings 带进解析。
func TestConfigFromSettingsFeedsTierMapIntoResolution(t *testing.T) {
	cfg := ConfigFromSettings(config.Settings{
		SubagentModelTiers: map[string]string{"haiku": "glm-4-flash"},
	}, "glm-4-plus")
	if cfg.Model != "glm-4-flash" {
		t.Errorf("Model = %q, want glm-4-flash", cfg.Model)
	}
}
