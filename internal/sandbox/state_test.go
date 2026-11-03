package sandbox

import (
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

// These tests are AUDIT-P1-35. The sandbox engine was fixed by AUDIT-P0-04 but
// its state was never reported: the only user-visible label read settings
// directly, so "sandbox=on/network" printed happily on a host where nothing was
// enforced. Every case below pins one way that lie used to be told.
//
// Platform is injected rather than probed so the linux branch is exercised from
// a darwin host (this repo's dev machine has no bubblewrap).

func platform(goos string, hasBinary bool) Platform {
	return Platform{GOOS: goos, HasBinary: func(string) bool { return hasBinary }}
}

// fullyConfigured asks for everything the given platform can actually enforce.
// It is platform-dependent on purpose (AUDIT-P1-37): seccomp exists only on
// linux and allowPty is only read by the darwin profile, so "everything
// enforceable" is not the same set on both.
func fullyConfigured(goos string) tools.SandboxConfig {
	cfg := tools.SandboxConfig{
		Enabled:         true,
		NetworkDisabled: true,
		UnixSocketDeny:  []string{"/var/run/docker.sock"},
	}
	switch goos {
	case "linux":
		cfg.SeccompEnabled = true
	case "darwin":
		cfg.AllowPty = true
	}
	return cfg
}

// TestDescribeStaysQuietOnHealthyInstall is the reverse assertion: a working
// sandbox must not gain a single character of warning noise, otherwise every
// healthy install grows a permanent scary label nobody reads.
func TestDescribeStaysQuietOnHealthyInstall(t *testing.T) {
	cases := map[string]string{
		"linux":  "on/seccomp/network/sockets",
		"darwin": "on/network/sockets",
	}
	for goos, want := range cases {
		state := describe(platform(goos, true), fullyConfigured(goos))
		if state.Label != want {
			t.Fatalf("%s healthy label = %q, want %q", goos, state.Label, want)
		}
		if len(state.Warnings) != 0 {
			t.Fatalf("%s healthy install produced warnings: %v", goos, state.Warnings)
		}
		if !state.Active {
			t.Fatalf("%s healthy install reported Active=false", goos)
		}
	}
}

// TestDescribeReportsSeccompIgnoredOnDarwin is AUDIT-P1-37's headline: seccomp
// is a Linux kernel facility, the darwin profile never reads the option, and the
// label printed "seccomp" anyway.
func TestDescribeReportsSeccompIgnoredOnDarwin(t *testing.T) {
	state := describe(platform("darwin", true), tools.SandboxConfig{Enabled: true, SeccompEnabled: true})
	if strings.Contains(state.Label, "/seccomp") {
		t.Fatalf("label = %q still advertises seccomp on darwin", state.Label)
	}
	if !strings.Contains(state.Label, "degraded:seccomp") {
		t.Fatalf("label = %q, want it to name seccomp as unenforced", state.Label)
	}
	joined := strings.Join(state.Warnings, "\n")
	if !strings.Contains(joined, "sandbox.seccomp.enabled") {
		t.Fatalf("warnings do not name the option: %v", state.Warnings)
	}
	if !strings.Contains(joined, "no fix on this platform") {
		t.Fatalf("warnings do not say the gap is unfixable on macos: %v", state.Warnings)
	}
	// The rest of the sandbox is untouched by this gap.
	if !state.Active {
		t.Fatal("Active = false although the darwin sandbox is running")
	}
}

// TestDescribeKeepsSeccompOnLinux is the reverse assertion for the same option:
// where seccomp really is wired into bubblewrap, nothing changes.
func TestDescribeKeepsSeccompOnLinux(t *testing.T) {
	state := describe(platform("linux", true), tools.SandboxConfig{Enabled: true, SeccompEnabled: true})
	if state.Label != "on/seccomp" {
		t.Fatalf("label = %q, want %q", state.Label, "on/seccomp")
	}
	if len(state.Warnings) != 0 {
		t.Fatalf("linux seccomp reported as unenforced: %v", state.Warnings)
	}
}

// TestDescribeReportsAllowPtyIgnoredOnLinux is the mirror case: allowPty is only
// read by macOSSandboxProfile, so asking for it on linux configures nothing.
func TestDescribeReportsAllowPtyIgnoredOnLinux(t *testing.T) {
	state := describe(platform("linux", true), tools.SandboxConfig{Enabled: true, AllowPty: true})
	if !strings.Contains(state.Label, "degraded:allowPty") {
		t.Fatalf("label = %q, want it to name allowPty as ignored", state.Label)
	}
	joined := strings.Join(state.Warnings, "\n")
	if !strings.Contains(joined, "sandbox.allowPty") {
		t.Fatalf("warnings do not name the option: %v", state.Warnings)
	}
}

// TestDescribeStaysQuietAboutAllowPtyWhenNotRequested is the reverse assertion.
// allowPty defaults to false, so a warning keyed on the default would fire for
// every linux user forever.
func TestDescribeStaysQuietAboutAllowPtyWhenNotRequested(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		state := describe(platform(goos, true), tools.SandboxConfig{Enabled: true})
		if len(state.Warnings) != 0 {
			t.Fatalf("%s: unrequested allowPty produced warnings: %v", goos, state.Warnings)
		}
	}
	// And on darwin the option is genuinely applied, so asking for it is quiet.
	state := describe(platform("darwin", true), tools.SandboxConfig{Enabled: true, AllowPty: true})
	if len(state.Warnings) != 0 {
		t.Fatalf("darwin allowPty reported as ignored: %v", state.Warnings)
	}
}

