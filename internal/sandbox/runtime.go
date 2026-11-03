package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/konglong87/super-niuma/internal/tools"
)

type ShellSpec struct {
	Path       string
	Args       []string
	Env        []string
	ExtraFiles []*os.File
	Cleanup    func() error
	Sandboxed  bool
}

func (s ShellSpec) Close() error {
	var errs []error
	for _, file := range s.ExtraFiles {
		if file != nil {
			errs = append(errs, file.Close())
		}
	}
	if s.Cleanup != nil {
		errs = append(errs, s.Cleanup())
	}
	return errors.Join(errs...)
}

func PrepareShell(cwd string, writableRoots []string, command string, cfg tools.SandboxConfig, dangerouslyDisable bool) (ShellSpec, error) {
	base := defaultShell(command)
	if !cfg.Enabled {
		return base, nil
	}
	if dangerouslyDisable {
		if cfg.AllowUnsandboxedCommands {
			return base, nil
		}
		return ShellSpec{}, fmt.Errorf("sandbox override requested but sandbox.allowUnsandboxedCommands is false")
	}
	if isExcludedCommand(command, cfg.ExcludedCommands) {
		return base, nil
	}
	if !platformEnabled(cfg.EnabledPlatforms) {
		if cfg.FailIfUnavailable {
			return ShellSpec{}, fmt.Errorf("sandbox.enabled is set but %s is not in sandbox.enabledPlatforms", platformName())
		}
		return base, nil
	}
	if cfg.FailIfUnavailable {
		if gaps := NetworkPolicyGaps(cfg); len(gaps) > 0 {
			return ShellSpec{}, fmt.Errorf("sandbox.failIfUnavailable is set but a configured network policy cannot be enforced: %s", strings.Join(gaps, "; "))
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return prepareMacOSShell(cwd, writableRoots, command, cfg, base)
	case "linux":
		return prepareLinuxShell(cwd, writableRoots, command, cfg, base)
	default:
		if cfg.FailIfUnavailable {
			return ShellSpec{}, fmt.Errorf("sandbox.enabled is set but OS sandboxing is unavailable on %s in this build", platformName())
		}
		return base, nil
	}
}

func prepareMacOSShell(cwd string, writableRoots []string, command string, cfg tools.SandboxConfig, fallback ShellSpec) (ShellSpec, error) {
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		if cfg.FailIfUnavailable {
			return ShellSpec{}, fmt.Errorf("sandbox.enabled is set but sandbox-exec is unavailable: %w", err)
		}
		return fallback, nil
	}
	shell, shellArg := posixShell()
	profile, err := macOSSandboxProfile(cwd, writableRoots, cfg)
	if err != nil {
		return ShellSpec{}, err
	}
	if err := os.MkdirAll("/tmp/claude", 0700); err != nil {
		return ShellSpec{}, err
	}
	if err := os.MkdirAll("/private/tmp/claude", 0700); err != nil && !os.IsExist(err) {
		return ShellSpec{}, err
	}
	return ShellSpec{
		Path:      sandboxExec,
		Args:      []string{"-p", profile, shell, shellArg, command},
		Env:       []string{"SANDBOX_RUNTIME=1", "TMPDIR=/tmp/claude"},
		Sandboxed: true,
	}, nil
}

func prepareLinuxShell(cwd string, writableRoots []string, command string, cfg tools.SandboxConfig, fallback ShellSpec) (ShellSpec, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		if cfg.FailIfUnavailable {
			return ShellSpec{}, fmt.Errorf("sandbox.enabled is set but bubblewrap (bwrap) is unavailable: %w", err)
		}
		return fallback, nil
	}
	spec, err := linuxSandboxSpec(bwrap, cwd, writableRoots, command, cfg)
	if err != nil {
		return ShellSpec{}, err
	}
	return spec, nil
}

func linuxSandboxSpec(bwrap, cwd string, writableRoots []string, command string, cfg tools.SandboxConfig) (ShellSpec, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return ShellSpec{}, err
	}
	cwd = resolveSandboxPath(cwd, ".")
	if err := os.MkdirAll("/tmp/claude", 0700); err != nil {
		return ShellSpec{}, err
	}
	allowWrite := append(defaultWritePaths(), cwd)
	allowWrite = append(allowWrite, writableRoots...)
	allowWrite = append(allowWrite, cfg.FilesystemAllowWrite...)
	denyWrite := append(defaultDenyWritePaths(cwd), cfg.FilesystemDenyWrite...)

	args := []string{
		"--new-session",
		"--die-with-parent",
		"--ro-bind", "/", "/",
		"--setenv", "SANDBOX_RUNTIME", "1",
		"--setenv", "TMPDIR", "/tmp/claude",
	}
	args = append(args, linuxWritableBindArgs(cwd, allowWrite)...)
	args = append(args, linuxDenyWriteArgs(cwd, allowWrite, denyWrite)...)
	args = append(args, linuxDenyReadArgs(cwd, cfg.FilesystemDenyRead, cfg.FilesystemAllowRead)...)
	args = append(args, linuxDenyUnixSocketArgs(cwd, cfg.UnixSocketDeny)...)
	args = append(args, "--dev", "/dev")
	args = append(args, "--unshare-pid")
	if cfg.NetworkDisabled {
		args = append(args, "--unshare-net")
	}
	if !cfg.EnableWeakerNestedSandbox {
		args = append(args, "--proc", "/proc")
	}
	var extraFiles []*os.File
	var cleanup func() error
	if cfg.SeccompEnabled {
		profile, remove, err := createSeccompProfileFile()
		if err != nil {
			return ShellSpec{}, err
		}
		extraFiles = append(extraFiles, profile)
		cleanup = remove
		args = append(args, "--seccomp", fmt.Sprint(3+len(extraFiles)-1))
	}
	args = append(args, "--chdir", cwd)
	shell, shellArg := posixShell()
	args = append(args, "--", shell, shellArg, command)
	return ShellSpec{
		Path:       bwrap,
		Args:       args,
		Env:        linuxSandboxEnv(cfg),
		ExtraFiles: extraFiles,
		Cleanup:    cleanup,
		Sandboxed:  true,
	}, nil
}

