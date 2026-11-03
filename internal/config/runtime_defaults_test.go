package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRuntimeDefaultsUsesConfiguredPrimaryRoute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	settingsDir := filepath.Join(home, ".niuma")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"provider":"custom","model":"ABC","baseURL":"https://provider.example/v1","apiKey":"test-key"}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ResolveRuntimeDefaults("")
	if !got.Configured || got.NeedsSetup {
		t.Fatalf("runtime defaults readiness = %#v", got)
	}
	if got.Provider != "custom" || got.Model != "ABC" {
		t.Fatalf("runtime defaults route = %#v", got)
	}
	if got.Source != "settings" {
		t.Fatalf("runtime defaults source = %q, want settings", got.Source)
	}
}

func TestResolveRuntimeDefaultsRequiresModelAndValidRoute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	settingsDir := filepath.Join(home, ".niuma")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"provider":"custom"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ResolveRuntimeDefaults("")
	if got.Configured || !got.NeedsSetup {
		t.Fatalf("unconfigured runtime defaults = %#v", got)
	}
}