// TestNetworkPolicyGapsExcludesPlatformGaps keeps AUDIT-P0-04's contract intact.
// PrepareShell refuses to start on a network gap under failIfUnavailable; a
// platform gap must not newly break configurations that run today.
func TestNetworkPolicyGapsExcludesPlatformGaps(t *testing.T) {
	gaps := networkPolicyGaps(platform("darwin", true), tools.SandboxConfig{
		Enabled:        true,
		SeccompEnabled: true,
	})
	if len(gaps) != 0 {
		t.Fatalf("seccomp leaked into the network gap contract: %v", gaps)
	}
	// A real network gap still shows up there.
	gaps = networkPolicyGaps(platform("darwin", true), tools.SandboxConfig{
		Enabled:             true,
		SeccompEnabled:      true,
		NetworkAllowDomains: []string{"example.com"},
	})
	if len(gaps) != 1 || !strings.Contains(gaps[0], "domain-filtered") {
		t.Fatalf("network gap missing from the network contract: %v", gaps)
	}
}

// TestDescribeStaysQuietWhenSandboxIsOff covers the default config
// (sandbox.enabled=false): a user who never asked for a sandbox must not be
// warned that they do not have one.
func TestDescribeStaysQuietWhenSandboxIsOff(t *testing.T) {
	state := describe(platform("windows", false), tools.SandboxConfig{
		NetworkDisabled:     true,
		NetworkAllowDomains: []string{"example.com"},
		NetworkMITMRequired: true,
	})
	if state.Label != "off" {
		t.Fatalf("label = %q, want %q", state.Label, "off")
	}
	if len(state.Warnings) != 0 {
		t.Fatalf("disabled sandbox produced warnings: %v", state.Warnings)
	}
	if state.Configured {
		t.Fatal("Configured = true for a disabled sandbox")
	}
}

// TestDescribeDegradesLabelWhenSandboxBinaryMissing is the core lie: settings
// say network is off, the binary that would enforce it is absent, and the old
// label printed "on/network" regardless.
func TestDescribeDegradesLabelWhenSandboxBinaryMissing(t *testing.T) {
	cases := []struct {
		goos   string
		binary string
		remedy string
	}{
		{goos: "darwin", binary: "sandbox-exec", remedy: "path"},
		{goos: "linux", binary: "bwrap", remedy: "install"},
	}
	for _, tc := range cases {
		state := describe(platform(tc.goos, false), fullyConfigured(tc.goos))
		if strings.HasPrefix(state.Label, "on") {
			t.Fatalf("%s: label = %q, want a label that cannot be read as enforced", tc.goos, state.Label)
		}
		if !strings.Contains(state.Label, "not-enforced") {
			t.Fatalf("%s: label = %q, want it to say not-enforced", tc.goos, state.Label)
		}
		if state.Active {
			t.Fatalf("%s: Active = true with no sandbox binary", tc.goos)
		}
		joined := strings.Join(state.Warnings, "\n")
		if !strings.Contains(joined, tc.binary) {
			t.Fatalf("%s: warnings do not name %s: %v", tc.goos, tc.binary, state.Warnings)
		}
		// Actionable: say what is not protected, and what to do about it here.
		if !strings.Contains(joined, "no shell command is sandboxed") {
			t.Fatalf("%s: warnings do not say what is unprotected: %v", tc.goos, state.Warnings)
		}
		if !strings.Contains(strings.ToLower(joined), tc.remedy) {
			t.Fatalf("%s: warnings carry no remedy (%q): %v", tc.goos, tc.remedy, state.Warnings)
		}
	}
}

