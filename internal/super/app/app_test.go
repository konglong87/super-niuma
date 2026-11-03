package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type readyWriter struct{ ready chan string }

func (w readyWriter) Write(p []byte) (int, error) {
	select {
	case w.ready <- string(p):
	default:
	}
	return len(p), nil
}

func TestRunUsesIsolatedStateAndShutsDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".niuma", "session-sentinel")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("existing session untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(home, "isolated-super")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"--listen", "127.0.0.1:0", "--state-dir", stateDir}, readyWriter{ready}, io.Discard)
	}()
	var address string
	select {
	case text := <-ready:
		firstLine := strings.SplitN(text, "\n", 2)[0]
		address = strings.TrimPrefix(firstLine, "Super Niuma intake-only: ")
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup deadline")
	}
	request, err := http.NewRequest(http.MethodGet, address+"/api/super/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Super-Niuma-Request", "1")
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Mode             string `json:"mode"`
		ExecutionEnabled bool   `json:"execution_enabled"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&status)
	response.Body.Close()
	if decodeErr != nil || response.StatusCode != 200 || status.Mode != "intake_only" || status.ExecutionEnabled {
		t.Fatalf("status=%+v HTTP=%d err=%v", status, response.StatusCode, decodeErr)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("shutdown deadline")
	}
	content, err := os.ReadFile(legacy)
	if err != nil || string(content) != "existing session untouched" {
		t.Fatalf("legacy state changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, DatabaseName)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(legacy))
	if err != nil || len(entries) != 1 {
		t.Fatalf("Super touched legacy directory: entries=%d err=%v", len(entries), err)
	}
}
