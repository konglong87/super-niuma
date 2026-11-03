package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileChangeFullJSON is a file_change that carries recoverable before AND after
// states (both content-addressed blobs) — the shape the first edit of a path in a
// turn records, from which redo can replay the branch's end state.
func fileChangeFullJSON(t *testing.T, path, beforeBlob, afterBlob string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"path":                 path,
		"before_exists":        true,
		"before_snapshot_path": beforeBlob,
		"after_exists":         true,
		"after_snapshot_path":  afterBlob,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// fileChangeLiteJSON is a superseded lite file_change: turn-level dedup dropped its
// after body/blob, leaving only a content hash. Its final content is unrecoverable
// (a tombstone for redo).
func fileChangeLiteJSON(t *testing.T, path, afterSHA string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"path":          path,
		"before_exists": true,
		"after_exists":  true,
		"after_sha256":  afterSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// recordFileEditTurn records one v2 turn (checkpoint, user msg, turn anchor, one
// file_change, assistant reply with an explicit id) so tests can address the
// resulting branch tip directly.
func recordFileEditTurn(t *testing.T, rec *Recorder, userID, replyID, changeJSON string) {
	t.Helper()
	if _, err := rec.Checkpoint("auto-"+userID, userID); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: userID, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(userID); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "file_change", Content: changeJSON}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: replyID, Type: "message", Role: "assistant", Content: "edited-answer"}); err != nil {
		t.Fatal(err)
	}
}

