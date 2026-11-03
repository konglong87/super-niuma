//go:build darwin

package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestComputerPermissionSettingsURLAllowlist(t *testing.T) {
	tests := []struct {
		target ComputerPermissionTarget
		want   string
	}{
		{target: ComputerPermissionAccessibility, want: computerPermissionAccessibilitySettingsURL},
		{target: ComputerPermissionScreenCapture, want: computerPermissionScreenCaptureSettingsURL},
	}

	for _, tt := range tests {
		got, err := computerPermissionSettingsURL(tt.target)
		if err != nil {
			t.Fatalf("computerPermissionSettingsURL(%q) error = %v", tt.target, err)
		}
		if got != tt.want {
			t.Fatalf("computerPermissionSettingsURL(%q) = %q, want %q", tt.target, got, tt.want)
		}
	}

	if _, err := computerPermissionSettingsURL(ComputerPermissionTarget("arbitrary")); err == nil {
		t.Fatal("computerPermissionSettingsURL accepted an unknown target")
	}
}

func TestOpenComputerPermissionSettingsUsesAllowlistedOpenCommand(t *testing.T) {
	var gotCommand string
	var gotArgs []string
	run := func(_ context.Context, command string, args ...string) error {
		gotCommand = command
		gotArgs = append([]string(nil), args...)
		return nil
	}

	if err := openComputerPermissionSettingsWithRunner(nil, ComputerPermissionAccessibility, run); err != nil {
		t.Fatalf("openComputerPermissionSettingsWithRunner() error = %v", err)
	}
	if gotCommand != computerPermissionSettingsOpenCommand {
		t.Fatalf("command = %q, want %q", gotCommand, computerPermissionSettingsOpenCommand)
	}
	wantArgs := []string{computerPermissionAccessibilitySettingsURL}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestOpenComputerPermissionSettingsWrapsLaunchError(t *testing.T) {
	wantErr := errors.New("open failed")
	err := openComputerPermissionSettingsWithRunner(context.Background(), ComputerPermissionScreenCapture, func(context.Context, string, ...string) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}
