package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionFileRequiresPrivateDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), filepath.Join(dir, "fixture.json")); err == nil {
		t.Fatal("accepted public directory")
	}
	if err := run(context.Background(), "relative.json"); err == nil {
		t.Fatal("accepted relative path")
	}
}
func TestCancelledFixtureCleansCapability(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(dir, "fixture.json")
	if err := run(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("capability leaked %v", err)
	}
}
func TestFixtureDoesNotOverwriteExistingConnection(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "fixture.json")
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), path); err == nil {
		t.Fatal("overwrote file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "existing" {
		t.Fatal("changed existing file")
	}
}
