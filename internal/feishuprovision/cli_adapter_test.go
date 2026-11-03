package feishuprovision

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
)

func TestCLIInstallerInstallsOfficialPackageWhenLarkCLIIsMissing(t *testing.T) {
	installed := false
	var command []string
	installer := CLIInstaller{
		LookPath: func(name string) (string, error) {
			if name == "lark-cli" && installed {
				return "/usr/local/bin/lark-cli", nil
			}
			return "", exec.ErrNotFound
		},
		CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			command = append([]string{name}, args...)
			installed = true
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	availability, err := installer.Install(context.Background())
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if !availability.LarkCLI {
		t.Fatalf("availability = %+v", availability)
	}
	want := []string{"npx", "--yes", "@larksuite/cli@latest", "install"}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command = %#v, want %#v", command, want)
	}
}

func TestCLIInstallerDoesNotReinstallAvailableCLI(t *testing.T) {
	called := false
	installer := CLIInstaller{
		LookPath: func(name string) (string, error) {
			if name == "lark-cli" {
				return "/usr/local/bin/lark-cli", nil
			}
			return "", exec.ErrNotFound
		},
		CommandContext: func(context.Context, string, ...string) *exec.Cmd {
			called = true
			return exec.Command("sh", "-c", "exit 1")
		},
	}

	availability, err := installer.Install(context.Background())
	if err != nil || !availability.LarkCLI {
		t.Fatalf("Install() = %+v, %v", availability, err)
	}
	if called {
		t.Fatal("installer invoked npx despite lark-cli being available")
	}
}
