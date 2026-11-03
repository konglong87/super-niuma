package bash

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestBashTool(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"command": "printf hello",
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if strings.TrimSpace(res.Content) != "hello" {
		t.Fatalf("content = %q, want hello", res.Content)
	}
}

func TestBashToolResultLimitMatchesClaudeCode(t *testing.T) {
	if got := New().MaxResultSizeChars(); got != 30_000 {
		t.Fatalf("MaxResultSizeChars() = %d, want 30000", got)
	}
}

func TestBashToolScrubsProviderEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "secret-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "secret-token")
	t.Setenv("ANTHROPIC_BASE_URL", "https://provider.example.test/v1")
	t.Setenv("CLAUDE_CODE_AUTH_TOKEN", "secret-claude-token")
	t.Setenv("CLAUDE_CODE_MODEL", "provider-model")
	t.Setenv("NIUMA_PROVIDER", "custom")
	input, _ := json.Marshal(map[string]any{
		"command": `if env | grep -E '^(ANTHROPIC_API_KEY|ANTHROPIC_AUTH_TOKEN|ANTHROPIC_BASE_URL|CLAUDE_CODE_AUTH_TOKEN|CLAUDE_CODE_MODEL|NIUMA_PROVIDER)='; then exit 7; fi; printf clean`,
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("Run() leaked provider env or failed: %s", res.Content)
	}
	if strings.TrimSpace(res.Content) != "clean" {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestCommandEnvUsesNonInteractiveGitDefaults(t *testing.T) {
	t.Setenv("GIT_EDITOR", "vim")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("ANTHROPIC_API_KEY", "secret-key")
	t.Setenv("GIT_SEQUENCE_EDITOR", "temporary-test-value")
	if err := os.Unsetenv("GIT_SEQUENCE_EDITOR"); err != nil {
		t.Fatal(err)
	}

	env := commandEnv(nil)
	if got := envValue(env, "GIT_EDITOR"); got != "true" {
		t.Fatalf("GIT_EDITOR = %q, want true", got)
	}
	if got := envValue(env, "GIT_TERMINAL_PROMPT"); got != "0" {
		t.Fatalf("GIT_TERMINAL_PROMPT = %q, want 0", got)
	}
	if got := envKeyCount(env, "GIT_SEQUENCE_EDITOR"); got != 0 {
		t.Fatalf("GIT_SEQUENCE_EDITOR entries = %d, want 0", got)
	}
	if got := envValue(env, "ANTHROPIC_API_KEY"); got != "" {
		t.Fatalf("provider secret must be filtered, got %q", got)
	}
}

func TestCommandEnvExtraOverridesDefaults(t *testing.T) {
	env := commandEnv([]string{"GIT_EDITOR=custom-editor"})
	if got := envValue(env, "GIT_EDITOR"); got != "custom-editor" {
		t.Fatalf("GIT_EDITOR = %q, want custom-editor", got)
	}
	if got := envKeyCount(env, "GIT_EDITOR"); got != 1 {
		t.Fatalf("GIT_EDITOR entries = %d, want 1", got)
	}
}

func TestBashSkillRuntimeEnvironmentIsAvailableInFreshProcesses(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "skill")
	active := &tools.SkillRuntime{Directory: directory, FilesystemBacked: true}
	input, _ := json.Marshal(map[string]any{
		"command": `printf '%s|%s|%s' "$NIUMA_SKILL_DIR" "$CLAUDE_SKILL_DIR" "${LOCAL_ONLY-unset}"`,
	})
	for i := 0; i < 2; i++ {
		res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir(), ActiveSkill: active})
		if res.IsError {
			t.Fatalf("fresh Bash call %d failed: %s", i, res.Content)
		}
		want := directory + "|" + directory + "|unset"
		if res.Content != want {
			t.Fatalf("fresh Bash call %d = %q, want %q", i, res.Content, want)
		}
	}
}

func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, ok := strings.Cut(env[i], "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

func envKeyCount(env []string, key string) int {
	count := 0
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if ok && name == key {
			count++
		}
	}
	return count
}

