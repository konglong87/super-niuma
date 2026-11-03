package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/session"
)

func TestLoadCodeScopeZeroValueMatchesAll(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configDir := filepath.Join(home, ".config")
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user instructions")
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "project instructions")
	mustWrite(t, filepath.Join(configDir, "team", "CLAUDE.md"), "team instructions")

	zero, err := LoadCodeWithOptions(project, "", LoadCodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	all, err := LoadCodeWithOptions(project, "", LoadCodeOptions{Scope: LoadScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zero, all) {
		t.Fatalf("zero-value scope differs from all:\nzero=%+v\nall=%+v", zero, all)
	}
	loaded, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, all) {
		t.Fatalf("Load differs from all scope:\nload=%+v\nall=%+v", loaded, all)
	}
}

func TestLoadForScopeWorkspaceSkipsGlobalSources(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configDir := filepath.Join(home, ".config")
	managedDir := filepath.Join(home, "managed-is-a-directory")
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NIUMA_MANAGED_MEMORY", managedDir)
	if err := os.MkdirAll(managedDir, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user instructions")
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "project instructions")
	mustWrite(t, filepath.Join(project, "CLAUDE.local.md"), "local instructions")
	mustWrite(t, filepath.Join(project, "WORKFLOW.md"), "workflow instructions")
	mustWrite(t, filepath.Join(configDir, "team", "CLAUDE.md"), "team instructions")
	mustWrite(t, filepath.Join(configDir, "memory", "auto.md"), "auto instructions")
	mustWrite(t, filepath.Join(ClaudeCodeProjectMemoryDir(project), "MEMORY.md"), "project memory")

	docs, err := LoadForScope(project, LoadScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	for _, want := range []string{"project instructions", "local instructions", "workflow instructions", "project memory"} {
		if !strings.Contains(got, want) {
			t.Fatalf("workspace scope missing %q:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"user instructions", "team instructions", "auto instructions"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("workspace scope included %q:\n%s", notWant, got)
		}
	}
	if _, err := LoadForScope(project, LoadScopeAll); err == nil {
		t.Fatal("all scope must attempt to read the invalid managed source")
	}
}

func TestLoadForScopeWorkspaceKeepsParentAndRejectsExternalProjectIncludes(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	configDir := filepath.Join(root, "config")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "parent project instructions")
	mustWrite(t, filepath.Join(root, "shared.md"), "external include instructions")
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "project instructions\n@../shared.md")

	docs, err := LoadForScope(project, LoadScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	for _, want := range []string{"parent project instructions", "project instructions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("workspace runtime scope missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "external include instructions") {
		t.Fatalf("workspace runtime scope loaded an external project include:\n%s", got)
	}
}

func TestLoadForScopeWorkspaceKeepsProjectSourceWhenUserPathMatches(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, ".claude")
	configDir := filepath.Join(home, ".config")
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "shared path instructions")

	docs, err := LoadForScope(project, LoadScopeWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, doc := range docs {
		if doc.Path == filepath.Join(project, "CLAUDE.md") {
			found = true
			if doc.Type != "Project" {
				t.Fatalf("shared path type = %q, want Project", doc.Type)
			}
		}
	}
	if !found {
		t.Fatalf("workspace scope did not load shared project path: %+v", docs)
	}
}

func TestLoadForScopeRejectsUnknownScope(t *testing.T) {
	_, err := LoadForScope(t.TempDir(), LoadScope("invalid"))
	if err == nil || !strings.Contains(err.Error(), "unknown memory load scope") {
		t.Fatalf("error = %v, want unknown scope error", err)
	}
}

func TestSystemAddendumLoadsUserAndProjectClaude(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	child := filepath.Join(project, "child")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	mustWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user instructions")
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "project instructions")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	addendum := SystemAddendum(child)
	if !strings.Contains(addendum, "user instructions") || !strings.Contains(addendum, "project instructions") {
		t.Fatalf("addendum = %s", addendum)
	}
	if !strings.Contains(addendum, "# Memory") || !strings.Contains(addendum, "durable user/project guidance") {
		t.Fatalf("addendum missing framing = %s", addendum)
	}
}

func TestClaudeCodeProjectMemoryDirUsesNativeProjectSlug(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	got := ClaudeCodeProjectMemoryDir(project)
	want := filepath.Join(home, ".claude", "projects", claudeCodeNativeProjectSlug(project), "memory")
	if got != want {
		t.Fatalf("memory dir = %q, want %q", got, want)
	}
}

