package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/memory"
)

var errMemoryLintWriter = errors.New("writer failed")

type memoryLintErrorWriter struct{}

func (memoryLintErrorWriter) Write([]byte) (int, error) {
	return 0, errMemoryLintWriter
}

func TestMemoryLintCommandHumanAndJSON(t *testing.T) {
	project := isolatedMemoryLintProject(t)
	mustWriteMemoryLintFixture(t, filepath.Join(project, "CLAUDE.md"), "[missing](docs/missing.md)\n")

	var human bytes.Buffer
	err := Run(context.Background(), []string{"--cwd", project, "memory", "lint"}, &human, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "1 broken link") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(human.String(), "CLAUDE.md:1") || !strings.Contains(human.String(), "scope=workspace") {
		t.Fatalf("human output = %s", human.String())
	}

	var jsonOut bytes.Buffer
	err = Run(context.Background(), []string{"--cwd", project, "memory", "lint", "--json"}, &jsonOut, &bytes.Buffer{})
	if err == nil {
		t.Fatal("JSON finding must return an error")
	}
	var output memoryLintOutput
	if decodeErr := json.Unmarshal(jsonOut.Bytes(), &output); decodeErr != nil {
		t.Fatalf("invalid JSON %q: %v", jsonOut.String(), decodeErr)
	}
	if output.Scope != memory.LoadScopeWorkspace || output.Documents != 1 || len(output.Findings) != 1 {
		t.Fatalf("JSON output = %+v", output)
	}
}

func TestMemoryLintWorkspaceDoesNotReadGlobalSources(t *testing.T) {
	project := isolatedMemoryLintProject(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	mustWriteMemoryLintFixture(t, filepath.Join(project, "CLAUDE.md"), "[exists](exists.md)\n")
	mustWriteMemoryLintFixture(t, filepath.Join(project, "exists.md"), "ok\n")
	mustWriteMemoryLintFixture(t, filepath.Join(home, ".claude", "CLAUDE.md"), "[user missing](user-missing.md)\n")

	var workspace bytes.Buffer
	if err := Run(context.Background(), []string{"--cwd", project, "memory", "lint", "--json"}, &workspace, &bytes.Buffer{}); err != nil {
		t.Fatalf("workspace lint: %v\n%s", err, workspace.String())
	}
	var workspaceOutput memoryLintOutput
	if err := json.Unmarshal(workspace.Bytes(), &workspaceOutput); err != nil {
		t.Fatal(err)
	}
	if len(workspaceOutput.Findings) != 0 {
		t.Fatalf("workspace findings = %+v", workspaceOutput.Findings)
	}

	var all bytes.Buffer
	err = Run(context.Background(), []string{"--cwd", project, "memory", "lint", "--scope", "all", "--json"}, &all, &bytes.Buffer{})
	if err == nil {
		t.Fatal("all scope must include the user broken link")
	}
	var allOutput memoryLintOutput
	if decodeErr := json.Unmarshal(all.Bytes(), &allOutput); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if allOutput.Scope != memory.LoadScopeAll || len(allOutput.Findings) != 1 || allOutput.Findings[0].Target != "user-missing.md" {
		t.Fatalf("all output = %+v", allOutput)
	}
}

func TestMemoryLintCommandRejectsInvalidArguments(t *testing.T) {
	project := isolatedMemoryLintProject(t)
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"memory"}, want: "usage: memory lint"},
		{args: []string{"memory", "bogus"}, want: "unknown memory command"},
		{args: []string{"memory", "lint", "--bogus"}, want: "unknown flag"},
		{args: []string{"memory", "lint", "--scope", "invalid"}, want: "workspace or all"},
		{args: []string{"memory", "lint", "extra"}, want: "positional argument"},
		{args: []string{"memory", "lint", "--cwd", project}, want: "unknown flag: --cwd"},
		{args: []string{"memory", "lint", "--scope", "all", "--scope", "workspace"}, want: "only be specified once"},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		err := Run(context.Background(), append([]string{"--cwd", project}, tc.args...), &out, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args %v error = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestMemoryCommandSkipsOnlyItsStartupUpdate(t *testing.T) {
	memorySpec, ok := lookupCommand("memory")
	if !ok {
		t.Fatal("memory command not registered")
	}
	statusSpec, ok := lookupCommand("status")
	if !ok {
		t.Fatal("status command not registered")
	}
	for _, tc := range []struct {
		name     string
		haveSpec bool
		spec     commandSpec
		want     int
	}{
		{name: "memory", haveSpec: true, spec: memorySpec, want: 0},
		{name: "other command", haveSpec: true, spec: statusSpec, want: 1},
		{name: "interactive", haveSpec: false, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			runStartupUpdateForCommand(context.Background(), t.TempDir(), tc.haveSpec, tc.spec, func(context.Context, string) {
				calls++
			})
			if calls != tc.want {
				t.Fatalf("update calls = %d, want %d", calls, tc.want)
			}
		})
	}
}

func TestWriteMemoryLintHumanReturnsWriterError(t *testing.T) {
	err := writeMemoryLintHuman(memoryLintErrorWriter{}, memoryLintOutput{Scope: memory.LoadScopeWorkspace})
	if !errors.Is(err, errMemoryLintWriter) {
		t.Fatalf("error = %v", err)
	}
}

func isolatedMemoryLintProject(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config")
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_MANAGED_MEMORY", "")
	t.Setenv("CLAUDE_CODE_MANAGED_MEMORY", "")
	return project
}

func mustWriteMemoryLintFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
