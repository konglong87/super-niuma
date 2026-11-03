package identity

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/product"
)

func TestFromSettingsAppliesConfiguredNames(t *testing.T) {
	t.Setenv(product.ProductNameEnv, "")
	t.Setenv(product.ProductKeyEnv, "")
	t.Setenv(product.ConfigDirNameEnv, "")
	t.Setenv(product.GuidanceFilenameEnv, "")
	id := FromSettings(Settings{
		ProductName:      "agentx",
		ProductKey:       "agentx",
		ConfigDirName:    ".agentx",
		GuidanceFilename: "agentx.md",
	})
	if id.ProductName != "agentx" || id.ProductKey != "agentx" || id.ConfigDirName != ".agentx" || id.GuidanceFilename != "agentx.md" {
		t.Fatalf("identity = %+v", id)
	}
	if id.LegacyGuidanceFile != "CLAUDE.md" || id.WorkflowFallbackFile != "AGENTS.md" {
		t.Fatalf("legacy compatibility names changed: %+v", id)
	}
}

func TestFromSettingsUsesCanonicalEnvironmentOverrides(t *testing.T) {
	t.Setenv(product.ProductNameEnv, "env-product")
	t.Setenv(product.ProductKeyEnv, "env-key")
	t.Setenv(product.ConfigDirNameEnv, ".env-product")
	t.Setenv(product.GuidanceFilenameEnv, "env-product.md")

	id := FromSettings(Settings{
		ProductName:      "settings-product",
		ProductKey:       "settings-key",
		ConfigDirName:    ".settings-product",
		GuidanceFilename: "settings-product.md",
	})
	if id.ProductName != "env-product" || id.ProductKey != "env-key" ||
		id.ConfigDirName != ".env-product" || id.GuidanceFilename != "env-product.md" {
		t.Fatalf("identity did not use canonical environment overrides: %+v", id)
	}
}

func TestFromSettingsFallsBackOnUnsafeNames(t *testing.T) {
	t.Setenv(product.ProductNameEnv, "")
	t.Setenv(product.ProductKeyEnv, "")
	t.Setenv(product.ConfigDirNameEnv, "")
	t.Setenv(product.GuidanceFilenameEnv, "")
	id := FromSettings(Settings{GuidanceFilename: "../bad.md"})
	if id.GuidanceFilename != "niuma.md" || id.ConfigDirName != ".niuma" {
		t.Fatalf("identity did not fall back to defaults: %+v", id)
	}
}
