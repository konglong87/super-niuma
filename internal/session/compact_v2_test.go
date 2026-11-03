package session

import "testing"

// TestV2CompactSummaryStaysOnChain proves that appending a compact_summary through
// the live recorder (as /compact does) chains it onto the active leaf AND advances
// the recorder's leaf, so the next turn continues from the summary instead of
// orphaning it on a side branch. Regression guard for v2-default compaction.
func TestV2CompactSummaryStaysOnChain(t *testing.T) {
	store := v2Store(t)
	rec, err := store.NewRecorderWithID("/tmp/compactv2", "cccccccc-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	recordTurn(t, rec, "aaaaaaaa-1111-4111-8111-111111111111", "q1", "a1")
	// Simulate /compact appending via the live recorder.
	if err := rec.Append(Entry{Type: "compact_summary", Content: "SUMMARY-OF-TURN-1"}); err != nil {
		t.Fatal(err)
	}
	recordTurn(t, rec, "bbbbbbbb-2222-4222-8222-222222222222", "q2", "a2")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	entries, format, err := LoadWithFormat(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV2 {
		t.Fatalf("format = %s, want v2", format)
	}
	chain := CurrentChain(entries)

	// Both the summary and the post-compact turn must be on the current chain: this
	// only holds if the recorder's leaf advanced to the summary.
	if !containsContentEntry(chain, "SUMMARY-OF-TURN-1") {
		t.Fatal("compact_summary must stay on the current chain")
	}
	if !containsContentEntry(chain, "a2") {
		t.Fatal("post-compact turn was orphaned off the current chain (leaf did not advance)")
	}
	// The chain is a single contiguous parent_id sequence.
	for i := 1; i < len(chain); i++ {
		if chain[i].ParentID != chain[i-1].ID {
			t.Fatalf("chain break at %d: parent=%q prev.id=%q", i, chain[i].ParentID, chain[i-1].ID)
		}
	}
	// The compact_summary precedes the post-compact turn on the chain.
	summaryIdx, turn2Idx := -1, -1
	for i, e := range chain {
		if e.Content == "SUMMARY-OF-TURN-1" {
			summaryIdx = i
		}
		if e.Content == "a2" {
			turn2Idx = i
		}
	}
	if summaryIdx < 0 || turn2Idx < 0 || summaryIdx >= turn2Idx {
		t.Fatalf("compact_summary must precede the post-compact turn: summaryIdx=%d turn2Idx=%d", summaryIdx, turn2Idx)
	}
}