func TestLoadCodeLoadsRulesLocalIncludesAndPathScopedDocuments(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	child := filepath.Join(project, "service", "api")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	mustWrite(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user instructions")
	mustWrite(t, filepath.Join(home, ".claude", "team", "CLAUDE.md"), "team instructions")
	mustWrite(t, filepath.Join(home, ".claude", "memory", "auto.md"), "auto memory")
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "project root\n@docs/included.md\n@../outside.md")
	mustWrite(t, filepath.Join(project, "docs", "included.md"), "included project instructions")
	mustWrite(t, filepath.Join(project, ".claude", "CLAUDE.md"), "project dot claude")
	mustWrite(t, filepath.Join(project, ".claude", "rules", "go.md"), "go rule")
	mustWrite(t, filepath.Join(project, "CLAUDE.local.md"), "local private rule")
	mustWrite(t, filepath.Join(project, "SKILL.md"), "skill workflow")
	mustWrite(t, filepath.Join(project, "WORKFLOW.md"), "root workflow")
	mustWrite(t, filepath.Join(project, "CONTRACT.md"), "root contract")
	mustWrite(t, filepath.Join(project, "references", "README.md"), "references workflow")
	mustWrite(t, filepath.Join(project, "references", "WORKFLOW.md"), "references contract workflow")
	mustWrite(t, filepath.Join(project, ".claude", "workflows", "docs.md"), "---\npaths: [service/api/**]\n---\nscoped workflow")
	mustWrite(t, filepath.Join(project, "service", "CLAUDE.md"), "---\npaths: [service/api/**]\n---\nservice scoped rule")
	mustWrite(t, filepath.Join(project, "service", "api", "CLAUDE.md"), "---\npaths: [web/**]\n---\nwrong scoped rule")
	mustWrite(t, filepath.Join(project, "service", "api", ".claude", "rules", "exclude.md"), "---\npaths:\n  - service/api/**\nexcludes:\n  - service/api/generated/**\n---\nexclude-aware rule")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}

	docs, err := LoadCode(child, "service/api/handler.go")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	for _, want := range []string{
		"user instructions",
		"team instructions",
		"auto memory",
		"project root",
		"included project instructions",
		"project dot claude",
		"go rule",
		"local private rule",
		"skill workflow",
		"root workflow",
		"root contract",
		"references workflow",
		"references contract workflow",
		"scoped workflow",
		"service scoped rule",
		"exclude-aware rule",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in loaded docs:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"wrong scoped rule", "outside file contents", "agents workflow"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("unexpected %q in loaded docs:\n%s", notWant, got)
		}
	}
	var workflowDocs int
	for _, doc := range docs {
		if IsWorkflowDocument(doc) {
			workflowDocs++
		}
	}
	if workflowDocs != 6 {
		t.Fatalf("workflow docs = %d, docs = %+v", workflowDocs, docs)
	}
}

func TestLoadCodeUsesAgentsAsSameDirectoryFallback(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, "AGENTS.md"), "agents fallback")

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "agents fallback") {
		t.Fatalf("AGENTS.md fallback not loaded:\n%s", got)
	}
	if len(docs) != 1 || docs[0].Type != "Workflow" {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestLoadCodeCanDisableAgentsFallback(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, "AGENTS.md"), "agents fallback")

	docs, err := LoadCodeWithOptions(project, "", LoadCodeOptions{DisableProjectAgentsFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(documentContents(docs), "\n"); strings.Contains(got, "agents fallback") {
		t.Fatalf("AGENTS.md fallback loaded despite disabled option:\n%s", got)
	}
	if len(docs) != 0 {
		t.Fatalf("docs = %+v, want no guidance docs", docs)
	}
}

func TestLoadCodeStripsWorkflowSkillsSystem(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, "AGENTS.md"), strings.Join([]string{
		"before rule",
		`<skills_system priority="1">`,
		"## Available Skills",
		"<skill><name>huge-skill</name></skill>",
		"</skills_system>",
		"after rule",
	}, "\n"))

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "before rule") || !strings.Contains(got, "after rule") {
		t.Fatalf("workflow rules were not preserved:\n%s", got)
	}
	for _, notWant := range []string{"skills_system", "Available Skills", "huge-skill"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("workflow skills block leaked %q:\n%s", notWant, got)
		}
	}
}

