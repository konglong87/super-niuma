package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/session"
)

// inlineFileChange builds a file_change whose before/after bodies are inline (no
// blob store needed), so a CLI-level test can exercise file restore end to end.
func inlineFileChange(t *testing.T, path, before, after string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"path":          path,
		"before":        before,
		"before_exists": true,
		"after":         after,
		"after_exists":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSessionRedoRestoresFilesByDefault drives the `session redo` CLI wiring over a
// real workspace file: the default replays files forward to the branch's end state,
// while --conversation-only moves only the conversation pointer.
func TestSessionRedoRestoresFilesByDefault(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	t.Setenv("NIUMA_CONFIG_DIR", root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", projects)

	store := session.Store{TranscriptProjectsRoot: projects, SchemaV2: true}
	id := "abcd1234-9999-4999-8999-999999999999"
	rec, err := store.NewRecorderWithID("/tmp/redofiles", id)
	if err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	target := filepath.Join(workdir, "f.txt")
	if err := os.WriteFile(target, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	leaf := "cccccccc-3333-4333-8333-333333333333"

	appendEntry := func(e session.Entry) {
		if err := rec.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	// Turn 1 (no file), then turn 2 edits f.txt "v1" -> "v2".
	if _, err := rec.Checkpoint("auto-"+u1, u1); err != nil {
		t.Fatal(err)
	}
	appendEntry(session.Entry{ID: u1, Type: "message", Role: "user", Content: "hi"})
	if err := rec.MarkTurn(u1); err != nil {
		t.Fatal(err)
	}
	appendEntry(session.Entry{Type: "message", Role: "assistant", Content: "a1"})
	if _, err := rec.Checkpoint("auto-"+u2, u2); err != nil {
		t.Fatal(err)
	}
	appendEntry(session.Entry{ID: u2, Type: "message", Role: "user", Content: "edit"})
	if err := rec.MarkTurn(u2); err != nil {
		t.Fatal(err)
	}
	appendEntry(session.Entry{Type: "file_change", Content: inlineFileChange(t, target, "v1", "v2")})
	appendEntry(session.Entry{ID: leaf, Type: "message", Role: "assistant", Content: "edited"})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	// Rewind before turn 2 → file back to "v1", turn 2 branch abandoned.
	if _, ok, err := store.RewindToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("after rewind file=%q, want v1", got)
	}

	// Default `session redo` replays the file forward to the branch end ("v2").
	var out bytes.Buffer
	if err := sessionCommand([]string{"redo", id, leaf}, &out); err != nil {
		t.Fatalf("session redo: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v2" {
		t.Fatalf("default redo must restore files to branch end, file=%q want v2", got)
	}

	// Rewind again, then --conversation-only must leave the file at "v1".
	if _, ok, err := store.RewindToMessage(id, u2); err != nil || !ok {
		t.Fatalf("second rewind ok=%v err=%v", ok, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("after second rewind file=%q, want v1", got)
	}
	out.Reset()
	if err := sessionCommand([]string{"redo", id, leaf, "--conversation-only"}, &out); err != nil {
		t.Fatalf("session redo --conversation-only: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "v1" {
		t.Fatalf("--conversation-only redo must not touch files, file=%q want v1", got)
	}
	entries, err := session.Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContent(session.CurrentChain(entries), "edited") {
		t.Fatal("--conversation-only redo must still move the conversation pointer")
	}
}
