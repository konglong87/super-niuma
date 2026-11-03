package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"
)

const (
	desktopBackgroundModeExternal = "external"
	desktopBackgroundModeLocal    = "local"
	desktopBackgroundDirectory    = "backgrounds"
	desktopBackgroundMaxBytes     = 20 * 1024 * 1024
	desktopBackgroundMaxDimension = 12_000
)

var supportedDesktopBackgroundTypes = map[string]string{
	"image/gif":  ".gif",
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type desktopAppearanceConfig struct {
	BackgroundMode string `json:"background_mode,omitempty"`
	BackgroundFile string `json:"background_file,omitempty"`
	BackgroundName string `json:"background_name,omitempty"`
	BackgroundMIME string `json:"background_mime,omitempty"`
}

func (c desktopAppearanceConfig) normalized() desktopAppearanceConfig {
	c.BackgroundMode = strings.ToLower(strings.TrimSpace(c.BackgroundMode))
	if c.BackgroundMode == "" {
		return c
	}
	if c.BackgroundMode != desktopBackgroundModeLocal {
		c.BackgroundMode = desktopBackgroundModeExternal
	}
	c.BackgroundFile = strings.TrimSpace(c.BackgroundFile)
	c.BackgroundName = strings.TrimSpace(c.BackgroundName)
	c.BackgroundMIME = strings.ToLower(strings.TrimSpace(c.BackgroundMIME))
	return c
}

type DesktopBackgroundImage struct {
	Mode    string `json:"mode"`
	Enabled bool   `json:"enabled"`
	Name    string `json:"name,omitempty"`
	MIME    string `json:"mime_type,omitempty"`
	DataURL string `json:"data_url,omitempty"`
}

func (a *app) GetBackgroundImage() (DesktopBackgroundImage, error) {
	a.mu.Lock()
	config := a.config.normalized()
	a.mu.Unlock()
	if config.Appearance.BackgroundMode != desktopBackgroundModeLocal || config.Appearance.BackgroundFile == "" {
		return DesktopBackgroundImage{Mode: desktopBackgroundModeExternal}, nil
	}

	path, err := a.backgroundAssetPath(config.Appearance.BackgroundFile)
	if err != nil {
		return DesktopBackgroundImage{Mode: desktopBackgroundModeExternal}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DesktopBackgroundImage{Mode: desktopBackgroundModeExternal}, nil
	}
	if err != nil {
		return DesktopBackgroundImage{}, fmt.Errorf("read desktop background: %w", err)
	}
	mimeType := config.Appearance.BackgroundMIME
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(path))
	}
	if _, ok := supportedDesktopBackgroundTypes[mimeType]; !ok {
		return DesktopBackgroundImage{Mode: desktopBackgroundModeExternal}, nil
	}
	return DesktopBackgroundImage{
		Mode:    desktopBackgroundModeLocal,
		Enabled: true,
		Name:    config.Appearance.BackgroundName,
		MIME:    mimeType,
		DataURL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
	}, nil
}

func (a *app) SaveBackgroundImage(dataURL, name string) (DesktopBackgroundImage, error) {
	mimeType, data, err := decodeDesktopBackgroundDataURL(dataURL)
	if err != nil {
		return DesktopBackgroundImage{}, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return DesktopBackgroundImage{}, fmt.Errorf("decode desktop background: %w", err)
	}
	if format == "" || config.Width <= 0 || config.Height <= 0 || config.Width > desktopBackgroundMaxDimension || config.Height > desktopBackgroundMaxDimension {
		return DesktopBackgroundImage{}, fmt.Errorf("desktop background dimensions exceed %dx%d", desktopBackgroundMaxDimension, desktopBackgroundMaxDimension)
	}

	hash := sha256.Sum256(data)
	extension := supportedDesktopBackgroundTypes[mimeType]
	relativePath := filepath.Join(desktopBackgroundDirectory, hex.EncodeToString(hash[:])+extension)
	absolutePath, err := a.backgroundAssetPath(relativePath)
	if err != nil {
		return DesktopBackgroundImage{}, err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return DesktopBackgroundImage{}, fmt.Errorf("create desktop background directory: %w", err)
	}
	if err := writePrivateFileIfMissing(absolutePath, data); err != nil {
		return DesktopBackgroundImage{}, fmt.Errorf("store desktop background: %w", err)
	}

	a.mu.Lock()
	previous := a.config.Appearance.normalized()
	next := a.config
	next.Appearance = desktopAppearanceConfig{
		BackgroundMode: desktopBackgroundModeLocal,
		BackgroundFile: relativePath,
		BackgroundName: filepath.Base(strings.TrimSpace(name)),
		BackgroundMIME: mimeType,
	}.normalized()
	a.mu.Unlock()
	if err := saveDesktopConfig(next); err != nil {
		return DesktopBackgroundImage{}, err
	}
	a.mu.Lock()
	a.config = next.normalized()
	a.mu.Unlock()
	removeManagedBackgroundIfUnreferenced(a, previous.BackgroundFile, relativePath)

	return DesktopBackgroundImage{
		Mode:    desktopBackgroundModeLocal,
		Enabled: true,
		Name:    next.Appearance.BackgroundName,
		MIME:    mimeType,
		DataURL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
	}, nil
}

