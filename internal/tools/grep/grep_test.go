package grep

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestGrepTool(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "a.go"), "package main\nfunc main() {}\n")
	mustWrite(t, filepath.Join(tmp, "a.txt"), "func hidden\n")

	input, _ := json.Marshal(map[string]string{
		"pattern":     "func",
		"glob":        "**/*.go",
		"output_mode": "content",
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go:2:func main()") || strings.Contains(res.Content, "a.txt") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestGrepToolContext(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("one\ntwo\nthree\nfour\n"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"pattern": "three", "glob": "**/*.go", "output_mode": "content", "before_context": 1, "after_context": 1})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, ":2:two") || !strings.Contains(res.Content, ":3:three") || !strings.Contains(res.Content, ":4:four") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestGrepOutputModes(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "a.txt"), "alpha\nbeta\nalpha\n")
	mustWrite(t, filepath.Join(tmp, "b.txt"), "alpha\n")

	input, _ := json.Marshal(map[string]string{
		"pattern":     "alpha",
		"output_mode": "files_with_matches",
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError || !strings.Contains(res.Content, "a.txt") || !strings.Contains(res.Content, "b.txt") || strings.Contains(res.Content, ":1:") {
		t.Fatalf("files result = %+v", res)
	}
	input, _ = json.Marshal(map[string]string{
		"pattern":     "alpha",
		"output_mode": "count",
	})
	res = New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError || !strings.Contains(res.Content, "a.txt:2") || !strings.Contains(res.Content, "b.txt:1") || !strings.Contains(res.Content, "Found 3 total occurrences across 2 files.") {
		t.Fatalf("count result = %+v", res)
	}
}

func TestGrepDefaultModeReturnsFilesWithMatches(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "a.go"), "package main\nfunc main() {}\n")

	input, _ := json.Marshal(map[string]string{"pattern": "func"})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Found 1 file\n") || !strings.Contains(res.Content, "a.go") || strings.Contains(res.Content, ":2:") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestGrepSkipsManagedAgentWorktrees(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "internal", "query.go"), "package internal\nfunc runtimeTodoStatus() {}\n")
	mustWrite(t, filepath.Join(tmp, ".claude", "worktrees", "agent-old", "internal", "query.go"), "package stale\nfunc runtimeTodoStatus() {}\n")

	input, _ := json.Marshal(map[string]string{
		"pattern":     "runtimeTodoStatus",
		"output_mode": "content",
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "internal/query.go") {
		t.Fatalf("content missing real match: %q", res.Content)
	}
	if strings.Contains(res.Content, ".claude/worktrees") || strings.Contains(res.Content, "package stale") {
		t.Fatalf("content includes managed worktree match: %q", res.Content)
	}
}

func TestGrepClaudeStyleAliasesAndPagination(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "main.go"), "one\ntwo\nTHREE\nfour\nfive\n")
	mustWrite(t, filepath.Join(tmp, "other.go"), "three\n")

	input, _ := json.Marshal(map[string]any{
		"pattern":     "three",
		"output_mode": "content",
		"type":        "go",
		"-i":          true,
		"-B":          1,
		"-A":          1,
		"head_limit":  2,
		"offset":      1,
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if strings.Contains(res.Content, "one") || !strings.Contains(res.Content, "main.go:3:THREE") || !strings.Contains(res.Content, "pagination = limit: 2, offset: 1") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestGrepLineNumbersCanBeDisabled(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "main.go"), "alpha\n")

	input, _ := json.Marshal(map[string]any{"pattern": "alpha", "output_mode": "content", "-n": false})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if strings.Contains(res.Content, ":1:") || !strings.Contains(res.Content, "main.go:alpha") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestGrepMultilineMatchesAcrossLines(t *testing.T) {
	tmp := t.TempDir()
	mustWrite(t, filepath.Join(tmp, "main.go"), "type Options struct {\n\tName string\n}\n")

	input, _ := json.Marshal(map[string]any{"pattern": "Options struct \\{.*Name", "output_mode": "content", "multiline": true})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go:1:type Options struct") {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestGrepToolResultLimitMatchesClaudeCode(t *testing.T) {
	if got := New().MaxResultSizeChars(); got != 20_000 {
		t.Fatalf("MaxResultSizeChars() = %d, want 20000", got)
	}
}

func TestGrepSchemaCarriesClaudeCodeCompatibleMetadata(t *testing.T) {
	schema := string(New().InputSchema())
	for _, want := range []string{
		`"$schema"`,
		`"type": "number"`,
		`File or directory to search in (rg PATH). Defaults to current working directory.`,
		`maps to rg --glob`,
		`head -N`,
		`rg -U --multiline-dotall`,
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
	for _, notWant := range []string{
		`"type": "integer", "description": "Number of lines`,
		`File or directory to search in. Defaults to current working directory.`,
	} {
		if strings.Contains(schema, notWant) {
			t.Fatalf("schema still contains old metadata %q:\n%s", notWant, schema)
		}
	}
}

func TestGrepDescriptionEncouragesSearchConvergence(t *testing.T) {
	desc := New().Description()
	for _, want := range []string{
		`output_mode:"files_with_matches"`,
		`"count"`,
		"candidate files",
		"Read only the few confirmed files",
		"enough file/function/line evidence",
		"synthesize the answer",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
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