func TestPreparePromptDocumentsBudgetsLargeWorkflow(t *testing.T) {
	t.Setenv("NIUMA_WORKFLOW_PROMPT_BUDGET_BYTES", "700")
	doc := Document{
		Path: filepath.Join(t.TempDir(), "AGENTS.md"),
		Type: "Workflow",
		Content: strings.Join([]string{
			"# Project Rules",
			"## Testing",
			"- MUST run tests before submit.",
			"- " + strings.Repeat("filler ", 300),
			"## Security",
			"- 不要把 secrets 写入日志。",
		}, "\n"),
	}

	promptDocs, report := PreparePromptDocuments([]Document{doc})
	if len(promptDocs) != 1 || report.BudgetedDocuments != 1 {
		t.Fatalf("promptDocs=%+v report=%+v", promptDocs, report)
	}
	got := promptDocs[0].Content
	for _, want := range []string{"Large workflow guidance has been budgeted", "Full file:", "## Section index", "## Testing", "MUST run tests", "不要把 secrets"} {
		if !strings.Contains(got, want) {
			t.Fatalf("budgeted workflow missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "filler filler filler") {
		t.Fatalf("budgeted workflow kept filler:\n%s", got)
	}
	if len(got) > 700 {
		t.Fatalf("budgeted bytes = %d, want <= 700\n%s", len(got), got)
	}
	if report.OriginalBytes <= report.PromptBytes || report.Documents[0].PromptBytes != len(got) {
		t.Fatalf("report = %+v got=%d", report, len(got))
	}
}

func TestLoadCodePrefersSameDirectoryClaudeOverAgents(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "claude guidance")
	mustWrite(t, filepath.Join(project, "AGENTS.md"), "agents fallback")

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "claude guidance") {
		t.Fatalf("CLAUDE.md not loaded:\n%s", got)
	}
	if strings.Contains(got, "agents fallback") {
		t.Fatalf("AGENTS.md loaded despite same-directory CLAUDE.md:\n%s", got)
	}
	if len(docs) != 1 || docs[0].Type != "Project" {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestLoadCodePrefersNiumaGuidanceOverClaudeAndAgents(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, "niuma.md"), "niuma guidance")
	mustWrite(t, filepath.Join(project, "CLAUDE.md"), "legacy guidance")
	mustWrite(t, filepath.Join(project, "AGENTS.md"), "agents fallback")

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "niuma guidance") {
		t.Fatalf("niuma.md not loaded:\n%s", got)
	}
	for _, notWant := range []string{"legacy guidance", "agents fallback"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("unexpected %q with niuma.md present:\n%s", notWant, got)
		}
	}
	if len(docs) != 1 || docs[0].Type != "Project" || filepath.Base(docs[0].Path) != "niuma.md" {
		t.Fatalf("docs = %+v", docs)
	}
}

func TestLoadCodeIgnoresRemovedProductGuidanceFilenames(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, "golang-cc.md"), "removed previous guidance")
	mustWrite(t, filepath.Join(project, "go-claude.md"), "removed legacy guidance")
	mustWrite(t, filepath.Join(project, "AGENTS.md"), "agents fallback")

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if strings.Contains(got, "removed previous guidance") || strings.Contains(got, "removed legacy guidance") {
		t.Fatalf("removed product guidance filename was loaded:\n%s", got)
	}
	if !strings.Contains(got, "agents fallback") {
		t.Fatalf("AGENTS.md fallback was not loaded:\n%s", got)
	}
}

func TestLoadCodeUsesConfiguredGuidanceFilename(t *testing.T) {
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, ".niuma-legacy"))
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".niuma-legacy", "settings.json"), `{
	  "identity": {"guidanceFilename": "agentx.md"}
	}`)
	mustWrite(t, filepath.Join(project, "agentx.md"), "agentx guidance")
	mustWrite(t, filepath.Join(project, "niuma.md"), "default guidance")

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "agentx guidance") || strings.Contains(got, "default guidance") {
		t.Fatalf("configured guidance was not preferred:\n%s", got)
	}
}

func TestLoadCodeFrontmatterExcludesPrompt(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "rules", "generated.md"), "---\npaths: [service/api/**]\nexclude: [service/api/generated/**]\n---\nshould not load")

	docs, err := LoadCode(project, "service/api/generated/client.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(documentContents(docs), "\n"); strings.Contains(got, "should not load") {
		t.Fatalf("excluded document loaded:\n%s", got)
	}
}

