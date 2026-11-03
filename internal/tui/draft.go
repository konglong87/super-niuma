package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const draftSchemaVersion = 1

// DraftStore persists only unsubmitted composer state. It is deliberately
// separate from the transcript recorder so draft text never becomes model
// context or an auditable conversation message.
type DraftStore interface {
	Load(sessionID, cwd string) (Draft, bool, error)
	Save(draft Draft) error
	Clear(sessionID, cwd string) error
}

type Draft struct {
	SchemaVersion  int                  `json:"schema_version"`
	SessionID      string               `json:"session_id"`
	CWD            string               `json:"cwd"`
	Text           string               `json:"text,omitempty"`
	AttachmentRefs []DraftAttachmentRef `json:"attachment_refs,omitempty"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

type DraftAttachmentRef struct {
	ID        int    `json:"id"`
	Type      string `json:"type,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Name      string `json:"name,omitempty"`
	Path      string `json:"path,omitempty"`
	URL       string `json:"url,omitempty"`
}

type fileDraftStore struct {
	root string
}

func defaultDraftStore() DraftStore {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return NewFileDraftStore("")
	}
	return NewFileDraftStore(filepath.Join(home, ".niuma", "drafts"))
}

func NewFileDraftStore(root string) DraftStore {
	return &fileDraftStore{root: strings.TrimSpace(root)}
}

func (s *fileDraftStore) Load(sessionID, cwd string) (Draft, bool, error) {
	path, err := s.pathFor(sessionID, cwd)
	if err != nil {
		return Draft{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, fmt.Errorf("read tui draft: %w", err)
	}
	var draft Draft
	if err := json.Unmarshal(data, &draft); err != nil {
		return Draft{}, false, fmt.Errorf("decode tui draft: %w", err)
	}
	if draft.SchemaVersion != draftSchemaVersion || draft.SessionID != strings.TrimSpace(sessionID) || filepath.Clean(draft.CWD) != filepath.Clean(strings.TrimSpace(cwd)) {
		return Draft{}, false, nil
	}
	return draft, true, nil
}

func (s *fileDraftStore) Save(draft Draft) error {
	path, err := s.pathFor(draft.SessionID, draft.CWD)
	if err != nil {
		return err
	}
	if strings.TrimSpace(draft.Text) == "" && len(draft.AttachmentRefs) == 0 {
		return s.Clear(draft.SessionID, draft.CWD)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create tui draft directory: %w", err)
	}
	draft.SchemaVersion = draftSchemaVersion
	if draft.UpdatedAt.IsZero() {
		draft.UpdatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return fmt.Errorf("encode tui draft: %w", err)
	}
	tmp, err := os.CreateTemp(s.root, ".draft-*.tmp")
	if err != nil {
		return fmt.Errorf("create tui draft temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect tui draft temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write tui draft: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tui draft: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit tui draft: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func (s *fileDraftStore) Clear(sessionID, cwd string) error {
	path, err := s.pathFor(sessionID, cwd)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear tui draft: %w", err)
	}
	return nil
}

func (s *fileDraftStore) pathFor(sessionID, cwd string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("tui draft store root is empty")
	}
	sessionID = strings.TrimSpace(sessionID)
	cwd = strings.TrimSpace(cwd)
	if sessionID == "" || cwd == "" {
		return "", errors.New("tui draft requires session id and cwd")
	}
	digest := sha256.Sum256([]byte(sessionID + "\x00" + filepath.Clean(cwd)))
	return filepath.Join(s.root, hex.EncodeToString(digest[:])+".json"), nil
}

func (m *Model) loadDraft() {
	if m.draftStore == nil {
		return
	}
	draft, ok, err := m.draftStore.Load(m.welcome.SessionID, m.welcome.CWD)
	if err != nil || !ok {
		return
	}
	m.setTextareaValue(draft.Text)
	m.attachments = make([]Attachment, 0, len(draft.AttachmentRefs))
	for _, ref := range draft.AttachmentRefs {
		m.attachments = append(m.attachments, Attachment{ID: ref.ID, Type: ref.Type, MediaType: ref.MediaType, Name: ref.Name, Path: ref.Path, URL: ref.URL})
	}
	if len(m.attachments) > 0 {
		m.nextAttachmentID = 1
		for _, attachment := range m.attachments {
			if attachment.ID >= m.nextAttachmentID {
				m.nextAttachmentID = attachment.ID + 1
			}
		}
	}
}

func (m *Model) saveDraft() {
	if m.draftStore == nil || strings.TrimSpace(m.welcome.SessionID) == "" || strings.TrimSpace(m.welcome.CWD) == "" {
		return
	}
	refs := make([]DraftAttachmentRef, 0, len(m.attachments))
	for _, attachment := range m.attachments {
		refs = append(refs, DraftAttachmentRef{ID: attachment.ID, Type: attachment.Type, MediaType: attachment.MediaType, Name: attachment.Name, Path: attachment.Path, URL: attachment.URL})
	}
	if err := m.draftStore.Save(Draft{SessionID: m.welcome.SessionID, CWD: m.welcome.CWD, Text: m.textarea.Value(), AttachmentRefs: refs, UpdatedAt: m.now().UTC()}); err != nil {
		m.err = err
	}
}

func (m *Model) clearDraft() {
	if m.draftStore == nil || strings.TrimSpace(m.welcome.SessionID) == "" || strings.TrimSpace(m.welcome.CWD) == "" {
		return
	}
	if err := m.draftStore.Clear(m.welcome.SessionID, m.welcome.CWD); err != nil {
		m.err = err
	}
}
