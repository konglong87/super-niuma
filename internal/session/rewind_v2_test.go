package session

import (
	"os"
	"testing"
)

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestV2RewindIsNonDestructiveAndRedoable(t *testing.T) {
	store := v2Store(t)
	id := "77777777-7777-4777-8777-777777777777"
	rec, err := store.NewRecorderWithID("/tmp/rewindapp", id)
	if err != nil {
		t.Fatal(err)
	}
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	recordTurn(t, rec, u1, "first question", "first answer")
	recordTurn(t, rec, u2, "second question", "second answer")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	sizeBefore := fileSize(t, rec.Path)

	// Non-destructive conversation rewind before the second user message.
	result, ok, err := store.RewindConversationToMessage(id, u2)
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if !result.TranscriptKept {
		t.Fatal("v2 rewind must keep the transcript (non-destructive)")
	}
	if result.EntriesRemoved == 0 {
		t.Fatal("expected some chain nodes set aside by rewind")
	}
	// File only grew: nothing was truncated.
	if got := fileSize(t, rec.Path); got <= sizeBefore {
		t.Fatalf("transcript shrank: before=%d after=%d (rewind must be append-only)", sizeBefore, got)
	}

	entries, _, err := LoadWithFormat(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	// The abandoned branch is still physically in the file.
	if !containsContentEntry(entries, "second answer") {
		t.Fatal("abandoned branch content must remain in the file")
	}
	// But the current chain no longer includes it.
	if containsContentEntry(CurrentChain(entries), "second answer") {
		t.Fatal("current chain must exclude the abandoned branch")
	}
	if !containsContentEntry(CurrentChain(entries), "first answer") {
		t.Fatal("current chain must keep the pre-rewind conversation")
	}

	// Continue the conversation from the rewind point: a parallel new branch.
	reopened, ok, err := store.OpenRecorder(id)
	if err != nil || !ok {
		t.Fatalf("OpenRecorder ok=%v err=%v", ok, err)
	}
	u2b := "cccccccc-3333-4333-8333-333333333333"
	recordTurn(t, reopened, u2b, "different second", "different answer")
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	// Two branches now exist; exactly one is active (the new one).
	branches, ok, err := store.Branches(id)
	if err != nil || !ok {
		t.Fatalf("Branches ok=%v err=%v", ok, err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d: %+v", len(branches), branches)
	}
	activeCount := 0
	var abandonedLeaf string
	for _, b := range branches {
		if b.Active {
			activeCount++
		} else {
			abandonedLeaf = b.LeafID
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly 1 active branch, got %d", activeCount)
	}
	if abandonedLeaf == "" {
		t.Fatal("could not locate the abandoned branch leaf")
	}

	// The active chain has the new answer, not the old one.
	entries, _, _ = LoadWithFormat(rec.Path)
	if !containsContentEntry(CurrentChain(entries), "different answer") {
		t.Fatal("active chain should hold the new branch")
	}
	if containsContentEntry(CurrentChain(entries), "second answer") {
		t.Fatal("active chain should not hold the abandoned branch")
	}

	// Redo back onto the abandoned branch: it is recoverable because nothing was deleted.
	if _, ok, err := store.Redo(id, abandonedLeaf); err != nil || !ok {
		t.Fatalf("Redo ok=%v err=%v", ok, err)
	}
	entries, _, _ = LoadWithFormat(rec.Path)
	if !containsContentEntry(CurrentChain(entries), "second answer") {
		t.Fatal("redo must restore the abandoned branch as the current chain")
	}
	if containsContentEntry(CurrentChain(entries), "different answer") {
		t.Fatal("redo should switch away from the new branch")
	}
}

// TestV2RewindThenContinueForksAfterLeafSync reproduces the live-TUI bug where a
// /rewind appended a branch_head out-of-band but the still-open recorder kept its
// stale pre-rewind leaf, so the next turn extended the old line instead of forking
// (no second branch ever appeared). SyncLeafFromDisk repositions the recorder so
// the continuation forks from the rewind target.
func TestV2RewindThenContinueForksAfterLeafSync(t *testing.T) {
	store := v2Store(t)
	id := "beefbeef-1111-4111-8111-111111111111"
	rec, err := store.NewRecorderWithID("/tmp/rewindfork", id)
	if err != nil {
		t.Fatal(err)
	}
	recordTurn(t, rec, "aaaaaaaa-1111-4111-8111-111111111111", "q1", "a1")
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	recordTurn(t, rec, u2, "q2", "answer-old")

	// The /rewind slash performs this out-of-band on the same file (via the store),
	// which does NOT touch the still-open recorder's in-memory leaf.
	if _, ok, err := store.RewindConversationToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	// The fix: resync the live recorder to the moved active leaf.
	if err := rec.SyncLeafFromDisk(); err != nil {
		t.Fatal(err)
	}
	// Continue the conversation — this must fork a NEW branch off the rewind point.
	recordTurn(t, rec, "cccccccc-3333-4333-8333-333333333333", "q2-alt", "answer-new")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	entries, _, err := LoadWithFormat(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	// A real fork exists: the abandoned old branch + the new branch = 2 leaves.
	if leaves := Leaves(entries); len(leaves) != 2 {
		t.Fatalf("expected 2 branches after rewind+continue, got %d (rewind did not fork)", len(leaves))
	}
	chain := CurrentChain(entries)
	if !containsContentEntry(chain, "answer-new") {
		t.Fatal("current chain should be the new branch")
	}
	if containsContentEntry(chain, "answer-old") {
		t.Fatal("rewound branch must not be on the current chain (rewind silently undone)")
	}
}

func TestV2CheckpointRewindIsNonDestructive(t *testing.T) {
	store := v2Store(t)
	id := "abcdef01-7777-4777-8777-777777777777"
	rec, err := store.NewRecorderWithID("/tmp/cprewind", id)
	if err != nil {
		t.Fatal(err)
	}
	recordTurn(t, rec, "aaaaaaaa-1111-4111-8111-111111111111", "q1", "a1")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	// A manual named checkpoint (by id) must land on the current chain in v2.
	if _, ok, err := store.Checkpoint(id, "mark"); err != nil || !ok {
		t.Fatalf("checkpoint ok=%v err=%v", ok, err)
	}
	reopened, ok, err := store.OpenRecorder(id)
	if err != nil || !ok {
		t.Fatalf("reopen ok=%v err=%v", ok, err)
	}
	recordTurn(t, reopened, "bbbbbbbb-2222-4222-8222-222222222222", "q2", "after mark")
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	sizeBefore := fileSize(t, rec.Path)

	result, ok, err := store.Rewind(id, "mark")
	if err != nil || !ok {
		t.Fatalf("checkpoint rewind ok=%v err=%v", ok, err)
	}
	if !result.TranscriptKept {
		t.Fatal("v2 checkpoint rewind must be non-destructive")
	}
	if fileSize(t, rec.Path) <= sizeBefore {
		t.Fatal("v2 checkpoint rewind must be append-only (file only grows)")
	}
	entries, _, err := LoadWithFormat(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContentEntry(entries, "after mark") {
		t.Fatal("abandoned branch content must remain in the file")
	}
	if containsContentEntry(CurrentChain(entries), "after mark") {
		t.Fatal("current chain must exclude the post-checkpoint turn after rewind")
	}
	if !containsContentEntry(CurrentChain(entries), "a1") {
		t.Fatal("current chain must keep the pre-checkpoint conversation")
	}
}

func TestV2RewindErrorsAndV1Unaffected(t *testing.T) {
	// Redo/Branches reject v1 linear transcripts.
	store := Store{TranscriptProjectsRoot: t.TempDir()}
	id := "88888888-8888-4888-8888-888888888888"
	rec, err := store.NewRecorderWithID("/tmp/v1app", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "message", Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Branches(id); err == nil {
		t.Fatal("Branches must error on a v1 transcript")
	}
	if _, _, err := store.Redo(id, "whatever"); err == nil {
		t.Fatal("Redo must error on a v1 transcript")
	}
}

func containsContentEntry(entries []Entry, content string) bool {
	for _, e := range entries {
		if e.Content == content {
			return true
		}
	}
	return false
}