// TestDescribeDegradesLabelWhenPlatformNotEnabled covers the config-only way to
// end up unprotected: the sandbox is on, but not for this OS.
func TestDescribeDegradesLabelWhenPlatformNotEnabled(t *testing.T) {
	cfg := fullyConfigured("darwin")
	cfg.EnabledPlatforms = []string{"linux"}
	state := describe(platform("darwin", true), cfg)
	if strings.HasPrefix(state.Label, "on") || !strings.Contains(state.Label, "not-enforced") {
		t.Fatalf("label = %q, want a not-enforced label", state.Label)
	}
	joined := strings.Join(state.Warnings, "\n")
	if !strings.Contains(joined, "sandbox.enabledPlatforms") || !strings.Contains(joined, "macos") {
		t.Fatalf("warnings do not name the option and platform: %v", state.Warnings)
	}
}

// TestDescribeDegradesLabelOnUnsupportedPlatform must also say the truth that
// there is nothing to fix locally.
func TestDescribeDegradesLabelOnUnsupportedPlatform(t *testing.T) {
	state := describe(platform("windows", true), fullyConfigured("windows"))
	if strings.HasPrefix(state.Label, "on") || !strings.Contains(state.Label, "not-enforced") {
		t.Fatalf("label = %q, want a not-enforced label", state.Label)
	}
	joined := strings.Join(state.Warnings, "\n")
	if !strings.Contains(joined, "windows") {
		t.Fatalf("warnings do not name the platform: %v", state.Warnings)
	}
	if !strings.Contains(joined, "no fix on this platform") {
		t.Fatalf("warnings do not say the gap is unfixable here: %v", state.Warnings)
	}
}

// TestDescribeNamesUnenforceableNetworkOptionInLabel is the AUDIT-P0-04 half:
// the OS sandbox runs, but a specific configured network option is a no-op for
// shell commands. The label has to name which one.
func TestDescribeNamesUnenforceableNetworkOptionInLabel(t *testing.T) {
	cases := []struct {
		name  string
		cfg   tools.SandboxConfig
		key   string
		about string
	}{
		{
			name:  "domain lists",
			cfg:   tools.SandboxConfig{Enabled: true, NetworkAllowDomains: []string{"example.com"}},
			key:   "network.domains",
			about: "domain-filtered",
		},
		{
			name:  "proxy required",
			cfg:   tools.SandboxConfig{Enabled: true, NetworkProxyRequired: true},
			key:   "network.proxy",
			about: "proxy.required",
		},
	}
	for _, tc := range cases {
		state := describe(platform("darwin", true), tc.cfg)
		if !strings.Contains(state.Label, "degraded:"+tc.key) {
			t.Fatalf("%s: label = %q, want it to name %q", tc.name, state.Label, tc.key)
		}
		if len(state.Warnings) == 0 {
			t.Fatalf("%s: no warnings for an unenforceable option", tc.name)
		}
		joined := strings.Join(state.Warnings, "\n")
		if !strings.Contains(joined, tc.about) {
			t.Fatalf("%s: warnings do not explain the gap (%q): %v", tc.name, tc.about, state.Warnings)
		}
		// The OS sandbox itself is still doing its filesystem job.
		if !state.Active {
			t.Fatalf("%s: Active = false although the sandbox binary is present", tc.name)
		}
	}
}

// TestDescribeDropsMisleadingNetworkPartFromLabel: allowDomains used to add a
// bare "network" to the label even though shell commands were never filtered.
// Printing a part that is not enforced is exactly the bug.
func TestDescribeDropsMisleadingNetworkPartFromLabel(t *testing.T) {
	state := describe(platform("darwin", true), tools.SandboxConfig{
		Enabled:             true,
		NetworkAllowDomains: []string{"example.com"},
	})
	if strings.Contains(state.Label, "/network/") || strings.HasSuffix(state.Label, "/network") {
		t.Fatalf("label = %q still advertises an unenforced network part", state.Label)
	}
}

// TestUnavailableReasonSummarisesState keeps the pre-existing API honest now
// that it is finally wired to a caller.
func TestUnavailableReasonSummarisesState(t *testing.T) {
	if reason := unavailableReason(platform("darwin", true), fullyConfigured("darwin")); reason != "" {
		t.Fatalf("healthy sandbox reported a reason: %q", reason)
	}
	if reason := unavailableReason(platform("darwin", false), fullyConfigured("darwin")); !strings.Contains(reason, "sandbox-exec") {
		t.Fatalf("reason = %q, want it to name the missing binary", reason)
	}
}
