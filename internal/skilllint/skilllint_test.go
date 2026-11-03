package skilllint

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckPathsFindsCrossProcessAndVacuousScanRisks(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(currentFile), "testdata", "cross_process_root", "SKILL.md")
	report, err := CheckPaths([]string{fixture})
	if err != nil {
		t.Fatal(err)
	}
	assertFinding := func(rule Rule) {
		t.Helper()
		for _, finding := range report.Findings {
			if finding.Rule == rule {
				return
			}
		}
		t.Fatalf("missing %s finding: %+v", rule, report.Findings)
	}
	assertFinding(RuleShellCrossFenceVariable)
	assertFinding(RuleEmptyScanVacuousSuccess)
	assertFinding(RuleUnstableSkillRelativePath)
}

func TestCrossProcessFixtureReproducesVacuousZeroOfZero(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	_, currentFile, _, _ := runtime.Caller(0)
	fixtureDir := filepath.Join(filepath.Dir(currentFile), "testdata", "cross_process_root")
	source, err := os.ReadFile(filepath.Join(fixtureDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	fences := shellFences(source)
	if len(fences) < 2 {
		t.Fatalf("fixture shell fences = %d", len(fences))
	}
	isolated := exec.Command("bash", "-c", fences[1].body)
	isolated.Dir = fixtureDir
	output, err := isolated.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated vacuous check unexpectedly failed: %v\n%s", err, output)
	}
	if !strings.Contains(strings.Join(strings.Fields(string(output)), ""), "0/0") {
		t.Fatalf("isolated output = %q, want 0/0", output)
	}
	sameProcess := exec.Command("bash", "-c", fences[0].body+"\n"+fences[1].body)
	sameProcess.Dir = fixtureDir
	output, err = sameProcess.CombinedOutput()
	if err != nil {
		t.Fatalf("same-process fixture failed: %v\n%s", err, output)
	}
	if !strings.Contains(strings.Join(strings.Fields(string(output)), ""), "1/1") {
		t.Fatalf("same-process output = %q, want non-empty 1/1", output)
	}
}

func TestCheckPathsAcceptsSelfContainedStableSkillDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	body := []byte("```bash\nroot=\"${NIUMA_SKILL_DIR:?}\"\ntotal=$(find \"$root\" -name SKILL.md | wc -l)\ntest \"$total\" -gt 0\n```\n")
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
	report, err := CheckPaths([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("self-contained Skill produced findings: %+v", report.Findings)
	}
}

func TestCheckPathsRejectsInlineBashSourcePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("```bash\nroot=$(dirname \"${BASH_SOURCE[0]}\")\n```\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := CheckPaths([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Rule == RuleInlineBashSourcePath {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing inline BASH_SOURCE finding: %+v", report.Findings)
	}
}
