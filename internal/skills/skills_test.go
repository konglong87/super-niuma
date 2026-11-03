package skills

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListAndLoadSkills(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(home, ".claude", "skills", "user-skill"), "---\nname: user-skill\ndescription: User metadata only\n---\n# User Skill\n\nUse me")
	writeSkill(t, filepath.Join(project, ".claude", "skills", "project-skill"), "---\nname: project-skill\ndescription: Project metadata only\n---\n# Project Skill\n\nUse me")

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("skills = %+v", list)
	}
	var projectMeta Skill
	for _, item := range list {
		if item.Name == "project-skill" {
			projectMeta = item
		}
	}
	if projectMeta.Content != "" || projectMeta.Description != "Project metadata only" {
		t.Fatalf("metadata should be lightweight: %+v", projectMeta)
	}
	skill, ok, err := Load(project, "project-skill")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(skill.Content, "Use me") {
		t.Fatalf("skill=%+v ok=%v", skill, ok)
	}
}

func TestListAggregatesGoClaudeLegacyAndEnvSkills(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	envLegacy := t.TempDir()
	envOwned := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_SKILL_PATHS", envLegacy)
	t.Setenv("NIUMA_SKILL_PATHS", envOwned)

	writeSkill(t, filepath.Join(home, ".claude", "skills", "legacy-user"), "---\nname: legacy-user\n---\n# Legacy User")
	writeSkill(t, filepath.Join(home, ".niuma-legacy", "skills", "go-user"), "---\nname: go-user\n---\n# Go User")
	writeSkill(t, filepath.Join(project, ".claude", "skills", "legacy-project"), "---\nname: legacy-project\n---\n# Legacy Project")
	writeSkill(t, filepath.Join(project, ".niuma-legacy", "skills", "go-project"), "---\nname: go-project\n---\n# Go Project")
	writeSkill(t, filepath.Join(envLegacy, "env-legacy"), "---\nname: env-legacy\n---\n# Env Legacy")
	writeSkill(t, filepath.Join(envOwned, "env-owned"), "---\nname: env-owned\n---\n# Env Owned")

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, item := range list {
		got[item.Name] = true
	}
	for _, name := range []string{"legacy-user", "go-user", "legacy-project", "go-project", "env-legacy", "env-owned"} {
		if !got[name] {
			t.Fatalf("skill %s missing from %+v", name, list)
		}
	}
}

func TestListAggregatesConfiguredIdentitySkills(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR_NAME", ".go-code")

	writeSkill(t, filepath.Join(home, ".go-code", "skills", "configured-user"), "---\nname: configured-user\n---\n# Configured User")
	writeSkill(t, filepath.Join(project, ".go-code", "skills", "configured-project"), "---\nname: configured-project\n---\n# Configured Project")

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, item := range list {
		got[item.Name] = true
	}
	for _, name := range []string{"configured-user", "configured-project"} {
		if !got[name] {
			t.Fatalf("skill %s missing from %+v", name, list)
		}
	}
}