func TestLoadCodeLoadsManagedMemoryFromEnv(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	managed := filepath.Join(t.TempDir(), "managed.md")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("NIUMA_MANAGED_MEMORY", managed)
	mustWrite(t, managed, "managed policy")

	addendum := SystemAddendum(project)
	if !strings.Contains(addendum, "managed policy") || !strings.Contains(addendum, "(Managed)") {
		t.Fatalf("addendum = %s", addendum)
	}
}

func TestLoadCodeLoadsClaudeCodeProjectMemoryIndex(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(configDir, "projects", claudeCodeNativeProjectSlug(project), "memory", "MEMORY.md"), "project memory index")

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "project memory index") {
		t.Fatalf("project memory index not loaded:\n%s", got)
	}
	if len(docs) != 1 || docs[0].Type != "ClaudeCodeProjectMemory" {
		t.Fatalf("docs = %+v", docs)
	}
	addendum := SystemAddendumFromDocuments(docs)
	if strings.Contains(addendum, "(ClaudeCodeProjectMemory)") {
		t.Fatalf("prompt addendum exposed internal type:\n%s", addendum)
	}
	if !strings.Contains(addendum, "(ProjectMemory)") || !strings.Contains(addendum, "project memory index") {
		t.Fatalf("prompt addendum missing display label or content:\n%s", addendum)
	}
}

func TestLoadCodeRecallsIndexedProjectMemoryByFrontmatterSummary(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	memoryDir := filepath.Join(configDir, "projects", claudeCodeNativeProjectSlug(project), "memory")
	mustWrite(t, filepath.Join(memoryDir, "MEMORY.md"), "- [Go logging](go-logging.md) - logging preference")
	mustWrite(t, filepath.Join(memoryDir, "go-logging.md"), "---\nname: Go logging preference\ndescription: Structured logging and observability conventions.\nmetadata:\n  type: project\n---\nUse structured logs.")
	mustWrite(t, filepath.Join(memoryDir, "unrelated.md"), "---\nname: Video editing preference\ndescription: FFMPEG timeline conventions.\nmetadata:\n  type: project\n---\nUse FFMPEG.")

	docs, err := LoadCode(project, "review structured logging observability")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %+v, want index plus one recalled memory", docs)
	}
	if docs[1].Path != filepath.Join(memoryDir, "go-logging.md") || !strings.Contains(docs[1].Content, "Use structured logs.") {
		t.Fatalf("recalled docs = %+v", docs)
	}
}

func TestLoadCodeProjectMemoryRecallFailsClosedForIgnoreRequestAndTraversal(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	memoryDir := filepath.Join(configDir, "projects", claudeCodeNativeProjectSlug(project), "memory")
	mustWrite(t, filepath.Join(memoryDir, "MEMORY.md"), "- [Secret](secret.md) - secret project preference\n- [Escape](../outside.md) - should never load")
	mustWrite(t, filepath.Join(memoryDir, "secret.md"), "---\nname: Secret preference\ndescription: Secret project preference.\nmetadata:\n  type: project\n---\nSECRET_MEMORY_CONTENT")
	mustWrite(t, filepath.Join(configDir, "projects", claudeCodeNativeProjectSlug(project), "outside.md"), "---\nname: Outside\n description: Secret project preference.\n---\nOUTSIDE_CONTENT")

	docs, err := LoadCode(project, "ignore memory and do not use memory secret project preference")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || strings.Contains(strings.Join(documentContents(docs), "\n"), "SECRET_MEMORY_CONTENT") {
		t.Fatalf("ignore request recalled memory: %+v", docs)
	}

	docs, err = LoadCode(project, "secret project preference")
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(documentContents(docs), "\n")
	if strings.Contains(all, "OUTSIDE_CONTENT") {
		t.Fatalf("path traversal recalled outside memory: %s", all)
	}
}

