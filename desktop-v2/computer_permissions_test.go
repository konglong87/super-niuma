package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseComputerPermissionTargetAllowlist(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  ComputerPermissionTarget
		valid bool
	}{
		{name: "accessibility", raw: "accessibility", want: ComputerPermissionAccessibility, valid: true},
		{name: "screen capture", raw: " screen_capture ", want: ComputerPermissionScreenCapture, valid: true},
		{name: "unknown", raw: "privacy_all", valid: false},
		{name: "empty", raw: "", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseComputerPermissionTarget(tt.raw)
			if tt.valid {
				if err != nil {
					t.Fatalf("parseComputerPermissionTarget(%q) error = %v", tt.raw, err)
				}
				if got != tt.want {
					t.Fatalf("parseComputerPermissionTarget(%q) = %q, want %q", tt.raw, got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseComputerPermissionTarget(%q) error = nil", tt.raw)
			}
		})
	}
}

func TestComputerPermissionBridgeContract(t *testing.T) {
	method, ok := reflect.TypeOf((*app)(nil)).MethodByName("OpenComputerPermissionSettings")
	if !ok {
		t.Fatal("OpenComputerPermissionSettings bridge method is missing")
	}
	if method.Type.NumIn() != 2 || method.Type.In(1).Kind() != reflect.String || method.Type.NumOut() != 1 || method.Type.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatalf("OpenComputerPermissionSettings has unexpected signature %s", method.Type)
	}
}

func TestOpenComputerPermissionSettingsRejectsUnknownTargetBeforePlatformLaunch(t *testing.T) {
	var application app
	err := application.OpenComputerPermissionSettings("arbitrary-url")
	if !errors.Is(err, errUnknownComputerPermissionTarget) {
		t.Fatalf("OpenComputerPermissionSettings() error = %v, want %v", err, errUnknownComputerPermissionTarget)
	}
}