func TestListSkillsFromPlugins(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{"name":"demo"}`)
	writeSkill(t, filepath.Join(project, ".claude", "plugins", "demo", "skills", "plugin-skill"), "---\ndescription: Plugin metadata only\n---\n# Plugin Skill\n\nUse me")
	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "demo:plugin-skill" || list[0].Plugin != "demo" {
		t.Fatalf("skills = %+v", list)
	}
	skill, ok, err := Load(project, "demo:plugin-skill")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(skill.Content, "Plugin Skill") {
		t.Fatalf("skill=%+v ok=%v", skill, ok)
	}
}

func TestListLoadsSkillsFromStandaloneClaudePluginRepos(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	pluginRoot := filepath.Join(home, ".claude", "mattpocock-skills")
	mustWrite(t, filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), `{
	  "name":"mattpocock-skills",
	  "skills":["./skills/engineering/tdd","./skills/productivity/teach"]
	}`)
	writeSkill(t, filepath.Join(pluginRoot, "skills", "engineering", "tdd"), "---\nname: tdd\ndescription: Test first\n---\n# TDD")
	writeSkill(t, filepath.Join(pluginRoot, "skills", "productivity", "teach"), "---\nname: teach\ndescription: Teach a concept\ndisable-model-invocation: true\n---\n# Teach")

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Skill{}
	for _, item := range list {
		got[item.Name] = item
	}
	if got["mattpocock-skills:tdd"].Plugin != "mattpocock-skills" || got["mattpocock-skills:teach"].Plugin != "mattpocock-skills" {
		t.Fatalf("plugin skills missing: %+v", list)
	}
	if !got["mattpocock-skills:teach"].DisableModelInvocation {
		t.Fatalf("teach metadata not parsed: %+v", got["mattpocock-skills:teach"])
	}
	skill, ok, err := Load(project, "mattpocock-skills:teach")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(skill.Content, "# Teach") {
		t.Fatalf("skill=%+v ok=%v", skill, ok)
	}
	skill, ok, err = Load(project, "teach")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || skill.Name != "mattpocock-skills:teach" || !strings.Contains(skill.Content, "# Teach") {
		t.Fatalf("bare local skill=%+v ok=%v", skill, ok)
	}
}

func TestCatalogPromptListsMetadataOnlyAndLegacyCommands(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(project, ".claude", "skills", "go-review"), "---\nname: go-review\ndescription: Review Go code\n---\n# Go Review\n\nFull private instructions")
	mustWrite(t, filepath.Join(project, ".claude", "commands", "legacy.md"), "---\ndescription: Legacy command\n---\n# Legacy\n\nHidden body")

	catalog, err := CatalogPrompt(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"go-review: Review Go code", "legacy: Legacy command (legacy command)", "blocking requirement", "Use the skill's exact name"} {
		if !strings.Contains(catalog, want) {
			t.Fatalf("catalog missing %q:\n%s", want, catalog)
		}
	}
	if strings.Contains(catalog, "Full private instructions") || strings.Contains(catalog, "Hidden body") {
		t.Fatalf("catalog leaked full instructions:\n%s", catalog)
	}
}

func TestCatalogPromptBudgetsLargeCatalogButKeepsExactNames(t *testing.T) {
	t.Setenv("NIUMA_SKILLS_CATALOG_BUDGET_BYTES", "1200")
	var list []Skill
	for i := 0; i < 20; i++ {
		list = append(list, Skill{
			Name:        fmt.Sprintf("skill-%02d", i),
			Description: "Use this skill for specialized work. " + strings.Repeat("long description ", 20),
		})
	}

	catalog, names := CatalogPromptFromSkillsWithNames(list, "", nil)
	if len(names) != 20 {
		t.Fatalf("names = %d", len(names))
	}
	if len(catalog) > 1200 {
		t.Fatalf("catalog bytes = %d, want <= 1200\n%s", len(catalog), catalog)
	}
	for _, want := range []string{"Skill names:", "skill-00", "skill-19", "Compact skill metadata", "Skill tool with the exact name"} {
		if !strings.Contains(catalog, want) {
			t.Fatalf("budgeted catalog missing %q:\n%s", want, catalog)
		}
	}
	if strings.Contains(catalog, strings.Repeat("long description ", 6)) {
		t.Fatalf("budgeted catalog kept unbounded description:\n%s", catalog)
	}
}

func TestListParsesBlockScalarFrontmatterDescription(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(project, ".claude", "skills", "block-skill"), "---\nname: block-skill\ndescription: |\n  First line.\n  Second line.\n---\n# Body")

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Description != "First line. Second line." {
		t.Fatalf("skills = %+v", list)
	}
}

func TestListParsesSkillFrontmatterMetadata(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(project, ".claude", "skills", "go-review"), `---
name: go-review
description: Review Go changes.
when_to_use: Use after editing Go files.
allowed-tools:
  - Read
  - Bash(go test:*)
argument-hint: "[path]"
arguments:
  - path
version: "1.2.3"
model: inherit
context: fork
agent: reviewer
effort: medium
hooks:
  PreToolUse:
    - command: "printf '{}'"
      matcher: "Bash:go test*"
paths: "internal/**, cmd/**"
user-invocable: false
---
# Body`)

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("skills = %+v", list)
	}
	skill := list[0]
	if skill.WhenToUse != "Use after editing Go files." || skill.ArgumentHint != "[path]" || skill.Version != "1.2.3" || skill.Model != "inherit" || skill.ExecutionContext != "fork" || skill.Agent != "reviewer" || skill.Effort != "medium" || skill.UserInvocable {
		t.Fatalf("metadata not parsed: %+v", skill)
	}
	if strings.Join(skill.AllowedTools, ",") != "Read,Bash(go test:*)" || strings.Join(skill.Paths, ",") != "internal/**,cmd/**" || strings.Join(skill.Arguments, ",") != "path" {
		t.Fatalf("list metadata not parsed: %+v", skill)
	}
	if len(skill.Hooks["PreToolUse"]) != 1 || skill.Hooks["PreToolUse"][0].Command != "printf '{}'" || skill.Hooks["PreToolUse"][0].Matcher != "Bash:go test*" {
		t.Fatalf("hooks metadata not parsed: %+v", skill.Hooks)
	}
}

