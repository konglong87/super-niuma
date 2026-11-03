package fileread

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestReadTool(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"file_path": "hello.txt"})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "hello\n") {
		t.Fatalf("content = %q, want hello prefix", res.Content)
	}
	if !strings.Contains(res.Content, "Whenever you read a file") {
		t.Fatalf("content missing read safety reminder: %q", res.Content)
	}
}

func TestReadToolRequiresFilePath(t *testing.T) {
	res := New().Run(context.Background(), json.RawMessage(`{"arguments":"{\"file_path\":\"a.go\"}{\"file_path\":\"b.go\"}"}`), tools.Context{CWD: t.TempDir()})
	if !res.IsError || !strings.Contains(res.Content, "file_path is required") {
		t.Fatalf("Run() = %+v, want file_path error", res)
	}
}

func TestReadToolDefaultsToFirstTwoThousandLines(t *testing.T) {
	tmp := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 2001; i++ {
		fmt.Fprintf(&b, "line-%04d\n", i)
	}
	if err := os.WriteFile(filepath.Join(tmp, "long.txt"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"file_path": "long.txt"})

	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "line-2000") || strings.Contains(res.Content, "line-2001") {
		t.Fatalf("content did not apply default 2000-line window: %.200q", res.Content[len(res.Content)-min(len(res.Content), 300):])
	}
}

func TestReadToolOffsetLimit(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"file_path": "hello.txt", "offset": 2, "limit": 1})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "two\n") {
		t.Fatalf("content = %q, want two prefix", res.Content)
	}
	if !strings.Contains(res.Content, "Whenever you read a file") {
		t.Fatalf("content missing read safety reminder: %q", res.Content)
	}
}

func TestReadToolLineNumbers(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"file_path": "hello.txt", "offset": 2, "limit": 2, "line_numbers": true})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "     2: two\n     3: three\n") {
		t.Fatalf("content = %q", res.Content)
	}
	if !strings.Contains(res.Content, "Whenever you read a file") {
		t.Fatalf("content missing read safety reminder: %q", res.Content)
	}
}

func TestReadToolBinarySummary(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "payload.bin"), []byte{0x00, 0x01, 0x02, 'x'}, 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"file_path": "payload.bin"})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Binary file detected:") || !strings.Contains(res.Content, "mime_type: application/octet-stream") {
		t.Fatalf("content = %q", res.Content)
	}
	if !strings.Contains(res.Content, "Whenever you read a file") {
		t.Fatalf("content missing read safety reminder: %q", res.Content)
	}
}

func TestReadDescriptionEncouragesPathConfirmationAndConvergence(t *testing.T) {
	desc := New().Description()
	for _, want := range []string{
		"confirmed by",
		"LS, Glob, Grep",
		"verify it before",
		"smallest useful line range",
		"enough file/function/line evidence to answer",
		"pages parameter",
		"PDF page",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}

func TestReadSchemaIncludesClaudeCompatiblePagesField(t *testing.T) {
	schema := string(New().InputSchema())
	for _, want := range []string{`"pages"`, "Page range for PDF files"} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
}

func TestClaudeCompatibleReadSchemaHidesGoOnlyFields(t *testing.T) {
	t.Setenv("NIUMA_PROMPT_PROFILE", "claude-compatible")
	schema := string(New().InputSchema())
	for _, want := range []string{
		`"$schema"`,
		`"file_path"`,
		`"offset"`,
		`"minimum": 0`,
		`"maximum": 9007199254740991`,
		`"exclusiveMinimum": 0`,
		`"limit"`,
		`"pages"`,
		"Maximum 20 pages per request",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
	for _, notWant := range []string{`"chunk_index"`, `"byte_offset"`, `"byte_limit"`, `"line_numbers"`} {
		if strings.Contains(schema, notWant) {
			t.Fatalf("compatible schema leaked %q:\n%s", notWant, schema)
		}
	}
	desc := New().Description()
	for _, notWant := range []string{"chunk_index", "byte_offset", "byte_limit", "line_numbers:true"} {
		if strings.Contains(desc, notWant) {
			t.Fatalf("compatible description leaked %q:\n%s", notWant, desc)
		}
	}
}

func TestClaudeCompatibleReadDefaultsToLineNumberedOutput(t *testing.T) {
	t.Setenv("NIUMA_PROMPT_PROFILE", "claude-compatible")
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"file_path": "hello.txt"})

	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "     1: one\n     2: two\n") {
		t.Fatalf("compatible content = %q", res.Content)
	}
}

