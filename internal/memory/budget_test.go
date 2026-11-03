package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The byte budget only ever applied to Type=="Workflow", so a huge CLAUDE.md,
// user memory or managed policy went into every prompt in full.
func TestPreparePromptDocumentsTruncatesLargeProjectMemory(t *testing.T) {
	t.Setenv("NIUMA_MEMORY_DOCUMENT_BUDGET_BYTES", "2048")
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	doc := Document{Path: path, Type: "Project", Content: "# Rules\n" + strings.Repeat("- keep this project tidy.\n", 4000)}

	promptDocs, report := PreparePromptDocuments([]Document{doc})
	got := promptDocs[0].Content
	if len(got) > 2048 {
		t.Fatalf("prompt bytes = %d, want <= 2048", len(got))
	}
	if !strings.Contains(got, memoryTruncationMarker) {
		t.Fatalf("truncated document carries no marker:\n%s", got)
	}
	if !strings.Contains(got, path) {
		t.Fatalf("truncation marker does not name the file to read:\n%s", got)
	}
	if !strings.Contains(got, "# Rules") {
		t.Fatalf("truncation dropped the opening section:\n%s", got)
	}
	if report.BudgetedDocuments != 1 || report.PromptBytes >= report.OriginalBytes {
		t.Fatalf("report = %+v", report)
	}
	if !report.Documents[0].Budgeted || report.Documents[0].PromptBytes != len(got) {
		t.Fatalf("document summary = %+v", report.Documents[0])
	}
}

// Every document type is subject to the budget, not just workflows.
func TestPreparePromptDocumentsTruncatesEveryDocumentType(t *testing.T) {
	t.Setenv("NIUMA_MEMORY_DOCUMENT_BUDGET_BYTES", "1024")
	t.Setenv("NIUMA_MEMORY_PROMPT_BUDGET_BYTES", "0")
	body := strings.Repeat("x", 8000)
	var docs []Document
	for _, typ := range []string{"Managed", "User", "Project", "Local", "Team", "Auto", "ClaudeCodeProjectMemory"} {
		docs = append(docs, Document{Path: typ + ".md", Type: typ, Content: body})
	}

	promptDocs, report := PreparePromptDocuments(docs)
	for i, doc := range promptDocs {
		if len(doc.Content) > 1024 {
			t.Fatalf("%s kept %d bytes, want <= 1024", doc.Type, len(doc.Content))
		}
		if !strings.Contains(doc.Content, memoryTruncationMarker) {
			t.Fatalf("%s (index %d) was truncated without a marker", doc.Type, i)
		}
	}
	if report.BudgetedDocuments != len(docs) {
		t.Fatalf("budgeted documents = %d, want %d", report.BudgetedDocuments, len(docs))
	}
}

// Without a total cap, N documents each under the per-document cap still blow up
// the prompt.
func TestPreparePromptDocumentsEnforcesTotalBudget(t *testing.T) {
	t.Setenv("NIUMA_MEMORY_DOCUMENT_BUDGET_BYTES", "1024")
	t.Setenv("NIUMA_MEMORY_PROMPT_BUDGET_BYTES", "2048")
	var docs []Document
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md", "e.md"} {
		docs = append(docs, Document{Path: name, Type: "Project", Content: strings.Repeat("y", 4000)})
	}

	promptDocs, report := PreparePromptDocuments(docs)
	if len(promptDocs) != len(docs) {
		t.Fatalf("documents were dropped: %d", len(promptDocs))
	}
	if !strings.Contains(promptDocs[0].Content, strings.Repeat("y", 100)) {
		t.Fatalf("the highest-priority document lost its content:\n%s", promptDocs[0].Content)
	}
	for i, doc := range promptDocs {
		if !strings.Contains(doc.Content, memoryTruncationMarker) {
			t.Fatalf("document %d has no marker after the budget ran out:\n%s", i, doc.Content)
		}
	}
	if report.TotalBudgetBytes != 2048 {
		t.Fatalf("report.TotalBudgetBytes = %d", report.TotalBudgetBytes)
	}
	// Once the budget is exhausted the remaining documents shrink to the marker,
	// so the overall prompt stays within a small multiple of the cap instead of
	// scaling with the files on disk.
	if report.PromptBytes > 2048+len(docs)*400 {
		t.Fatalf("prompt bytes = %d, want the total budget to bind", report.PromptBytes)
	}
}

func TestPreparePromptDocumentsTruncationIsRuneSafe(t *testing.T) {
	t.Setenv("NIUMA_MEMORY_DOCUMENT_BUDGET_BYTES", "512")
	doc := Document{Path: "CLAUDE.md", Type: "Project", Content: "x" + strings.Repeat("中文规则很长", 400)}

	promptDocs, _ := PreparePromptDocuments([]Document{doc})
	got := promptDocs[0].Content
	if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("truncation split a rune: %q", got)
	}
}

func TestPreparePromptDocumentsLeavesSmallDocumentsAlone(t *testing.T) {
	doc := Document{Path: "CLAUDE.md", Type: "Project", Content: "keep it short"}

	promptDocs, report := PreparePromptDocuments([]Document{doc})
	if promptDocs[0].Content != "keep it short" {
		t.Fatalf("content = %q", promptDocs[0].Content)
	}
	if report.BudgetedDocuments != 0 || report.PromptBytes != report.OriginalBytes {
		t.Fatalf("report = %+v", report)
	}
}

func TestPreparePromptDocumentsBudgetsCanBeDisabled(t *testing.T) {
	t.Setenv("NIUMA_MEMORY_DOCUMENT_BUDGET_BYTES", "0")
	t.Setenv("NIUMA_MEMORY_PROMPT_BUDGET_BYTES", "0")
	body := strings.Repeat("z", 200_000)

	promptDocs, report := PreparePromptDocuments([]Document{{Path: "CLAUDE.md", Type: "Project", Content: body}})
	if promptDocs[0].Content != body || report.BudgetedDocuments != 0 {
		t.Fatalf("budget was applied although it is disabled: %d bytes, report=%+v", len(promptDocs[0].Content), report)
	}
}

// The project memory index is capped at 25KB with a byte slice, which splits CJK
// runes at the cut.
func TestLimitProjectMemoryContentIsRuneSafe(t *testing.T) {
	// Two leading ASCII bytes push the 25KB cut off every rune boundary.
	got := limitProjectMemoryContent("xy" + strings.Repeat("中文记忆索引", 5000))
	if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("project memory cap split a rune at the end: %q", got[len(got)-40:])
	}
}
