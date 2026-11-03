package session

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// numberedTurns builds n user/assistant message pairs labelled turn-001 upward,
// each padded so a small byte budget can only hold the last few.
func numberedTurns(n int) []Entry {
	entries := make([]Entry, 0, n)
	for i := 1; i <= n; i++ {
		entries = append(entries, Entry{
			Type:    "message",
			Role:    "user",
			Content: fmt.Sprintf("turn-%03d %s", i, strings.Repeat("x", 60)),
		})
	}
	return entries
}

func TestBuildCompactSummaryKeepsMostRecentTurns(t *testing.T) {
	entries := numberedTurns(40)
	// Room for roughly five turns, so what survives is unambiguous.
	summary := BuildCompactSummary(entries, 500)
	// Compaction exists to make room for the conversation to continue: the recent
	// turns are the ones still needed, the oldest are what may be dropped.
	for _, want := range []string{"turn-040", "turn-039"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary dropped the most recent turn %s (compaction is keeping the wrong end):\n%s", want, summary)
		}
	}
	for _, unwanted := range []string{"turn-001", "turn-002"} {
		if strings.Contains(summary, unwanted) {
			t.Fatalf("summary kept the oldest turn %s instead of recent context:\n%s", unwanted, summary)
		}
	}
	// Dropping turns must be stated, not silent.
	if !strings.Contains(summary, "omitted") {
		t.Fatalf("summary dropped older turns without saying so:\n%s", summary)
	}
	if len(summary) > 500 {
		t.Fatalf("summary is %d bytes, want <= 500", len(summary))
	}
}

func TestBuildCompactSummaryKeepsTurnsInChronologicalOrder(t *testing.T) {
	summary := BuildCompactSummary(numberedTurns(40), 500)
	prev := -1
	for i := 1; i <= 40; i++ {
		idx := strings.Index(summary, fmt.Sprintf("turn-%03d", i))
		if idx < 0 {
			continue
		}
		if idx < prev {
			t.Fatalf("turn-%03d appears before an earlier turn; the kept tail must stay in order:\n%s", i, summary)
		}
		prev = idx
	}
	if prev < 0 {
		t.Fatal("no turns survived at all")
	}
}

func TestBuildCompactSummaryCarriesPreviousSummaryAndRecentTail(t *testing.T) {
	entries := []Entry{
		{Type: "message", Role: "user", Content: "ANCIENT-ALREADY-SUMMARIZED"},
		{Type: "compact_summary", Content: "PREVIOUS-SUMMARY-TEXT"},
	}
	entries = append(entries, numberedTurns(30)...)
	summary := BuildCompactSummary(entries, 500)
	if !strings.Contains(summary, "PREVIOUS-SUMMARY-TEXT") {
		t.Fatalf("previous summary must stay as the header:\n%s", summary)
	}
	if strings.Contains(summary, "ANCIENT-ALREADY-SUMMARIZED") {
		t.Fatalf("entries already folded into the previous summary must not be re-rendered:\n%s", summary)
	}
	if !strings.Contains(summary, "turn-030") {
		t.Fatalf("summary dropped the most recent turn after a previous summary:\n%s", summary)
	}
}

func TestBuildCompactSummaryKeepsHeadOfAnOversizedNewestEntry(t *testing.T) {
	entries := []Entry{
		{Type: "message", Role: "user", Content: "OLD-QUESTION"},
		{Type: "tool_result", ToolName: "Read", Content: "NEEDLE-AT-HEAD " + strings.Repeat("y", 4000)},
	}
	summary := BuildCompactSummary(entries, 400)
	if len(summary) > 400 {
		t.Fatalf("summary is %d bytes, want <= 400", len(summary))
	}
	// A single newest entry larger than the whole budget must still leave a
	// usable head, not an empty summary.
	if !strings.Contains(summary, "NEEDLE-AT-HEAD") {
		t.Fatalf("oversized newest entry was dropped entirely:\n%s", summary)
	}
}

func TestBuildCompactSummaryRendersEverythingThatFits(t *testing.T) {
	entries := []Entry{
		{Type: "message", Role: "user", Content: "please inspect README"},
		{Type: "tool_call", ToolName: "Read", Content: `{"file_path":"README.md"}`},
		{Type: "tool_result", ToolName: "Read", Content: "# Project"},
		{Type: "message", Role: "assistant", Content: "README starts with a project heading."},
		{Type: "usage", Model: "claude-sonnet-4-6", InputTokens: 8},
	}
	summary := BuildCompactSummary(entries, 1024)
	want := "Conversation summary:\nUSER: please inspect README\nTOOL CALL Read: {\"file_path\":\"README.md\"}\nTOOL RESULT Read: # Project\nASSISTANT: README starts with a project heading."
	if summary != want {
		t.Fatalf("summary =\n%q\nwant\n%q", summary, want)
	}
}

func TestCompactEntriesPersistsSummaryProvenanceWithoutSourceContent(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	entries := []Entry{
		{ID: "user-1", Type: "message", Role: "user", Content: "inspect the project"},
		{ID: "tool-1", Type: "tool_result", ToolName: "Read", Content: "large evidence body"},
		{ID: "assistant-1", Type: "message", Role: "assistant", Content: "the project is healthy"},
	}
	if err := writeEntries(path, entries); err != nil {
		t.Fatal(err)
	}
	entry, err := CompactEntries(path, entries, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var provenance SummaryProvenance
	if err := json.Unmarshal(entry.CompactMetadata, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Version != 1 || provenance.SourceStartID != "user-1" || provenance.SourceEndID != "assistant-1" {
		t.Fatalf("provenance boundaries = %+v", provenance)
	}
	if provenance.SourceEntryCount != len(entries) || provenance.SourceDigest == "" {
		t.Fatalf("provenance coverage = %+v", provenance)
	}
	if !reflect.DeepEqual(provenance.SourceEntryIDs, []string{"assistant-1", "tool-1", "user-1"}) {
		t.Fatalf("referenced entry IDs = %#v", provenance.SourceEntryIDs)
	}
	if strings.Contains(string(entry.CompactMetadata), "large evidence body") {
		t.Fatal("compact metadata copied source content")
	}
}
