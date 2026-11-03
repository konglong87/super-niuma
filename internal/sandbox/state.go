package sandbox

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

// This file answers one question for the UI: is the sandbox the user configured
// actually enforcing anything on this machine?
//
// AUDIT-P0-04 fixed the engine and AUDIT-P1-35 found that nobody told the user.
// The only sandbox status a user ever saw was built straight from settings, so a
// host with no sandbox-exec on PATH still printed "sandbox=on/network". That is
// the worst failure mode available here: the user believes they are isolated and
// behaves accordingly.
//
// Following AUDIT-P0-06 (config.Warnings), this reports and never refuses, and
// stays completely silent when nothing is wrong — a healthy install must not
// grow a permanent warning that trains people to ignore warnings.

// Platform holds the two host facts that decide whether the OS sandbox can wrap
// a shell command. It is a parameter rather than a direct runtime lookup so the
// linux branch stays testable from a darwin host and vice versa.
type Platform struct {
	GOOS      string
	HasBinary func(name string) bool
}

func hostPlatform() Platform {
	return Platform{GOOS: runtime.GOOS, HasBinary: commandExists}
}

// name is the spelling used in sandbox.enabledPlatforms.
func (p Platform) name() string {
	if p.GOOS == "darwin" {
		return "macos"
	}
	return p.GOOS
}

// sandboxBinary is the helper that enforces the OS sandbox here, or "" when this
// platform has no OS sandbox at all.
func (p Platform) sandboxBinary() string {
	switch p.GOOS {
	case "darwin":
		return "sandbox-exec"
	case "linux":
		return "bwrap"
	default:
		return ""
	}
}

func (p Platform) hasBinary(name string) bool {
	if p.HasBinary == nil {
		return commandExists(name)
	}
	return p.HasBinary(name)
}

func (p Platform) sandboxAvailable() bool {
	binary := p.sandboxBinary()
	return binary != "" && p.hasBinary(binary)
}

// enabledByConfig reports whether sandbox.enabledPlatforms covers this platform.
func (p Platform) enabledByConfig(enabled []string) bool {
	if len(enabled) == 0 {
		return true
	}
	current := p.name()
	for _, item := range enabled {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == current || item == p.GOOS {
			return true
		}
	}
	return false
}

// State is the user-facing sandbox status. Label goes on the status line and the
// welcome card; Warnings are shown verbatim by /status and the welcome card and
// are empty whenever the configuration is fully enforced.
type State struct {
	// Configured mirrors sandbox.enabled.
	Configured bool
	// Active reports whether the OS sandbox will really wrap shell commands.
	Active bool
	// Label never reads as enforced when Active is false or Warnings is set.
	Label string
	// Warnings each name one option, why it is not enforced, and what the
	// operator can do about it on this machine.
	Warnings []string
}

// Describe reports the sandbox state on the current host.
func Describe(cfg tools.SandboxConfig) State {
	return describe(hostPlatform(), cfg)
}

func describe(p Platform, cfg tools.SandboxConfig) State {
	if !cfg.Enabled {
		return State{Label: "off"}
	}
	state := State{Configured: true}
	// A host where nothing at all is sandboxed makes every per-option gap moot,
	// so report the one thing the user has to fix.
	if reason := p.inactiveReason(); reason != "" {
		return State{Configured: true, Label: "degraded/not-enforced", Warnings: []string{reason}}
	}
	if !p.enabledByConfig(cfg.EnabledPlatforms) {
		return State{
			Configured: true,
			Label:      "degraded/not-enforced",
			Warnings: []string{fmt.Sprintf(
				"sandbox.enabled is set but %s is not in sandbox.enabledPlatforms, so no shell command is sandboxed on this machine. Add %q to sandbox.enabledPlatforms to enforce it here.",
				p.name(), p.name())},
		}
	}
	state.Active = true

	// Only advertise what is genuinely enforced: a part that names an option the
	// platform ignores is the same lie in smaller print.
	parts := []string{"on"}
	if cfg.SeccompEnabled && p.supportsSeccomp() {
		parts = append(parts, "seccomp")
	}
	if cfg.NetworkDisabled {
		parts = append(parts, "network")
	}
	if len(cfg.UnixSocketDeny) > 0 {
		parts = append(parts, "sockets")
	}
	if gaps := policyGaps(p, cfg); len(gaps) > 0 {
		keys := make([]string, 0, len(gaps))
		for _, gap := range gaps {
			keys = append(keys, gap.Key)
			state.Warnings = append(state.Warnings, gap.String())
		}
		parts = append(parts, "degraded:"+strings.Join(keys, ","))
	}
	state.Label = strings.Join(parts, "/")
	return state
}

