package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/control"
)

func TestConfigPrivateAtomicStorageAndRevocation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s := &configStore{path: filepath.Join(dir, ConfigFileName)}
	c, err := decodeConfig([]byte(exampleConfig()))
	if err != nil {
		t.Fatal(err)
	}
	c.Profiles[0].Grant.Workspace = dir
	c.Profiles[0].Enabled = true
	b, _ := json.Marshal(c)
	saved, err := s.save(string(b))
	if err != nil {
		t.Fatal(err)
	}
	p := control.Principal{Scope: "local", Owner: "local"}
	g := saved.Profiles[0].Grant
	if err = s.Current(context.Background(), p, g); err != nil {
		t.Fatal(err)
	}
	if err = s.Current(context.Background(), control.Principal{Scope: "local", Owner: "other"}, g); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("owner mismatch %v", err)
	}
	saved.Profiles[0].Enabled = false
	b, _ = json.Marshal(saved)
	if _, err = s.save(string(b)); err != nil {
		t.Fatal(err)
	}
	if err = s.Current(context.Background(), p, g); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("revocation ignored %v", err)
	}
	fi, err := os.Stat(s.path)
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("config mode %v %v", fi, err)
	}
	if err = os.Chmod(s.path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = s.load(); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("insecure credentials ref file accepted: %v", err)
	}
}
func TestConfigRejectsSecretsUnknownFieldsAndInvalidGraph(t *testing.T) {
	var m map[string]any
	_ = json.Unmarshal([]byte(exampleConfig()), &m)
	m["api_key"] = "must-not-store-raw-secret"
	b, _ := json.Marshal(m)
	if _, err := decodeConfig(b); err == nil {
		t.Fatal("raw secret option accepted")
	}
	delete(m, "api_key")
	b, _ = json.Marshal(m)
	if _, err := decodeConfig(append(b, []byte(" {}")...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	c, _ := decodeConfig([]byte(exampleConfig()))
	c.Profiles[0].Grant.Nodes[0].DependsOn = []string{"implement"}
	b, _ = json.Marshal(c)
	if _, err := decodeConfig(b); err == nil {
		t.Fatal("cycle accepted")
	}
}
func TestConfigRefusesSymlinkAndSharedDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(exampleConfig()), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ConfigFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	s := &configStore{path: link}
	if _, err := s.load(); !errors.Is(err, control.ErrDenied) {
		t.Fatal("symlink config loaded")
	}
	if _, err := s.save(exampleConfig()); !errors.Is(err, control.ErrDenied) {
		t.Fatal("symlink config overwritten")
	}
}
