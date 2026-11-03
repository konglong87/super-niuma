package handoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
)

// LocalSnapshotStore returns exactly one immutable byte snapshot per capture.
// Its implementation owns the pre/post file consistency checks; the reader
// parses only that returned byte slice and drops it after structured extraction.
type LocalSnapshotStore interface {
	Find(string) (session.Summary, bool, error)
	ReadSnapshot(string) (ImmutableLocalSnapshot, error)
	ReadEntrySnapshot(string, LocalEntrySelector) (LocalEntrySnapshot, error)
}

type ImmutableLocalSnapshot struct {
	Data   []byte
	SHA256 string
}

type LocalEntrySelector struct {
	EntryID string
	Line    int
}

type LocalEntrySnapshot struct {
	Entry  session.Entry
	Format session.TranscriptFormat
}

type LocalSourceReader struct{ store LocalSnapshotStore }

func NewLocalSourceReader(store LocalSnapshotStore) *LocalSourceReader {
	return &LocalSourceReader{store: store}
}

// DefaultLocalSnapshotStore bridges the existing local session index to one
// byte snapshot. It detects a writer winning the capture race before parsing.
type localSnapshotOps struct {
	stat     func(string) (os.FileInfo, error)
	readFile func(string) ([]byte, error)
}

type DefaultLocalSnapshotStore struct {
	Store session.Store
	ops   *localSnapshotOps
}

func NewDefaultLocalSourceReader() *LocalSourceReader {
	return NewLocalSourceReader(DefaultLocalSnapshotStore{Store: session.DefaultStore()})
}

func (s DefaultLocalSnapshotStore) Find(id string) (session.Summary, bool, error) {
	return s.Store.Find(id)
}

func (s DefaultLocalSnapshotStore) ReadSnapshot(path string) (ImmutableLocalSnapshot, error) {
	ops := s.snapshotOps()
	before, err := ops.stat(path)
	if err != nil {
		return ImmutableLocalSnapshot{}, err
	}
	first, err := ops.readFile(path)
	if err != nil {
		return ImmutableLocalSnapshot{}, err
	}
	second, err := ops.readFile(path)
	if err != nil {
		return ImmutableLocalSnapshot{}, err
	}
	after, err := ops.stat(path)
	if err != nil {
		return ImmutableLocalSnapshot{}, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return ImmutableLocalSnapshot{}, &Error{Code: CodeSourceChanged, Message: "local transcript changed while capturing"}
	}
	firstHash := sha256.Sum256(first)
	secondHash := sha256.Sum256(second)
	if firstHash != secondHash || !bytes.Equal(first, second) {
		return ImmutableLocalSnapshot{}, &Error{Code: CodeSourceChanged, Message: "local transcript bytes changed while capturing"}
	}
	return ImmutableLocalSnapshot{Data: second, SHA256: hex.EncodeToString(secondHash[:])}, nil
}

func (s DefaultLocalSnapshotStore) ReadEntrySnapshot(path string, selector LocalEntrySelector) (LocalEntrySnapshot, error) {
	snapshot, err := s.ReadSnapshot(path)
	if err != nil {
		return LocalEntrySnapshot{}, err
	}
	return exactLocalEntry(snapshot, selector)
}

func (s DefaultLocalSnapshotStore) snapshotOps() localSnapshotOps {
	if s.ops != nil && s.ops.stat != nil && s.ops.readFile != nil {
		return *s.ops
	}
	return localSnapshotOps{stat: os.Stat, readFile: os.ReadFile}
}

func (r *LocalSourceReader) Capture(_ context.Context, request sessioncontrol.RequestContext, ref sessioncontrol.SessionRef) (SourceSnapshot, error) {
	if err := validateReaderRequest(request, ref, sessioncontrol.SourceLocal); err != nil {
		return SourceSnapshot{}, err
	}
	if r == nil || r.store == nil {
		return SourceSnapshot{}, &Error{Code: CodeInvalidPackage, Message: "local snapshot store is required"}
	}
	summary, ok, err := r.store.Find(ref.Key)
	if err != nil {
		return SourceSnapshot{}, err
	}
	if !ok || summary.SessionID != ref.Key {
		return SourceSnapshot{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeNotFound, Message: "local handoff source was not found"}
	}
	snapshotBytes, err := r.store.ReadSnapshot(summary.Path)
	if err != nil {
		return SourceSnapshot{}, normalizeLocalSourceError(err)
	}
	if err := validateImmutableLocalSnapshot(snapshotBytes); err != nil {
		return SourceSnapshot{}, err
	}
	entries, format, err := session.ParseWithFormat(snapshotBytes.Data)
	snapshotBytes.Data = nil
	if err != nil {
		return SourceSnapshot{}, err
	}
	if format != session.TranscriptFormatGolangCCV1 && format != session.TranscriptFormatGolangCCV2 {
		return SourceSnapshot{}, &Error{Code: CodeUnsupportedSource, Message: "local transcript format cannot be captured for handoff"}
	}
	if len(entries) == 0 {
		return SourceSnapshot{}, &Error{Code: CodeUnsupportedSource, Message: "empty local transcript cannot be captured for handoff"}
	}
	snapshot := localSnapshot(ref, entries, format)
	if snapshot.Source.Cursor == "" {
		return SourceSnapshot{}, &Error{Code: CodeUnsupportedSource, Message: "local transcript has no active handoff cursor"}
	}
	return snapshot, nil
}

