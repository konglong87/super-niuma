package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscriptImportClaudeCodeCommand(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))

	native := strings.Join([]string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}`,
	}, "\n") + "\n"
	srcPath := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(srcPath, []byte(native), 0600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := transcriptCommand([]string{"import-claude-code", srcPath, "--cwd", "/tmp/importapp"}, "/tmp/importapp", &out); err != nil {
		t.Fatalf("import command: %v", err)
	}
	if !strings.Contains(out.String(), "Imported") || !strings.Contains(out.String(), "Messages: 2") {
		t.Fatalf("unexpected import output: %s", out.String())
	}

	// Unknown subcommand and missing path are clear errors.
	if err := transcriptCommand([]string{"bogus"}, "/tmp/x", &out); err == nil {
		t.Fatal("expected error for unknown transcript subcommand")
	}
	if err := transcriptCommand([]string{"import-claude-code"}, "/tmp/x", &out); err == nil {
		t.Fatal("expected error for missing transcript path")
	}
}