func (a *app) SetBackgroundMode(mode string) (DesktopBackgroundImage, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != desktopBackgroundModeExternal && mode != desktopBackgroundModeLocal {
		return DesktopBackgroundImage{}, errors.New("unsupported desktop background mode")
	}
	a.mu.Lock()
	config := a.config
	config.Appearance = config.Appearance.normalized()
	if mode == desktopBackgroundModeLocal && config.Appearance.BackgroundFile == "" {
		a.mu.Unlock()
		return DesktopBackgroundImage{Mode: desktopBackgroundModeExternal}, nil
	}
	config.Appearance.BackgroundMode = mode
	a.mu.Unlock()
	if err := saveDesktopConfig(config); err != nil {
		return DesktopBackgroundImage{}, err
	}
	a.mu.Lock()
	a.config = config.normalized()
	a.mu.Unlock()
	if mode == desktopBackgroundModeLocal {
		return a.GetBackgroundImage()
	}
	return DesktopBackgroundImage{Mode: desktopBackgroundModeExternal}, nil
}

func (a *app) ClearBackgroundImage() error {
	a.mu.Lock()
	config := a.config
	previous := config.Appearance.normalized()
	config.Appearance = desktopAppearanceConfig{BackgroundMode: desktopBackgroundModeExternal}
	a.mu.Unlock()
	if err := saveDesktopConfig(config); err != nil {
		return err
	}
	a.mu.Lock()
	a.config = config.normalized()
	a.mu.Unlock()
	if previous.BackgroundFile != "" {
		return removeManagedBackground(a, previous.BackgroundFile)
	}
	return nil
}

func (a *app) backgroundAssetPath(relativePath string) (string, error) {
	root, err := desktopDataDir()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(relativePath)
	if clean == "." || filepath.IsAbs(clean) {
		return "", errors.New("invalid desktop background path")
	}
	absolute := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("desktop background path escapes data directory")
	}
	return absolute, nil
}

func decodeDesktopBackgroundDataURL(value string) (string, []byte, error) {
	parts := strings.SplitN(value, ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "data:") || !strings.Contains(strings.ToLower(parts[0]), ";base64") {
		return "", nil, errors.New("desktop background must be a base64 data URL")
	}
	header := strings.TrimPrefix(parts[0], "data:")
	mediaType, _, err := mime.ParseMediaType(strings.ReplaceAll(header, ";base64", ""))
	if err != nil {
		return "", nil, errors.New("desktop background has an invalid media type")
	}
	mediaType = strings.ToLower(mediaType)
	if _, ok := supportedDesktopBackgroundTypes[mediaType]; !ok {
		return "", nil, fmt.Errorf("unsupported desktop background type %s", mediaType)
	}
	data, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", nil, errors.New("desktop background data is not valid base64")
	}
	if len(data) == 0 || len(data) > desktopBackgroundMaxBytes {
		return "", nil, fmt.Errorf("desktop background must be between 1 byte and %d MB", desktopBackgroundMaxBytes/(1024*1024))
	}
	return mediaType, data, nil
}

func writePrivateFileIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func removeManagedBackgroundIfUnreferenced(a *app, previous, current string) {
	if previous == "" || previous == current {
		return
	}
	_ = removeManagedBackground(a, previous)
}

func removeManagedBackground(a *app, relativePath string) error {
	path, err := a.backgroundAssetPath(relativePath)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