func TestClaudeCompatibleStrictReadUsesCompatibleSchemaAndOutput(t *testing.T) {
	t.Setenv("NIUMA_PROMPT_PROFILE", "claude-compatible-strict")
	schema := string(New().InputSchema())
	for _, notWant := range []string{`"chunk_index"`, `"byte_offset"`, `"byte_limit"`, `"line_numbers"`} {
		if strings.Contains(schema, notWant) {
			t.Fatalf("strict compatible schema leaked %q:\n%s", notWant, schema)
		}
	}

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"file_path": "hello.txt"})

	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})

	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "     1: one\n     2: two\n") {
		t.Fatalf("strict compatible content = %q", res.Content)
	}
}

func TestReadPartialFooterMoreBelow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("l1\nl2\nl3\nl4\nl5\n"), 0644); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]any{"file_path": p, "offset": 2, "limit": 2})
	res := New().Run(context.Background(), in, tools.Context{CWD: dir})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "read lines 2-3") || !strings.Contains(res.Content, "more") {
		t.Fatalf("footer missing/wrong: %q", res.Content)
	}
}

func TestReadFullNoFooter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("l1\nl2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]any{"file_path": p})
	res := New().Run(context.Background(), in, tools.Context{CWD: dir})
	if strings.Contains(res.Content, "read lines") {
		t.Fatalf("full read should have no partial footer: %q", res.Content)
	}
}

// TestReadPartialFooterCountsTrailingBlankLines guards against countReadLines
// undercounting a scoped read whose returned chunk ends on one or more blank
// lines. The file has 4 real lines: "l1", "l2", "", "" (l2 then two trailing
// blanks). Reading offset=2, limit=3 selects lines 2-4 ("l2", "", ""). Before
// the fix, countReadLines("l2\n\n") collapsed to 1 (strings.TrimRight strips
// the whole trailing run), reporting "read lines 2-2" — silently
// undercounting by 2 lines. After the fix, it correctly counts 2 embedded
// newlines and does not collapse the trailing run, reporting "read lines
// 2-3": the footer is present and the undercount is bounded to 1 instead of
// collapsing toward 0/1, regardless of how many trailing blank lines there
// are (see the reviewer-confirmed bug report for detail on why a fully exact
// count is a separate, out-of-scope change).
func TestReadPartialFooterCountsTrailingBlankLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("l1\nl2\n\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]any{"file_path": p, "offset": 2, "limit": 3})
	res := New().Run(context.Background(), in, tools.Context{CWD: dir})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "[read lines 2-3;") {
		t.Fatalf("footer missing or undercounted trailing blank lines, want \"read lines 2-3\": %q", res.Content)
	}
	if strings.Contains(res.Content, "read lines 2-2") {
		t.Fatalf("footer regressed to the pre-fix collapsed range 2-2: %q", res.Content)
	}
}

func TestReadPagesReturnsExplicitBoundary(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"file_path": "hello.txt", "pages": "1-2"})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError || !strings.Contains(res.Content, "pages is only supported for PDF files") {
		t.Fatalf("text pages result = %+v", res)
	}

	if err := os.WriteFile(filepath.Join(tmp, "doc.pdf"), []byte("%PDF-1.4"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ = json.Marshal(map[string]string{"file_path": "doc.pdf", "pages": "1-2"})
	res = New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError || !strings.Contains(res.Content, "PDF page range reading via pages is not implemented") {
		t.Fatalf("pdf pages result = %+v", res)
	}
}
