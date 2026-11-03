package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPNGDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestDesktopBackgroundPersistsAndRestoresByReference(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv(desktopConfigDirEnv, configRoot)
	application := &app{config: desktopConfig{Workspace: t.TempDir()}}

	saved, err := application.SaveBackgroundImage(testPNGDataURL, "sunset.png")
	if err != nil {
		t.Fatalf("SaveBackgroundImage() error = %v", err)
	}
	if saved.Mode != desktopBackgroundModeLocal || !saved.Enabled || saved.Name != "sunset.png" {
		t.Fatalf("saved background = %#v", saved)
	}

	dataDir, err := desktopDataDir()
	if err != nil {
		t.Fatalf("desktopDataDir() error = %v", err)
	}
	configPath := filepath.Join(dataDir, desktopConfigFileName)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read desktop config: %v", err)
	}
	var persisted desktopConfig
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode desktop config: %v", err)
	}
	if persisted.Appearance.BackgroundMode != desktopBackgroundModeLocal {
		t.Fatalf("background mode = %q", persisted.Appearance.BackgroundMode)
	}
	if strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})) {
		t.Fatal("desktop config unexpectedly contains image bytes")
	}
	if !strings.HasPrefix(persisted.Appearance.BackgroundFile, desktopBackgroundDirectory+string(filepath.Separator)) {
		t.Fatalf("background file = %q", persisted.Appearance.BackgroundFile)
	}

	reloaded, err := loadDesktopConfig()
	if err != nil {
		t.Fatalf("loadDesktopConfig() error = %v", err)
	}
	restored := &app{config: reloaded}
	got, err := restored.GetBackgroundImage()
	if err != nil {
		t.Fatalf("GetBackgroundImage() error = %v", err)
	}
	if got.Mode != desktopBackgroundModeLocal || got.DataURL != testPNGDataURL {
		t.Fatalf("restored background = %#v", got)
	}
}

func TestDesktopBackgroundRejectsUnsupportedOrInvalidData(t *testing.T) {
	application := &app{config: desktopConfig{}}
	for _, dataURL := range []string{
		"data:image/svg+xml;base64,PHN2Zy8+",
		"data:image/png;base64,not-base64",
		"file:///Users/test/background.png",
	} {
		if _, err := application.SaveBackgroundImage(dataURL, "background.png"); err == nil {
			t.Fatalf("SaveBackgroundImage(%q) unexpectedly succeeded", dataURL)
		}
	}
}

func TestDesktopBackgroundClearRemovesManagedFile(t *testing.T) {
	t.Setenv(desktopConfigDirEnv, t.TempDir())
	application := &app{config: desktopConfig{}}
	saved, err := application.SaveBackgroundImage(testPNGDataURL, "background.png")
	if err != nil {
		t.Fatalf("SaveBackgroundImage() error = %v", err)
	}
	relative := application.config.Appearance.BackgroundFile
	absolute, err := application.backgroundAssetPath(relative)
	if err != nil {
		t.Fatalf("backgroundAssetPath() error = %v", err)
	}
	if _, err := os.Stat(absolute); err != nil {
		t.Fatalf("stored background missing: %v", err)
	}
	if err := application.ClearBackgroundImage(); err != nil {
		t.Fatalf("ClearBackgroundImage() error = %v", err)
	}
	if application.config.Appearance.BackgroundMode != desktopBackgroundModeExternal {
		t.Fatalf("mode after clear = %q", application.config.Appearance.BackgroundMode)
	}
	if _, err := os.Stat(absolute); !os.IsNotExist(err) {
		t.Fatalf("stored background still exists, err=%v", err)
	}
	if saved.DataURL == "" {
		t.Fatal("saved background did not return a preview")
	}
}
