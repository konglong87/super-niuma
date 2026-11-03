package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitDiscoveryDoesNotLoadWorkspaceUserOrPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_BUNDLED_SKILLS_PATHS", "")
	t.Setenv("NIUMA_BUNDLED_SKILLS_PATHS", "")
	workspace := t.TempDir()
	explicit := t.TempDir()
	writeTestSkill := func(root, name string) {
		dir := filepath.Join(root, ".claude", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: test\nuser-invocable: true\n---\nbody"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTestSkill(workspace, "workspace-skill")
	writeTestSkill(explicit, "explicit-skill")
	items, err := ListWithOptions(workspace, DiscoveryOptions{ExplicitRoots: []string{explicit}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "explicit-skill" {
		t.Fatalf("skills = %+v", items)
	}
}
