package sandbox

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestCheckPowerShellCommandAllowsWorkspaceWrites(t *testing.T) {
	cwd := t.TempDir()
	for _, command := range []string{
		`Set-Content -Path note.txt -Value hello`,
		`"hello" | Out-File nested.txt`,
		`New-Item -Path nested -ItemType Directory`,
		`Copy-Item source.txt nested/copy.txt`,
	} {
		if err := CheckPowerShellCommand(cwd, nil, command, tools.SandboxConfig{}); err != nil {
			t.Fatalf("%s err = %v", command, err)
		}
	}
}

func TestCheckPowerShellCommandRejectsWritesOutsideWorkspace(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	for _, command := range []string{
		`Set-Content -Path ` + filepath.Join(other, "note.txt") + ` -Value hello`,
		`"hello" > ` + filepath.Join(other, "note.txt"),
		`Remove-Item ` + filepath.Join(other, "note.txt"),
		`Copy-Item local.txt ` + filepath.Join(other, "copy.txt"),
		`Move-Item local.txt ` + filepath.Join(other, "moved.txt"),
	} {
		err := CheckPowerShellCommand(cwd, nil, command, tools.SandboxConfig{})
		if err == nil || !strings.Contains(err.Error(), "outside the current workspace") {
			t.Fatalf("%s err = %v", command, err)
		}
	}
}

func TestCheckPowerShellCommandRejectsDynamicWritePath(t *testing.T) {
	if err := CheckPowerShellCommand(t.TempDir(), nil, `Set-Content -Path $env:TARGET -Value hello`, tools.SandboxConfig{}); err == nil {
		t.Fatal("expected dynamic path rejection")
	}
}

func TestPreparePowerShellRejectsSandboxUnavailableWhenStrict(t *testing.T) {
	_, err := PreparePowerShell(`Write-Output hello`, tools.SandboxConfig{Enabled: true, FailIfUnavailable: true}, false)
	if err == nil || !strings.Contains(err.Error(), "PowerShell OS sandbox is unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestPreparePowerShellAllowedFallbackCarriesNetworkPolicyEnv(t *testing.T) {
	spec, err := PreparePowerShell(`Write-Output hello`, tools.SandboxConfig{
		Enabled:                  true,
		AllowUnsandboxedCommands: true,
		NetworkProxyURL:          "http://127.0.0.1:8080",
		NetworkMITMCAFile:        "/tmp/ca.pem",
	}, false)
	if err != nil {
		if strings.Contains(err.Error(), "PowerShell executable not found") {
			t.Skip(err)
		}
		t.Fatalf("PreparePowerShell() err = %v", err)
	}
	env := strings.Join(spec.Env, "\x00")
	for _, want := range []string{"SANDBOX_RUNTIME=unsupported-powershell", "HTTPS_PROXY=http://127.0.0.1:8080", "SSL_CERT_FILE=/tmp/ca.pem"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q in %+v", want, spec.Env)
		}
	}
}
