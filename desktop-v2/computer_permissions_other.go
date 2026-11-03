//go:build !darwin

package main

import "context"

func openComputerPermissionSettings(context.Context, ComputerPermissionTarget) error {
	return errComputerPermissionSettingsUnsupported
}
