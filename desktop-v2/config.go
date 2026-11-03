package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	desktopConfigFileName    = "config-v2.json"
	desktopDataDirName       = ".niuma"
	desktopConfigDirEnv      = "NIUMA_DESKTOP_CONFIG_DIR"
	desktopSessionBackendEnv = "NIUMA_DESKTOP_SESSION_BACKEND"
	defaultSessionBackend    = "jsonl"
	defaultWorkspaceRoot     = "niuma-workspace"
	defaultWorkspaceName     = "niuma"
)

const (
	defaultWindowWidth  = 1440
	defaultWindowHeight = 900
	minWindowWidth      = 1024
	minWindowHeight     = 700
)

type DesktopWindowGeometry struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (g DesktopWindowGeometry) valid() bool {
	return g.Width >= minWindowWidth && g.Height >= minWindowHeight
}

type windowState struct {
	Geometry   DesktopWindowGeometry `json:"geometry,omitempty"`
	Maximized  bool                  `json:"maximized,omitempty"`
	Fullscreen bool                  `json:"fullscreen,omitempty"`
}

func (s windowState) normalized() windowState {
	if !s.Geometry.valid() {
		s.Geometry = DesktopWindowGeometry{}
	}
	if s.Fullscreen {
		s.Maximized = false
	}
	return s
}

type desktopConfig struct {
	Workspace      string                  `json:"workspace,omitempty"`
	SessionBackend string                  `json:"session_backend,omitempty"`
	Appearance     desktopAppearanceConfig `json:"appearance,omitempty"`
	Window         windowState             `json:"window,omitempty"`
}

func (c desktopConfig) normalized() desktopConfig {
	c.Appearance = c.Appearance.normalized()
	c.Window = c.Window.normalized()
	return c
}

func desktopDataDir() (string, error) {
	if root := strings.TrimSpace(os.Getenv(desktopConfigDirEnv)); root != "" {
		return filepath.Join(root, "niuma"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, desktopDataDirName), nil
}

func desktopConfigPath() (string, error) {
	dir, err := desktopDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, desktopConfigFileName), nil
}

func loadDesktopConfig() (desktopConfig, error) {
	path, err := desktopConfigPath()
	if err != nil {
		return desktopConfig{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return desktopConfig{}, nil
	}
	if err != nil {
		return desktopConfig{}, err
	}
	var config desktopConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return desktopConfig{}, err
	}
	config.Workspace = strings.TrimSpace(config.Workspace)
	config.SessionBackend = strings.ToLower(strings.TrimSpace(config.SessionBackend))
	if config.SessionBackend != "" && config.SessionBackend != "jsonl" && config.SessionBackend != "sqlite" {
		config.SessionBackend = ""
	}
	if config.Workspace != "" {
		info, statErr := os.Stat(config.Workspace)
		if statErr != nil || !info.IsDir() {
			config.Workspace = ""
		}
	}
	return config.normalized(), nil
}

func saveDesktopConfig(config desktopConfig) error {
	path, err := desktopConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config.normalized(), "", "  ")
	if err != nil {
		return err
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func chooseWorkspace(ctx context.Context) (string, error) {
	workspace, err := wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: "选择 niuma 工作目录",
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace selection cancelled")
	}
	return normalizeWorkspaceSelection(workspace)
}

func defaultWorkspacePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultWorkspaceRoot, defaultWorkspaceName), nil
}

func ensureDefaultWorkspace() (string, error) {
	workspace, err := defaultWorkspacePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return "", fmt.Errorf("create default workspace: %w", err)
	}
	return workspace, nil
}

func selectWorkspace(ctx context.Context) (string, error) {
	workspace, err := wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: "选择会话工作文件夹",
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(workspace) == "" {
		return "", nil
	}
	return normalizeWorkspaceSelection(workspace)
}

func normalizeWorkspaceSelection(workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", errors.New("workspace selection cancelled")
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("selected workspace is not a directory")
	}
	return filepath.Clean(workspace), nil
}
