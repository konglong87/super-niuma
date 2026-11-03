package sandbox

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"unicode"

	"github.com/konglong87/super-niuma/internal/tools"
)

// CheckPowerShellCommand mirrors CheckShellCommand; see the note there on why cfg
// is required rather than optional.
func CheckPowerShellCommand(cwd string, writableRoots []string, command string, cfg tools.SandboxConfig) error {
	tokens := powerShellTokens(command)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "|" || token == ";" {
			continue
		}
		if isPowerShellRedirect(token) {
			if i+1 >= len(tokens) {
				return fmt.Errorf("PowerShell redirection target is required")
			}
			if err := checkPowerShellWritePath(cwd, writableRoots, tokens[i+1], cfg); err != nil {
				return err
			}
			i++
			continue
		}
		name := powerShellCommandName(token)
		if name == "" || !isMutatingPowerShellCommand(name) {
			continue
		}
		segment := powerShellSegment(tokens, i+1)
		for _, path := range powerShellMutatingPaths(name, segment) {
			if err := checkPowerShellWritePath(cwd, writableRoots, path, cfg); err != nil {
				return err
			}
		}
		i += len(segment)
	}
	return nil
}

func PreparePowerShell(command string, cfg tools.SandboxConfig, dangerouslyDisable bool) (ShellSpec, error) {
	if !cfg.Enabled {
		return defaultPowerShell(command)
	}
	if dangerouslyDisable && !cfg.AllowUnsandboxedCommands {
		return ShellSpec{}, fmt.Errorf("sandbox override requested but sandbox.allowUnsandboxedCommands is false")
	}
	if cfg.FailIfUnavailable || !cfg.AllowUnsandboxedCommands {
		return ShellSpec{}, fmt.Errorf("PowerShell OS sandbox is unavailable on %s; use Bash sandbox or allow unsandboxed PowerShell explicitly", platformName())
	}
	base, err := defaultPowerShell(command)
	if err != nil {
		return ShellSpec{}, err
	}
	base.Env = append(base.Env, "SANDBOX_RUNTIME=unsupported-powershell")
	base.Env = append(base.Env, tools.NetworkProxyEnv(cfg)...)
	return base, nil
}

func defaultPowerShell(command string) (ShellSpec, error) {
	candidates := []string{"pwsh", "powershell"}
	if runtime.GOOS == "windows" {
		candidates = []string{"pwsh.exe", "powershell.exe"}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return ShellSpec{
				Path: path,
				Args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command},
			}, nil
		}
	}
	return ShellSpec{}, fmt.Errorf("PowerShell executable not found: install pwsh or powershell")
}

func powerShellTokens(command string) []string {
	var tokens []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		case r == '|' || r == ';':
			flush()
			tokens = append(tokens, string(r))
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func isPowerShellRedirect(token string) bool {
	token = strings.TrimSpace(token)
	if token == ">" || token == ">>" {
		return true
	}
	return strings.HasSuffix(token, ">") && len(token) <= 3
}

func powerShellCommandName(token string) string {
	token = strings.Trim(strings.ToLower(strings.TrimSpace(token)), "&")
	if token == "" || strings.HasPrefix(token, "-") {
		return ""
	}
	return token
}

func powerShellSegment(tokens []string, start int) []string {
	var out []string
	for i := start; i < len(tokens); i++ {
		if tokens[i] == "|" || tokens[i] == ";" {
			break
		}
		out = append(out, tokens[i])
	}
	return out
}

func isMutatingPowerShellCommand(name string) bool {
	switch name {
	case "set-content", "add-content", "out-file", "remove-item", "new-item", "move-item", "copy-item", "rename-item",
		"clear-content", "mkdir", "rmdir", "rm", "del", "erase", "ni", "sc", "ac", "mi", "mv", "move", "cp", "copy", "ri":
		return true
	default:
		return false
	}
}

func powerShellMutatingPaths(name string, args []string) []string {
	explicit := powerShellExplicitPathArgs(args)
	if len(explicit) > 0 {
		return explicit
	}
	plain := powerShellPlainArgs(args)
	switch name {
	case "copy-item", "cp", "copy":
		if len(plain) > 0 {
			return []string{plain[len(plain)-1]}
		}
	case "move-item", "mv", "move", "rename-item", "mi":
		return plain
	default:
		return plain
	}
	return nil
}

func powerShellExplicitPathArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		key := strings.ToLower(strings.TrimSpace(args[i]))
		if !powerShellPathParameter(key) || i+1 >= len(args) {
			continue
		}
		out = append(out, args[i+1])
		i++
	}
	return out
}

func powerShellPathParameter(key string) bool {
	switch key {
	case "-path", "-literalpath", "-filepath", "-destination", "-target", "-name":
		return true
	default:
		return false
	}
}

func powerShellPlainArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func checkPowerShellWritePath(cwd string, writableRoots []string, path string, cfg tools.SandboxConfig) error {
	if strings.Contains(path, "$") || strings.Contains(path, "%") || strings.Contains(path, "*") || strings.Contains(path, "?") {
		return fmt.Errorf("dynamic path in mutating PowerShell command is not allowed")
	}
	resolved, err := tools.ResolvePath(cwd, path)
	if err != nil {
		return err
	}
	return tools.EnsureWritablePathWithSandbox(cwd, writableRoots, resolved, cfg)
}
