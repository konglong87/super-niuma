package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListPlugins(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".claude", "plugins", "user", ".codex-plugin", "plugin.json"), `{"name":"user","version":"1.0.0"}`)
	mustWrite(t, filepath.Join(project, ".claude", "plugins", "project", ".claude-plugin", "plugin.json"), `{"name":"project","description":"Project plugin"}`)

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("plugins = %+v", list)
	}
	if list[0].Name != "project" || list[1].Name != "user" {
		t.Fatalf("plugins = %+v", list)
	}
	found, ok, err := Find(project, "project")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || found.Description != "Project plugin" {
		t.Fatalf("found=%+v ok=%v", found, ok)
	}
}

func TestListIncludesStandaloneClaudePluginRepos(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	standalone := filepath.Join(home, ".claude", "mattpocock-skills")
	mustWrite(t, filepath.Join(standalone, ".claude-plugin", "plugin.json"), `{
	  "name":"mattpocock-skills",
	  "skills":["./skills/engineering/tdd","./skills/productivity/teach"]
	}`)
	mustWrite(t, filepath.Join(home, ".claude", "plugins", "installed", ".codex-plugin", "plugin.json"), `{"name":"installed"}`)

	list, err := List(project)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Manifest{}
	for _, manifest := range list {
		got[manifest.Name] = manifest
	}
	if got["mattpocock-skills"].Path != standalone {
		t.Fatalf("standalone plugin not discovered: %+v", list)
	}
	if got["installed"].Path == "" {
		t.Fatalf("installed plugin missing: %+v", list)
	}

	dirs, err := SkillDirs(project)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(standalone, "skills", "productivity", "teach")
	found := false
	for _, dir := range dirs {
		if dir == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("standalone skill dir %s missing from %+v", want, dirs)
	}
}

func TestPluginContributionDirsAndMCP(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	pluginRoot := filepath.Join(project, ".claude", "plugins", "demo")
	mustWrite(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{
	  "name":"demo",
	  "skills":["custom-skills"],
	  "agents":["custom-agents"],
	  "outputStyles":["custom-output-styles"],
	  "mcpServers":{"demo":{"type":"http","url":"https://example.test/mcp"}}
	}`)
	if err := os.MkdirAll(filepath.Join(pluginRoot, "custom-skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "custom-agents"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "custom-output-styles"), 0755); err != nil {
		t.Fatal(err)
	}
	skillDirs, err := SkillDirs(project)
	if err != nil {
		t.Fatal(err)
	}
	agentDirs, err := AgentDirs(project)
	if err != nil {
		t.Fatal(err)
	}
	agentPaths, err := AgentPaths(project)
	if err != nil {
		t.Fatal(err)
	}
	outputStylePaths, err := OutputStylePaths(project)
	if err != nil {
		t.Fatal(err)
	}
	servers, err := MCPServers(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(skillDirs) != 1 || skillDirs[0] != filepath.Join(pluginRoot, "custom-skills") {
		t.Fatalf("skillDirs = %+v", skillDirs)
	}
	if len(agentDirs) != 1 || agentDirs[0] != filepath.Join(pluginRoot, "custom-agents") {
		t.Fatalf("agentDirs = %+v", agentDirs)
	}
	if len(agentPaths) != 1 || agentPaths[0].Plugin != "demo" || agentPaths[0].Path != filepath.Join(pluginRoot, "custom-agents") {
		t.Fatalf("agentPaths = %+v", agentPaths)
	}
	if len(outputStylePaths) != 1 || outputStylePaths[0].Path != filepath.Join(pluginRoot, "custom-output-styles") || outputStylePaths[0].Plugin != "demo" {
		t.Fatalf("outputStylePaths = %+v", outputStylePaths)
	}
	if servers["demo"].URL != "https://example.test/mcp" {
		t.Fatalf("servers = %+v", servers)
	}
}

func TestInstallLocalPlugin(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	source := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(source, ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1.2.3"}`)
	mustWrite(t, filepath.Join(source, "skills", "one", "SKILL.md"), "# Demo\n")

	manifest, err := InstallLocal(project, source, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "demo" || manifest.Version != "1.2.3" {
		t.Fatalf("manifest = %+v", manifest)
	}
	installed := filepath.Join(home, ".niuma", "plugins", "demo", "skills", "one", "SKILL.md")
	if data, err := os.ReadFile(installed); err != nil || string(data) != "# Demo\n" {
		t.Fatalf("installed data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "plugins", "demo", "skills", "one", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy user plugin install path should not be written, err=%v", err)
	}
	if _, err := InstallLocal(project, source, InstallOptions{}); err == nil {
		t.Fatal("expected duplicate install error")
	}
	if _, err := InstallLocal(project, source, InstallOptions{Force: true}); err != nil {
		t.Fatalf("force install: %v", err)
	}
}

func TestInstallLocalProjectPluginAndUninstall(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	source := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(source, "plugin.json"), `{"name":"project-demo"}`)

	if _, err := InstallLocal(project, source, InstallOptions{Project: true}); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(project, ".niuma", "plugins", "project-demo", "plugin.json")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("installed stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "plugins", "project-demo", "plugin.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy project plugin install path should not be written, err=%v", err)
	}
	if err := Uninstall(project, "project-demo", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("installed still exists err=%v", err)
	}
}

func TestInstallLocalProjectPluginUsesConfiguredIdentityPath(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	source := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR_NAME", ".go-code")
	mustWrite(t, filepath.Join(source, "plugin.json"), `{"name":"project-demo"}`)

	if _, err := InstallLocal(project, source, InstallOptions{Project: true}); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(project, ".go-code", "plugins", "project-demo", "plugin.json")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("installed stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".niuma", "plugins", "project-demo", "plugin.json")); !os.IsNotExist(err) {
		t.Fatalf("default project plugin install path should not be written, err=%v", err)
	}
}

func TestValidatePluginsChecksContributionPathsAndMCP(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	pluginRoot := filepath.Join(project, ".claude", "plugins", "demo")
	mustWrite(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{
	  "name":"demo",
	  "skills":["skills"],
	  "agents":["agents"],
	  "mcpServers":{"demo":{"command":"demo-mcp"}}
	}`)
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "agents"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Validate(project); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{"name":"demo","skills":["missing"]}`)
	if err := Validate(project); err == nil {
		t.Fatal("expected missing skill path error")
	}

	mustWrite(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{"name":"demo","mcpServers":{"bad":{}}}`)
	if err := Validate(project); err == nil {
		t.Fatal("expected invalid mcp server error")
	}

	mustWrite(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{"name":"demo","outputStyles":["styles/not-markdown.txt"]}`)
	mustWrite(t, filepath.Join(pluginRoot, "styles", "not-markdown.txt"), "bad")
	if err := Validate(project); err == nil {
		t.Fatal("expected invalid output style path error")
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
