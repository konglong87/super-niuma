package sandbox

import (
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

// TestMacOSProfileDeniesNetworkWhenDisabled is AUDIT-P0-04: the darwin profile
// carried no network primitive at all, so sandbox.network.disabled was enforced
// only by a command-name allowlist.
func TestMacOSProfileDeniesNetworkWhenDisabled(t *testing.T) {
	profile, err := macOSSandboxProfile(t.TempDir(), nil, tools.SandboxConfig{Enabled: true, NetworkDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, "(deny network*)") {
		t.Fatalf("profile has no network deny rule:\n%s", profile)
	}
}

func TestMacOSProfileLeavesNetworkAloneWhenNotDisabled(t *testing.T) {
	profile, err := macOSSandboxProfile(t.TempDir(), nil, tools.SandboxConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(profile, "(deny network*)") {
		t.Fatalf("profile denies network without sandbox.network.disabled:\n%s", profile)
	}
}

// TestMacOSSandboxActuallyBlocksOutboundConnect runs the generated profile
// through sandbox-exec against a listener on loopback. A profile that merely
// contains the right text but is not honoured would pass the check above.
func TestMacOSSandboxActuallyBlocksOutboundConnect(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec only")
	}
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	nc, err := exec.LookPath("nc")
	if err != nil {
		t.Skipf("nc unavailable: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	probe := nc + " -z -w 2 127.0.0.1 " + strconv.Itoa(addr.Port)

	// Unsandboxed the connect must succeed, otherwise the test proves nothing.
	if out, err := exec.Command("/bin/sh", "-c", probe).CombinedOutput(); err != nil {
		t.Skipf("loopback probe fails without a sandbox (%v): %s", err, out)
	}

	profile, err := macOSSandboxProfile(t.TempDir(), nil, tools.SandboxConfig{Enabled: true, NetworkDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(sandboxExec, "-p", profile, "/bin/sh", "-c", probe).CombinedOutput()
	if err == nil {
		t.Fatalf("sandboxed connect succeeded, want it blocked: %s", out)
	}
}

// TestNetworkPolicyGapsReportsUnenforceableOptions is the other half of
// AUDIT-P0-04: a network option that cannot be enforced must be reported, never
// silently ignored.
func TestNetworkPolicyGapsReportsUnenforceableOptions(t *testing.T) {
	if gaps := NetworkPolicyGaps(tools.SandboxConfig{NetworkProxyRequired: true}); len(gaps) != 0 {
		t.Fatalf("disabled sandbox reported gaps: %v", gaps)
	}
	if gaps := NetworkPolicyGaps(tools.SandboxConfig{Enabled: true, NetworkDisabled: true}); len(gaps) != 0 && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		t.Fatalf("network.disabled reported as unenforceable on %s: %v", runtime.GOOS, gaps)
	}
	gaps := NetworkPolicyGaps(tools.SandboxConfig{Enabled: true, NetworkAllowDomains: []string{"example.com"}})
	if len(gaps) == 0 || !strings.Contains(strings.Join(gaps, " "), "domain-filtered") {
		t.Fatalf("domain filtering gap not reported: %v", gaps)
	}
	gaps = NetworkPolicyGaps(tools.SandboxConfig{Enabled: true, NetworkMITMRequired: true})
	if len(gaps) == 0 || !strings.Contains(strings.Join(gaps, " "), "mitm.required") {
		t.Fatalf("mitm gap not reported: %v", gaps)
	}
	// UnavailableReason must surface the same gaps rather than reporting "".
	reason := UnavailableReason(tools.SandboxConfig{Enabled: true, NetworkAllowDomains: []string{"example.com"}})
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if !IsAvailable() {
			t.Skip("platform sandbox binary unavailable; primary reason wins")
		}
		if !strings.Contains(reason, "domain-filtered") {
			t.Fatalf("UnavailableReason = %q, want the domain filtering gap", reason)
		}
	}
}

// TestPrepareShellRefusesUnenforceableNetworkPolicyWhenStrict makes the failure
// loud for callers that asked for strictness.
func TestPrepareShellRefusesUnenforceableNetworkPolicyWhenStrict(t *testing.T) {
	cfg := tools.SandboxConfig{
		Enabled:             true,
		FailIfUnavailable:   true,
		NetworkMITMRequired: true,
	}
	if _, err := PrepareShell(t.TempDir(), nil, "printf hello", cfg, false); err == nil {
		t.Fatal("PrepareShell accepted an unenforceable network policy under failIfUnavailable")
	}
	// Without failIfUnavailable the sandbox stays best-effort, as elsewhere.
	cfg.FailIfUnavailable = false
	if _, err := PrepareShell(t.TempDir(), nil, "printf hello", cfg, false); err != nil {
		t.Fatalf("best-effort PrepareShell failed: %v", err)
	}
}
