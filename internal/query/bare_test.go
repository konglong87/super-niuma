package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestBareProfileUsesMinimalPromptAndDisablesImplicitContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("implicit workspace memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := New(nil, tools.NewRegistry(), Options{CWD: cwd, RuntimeProfile: runtimeprofile.ProfileBare})
	parts := session.defaultSystemPromptParts()
	joined := strings.Join(parts, "\n")
	if len(parts) != 1 || !strings.Contains(joined, "You are niuma") || !strings.Contains(joined, "CWD: "+cwd) || strings.Contains(joined, "Git Snapshot") {
		t.Fatalf("prompt parts = %q", joined)
	}
	assembly := session.assembleContextMessages(t.Context(), "hello")
	if len(assembly.codeDocs) != 0 || len(assembly.userMessages) != 0 || assembly.systemMemory != "" {
		t.Fatalf("assembly = %+v", assembly)
	}
}

func TestBareProfileLoadsExplicitContextAndDisablesHooksAndAutoMemoryRoot(t *testing.T) {
	cwd := t.TempDir()
	explicit := t.TempDir()
	if err := os.WriteFile(filepath.Join(explicit, "CLAUDE.md"), []byte("explicit context"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := New(nil, tools.NewRegistry(), Options{
		CWD:                  cwd,
		RuntimeProfile:       runtimeprofile.ProfileBare,
		ExplicitContextRoots: []string{explicit},
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.UserPromptSubmit: {{Command: "must-not-run"}},
		}),
	})
	assembly := session.assembleContextMessages(t.Context(), "hello")
	if len(assembly.codeDocs) != 1 || !strings.Contains(assembly.codeDocs[0].Content, "explicit context") {
		t.Fatalf("docs = %+v", assembly.codeDocs)
	}
	if session.hasHookEvent(hooks.UserPromptSubmit) {
		t.Fatal("bare session exposed hooks")
	}
	if len(session.options.WritableRoots) != 0 {
		t.Fatalf("auto memory root leaked: %v", session.options.WritableRoots)
	}
}

func TestBareContextManifestSeparatesPromptAndRuntimeProfiles(t *testing.T) {
	session := New(nil, tools.NewRegistry(), Options{CWD: t.TempDir(), RuntimeProfile: runtimeprofile.ProfileBare, PromptMode: "code"})
	manifest := session.contextManifest(session.effectiveSystemBlocks(), contextAssembly{}, SkillsCatalogManifest{}, TenantSkillInlineManifest{})
	if manifest.Profile != "code" || manifest.RuntimeProfile != "bare" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestUnknownRuntimeProfileFailsClosedInDirectQueryConstruction(t *testing.T) {
	registry := tools.NewRegistry(namedQueryTool{name: "Read"})
	session := New(nil, registry, Options{
		CWD:            t.TempDir(),
		RuntimeProfile: runtimeprofile.Profile("unexpected"),
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.UserPromptSubmit: {{Command: "must-not-run"}},
		}),
	})
	if len(session.ToolDefinitions()) != 0 || session.hasHookEvent(hooks.UserPromptSubmit) || len(session.options.WritableRoots) != 0 {
		t.Fatalf("unknown profile did not fail closed: tools=%v hooks=%v roots=%v", session.ToolDefinitions(), session.hasHookEvent(hooks.UserPromptSubmit), session.options.WritableRoots)
	}
}
