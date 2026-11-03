package sessioncontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/session"
)

type localStoreFake struct {
	summaries []session.Summary
	entries   []session.Entry
	format    session.TranscriptFormat
	findCalls int
	loadCalls int
}

func (f *localStoreFake) List() ([]session.Summary, error) { return f.summaries, nil }
func (f *localStoreFake) Find(id string) (session.Summary, bool, error) {
	f.findCalls++
	for _, summary := range f.summaries {
		if summary.SessionID == id {
			return summary, true, nil
		}
	}
	return session.Summary{}, false, nil
}
func (f *localStoreFake) LoadWithFormat(string) ([]session.Entry, session.TranscriptFormat, error) {
	f.loadCalls++
	return f.entries, f.format, nil
}

func TestLocalListMapsSummaries(t *testing.T) {
	updated := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	adapter := NewLocalAdapter(&localStoreFake{summaries: []session.Summary{{SessionID: "abc", Title: "Demo", ModTime: updated}}})
	got, err := adapter.List()
	if err != nil || len(got) != 1 {
		t.Fatalf("List() = %#v, %v", got, err)
	}
	if got[0].Ref.String() != "local:abc" || got[0].Title != "Demo" || !got[0].UpdatedAt.Equal(updated) || !got[0].ReadOnly {
		t.Fatalf("unexpected snapshot: %#v", got[0])
	}
}

func TestLocalTenantRefRejected(t *testing.T) {
	fake := &localStoreFake{}
	_, err := NewLocalAdapter(fake).Get(SessionRef{Source: SourceTenant, Key: "1"})
	if err == nil || err.(*RefError).Code != CodeInvalidRef || fake.findCalls != 0 {
		t.Fatalf("Get tenant ref: err=%v findCalls=%d", err, fake.findCalls)
	}
}

func TestLocalDirectMalformedRefRejectedBeforeStore(t *testing.T) {
	fake := &localStoreFake{}
	_, err := NewLocalAdapter(fake).Get(SessionRef{Source: SourceLocal, Key: "../x"})
	var refErr *RefError
	if !errors.As(err, &refErr) || refErr.Code != CodeInvalidRef || fake.findCalls != 0 {
		t.Fatalf("Get malformed ref: err=%v findCalls=%d", err, fake.findCalls)
	}
}

func TestLocalGetIsReadOnlyAndCountsEntries(t *testing.T) {
	fake := &localStoreFake{summaries: []session.Summary{{SessionID: "abc", Path: "/ignored", Title: "Demo"}}, entries: []session.Entry{{Type: "message"}}, format: session.TranscriptFormatGolangCCV1}
	got, err := NewLocalAdapter(fake).Get(SessionRef{Source: SourceLocal, Key: "abc"})
	if err != nil || got.EntryCount != 1 || got.Format != session.TranscriptFormatGolangCCV1 || !got.Snapshot.ReadOnly || fake.loadCalls != 1 {
		t.Fatalf("Get() = %#v, %v calls=%d", got, err, fake.loadCalls)
	}
}

func TestLocalReadStateIdle(t *testing.T) {
	fake := &localStoreFake{summaries: []session.Summary{{SessionID: "abc"}}}
	status, err := NewLocalAdapter(fake).ReadState(SessionRef{Source: SourceLocal, Key: "abc"})
	if err != nil || status != StatusIdle {
		t.Fatalf("ReadState() = %q, %v", status, err)
	}
}

func TestLocalMissingSessionIsTyped(t *testing.T) {
	_, err := NewLocalAdapter(&localStoreFake{}).Get(SessionRef{Source: SourceLocal, Key: "missing"})
	var sessionErr *LocalSessionError
	if !errors.As(err, &sessionErr) || sessionErr.Code != CodeLocalSessionNotFound {
		t.Fatalf("Get missing error = %v, want typed not-found", err)
	}
}

func TestLocalUnsupportedFormatIsInspectionOnly(t *testing.T) {
	fake := &localStoreFake{
		summaries: []session.Summary{{SessionID: "native", Path: "/ignored"}},
		format:    session.TranscriptFormatClaudeCodeNative,
	}
	got, err := NewLocalAdapter(fake).Get(SessionRef{Source: SourceLocal, Key: "native"})
	if err != nil || got.Format != session.TranscriptFormatClaudeCodeNative || got.InspectionDetail == "" || !got.Snapshot.ReadOnly {
		t.Fatalf("unsupported inspection = %#v, %v", got, err)
	}
}

func TestLocalReadStateRecognizesTerminalMarker(t *testing.T) {
	fake := &localStoreFake{
		summaries: []session.Summary{{SessionID: "done", Path: "/ignored"}},
		entries:   []session.Entry{{Type: "session_completed"}},
	}
	status, err := NewLocalAdapter(fake).ReadState(SessionRef{Source: SourceLocal, Key: "done"})
	if err != nil || status != StatusCompleted {
		t.Fatalf("ReadState terminal = %q, %v", status, err)
	}
}

func TestLocalEvidenceIndexExcludesContent(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	if err := os.WriteFile(path, []byte(`{"type":"message","content":"secret"}
`), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &localStoreFake{
		summaries: []session.Summary{{SessionID: "evidence", Path: path}},
		entries:   []session.Entry{{Type: "message", Content: "secret", Timestamp: time.Unix(10, 0)}},
	}
	got, err := NewLocalAdapter(fake).EvidenceIndex(SessionRef{Source: SourceLocal, Key: "evidence"})
	if err != nil || got.Locator != "local:evidence#snapshot" || got.SHA256 == "" || len(got.Events) != 1 {
		t.Fatalf("EvidenceIndex = %#v, %v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret")) {
		t.Fatal("evidence index contains transcript content")
	}
}
