package handoff

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
)

func TestLocalHandoffSourceUsesActiveV2ChainAndOpaqueLocators(t *testing.T) {
	path := writeLocalTranscript(t, `{"schema":"niuma.transcript.v2","type":"session_meta"}
{"schema":"niuma.transcript.v2","id":"root","type":"message","role":"user","content":"root"}
{"schema":"niuma.transcript.v2","id":"active","parent_id":"root","type":"tool_result","tool_name":"go test"}
{"schema":"niuma.transcript.v2","id":"skipped","parent_id":"root","type":"message","role":"user","content":"branch secret"}
{"schema":"niuma.transcript.v2","type":"branch_head","leaf_id":"active"}
`)
	reader := NewLocalSourceReader(fileLocalSourceStore{summary: session.Summary{SessionID: "local-1", Path: path}})

	snapshot, err := reader.Capture(context.Background(), handoffRequestContext(), localSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source.Cursor != "entry:active" || len(snapshot.Evidence) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, item := range snapshot.Evidence {
		if containsAny(item.Ref, path, "/", "branch secret") {
			t.Fatalf("locator leaks source path or skipped branch: %#v", item)
		}
	}
	assertSourceSnapshotExtractable(t, snapshot)
}

func TestLocalHandoffSourceLegacyCursorResolverAndReadOnly(t *testing.T) {
	path := writeLocalTranscript(t, `{"type":"message","role":"user","content":"legacy objective"}
{"type":"tool_result","tool_name":"go test"}
`)
	reader := NewLocalSourceReader(fileLocalSourceStore{summary: session.Summary{SessionID: "local-1", Path: path}})

	snapshot, err := reader.Capture(context.Background(), handoffRequestContext(), localSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source.Cursor != "line:2" {
		t.Fatalf("cursor = %q", snapshot.Source.Cursor)
	}
	assertSourceSnapshotExtractable(t, snapshot)
	evidence := snapshot.Evidence[1]
	got, err := reader.Resolve(context.Background(), handoffRequestContext(), localSourceRef(), evidence.Ref, evidence.SHA256)
	if err != nil || got.Ref != evidence.Ref || got.SHA256 != evidence.SHA256 {
		t.Fatalf("Resolve() = %#v, %v", got, err)
	}
	if _, err := reader.Resolve(context.Background(), handoffRequestContext(), localSourceRef(), evidence.Ref, "wrong"); ErrorCodeOf(err) != CodeInvalidHash {
		t.Fatalf("Resolve(bad hash) error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("local source read mutated file: %v", err)
	}
}

func TestLocalHandoffSourceResolverUsesExactEntryOutsideActiveChain(t *testing.T) {
	path := writeLocalTranscript(t, `{"schema":"niuma.transcript.v2","type":"session_meta"}
{"schema":"niuma.transcript.v2","id":"root","type":"message","role":"user","content":"root"}
{"schema":"niuma.transcript.v2","id":"old-active","parent_id":"root","type":"tool_result","tool_name":"go test"}
{"schema":"niuma.transcript.v2","id":"other","parent_id":"root","type":"message","role":"user","content":"other"}
{"schema":"niuma.transcript.v2","type":"branch_head","leaf_id":"old-active"}
`)
	store := fileLocalSourceStore{summary: session.Summary{SessionID: "local-1", Path: path}}
	reader := NewLocalSourceReader(store)
	snapshot, err := reader.Capture(context.Background(), handoffRequestContext(), localSourceRef())
	if err != nil {
		t.Fatal(err)
	}
	oldEvidence := snapshot.Evidence[1]
	if err := os.WriteFile(path, []byte(`{"schema":"niuma.transcript.v2","type":"session_meta"}
{"schema":"niuma.transcript.v2","id":"root","type":"message","role":"user","content":"root"}
{"schema":"niuma.transcript.v2","id":"old-active","parent_id":"root","type":"tool_result","tool_name":"go test"}
{"schema":"niuma.transcript.v2","id":"other","parent_id":"root","type":"message","role":"user","content":"other"}
{"schema":"niuma.transcript.v2","type":"branch_head","leaf_id":"other"}
`), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := reader.Resolve(context.Background(), handoffRequestContext(), localSourceRef(), oldEvidence.Ref, oldEvidence.SHA256)
	if err != nil || resolved.Ref != oldEvidence.Ref {
		t.Fatalf("Resolve(old inactive entry) = %+v, %v", resolved, err)
	}
}

func TestDefaultLocalSnapshotStoreRejectsSameSizeMtimeReplacement(t *testing.T) {
	path := writeLocalTranscript(t, `{"type":"message","content":"first"}
`)
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store := DefaultLocalSnapshotStore{ops: &localSnapshotOps{
		stat: os.Stat,
		readFile: func(readPath string) ([]byte, error) {
			data, readErr := os.ReadFile(readPath)
			if readErr != nil {
				return nil, readErr
			}
			replacement := filepath.Join(filepath.Dir(readPath), "replacement.jsonl")
			content := []byte(`{"type":"message","content":"other"}
`)
			if len(content) != len(data) {
				return nil, fmt.Errorf("fixture size %d != %d", len(content), len(data))
			}
			if writeErr := os.WriteFile(replacement, content, 0600); writeErr != nil {
				return nil, writeErr
			}
			if timeErr := os.Chtimes(replacement, original.ModTime(), original.ModTime()); timeErr != nil {
				return nil, timeErr
			}
			if renameErr := os.Rename(replacement, readPath); renameErr != nil {
				return nil, renameErr
			}
			return data, nil
		},
	}}
	if _, err := store.ReadSnapshot(path); ErrorCodeOf(err) != CodeSourceChanged {
		t.Fatalf("ReadSnapshot(replaced) error = %v", err)
	}
}

func TestDefaultLocalSnapshotStoreRejectsDifferentBytesWithIdenticalStat(t *testing.T) {
	path := writeLocalTranscript(t, `{"type":"message","content":"first"}
`)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	store := DefaultLocalSnapshotStore{ops: &localSnapshotOps{
		stat: func(string) (os.FileInfo, error) { return info, nil },
		readFile: func(string) ([]byte, error) {
			reads++
			if reads == 1 {
				return []byte(`{"type":"message","content":"first"}`), nil
			}
			return []byte(`{"type":"message","content":"other"}`), nil
		},
	}}
	if _, err := store.ReadSnapshot(path); ErrorCodeOf(err) != CodeSourceChanged {
		t.Fatalf("ReadSnapshot(different stable bytes) error = %v", err)
	}
	if reads != 2 {
		t.Fatalf("ReadSnapshot() reads = %d, want 2", reads)
	}
}

func TestLocalHandoffSourceRejectsSnapshotWhoseHashDoesNotMatchParsingBytes(t *testing.T) {
	path := writeLocalTranscript(t, `{"type":"message","content":"value"}
`)
	store := corruptHashLocalSourceStore{fileLocalSourceStore{summary: session.Summary{SessionID: "local-1", Path: path}}}
	if _, err := NewLocalSourceReader(store).Capture(context.Background(), handoffRequestContext(), localSourceRef()); ErrorCodeOf(err) != CodeSourceChanged {
		t.Fatalf("Capture(corrupt snapshot hash) error = %v", err)
	}
}

func TestLocalHandoffSourceRejectsChangedAndUnsupportedSnapshots(t *testing.T) {
	path := writeLocalTranscript(t, `{"type":"message","content":"ok"}
`)
	changed := changingLocalSourceStore{fileLocalSourceStore: fileLocalSourceStore{summary: session.Summary{SessionID: "local-1", Path: path}}}
	if _, err := NewLocalSourceReader(changed).Capture(context.Background(), handoffRequestContext(), localSourceRef()); ErrorCodeOf(err) != CodeSourceChanged {
		t.Fatalf("changed capture error = %v", err)
	}

	native := writeLocalTranscript(t, `{"type":"user","message":{"content":"native"}}
`)
	if _, err := NewLocalSourceReader(fileLocalSourceStore{summary: session.Summary{SessionID: "local-1", Path: native}}).Capture(context.Background(), handoffRequestContext(), localSourceRef()); ErrorCodeOf(err) != CodeUnsupportedSource {
		t.Fatalf("native capture error = %v", err)
	}

	mixed := writeLocalTranscript(t, `{"type":"message","content":"go-cc"}
{"type":"user","message":{"content":"native"}}
`)
	if _, err := NewLocalSourceReader(fileLocalSourceStore{summary: session.Summary{SessionID: "local-1", Path: mixed}}).Capture(context.Background(), handoffRequestContext(), localSourceRef()); ErrorCodeOf(err) != CodeUnsupportedSource {
		t.Fatalf("mixed capture error = %v", err)
	}
}

func localSourceRef() sessioncontrol.SessionRef {
	return sessioncontrol.SessionRef{Source: sessioncontrol.SourceLocal, Key: "local-1"}
}

func writeLocalTranscript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type fileLocalSourceStore struct{ summary session.Summary }

func (s fileLocalSourceStore) Find(id string) (session.Summary, bool, error) {
	return s.summary, s.summary.SessionID == id, nil
}
func (fileLocalSourceStore) ReadSnapshot(path string) (ImmutableLocalSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ImmutableLocalSnapshot{}, err
	}
	sum := sha256.Sum256(data)
	return ImmutableLocalSnapshot{Data: data, SHA256: fmt.Sprintf("%x", sum[:])}, nil
}
func (fileLocalSourceStore) ReadEntrySnapshot(path string, selector LocalEntrySelector) (LocalEntrySnapshot, error) {
	snapshot, err := fileLocalSourceStore{}.ReadSnapshot(path)
	if err != nil {
		return LocalEntrySnapshot{}, err
	}
	return exactLocalEntry(snapshot, selector)
}

type changingLocalSourceStore struct{ fileLocalSourceStore }

func (s changingLocalSourceStore) ReadSnapshot(path string) (ImmutableLocalSnapshot, error) {
	if err := os.WriteFile(path, []byte(`{"type":"message","content":"changed"}`), 0600); err != nil {
		return ImmutableLocalSnapshot{}, err
	}
	return ImmutableLocalSnapshot{}, &Error{Code: CodeSourceChanged, Message: "local transcript changed while capturing"}
}

func (s changingLocalSourceStore) ReadEntrySnapshot(string, LocalEntrySelector) (LocalEntrySnapshot, error) {
	return LocalEntrySnapshot{}, &Error{Code: CodeSourceChanged, Message: "local transcript changed while resolving"}
}

type corruptHashLocalSourceStore struct{ fileLocalSourceStore }

func (s corruptHashLocalSourceStore) ReadSnapshot(path string) (ImmutableLocalSnapshot, error) {
	snapshot, err := s.fileLocalSourceStore.ReadSnapshot(path)
	snapshot.SHA256 = strings.Repeat("0", 64)
	return snapshot, err
}

var _ LocalSnapshotStore = fileLocalSourceStore{}
