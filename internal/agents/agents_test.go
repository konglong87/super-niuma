package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndLoadAgents(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeAgent(t, filepath.Join(home, ".claude", "agents", "reviewer.md"), `---
name: reviewer
description: Reviews code
tools: Read,Grep
model: claude-opus
---
Review carefully.`)
	writeAgent(t, filepath.Join(project, ".claude", "agents", "planner.md"), `# Planner

Plan work.`)
	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 {
		t.Fatalf("agents = %+v", list)
	}
	agent, ok, err := Load(project, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || agent.Description != "Reviews code" || len(agent.Tools) != 2 || agent.Prompt != "Review carefully." {
		t.Fatalf("agent=%+v ok=%v", agent, ok)
	}
	if agent.Model != "claude-opus" {
		t.Fatalf("model = %q", agent.Model)
	}
}

func TestLoadAgentDefinitionSchemaFields(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeAgent(t, filepath.Join(project, ".claude", "agents", "full.md"), `---
name: full
description: Full schema agent
tools:
  - Read
  - Grep
disallowedTools:
  - Bash
mcpServers:
  - local
  - search:
      type: http
      url: https://example.test/mcp
criticalSystemReminder_EXPERIMENTAL: Stay focused.
skills:
  - review
initialPrompt: /status
maxTurns: 7
background: true
omitGitStatus: true
memory: project
effort: high
permissionMode: ask
futureField: keep-me-visible
---
Full prompt.`)
	agent, ok, err := Load(project, "full")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("agent not found")
	}
	if agent.Name != "full" || agent.Description != "Full schema agent" || agent.Prompt != "Full prompt." {
		t.Fatalf("basic fields = %+v", agent)
	}
	if got := join(agent.Tools); got != "Read,Grep" {
		t.Fatalf("tools = %q", got)
	}
	if got := join(agent.DisallowedTools); got != "Bash" {
		t.Fatalf("disallowed tools = %q", got)
	}
	if len(agent.MCPServers) != 2 || agent.MCPServers[0].Name != "local" || agent.MCPServers[1].Name != "search" || agent.MCPServers[1].Config["url"] != "https://example.test/mcp" {
		t.Fatalf("mcp servers = %+v", agent.MCPServers)
	}
	if agent.CriticalSystemReminderExperimental != "Stay focused." || join(agent.Skills) != "review" || agent.InitialPrompt != "/status" {
		t.Fatalf("context fields = %+v", agent)
	}
	if agent.MaxTurns != 7 || !agent.Background || !agent.OmitGitStatus || agent.Memory != "project" || agent.Effort != "high" || agent.PermissionMode != "ask" {
		t.Fatalf("runtime fields = %+v", agent)
	}
	if agent.UnknownFields["futureField"] != "keep-me-visible" {
		t.Fatalf("unknown fields = %+v", agent.UnknownFields)
	}
}

func TestLoadAgentDefinitionSnakeCaseAliases(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeAgent(t, filepath.Join(project, ".claude", "agents", "aliases.md"), `---
name: aliases
disallowed_tools: Bash, Task
mcp_servers:
  - db:
      command: sqlite
initial_prompt: hello
max_turns: 3
permission_mode: deny
effort: 2
omit_git_status: true
---
Alias prompt.`)
	agent, ok, err := Load(project, "aliases")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("agent not found")
	}
	if join(agent.DisallowedTools) != "Bash,Task" || agent.InitialPrompt != "hello" || agent.MaxTurns != 3 || agent.PermissionMode != "deny" || agent.Effort != "2" || !agent.OmitGitStatus {
		t.Fatalf("aliases not parsed: %+v", agent)
	}
	if len(agent.MCPServers) != 1 || agent.MCPServers[0].Name != "db" || agent.MCPServers[0].Config["command"] != "sqlite" {
		t.Fatalf("mcp aliases = %+v", agent.MCPServers)
	}
}

func TestLoadAgentDefinitionParsesBooleanMemory(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeAgent(t, filepath.Join(project, ".claude", "agents", "memory.md"), `---
name: memory
memory: true
---
Remember.`)
	agent, ok, err := Load(project, "memory")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("agent not found")
	}
	if agent.Memory != "true" {
		t.Fatalf("memory = %q", agent.Memory)
	}
}