func linuxSandboxEnv(cfg tools.SandboxConfig) []string {
	env := []string{"SANDBOX_RUNTIME=1", "TMPDIR=/tmp/claude"}
	if cfg.NetworkDisabled {
		env = append(env, "SANDBOX_NETWORK=disabled")
	}
	env = append(env, tools.NetworkProxyEnv(cfg)...)
	if cfg.SeccompEnabled {
		mode := strings.TrimSpace(cfg.SeccompMode)
		if mode == "" {
			mode = "bwrap-seccomp-bpf"
		}
		env = append(env, "SANDBOX_SECCOMP="+mode)
	}
	return env
}

func linuxWritableBindArgs(cwd string, paths []string) []string {
	var args []string
	seen := map[string]bool{}
	for _, path := range paths {
		resolved := resolveSandboxPath(cwd, path)
		if resolved == "" || seen[resolved] || strings.HasPrefix(resolved, "/dev/") {
			continue
		}
		if _, err := os.Stat(resolved); err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(resolved); err == nil && !sameCleanPath(real, resolved) && !pathWithin(real, filepath.Dir(resolved)) {
			continue
		}
		args = append(args, "--bind", resolved, resolved)
		seen[resolved] = true
	}
	return args
}

func linuxDenyWriteArgs(cwd string, allowWrite []string, denyWrite []string) []string {
	allowed := resolvedExistingPaths(cwd, allowWrite)
	var args []string
	seen := map[string]bool{}
	for _, path := range denyWrite {
		resolved := resolveSandboxPath(cwd, path)
		if resolved == "" || seen[resolved] || strings.HasPrefix(resolved, "/dev/") || !withinAny(resolved, allowed) {
			continue
		}
		if _, err := os.Stat(resolved); err == nil {
			args = append(args, "--ro-bind", resolved, resolved)
			seen[resolved] = true
			continue
		}
		parent := filepath.Dir(resolved)
		if _, err := os.Stat(parent); err == nil && withinAny(parent, allowed) {
			args = append(args, "--ro-bind", "/dev/null", resolved)
			seen[resolved] = true
		}
	}
	return args
}

func linuxDenyUnixSocketArgs(cwd string, configured []string) []string {
	paths := append([]string{
		"/var/run/docker.sock",
		"/run/docker.sock",
		os.Getenv("SSH_AUTH_SOCK"),
		os.Getenv("GPG_AGENT_INFO"),
	}, configured...)
	var args []string
	seen := map[string]bool{}
	for _, path := range paths {
		resolved := resolveSandboxPath(cwd, path)
		if resolved == "" || seen[resolved] {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			continue
		}
		args = append(args, "--ro-bind", "/dev/null", resolved)
		seen[resolved] = true
	}
	return args
}

func linuxDenyReadArgs(cwd string, denyRead []string, allowRead []string) []string {
	var args []string
	for _, path := range denyRead {
		resolved := resolveSandboxPath(cwd, path)
		if resolved == "" {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		if info.IsDir() {
			args = append(args, "--tmpfs", resolved)
			for _, allow := range allowRead {
				allowPath := resolveSandboxPath(cwd, allow)
				if allowPath == "" || !pathWithin(allowPath, resolved) {
					continue
				}
				if _, err := os.Stat(allowPath); err == nil {
					args = append(args, "--ro-bind", allowPath, allowPath)
				}
			}
		} else {
			args = append(args, "--ro-bind", "/dev/null", resolved)
		}
	}
	return args
}

func resolvedExistingPaths(cwd string, paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		resolved := resolveSandboxPath(cwd, path)
		if resolved == "" || seen[resolved] {
			continue
		}
		if _, err := os.Stat(resolved); err == nil {
			out = append(out, resolved)
			seen[resolved] = true
		}
	}
	return out
}

func withinAny(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithin(path, root) {
			return true
		}
	}
	return false
}

