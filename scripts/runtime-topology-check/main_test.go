package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHelpReturnsSuccess(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if status := run([]string{"--help"}, &stdout, &stderr); status != 0 {
		t.Fatalf("status = %d, stderr = %s", status, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestValidateTopologyRegistryRejectsUnmappedRuntimeFile(t *testing.T) {
	root, registry, files := topologyFixture(t)
	files = append(files, "internal/newruntime/new.go")
	problems := validateTopologyRegistry(root, defaultRegistryPath, registry, files)
	assertProblemContains(t, problems, "internal/newruntime/new.go")
}

func TestValidateTopologyRegistryRejectsStaleSymbolAnchor(t *testing.T) {
	root, registry, files := topologyFixture(t)
	registry.Anchors[0].Symbols = []string{"removedSymbol"}
	problems := validateTopologyRegistry(root, defaultRegistryPath, registry, files)
	assertProblemContains(t, problems, "removedSymbol")
}

func TestValidateTopologyRegistryRejectsNodeMissingFromDocument(t *testing.T) {
	root, registry, files := topologyFixture(t)
	writeTopologyFixtureFile(t, root, registry.Document, "global-runtime-topology.mmd\n")
	problems := validateTopologyRegistry(root, defaultRegistryPath, registry, files)
	assertProblemContains(t, problems, "does not mention node RT-PROMPT")
}

func TestValidateTopologyRegistryRejectsUnregisteredViewFile(t *testing.T) {
	root, registry, files := topologyFixture(t)
	files = append(files, "diagrams/orphan-topology.mmd")
	problems := validateTopologyRegistry(root, defaultRegistryPath, registry, files)
	assertProblemContains(t, problems, "orphan-topology.mmd")
}

func TestEvaluateTopologyImpactRequiresDeclarationForRuntimeChange(t *testing.T) {
	_, registry, _ := topologyFixture(t)
	report, problems := evaluateTopologyImpact(defaultRegistryPath, registry, []string{"internal/query/query.go"}, impactDeclaration{})
	if strings.Join(report.AffectedNodes, ",") != "RT-PROMPT" {
		t.Fatalf("affected nodes = %v", report.AffectedNodes)
	}
	assertProblemContains(t, problems, "Topology-Impact")
	assertProblemContains(t, problems, "Blast-Radius")
	assertProblemContains(t, problems, "Topology-Reason")
}

func TestEvaluateTopologyImpactAcceptsReasonedNoTopologyChange(t *testing.T) {
	_, registry, _ := topologyFixture(t)
	report, problems := evaluateTopologyImpact(defaultRegistryPath, registry, []string{"internal/query/query.go"}, impactDeclaration{
		Impact:      impactNone,
		BlastRadius: blastRadiusB1,
		Reason:      "implementation stays inside the existing prompt node",
	})
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if strings.Join(report.RecommendedTests, ",") != "go test ./internal/query" {
		t.Fatalf("recommended tests = %v", report.RecommendedTests)
	}
}

func TestEvaluateTopologyImpactRequiresAllRenderedArtifacts(t *testing.T) {
	_, registry, _ := topologyFixture(t)
	changed := []string{
		"internal/query/query.go",
		"diagrams/global-runtime-topology.mmd",
		"diagrams/global-runtime-topology.svg",
	}
	_, problems := evaluateTopologyImpact(defaultRegistryPath, registry, changed, impactDeclaration{
		Impact:      impactUpdated,
		BlastRadius: blastRadiusB2,
		Reason:      "the prompt node now has a new downstream consumer",
	})
	assertProblemContains(t, problems, "global-runtime-topology.png")
	assertProblemContains(t, problems, "global-runtime-topology.excalidraw")
}

func TestEvaluateTopologyImpactRejectsArtifactWithoutSource(t *testing.T) {
	_, registry, _ := topologyFixture(t)
	_, problems := evaluateTopologyImpact(defaultRegistryPath, registry, []string{
		"diagrams/global-runtime-topology.svg",
	}, impactDeclaration{})
	assertProblemContains(t, problems, "changed without its Mermaid source")
}

func TestEvaluateTopologyImpactRejectsUpdatedWithoutTopologySource(t *testing.T) {
	_, registry, _ := topologyFixture(t)
	_, problems := evaluateTopologyImpact(defaultRegistryPath, registry, []string{
		"internal/query/query.go",
	}, impactDeclaration{
		Impact:      impactUpdated,
		BlastRadius: blastRadiusB2,
		Reason:      "the prompt node now has a new downstream consumer",
	})
	assertProblemContains(t, problems, "no registry, document, or Mermaid source changed")
}

func TestEvaluateTopologyImpactAcceptsSynchronizedTopologyUpdate(t *testing.T) {
	_, registry, _ := topologyFixture(t)
	changed := []string{
		"internal/query/query.go",
		"diagrams/global-runtime-topology.mmd",
		"diagrams/global-runtime-topology.svg",
		"diagrams/global-runtime-topology.png",
		"diagrams/global-runtime-topology.excalidraw",
	}
	_, problems := evaluateTopologyImpact(defaultRegistryPath, registry, changed, impactDeclaration{
		Impact:      impactUpdated,
		BlastRadius: blastRadiusB2,
		Reason:      "the prompt node now has a new downstream consumer",
	})
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestParseImpactDeclarationReadsPRAndCommitFormats(t *testing.T) {
	declaration := parseImpactDeclaration(`
Topology impact: none
Blast-Radius: B3_GLOBAL_RUNTIME
Topology_Reason: shared prompt wording changed without adding a new edge
`)
	if declaration.Impact != impactNone || declaration.BlastRadius != blastRadiusB3 {
		t.Fatalf("declaration = %+v", declaration)
	}
	if !strings.Contains(declaration.Reason, "shared prompt") {
		t.Fatalf("reason = %q", declaration.Reason)
	}
}

func TestParseImpactDeclarationIgnoresPRTemplatePlaceholders(t *testing.T) {
	declaration := parseImpactDeclaration(`
Topology impact: <!-- updated | none -->
Blast radius: <!-- B0_LOCAL | B1_SCENARIO -->
Topology reason: <!-- explain the relationship -->
`)
	if !declaration.incomplete() {
		t.Fatalf("placeholder declaration = %+v", declaration)
	}
}

func TestDeclarationFromGitHubEventMarksPullRequestEvenWhenTemplateIsBlank(t *testing.T) {
	eventPath := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(eventPath, []byte(`{"pull_request":{"body":"Topology impact: <!-- updated | none -->"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_EVENT_PATH", eventPath)
	declaration, isPullRequest := declarationFromGitHubEvent()
	if !isPullRequest {
		t.Fatal("expected pull request event")
	}
	if !declaration.incomplete() {
		t.Fatalf("declaration = %+v", declaration)
	}
}

func TestGitWorkingTreeChangedFilesIncludesTrackedAndUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	runGitForTest(t, root, "init")
	runGitForTest(t, root, "config", "user.email", "topology@example.com")
	runGitForTest(t, root, "config", "user.name", "Topology Test")
	writeTopologyFixtureFile(t, root, "internal/query/query.go", "package query\n")
	runGitForTest(t, root, "add", "internal/query/query.go")
	runGitForTest(t, root, "commit", "-m", "initial")
	writeTopologyFixtureFile(t, root, "internal/query/query.go", "package query\n// changed\n")
	writeTopologyFixtureFile(t, root, "internal/newruntime/new.go", "package newruntime\n")

	changed, err := gitWorkingTreeChangedFiles(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(changed, ",")
	for _, want := range []string{"internal/newruntime/new.go", "internal/query/query.go"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("changed files %v do not contain %q", changed, want)
		}
	}
}

func topologyFixture(t *testing.T) (string, topologyRegistry, []string) {
	t.Helper()
	root := t.TempDir()
	files := []string{
		defaultRegistryPath,
		"docs/architecture/global_runtime_topology.md",
		"internal/query/query.go",
		"diagrams/global-runtime-topology.mmd",
		"diagrams/global-runtime-topology.svg",
		"diagrams/global-runtime-topology.png",
		"diagrams/global-runtime-topology.excalidraw",
	}
	contents := map[string]string{
		"docs/architecture/global_runtime_topology.md": "RT-PROMPT global-runtime-topology.mmd\n",
		"internal/query/query.go":                      "package query\nfunc defaultSystemPromptParts() {}\n",
	}
	for _, name := range files {
		content := contents[name]
		writeTopologyFixtureFile(t, root, name, content)
	}
	registry := topologyRegistry{
		Version:       supportedRegistryVersion,
		Document:      "docs/architecture/global_runtime_topology.md",
		CoverageRoots: []string{"internal/"},
		ViewRoots:     []string{"diagrams/"},
		Nodes: []topologyNode{{
			ID:      "RT-PROMPT",
			Summary: "prompt assembly",
			Paths:   []string{"internal/query/"},
			Tests:   []string{"go test ./internal/query"},
		}},
		Views: []topologyView{{
			ID:     "global-runtime",
			Source: "diagrams/global-runtime-topology.mmd",
			Artifacts: []string{
				"diagrams/global-runtime-topology.svg",
				"diagrams/global-runtime-topology.png",
				"diagrams/global-runtime-topology.excalidraw",
			},
			Covers: []string{"RT-PROMPT"},
		}},
		Anchors: []topologyAnchor{{
			Path:    "internal/query/query.go",
			Symbols: []string{"defaultSystemPromptParts"},
		}},
	}
	return root, registry, files
}

func writeTopologyFixtureFile(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertProblemContains(t *testing.T, problems []error, want string) {
	t.Helper()
	for _, problem := range problems {
		if strings.Contains(problem.Error(), want) {
			return
		}
	}
	t.Fatalf("problems %v do not contain %q", problems, want)
}

func runGitForTest(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
