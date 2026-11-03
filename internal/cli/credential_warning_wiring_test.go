package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The permission check added for AUDIT-P0-06 is only useful if an operator can
// actually see it. These assert the two diagnostic commands surface it, so a
// later refactor cannot silently drop the wiring.

func writeWorldReadableCredentialConfig(t *testing.T, project string) string {
	t.Helper()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	path := filepath.Join(project, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"env":{"ANTHROPIC_API_KEY":"sk-test-not-a-real-key"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStatusCommandReportsInsecureCredentialFile(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	path := writeWorldReadableCredentialConfig(t, project)

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		ConfigWarnings []string `json:"configWarnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v\n%s", err, out.String())
	}
	joined := strings.Join(payload.ConfigWarnings, "\n")
	if !strings.Contains(joined, path) || !strings.Contains(joined, "chmod 600") {
		t.Fatalf("configWarnings = %+v, want a chmod 600 warning for %s", payload.ConfigWarnings, path)
	}
	if strings.Contains(out.String(), "sk-test-not-a-real-key") {
		t.Fatalf("status leaked the credential:\n%s", out.String())
	}
}

func TestDoctorReportsInsecureCredentialFile(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")
	path := writeWorldReadableCredentialConfig(t, project)
	t.Chdir(project)

	var out bytes.Buffer
	if err := doctor(&out); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Settings struct {
			ConfigWarnings []string `json:"configWarnings"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode doctor: %v\n%s", err, out.String())
	}
	if !strings.Contains(strings.Join(payload.Settings.ConfigWarnings, "\n"), path) {
		t.Fatalf("configWarnings = %+v, want a mention of %s", payload.Settings.ConfigWarnings, path)
	}
}
