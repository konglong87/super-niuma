package toolpolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/gitpolicy"
)

func TestCheckAppliesGitAuthorizationOnlyToExecutedCommands(t *testing.T) {
	readOnly := []byte(`{"command":"rg -n 'git push' internal/query"}`)
	if decision := Check("Bash", readOnly, t.TempDir(), gitpolicy.Authorization{}); decision != nil {
		t.Fatalf("quoted example blocked: %+v", decision)
	}
	commit := []byte(`{"command":"git -C . commit -m x"}`)
	if decision := Check("Bash", commit, t.TempDir(), gitpolicy.Authorization{}); decision == nil || decision.RuleID != "shared_state_authorization" {
		t.Fatalf("unauthorized commit decision=%+v", decision)
	}
}

func TestCheckExplainsExactGitAuthorizationRecovery(t *testing.T) {
	prompt := `I authorize this exact command:

` + "```bash" + `
git commit --author="konglong87 <>" -m "<original message>"
git push origin master
` + "```"
	authorization := gitpolicy.ParseAuthorization(prompt)
	attempt := []byte(`{"command":"git commit --author=\"konglong87 <>\" -m \"docs: exact\""}`)
	decision := Check("Bash", attempt, t.TempDir(), authorization)
	if decision == nil || decision.RuleID != "shared_state_authorization" {
		t.Fatalf("placeholder scope mismatch decision=%+v", decision)
	}
	for _, want := range []string{
		"commit message does not match",
		"Do not retry",
		"Placeholders such as <message> are literal values, not wildcards",
		"Ask the user to authorize the exact intended command in a new message",
		"or tell the user to run it manually",
	} {
		if !strings.Contains(decision.Message, want) {
			t.Fatalf("decision message %q does not contain %q", decision.Message, want)
		}
	}

	exact := []byte(`{"command":"git commit --author=\"konglong87 <>\" -m \"<original message>\""}`)
	if decision := Check("Bash", exact, t.TempDir(), authorization); decision != nil {
		t.Fatalf("exact literal authorized command was blocked: %+v", decision)
	}
}

func TestCheckBlocksDuplicateNumberedDirectoryFromWriteAndMkdir(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"05-tools", "06-experts", "07-guides"} {
		if err := os.Mkdir(filepath.Join(cwd, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeInput, _ := json.Marshal(map[string]any{"file_path": filepath.Join(cwd, "06-career", "SKILL.md")})
	if decision := Check("Write", writeInput, cwd, gitpolicy.Authorization{}); decision == nil || decision.RuleID != "numbered_directory_collision" {
		t.Fatalf("write decision=%+v", decision)
	}
	mkdirInput, _ := json.Marshal(map[string]any{"command": "mkdir -p 06-career"})
	if decision := Check("Bash", mkdirInput, cwd, gitpolicy.Authorization{}); decision == nil || decision.RuleID != "numbered_directory_collision" {
		t.Fatalf("mkdir decision=%+v", decision)
	}
}

func TestCheckDoesNotImposeNumberingConventionWithoutCatalogEvidence(t *testing.T) {
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "06-existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"command": "mkdir -p 06-second"})
	if decision := Check("Bash", input, cwd, gitpolicy.Authorization{}); decision != nil {
		t.Fatalf("single numbered sibling is not enough to infer a catalog convention: %+v", decision)
	}
}

func TestCheckDoesNotImposeUniqueNumbersWhenExistingCatalogAllowsDuplicates(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"06-frontend", "06-backend", "07-shared"} {
		if err := os.Mkdir(filepath.Join(cwd, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	input, _ := json.Marshal(map[string]any{"command": "mkdir -p 06-mobile"})
	if decision := Check("Bash", input, cwd, gitpolicy.Authorization{}); decision != nil {
		t.Fatalf("existing duplicate prefixes disprove a unique sequence convention: %+v", decision)
	}
}