// TestV2RedoReplaysFilesToBranchEnd proves redo (default: files+conversation)
// replays the workspace forward to the target branch's end state, the mirror of a
// file-restoring rewind.
func TestV2RedoReplaysFilesToBranchEnd(t *testing.T) {
	store := v2Store(t)
	id := "44444444-aaaa-4aaa-8aaa-444444444444"
	rec, err := store.NewRecorderWithID(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	target := filepath.Join(workdir, "f.txt")
	// Turn 2 left the file at "v2" on disk.
	if err := os.WriteFile(target, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	beforeBlob := mkBlob(t, "v1")
	afterBlob := mkBlob(t, "v2")
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	abandonedLeaf := "cccccccc-3333-4333-8333-333333333333"
	recordTurn(t, rec, u1, "hello", "hi")
	recordFileEditTurn(t, rec, u2, abandonedLeaf, fileChangeFullJSON(t, target, beforeBlob, afterBlob))
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	// Rewind before turn 2: restores the file to the fork-point state ("v1") and
	// abandons turn 2's branch.
	if _, ok, err := store.RewindToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("after rewind file=%q, want v1", got)
	}
	sizeAfterRewind := fileSize(t, rec.Path)

	// Redo back onto the abandoned branch: the file must be replayed forward to "v2".
	result, ok, err := store.RedoToBranch(id, abandonedLeaf)
	if err != nil || !ok {
		t.Fatalf("redo ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 1 {
		t.Fatalf("FilesRestored=%d, want 1", result.FilesRestored)
	}
	if got, _ := os.ReadFile(target); string(got) != "v2" {
		t.Fatalf("after redo file=%q, want v2 (branch-end state)", got)
	}
	if fileSize(t, rec.Path) <= sizeAfterRewind {
		t.Fatal("redo must be append-only on the transcript")
	}
	entries, err := Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContentEntry(CurrentChain(entries), "edited-answer") {
		t.Fatal("after redo the current chain must be the redone branch")
	}
}

// TestV2RedoReclaimedAfterBlobReportsTombstone proves that when the target
// branch's end-state blob was reclaimed, redo aborts atomically with a clear
// tombstone error, touching neither the workspace nor the transcript.
func TestV2RedoReclaimedAfterBlobReportsTombstone(t *testing.T) {
	store := v2Store(t)
	id := "55555555-aaaa-4aaa-8aaa-555555555555"
	rec, err := store.NewRecorderWithID(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	target := filepath.Join(workdir, "f.txt")
	if err := os.WriteFile(target, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	beforeBlob := mkBlob(t, "v1")
	afterBlob := mkBlob(t, "v2")
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	abandonedLeaf := "cccccccc-3333-4333-8333-333333333333"
	recordTurn(t, rec, u1, "hello", "hi")
	recordFileEditTurn(t, rec, u2, abandonedLeaf, fileChangeFullJSON(t, target, beforeBlob, afterBlob))
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.RewindToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	sizeAfterRewind := fileSize(t, rec.Path)

	// The branch-end blob was reclaimed by a retention/GC policy.
	if err := os.Remove(afterBlob); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.RedoToBranch(id, abandonedLeaf)
	if err == nil {
		t.Fatal("expected a tombstone error when the branch-end blob is reclaimed")
	}
	if !strings.Contains(err.Error(), "reclaimed") {
		t.Fatalf("error should explain the reclaimed file history, got: %v", err)
	}
	// Atomic: the workspace stayed at the rewound state and the pointer never moved.
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("file must be untouched after a failed redo, got %q", got)
	}
	if fileSize(t, rec.Path) != sizeAfterRewind {
		t.Fatal("transcript must be untouched after a failed redo")
	}
	entries, err := Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if containsContentEntry(CurrentChain(entries), "edited-answer") {
		t.Fatal("a failed redo must not switch the current chain")
	}
}

// TestV2RedoSupersededAfterReportsTombstone proves redo refuses to reconstruct a
// path whose branch-end content was a superseded lite entry (after body never
// stored): it aborts before any mutation rather than writing an empty/wrong file.
func TestV2RedoSupersededAfterReportsTombstone(t *testing.T) {
	store := v2Store(t)
	id := "66666666-aaaa-4aaa-8aaa-666666666666"
	rec, err := store.NewRecorderWithID(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	target := filepath.Join(workdir, "f.txt")
	if err := os.WriteFile(target, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	beforeBlob := mkBlob(t, "v1")
	afterBlob := mkBlob(t, "v2")
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	abandonedLeaf := "cccccccc-3333-4333-8333-333333333333"
	recordTurn(t, rec, u1, "hello", "hi")
	// Turn 2 edits the path twice: the first change is full, the second supersedes
	// it as a lite entry (its final "v3" content was never stored).
	if _, err := rec.Checkpoint("auto-"+u2, u2); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: u2, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(u2); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "file_change", Content: fileChangeFullJSON(t, target, beforeBlob, afterBlob)}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "file_change", Content: fileChangeLiteJSON(t, target, "deadbeefdeadbeef")}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: abandonedLeaf, Type: "message", Role: "assistant", Content: "edited-answer"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.RewindToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("after rewind file=%q, want v1 (earliest before, lite entry ignored)", got)
	}
	sizeAfterRewind := fileSize(t, rec.Path)

	_, _, err = store.RedoToBranch(id, abandonedLeaf)
	if err == nil {
		t.Fatal("expected a tombstone error for a superseded branch-end state")
	}
	if !strings.Contains(err.Error(), "not retained") {
		t.Fatalf("error should explain the un-retained final content, got: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("file must be untouched after a failed redo, got %q", got)
	}
	if fileSize(t, rec.Path) != sizeAfterRewind {
		t.Fatal("transcript must be untouched after a failed redo")
	}
}

// TestV2RedoConversationOnlyLeavesFiles proves --conversation-only redo (Store.Redo)
// moves only the conversation pointer and never touches the workspace.
func TestV2RedoConversationOnlyLeavesFiles(t *testing.T) {
	store := v2Store(t)
	id := "77777777-aaaa-4aaa-8aaa-777777777777"
	rec, err := store.NewRecorderWithID(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	target := filepath.Join(workdir, "f.txt")
	if err := os.WriteFile(target, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	beforeBlob := mkBlob(t, "v1")
	afterBlob := mkBlob(t, "v2")
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	abandonedLeaf := "cccccccc-3333-4333-8333-333333333333"
	recordTurn(t, rec, u1, "hello", "hi")
	recordFileEditTurn(t, rec, u2, abandonedLeaf, fileChangeFullJSON(t, target, beforeBlob, afterBlob))
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.RewindToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}

	result, ok, err := store.Redo(id, abandonedLeaf)
	if err != nil || !ok {
		t.Fatalf("conversation-only redo ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 0 {
		t.Fatalf("conversation-only redo must not restore files, FilesRestored=%d", result.FilesRestored)
	}
	// The pointer moved (current chain is the redone branch) but the file did not.
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("conversation-only redo must leave the file untouched, got %q", got)
	}
	entries, err := Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContentEntry(CurrentChain(entries), "edited-answer") {
		t.Fatal("conversation-only redo must still move the conversation pointer")
	}
}
