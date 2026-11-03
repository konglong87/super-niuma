package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitDiscoveryLoadsOnlyRootsAndKeepsIncludesInsideRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("user memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "CLAUDE.md"), []byte("workspace memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "explicit")
	if err := os.MkdirAll(filepath.Join(root, ".claude", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.md"), []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.md"), []byte("inside include"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "niuma.md"), []byte("explicit memory\n@inside.md\n@../outside.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "rules", "rule.md"), []byte("explicit rule"), 0o600); err != nil {
		t.Fatal(err)
	}

	docs, err := LoadCodeWithOptions(workspace, "", LoadCodeOptions{DiscoveryMode: DiscoveryExplicit, ExplicitRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	for _, doc := range docs {
		content.WriteString(doc.Content)
		content.WriteByte('\n')
		if !sameTree(root, doc.Path) {
			t.Fatalf("document escaped explicit root: %s", doc.Path)
		}
	}
	got := content.String()
	if !strings.Contains(got, "explicit memory") || !strings.Contains(got, "inside include") || !strings.Contains(got, "explicit rule") {
		t.Fatalf("explicit documents missing: %q", got)
	}
	for _, forbidden := range []string{"outside secret", "workspace memory", "user memory"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unexpected %q in %q", forbidden, got)
		}
	}
}

func TestExplicitDiscoveryUsesConfiguredGuidanceFilename(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	root := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("NIUMA_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	mustWrite(t, filepath.Join(configDir, "settings.json"), `{
	  "identity": {"guidanceFilename": "agentx.md"}
	}`)
	mustWrite(t, filepath.Join(root, "agentx.md"), "configured explicit guidance")
	mustWrite(t, filepath.Join(root, "niuma.md"), "canonical guidance should not win")

	docs, err := LoadCodeWithOptions(t.TempDir(), "", LoadCodeOptions{
		DiscoveryMode: DiscoveryExplicit,
		ExplicitRoots: []string{root},
	})
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	for _, doc := range docs {
		content.WriteString(doc.Content)
		content.WriteByte('\n')
	}
	got := content.String()
	if !strings.Contains(got, "configured explicit guidance") {
		t.Fatalf("configured guidance was not loaded: %q", got)
	}
	if strings.Contains(got, "canonical guidance should not win") {
		t.Fatalf("canonical guidance bypassed configured identity: %q", got)
	}
}

func TestExplicitDiscoveryWithoutRootsReturnsEmpty(t *testing.T) {
	docs, err := LoadCodeWithOptions(t.TempDir(), "", LoadCodeOptions{DiscoveryMode: DiscoveryExplicit})
	if err != nil || len(docs) != 0 {
		t.Fatalf("docs=%v err=%v", docs, err)
	}
}

func TestExplicitDiscoveryRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.md")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	docs, err := LoadCodeWithOptions(root, "", LoadCodeOptions{DiscoveryMode: DiscoveryExplicit, ExplicitRoots: []string{root}})
	if err != nil || len(docs) != 0 {
		t.Fatalf("docs=%+v err=%v", docs, err)
	}
}