func (r *LocalSourceReader) Resolve(ctx context.Context, request sessioncontrol.RequestContext, ref sessioncontrol.SessionRef, locator, expectedSHA256 string) (ResolvedEvidence, error) {
	_ = ctx
	if err := validateReaderRequest(request, ref, sessioncontrol.SourceLocal); err != nil {
		return ResolvedEvidence{}, err
	}
	if r == nil || r.store == nil {
		return ResolvedEvidence{}, &Error{Code: CodeInvalidPackage, Message: "local snapshot store is required"}
	}
	summary, ok, err := r.store.Find(ref.Key)
	if err != nil {
		return ResolvedEvidence{}, err
	}
	if !ok || summary.SessionID != ref.Key {
		return ResolvedEvidence{}, localEvidenceNotFound()
	}
	selector, err := parseLocalEvidenceLocator(ref, locator)
	if err != nil {
		return ResolvedEvidence{}, err
	}
	entrySnapshot, err := r.store.ReadEntrySnapshot(summary.Path, selector)
	if err != nil {
		return ResolvedEvidence{}, normalizeExactLocalError(err)
	}
	evidence := evidenceForLocalEntry(locator, entrySnapshot.Entry)
	if evidence.SHA256 != expectedSHA256 {
		return ResolvedEvidence{}, &Error{Code: CodeInvalidHash, Message: "evidence hash does not match its current source record"}
	}
	return ResolvedEvidence{Ref: evidence.Ref, Claim: evidence.Claim, Verification: evidence.Verification, SHA256: evidence.SHA256}, nil
}

func localSnapshot(ref sessioncontrol.SessionRef, entries []session.Entry, format session.TranscriptFormat) SourceSnapshot {
	chain := session.CurrentChain(entries)
	snapshot := SourceSnapshot{Source: Source{Ref: ref.String()}, Budget: Budget{LimitTokens: DefaultPackageTokenLimit}}
	if format == session.TranscriptFormatGolangCCV2 {
		if leaf := session.ActiveLeaf(entries); leaf != "" {
			snapshot.Source.Cursor = CursorPrefixEntry + ":" + leaf
		}
	} else {
		snapshot.Source.Cursor = CursorPrefixLine + ":" + strconv.Itoa(len(entries))
	}
	for index, entry := range chain {
		locator := localLocator(ref, entry, format, index+1)
		if strings.EqualFold(entry.Type, "message") && strings.EqualFold(entry.Role, "user") {
			snapshot.Objectives = append(snapshot.Objectives, Candidate{Locator: locator, Text: entry.Content, Precedence: PrecedenceUserInstruction})
		}
		if entry.Type == "" {
			continue
		}
		evidence := evidenceForLocalEntry(locator, entry)
		snapshot.Evidence = append(snapshot.Evidence, evidence)
		if fact := localEntryFact(locator, entry); fact != nil {
			snapshot.Facts = append(snapshot.Facts, *fact)
		}
	}
	return snapshot
}

func localLocator(ref sessioncontrol.SessionRef, entry session.Entry, format session.TranscriptFormat, line int) string {
	if format == session.TranscriptFormatGolangCCV2 {
		return ref.String() + "#entry:" + entry.ID
	}
	return ref.String() + "#line:" + strconv.Itoa(line)
}

func immutableLocalSnapshot(data []byte) ImmutableLocalSnapshot {
	sum := sha256.Sum256(data)
	return ImmutableLocalSnapshot{Data: data, SHA256: hex.EncodeToString(sum[:])}
}

func validateImmutableLocalSnapshot(snapshot ImmutableLocalSnapshot) error {
	actual := immutableLocalSnapshot(snapshot.Data).SHA256
	if snapshot.SHA256 == "" || snapshot.SHA256 != actual {
		return &Error{Code: CodeSourceChanged, Message: "local snapshot hash does not match parsing bytes"}
	}
	return nil
}