func TestBashToolSchemaIncludesCompatibleBackgroundFields(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(New().InputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"timeout_ms", "timeout", "run_in_background", "verification", "dangerouslyDisableSandbox"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("schema missing %s: %s", field, New().InputSchema())
		}
	}
	desc := New().Description()
	for _, phrase := range []string{
		"run_in_background=true",
		"Do not add '&'",
		"timeout_ms or timeout",
		"120s by default",
		"Prefer other tools over Bash",
		"Do not use Bash for cat/head/tail",
		"Repository-wide read-only audits are an exception",
		"git status/log/tag",
		"find/wc",
		"read-only rg pipelines",
		"Repair/release verification is also an exception",
		"git status --short --branch",
		"git diff --summary",
		"git ls-remote --tags",
		"Quote paths that contain spaces",
		"multiple Bash tool calls",
		"Do not use sleep as a substitute",
		"destructive git commands or skip hooks/signing",
		"rerun with narrower filters",
		"redirect output to a file",
		"Read offset/limit",
	} {
		if !strings.Contains(desc, phrase) {
			t.Fatalf("description missing %q:\n%s", phrase, desc)
		}
	}
	if !strings.Contains(string(schema.Properties["timeout_ms"]), "Default 120000") {
		t.Fatalf("timeout_ms schema does not advertise 120000ms default: %s", schema.Properties["timeout_ms"])
	}
}

func TestBashVerificationMetadataPreservesExpectedBaselineFailure(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"command": "test 0 -gt 0",
		"verification": map[string]any{
			"probe_id":    "nonempty-scan",
			"phase":       "baseline",
			"targets":     []string{"skills/check/SKILL.md"},
			"expect_exit": "nonzero",
		},
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if !res.IsError {
		t.Fatal("real nonzero exit must remain an error")
	}
	if res.Verification == nil || !res.Verification.MatchedExpectation {
		t.Fatalf("expected matched baseline evidence: %+v", res.Verification)
	}
}

func TestBashVerificationMetadataRejectsBackgroundProbe(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"command":           "true",
		"run_in_background": true,
		"verification": map[string]any{
			"probe_id":    "probe",
			"phase":       "post_change",
			"targets":     []string{"file"},
			"expect_exit": "zero",
		},
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if !res.IsError || !strings.Contains(res.Content, "foreground") {
		t.Fatalf("background verification result = %+v", res)
	}
}

func TestBashToolTimeoutAlias(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"command": "sleep 1",
		"timeout": 10,
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
	if !res.IsError || !strings.Contains(res.Content, "command timed out after 10ms") {
		t.Fatalf("result = %+v", res)
	}
}