func TestLoadCodeProjectMemoryRecallRejectsSymlinkOutsideMemoryDir(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	memoryDir := filepath.Join(configDir, "projects", claudeCodeNativeProjectSlug(project), "memory")
	outside := filepath.Join(t.TempDir(), "outside.md")
	mustWrite(t, outside, "---\nname: Secret project preference\ndescription: Secret project preference.\nmetadata:\n  type: project\n---\nOUTSIDE_SYMLINK_CONTENT")
	mustWrite(t, filepath.Join(memoryDir, "MEMORY.md"), "- [Secret](secret.md) - secret project preference")
	if err := os.Symlink(outside, filepath.Join(memoryDir, "secret.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	docs, err := LoadCode(project, "secret project preference")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(documentContents(docs), "\n"), "OUTSIDE_SYMLINK_CONTENT") {
		t.Fatal("recall followed a symlink outside the project memory directory")
	}
}

func TestLoadCodeFallsBackToGoProjectMemorySlug(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(configDir, "projects", session.ProjectSlug(project), "memory", "MEMORY.md"), "fallback project memory index")

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "fallback project memory index") {
		t.Fatalf("fallback project memory index not loaded:\n%s", got)
	}
}

func TestLoadCodeLimitsClaudeCodeProjectMemoryIndex(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	var lines []string
	for i := 0; i < 240; i++ {
		lines = append(lines, "line")
	}
	lines = append(lines, strings.Repeat("x", 30*1024))
	mustWrite(t, filepath.Join(configDir, "projects", session.ProjectSlug(project), "memory", "MEMORY.md"), strings.Join(lines, "\n"))

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %+v", docs)
	}
	if gotLines := strings.Count(docs[0].Content, "\n") + 1; gotLines != 200 {
		t.Fatalf("loaded lines = %d, want 200", gotLines)
	}
	if len(docs[0].Content) > 25*1024 {
		t.Fatalf("loaded bytes = %d, want <= 25KB", len(docs[0].Content))
	}
}

func TestLoadCodeLimitsClaudeCodeProjectMemoryIndexBytes(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(configDir, "projects", session.ProjectSlug(project), "memory", "MEMORY.md"), strings.Repeat("x", 30*1024))

	docs, err := LoadCode(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %+v", docs)
	}
	if len(docs[0].Content) != 25*1024 {
		t.Fatalf("loaded bytes = %d, want 25KB", len(docs[0].Content))
	}
}

func TestLoadAgentLoadsScopedMemory(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	child := filepath.Join(project, "child")
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(configDir, "agent-memory", "reviewer", "MEMORY.md"), "user reviewer memory")
	mustWrite(t, filepath.Join(project, ".claude", "agent-memory", "reviewer", "MEMORY.md"), "project reviewer memory")
	mustWrite(t, filepath.Join(project, ".claude", "agent-memory-local", "reviewer", "MEMORY.md"), "local reviewer memory")
	mustWrite(t, filepath.Join(project, ".claude", "agent-memory", "other", "MEMORY.md"), "other agent memory")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}

	docs, err := LoadAgent(child, "reviewer", "all")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	for _, want := range []string{"user reviewer memory", "project reviewer memory", "local reviewer memory"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in docs:\n%s", want, got)
		}
	}
	if strings.Contains(got, "other agent memory") {
		t.Fatalf("loaded another agent's memory:\n%s", got)
	}

	docs, err = LoadAgent(child, "reviewer", "project")
	if err != nil {
		t.Fatal(err)
	}
	got = strings.Join(documentContents(docs), "\n")
	if strings.Contains(got, "user reviewer memory") || strings.Contains(got, "local reviewer memory") || !strings.Contains(got, "project reviewer memory") {
		t.Fatalf("project scoped docs:\n%s", got)
	}
}

func TestLoadAgentSanitizesAgentName(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "agent-memory", "reviewer", "MEMORY.md"), "reviewer memory")
	mustWrite(t, filepath.Join(project, ".claude", "agent-memory", "secret", "MEMORY.md"), "secret memory")

	docs, err := LoadAgent(project, "../reviewer", "project")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(documentContents(docs), "\n")
	if !strings.Contains(got, "reviewer memory") || strings.Contains(got, "secret memory") {
		t.Fatalf("docs = %s", got)
	}
}

func TestSystemAddendumHeaderRequiresConflictSurfacing(t *testing.T) {
	header := SystemAddendumFromDocuments([]Document{{Path: "/tmp/CLAUDE.md", Content: "x", Type: "ClaudeCodeProjectMemory"}})
	for _, want := range []string{"scoped by the situation", "contradict each other", "instead of silently picking one"} {
		if !strings.Contains(header, want) {
			t.Fatalf("memory header missing %q:\n%s", want, header)
		}
	}
}

func documentContents(docs []Document) []string {
	out := make([]string, 0, len(docs))
	for _, doc := range docs {
		out = append(out, doc.Content)
	}
	return out
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
