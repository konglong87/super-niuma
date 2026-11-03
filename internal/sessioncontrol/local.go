package sessioncontrol

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/session"
)

// LocalSessionStore is the read-only surface needed by LocalAdapter.
// Implementations may be backed by the user's filesystem or an in-memory fake.
type LocalSessionStore interface {
	List() ([]session.Summary, error)
	Find(string) (session.Summary, bool, error)
	LoadWithFormat(string) ([]session.Entry, session.TranscriptFormat, error)
}

type defaultLocalSessionStore struct{ store session.Store }

func (s defaultLocalSessionStore) List() ([]session.Summary, error) { return s.store.List() }
func (s defaultLocalSessionStore) Find(id string) (session.Summary, bool, error) {
	return s.store.Find(id)
}
func (defaultLocalSessionStore) LoadWithFormat(path string) ([]session.Entry, session.TranscriptFormat, error) {
	return session.LoadWithFormat(path)
}

type LocalSnapshot struct {
	ID             uint64
	Ref            SessionRef
	Source         string
	Title          string
	Status         SessionStatus
	Format         session.TranscriptFormat
	EntryCount     int
	Model          string
	Provider       string
	PermissionMode string
	Effort         string
	PromptMode     string
	CWD            string
	StartedAt      time.Time
	UpdatedAt      time.Time
	ActiveRunID    uint64
	Links          []SessionLink
	ReadOnly       bool
	Channel        *SessionChannel
}

type LocalDetail struct {
	Snapshot LocalSnapshot
	Format   session.TranscriptFormat
	// EntryCount intentionally omits transcript content from the read response.
	EntryCount       int
	InspectionDetail string
}

type LocalSessionErrorCode string

const (
	CodeLocalSessionNotFound LocalSessionErrorCode = "session_not_found"
)

type LocalSessionError struct {
	Code    LocalSessionErrorCode
	Message string
}

func (e *LocalSessionError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

type EvidenceEventSummary struct {
	Type      string
	Timestamp time.Time
	Turn      int
	ToolName  string
	IsError   bool
}

type LocalEvidenceIndex struct {
	Ref     SessionRef
	Locator string
	SHA256  string
	Events  []EvidenceEventSummary
}

type LocalAdapter struct{ store LocalSessionStore }

func NewLocalAdapter(store LocalSessionStore) *LocalAdapter {
	if store == nil {
		store = defaultLocalSessionStore{store: session.DefaultStore()}
	}
	return &LocalAdapter{store: store}
}

func (a *LocalAdapter) List() ([]LocalSnapshot, error) {
	summaries, err := a.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]LocalSnapshot, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, LocalSnapshot{
			Ref:       SessionRef{Source: SourceLocal, Key: summary.SessionID},
			Title:     summary.Title,
			Status:    StatusIdle,
			UpdatedAt: summary.ModTime,
			ReadOnly:  true,
		})
	}
	return out, nil
}

func (a *LocalAdapter) Get(ref SessionRef) (LocalDetail, error) {
	if err := validateLocalRef(ref); err != nil {
		return LocalDetail{}, err
	}
	summary, ok, err := a.store.Find(ref.Key)
	if err != nil {
		return LocalDetail{}, err
	}
	if !ok {
		return LocalDetail{}, localSessionNotFound(ref.Key)
	}
	entries, format, err := a.store.LoadWithFormat(summary.Path)
	if err != nil {
		return LocalDetail{}, err
	}
	status := terminalStatus(entries)
	detail := inspectionDetail(format)
	return LocalDetail{
		Snapshot:         LocalSnapshot{Ref: ref, Title: summary.Title, Status: status, Format: format, EntryCount: len(entries), UpdatedAt: summary.ModTime, ReadOnly: true},
		Format:           format,
		EntryCount:       len(entries),
		InspectionDetail: detail,
	}, nil
}

func (a *LocalAdapter) ReadState(ref SessionRef) (SessionStatus, error) {
	if err := validateLocalRef(ref); err != nil {
		return "", err
	}
	summary, ok, err := a.store.Find(ref.Key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", localSessionNotFound(ref.Key)
	}
	entries, _, err := a.store.LoadWithFormat(summary.Path)
	if err != nil {
		return "", err
	}
	return terminalStatus(entries), nil
}

func (a *LocalAdapter) EvidenceIndex(ref SessionRef) (LocalEvidenceIndex, error) {
	if err := validateLocalRef(ref); err != nil {
		return LocalEvidenceIndex{}, err
	}
	summary, ok, err := a.store.Find(ref.Key)
	if err != nil {
		return LocalEvidenceIndex{}, err
	}
	if !ok {
		return LocalEvidenceIndex{}, localSessionNotFound(ref.Key)
	}
	entries, _, err := a.store.LoadWithFormat(summary.Path)
	if err != nil {
		return LocalEvidenceIndex{}, err
	}
	events := make([]EvidenceEventSummary, 0, len(entries))
	for _, entry := range entries {
		events = append(events, EvidenceEventSummary{Type: entry.Type, Timestamp: entry.Timestamp, Turn: entry.Turn, ToolName: entry.ToolName, IsError: entry.IsError})
	}
	payload, err := json.Marshal(events)
	if err != nil {
		return LocalEvidenceIndex{}, err
	}
	hash := sha256.Sum256(payload)
	return LocalEvidenceIndex{Ref: ref, Locator: ref.String() + "#snapshot", SHA256: fmt.Sprintf("%x", hash[:]), Events: events}, nil
}

func localSessionNotFound(id string) error {
	return &LocalSessionError{Code: CodeLocalSessionNotFound, Message: fmt.Sprintf("local session %q not found", id)}
}

func inspectionDetail(format session.TranscriptFormat) string {
	switch format {
	case session.TranscriptFormatClaudeCodeNative:
		return "transcript uses Claude Code native format; inspection is read-only and cannot create model context"
	case session.TranscriptFormatMixed:
		return "transcript contains mixed schemas; inspection is read-only and cannot create model context"
	default:
		return ""
	}
}

func terminalStatus(entries []session.Entry) SessionStatus {
	status := StatusIdle
	for _, entry := range entries {
		switch strings.ToLower(strings.TrimSpace(entry.Type)) {
		case "session_completed", "session_complete", "completed", "complete", "terminal_completed", "run_finished", "done":
			status = StatusCompleted
		case "session_failed", "failed", "terminal_failed", "runtime_error":
			status = StatusFailed
		case "session_stopped", "stopped", "cancelled", "canceled", "timeout", "terminal_stopped":
			status = StatusStopped
		}
	}
	return status
}

func validateLocalRef(ref SessionRef) error {
	parsed, err := ParseRef(ref.String())
	if err != nil {
		return err
	}
	if parsed != ref {
		return invalidRef("local session ref must use its canonical source and key")
	}
	if ref.Source == SourceTenant {
		return &RefError{Code: CodeInvalidRef, Message: "local adapter does not accept tenant refs"}
	}
	if ref.Source != SourceLocal || ref.Key == "" {
		return &RefError{Code: CodeInvalidRef, Message: "local adapter requires a local ref"}
	}
	return nil
}
