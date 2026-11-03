package feishuprovision

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

type CLIDiscovery struct{ LookPath func(string) (string, error) }

func (d CLIDiscovery) DiscoverCLI(ctx context.Context) (provisioning.CLIAvailability, error) {
	lookup := d.LookPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	result := provisioning.CLIAvailability{}
	if _, err := lookup("niuma"); err == nil {
		result.ProjectCLI = true
	}
	if _, err := lookup("lark-cli"); err == nil {
		result.LarkCLI = true
	}
	if !result.ProjectCLI && !result.LarkCLI {
		result.Message = "no Feishu onboarding CLI found; select an existing account or enter credentials manually"
	}
	return result, nil
}

// CLIInstaller owns the optional local lark-cli bootstrap. The desktop flow
// keeps this behind a small port so a server can report availability without
// ever making the browser execute a local command.
type CLIInstaller struct {
	LookPath       func(string) (string, error)
	CommandContext func(context.Context, string, ...string) *exec.Cmd
}

func (i CLIInstaller) commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if i.CommandContext != nil {
		return i.CommandContext(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

func (i CLIInstaller) discovery() CLIDiscovery {
	return CLIDiscovery{LookPath: i.LookPath}
}

func (i CLIInstaller) Availability(ctx context.Context) (provisioning.CLIAvailability, error) {
	return i.discovery().DiscoverCLI(ctx)
}

// Install uses the official npm bootstrap published by Lark. It is
// deliberately explicit and bounded by the caller's context; no package
// manager command is started while merely reading settings.
func (i CLIInstaller) Install(ctx context.Context) (provisioning.CLIAvailability, error) {
	if ctx == nil {
		return provisioning.CLIAvailability{}, fmt.Errorf("lark-cli install: context is required")
	}
	if available, err := i.Availability(ctx); err == nil && available.LarkCLI {
		return available, nil
	}
	cmd := i.commandContext(ctx, "npx", "--yes", "@larksuite/cli@latest", "install")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if len(message) > 500 {
			message = message[len(message)-500:]
		}
		if message == "" {
			return provisioning.CLIAvailability{}, fmt.Errorf("lark-cli install: %w", err)
		}
		return provisioning.CLIAvailability{}, fmt.Errorf("lark-cli install: %w: %s", err, message)
	}
	available, err := i.Availability(ctx)
	if err != nil {
		return available, err
	}
	if !available.LarkCLI {
		return available, fmt.Errorf("lark-cli install completed but executable was not found")
	}
	return available, nil
}
