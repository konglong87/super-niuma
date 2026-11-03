package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportClaudeCodeBuildsResumableV2Session(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	store := Store{TranscriptProjectsRoot: t.TempDir()}

	native := []string{
		`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"s","message":{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"README.md"}}]}}`,
		`{"type":"user","uuid":"u2","parentUuid":"a1","sessionId":"s","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file body","is_error":false}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"u2","sessionId":"s","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"user","uuid":"side1","parentUuid":"a1","sessionId":"s","isSidechain":true,"message":{"role":"user","content":"sidechain work"}}`,
		`{"type":"system","uuid":"sys1","content":"system note"}`,
	}
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "native.jsonl")
	if err := os.WriteFile(srcPath, []byte(joinLines(native)), 0600); err != nil {
		t.Fatal(err)
	}
	srcInfo, _ := os.Stat(srcPath)

	result, err := ImportClaudeCode(store, srcPath, "/tmp/importedapp")
	if err != nil {
		t.Fatal(err)
	}

	// Source is untouched (read-only import).
	afterInfo, _ := os.Stat(srcPath)
	if afterInfo.ModTime() != srcInfo.ModTime() || afterInfo.Size() != srcInfo.Size() {
		t.Fatal("import must not modify the source transcript")
	}

	// Counts: 3 messages (hello, hi, done), 1 tool call, 1 tool result.
	if result.Messages != 3 || result.ToolCalls != 1 || result.ToolResults != 1 {
		t.Fatalf("import counts wrong: %+v", result)
	}
	// The sidechain and the system line are reported as skipped.
	if result.Skipped["sidechain"] != 1 {
		t.Fatalf("expected 1 skipped sidechain, got %+v", result.Skipped)
	}
	if result.Skipped["system"] != 1 {
		t.Fatalf("expected 1 skipped system line, got %+v", result.Skipped)
	}

	// The imported transcript is a resumable v2 message graph.
	entries, format, err := LoadWithFormat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV2 {
		t.Fatalf("imported format = %s, want v2", format)
	}
	if err := ValidateResumeFormat(format); err != nil {
		t.Fatalf("imported transcript must be resumable: %v", err)
	}
	chain := CurrentChain(entries)
	// The main chain, in order, holds hello / hi / tool_call / tool_result / done.
	var order []string
	for _, e := range chain {
		switch e.Type {
		case "message":
			order = append(order, e.Role+":"+e.Content)
		case "tool_call":
			order = append(order, "tool_call:"+e.ToolID)
		case "tool_result":
			order = append(order, "tool_result:"+e.ToolID)
		}
	}
	want := []string{"user:hello", "assistant:hi", "tool_call:toolu_1", "tool_result:toolu_1", "assistant:done"}
	if len(order) != len(want) {
		t.Fatalf("chain = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("chain[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
	// The sidechain content never entered the imported conversation.
	for _, e := range chain {
		if e.Content == "sidechain work" {
			t.Fatal("sidechain content must not be imported into the main chain")
		}
	}
}

func TestImportClaudeCodeRejectsEmpty(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	store := Store{TranscriptProjectsRoot: t.TempDir()}
	srcPath := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(srcPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportClaudeCode(store, srcPath, "/tmp/x"); err == nil {
		t.Fatal("expected an error importing an empty transcript")
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
