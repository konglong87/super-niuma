package main

import (
	"context"
	"errors"
	"strings"
)

// ComputerPermissionTarget identifies one of the macOS privacy permissions
// required by Computer Use. Keep this enum closed so the bridge cannot be used
// to open arbitrary URLs or settings panes.
type ComputerPermissionTarget string

const (
	ComputerPermissionAccessibility ComputerPermissionTarget = "accessibility"
	ComputerPermissionScreenCapture ComputerPermissionTarget = "screen_capture"
)

var (
	errUnknownComputerPermissionTarget       = errors.New("unsupported computer permission target")
	errComputerPermissionSettingsUnsupported = errors.New("computer permission settings navigation is only supported on macOS")
)

func parseComputerPermissionTarget(raw string) (ComputerPermissionTarget, error) {
	target := ComputerPermissionTarget(strings.TrimSpace(raw))
	switch target {
	case ComputerPermissionAccessibility, ComputerPermissionScreenCapture:
		return target, nil
	default:
		return "", errUnknownComputerPermissionTarget
	}
}

// OpenComputerPermissionSettings opens the native settings pane for a
// supported Computer Use permission. The platform-specific implementation
// owns the actual launch mechanism; this method only exposes the allowlisted
// Wails bridge contract.
func (a *app) OpenComputerPermissionSettings(rawTarget string) error {
	target, err := parseComputerPermissionTarget(rawTarget)
	if err != nil {
		return err
	}
	return openComputerPermissionSettings(a.windowContext(), target)
}

func computerPermissionContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
