package session

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/files"
)

// newFileHistoryStore wires a store whose transcripts live in one temp dir and
// whose blob store (via the default SnapshotDir) is redirected to another, so
// GCFileHistory's file.ListBlobs/GCSnapshots and s.List agree and stay isolated.
func newFileHistoryStore(t *testing.T) Store {
	t.Helper()
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	return Store{TranscriptProjectsRoot: t.TempDir()}
}

func mkBlob(t *testing.T, content string) string {
	t.Helper()
	p, err := files.StoreSnapshotContentAddressed("", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fileChangeJSON(t *testing.T, path, beforeSnapshot string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"path":                 path,
		"before_exists":        true,
		"before_snapshot_path": beforeSnapshot,
		"after_exists":         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGCFileHistoryTurnWindowReclaimsOldTurns(t *testing.T) {
	store := newFileHistoryStore(t)
	rec, err := store.NewRecorderWithID(t.TempDir(), "66666666-6666-4666-8666-666666666666")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var blobs []string
	for turn := 0; turn < 3; turn++ {
		ts := base.Add(time.Duration(turn) * time.Hour)
		if err := rec.Append(Entry{Type: "checkpoint", Name: fmt.Sprintf("auto-t%d", turn), Timestamp: ts}); err != nil {
			t.Fatal(err)
		}
		blob := mkBlob(t, fmt.Sprintf("turn-%d-content", turn))
		blobs = append(blobs, blob)
		if err := rec.Append(Entry{Type: "file_change", Content: fileChangeJSON(t, fmt.Sprintf("/f%d.txt", turn), blob), Timestamp: ts.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	res, err := store.GCFileHistory(FileHistoryGCOptions{MaxTurns: 2, Now: base.Add(10 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Fatalf("Removed=%d, want 1 (oldest turn only)", res.Removed)
	}
	if _, err := os.Stat(blobs[0]); !os.IsNotExist(err) {
		t.Fatalf("oldest-turn blob must be reclaimed, err=%v", err)
	}
	for i := 1; i < 3; i++ {
		if _, err := os.Stat(blobs[i]); err != nil {
			t.Fatalf("in-window blob %d must survive: %v", i, err)
		}
	}
}

func TestGCFileHistoryByteCapEvictsOldest(t *testing.T) {
	store := newFileHistoryStore(t)
	rec, err := store.NewRecorderWithID(t.TempDir(), "77777777-7777-4777-8777-777777777777")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := rec.Append(Entry{Type: "checkpoint", Name: "auto-t", Timestamp: base}); err != nil {
		t.Fatal(err)
	}
	var blobs []string
	for i := 0; i < 3; i++ {
		content := strings.Repeat(string(rune('a'+i)), 1000) // three distinct 1000-byte blobs
		blob := mkBlob(t, content)
		blobs = append(blobs, blob)
		if err := rec.Append(Entry{Type: "file_change", Content: fileChangeJSON(t, fmt.Sprintf("/b%d", i), blob), Timestamp: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	// Cap at 2500 bytes: only the two newest 1000-byte blobs fit.
	res, err := store.GCFileHistory(FileHistoryGCOptions{MaxBytes: 2500, Now: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Fatalf("Removed=%d, want 1 (oldest over budget)", res.Removed)
	}
	if _, err := os.Stat(blobs[0]); !os.IsNotExist(err) {
		t.Fatalf("oldest blob must be evicted by byte cap, err=%v", err)
	}
	if _, err := os.Stat(blobs[2]); err != nil {
		t.Fatalf("newest blob must survive byte cap: %v", err)
	}
}

func TestGCFileHistoryDryRunReclaimsNothing(t *testing.T) {
	store := newFileHistoryStore(t)
	// An orphan blob with no transcript reference is a reclaim candidate.
	orphan := mkBlob(t, "orphan-content")

	res, err := store.GCFileHistory(FileHistoryGCOptions{MaxTurns: 20, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Removed != 1 || len(res.Candidates) != 1 {
		t.Fatalf("dry run should report 1 candidate without deleting, got %+v", res)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("dry run must not delete blobs: %v", err)
	}
}
