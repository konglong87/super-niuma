package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopSessionBackendDefaultsToJSONL(t *testing.T) {
	t.Setenv("NIUMA_DESKTOP_SESSION_BACKEND", "")
	if got := desktopSessionBackend(desktopConfig{}); got != "jsonl" {
		t.Fatalf("desktopSessionBackend(empty) = %q, want jsonl", got)
	}
}

func TestDesktopSessionBackendUsesConfigAndEnvironmentOverride(t *testing.T) {
	t.Setenv("NIUMA_DESKTOP_SESSION_BACKEND", "")
	if got := desktopSessionBackend(desktopConfig{SessionBackend: "sqlite"}); got != "sqlite" {
		t.Fatalf("desktopSessionBackend(config) = %q, want sqlite", got)
	}
	t.Setenv("NIUMA_DESKTOP_SESSION_BACKEND", "jsonl")
	if got := desktopSessionBackend(desktopConfig{SessionBackend: "sqlite"}); got != "jsonl" {
		t.Fatalf("desktopSessionBackend(env) = %q, want jsonl", got)
	}
}

func TestDesktopSessionBackendUsesNamedConstantDefault(t *testing.T) {
	t.Setenv(desktopSessionBackendEnv, "")
	if got := desktopSessionBackend(desktopConfig{}); got != defaultSessionBackend {
		t.Fatalf("desktopSessionBackend(empty) = %q, want %q", got, defaultSessionBackend)
	}
}

func TestDesktopConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIUMA_DESKTOP_CONFIG_DIR", root)
	workspace := filepath.Join(root, "project")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveDesktopConfig(desktopConfig{Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	got, err := loadDesktopConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != workspace {
		t.Fatalf("workspace = %q, want %q", got.Workspace, workspace)
	}
	info, err := os.Stat(filepath.Join(root, "niuma", desktopConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadDesktopConfigClearsMissingWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIUMA_DESKTOP_CONFIG_DIR", root)
	if err := saveDesktopConfig(desktopConfig{Workspace: filepath.Join(root, "missing")}); err != nil {
		t.Fatal(err)
	}
	got, err := loadDesktopConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != "" {
		t.Fatalf("workspace = %q, want empty", got.Workspace)
	}
}

func TestDesktopDataDirDefaultsToNiumaOwnedHomeDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("NIUMA_DESKTOP_CONFIG_DIR", "")
	got, err := desktopDataDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".niuma")
	if got != want {
		t.Fatalf("desktop data dir = %q, want %q", got, want)
	}
}

func TestDesktopConfigRoundTripPreservesWindowState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIUMA_DESKTOP_CONFIG_DIR", root)
	workspace := filepath.Join(root, "project")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	want := desktopConfig{
		Workspace: workspace,
		Window: windowState{
			Geometry:  DesktopWindowGeometry{X: 120, Y: 80, Width: 1280, Height: 760},
			Maximized: true,
		},
	}.normalized()
	if err := saveDesktopConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadDesktopConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("config = %#v, want %#v", got, want)
	}
}

func TestLoadDesktopConfigDropsInvalidWindowGeometry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIUMA_DESKTOP_CONFIG_DIR", root)
	if err := saveDesktopConfig(desktopConfig{
		Window: windowState{
			Geometry:   DesktopWindowGeometry{X: 10, Y: 20, Width: 640, Height: 480},
			Fullscreen: true,
			Maximized:  true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := loadDesktopConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Window.Geometry != (DesktopWindowGeometry{}) {
		t.Fatalf("geometry = %#v, want zero geometry", got.Window.Geometry)
	}
	if !got.Window.Fullscreen || got.Window.Maximized {
		t.Fatalf("window state = %#v, want fullscreen only", got.Window)
	}
}

func TestNormalizeWorkspaceSelectionRequiresExistingDirectory(t *testing.T) {
	root := t.TempDir()
	got, err := normalizeWorkspaceSelection(filepath.Join(root, ".", "selected"))
	if err == nil {
		t.Fatal("normalizeWorkspaceSelection accepted a missing directory")
	}
	if got != "" {
		t.Fatalf("missing directory result = %q, want empty", got)
	}

	workspace := filepath.Join(root, "selected")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = normalizeWorkspaceSelection(filepath.Join(workspace, "."))
	if err != nil {
		t.Fatalf("normalizeWorkspaceSelection() error = %v", err)
	}
	if got != workspace {
		t.Fatalf("normalized workspace = %q, want %q", got, workspace)
	}
}

func TestEnsureDefaultWorkspaceUsesProductDirectoryAndReusesIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("NIUMA_DESKTOP_CONFIG_DIR", "")

	first, err := ensureDefaultWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, defaultWorkspaceRoot, defaultWorkspaceName)
	if first != want {
		t.Fatalf("default workspace = %q, want %q", first, want)
	}
	if info, err := os.Stat(first); err != nil || !info.IsDir() {
		t.Fatalf("default workspace is not a directory: %v", err)
	}
	second, err := ensureDefaultWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("reused workspace = %q, want %q", second, first)
	}
}
