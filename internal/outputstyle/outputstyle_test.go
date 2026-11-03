package outputstyle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLoadsProjectOutputStyleOverUser(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeStyle(t, filepath.Join(home, ".claude", "output-styles", "focused.md"), "---\nname: Focused\n---\nuser prompt")
	writeStyle(t, filepath.Join(project, ".claude", "output-styles", "focused.md"), "---\nname: Focused\ndescription: Project style\nkeep-coding-instructions: false\n---\nproject prompt")

	style, err := Resolve(project, "Focused")
	if err != nil {
		t.Fatal(err)
	}
	if style == nil || style.Prompt != "project prompt" || style.Source != "projectSettings" || style.KeepCodingInstructions {
		t.Fatalf("style = %+v", style)
	}
}

func TestResolveDefaultOutputStyleIsNil(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	style, err := Resolve(t.TempDir(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if style != nil {
		t.Fatalf("style = %+v", style)
	}
}

func TestResolveForcedPluginOutputStyleWins(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeStyle(t, filepath.Join(project, ".claude", "output-styles", "focused.md"), "---\nname: Focused\n---\nproject prompt")
	writeStyle(t, filepath.Join(project, ".claude", "output-styles", "shadow.md"), "---\nname: demo:PluginStyle\n---\nproject shadow")
	writeStyle(t, filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{"name":"demo"}`)
	writeStyle(t, filepath.Join(project, ".claude", "plugins", "demo", "output-styles", "plugin.md"), "---\nname: PluginStyle\nforce-for-plugin: true\n---\nplugin prompt")

	style, err := Resolve(project, "Focused")
	if err != nil {
		t.Fatal(err)
	}
	if style == nil || style.Name != "demo:PluginStyle" || style.Source != "plugin" || style.Plugin != "demo" || !style.ForceForPlugin || style.Prompt != "plugin prompt" {
		t.Fatalf("style = %+v", style)
	}
}

func TestAllLoadsPluginManifestOutputStyleFile(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeStyle(t, filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), `{"name":"demo","outputStyles":["styles/custom.md"]}`)
	writeStyle(t, filepath.Join(project, ".claude", "plugins", "demo", "styles", "custom.md"), "---\nname: Custom\n---\ncustom prompt")

	styles, err := All(project)
	if err != nil {
		t.Fatal(err)
	}
	style, ok := styles["demo:Custom"]
	if !ok || style.Prompt != "custom prompt" || style.Plugin != "demo" {
		t.Fatalf("styles = %+v", styles)
	}
}

func writeStyle(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
