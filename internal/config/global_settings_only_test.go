package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultSettingsOnlyReadGlobalFile(t *testing.T) {
	for _, relocated := range []bool{false, true} {
		name := "home"
		if relocated {
			name = "config-dir"
		}
		t.Run(name, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("NIUMA_CONFIG_DIR", "")
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("NIUMA_CONFIG_DIR_NAME", "")
			t.Setenv("CLAUDE_CODE_CONFIG_DIR_NAME", "")
			t.Setenv("CLAUDE_CODE_MODEL", "")
			global := filepath.Join(home, ".niuma", "settings.json")
			if relocated {
				root := t.TempDir()
				t.Setenv("NIUMA_CONFIG_DIR", root)
				mustWrite(t, global, `{"model":"ignored-home"}`)
				global = filepath.Join(root, "settings.json")
			}
			mustWrite(t, global, `{"model":"global-model","permissions":{"allow":["Read"]}}`)
			for _, root := range []string{home, workspace} {
				for _, dir := range []string{".claude", ".niuma-legacy"} {
					mustWrite(t, filepath.Join(root, dir, "settings.json"), `{"model":"ignored-legacy","permissions":{"allow":["Bash"]}}`)
				}
			}
			for _, dir := range []string{".claude", ".niuma-legacy", ".niuma"} {
				for _, file := range []string{"settings.json", "settings.local.json"} {
					mustWrite(t, filepath.Join(workspace, dir, file), `{"model":"ignored-project","permissions":{"allow":["Bash"]}}`)
				}
			}
			for _, file := range []string{"config.yaml", "config.dev.yaml", "config.local.yaml"} {
				mustWrite(t, filepath.Join(workspace, "config", file), "model: ignored-yaml\n")
			}
			t.Setenv("NIUMA_ENV", "dev")
			for _, cwd := range []string{"", workspace, filepath.Join(workspace, "nested")} {
				for label, loaded := range map[string]LoadedSettings{"runtime": LoadSettings(cwd), "owned": LoadOwnedSettings(cwd)} {
					if loaded.Model != "global-model" || !reflect.DeepEqual(loaded.Sources, []string{global}) || !reflect.DeepEqual(loaded.Permissions.Allow, []string{"Read"}) {
						t.Errorf("%s cwd=%q: unexpected configuration: %+v", label, cwd, loaded)
					}
				}
				inspected, sources := InspectSettings(cwd)
				if inspected.Model != "global-model" || sources["model"] != global {
					t.Errorf("inspection selected old source: %+v", sources)
				}
				if got := ResolveModel(cwd, ""); got != "global-model" {
					t.Errorf("model bypass: %q", got)
				}
			}
			if got := ResolveModel(workspace, "explicit-model"); got != "explicit-model" {
				t.Fatalf("explicit model lost: %q", got)
			}
		})
	}
}

func TestMissingOrInvalidGlobalSettingsNeverFallBack(t *testing.T) {
	for _, body := range []string{"", "invalid-json"} {
		t.Run(body, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(home, ".niuma"))
			if body != "" {
				mustWrite(t, filepath.Join(home, ".niuma", "settings.json"), body)
			}
			mustWrite(t, filepath.Join(home, ".niuma-legacy", "settings.json"), `{"model":"legacy"}`)
			mustWrite(t, filepath.Join(workspace, ".niuma", "settings.json"), `{"model":"project"}`)
			mustWrite(t, filepath.Join(workspace, "config", "config.yaml"), "model: yaml\n")
			for _, loaded := range []LoadedSettings{LoadSettings(workspace), LoadOwnedSettings(workspace)} {
				if loaded.Model != "" || len(loaded.Sources) != 0 {
					t.Fatalf("unexpected fallback: %+v", loaded)
				}
			}
			if loaded := LoadProjectConfigSettings(workspace); !reflect.DeepEqual(loaded, Settings{}) {
				t.Fatalf("project bypass remains: %+v", loaded)
			}
		})
	}
}
