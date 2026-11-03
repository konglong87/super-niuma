package powershell

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestPowerShellRejectsUnsafeWrite(t *testing.T) {
	other := t.TempDir()
	res := New().Run(context.Background(), mustJSON(map[string]any{
		"command": `Set-Content -Path ` + filepath.Join(other, "out.txt") + ` -Value hello`,
	}), tools.Context{CWD: t.TempDir()})
	if !res.IsError || !strings.Contains(res.Content, "outside the current workspace") {
		t.Fatalf("res = %+v", res)
	}
}

func TestPowerShellToolResultLimitMatchesClaudeCode(t *testing.T) {
	if got := New().MaxResultSizeChars(); got != 30_000 {
		t.Fatalf("MaxResultSizeChars() = %d, want 30000", got)
	}
}

func TestPowerShellDescriptionExplainsLargeOutputRecovery(t *testing.T) {
	text := New().Description()
	for _, want := range []string{
		"Output is limited to 200 KB",
		"rerun with narrower filters",
		"redirect output to a file",
		"Read offset/limit",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("description missing %q:\n%s", want, text)
		}
	}
}

func TestPowerShellRunsWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell executable unavailable")
		}
	}
	res := New().Run(context.Background(), mustJSON(map[string]any{
		"command": `Write-Output hello`,
	}), tools.Context{CWD: t.TempDir()})
	if res.IsError || !strings.Contains(res.Content, "hello") {
		t.Fatalf("res = %+v", res)
	}
}

func TestPowerShellSuccessEmptyOutputAnnotated(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("pwsh not available")
		}
	}
	res := New().Run(context.Background(), mustJSON(map[string]any{
		"command": `$null`,
	}), tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "no output") || !strings.Contains(res.Content, "exited successfully") {
		t.Fatalf("empty output not annotated: %q", res.Content)
	}
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

// TestCommandEnvStripsSecrets locks AUDIT-P1-16 for the PowerShell path. There is
// no pwsh on most CI images, so this asserts the environment Run hands to the
// child rather than spawning one.
func TestCommandEnvStripsSecrets(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "leaked-"+key)
	}
	t.Setenv("GOPATH", "/keep/gopath")

	env := commandEnv(nil)
	for _, item := range env {
		if strings.HasPrefix(item, "ANTHROPIC_API_KEY=") || strings.HasPrefix(item, "GITHUB_TOKEN=") ||
			strings.HasPrefix(item, "AWS_SECRET_ACCESS_KEY=") || strings.HasPrefix(item, "OPENAI_API_KEY=") {
			t.Errorf("PowerShell child env leaks %q", item)
		}
	}
	if !slices.Contains(env, "GOPATH=/keep/gopath") {
		t.Error("PowerShell child env dropped GOPATH")
	}
	if !slices.Contains(commandEnv([]string{"SPEC=1"}), "SPEC=1") {
		t.Error("sandbox spec env must still be applied")
	}
}

func TestCommandEnvIncludesSkillRuntimeDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "skill")
	env := commandEnv(tools.EnvironmentWithSkillRuntime(nil, &tools.SkillRuntime{
		Directory:        directory,
		FilesystemBacked: true,
	}))
	if !slices.Contains(env, tools.GolangCCSkillDirEnv+"="+directory) || !slices.Contains(env, tools.ClaudeSkillDirEnv+"="+directory) {
		t.Fatalf("PowerShell environment missing Skill directory aliases: %v", env)
	}
}

func TestPowerShellRejectsMalformedInput(t *testing.T) {
	res := New().Run(context.Background(), json.RawMessage(`{`), tools.Context{CWD: t.TempDir()})
	if !res.IsError || !strings.Contains(res.Content, "unexpected end") {
		t.Fatalf("res = %+v", res)
	}
}

func TestPowerShellRefusesUnsandboxedRunInStrictMode(t *testing.T) {
	res := New().Run(context.Background(), mustJSON(map[string]any{
		"command":                   "Write-Output hi",
		"dangerouslyDisableSandbox": true,
	}), tools.Context{CWD: t.TempDir(), Sandbox: tools.SandboxConfig{Enabled: true, AllowUnsandboxedCommands: false}})
	if !res.IsError {
		t.Fatalf("strict mode must refuse an unsandboxed run: %+v", res)
	}
}

func TestPowerShellReportsTimeout(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell executable unavailable")
		}
	}
	res := New().Run(context.Background(), mustJSON(map[string]any{
		"command":    "Start-Sleep -Seconds 5",
		"timeout_ms": 200,
	}), tools.Context{CWD: t.TempDir()})
	if !res.IsError || !strings.Contains(res.Content, "timed out after 200ms") {
		t.Fatalf("res = %+v", res)
	}
}

func TestLimitedBufferTruncatesAndAnnotates(t *testing.T) {
	buf := &limitedBuffer{limit: 10}
	if _, err := buf.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "0123456789") {
		t.Fatalf("kept = %q", got)
	}
	if !strings.Contains(got, "[output truncated after 10 bytes]") {
		t.Fatalf("missing truncation notice: %q", got)
	}

	// Writes arriving after the limit is already full still report success.
	if n, err := buf.Write([]byte("more")); n != 4 || err != nil {
		t.Fatalf("Write after full = (%d, %v)", n, err)
	}

	unlimited := &limitedBuffer{limit: 0}
	if _, err := unlimited.Write([]byte("dropped")); err != nil {
		t.Fatal(err)
	}
	if unlimited.String() != "" {
		t.Fatalf("limit 0 should discard, got %q", unlimited.String())
	}

	exact := &limitedBuffer{limit: 4}
	_, _ = exact.Write([]byte("abcd"))
	if exact.String() != "abcd" {
		t.Fatalf("exact fit = %q", exact.String())
	}
}
