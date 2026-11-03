package super

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func unsetStateOverride(t *testing.T) {
	t.Helper()
	t.Setenv(StateDirectoryEnv, "")
	if err := os.Unsetenv(StateDirectoryEnv); err != nil {
		t.Fatal(err)
	}
}

func TestStateDirectoryDefaultIsLazyAndIsolated(t *testing.T) {
	unsetStateOverride(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got, err := StateDirectory()
	want := filepath.Join(home, StateDirectoryName)
	if err != nil || got != want {
		t.Fatalf("state = %q, %v; want %q", got, err, want)
	}
	if _, err := os.Stat(want); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolver created state: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolver touched legacy state: %v, %v", entries, err)
	}
}

func TestStateDirectoryExplicitOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "isolated")
	t.Setenv(StateDirectoryEnv, want+string(filepath.Separator)+".")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	got, err := StateDirectory()
	if err != nil || got != want {
		t.Fatalf("override = %q, %v", got, err)
	}
	if _, err := os.Stat(want); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolver created override: %v", err)
	}
}

func TestStateDirectoryRejectsInvalidOverrideAndHome(t *testing.T) {
	for _, value := range []string{"", "relative", "~/state"} {
		t.Run("override="+value, func(t *testing.T) {
			t.Setenv(StateDirectoryEnv, value)
			if got, err := StateDirectory(); err == nil || got != "" {
				t.Fatalf("invalid override = %q, %v", got, err)
			}
		})
	}
	for _, value := range []string{"", "relative"} {
		t.Run("home="+value, func(t *testing.T) {
			unsetStateOverride(t)
			t.Setenv("HOME", value)
			t.Setenv("USERPROFILE", value)
			if got, err := StateDirectory(); err == nil || got != "" {
				t.Fatalf("invalid home = %q, %v", got, err)
			}
		})
	}
}
