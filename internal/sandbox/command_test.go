package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestCheckShellCommandAllowsWorkspaceWrites(t *testing.T) {
	cwd := t.TempDir()
	for _, command := range []string{
		"echo hello > note.txt",
		"mkdir -p nested/path",
		"touch ./nested/file.txt",
		"cp source.txt nested/copy.txt",
	} {
		if err := CheckShellCommand(cwd, nil, command, tools.SandboxConfig{}); err != nil {
			t.Fatalf("%s err = %v", command, err)
		}
	}
}

func TestCheckShellCommandRejectsWritesOutsideWorkspace(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	for _, command := range []string{
		"echo hello > " + filepath.Join(other, "note.txt"),
		"echo hello >> " + filepath.Join(other, "note.txt"),
		"touch " + filepath.Join(other, "file.txt"),
		"mv local.txt " + filepath.Join(other, "file.txt"),
		"ln -s " + filepath.Join(other, "dir") + " linked",
		"sudo tee " + filepath.Join(other, "file.txt"),
		"sed -i 's/a/b/' " + filepath.Join(other, "file.txt"),
		"install local.txt " + filepath.Join(other, "file.txt"),
	} {
		err := CheckShellCommand(cwd, nil, command, tools.SandboxConfig{})
		if err == nil || !strings.Contains(err.Error(), "outside the current workspace") {
			t.Fatalf("%s err = %v", command, err)
		}
	}
}

