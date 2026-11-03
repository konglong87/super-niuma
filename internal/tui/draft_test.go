package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileDraftStoreRoundTripAndIsolation(t *testing.T) {
	root := t.TempDir()
	store := NewFileDraftStore(root)
	draft := Draft{SessionID: "session-1", CWD: "/repo", Text: "unfinished prompt", AttachmentRefs: []DraftAttachmentRef{{ID: 1, Name: "shot.png", Path: "/tmp/shot.png"}}}
	if err := store.Save(draft); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	got, ok, err := store.Load("session-1", "/repo")
	if err != nil || !ok {
		t.Fatalf("load draft: ok=%v err=%v", ok, err)
	}
	if got.Text != draft.Text || len(got.AttachmentRefs) != 1 || got.AttachmentRefs[0].Path != "/tmp/shot.png" {
		t.Fatalf("draft = %+v", got)
	}
	if _, ok, err := store.Load("session-2", "/repo"); err != nil || ok {
		t.Fatalf("isolated draft = ok:%v err:%v", ok, err)
	}
	files, err := filepath.Glob(filepath.Join(root, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("draft files = %v err=%v", files, err)
	}
	info, err := os.Stat(files[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("draft mode = %v err=%v", info.Mode().Perm(), err)
	}
}

func TestFileDraftStoreClear(t *testing.T) {
	store := NewFileDraftStore(t.TempDir())
	if err := store.Save(Draft{SessionID: "session-1", CWD: "/repo", Text: "draft"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear("session-1", "/repo"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Load("session-1", "/repo"); err != nil || ok {
		t.Fatalf("draft after clear = ok:%v err:%v", ok, err)
	}
}

func TestModelLoadsAndClearsLocalDraftWithoutTranscriptEntry(t *testing.T) {
	store := NewFileDraftStore(t.TempDir())
	if err := store.Save(Draft{SessionID: "session-1", CWD: "/repo", Text: "resume me"}); err != nil {
		t.Fatal(err)
	}
	model := NewModel(nil, Options{
		Welcome:    WelcomeInfo{SessionID: "session-1", CWD: "/repo"},
		DraftStore: store,
		Run:        func(context.Context, string) (string, error) { return "ok", nil },
	})
	if model.textarea.Value() != "resume me" {
		t.Fatalf("restored textarea = %q", model.textarea.Value())
	}
	model.setTextareaValue("new draft")
	model.saveDraft()
	loaded, ok, err := store.Load("session-1", "/repo")
	if err != nil || !ok || loaded.Text != "new draft" {
		t.Fatalf("saved draft = %+v ok=%v err=%v", loaded, ok, err)
	}
	model.resetTextarea()
	model.clearDraft()
	if _, ok, err := store.Load("session-1", "/repo"); err != nil || ok {
		t.Fatalf("draft after clear = ok:%v err:%v", ok, err)
	}
}