func pathWithin(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sameCleanPath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func macOSSandboxProfile(cwd string, writableRoots []string, cfg tools.SandboxConfig) (string, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	allowWrite := append(defaultWritePaths(), cwd)
	allowWrite = append(allowWrite, writableRoots...)
	allowWrite = append(allowWrite, cfg.FilesystemAllowWrite...)
	denyWrite := append(defaultDenyWritePaths(cwd), cfg.FilesystemDenyWrite...)
	denyRead := append([]string(nil), cfg.FilesystemDenyRead...)
	allowRead := append([]string(nil), cfg.FilesystemAllowRead...)

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n\n")
	if len(denyRead) > 0 {
		b.WriteString("; File read\n")
		for _, path := range denyRead {
			writeRule(&b, "deny", "file-read*", path, cwd)
		}
		for _, path := range allowRead {
			writeRule(&b, "allow", "file-read*", path, cwd)
		}
		b.WriteString("(allow file-read-metadata (vnode-type DIRECTORY))\n\n")
	}
	if cfg.NetworkDisabled {
		// Verified against sandbox-exec: `(deny network*)` turns an outbound TCP
		// connect into EPERM while leaving local execution untouched. Without it
		// the darwin profile carried no network primitive at all and
		// sandbox.network.disabled was enforced only by a command-name allowlist
		// that any compiled binary or `python3 -c` walked straight past.
		b.WriteString("; Network\n")
		b.WriteString("(deny network*)\n\n")
	}
	b.WriteString("; File write\n")
	b.WriteString("(deny file-write* (subpath \"/\"))\n")
	for _, path := range allowWrite {
		writeRule(&b, "allow", "file-write*", path, cwd)
	}
	if cfg.AllowPty {
		b.WriteString("(allow pseudo-tty)\n")
		b.WriteString("(allow file-read* file-write* (literal \"/dev/ptmx\") (regex #\"^/dev/ttys\"))\n")
	}
	for _, path := range denyWrite {
		writeRule(&b, "deny", "file-write*", path, cwd)
	}
	return b.String(), nil
}

func writeRule(b *strings.Builder, action, operation, path, cwd string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	resolved := resolveSandboxPath(cwd, path)
	b.WriteString("(")
	b.WriteString(action)
	b.WriteString(" ")
	b.WriteString(operation)
	b.WriteString(" (subpath ")
	b.WriteString(quoteProfileString(resolved))
	b.WriteString("))\n")
	if strings.HasPrefix(resolved, "/private/var/") {
		b.WriteString("(")
		b.WriteString(action)
		b.WriteString(" ")
		b.WriteString(operation)
		b.WriteString(" (subpath ")
		b.WriteString(quoteProfileString(strings.TrimPrefix(resolved, "/private")))
		b.WriteString("))\n")
	} else if strings.HasPrefix(resolved, "/var/") {
		b.WriteString("(")
		b.WriteString(action)
		b.WriteString(" ")
		b.WriteString(operation)
		b.WriteString(" (subpath ")
		b.WriteString(quoteProfileString("/private" + resolved))
		b.WriteString("))\n")
	}
}

func resolveSandboxPath(cwd, path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return path
}

func quoteProfileString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func defaultWritePaths() []string {
	paths := []string{
		"/dev/stdout",
		"/dev/stderr",
		"/dev/null",
		"/dev/tty",
		"/dev/dtracehelper",
		"/dev/autofs_nowait",
		"/tmp/claude",
		"/private/tmp/claude",
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".npm/_logs"), filepath.Join(home, ".claude/debug"))
	}
	return paths
}

// defaultDenyWritePaths mirrors the tool-layer write protection into the OS
// sandbox profile. It reads the single source in internal/tools rather than
// keeping a local copy — the local copy had drifted to .claude/ only. The
// sandbox-only set is included unconditionally here because this function is
// reached only while building a real sandbox profile.
func defaultDenyWritePaths(cwd string) []string {
	patterns := append(tools.DefaultDenyWritePaths(), tools.SandboxOnlyDenyWritePaths()...)
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		out = append(out, filepath.Join(cwd, pattern))
	}
	return out
}

func platformEnabled(enabled []string) bool {
	return hostPlatform().enabledByConfig(enabled)
}

func platformName() string {
	return hostPlatform().name()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func isExcludedCommand(command string, patterns []string) bool {
	command = strings.TrimSpace(command)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if pattern == command {
			return true
		}
		if strings.HasSuffix(pattern, ":*") && strings.HasPrefix(command, strings.TrimSuffix(pattern, ":*")) {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(command, strings.TrimSuffix(pattern, "*")) {
			return true
		}
		if ok, _ := filepath.Match(pattern, command); ok {
			return true
		}
	}
	return false
}

func defaultShell(command string) ShellSpec {
	shell, shellArg := posixShell()
	if runtime.GOOS == "windows" {
		shell, shellArg = "cmd.exe", "/C"
	}
	return ShellSpec{Path: shell, Args: []string{shellArg, command}}
}

func posixShell() (string, string) {
	return "/bin/sh", "-c"
}
