package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const credentialJSON = `{"provider":"custom","baseURL":"https://example.test/v1","apiKey":"sk-test-not-a-real-key"}`

func mustWriteMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile applies umask, so force the exact mode we are testing.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialPermissionWarningsFlagsWorldReadableLocalConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")

	local := filepath.Join(project, "config", "settings.json")
	mustWriteMode(t, local, credentialJSON, 0644)

	warnings := CredentialPermissionWarnings(project)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one for %s", warnings, local)
	}
	if warnings[0].Path != local {
		t.Fatalf("warning path = %q, want %q", warnings[0].Path, local)
	}
	if warnings[0].Mode.Perm() != 0644 {
		t.Fatalf("warning mode = %04o, want 0644", warnings[0].Mode.Perm())
	}
	message := warnings[0].String()
	for _, want := range []string{local, "chmod 600"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q does not mention %q", message, want)
		}
	}
	// The warning must never echo the secret it is warning about.
	if strings.Contains(message, "sk-test-not-a-real-key") {
		t.Fatalf("message leaks the credential: %q", message)
	}
}

func TestCredentialPermissionWarningsIgnoresOwnerOnlyFile(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")

	mustWriteMode(t, filepath.Join(project, "config", "settings.json"), credentialJSON, 0600)

	if warnings := CredentialPermissionWarnings(project); len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none for a 0600 file", warnings)
	}
}

func TestCredentialPermissionWarningsIgnoresCredentialFreeFile(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")

	// A world-readable file with no credentials in it is fine: config/config.yaml
	// is committed on purpose and must not produce noise.
	mustWriteMode(t, filepath.Join(project, "config", "settings.json"), `{"model":"glm-5.1"}`, 0644)

	if warnings := CredentialPermissionWarnings(project); len(warnings) != 0 {
		t.Fatalf("warnings = %+v, want none for a credential-free file", warnings)
	}
}

func TestCredentialPermissionWarningsDetectsProviderAPIKey(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")

	mustWriteMode(t, filepath.Join(project, "config", "settings.json"), `{"fallback":{"enabled":true,"providers":[{"name":"gateway","type":"openai","baseURL":"https://example.invalid","apiKey":"sk-provider-not-a-real-key"}]}}`, 0644)

	if warnings := CredentialPermissionWarnings(project); len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one for a provider apiKey", warnings)
	}
}

// LoadForCWD must carry the warnings, otherwise nothing downstream can surface
// them and this check silently disappears in a later refactor.
func TestLoadForCWDReportsCredentialPermissionWarnings(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	local := filepath.Join(project, "config", "settings.json")
	mustWriteMode(t, local, credentialJSON, 0644)

	cfg := LoadForCWD(project)
	if len(cfg.Warnings) == 0 {
		t.Fatal("cfg.Warnings is empty, want the insecure-permission warning")
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), local) {
		t.Fatalf("cfg.Warnings = %+v, want a mention of %s", cfg.Warnings, local)
	}
	// Loading must still succeed: refusing would break existing setups.
	if cfg.APIKey != "sk-test-not-a-real-key" {
		t.Fatalf("cfg.APIKey = %q, want the loader to still apply the file", cfg.APIKey)
	}
}
