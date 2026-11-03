package query

import (
	"encoding/json"
	"testing"

	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
)

// recordedFileChanges drives recordFileChange on a fresh Session/Recorder and
// returns the file_change entries it wrote, parsed back into tools.FileChange.
func recordedFileChanges(t *testing.T, apply func(s *Session)) []tools.FileChange {
	t.Helper()
	redirectSnapshotDir(t)
	store := session.Store{TranscriptProjectsRoot: t.TempDir()}
	rec, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{options: Options{Recorder: rec}}
	s.turnFileChangeSeen = map[string]bool{}
	apply(s)
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	var out []tools.FileChange
	for _, e := range entries {
		if e.Type != "file_change" {
			continue
		}
		var fc tools.FileChange
		if err := json.Unmarshal([]byte(e.Content), &fc); err != nil {
			t.Fatalf("unmarshal file_change: %v", err)
		}
		out = append(out, fc)
	}
	return out
}

func TestRecordFileChangeTurnDedup(t *testing.T) {
	changes := recordedFileChanges(t, func(s *Session) {
		// Three edits of the same file within one turn.
		s.recordFileChange(tools.FileChange{Path: "foo.txt", BeforeExists: true, Before: "v1\n", AfterExists: true, After: "v2\n"})
		s.recordFileChange(tools.FileChange{Path: "foo.txt", BeforeExists: true, Before: "v2\n", AfterExists: true, After: "v3\n"})
		s.recordFileChange(tools.FileChange{Path: "foo.txt", BeforeExists: true, Before: "v3\n", AfterExists: true, After: "v4\n"})
	})
	if len(changes) != 3 {
		t.Fatalf("want 3 file_change entries, got %d", len(changes))
	}
	// First is recoverable: body externalized to a blob, not superseded.
	first := changes[0]
	if first.BeforeSuperseded {
		t.Fatalf("first change must be recoverable, not superseded")
	}
	if first.BeforeSnapshotPath == "" || first.Before != "" {
		t.Fatalf("first change before must be externalized: path=%q before=%q", first.BeforeSnapshotPath, first.Before)
	}
	// Remaining are lite: superseded, no bodies, no blob refs, but hashed.
	recoverable := 0
	for _, c := range changes {
		if c.BeforeSnapshotPath != "" || c.AfterSnapshotPath != "" {
			recoverable++
		}
	}
	if recoverable != 1 {
		t.Fatalf("exactly 1 recoverable change expected in a turn, got %d", recoverable)
	}
	for i := 1; i < len(changes); i++ {
		c := changes[i]
		if !c.BeforeSuperseded {
			t.Fatalf("change %d must be superseded (lite)", i)
		}
		if c.Before != "" || c.After != "" || c.BeforeSnapshotPath != "" || c.AfterSnapshotPath != "" {
			t.Fatalf("lite change %d must carry no body/blob: %+v", i, c)
		}
		if c.BeforeSHA256 == "" || c.AfterSHA256 == "" {
			t.Fatalf("lite change %d must keep audit hashes", i)
		}
	}
}

func TestRecordFileChangeResetsPerTurn(t *testing.T) {
	changes := recordedFileChanges(t, func(s *Session) {
		s.recordFileChange(tools.FileChange{Path: "a.txt", BeforeExists: true, Before: "1\n", AfterExists: true, After: "2\n"})
		// New turn: same file must be recoverable again.
		s.turnFileChangeSeen = map[string]bool{}
		s.recordFileChange(tools.FileChange{Path: "a.txt", BeforeExists: true, Before: "2\n", AfterExists: true, After: "3\n"})
	})
	if len(changes) != 2 {
		t.Fatalf("want 2 changes, got %d", len(changes))
	}
	if changes[0].BeforeSuperseded || changes[1].BeforeSuperseded {
		t.Fatalf("both first-in-turn changes must be recoverable, got %+v", changes)
	}
	if changes[0].BeforeSnapshotPath == "" || changes[1].BeforeSnapshotPath == "" {
		t.Fatalf("both first-in-turn changes must externalize before")
	}
}

func TestRecordFileChangeDistinctPathsBothRecoverable(t *testing.T) {
	changes := recordedFileChanges(t, func(s *Session) {
		s.recordFileChange(tools.FileChange{Path: "a.txt", BeforeExists: true, Before: "a\n", AfterExists: true, After: "aa\n"})
		s.recordFileChange(tools.FileChange{Path: "b.txt", BeforeExists: true, Before: "b\n", AfterExists: true, After: "bb\n"})
	})
	for i, c := range changes {
		if c.BeforeSuperseded || c.BeforeSnapshotPath == "" {
			t.Fatalf("distinct path change %d must be recoverable: %+v", i, c)
		}
	}
}

// redirectSnapshotDir points the default content-addressed snapshot store at a
// temp dir so externalization in these tests never touches the real ~/.niuma-legacy.
func redirectSnapshotDir(t *testing.T) {
	t.Helper()
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
}

func TestRecordFileChangeV2CarriesMessageID(t *testing.T) {
	redirectSnapshotDir(t)
	store := session.Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	rec, err := store.NewRecorderWithID(t.TempDir(), "aaaaaaaa-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{options: Options{Recorder: rec}}
	s.turnFileChangeSeen = map[string]bool{}
	s.currentTurnMessageID = "msg-turn-1"
	s.recordFileChange(tools.FileChange{Path: "foo.txt", BeforeExists: true, Before: "v1\n", AfterExists: true, After: "v2\n"})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Type != "file_change" {
			continue
		}
		var fc tools.FileChange
		if err := json.Unmarshal([]byte(e.Content), &fc); err != nil {
			t.Fatal(err)
		}
		found = true
		if fc.MessageID != "msg-turn-1" {
			t.Fatalf("v2 file_change message_id = %q, want %q", fc.MessageID, "msg-turn-1")
		}
	}
	if !found {
		t.Fatal("no file_change entry recorded")
	}
}

