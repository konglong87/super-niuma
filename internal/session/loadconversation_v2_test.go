package session

import (
	"strings"
	"testing"
)

func TestLoadConversationExcludesAbandonedBranch(t *testing.T) {
	store, id := buildBranchedSession(t) // active tip "new answer"; abandoned "old answer"
	summary, ok, err := store.Find(id)
	if err != nil || !ok {
		t.Fatalf("find ok=%v err=%v", ok, err)
	}

	conv, err := LoadConversation(summary.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContentEntry(conv, "new answer") {
		t.Fatal("LoadConversation should include the active branch")
	}
	if containsContentEntry(conv, "old answer") {
		t.Fatal("LoadConversation must exclude the abandoned branch")
	}

	// Compact summarizes the current conversation only.
	entry, err := Compact(summary.Path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.Content, "new answer") {
		t.Fatalf("compact summary missing active branch:\n%s", entry.Content)
	}
	if strings.Contains(entry.Content, "old answer") {
		t.Fatalf("compact summary leaked abandoned branch:\n%s", entry.Content)
	}
}

func TestLoadConversationIdentityForV1(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir()} // v1
	id := "eeeeeeee-1111-4111-8111-111111111111"
	rec, err := store.NewRecorderWithID("/tmp/v1conv", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{Type: "message", Role: "user", Content: "only turn"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := LoadConversation(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv) != len(raw) {
		t.Fatalf("v1 LoadConversation must be identity: got %d want %d", len(conv), len(raw))
	}
}