func exactLocalEntry(snapshot ImmutableLocalSnapshot, selector LocalEntrySelector) (LocalEntrySnapshot, error) {
	if err := validateImmutableLocalSnapshot(snapshot); err != nil {
		return LocalEntrySnapshot{}, err
	}
	entries, format, err := session.ParseWithFormat(snapshot.Data)
	if err != nil {
		return LocalEntrySnapshot{}, err
	}
	if selector.EntryID != "" {
		if format != session.TranscriptFormatGolangCCV2 {
			return LocalEntrySnapshot{}, localEvidenceNotFound()
		}
		for _, entry := range entries {
			if entry.ID == selector.EntryID {
				return LocalEntrySnapshot{Entry: entry, Format: format}, nil
			}
		}
		return LocalEntrySnapshot{}, localEvidenceNotFound()
	}
	if format != session.TranscriptFormatGolangCCV1 || selector.Line <= 0 || selector.Line > len(entries) {
		return LocalEntrySnapshot{}, localEvidenceNotFound()
	}
	return LocalEntrySnapshot{Entry: entries[selector.Line-1], Format: format}, nil
}

func parseLocalEvidenceLocator(ref sessioncontrol.SessionRef, locator string) (LocalEntrySelector, error) {
	if !validEvidenceRef(ref.String(), locator) {
		return LocalEntrySelector{}, localEvidenceNotFound()
	}
	source, typed, ok := strings.Cut(locator, "#")
	if !ok || source != ref.String() {
		return LocalEntrySelector{}, localEvidenceNotFound()
	}
	kind, value, ok := strings.Cut(typed, ":")
	if !ok || value == "" {
		return LocalEntrySelector{}, localEvidenceNotFound()
	}
	switch kind {
	case CursorPrefixEntry:
		return LocalEntrySelector{EntryID: value}, nil
	case CursorPrefixLine:
		line, parseErr := strconv.Atoi(value)
		if parseErr != nil || line <= 0 {
			return LocalEntrySelector{}, localEvidenceNotFound()
		}
		return LocalEntrySelector{Line: line}, nil
	default:
		return LocalEntrySelector{}, localEvidenceNotFound()
	}
}

func normalizeExactLocalError(err error) error {
	var typed *Error
	if errors.As(err, &typed) && typed.Code == CodeSourceChanged {
		return typed
	}
	return localEvidenceNotFound()
}

func localEvidenceNotFound() error {
	return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeNotFound, Message: "handoff evidence was not found in the local source"}
}

func evidenceForLocalEntry(locator string, entry session.Entry) Evidence {
	verification, verifier := localEntryVerification(entry.Type)
	return Evidence{Ref: locator, Claim: capText(entry.Type + localToolSuffix(entry.ToolName)), Verification: verification, SHA256: canonicalRecordHash(struct {
		ID       string `json:"id"`
		ParentID string `json:"parent_id"`
		Type     string `json:"type"`
		Role     string `json:"role"`
		Content  string `json:"content"`
		ToolName string `json:"tool_name"`
		IsError  bool   `json:"is_error"`
		Turn     int    `json:"turn"`
	}{entry.ID, entry.ParentID, entry.Type, entry.Role, entry.Content, entry.ToolName, entry.IsError, entry.Turn}), Verifier: localVerifier(locator, verification, verifier, entry)}
}

func localToolSuffix(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return ": " + strings.TrimSpace(name)
}
func localEntryVerification(entryType string) (Verification, VerifierKind) {
	switch strings.TrimSpace(entryType) {
	case "tool_result":
		return VerificationVerified, VerifierToolTrace
	case "file_change":
		return VerificationVerified, VerifierFileChange
	default:
		if strings.Contains(strings.ToLower(entryType), "capability") {
			return VerificationVerified, VerifierCapabilityLoop
		}
		return VerificationReported, ""
	}
}
func localVerifier(locator string, verification Verification, kind VerifierKind, entry session.Entry) Verifier {
	if verification != VerificationVerified {
		return Verifier{}
	}
	return Verifier{Kind: kind, Ref: locator, SHA256: canonicalRecordHash(struct{ Type, ID string }{entry.Type, entry.ID})}
}
func localEntryFact(locator string, entry session.Entry) *FactCandidate {
	if entry.IsError {
		return &FactCandidate{Locator: locator, Text: entry.Type + " failed", Kind: FactRisk, Precedence: PrecedenceStructuredFact}
	}
	return nil
}
func normalizeLocalSourceError(err error) error {
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return fmt.Errorf("local handoff snapshot: %w", err)
}