// inactiveReason explains why the OS sandbox cannot run here at all, or "" when
// it can. Config-level exclusions are handled by the caller so that the host
// capability check comes first.
func (p Platform) inactiveReason() string {
	binary := p.sandboxBinary()
	if binary == "" {
		return fmt.Sprintf("sandbox.enabled is set but this build has no OS sandbox for %s, so no shell command is sandboxed. There is no fix on this platform: run on macOS or Linux for OS-level sandboxing, or set sandbox.enabled=false so the configuration stops implying protection you do not have.", p.name())
	}
	if p.hasBinary(binary) {
		return ""
	}
	return fmt.Sprintf("sandbox.enabled is set but %s was not found on PATH, so no shell command is sandboxed on this machine. %s", binary, p.missingBinaryRemedy())
}

func (p Platform) missingBinaryRemedy() string {
	switch p.GOOS {
	case "darwin":
		return "sandbox-exec ships with macOS, so a PATH that drops /usr/bin is the usual cause: repair PATH, or set sandbox.enabled=false so the configuration stops implying protection you do not have."
	default:
		return "Install bubblewrap (apt install bubblewrap / dnf install bubblewrap), or set sandbox.enabled=false so the configuration stops implying protection you do not have."
	}
}

// IsAvailable reports whether this host has the binary that enforces the OS
// sandbox.
func IsAvailable() bool {
	return hostPlatform().sandboxAvailable()
}

// UnavailableReason summarises everything the configured sandbox fails to
// enforce here, or "" when the configuration is fully enforced.
func UnavailableReason(cfg tools.SandboxConfig) string {
	return unavailableReason(hostPlatform(), cfg)
}

func unavailableReason(p Platform, cfg tools.SandboxConfig) string {
	return strings.Join(describe(p, cfg).Warnings, "; ")
}

// PolicyGap is one configured option that this platform cannot enforce against
// an arbitrary shell command.
type PolicyGap struct {
	// Key is the short form used in the status label, e.g. "network.domains".
	Key string
	// Detail says what is not enforced and why.
	Detail string
	// Remedy says what the operator can do here, including saying so when the
	// answer is nothing.
	Remedy string
	// Network marks the gaps that belong to AUDIT-P0-04's contract, i.e. the
	// ones PrepareShell refuses to start on under sandbox.failIfUnavailable.
	// Platform gaps are reported but deliberately do not gate startup: newly
	// refusing a configuration that runs fine today would be a regression.
	Network bool
}

func (g PolicyGap) String() string {
	if g.Remedy == "" {
		return g.Detail
	}
	return g.Detail + " " + g.Remedy
}

// NetworkPolicyGaps returns the configured network isolation options this
// platform cannot enforce against arbitrary shell commands.
//
// A network option that silently does nothing is worse than one reported as
// unavailable, so every caller that shows sandbox state must surface these, and
// PrepareShell refuses to start when sandbox.failIfUnavailable is set.
func NetworkPolicyGaps(cfg tools.SandboxConfig) []string {
	return networkPolicyGaps(hostPlatform(), cfg)
}

func networkPolicyGaps(p Platform, cfg tools.SandboxConfig) []string {
	var out []string
	for _, gap := range policyGaps(p, cfg) {
		if gap.Network {
			out = append(out, gap.String())
		}
	}
	return out
}