func TestCheckShellCommandAllowsAdditionalDirectory(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	command := "echo hello > " + filepath.Join(other, "note.txt")
	if err := CheckShellCommand(cwd, []string{other}, command, tools.SandboxConfig{}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckShellCommandRejectsSymlinkWriteEscape(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	link := filepath.Join(cwd, "linked")
	if err := os.Symlink(other, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := CheckShellCommand(cwd, nil, "printf hello > linked/out.txt", tools.SandboxConfig{})
	if err == nil || !strings.Contains(err.Error(), "outside the current workspace") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckShellCommandRejectsNestedShellWriteEscape(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	for _, command := range []string{
		"sh -c 'printf hello > " + filepath.Join(other, "out.txt") + "'",
		"bash -lc 'touch " + filepath.Join(other, "out.txt") + "'",
		"env FOO=bar sh -c 'mkdir -p " + filepath.Join(other, "nested") + "'",
	} {
		err := CheckShellCommand(cwd, nil, command, tools.SandboxConfig{})
		if err == nil || !strings.Contains(err.Error(), "outside the current workspace") {
			t.Fatalf("%s err = %v", command, err)
		}
	}
}

func TestCheckShellCommandAllowsNestedShellWorkspaceWrite(t *testing.T) {
	cwd := t.TempDir()
	if err := CheckShellCommand(cwd, nil, "sh -c 'printf hello > nested.txt'", tools.SandboxConfig{}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckShellCommandRejectsDynamicMutatingPath(t *testing.T) {
	cwd := t.TempDir()
	if err := CheckShellCommand(cwd, nil, "rm $TARGET", tools.SandboxConfig{}); err == nil {
		t.Fatal("expected dynamic mutating path to be rejected")
	}
	if err := CheckShellCommand(cwd, nil, "echo hi > $TARGET", tools.SandboxConfig{}); err == nil {
		t.Fatal("expected dynamic redirect path to be rejected")
	}
}

func TestCheckShellCommandAllowsNullDeviceRedirects(t *testing.T) {
	cwd := t.TempDir()
	for _, command := range []string{
		`find . -name "*.md" 2>/dev/null | wc -l`,
		`printf hello >/dev/null`,
		`printf hello >> /dev/null`,
		`sh -c 'find . -name "*.go" 2>/dev/null | wc -l'`,
	} {
		if err := CheckShellCommand(cwd, nil, command, tools.SandboxConfig{}); err != nil {
			t.Fatalf("%s err = %v", command, err)
		}
	}
}

// Bash and PowerShell used to reach tools.EnsureWritablePath, which enforces the
// workspace boundary but not the default write protection, so a shell command
// could rewrite the settings file that decides its own permission mode, or drop a
// git hook, while Write/Edit refused the same path.
func TestCheckShellCommandDeniesDefaultProtectedPaths(t *testing.T) {
	cwd := t.TempDir()
	for _, rel := range []string{
		".niuma/settings.json",
		".niuma/settings.local.json",
		".niuma-legacy/settings.json",
		".claude/settings.json",
		".claude/settings.local.json",
		".git/hooks/pre-commit",
		".git/config",
	} {
		// Both the redirect path and the mutating-command path must be covered:
		// they reach the writable-path check through different AST branches.
		for _, command := range []string{
			"echo pwned > " + rel,
			"echo pwned >> " + rel,
			"cp /etc/hosts " + rel,
			"sh -c 'echo pwned > " + rel + "'",
		} {
			if err := CheckShellCommand(cwd, nil, command, tools.SandboxConfig{}); err == nil {
				t.Fatalf("%q was allowed, want denied by default write protection", command)
			}
		}
	}
}

func TestCheckPowerShellCommandDeniesDefaultProtectedPaths(t *testing.T) {
	cwd := t.TempDir()
	for _, command := range []string{
		`Set-Content -Path .niuma/settings.json -Value pwned`,
		`Out-File -FilePath .claude/settings.json`,
		`Remove-Item -Path .git/config`,
	} {
		if err := CheckPowerShellCommand(cwd, nil, command, tools.SandboxConfig{}); err == nil {
			t.Fatalf("%q was allowed, want denied by default write protection", command)
		}
	}
}

// .claude/skills is deliberately writable while the sandbox is off so skill
// authoring works in the default configuration (see sandboxOnlyDenyWritePaths).
// Tightening the shell path must not take that with it.
func TestCheckShellCommandKeepsSkillsWritableWhileSandboxOff(t *testing.T) {
	cwd := t.TempDir()
	command := "echo body > .claude/skills/demo/SKILL.md"
	if err := CheckShellCommand(cwd, nil, command, tools.SandboxConfig{}); err != nil {
		t.Fatalf("sandbox off: %q err = %v, want allowed", command, err)
	}
	if err := CheckShellCommand(cwd, nil, command, tools.SandboxConfig{Enabled: true}); err == nil {
		t.Fatalf("sandbox on: %q was allowed, want denied", command)
	}
}

// Documents a known limit of the deny mechanism rather than asserting a fix:
// matchDenyWritePath resolves each pattern against cwd only, so a protected
// filename inside an additionalDirectory is not protected. This is a property of
// the shared tool-layer check — Write/Edit behave identically — not of the shell
// path, so it is out of scope here. Pinned so that widening the resolution to
// every writable root stays a deliberate decision with its own review.
func TestCheckShellCommandDenyPatternsResolveAgainstCWDOnly(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	inCWD := "echo pwned > .niuma/settings.json"
	if err := CheckShellCommand(cwd, []string{other}, inCWD, tools.SandboxConfig{}); err == nil {
		t.Fatalf("%q was allowed, want denied", inCWD)
	}
	inOther := "echo pwned > " + filepath.Join(other, ".niuma", "settings.json")
	if err := CheckShellCommand(cwd, []string{other}, inOther, tools.SandboxConfig{}); err != nil {
		t.Fatalf("%q err = %v; deny patterns are cwd-relative, so this is expected to pass today", inOther, err)
	}
}

func TestCheckShellNetworkPolicy(t *testing.T) {
	cases := []struct {
		name    string
		command string
		cfg     tools.SandboxConfig
		want    string
	}{
		{
			name:    "disabled",
			command: "curl https://example.com",
			cfg:     tools.SandboxConfig{NetworkDisabled: true},
			want:    "network.disabled",
		},
		{
			name:    "raw proxy bypass",
			command: "ssh example.com",
			cfg:     tools.SandboxConfig{NetworkProxyRequired: true, NetworkProxyURL: "http://127.0.0.1:8080"},
			want:    "proxy.required",
		},
		{
			name:    "curl proxy bypass",
			command: "curl --noproxy '*' https://example.com",
			cfg:     tools.SandboxConfig{NetworkProxyRequired: true, NetworkProxyURL: "http://127.0.0.1:8080"},
			want:    "disables proxy",
		},
		{
			name:    "tls bypass",
			command: "curl -k https://example.com",
			cfg:     tools.SandboxConfig{NetworkMITMRequired: true, NetworkMITMCAFile: "/tmp/ca.pem"},
			want:    "TLS verification",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckShellNetworkPolicy(tc.command, tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := CheckShellNetworkPolicy("curl https://example.com", tools.SandboxConfig{NetworkProxyRequired: true, NetworkProxyURL: "http://127.0.0.1:8080"}); err != nil {
		t.Fatalf("proxy-aware curl should be allowed: %v", err)
	}
}

func TestPrepareShellDisabled(t *testing.T) {
	spec, err := PrepareShell(t.TempDir(), nil, "printf hello", tools.SandboxConfig{}, false)
	if err != nil {
		t.Fatalf("PrepareShell() err = %v", err)
	}
	if spec.Sandboxed {
		t.Fatalf("sandboxed = true, want false")
	}
}

func TestPrepareShellMacOSUsesSandboxExec(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	spec, err := PrepareShell(t.TempDir(), nil, "printf hello", tools.SandboxConfig{Enabled: true, FailIfUnavailable: true}, false)
	if err != nil {
		t.Fatalf("PrepareShell() err = %v", err)
	}
	if !spec.Sandboxed || filepath.Base(spec.Path) != "sandbox-exec" {
		t.Fatalf("spec = %+v", spec)
	}
	if !strings.Contains(strings.Join(spec.Args, "\n"), "file-write") {
		t.Fatalf("profile args do not include write rules: %+v", spec.Args)
	}
}

func TestLinuxSandboxSpecUsesBubblewrapFilesystemIsolation(t *testing.T) {
	cwd := t.TempDir()
	additional := t.TempDir()
	denyRead := filepath.Join(cwd, "private")
	if err := os.MkdirAll(denyRead, 0755); err != nil {
		t.Fatal(err)
	}
	denyWrite := filepath.Join(cwd, ".git", "hooks")
	if err := os.MkdirAll(denyWrite, 0755); err != nil {
		t.Fatal(err)
	}
	spec, err := linuxSandboxSpec("/usr/bin/bwrap", cwd, []string{additional}, "printf hello", tools.SandboxConfig{
		Enabled:              true,
		FilesystemDenyRead:   []string{"private"},
		FilesystemDenyWrite:  []string{".git/hooks"},
		FilesystemAllowWrite: []string{"."},
	})
	if err != nil {
		t.Fatalf("linuxSandboxSpec() err = %v", err)
	}
	if !spec.Sandboxed || spec.Path != "/usr/bin/bwrap" {
		t.Fatalf("spec = %+v", spec)
	}
	cwd = resolveSandboxPath(cwd, ".")
	additional = resolveSandboxPath(cwd, additional)
	denyRead = resolveSandboxPath(cwd, "private")
	denyWrite = resolveSandboxPath(cwd, ".git/hooks")
	args := strings.Join(spec.Args, "\x00")
	for _, want := range []string{
		"--ro-bind\x00/\x00/",
		"--bind\x00" + cwd + "\x00" + cwd,
		"--bind\x00" + additional + "\x00" + additional,
		"--ro-bind\x00" + denyWrite + "\x00" + denyWrite,
		"--tmpfs\x00" + denyRead,
		"--unshare-pid",
		"--proc\x00/proc",
		"--chdir\x00" + cwd,
		"--\x00/bin/sh\x00-c\x00printf hello",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q in %+v", want, spec.Args)
		}
	}
}

func TestLinuxSandboxSpecWeakerNestedSandboxOmitsProc(t *testing.T) {
	spec, err := linuxSandboxSpec("/usr/bin/bwrap", t.TempDir(), nil, "printf hello", tools.SandboxConfig{
		Enabled:                   true,
		EnableWeakerNestedSandbox: true,
	})
	if err != nil {
		t.Fatalf("linuxSandboxSpec() err = %v", err)
	}
	if strings.Contains(strings.Join(spec.Args, "\x00"), "--proc\x00/proc") {
		t.Fatalf("args unexpectedly mount proc: %+v", spec.Args)
	}
}

func TestLinuxSandboxSpecNetworkAndSocketIsolation(t *testing.T) {
	cwd := t.TempDir()
	socketPath := filepath.Join(cwd, "docker.sock")
	if err := os.WriteFile(socketPath, []byte("socket-placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	spec, err := linuxSandboxSpec("/usr/bin/bwrap", cwd, nil, "printf hello", tools.SandboxConfig{
		Enabled:         true,
		NetworkDisabled: true,
		UnixSocketDeny:  []string{"docker.sock"},
		SeccompEnabled:  true,
		SeccompMode:     "bwrap",
	})
	if err != nil {
		t.Fatalf("linuxSandboxSpec() err = %v", err)
	}
	defer spec.Close()
	socketPath = resolveSandboxPath(cwd, "docker.sock")
	args := strings.Join(spec.Args, "\x00")
	for _, want := range []string{
		"--unshare-net",
		"--ro-bind\x00/dev/null\x00" + socketPath,
		"--seccomp\x003",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q in %+v", want, spec.Args)
		}
	}
	if len(spec.ExtraFiles) != 1 || spec.ExtraFiles[0] == nil {
		t.Fatalf("seccomp extra files = %+v", spec.ExtraFiles)
	}
	data, err := os.ReadFile(spec.ExtraFiles[0].Name())
	if err != nil {
		t.Fatalf("read seccomp profile: %v", err)
	}
	if len(data) == 0 || len(data)%8 != 0 {
		t.Fatalf("seccomp profile size = %d, want non-empty sock_filter array", len(data))
	}
	env := strings.Join(spec.Env, "\x00")
	for _, want := range []string{"SANDBOX_NETWORK=disabled", "SANDBOX_SECCOMP=bwrap"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q in %+v", want, spec.Env)
		}
	}
}

func TestSeccompBPFProgramContainsDenyRules(t *testing.T) {
	program, err := seccompBPFProgram("amd64")
	if err != nil {
		t.Fatalf("seccompBPFProgram() err = %v", err)
	}
	if len(program) < 8*8 || len(program)%8 != 0 {
		t.Fatalf("program size = %d", len(program))
	}
	if !containsLittleEndianUint32(program, 101) || !containsLittleEndianUint32(program, 321) {
		t.Fatalf("program does not include ptrace/bpf deny syscall numbers")
	}
}

func TestLinuxSandboxEnvIncludesNetworkProxyAndMITM(t *testing.T) {
	env := linuxSandboxEnv(tools.SandboxConfig{
		NetworkProxyURL:     "http://127.0.0.1:8080",
		NetworkProxyMode:    "required",
		NetworkMITMCAFile:   "/tmp/ca.pem",
		NetworkMITMRequired: true,
	})
	joined := strings.Join(env, "\x00")
	for _, want := range []string{
		"HTTPS_PROXY=http://127.0.0.1:8080",
		"ALL_PROXY=http://127.0.0.1:8080",
		"SSL_CERT_FILE=/tmp/ca.pem",
		"SANDBOX_NETWORK_MITM=required",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q in %+v", want, env)
		}
	}
}

func containsLittleEndianUint32(data []byte, value uint32) bool {
	want := []byte{byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24)}
	return strings.Contains(string(data), string(want))
}

func TestPrepareShellRejectsDisallowedOverride(t *testing.T) {
	_, err := PrepareShell(t.TempDir(), nil, "printf hello", tools.SandboxConfig{Enabled: true, AllowUnsandboxedCommands: false}, true)
	if err == nil || !strings.Contains(err.Error(), "allowUnsandboxedCommands") {
		t.Fatalf("err = %v", err)
	}
}
