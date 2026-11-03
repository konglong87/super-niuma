package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordV2FileTurn records one v2 turn that edits a single path, referencing a
// before-state blob (the turn-start content). Returns the user message id.
func recordV2FileTurn(t *testing.T, rec *Recorder, userID, path, beforeBlob string) {
	t.Helper()
	if _, err := rec.Checkpoint("auto-"+userID, userID); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: userID, Type: "message", Role: "user", Content: "edit " + path}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(userID); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "file_change", Content: fileChangeJSON(t, path, beforeBlob)}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "message", Role: "assistant", Content: "done"}); err != nil {
		t.Fatal(err)
	}
}

func TestGCFileHistoryV2ReclaimsAbandonedBranch(t *testing.T) {
	store := v2Store(t)
	id := "11111111-aaaa-4aaa-8aaa-111111111111"
	rec, err := store.NewRecorderWithID(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	blobA := mkBlob(t, "content-A")
	blobB := mkBlob(t, "content-B")
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	recordV2FileTurn(t, rec, u1, "/f.txt", blobA)
	recordV2FileTurn(t, rec, u2, "/f.txt", blobB)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	// Non-destructively abandon turn 2. blobB is now referenced only off the
	// current chain; blobA stays on the current chain.
	if _, ok, err := store.RewindConversationToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}

	res, err := store.GCFileHistory(FileHistoryGCOptions{MaxTurns: 20})
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed < 1 {
		t.Fatalf("expected the abandoned-branch blob to be reclaimed, got %+v", res)
	}
	if _, err := os.Stat(blobA); err != nil {
		t.Fatalf("current-chain blob must survive GC: %v", err)
	}
	if _, err := os.Stat(blobB); !os.IsNotExist(err) {
		t.Fatalf("abandoned-branch blob must be reclaimed by the current-chain window, err=%v", err)
	}
}

func TestV2RewindRestoresFileToTargetChainState(t *testing.T) {
	store := v2Store(t)
	id := "22222222-aaaa-4aaa-8aaa-222222222222"
	rec, err := store.NewRecorderWithID(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	// A real workspace file, edited in turn 2 from "v1" to "v2".
	workdir := t.TempDir()
	target := filepath.Join(workdir, "f.txt")
	if err := os.WriteFile(target, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	beforeBlob := mkBlob(t, "v1") // turn-start (pre-edit) content
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	// Turn 1 touches nothing recoverable for the file; turn 2 edits it.
	recordTurn(t, rec, u1, "hello", "hi")
	if _, err := rec.Checkpoint("auto-"+u2, u2); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: u2, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(u2); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "file_change", Content: fileChangeJSON(t, target, beforeBlob)}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "message", Role: "assistant", Content: "edited"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	sizeBefore := fileSize(t, rec.Path)

	// Files+conversation rewind before turn 2 restores the file to the target
	// chain's state ("v1") non-destructively.
	result, ok, err := store.RewindToMessage(id, u2)
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 1 {
		t.Fatalf("FilesRestored=%d, want 1", result.FilesRestored)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1" {
		t.Fatalf("file restored to %q, want %q", got, "v1")
	}
	// Transcript appended, not truncated.
	if fileSize(t, rec.Path) <= sizeBefore {
		t.Fatal("v2 files+conversation rewind must be append-only on the transcript")
	}
}

func TestV2RewindReclaimedBlobReportsTombstone(t *testing.T) {
	store := v2Store(t)
	id := "33333333-aaaa-4aaa-8aaa-333333333333"
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
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	recordTurn(t, rec, u1, "hello", "hi")
	if _, err := rec.Checkpoint("auto-"+u2, u2); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: u2, Type: "message", Role: "user", Content: "edit"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(u2); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "file_change", Content: fileChangeJSON(t, target, beforeBlob)}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	sizeBefore := fileSize(t, rec.Path)

	// The before-state blob was reclaimed (e.g. by a retention policy) → rewind
	// must fail with a clear tombstone error and touch neither disk nor transcript.
	if err := os.Remove(beforeBlob); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.RewindToMessage(id, u2)
	if err == nil {
		t.Fatal("expected a tombstone error when the before-state blob is reclaimed")
	}
	if !strings.Contains(err.Error(), "reclaimed") {
		t.Fatalf("error should explain the reclaimed file history, got: %v", err)
	}
	// Compensating transaction: nothing changed.
	if got, _ := os.ReadFile(target); string(got) != "v2" {
		t.Fatalf("file must be untouched after failed rewind, got %q", got)
	}
	if fileSize(t, rec.Path) != sizeBefore {
		t.Fatal("transcript must be untouched after a failed rewind")
	}
}