// platformPolicyGaps reports options that this platform's sandbox never reads at
// all (AUDIT-P1-37). These are not network gaps: the sandbox is doing everything
// it can, the configuration simply asks for something that does not exist here.
//
// Each is keyed on the option being explicitly requested, never on its default,
// or the warning would fire forever on every host.
func platformPolicyGaps(p Platform, cfg tools.SandboxConfig) []PolicyGap {
	var gaps []PolicyGap
	if cfg.SeccompEnabled && !p.supportsSeccomp() {
		gaps = append(gaps, PolicyGap{
			Key:    "seccomp",
			Detail: fmt.Sprintf("sandbox.seccomp.enabled is ignored on %s: seccomp is a Linux kernel facility and only the bubblewrap path passes a filter, so no syscall filtering is applied here.", p.name()),
			Remedy: "There is no fix on this platform: drop the option so it stops implying syscall filtering, or run on Linux with bubblewrap.",
		})
	}
	if cfg.AllowPty && !p.readsAllowPty() {
		gaps = append(gaps, PolicyGap{
			Key: "allowPty",
			// Deliberately does not claim pty is therefore denied: under
			// bubblewrap whatever pty access exists comes from `--dev /dev`
			// regardless of this option, and that has not been verified on a
			// real Linux host.
			Detail: fmt.Sprintf("sandbox.allowPty is ignored on %s: only the darwin sandbox profile reads it, so setting it neither grants nor withholds pty access here.", p.name()),
			Remedy: "There is no fix on this platform: do not rely on the option outside macOS.",
		})
	}
	return gaps
}

// supportsSeccomp mirrors the one place seccomp is actually wired up:
// linuxSandboxSpec passes a filter fd to bubblewrap. The darwin profile has no
// equivalent primitive.
func (p Platform) supportsSeccomp() bool { return p.GOOS == "linux" }

// readsAllowPty mirrors macOSSandboxProfile, the only reader of cfg.AllowPty.
func (p Platform) readsAllowPty() bool { return p.GOOS == "darwin" }

func policyGaps(p Platform, cfg tools.SandboxConfig) []PolicyGap {
	if !cfg.Enabled {
		return nil
	}
	gaps := platformPolicyGaps(p, cfg)
	switch p.GOOS {
	case "darwin", "linux":
		// sandbox.network.disabled is enforced by the OS sandbox itself:
		// seatbelt `(deny network*)` on darwin, `--unshare-net` on linux.
	default:
		if cfg.NetworkDisabled {
			gaps = append(gaps, PolicyGap{
				Key:     "network.disabled",
				Detail:  fmt.Sprintf("sandbox.network.disabled is not enforced on %s: this build has no network isolation primitive there, so shell commands reach the network normally.", p.name()),
				Remedy:  "There is no fix on this platform: run on macOS or Linux for network isolation.",
				Network: true,
			})
		}
	}
	// Domain filtering has no OS-sandbox equivalent. It is applied to the
	// built-in HTTP tools, but a shell command reaches the network directly.
	if !cfg.NetworkDisabled && (len(cfg.NetworkAllowDomains) > 0 || len(cfg.NetworkDenyDomains) > 0) {
		gaps = append(gaps, PolicyGap{
			Key:     "network.domains",
			Detail:  "sandbox.network.allowDomains/denyDomains filter the built-in HTTP tools only: shell commands are not domain-filtered.",
			Remedy:  "Set sandbox.network.disabled=true to cut shell network access entirely, or enforce the domain list at the network layer; it cannot be enforced in-process.",
			Network: true,
		})
	}
	// Proxy and MITM enforcement for shell commands is a command-name allowlist,
	// which a compiled binary or `python3 -c` bypasses.
	if cfg.NetworkProxyRequired || cfg.NetworkMITMRequired {
		gaps = append(gaps, PolicyGap{
			Key:     "network.proxy",
			Detail:  "sandbox.network.proxy.required/mitm.required are checked for known network commands only: a compiled binary or inline interpreter bypasses them.",
			Remedy:  "Enforce the proxy at the network layer, or set sandbox.network.disabled=true; it cannot be enforced in-process.",
			Network: true,
		})
	}
	return gaps
}