func TestBashToolRunInBackgroundPersistsJobAndLogs(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	cwd := t.TempDir()
	skillDirectory := filepath.Join(cwd, "skill")
	input, _ := json.Marshal(map[string]any{
		"command":           `printf 'GIT_EDITOR=%s\nGIT_TERMINAL_PROMPT=%s\nSKILL_DIR=%s\n' "$GIT_EDITOR" "$GIT_TERMINAL_PROMPT" "$NIUMA_SKILL_DIR"; printf background-start; sleep 0.05; printf background-done`,
		"description":       "background test command",
		"run_in_background": true,
		"timeout":           3000,
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: cwd, ActiveSkill: &tools.SkillRuntime{Directory: skillDirectory, FilesystemBacked: true}})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	var payload struct {
		Background   bool   `json:"background"`
		ID           string `json:"id"`
		Status       string `json:"status"`
		LogPath      string `json:"log_path"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(res.Content), &payload); err != nil {
		t.Fatalf("content is not background JSON: %s err=%v", res.Content, err)
	}
	if !payload.Background || payload.ID == "" || payload.Status != "running" || payload.LogPath == "" || !strings.Contains(payload.Instructions, "/logs "+payload.ID) {
		t.Fatalf("payload = %+v", payload)
	}
	// The background payload is where the model learns how to follow the
	// command it just started. Pointing it at the log file instead of
	// BashOutput/KillShell is what left AUDIT-P1-13 open in practice.
	for _, want := range []string{"BashOutput", "KillShell", "bash_id=" + payload.ID} {
		if !strings.Contains(payload.Instructions, want) {
			t.Fatalf("instructions do not mention %q: %q", want, payload.Instructions)
		}
	}
	store := background.DefaultStore()
	var job background.Job
	var logs string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		found, ok, err := store.Find(payload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("background job %s not found", payload.ID)
		}
		job = found
		logs, _, err = store.Logs(payload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" && strings.Contains(logs, "background-done") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Kind != "bash" || job.CWD != cwd || job.Prompt != "background test command" || job.PID == 0 || job.StartedAt == nil {
		t.Fatalf("job = %+v", job)
	}
	if job.Status != "completed" || !strings.Contains(logs, "background-start") || !strings.Contains(logs, "background-done") {
		t.Fatalf("status=%s logs=%q", job.Status, logs)
	}
	if !strings.Contains(logs, "GIT_EDITOR=true\n") || !strings.Contains(logs, "GIT_TERMINAL_PROMPT=0\n") {
		t.Fatalf("background command did not inherit non-interactive Git environment: %q", logs)
	}
	if !strings.Contains(logs, "SKILL_DIR="+skillDirectory+"\n") {
		t.Fatalf("background command did not inherit the stable Skill directory: %q", logs)
	}
}

func TestBashToolContinuesRebaseWithoutInteractiveEditor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	gitConfigDir := t.TempDir()
	hooksPath := filepath.Join(gitConfigDir, "hooks")
	if err := os.MkdirAll(hooksPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksPath, "pre-commit"), []byte("#!/bin/sh\nexit 93\n"), 0700); err != nil {
		t.Fatal(err)
	}
	globalConfig := filepath.Join(gitConfigDir, "gitconfig")
	globalConfigBody := "[commit]\n\tgpgSign = true\n[gpg]\n\tprogram = false\n[core]\n\thooksPath = " + hooksPath + "\n"
	if err := os.WriteFile(globalConfig, []byte(globalConfigBody), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	if systemConfig := os.Getenv("GIT_CONFIG_SYSTEM"); systemConfig != "" {
		t.Logf("ignoring inherited GIT_CONFIG_SYSTEM=%s", systemConfig)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	remoteClone := filepath.Join(root, "remote-clone")
	localClone := filepath.Join(root, "local-clone")
	runGitFixture(t, root, "init", "--bare", "--initial-branch=main", remote)
	runGitFixture(t, root, "clone", remote, remoteClone)
	configureGitFixture(t, remoteClone)
	conflictPath := filepath.Join(remoteClone, "conflict.txt")
	if err := os.WriteFile(conflictPath, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, remoteClone, "add", "conflict.txt")
	runGitFixture(t, remoteClone, "commit", "-m", "base commit")
	runGitFixture(t, remoteClone, "push", "-u", "origin", "main")

	runGitFixture(t, root, "clone", remote, localClone)
	configureGitFixture(t, localClone)
	if err := os.WriteFile(filepath.Join(localClone, "conflict.txt"), []byte("local change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, localClone, "add", "conflict.txt")
	runGitFixture(t, localClone, "commit", "-m", "preserve local subject")

	if err := os.WriteFile(conflictPath, []byte("remote change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, remoteClone, "add", "conflict.txt")
	runGitFixture(t, remoteClone, "commit", "-m", "remote change")
	runGitFixture(t, remoteClone, "push")

	pull := runBashTool(t, localClone, "git pull --rebase", 5000)
	if !pull.IsError || !strings.Contains(pull.Content, "CONFLICT") {
		t.Fatalf("git pull --rebase result = %+v, want conflict", pull)
	}
	if err := os.WriteFile(filepath.Join(localClone, "conflict.txt"), []byte("resolved change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := runBashTool(t, localClone, "git add conflict.txt", 5000); result.IsError {
		t.Fatalf("git add result = %+v", result)
	}

	t.Setenv("GIT_EDITOR", "vim")
	continued := runBashTool(t, localClone, "git rebase --continue", 5000)
	if continued.IsError {
		t.Fatalf("git rebase --continue result = %+v", continued)
	}
	for _, stateDir := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(localClone, ".git", stateDir)); !os.IsNotExist(err) {
			t.Fatalf("rebase state %s remains: %v", stateDir, err)
		}
	}
	if got := strings.TrimSpace(runGitFixture(t, localClone, "log", "-1", "--format=%s")); got != "preserve local subject" {
		t.Fatalf("rebased commit subject = %q", got)
	}
}

func runBashTool(t *testing.T, cwd, command string, timeoutMS int) tools.Result {
	t.Helper()
	input, err := json.Marshal(map[string]any{
		"command":    command,
		"timeout_ms": timeoutMS,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New().Run(context.Background(), input, tools.Context{CWD: cwd})
}

func configureGitFixture(t *testing.T, cwd string) {
	t.Helper()
	hooksPath := filepath.Join(cwd, ".git", "test-hooks")
	if err := os.MkdirAll(hooksPath, 0755); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, cwd, "config", "user.name", "Bash Tool Test")
	runGitFixture(t, cwd, "config", "user.email", "bash-tool-test@example.invalid")
	runGitFixture(t, cwd, "config", "commit.gpgSign", "false")
	runGitFixture(t, cwd, "config", "core.hooksPath", hooksPath)
}

func runGitFixture(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func TestBashToolRejectsDangerousCommand(t *testing.T) {
	for _, command := range []string{
		"git reset --hard",
		"git clean -xdf",
		"sudo rm -rf /",
		"rm -rf -- /",
		`rm -rf "$HOME"`,
		"chown -R me /",
		"dd if=/tmp/image of=/dev/disk9",
		": > /dev/disk9",
		"chmod -R 777 .",
	} {
		input, _ := json.Marshal(map[string]any{
			"command": command,
		})
		res := New().Run(context.Background(), input, tools.Context{CWD: t.TempDir()})
		if !res.IsError || !strings.Contains(res.Content, "refusing") {
			t.Fatalf("%s result = %+v", command, res)
		}
	}
}

func TestDangerousCommandOverrideEnvironmentMigration(t *testing.T) {
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "1")
	if decision := dangerousCommandDecision("git reset --hard"); decision.Reason != "" {
		t.Fatalf("canonical override was ignored: %q", decision.Reason)
	}

	// Foreign product namespaces must not enable Niuma's destructive override.
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "")
	t.Setenv("GOLANG_CC_ALLOW_DESTRUCTIVE", "1")
	if decision := dangerousCommandDecision("git reset --hard"); decision.Reason == "" {
		t.Fatal("foreign product override unexpectedly enabled Niuma")
	}
}

// NIUMA_ALLOW_DESTRUCTIVE=1 disables the only refusal layer that survives
// --dangerously-skip-permissions, and it used to do so silently: the override
// short-circuited ahead of the classification, so nothing recorded which
// protection had been waived or on what command.
func TestDangerousCommandOverrideIsAudited(t *testing.T) {
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "1")

	var audits []tools.PermissionAudit
	input, _ := json.Marshal(map[string]any{"command": "git reset --hard"})
	res := New().Run(context.Background(), input, tools.Context{
		CWD:             t.TempDir(),
		PermissionAudit: func(audit tools.PermissionAudit) { audits = append(audits, audit) },
	})
	if res.IsError && strings.Contains(res.Content, "refusing") {
		t.Fatalf("override did not take effect: %+v", res)
	}
	if len(audits) != 1 {
		t.Fatalf("audits = %+v, want exactly one override record", audits)
	}
	audit := audits[0]
	if !audit.Allowed {
		t.Errorf("Allowed = false; the command is about to run, so the record must say so")
	}
	if audit.Rule != destructiveOverrideRule {
		t.Errorf("Rule = %q, want %q", audit.Rule, destructiveOverrideRule)
	}
	if audit.Source != allowDestructiveEnv {
		t.Errorf("Source = %q, want the switch responsible %q", audit.Source, allowDestructiveEnv)
	}
	if !strings.Contains(audit.Reason, "git reset --hard") && !strings.Contains(audit.Reason, "refusing git reset --hard") {
		t.Errorf("Reason = %q, want it to name the waived refusal", audit.Reason)
	}
	if audit.Request != "git reset --hard" {
		t.Errorf("Request = %q, want the command", audit.Request)
	}
}

// A command the rule table never objected to must not produce an override record,
// even while the override is set — otherwise every ordinary Bash call would look
// like a waived protection.
func TestDangerousCommandOverrideDoesNotAuditOrdinaryCommands(t *testing.T) {
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "1")

	var audits []tools.PermissionAudit
	input, _ := json.Marshal(map[string]any{"command": "echo hi"})
	New().Run(context.Background(), input, tools.Context{
		CWD:             t.TempDir(),
		PermissionAudit: func(audit tools.PermissionAudit) { audits = append(audits, audit) },
	})
	for _, audit := range audits {
		if audit.Rule == destructiveOverrideRule {
			t.Fatalf("ordinary command recorded as a waived protection: %+v", audit)
		}
	}
}

// Without the override the command is still refused, and refusing it is not an
// override event.
func TestDangerousCommandWithoutOverrideStillRefusesAndDoesNotAudit(t *testing.T) {
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "")
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "")
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "")
	t.Setenv("NIUMA_ALLOW_DESTRUCTIVE", "")

	var audits []tools.PermissionAudit
	input, _ := json.Marshal(map[string]any{"command": "git reset --hard"})
	res := New().Run(context.Background(), input, tools.Context{
		CWD:             t.TempDir(),
		PermissionAudit: func(audit tools.PermissionAudit) { audits = append(audits, audit) },
	})
	if !res.IsError || !strings.Contains(res.Content, "refusing") {
		t.Fatalf("result = %+v, want refusal", res)
	}
	if !strings.Contains(res.Content, allowDestructiveEnv) {
		t.Errorf("refusal should still name the override switch: %q", res.Content)
	}
	for _, audit := range audits {
		if audit.Rule == destructiveOverrideRule {
			t.Fatalf("refusal recorded as an override: %+v", audit)
		}
	}
}

func TestBashToolRejectsWritesOutsideWorkspace(t *testing.T) {
	tmp := t.TempDir()
	other := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"command": "printf hello > " + filepath.Join(other, "out.txt"),
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError || !strings.Contains(res.Content, "outside the current workspace") {
		t.Fatalf("result = %+v", res)
	}
}

func TestBashToolAllowsAdditionalWritableDirectory(t *testing.T) {
	tmp := t.TempDir()
	other := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"command": "printf hello > " + filepath.Join(other, "out.txt"),
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp, WritableRoots: []string{other}})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
}

func TestBashToolOSSandboxBlocksRuntimeWriteEscape(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skipf("awk unavailable: %v", err)
	}
	cwd := t.TempDir()
	other := t.TempDir()
	target := filepath.Join(other, "escaped.txt")
	input, _ := json.Marshal(map[string]any{
		"command": `awk 'BEGIN { print "owned" > "` + target + `" }'`,
	})
	res := New().Run(context.Background(), input, tools.Context{
		CWD: cwd,
		Sandbox: tools.SandboxConfig{
			Enabled:           true,
			FailIfUnavailable: true,
		},
	})
	if !res.IsError {
		t.Fatalf("result = %+v, want sandbox error", res)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target stat err = %v, want not exist", err)
	}
}

func TestBashToolOSSandboxAllowsWorkspaceWrite(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	cwd := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"command": "printf hello > out.txt",
	})
	res := New().Run(context.Background(), input, tools.Context{
		CWD: cwd,
		Sandbox: tools.SandboxConfig{
			Enabled:           true,
			FailIfUnavailable: true,
		},
	})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "out.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("data = %q err = %v", data, err)
	}
}

func TestBashToolLinuxOSSandboxBlocksRuntimeWriteEscape(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux bwrap only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap unavailable: %v", err)
	}
	requireWorkingBwrap(t)
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skipf("awk unavailable: %v", err)
	}
	cwd := t.TempDir()
	other := t.TempDir()
	target := filepath.Join(other, "escaped.txt")
	input, _ := json.Marshal(map[string]any{
		"command": `awk 'BEGIN { print "owned" > "` + target + `" }'`,
	})
	res := New().Run(context.Background(), input, tools.Context{
		CWD: cwd,
		Sandbox: tools.SandboxConfig{
			Enabled:           true,
			FailIfUnavailable: true,
		},
	})
	if !res.IsError {
		t.Fatalf("result = %+v, want sandbox error", res)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target stat err = %v, want not exist", err)
	}
}

func TestBashToolLinuxOSSandboxAllowsWorkspaceWrite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux bwrap only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap unavailable: %v", err)
	}
	requireWorkingBwrap(t)
	cwd := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"command": "printf hello > out.txt",
	})
	res := New().Run(context.Background(), input, tools.Context{
		CWD: cwd,
		Sandbox: tools.SandboxConfig{
			Enabled:           true,
			FailIfUnavailable: true,
		},
	})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "out.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("data = %q err = %v", data, err)
	}
}

func TestBashToolLinuxSeccompBlocksHighRiskSyscall(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux bwrap only")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap unavailable: %v", err)
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skipf("unshare unavailable: %v", err)
	}
	requireWorkingBwrap(t)
	cwd := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"command": "unshare -U true",
	})
	res := New().Run(context.Background(), input, tools.Context{
		CWD: cwd,
		Sandbox: tools.SandboxConfig{
			Enabled:           true,
			FailIfUnavailable: true,
			SeccompEnabled:    true,
		},
	})
	if !res.IsError {
		t.Fatalf("result = %+v, want seccomp/bwrap error", res)
	}
}

func TestBashToolShellNetworkPolicyBlocksBypass(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"command": "curl -k https://example.com",
	})
	res := New().Run(context.Background(), input, tools.Context{
		CWD: t.TempDir(),
		Sandbox: tools.SandboxConfig{
			NetworkMITMRequired: true,
			NetworkMITMCAFile:   "/tmp/ca.pem",
		},
	})
	if !res.IsError || !strings.Contains(res.Content, "TLS verification") {
		t.Fatalf("result = %+v", res)
	}
}

func TestLimitedBufferTruncates(t *testing.T) {
	var b limitedBuffer
	b.limit = 5
	if n, err := b.Write([]byte("hello world")); err != nil || n != len("hello world") {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got := b.String(); !strings.Contains(got, "hello") || !strings.Contains(got, "output truncated") {
		t.Fatalf("buffer = %q", got)
	}
}

func requireWorkingBwrap(t *testing.T) {
	t.Helper()
	cmd := exec.Command("bwrap", "--ro-bind", "/", "/", "--dev", "/dev", "--unshare-pid", "--proc", "/proc", "--", "/bin/sh", "-c", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bwrap cannot run in this environment: %v\n%s", err, out)
	}
}

func TestBashSuccessEmptyOutputAnnotated(t *testing.T) {
	in, _ := json.Marshal(map[string]any{"command": "true"})
	res := New().Run(context.Background(), in, tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "no output") || !strings.Contains(res.Content, "exited successfully") {
		t.Fatalf("empty output not annotated: %q", res.Content)
	}
}

// The Bash description is where the model decides how to follow a background
// command before it ever sees a result payload; leaving it pointing at
// log_path/sleep keeps AUDIT-P1-13's polling problem alive.
func TestBashDescriptionPointsAtBashOutputForBackgroundPolling(t *testing.T) {
	description := New().Description()
	for _, want := range []string{"BashOutput", "KillShell"} {
		if !strings.Contains(description, want) {
			t.Fatalf("Bash description does not mention %q", want)
		}
	}
}
