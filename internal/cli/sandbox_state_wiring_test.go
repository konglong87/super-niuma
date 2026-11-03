package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/sandbox"
)

// AUDIT-P1-35: the sandbox label was built from settings alone, so it happily
// claimed protection the host was not providing. These pin the label to the
// actual verdict and pin the warnings to the surfaces a user reads.

func trueValue() *bool {
	value := true
	return &value
}

func requireHostSandbox(t *testing.T) {
	t.Helper()
	if !sandbox.IsAvailable() {
		t.Skip("host has no OS sandbox binary; the degraded label is correct here")
	}
}

// TestTUISandboxLabelUnchangedOnHealthyInstall is the reverse assertion for the
// label: a fully enforced sandbox must render exactly the string it always did.
//
// Note what is NOT in this config: seccomp. An earlier version of this test set
// it and called the result healthy, which AUDIT-P1-37 showed was wrong on macOS
// — seccomp is only wired into the bubblewrap path. "Everything enforceable" is
// a per-platform set, so this uses only options every supported platform honours.
func TestTUISandboxLabelUnchangedOnHealthyInstall(t *testing.T) {
	requireHostSandbox(t)
	settings := &config.SandboxSettings{Enabled: trueValue()}
	settings.Network.Disabled = trueValue()
	settings.UnixSockets.Deny = []string{"/var/run/docker.sock"}

	if label := tuiSandboxLabel(settings); label != "on/network/sockets" {
		t.Fatalf("label = %q, want the unchanged %q", label, "on/network/sockets")
	}
	if warnings := tuiSandboxWarnings(settings); len(warnings) != 0 {
		t.Fatalf("healthy sandbox produced warnings: %v", warnings)
	}
}

// TestTUISandboxLabelDegradesForPlatformIgnoredOption is AUDIT-P1-37 through the
// real wiring on whichever host runs the suite.
func TestTUISandboxLabelDegradesForPlatformIgnoredOption(t *testing.T) {
	requireHostSandbox(t)
	settings := &config.SandboxSettings{Enabled: trueValue()}
	var wantKey, wantOption string
	switch runtime.GOOS {
	case "darwin":
		settings.Seccomp.Enabled = trueValue()
		wantKey, wantOption = "degraded:seccomp", "sandbox.seccomp.enabled"
	case "linux":
		settings.AllowPty = trueValue()
		wantKey, wantOption = "degraded:allowPty", "sandbox.allowPty"
	default:
		t.Skipf("no platform-ignored sandbox option to assert on %s", runtime.GOOS)
	}

	if label := tuiSandboxLabel(settings); !strings.Contains(label, wantKey) {
		t.Fatalf("label = %q, want it to contain %q", label, wantKey)
	}
	warnings := tuiSandboxWarnings(settings)
	if len(warnings) == 0 || !strings.Contains(warnings[0], wantOption) {
		t.Fatalf("warnings = %v, want one naming %s", warnings, wantOption)
	}
}

func TestTUISandboxLabelOffWhenDisabled(t *testing.T) {
	if label := tuiSandboxLabel(nil); label != "off" {
		t.Fatalf("label = %q, want %q", label, "off")
	}
	if warnings := tuiSandboxWarnings(nil); len(warnings) != 0 {
		t.Fatalf("disabled sandbox produced warnings: %v", warnings)
	}
}

// TestTUISandboxLabelDegradesWhenNetworkPolicyUnenforceable is the exact config
// the audit called out: allowDomains used to print a confident "on/network".
func TestTUISandboxLabelDegradesWhenNetworkPolicyUnenforceable(t *testing.T) {
	requireHostSandbox(t)
	settings := &config.SandboxSettings{Enabled: trueValue()}
	settings.Network.AllowDomains = []string{"example.com"}

	label := tuiSandboxLabel(settings)
	if !strings.Contains(label, "degraded:network.domains") {
		t.Fatalf("label = %q, want it to name the unenforced option", label)
	}
	if strings.Contains(label, "/network/") || strings.HasSuffix(label, "/network") {
		t.Fatalf("label = %q still advertises network as enforced", label)
	}
	warnings := tuiSandboxWarnings(settings)
	if len(warnings) == 0 {
		t.Fatal("no warnings for an unenforceable network policy")
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "domain-filtered") {
		t.Fatalf("warnings do not explain the gap: %v", warnings)
	}
}

func writeSandboxConfig(t *testing.T, project, body string) {
	t.Helper()
	path := filepath.Join(project, "config", "config.local.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	activateSettingsFixture(t, path)
}

func runStatusJSON(t *testing.T, project string) (map[string]any, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_ENV", "")
	t.Setenv("CLAUDE_CODE_MODEL", "")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "status"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode status: %v\n%s", err, out.String())
	}
	return payload, out.String()
}

// TestStatusReportsSandboxWarnings closes the "zero callers" half of the audit:
// UnavailableReason finally reaches a user-visible surface.
func TestStatusReportsSandboxWarnings(t *testing.T) {
	requireHostSandbox(t)
	project := t.TempDir()
	writeSandboxConfig(t, project, "sandbox:\n  enabled: true\n  network:\n    allowDomains:\n      - example.com\n")

	payload, raw := runStatusJSON(t, project)
	warnings, ok := payload["sandboxWarnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("status has no sandboxWarnings:\n%s", raw)
	}
	joined := ""
	for _, warning := range warnings {
		joined += warning.(string) + "\n"
	}
	if !strings.Contains(joined, "allowDomains") || !strings.Contains(joined, "domain-filtered") {
		t.Fatalf("sandboxWarnings do not name the option and reason: %s", joined)
	}
	// Actionable, not a vague warning: it must say what to do instead.
	if !strings.Contains(joined, "sandbox.network.disabled=true") {
		t.Fatalf("sandboxWarnings carry no remedy: %s", joined)
	}
}

// TestStatusOmitsSandboxWarningsWhenNothingIsWrong is the reverse assertion for
// /status: the default install (sandbox off) must not gain a field, or operators
// learn to ignore the field that matters.
func TestStatusOmitsSandboxWarningsWhenNothingIsWrong(t *testing.T) {
	project := t.TempDir()

	payload, raw := runStatusJSON(t, project)
	if _, present := payload["sandboxWarnings"]; present {
		t.Fatalf("status grew a sandboxWarnings field on a default install:\n%s", raw)
	}
	if strings.Contains(raw, "sandbox") {
		t.Fatalf("status mentions sandbox on a default install:\n%s", raw)
	}
}
