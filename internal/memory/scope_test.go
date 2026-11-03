package memory

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A pattern is anchored at the repo root, but substring matching accepted it
// anywhere in the prompt -- so `api/**` loaded for a turn that only touched
// docs/api/README.md.
func TestLoadCodeDoesNotMatchPatternAsBareSubstring(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "rules", "api.md"), "---\npaths: [api/**]\n---\napi package rule")

	docs, err := LoadCode(project, "update docs/api/README.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(documentContents(docs), "\n"); strings.Contains(got, "api package rule") {
		t.Fatalf("root-anchored pattern matched a nested path:\n%s", got)
	}
}

// "internal/memory" is a substring of "internal/memory-mapped/cache.go", which
// is how substring matching leaked memories into unrelated turns.
func TestLoadCodeDoesNotMatchPathPrefixAcrossSegmentBoundaries(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "rules", "memory.md"), "---\npaths: [internal/memory/**]\n---\nmemory package rule")

	docs, err := LoadCode(project, "rework internal/memory-mapped/cache.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(documentContents(docs), "\n"); strings.Contains(got, "memory package rule") {
		t.Fatalf("segment-crossing prefix matched:\n%s", got)
	}
}

// Wildcards in the middle of a pattern were never honoured: the old matcher only
// stripped a trailing /** or /* and then looked for the rest as a substring, so
// internal/**/*_test.go could not match anything.
func TestLoadCodeHonoursDoubleStarAndSuffixGlobs(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "rules", "tests.md"), "---\npaths: [internal/**/*_test.go]\n---\ntest file rule")

	docs, err := LoadCode(project, "update internal/memory/memory_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(documentContents(docs), "\n"); !strings.Contains(got, "test file rule") {
		t.Fatalf("mid-pattern ** did not match:\n%s", got)
	}

	docs, err = LoadCode(project, "update internal/memory/memory.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(documentContents(docs), "\n"); strings.Contains(got, "test file rule") {
		t.Fatalf("*_test.go matched a non-test file:\n%s", got)
	}
}

// A turn with no identifiable files must not hide guidance -- the frontmatter is
// a scope hint, and an unknown scope is not evidence of a mismatch.
func TestLoadCodeKeepsPathScopedDocumentsWhenNoFilesAreKnown(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	mustWrite(t, filepath.Join(project, ".claude", "rules", "memory.md"), "---\npaths: [internal/memory/**]\n---\nmemory package rule")

	docs, err := LoadCode(project, "what does this project do?")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(documentContents(docs), "\n"); !strings.Contains(got, "memory package rule") {
		t.Fatalf("path-scoped doc was hidden although no file was identifiable:\n%s", got)
	}
}

func TestPromptFileCandidatesExtractsPathsNotProse(t *testing.T) {
	got := promptFileCandidates("Fix `internal/memory/memory.go:697` and ./docs/todo.md, then check @web/src/App.tsx (see https://example.com/a/b.go)")
	want := []string{"internal/memory/memory.go", "docs/todo.md", "web/src/App.tsx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
}

func TestPromptFileCandidatesIgnoresProseWithoutPaths(t *testing.T) {
	if got := promptFileCandidates("please make the retrieval faster and less noisy"); len(got) != 0 {
		t.Fatalf("candidates = %q, want none", got)
	}
}

func TestPathMatchesPatternSubtreeAndExactForms(t *testing.T) {
	cases := []struct {
		pattern string
		value   string
		want    bool
	}{
		{"**", "anything/at/all.go", true},
		{"internal/memory/**", "internal/memory/memory.go", true},
		{"internal/memory/**", "internal/memory", true},
		{"internal/memory/**", "internal/memory-mapped/cache.go", false},
		{"internal/memory", "internal/memory/memory.go", true},
		{"go.mod", "go.mod", true},
		{"go.mod", "internal/go.mod", false},
		{"internal/**/*_test.go", "internal/memory/memory_test.go", true},
		{"internal/**/*_test.go", "internal/memory/memory.go", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false},
	}
	for _, tc := range cases {
		if got := pathMatchesPattern(tc.pattern, tc.value); got != tc.want {
			t.Fatalf("pathMatchesPattern(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}