func TestListDiscoversEnvSkillRootsDynamically(t *testing.T) {
	home := t.TempDir()
	extra := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_SKILL_PATHS", extra)
	writeSkill(t, filepath.Join(extra, "env-skill"), "---\nname: env-skill\ndescription: Dynamic env skill\n---\n# Env")

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "env-skill" {
		t.Fatalf("skills = %+v", list)
	}

	writeSkill(t, filepath.Join(extra, "second-skill"), "---\nname: second-skill\ndescription: Added later\n---\n# Second")
	list, err = List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("dynamic skills = %+v", list)
	}
}

func TestListDiscoversBundledMarketplaceAndMCPSkills(t *testing.T) {
	home := t.TempDir()
	bundled := t.TempDir()
	mcpRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_BUNDLED_SKILLS_PATHS", bundled)
	t.Setenv("CLAUDE_MCP_SKILL_PATHS", mcpRoot)
	writeSkill(t, filepath.Join(bundled, "bundled-skill"), "---\nname: bundled-skill\ndescription: Bundled\n---\n# Bundled")
	writeSkill(t, filepath.Join(home, ".claude", "skills-marketplace", "market-skill"), "---\nname: market-skill\ndescription: Market\n---\n# Market")
	writeSkill(t, filepath.Join(home, ".claude", "mcp-skills", "mcp-cache-skill"), "---\nname: mcp-cache-skill\ndescription: MCP Cache\n---\n# MCP Cache")
	writeSkill(t, filepath.Join(mcpRoot, "mcp-env-skill"), "---\nname: mcp-env-skill\ndescription: MCP Env\n---\n# MCP Env")

	list, err := List(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Source{}
	for _, item := range list {
		got[item.Name] = item.Source
	}
	for name, source := range map[string]Source{
		"bundled-skill":   SourceBundled,
		"market-skill":    SourceMarketplace,
		"mcp-cache-skill": SourceMCP,
		"mcp-env-skill":   SourceMCP,
	} {
		if got[name] != source {
			t.Fatalf("%s source = %q in %+v", name, got[name], list)
		}
	}
}

func TestSyncMarketplaceWritesSkills(t *testing.T) {
	target := t.TempDir()
	source := filepath.Join(t.TempDir(), "index.yaml")
	mustWrite(t, source, `skills:
  - name: market-sync
    description: Synced skill
    content: "# Synced body"
`)
	synced, err := SyncMarketplace(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(synced) != 1 {
		t.Fatalf("synced = %+v", synced)
	}
	data, err := os.ReadFile(filepath.Join(target, "market-sync", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Synced skill") || !strings.Contains(string(data), "Synced body") {
		t.Fatalf("skill file = %s", data)
	}
}

func TestSyncMarketplaceDefaultTargetUsesGolangCC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(t.TempDir(), "index.yaml")
	mustWrite(t, source, `skills:
  - name: market-sync
    content: "# Synced body"
`)
	if _, err := SyncMarketplace("", source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".niuma", "skills-marketplace", "market-sync", "SKILL.md")); err != nil {
		t.Fatalf("niuma marketplace skill missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills-marketplace", "market-sync", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy marketplace target should not be written, err=%v", err)
	}
}

func TestInstallMarketplaceWritesSingleSkill(t *testing.T) {
	target := t.TempDir()
	dir := t.TempDir()
	source := filepath.Join(dir, "index.yaml")
	body := "---\nname: market-install\n---\n# Installed body\n"
	bodyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	mustWrite(t, filepath.Join(dir, "external.md"), body)
	mustWrite(t, source, `skills:
  - name: market-install
    description: Install one skill
    version: 1.2.3
    content_sha256: `+bodyHash+`
    path: external.md
  - name: other
    description: Other skill
    content: "# Other"
`)
	skill, err := InstallMarketplace(target, source, "market-install")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "market-install" {
		t.Fatalf("skill = %+v", skill)
	}
	if skill.Version != "1.2.3" {
		t.Fatalf("version = %q", skill.Version)
	}
	if _, err := os.Stat(filepath.Join(target, "other", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("other skill should not be installed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "market-install", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Installed body") {
		t.Fatalf("skill file = %s", data)
	}
}

func TestInstallMarketplaceRejectsHashMismatch(t *testing.T) {
	target := t.TempDir()
	source := filepath.Join(t.TempDir(), "index.yaml")
	mustWrite(t, source, `skills:
  - name: market-install
    description: Install one skill
    content_sha256: bad
    content: "# Installed body"
`)
	_, err := InstallMarketplace(target, source, "market-install")
	if err == nil || !strings.Contains(err.Error(), "content_sha256 mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallMarketplaceValidatesSignature(t *testing.T) {
	target := t.TempDir()
	source := filepath.Join(t.TempDir(), "index.yaml")
	body := "---\nname: signed-skill\n---\n# Signed body\n"
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, []byte(body))
	bodyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	mustWrite(t, source, fmt.Sprintf(`skills:
  - name: signed-skill
    description: Signed skill
    content_sha256: %s
    signature_alg: ed25519
    public_key: %s
    signature: %s
    content: |
%s
`, bodyHash, base64.StdEncoding.EncodeToString(publicKey), base64.StdEncoding.EncodeToString(signature), indentYAMLBlock(body, 6)))

	skill, err := InstallMarketplace(target, source, "signed-skill")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "signed-skill" {
		t.Fatalf("skill = %+v", skill)
	}
}

func TestInstallMarketplaceRejectsBadSignature(t *testing.T) {
	target := t.TempDir()
	source := filepath.Join(t.TempDir(), "index.yaml")
	body := "# Signed body"
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, []byte("different body"))
	mustWrite(t, source, fmt.Sprintf(`skills:
  - name: signed-skill
    description: Signed skill
    signature_alg: ed25519
    public_key: %s
    signature: %s
    content: %q
`, base64.StdEncoding.EncodeToString(publicKey), base64.StdEncoding.EncodeToString(signature), body))

	_, err = InstallMarketplace(target, source, "signed-skill")
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallMarketplaceRequiresSignaturePair(t *testing.T) {
	target := t.TempDir()
	source := filepath.Join(t.TempDir(), "index.yaml")
	mustWrite(t, source, `skills:
  - name: signed-skill
    description: Signed skill
    public_key: only-key
    content: "# Signed body"
`)

	_, err := InstallMarketplace(target, source, "signed-skill")
	if err == nil || !strings.Contains(err.Error(), "signature and public_key must be provided together") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchMarketplace(t *testing.T) {
	source := filepath.Join(t.TempDir(), "index.yaml")
	mustWrite(t, source, `skills:
  - name: go-review
    description: Review Go code
  - name: docs
    description: Write docs
`)
	results, err := SearchMarketplace(source, "review")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "go-review" {
		t.Fatalf("results = %+v", results)
	}
}

func TestInspectMarketplaceReportsVerificationState(t *testing.T) {
	source := filepath.Join(t.TempDir(), "index.yaml")
	mustWrite(t, source, `skills:
  - name: signed
    description: Signed skill
    version: 1.2.3
    content_sha256: abc
    public_key: key
    signature: sig
    path: signed/SKILL.md
  - name: inline
    description: Inline skill
    content: "# Inline"
`)
	report, err := InspectMarketplace(source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Count != 2 || report.Hashed != 1 || report.Signed != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Items[0].Name != "inline" || !report.Items[0].InlineContent || report.Items[0].Signed {
		t.Fatalf("inline item = %+v", report.Items[0])
	}
	if report.Items[1].Name != "signed" || !report.Items[1].Signed || report.Items[1].SignatureAlg != "ed25519" || report.Items[1].Path != "signed/SKILL.md" {
		t.Fatalf("signed item = %+v", report.Items[1])
	}
}

func TestMarketplaceStatusReportsMissingCurrentAndOutdated(t *testing.T) {
	target := t.TempDir()
	source := filepath.Join(t.TempDir(), "index.yaml")
	currentBody := "---\nname: current\nversion: 1.0.0\n---\n# Current\n"
	outdatedBody := "---\nname: outdated\nversion: 1.0.0\n---\n# Old\n"
	newOutdatedBody := "---\nname: outdated\nversion: 2.0.0\n---\n# New\n"
	mustWrite(t, filepath.Join(target, "current", "SKILL.md"), currentBody)
	mustWrite(t, filepath.Join(target, "outdated", "SKILL.md"), outdatedBody)
	mustWrite(t, source, fmt.Sprintf(`skills:
  - name: current
    version: 1.0.0
    content_sha256: %s
  - name: outdated
    version: 2.0.0
    content_sha256: %s
  - name: missing
    version: 1.0.0
`, fmt.Sprintf("%x", sha256.Sum256([]byte(currentBody))), fmt.Sprintf("%x", sha256.Sum256([]byte(newOutdatedBody)))))

	report, err := MarketplaceStatus(target, source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Count != 3 || report.Installed != 2 || report.Current != 1 || report.Outdated != 1 || report.Missing != 1 {
		t.Fatalf("report = %+v", report)
	}
	statuses := map[string]string{}
	for _, item := range report.Items {
		statuses[item.Name] = item.Status
	}
	if statuses["current"] != "current" || statuses["outdated"] != "outdated" || statuses["missing"] != "missing" {
		t.Fatalf("statuses = %+v report=%+v", statuses, report)
	}
}

func TestPackageSkill(t *testing.T) {
	source := filepath.Join(t.TempDir(), "skill")
	writeSkill(t, source, "---\nname: demo\ndescription: Demo\n---\n# Demo")
	mustWrite(t, filepath.Join(source, "references", "notes.md"), "notes")
	output := filepath.Join(t.TempDir(), "demo.skill.zip")
	if err := PackageSkill(source, output); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]bool{}
	for _, file := range zr.File {
		got[file.Name] = true
	}
	if !got["SKILL.md"] || !got["references/notes.md"] {
		t.Fatalf("zip files = %+v", got)
	}
	if err := PackageSkill(t.TempDir(), filepath.Join(t.TempDir(), "bad.zip")); err == nil {
		t.Fatal("expected missing SKILL.md error")
	}
}

func TestSearchSkillsMatchesMetadataAndSource(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(project, ".claude", "skills", "go-review"), "---\nname: go-review\ndescription: Review Go code\nwhen_to_use: after edits\n---\n# Go")
	writeSkill(t, filepath.Join(project, ".claude", "skills", "docs"), "---\nname: docs\ndescription: Write docs\n---\n# Docs")

	results, err := Search(project, "after edits")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Name != "go-review" {
		t.Fatalf("results = %+v", results)
	}
	results, err = Search(project, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("source results = %+v", results)
	}
}

func TestValidateBundledCatalog(t *testing.T) {
	home := t.TempDir()
	bundled := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_BUNDLED_SKILLS_PATHS", bundled)
	content := `---
name: bundled-review
description: Review bundled code
when_to_use: After code edits.
allowed-tools:
  - Read
  - Bash(go test:*)
model: inherit
context: fork
agent: reviewer
effort: medium
---
# Bundled Review
`
	writeSkill(t, filepath.Join(bundled, "bundled-review"), content)
	source := filepath.Join(t.TempDir(), "bundled.yaml")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	mustWrite(t, source, fmt.Sprintf(`skills:
  - name: bundled-review
    description: Review bundled code
    when_to_use: After code edits.
    allowed_tools:
      - Read
      - Bash(go test:*)
    model: inherit
    context: fork
    agent: reviewer
    effort: medium
    content_sha256: %s
`, hash))
	report, err := ValidateBundledCatalog(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Expected != 1 || report.Found != 1 || strings.Join(report.Matched, ",") != "bundled-review" || len(report.Mismatch) != 0 || len(report.Missing) != 0 || len(report.Extra) != 0 {
		t.Fatalf("report = %+v", report)
	}

	mustWrite(t, source, `skills:
  - name: bundled-review
    description: Different
  - name: missing-skill
`)
	report, err = ValidateBundledCatalog(t.TempDir(), source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.Missing, ",") != "missing-skill" || len(report.Mismatch) != 1 || report.Mismatch[0].Field != "description" {
		t.Fatalf("mismatch report = %+v", report)
	}
}

func TestCatalogPromptSkipsModelDisabledSkill(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(project, ".claude", "skills", "hidden"), "---\ndescription: Hidden\ndisable-model-invocation: true\n---\n# Hidden")

	catalog, err := CatalogPrompt(project)
	if err != nil {
		t.Fatal(err)
	}
	if catalog != "" {
		t.Fatalf("disabled skill should not be in catalog:\n%s", catalog)
	}
}

func TestCatalogPromptFiltersByPaths(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(project, ".claude", "skills", "go-skill"), "---\ndescription: Go files\npaths: internal/**\n---\n# Go")

	catalog, err := CatalogPromptForPrompt(project, "please inspect README.md")
	if err != nil {
		t.Fatal(err)
	}
	if catalog != "" {
		t.Fatalf("catalog should not include path-mismatched skill:\n%s", catalog)
	}
	catalog, err = CatalogPromptForPrompt(project, "please inspect internal/query/query.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(catalog, "go-skill") {
		t.Fatalf("catalog missing path-matched skill:\n%s", catalog)
	}
}

func TestCatalogPromptForPromptExcludingSkipsSeenSkills(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(project, ".claude", "skills", "go-skill"), "---\ndescription: Go files\npaths: internal/**\n---\n# Go")
	writeSkill(t, filepath.Join(project, ".claude", "skills", "review-skill"), "---\ndescription: Review files\npaths: internal/**\n---\n# Review")

	catalog, names, err := CatalogPromptForPromptExcluding(project, "internal/query/query.go", map[string]bool{"go-skill": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(catalog, "go-skill") {
		t.Fatalf("catalog included seen skill:\n%s", catalog)
	}
	if !strings.Contains(catalog, "review-skill") || strings.Join(names, ",") != "review-skill" {
		t.Fatalf("catalog=%q names=%+v", catalog, names)
	}
}

func TestRecordFeedbackAppendsJSONL(t *testing.T) {
	target := filepath.Join(t.TempDir(), "feedback.jsonl")
	feedback, err := RecordFeedback(target, "go-review", 5, "Useful")
	if err != nil {
		t.Fatal(err)
	}
	if feedback.Name != "go-review" || feedback.Rating != 5 || feedback.Path != target {
		t.Fatalf("feedback = %+v", feedback)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"go-review"`) || !strings.Contains(string(data), `"rating":5`) {
		t.Fatalf("feedback file = %s", data)
	}
	if _, err := RecordFeedback(target, "bad", 6, ""); err == nil || !strings.Contains(err.Error(), "rating") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordFeedbackDefaultTargetUsesGolangCC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	feedback, err := RecordFeedback("", "go-review", 5, "Useful")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".niuma", "skill-feedback.jsonl")
	if feedback.Path != want {
		t.Fatalf("feedback path = %q, want %q", feedback.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("niuma feedback missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skill-feedback.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("legacy feedback path should not be written, err=%v", err)
	}
}

func TestWatchReportsSkillChanges(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(project, ".claude", "skills", "watched")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs, err := Watch(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: watched\n---\n# Watched")

	event := waitSkillWatchEvent(t, events, errs, "watched")
	if event.Source != SourceProject || event.Path == "" || event.Operation == "" {
		t.Fatalf("event = %+v", event)
	}
}

func TestWatchReportsPluginLegacyCommandChanges(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	commandsDir := filepath.Join(project, ".claude", "plugins", "demo", "commands")
	mustWrite(t, filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{"name":"demo"}`)
	if err := os.MkdirAll(commandsDir, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs, err := Watch(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(commandsDir, "legacy.md"), "# Legacy")

	event := waitSkillWatchEvent(t, events, errs, "demo:legacy")
	if event.Source != SourcePlugin || event.Plugin != "demo" {
		t.Fatalf("event = %+v", event)
	}
}

func writeSkill(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
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

func indentYAMLBlock(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func waitSkillWatchEvent(t *testing.T, events <-chan WatchEvent, errs <-chan error, name string) WatchEvent {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("watch events closed")
			}
			if event.Name == name {
				return event
			}
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for watch event %q", name)
		}
	}
}