func TestListAgentsFromPlugins(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{"name":"demo"}`)
	writeAgent(t, filepath.Join(project, ".claude", "plugins", "demo", "agents", "plugin-agent.md"), `---
name: plugin-agent
description: Plugin full schema
tools: Read,Grep
disallowedTools:
  - Bash
mcpServers:
  - plugin-mcp:
      command: plugin-server
skills:
  - plugin-skill
initialPrompt: start here
maxTurns: 4
background: true
memory: project
effort: high
permissionMode: ask
---
Help.`)
	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	var agent Agent
	for _, candidate := range list {
		if candidate.Name == "plugin-agent" {
			agent = candidate
			break
		}
	}
	if agent.Name != "plugin-agent" || agent.Source != "plugin" || agent.Plugin != "demo" {
		t.Fatalf("agents = %+v", list)
	}
	if agent.Description != "Plugin full schema" || join(agent.Tools) != "Read,Grep" || join(agent.DisallowedTools) != "Bash" {
		t.Fatalf("tool fields = %+v", agent)
	}
	if len(agent.MCPServers) != 1 || agent.MCPServers[0].Name != "plugin-mcp" || agent.MCPServers[0].Config["command"] != "plugin-server" {
		t.Fatalf("mcp servers = %+v", agent.MCPServers)
	}
	if join(agent.Skills) != "plugin-skill" || agent.InitialPrompt != "start here" || agent.MaxTurns != 4 || !agent.Background || agent.Memory != "project" || agent.Effort != "high" || agent.PermissionMode != "ask" {
		t.Fatalf("schema fields = %+v", agent)
	}
}

func TestBuiltInAgentsAreListedAndLoadable(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, agent := range list {
		if agent.Source == "built-in" {
			names = append(names, agent.Name)
		}
	}
	if got := join(names); got != "Explore,Plan,general-purpose" {
		t.Fatalf("built-ins = %q", got)
	}
	explore, ok, err := Load(project, "Explore")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || explore.Model != "haiku" || !explore.OmitGitStatus || !strings.Contains(explore.Prompt, "READ-ONLY exploration task") {
		t.Fatalf("Explore = %+v ok=%v", explore, ok)
	}
	plan, ok, err := Load(project, "Plan")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || plan.Model != "inherit" || !plan.OmitGitStatus || !strings.Contains(plan.Prompt, "Critical Files for Implementation") {
		t.Fatalf("Plan = %+v ok=%v", plan, ok)
	}
}

func TestAgentPrecedenceProjectOverridesPluginOverridesUser(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeAgent(t, filepath.Join(home, ".claude", "agents", "shared.md"), "---\nname: shared\ndescription: User\n---\nUser.")
	mustWrite(t, filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{"name":"demo"}`)
	writeAgent(t, filepath.Join(project, ".claude", "plugins", "demo", "agents", "shared.md"), "---\nname: shared\ndescription: Plugin\n---\nPlugin.")
	writeAgent(t, filepath.Join(project, ".claude", "agents", "shared.md"), "---\nname: shared\ndescription: Project\n---\nProject.")

	agent, ok, err := Load(project, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("agent not found")
	}
	if agent.Description != "Project" || agent.Source != "project" || agent.Plugin != "" || agent.Prompt != "Project." {
		t.Fatalf("agent = %+v", agent)
	}

	if err := os.Remove(filepath.Join(project, ".claude", "agents", "shared.md")); err != nil {
		t.Fatal(err)
	}
	agent, ok, err = Load(project, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || agent.Description != "Plugin" || agent.Source != "plugin" || agent.Plugin != "demo" {
		t.Fatalf("plugin fallback agent = %+v ok=%v", agent, ok)
	}
}

func TestDoctorReportsAuthoringIssues(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeAgent(t, filepath.Join(project, ".claude", "agents", "bad.md"), `---
name: bad
tools: Read,Bash
disallowedTools: Bash
permissionMode: root
effort: huge
futureField: keep-me-visible
background: true
initialPrompt: start
---
`)
	report, err := Doctor(project)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "error" || report.Errors == 0 || report.Warnings < 4 {
		t.Fatalf("report = %+v", report)
	}
	var sawPrompt, sawUnknown, sawConflict bool
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Field == "prompt" && diagnostic.Level == "error" {
			sawPrompt = true
		}
		if diagnostic.Field == "futureField" && diagnostic.Level == "warning" {
			sawUnknown = true
		}
		if diagnostic.Field == "tools" && strings.Contains(diagnostic.Message, "Bash") {
			sawConflict = true
		}
	}
	if !sawPrompt || !sawUnknown || !sawConflict {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func join(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ","
		}
		out += value
	}
	return out
}

func writeAgent(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
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
