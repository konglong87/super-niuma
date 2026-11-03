//go:build !darwin

package main

import (
	"context"
	"errors"
	"testing"
)

func TestOpenComputerPermissionSettingsReportsUnsupportedPlatform(t *testing.T) {
	if err := openComputerPermissionSettings(context.Background(), ComputerPermissionAccessibility); !errors.Is(err, errComputerPermissionSettingsUnsupported) {
		t.Fatalf("openComputerPermissionSettings() error = %v, want %v", err, errComputerPermissionSettingsUnsupported)
	}
}