func TestExternalizeFileChangeMovesBodiesToBlobs(t *testing.T) {
	redirectSnapshotDir(t)

	// A tiny .env-sized change: well under any old 8 KiB threshold, exactly the
	// leak case P0-3 must close.
	change := tools.FileChange{
		Path:         ".env",
		BeforeExists: true,
		Before:       "SECRET=old-value\n",
		AfterExists:  true,
		After:        "SECRET=new-value\n",
	}
	externalizeFileChangeForTranscript(&change)

	if change.Before != "" || change.After != "" {
		t.Fatalf("file bodies must not stay inline: before=%q after=%q", change.Before, change.After)
	}
	if change.BeforeSnapshotPath == "" || change.AfterSnapshotPath == "" {
		t.Fatalf("bodies must be externalized to blobs: before=%q after=%q", change.BeforeSnapshotPath, change.AfterSnapshotPath)
	}
	if change.BeforeSHA256 == "" || change.AfterSHA256 == "" {
		t.Fatalf("audit hashes must be recorded: before=%q after=%q", change.BeforeSHA256, change.AfterSHA256)
	}
	if got := files.SnapshotHashFromPath(change.BeforeSnapshotPath); got != change.BeforeSHA256 {
		t.Fatalf("before hash mismatch: path %q vs field %q", got, change.BeforeSHA256)
	}
}

func TestExternalizeFileChangeKeepsEmptyInline(t *testing.T) {
	redirectSnapshotDir(t)

	// New file: empty before must stay inline (no blob), new body must externalize.
	change := tools.FileChange{
		Path:         "new.txt",
		BeforeExists: false,
		Before:       "",
		AfterExists:  true,
		After:        "hello\n",
	}
	externalizeFileChangeForTranscript(&change)

	if change.BeforeSnapshotPath != "" || change.BeforeSHA256 != "" {
		t.Fatalf("empty before must not create a blob: path=%q hash=%q", change.BeforeSnapshotPath, change.BeforeSHA256)
	}
	if change.AfterSnapshotPath == "" || change.After != "" {
		t.Fatalf("new body must externalize: path=%q after=%q", change.AfterSnapshotPath, change.After)
	}
}

func TestExternalizeFileChangeLeavesPlaceholderInline(t *testing.T) {
	redirectSnapshotDir(t)

	// A large-file capture placeholder is a marker, not a body: it must stay
	// inline and never become a blob.
	change := tools.FileChange{
		Path:         "big.bin",
		BeforeExists: true,
		Before:       "",
		AfterExists:  true,
		After:        files.CaptureSnapshotPlaceholder,
	}
	externalizeFileChangeForTranscript(&change)

	if change.After != files.CaptureSnapshotPlaceholder {
		t.Fatalf("placeholder must stay inline, got %q", change.After)
	}
	if change.AfterSnapshotPath != "" {
		t.Fatalf("placeholder must not be stored as a blob, got %q", change.AfterSnapshotPath)
	}
}

func TestExternalizeFileChangeSkipsSymlinkAndDir(t *testing.T) {
	redirectSnapshotDir(t)

	link := tools.FileChange{
		Path:             "link",
		BeforeExists:     true,
		BeforeIsSymlink:  true,
		BeforeLinkTarget: "target",
		AfterExists:      true,
		AfterIsSymlink:   true,
		AfterLinkTarget:  "target2",
	}
	externalizeFileChangeForTranscript(&link)
	if link.BeforeSnapshotPath != "" || link.AfterSnapshotPath != "" {
		t.Fatalf("symlink change must not externalize bodies")
	}

	dir := tools.FileChange{
		Path:         "d",
		BeforeExists: true,
		BeforeIsDir:  true,
		AfterExists:  true,
		AfterIsDir:   true,
	}
	externalizeFileChangeForTranscript(&dir)
	if dir.BeforeSnapshotPath != "" || dir.AfterSnapshotPath != "" {
		t.Fatalf("directory change must not externalize bodies")
	}
}

func TestExternalizeFileChangeBackfillsHashForExistingBlob(t *testing.T) {
	redirectSnapshotDir(t)

	// Producer already externalized (e.g. Edit via ReplaceDetailed) but did not
	// set the audit hash: backfill from the content-addressed blob name, leave
	// the path untouched.
	blob, hash, err := files.ExternalizeContent("", "already-externalized\n")
	if err != nil {
		t.Fatal(err)
	}
	change := tools.FileChange{
		Path:               "x.go",
		BeforeExists:       true,
		BeforeSnapshotPath: blob,
		AfterExists:        true,
		After:              "",
	}
	externalizeFileChangeForTranscript(&change)
	if change.BeforeSnapshotPath != blob {
		t.Fatalf("existing blob path must be preserved: %q vs %q", change.BeforeSnapshotPath, blob)
	}
	if change.BeforeSHA256 != hash {
		t.Fatalf("hash must be backfilled: got %q want %q", change.BeforeSHA256, hash)
	}
}
