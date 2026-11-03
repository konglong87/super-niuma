package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSessionGoldenExportMarkdown(t *testing.T) {
	entries := goldenSessionEntries()
	assertSessionGolden(t, "export_markdown.md", ExportMarkdown(entries))
}

func TestSessionGoldenCompactSummary(t *testing.T) {
	store := Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range goldenSessionEntries() {
		if err := recorder.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Compact(recorder.Path, 1024); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalSessionGolden(t, normalizeSessionEntries(entries))
	assertSessionGolden(t, "compact_summary.json", got)
}

func goldenSessionEntries() []Entry {
	return []Entry{
		{Type: "message", Role: "user", Content: "please inspect README"},
		{Type: "tool_call", ToolID: "toolu_read", ToolName: "Read", Content: `{"file_path":"README.md"}`},
		{Type: "tool_result", ToolID: "toolu_read", ToolName: "Read", Content: "# Project"},
		{Type: "message", Role: "assistant", Content: "README starts with a project heading."},
		{Type: "usage", Model: "claude-sonnet-4-6", InputTokens: 8, OutputTokens: 6},
	}
}

func normalizeSessionEntries(entries []Entry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		record := map[string]any{
			"timestamp": "<timestamp>",
			"type":      entry.Type,
		}
		if entry.Role != "" {
			record["role"] = entry.Role
		}
		if entry.Content != "" {
			record["content"] = entry.Content
		}
		if entry.ToolID != "" {
			record["tool_id"] = entry.ToolID
		}
		if entry.ToolName != "" {
			record["tool_name"] = entry.ToolName
		}
		if entry.Model != "" {
			record["model"] = entry.Model
		}
		if entry.InputTokens != 0 {
			record["input_tokens"] = entry.InputTokens
		}
		if entry.OutputTokens != 0 {
			record["output_tokens"] = entry.OutputTokens
		}
		out = append(out, record)
	}
	return out
}

func marshalSessionGolden(t *testing.T, value any) string {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func assertSessionGolden(t *testing.T, name string, got string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "golden", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	want := strings.TrimRight(string(data), "\n")
	got = strings.TrimRight(got, "\n")
	if got != want {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
