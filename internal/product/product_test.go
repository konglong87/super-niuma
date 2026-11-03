package product

import (
	"strings"
	"testing"
)

func TestCanonicalProductIdentity(t *testing.T) {
	if DisplayName != "牛马 Niuma" || Name != "Niuma" || BinaryName != "niuma" || ProductKey != "niuma" {
		t.Fatalf("identity = %q/%q/%q/%q", DisplayName, Name, BinaryName, ProductKey)
	}
	if ConfigDirName != ".niuma" || GuidanceFilename != "niuma.md" || EnvPrefix != "NIUMA_" {
		t.Fatalf("identity = %q/%q/%q", ConfigDirName, GuidanceFilename, EnvPrefix)
	}
	if LegacyConfigDirName == ".golang-cc" || LegacyConfigDirName == ".go-claude" {
		t.Fatalf("legacy directory overlaps another product: %q", LegacyConfigDirName)
	}
}
func TestProductOwnedIdentifiersUseNiumaNamespace(t *testing.T) {
	for name, value := range map[string]string{"transcript v1": TranscriptSchemaV1, "transcript v2": TranscriptSchemaV2, "tenant quota redis": TenantQuotaRedisPrefix, "mobile usage redis": MobileUsageRedisPrefix} {
		if !strings.HasPrefix(value, "niuma") {
			t.Errorf("%s identifier = %q", name, value)
		}
	}
}
func TestGetenvReadsOnlyRequestedKey(t *testing.T) {
	t.Setenv("NIUMA_PROVIDER", "niuma-provider")
	t.Setenv("GOLANG_CC_PROVIDER", "foreign-provider")
	if got := Getenv("NIUMA_PROVIDER"); got != "niuma-provider" {
		t.Fatalf("Getenv()=%q", got)
	}
}
func TestLookupEnvPreservesExplicitEmptyValue(t *testing.T) {
	t.Setenv("NIUMA_PROVIDER", "")
	if got, ok := LookupEnv("NIUMA_PROVIDER"); !ok || got != "" {
		t.Fatalf("LookupEnv()=%q,%v", got, ok)
	}
	if _, ok := LookupEnv("NIUMA_UNSET"); ok {
		t.Fatal("unset key reported present")
	}
}
