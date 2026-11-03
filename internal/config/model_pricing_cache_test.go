package config

import (
	"os"
	"path/filepath"
	"testing"
)

// AUDIT-P1-36: session.Rate has cache-price fields but nothing could ever set
// them, because ModelPrice only carried input/output. Operators on a provider
// whose cache discount differs from Anthropic's had no way to say so.
func TestModelPriceCarriesCacheRatesFromJSONSettings(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)

	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "modelPricing": {"deepseek-v4-flash": {
	    "input": 0.14, "output": 0.28,
	    "cacheRead": 0.0028, "cacheWrite5m": 0.14, "cacheWrite1h": 0.2
	  }}
	}`)

	price := ModelPricing(project)["deepseek-v4-flash"]
	if price.CacheRead != 0.0028 {
		t.Fatalf("cacheRead = %v, want 0.0028", price.CacheRead)
	}
	if price.CacheWrite5m != 0.14 {
		t.Fatalf("cacheWrite5m = %v, want 0.14", price.CacheWrite5m)
	}
	if price.CacheWrite1h != 0.2 {
		t.Fatalf("cacheWrite1h = %v, want 0.2", price.CacheWrite1h)
	}
}

func TestExplicitYAMLModelPriceCarriesCacheRates(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")

	if err := os.MkdirAll(filepath.Join(project, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(project, "config", "config.yaml"),
		"modelPricing:\n  glm-5.1:\n    input: 10\n    output: 20\n    cacheRead: 2\n")

	if got := LoadSettingsFile(filepath.Join(project, "config", "config.yaml")).ModelPricing["glm-5.1"].CacheRead; got != 2 {
		t.Fatalf("cacheRead = %v, want 2 (yaml key cacheRead)", got)
	}
}

// Omitting the cache keys must keep meaning "derive it", not "it is free".
func TestModelPriceWithoutCacheRatesStaysZero(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)

	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "modelPricing": {"glm-5.1": {"input": 10, "output": 20}}
	}`)

	price := ModelPricing(project)["glm-5.1"]
	if price.CacheRead != 0 || price.CacheWrite5m != 0 || price.CacheWrite1h != 0 {
		t.Fatalf("unset cache rates = %+v, want all zero", price)
	}
}

// Cache-price defaults are grouped by provider kind, so the pricing layer needs
// to know which provider serves a model.
func TestProviderKindForModelResolvesPrimaryAndFallbacks(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".claude"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	isolateAmbientProviderEnv(t)

	mustWrite(t, filepath.Join(project, ".claude", "settings.json"), `{
	  "model": "glm-5.1",
	  "provider": "custom",
	  "fallback": {"enabled": true, "providers": [
	    {"name": "ds", "type": "openai-compatible", "model": "deepseek-v4-pro"},
	    {"name": "plain", "model": "claude-haiku-4-5"}
	  ]}
	}`)

	for _, tc := range []struct {
		model     string
		wantKind  string
		wantFound bool
	}{
		{model: "glm-5.1", wantKind: "custom", wantFound: true},
		{model: "deepseek-v4-pro", wantKind: "openai-compatible", wantFound: true},
		// A provider entry with no explicit type is Anthropic, matching the
		// client's own default.
		{model: "claude-haiku-4-5", wantKind: "anthropic", wantFound: true},
		{model: "some-model-nobody-serves", wantKind: "", wantFound: false},
		{model: "", wantKind: "", wantFound: false},
	} {
		kind, found := ProviderKindForModel(project, tc.model)
		if kind != tc.wantKind || found != tc.wantFound {
			t.Fatalf("ProviderKindForModel(%q) = (%q, %v), want (%q, %v)",
				tc.model, kind, found, tc.wantKind, tc.wantFound)
		}
	}
}

func TestProviderKindClassifiersCoverEverySupportedKind(t *testing.T) {
	for _, kind := range []string{"", "anthropic", "anthropic-compatible", "ANTHROPIC"} {
		if !ProviderKindAnthropic(kind) {
			t.Fatalf("ProviderKindAnthropic(%q) = false, want true", kind)
		}
		if ProviderKindOpenAI(kind) {
			t.Fatalf("ProviderKindOpenAI(%q) = true, want false", kind)
		}
	}
	for _, kind := range []string{"custom", "openai", "openai-compatible", "openai-chat-completions"} {
		if !ProviderKindOpenAI(kind) {
			t.Fatalf("ProviderKindOpenAI(%q) = false, want true", kind)
		}
		if ProviderKindAnthropic(kind) {
			t.Fatalf("ProviderKindAnthropic(%q) = true, want false", kind)
		}
	}
	for _, kind := range []string{"bedrock", "vertex", "nonsense"} {
		if ProviderKindAnthropic(kind) || ProviderKindOpenAI(kind) {
			t.Fatalf("kind %q must classify as neither Anthropic nor OpenAI", kind)
		}
	}
}
