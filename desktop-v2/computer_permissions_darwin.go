//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

const computerPermissionSettingsOpenCommand = "open"

const (
	computerPermissionAccessibilitySettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	computerPermissionScreenCaptureSettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
)

type computerPermissionSettingsCommandRunner func(context.Context, string, ...string) error

func computerPermissionSettingsURL(target ComputerPermissionTarget) (string, error) {
	switch target {
	case ComputerPermissionAccessibility:
		return computerPermissionAccessibilitySettingsURL, nil
	case ComputerPermissionScreenCapture:
		return computerPermissionScreenCaptureSettingsURL, nil
	default:
		return "", errUnknownComputerPermissionTarget
	}
}

func runComputerPermissionSettingsCommand(ctx context.Context, command string, args ...string) error {
	return exec.CommandContext(computerPermissionContext(ctx), command, args...).Run()
}

func openComputerPermissionSettings(ctx context.Context, target ComputerPermissionTarget) error {
	return openComputerPermissionSettingsWithRunner(ctx, target, runComputerPermissionSettingsCommand)
}

func openComputerPermissionSettingsWithRunner(ctx context.Context, target ComputerPermissionTarget, run computerPermissionSettingsCommandRunner) error {
	settingsURL, err := computerPermissionSettingsURL(target)
	if err != nil {
		return err
	}
	if run == nil {
		return errors.New("computer permission settings command runner is nil")
	}
	if err := run(computerPermissionContext(ctx), computerPermissionSettingsOpenCommand, settingsURL); err != nil {
		return fmt.Errorf("open computer permission settings: %w", err)
	}
	return nil
}
