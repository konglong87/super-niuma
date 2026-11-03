package main

import (
	"path/filepath"
	"testing"
)

func TestLocalServiceControllerStartsStopped(t *testing.T) {
	controller := newLocalServiceController(localServiceConfig{
		port:  18087,
		token: "test-token",
	})

	status := controller.status()
	if status.State != string(localServiceStopped) {
		t.Fatalf("initial state = %q, want %q", status.State, localServiceStopped)
	}
	if status.Port != 18087 {
		t.Fatalf("initial port = %d, want 18087", status.Port)
	}
}

func TestLocalServiceControllerReportsStartFailure(t *testing.T) {
	dir := t.TempDir()
	controller := newLocalServiceController(localServiceConfig{
		executable: filepath.Join(dir, "missing-niuma"),
		workspace:  dir,
		port:       18087,
		token:      "test-token",
		logPath:    filepath.Join(dir, "server.log"),
	})

	if err := controller.start(); err == nil {
		t.Fatal("start() error = nil, want missing executable error")
	}
	status := controller.status()
	if status.State != string(localServiceFailed) {
		t.Fatalf("failed state = %q, want %q", status.State, localServiceFailed)
	}
	if status.Error == "" {
		t.Fatal("failed status error is empty")
	}
}
