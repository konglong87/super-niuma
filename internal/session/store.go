package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/identity"
	"github.com/konglong87/super-niuma/internal/product"
)

type Entry struct {
	ID     string `json:"id,omitempty"`
	Schema string `json:"schema,omitempty"`
	// Sequence is a stable append order for cross-surface session events. It is
	// independent from the message-graph parent chain and is only populated for
	// entries written through AppendEvent.
	Sequence  uint64 `json:"sequence,omitempty"`
	TaskID    uint64 `json:"task_id,omitempty"`
	EventType string `json:"event_type,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
	Source    string `json:"source,omitempty"`
	Surface   string `json:"surface,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Scope     string `json:"scope,omitempty"`
	TenantKey string `json:"tenant_key,omitempty"`
	UserKey   string `json:"user_key,omitempty"`
	// ParentID links a v2 message-graph entry to its predecessor on the chain.
	// Empty on the root and on every v1 entry. See internal/session/graph.go.
	ParentID string `json:"parent_id,omitempty"`
	// LeafID and Reason are only set on branch_head entries: the active leaf this
	// pointer moves to, and why (new_turn/rewind/redo).
	LeafID                              string              `json:"leaf_id,omitempty"`
	Reason                              string              `json:"reason,omitempty"`
	Timestamp                           time.Time           `json:"timestamp"`
	Type                                string              `json:"type"`
	Role                                string              `json:"role,omitempty"`
	Content                             string              `json:"content,omitempty"`
	Signature                           string              `json:"signature,omitempty"`
	ToolID                              string              `json:"tool_id,omitempty"`
	ToolName                            string              `json:"tool_name,omitempty"`
	IsError                             bool                `json:"is_error,omitempty"`
	Name                                string              `json:"name,omitempty"`
	Turn                                int                 `json:"turn,omitempty"`
	Provider                            string              `json:"provider,omitempty"`
	Estimated                           bool                `json:"estimated,omitempty"`
	UsageSource                         string              `json:"usage_source,omitempty"`
	Model                               string              `json:"model,omitempty"`
	InputTokens                         int                 `json:"input_tokens,omitempty"`
	CacheCreationInputTokens            int                 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int                 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int                 `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int                 `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	ServiceTier                         string              `json:"service_tier,omitempty"`
	InferenceGeo                        string              `json:"inference_geo,omitempty"`
	Speed                               string              `json:"speed,omitempty"`
	OutputTokens                        int                 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens               int                 `json:"reasoning_output_tokens,omitempty"`
	CompactMetadata                     json.RawMessage     `json:"compact_metadata,omitempty"`
	Metadata                            json.RawMessage     `json:"metadata,omitempty"`
	Replacements                        []ReplacementRecord `json:"replacements,omitempty"`
}

type ReplacementRecord struct {
	Kind        string `json:"kind"`
	ToolUseID   string `json:"tool_use_id"`
	Replacement string `json:"replacement"`
}

type Store struct {
	Root                          string
	TranscriptProjectsRoot        string
	LegacyTranscriptProjectsRoots []string
	// SchemaV2 makes NewRecorder* create v2 message-graph transcripts. It gates
	// the graph write path for gradual rollout; callers set it from a feature
	// flag. Resuming an existing transcript ignores this flag and follows the
	// schema already on disk (OpenRecorder), so a file is never schema-mixed.
	SchemaV2 bool
}

type Recorder struct {
	mu        sync.Mutex
	SessionID string
	Path      string
	file      *os.File
	encoder   *json.Encoder
	lockPath  string
	// schema is non-empty ("niuma.transcript.v2") when this recorder writes a
	// message-graph transcript; leaf tracks the current active leaf so each new
	// entry chains from it via parent_id.
	schema string
	leaf   string
}

type Summary struct {
	SessionID string    `json:"session_id"`
	Path      string    `json:"path"`
	ModTime   time.Time `json:"mod_time"`
	Title     string    `json:"title,omitempty"`
}

type UsageSummary struct {
	Sessions                            int                   `json:"sessions"`
	InputTokens                         int                   `json:"input_tokens"`
	CacheCreationInputTokens            int                   `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int                   `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int                   `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int                   `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	OutputTokens                        int                   `json:"output_tokens"`
	TotalTokens                         int                   `json:"total_tokens"`
	CostUSD                             float64               `json:"cost_usd"`
	Models                              map[string]ModelUsage `json:"models,omitempty"`
	// UnknownPricingModels lists models excluded from CostUSD because no price is
	// known for them. Without it a $0 total is indistinguishable from a free
	// session — configure settings.modelPricing for these (AUDIT-P0-15).
	UnknownPricingModels []string    `json:"unknown_pricing_models,omitempty"`
	TurnUsages           []TurnUsage `json:"turns,omitempty"`
}

type ModelUsage struct {
	InputTokens                         int     `json:"input_tokens"`
	CacheCreationInputTokens            int     `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int     `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int     `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int     `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	OutputTokens                        int     `json:"output_tokens"`
	TotalTokens                         int     `json:"total_tokens"`
	CostUSD                             float64 `json:"cost_usd"`
	// CostKnown is false when no price is known for the model, so CostUSD is 0
	// because it is unknown rather than because the model is free.
	CostKnown bool `json:"cost_known"`
}

type RewindResult struct {
	SessionID      string `json:"session_id"`
	Checkpoint     string `json:"checkpoint,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	EntriesKept    int    `json:"entries_kept"`
	EntriesRemoved int    `json:"entries_removed"`
	FilesRestored  int    `json:"files_restored"`
	TranscriptKept bool   `json:"transcript_kept,omitempty"`
	// MetadataDegraded lists filesystem metadata that could not be restored
	// (e.g. ownership without privilege, unsupported xattrs). Content and mode
	// were still restored; these are observable degradations, not failures.
	MetadataDegraded []string `json:"metadata_degraded,omitempty"`
}

type ForkResult struct {
	SourceSessionID string `json:"source_session_id"`
	SessionID       string `json:"session_id"`
	Path            string `json:"path"`
	Checkpoint      string `json:"checkpoint,omitempty"`
	EntriesCopied   int    `json:"entries_copied"`
}

type fileChangeSnapshot struct {
	Path               string          `json:"path"`
	Before             string          `json:"before"`
	BeforeExists       bool            `json:"before_exists"`
	After              string          `json:"after"`
	AfterExists        bool            `json:"after_exists"`
	AfterSHA256        string          `json:"after_sha256,omitempty"` // tombstone signal for redo; see afterAsBefore
	BeforeSnapshotPath string          `json:"before_snapshot_path,omitempty"`
	AfterSnapshotPath  string          `json:"after_snapshot_path,omitempty"`
	BeforeMode         os.FileMode     `json:"before_mode,omitempty"`
	AfterMode          os.FileMode     `json:"after_mode,omitempty"`
	BeforeModeKnown    bool            `json:"before_mode_known,omitempty"`
	AfterModeKnown     bool            `json:"after_mode_known,omitempty"`
	ModeChanged        bool            `json:"mode_changed,omitempty"`
	BeforeIsSymlink    bool            `json:"before_is_symlink,omitempty"`
	AfterIsSymlink     bool            `json:"after_is_symlink,omitempty"`
	BeforeLinkTarget   string          `json:"before_link_target,omitempty"`
	AfterLinkTarget    string          `json:"after_link_target,omitempty"`
	BeforeIsDir        bool            `json:"before_is_dir,omitempty"`
	AfterIsDir         bool            `json:"after_is_dir,omitempty"`
	BeforeMetadata     *files.Metadata `json:"before_metadata,omitempty"`
	Source             string          `json:"source,omitempty"`
	MessageID          string          `json:"message_id,omitempty"`
}

func DefaultStore() Store {
	configRoot, err := identity.Default().GlobalConfigRoot()
	if err != nil {
		configRoot = ""
	}
	projectsRoot := strings.TrimSpace(product.Getenv("NIUMA_TRANSCRIPT_PROJECTS_DIR"))
	if projectsRoot == "" && configRoot != "" {
		projectsRoot = filepath.Join(configRoot, "projects")
	}
	store := Store{Root: configRoot, TranscriptProjectsRoot: projectsRoot}
	if strings.TrimSpace(product.Getenv("NIUMA_TRANSCRIPT_PROJECTS_DIR")) == "" {
		if legacyRoot, legacyErr := identity.LegacyOwnedGlobalConfigRoot(); legacyErr == nil && legacyRoot != "" {
			legacyProjects := filepath.Join(legacyRoot, "projects")
			if filepath.Clean(legacyProjects) != filepath.Clean(projectsRoot) {
				store.LegacyTranscriptProjectsRoots = []string{legacyProjects}
			}
		}
	}
	return store
}

func (s Store) IsZero() bool {
	return strings.TrimSpace(s.Root) == "" && strings.TrimSpace(s.TranscriptProjectsRoot) == ""
}

func (s Store) projectsRoot() string {
	if root := strings.TrimSpace(s.TranscriptProjectsRoot); root != "" {
		return root
	}
	if root := strings.TrimSpace(s.Root); root != "" {
		return filepath.Join(root, "projects")
	}
	return ""
}

func (s Store) sessionLockBase(sessionID string) string {
	root := s.projectsRoot()
	if root == "" {
		root = s.Root
	}
	return filepath.Join(root, ".session-locks", strings.TrimSpace(sessionID))
}

func (s Store) NewRecorder(cwd string) (*Recorder, error) {
	if s.projectsRoot() == "" {
		return nil, fmt.Errorf("session root is empty")
	}
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	return s.NewRecorderWithID(cwd, id)
}

func (s Store) NewRecorderWithID(cwd, id string) (*Recorder, error) {
	projectsRoot := s.projectsRoot()
	if projectsRoot == "" {
		return nil, fmt.Errorf("session root is empty")
	}
	if !IsValidID(id) {
		return nil, fmt.Errorf("invalid session id: %s", id)
	}
	dir := filepath.Join(projectsRoot, ProjectSlug(cwd))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".jsonl")
	return withTranscriptLockValue(s.sessionLockBase(id), func() (*Recorder, error) {
		return s.newRecorderAtPath(cwd, id, path)
	})
}

func (s Store) OpenRecorder(sessionID string) (*Recorder, bool, error) {
	if !IsValidID(sessionID) {
		return nil, false, fmt.Errorf("invalid session id: %s", sessionID)
	}
	var recorder *Recorder
	var ok bool
	err := withTranscriptLock(s.sessionLockBase(sessionID), func() error {
		summary, found, err := s.PrepareForWrite(sessionID)
		if err != nil {
			return err
		}
		ok = found
		if !found {
			return nil
		}
		recorder, _, err = s.openRecorderSummary(summary)
		return err
	})
	return recorder, ok, err
}

// OpenOrCreateRecorder opens the requested session when it exists and creates
// it otherwise. The existence check and first write are protected together so
// two independent processes cannot both initialize the same transcript.
func (s Store) OpenOrCreateRecorder(cwd, id string) (*Recorder, bool, error) {
	projectsRoot := s.projectsRoot()
	if projectsRoot == "" {
		return nil, false, fmt.Errorf("session root is empty")
	}
	if !IsValidID(id) {
		return nil, false, fmt.Errorf("invalid session id: %s", id)
	}
	dir := filepath.Join(projectsRoot, ProjectSlug(cwd))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, false, err
	}
	path := filepath.Join(dir, id+".jsonl")
	var recorder *Recorder
	var created bool
	err := withTranscriptLock(s.sessionLockBase(id), func() error {
		summary, ok, err := s.PrepareForWrite(id)
		if err != nil {
			return err
		}
		if ok {
			recorder, _, err = s.openRecorderSummary(summary)
			return err
		}
		recorder, err = s.newRecorderAtPath(cwd, id, path)
		created = err == nil
		return err
	})
	return recorder, created, err
}

func (s Store) newRecorderAtPath(cwd, id, path string) (*Recorder, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	schema, leaf := "", ""
	if info.Size() > 0 {
		entries, err := Load(path)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if IsV2Entries(entries) {
			schema = SchemaV2
			leaf = ActiveLeaf(entries)
		}
	}
	if schema == "" && s.SchemaV2 {
		schema = SchemaV2
	}
	recorder := &Recorder{
		SessionID: id,
		Path:      path,
		file:      file,
		encoder:   json.NewEncoder(file),
		schema:    schema,
		leaf:      leaf,
		lockPath:  transcriptLockPath(path),
	}
	if info.Size() == 0 && schema != "" {
		if err := recorder.writeSessionMetaUnlocked(cwd); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	return recorder, nil
}

func (s Store) openRecorderSummary(summary Summary) (*Recorder, bool, error) {
	schema, leaf := "", ""
	entries, err := Load(summary.Path)
	if err != nil {
		return nil, true, err
	}
	if IsV2Entries(entries) {
		schema = SchemaV2
		leaf = ActiveLeaf(entries)
	}
	file, err := os.OpenFile(summary.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, true, err
	}
	return &Recorder{
		SessionID: summary.SessionID,
		Path:      summary.Path,
		file:      file,
		encoder:   json.NewEncoder(file),
		schema:    schema,
		leaf:      leaf,
		lockPath:  transcriptLockPath(summary.Path),
	}, true, nil
}

// PrepareForWrite copy-on-write migrates a legacy transcript into the
// canonical projects root. The legacy file remains unchanged for rollback.
func (s Store) PrepareForWrite(sessionID string) (Summary, bool, error) {
	summary, ok, err := s.Find(sessionID)
	if err != nil || !ok {
		return summary, ok, err
	}
	canonicalRoot := strings.TrimSpace(s.projectsRoot())
	if canonicalRoot == "" {
		return summary, true, nil
	}
	for _, legacyRoot := range s.LegacyTranscriptProjectsRoots {
		rel, inside := relativePathWithin(legacyRoot, summary.Path)
		if !inside {
			continue
		}
		target := filepath.Join(canonicalRoot, rel)
		if err := copyFileIfAbsent(summary.Path, target); err != nil {
			return Summary{}, true, err
		}
		info, err := os.Stat(target)
		if err != nil {
			return Summary{}, true, err
		}
		summary.Path = target
		summary.ModTime = info.ModTime()
		summary.Title = transcriptTitle(target)
		return summary, true, nil
	}
	return summary, true, nil
}

func relativePathWithin(root, path string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return rel, true
}

func copyFileIfAbsent(source, target string) error {
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".transcript-migrate-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Publishing with a hard link is atomic and never overwrites a canonical
	// transcript another process may have migrated concurrently.
	if err := os.Link(tmpPath, target); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// writeSessionMeta emits the mandatory first line of a v2 transcript. It is a
// header (not a chain node), so it carries the schema tag but no parent_id.
func (r *Recorder) writeSessionMeta(cwd string) error {
	return r.withLock(func() error {
		return r.writeSessionMetaUnlocked(cwd)
	})
}

func (r *Recorder) writeSessionMetaUnlocked(cwd string) error {
	meta := map[string]any{
		"app":            product.Name,
		"schema_version": 2,
		"session_id":     r.SessionID,
		"cwd":            cwd,
		"project_slug":   ProjectSlug(cwd),
		"source":         "repl_main_thread",
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return r.appendV2(Entry{Type: EntryTypeSessionMeta, Metadata: raw})
}

func (r *Recorder) Append(entry Entry) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.encoder == nil {
		return nil
	}
	return r.withLock(func() error {
		if r.schema != "" {
			// Another process may have appended since this recorder opened.
			// Refreshing under the same lock keeps the next graph edge attached
			// to the actual on-disk leaf.
			entries, err := Load(r.Path)
			if err != nil {
				return err
			}
			r.leaf = ActiveLeaf(entries)
			return r.appendV2(entry)
		}
		if entry.ID == "" && shouldAutoID(entry) {
			entry.ID = NewEntryID()
		}
		if entry.Timestamp.IsZero() {
			entry.Timestamp = time.Now().UTC()
		}
		return r.encoder.Encode(entry)
	})
}

// AppendEntryToPath appends one transcript entry through the same process and
// cross-process lock used by Recorder.Append. This is for side-channel writers
// such as recap that do not own a live Recorder.
func AppendEntryToPath(path string, entry Entry) error {
	return withTranscriptLock(path, func() error {
		return appendEntryToPathLocked(path, entry)
	})
}

// AppendEntryToPathIfCurrent appends only when the active conversation head is
// still expectedHead. The check and append are deliberately performed under
// one transcript lock so an asynchronous recap cannot become stale between
// validation and publication.
func AppendEntryToPathIfCurrent(path, expectedHead string, entry Entry) (bool, error) {
	return withTranscriptLockValue(path, func() (bool, error) {
		entries, err := LoadConversation(path)
		if err != nil {
			return false, err
		}
		if LatestConversationEntryID(entries) != strings.TrimSpace(expectedHead) {
			return false, nil
		}
		return true, appendEntryToPathLocked(path, entry)
	})
}

func appendEntryToPathLocked(path string, entry Entry) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("transcript path is empty")
	}
	if entry.ID == "" {
		entry.ID = NewEntryID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	if existing, err := Load(path); err == nil && IsV2Entries(existing) {
		entry.Schema = SchemaV2
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	encoderErr := json.NewEncoder(file).Encode(entry)
	if encoderErr != nil {
		_ = file.Close()
		return encoderErr
	}
	return file.Close()
}

func (r *Recorder) withLock(fn func() error) error {
	if r == nil || strings.TrimSpace(r.lockPath) == "" {
		return fn()
	}
	return withTranscriptLock(r.lockPath, fn)
}

// appendV2 writes an entry into a v2 message-graph transcript. Every entry is
// tagged with the schema. session_meta is a header and branch_head is a pointer
// (both carry an id but never join the chain); every other entry is a chain node
// that links to the current active leaf via parent_id and then becomes the new
// leaf, so walking parent_id from the leaf reproduces the conversation order.
func (r *Recorder) appendV2(entry Entry) error {
	entry.Schema = r.schema
	if entry.ID == "" {
		entry.ID = NewEntryID()
	}
	switch entry.Type {
	case EntryTypeBranchHead:
		if entry.LeafID != "" {
			r.leaf = entry.LeafID
		}
	case EntryTypeSessionMeta, EntryTypeRuntimeSpan:
		// Side-band metadata: schema-tagged, but outside the conversation chain.
	default:
		entry.ParentID = r.leaf
		r.leaf = entry.ID
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	return r.encoder.Encode(entry)
}

// IsV2 reports whether this recorder writes a v2 message-graph transcript.
func (r *Recorder) IsV2() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.schema != ""
}

// Leaf returns the current active leaf id (the tip of the conversation). Empty
// for a v1 recorder or before the first chain entry.
func (r *Recorder) Leaf() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.leaf
}

// SyncLeafFromDisk re-reads the transcript and repositions this v2 recorder's
// active leaf to the file's current active leaf. Call it after an out-of-band
// operation (rewind/redo) appended a branch_head to the same file via the store,
// so the NEXT entry this live recorder writes chains from the moved leaf
// (creating a real fork) instead of the stale pre-operation tip. No-op for a v1
// recorder or when the path is unknown.
func (r *Recorder) SyncLeafFromDisk() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.schema == "" || strings.TrimSpace(r.Path) == "" {
		return nil
	}
	entries, err := Load(r.Path)
	if err != nil {
		return err
	}
	r.leaf = ActiveLeaf(entries)
	return nil
}

// MarkTurn appends a branch_head anchoring the current turn at leafID (the user
// message that opened the turn). It is a no-op for a v1 recorder. The active leaf
// keeps advancing as the turn's assistant/tool entries chain on, so these
// per-turn anchors form a continuous branch_head chain over the user messages
// without pinning the tip. See internal/session/graph.go for how the tip resolves.
func (r *Recorder) MarkTurn(leafID string) error {
	if !r.IsV2() || strings.TrimSpace(leafID) == "" {
		return nil
	}
	return r.Append(Entry{Type: EntryTypeBranchHead, LeafID: leafID, Reason: BranchHeadReasonNewTurn})
}

func shouldAutoID(entry Entry) bool {
	switch entry.Type {
	case "message", "tool_call", "tool_result", "checkpoint", "rewind", "fork", "compact_summary", "recap_summary":
		return true
	default:
		return false
	}
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	return r.file.Close()
}

func Load(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []Entry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

func (s Store) List() ([]Summary, error) {
	return s.listAcross(s.readProjectsRoots())
}

func (s Store) ListForCWD(cwd string) ([]Summary, error) {
	roots := s.readProjectsRoots()
	projectRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		projectRoots = append(projectRoots, filepath.Join(root, ProjectSlug(cwd)))
	}
	return s.listAcross(projectRoots)
}

func (s Store) readProjectsRoots() []string {
	roots := make([]string, 0, 1+len(s.LegacyTranscriptProjectsRoots))
	if root := strings.TrimSpace(s.projectsRoot()); root != "" {
		roots = append(roots, root)
	}
	for _, root := range s.LegacyTranscriptProjectsRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		duplicate := false
		for _, existing := range roots {
			if filepath.Clean(existing) == filepath.Clean(root) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			roots = append(roots, root)
		}
	}
	return roots
}

func (s Store) listAcross(roots []string) ([]Summary, error) {
	bySession := make(map[string]Summary)
	for _, root := range roots {
		summaries, err := s.listUnder(root)
		if err != nil {
			return nil, err
		}
		for _, summary := range summaries {
			current, ok := bySession[summary.SessionID]
			if !ok || summary.ModTime.After(current.ModTime) {
				bySession[summary.SessionID] = summary
			}
		}
	}
	summaries := make([]Summary, 0, len(bySession))
	for _, summary := range bySession {
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].ModTime.After(summaries[j].ModTime)
	})
	return summaries, nil
}

func (s Store) listUnder(root string) ([]Summary, error) {
	var summaries []Summary
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		id := strings.TrimSuffix(d.Name(), ".jsonl")
		summaries = append(summaries, Summary{SessionID: id, Path: path, ModTime: info.ModTime(), Title: transcriptTitle(path)})
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].ModTime.After(summaries[j].ModTime)
	})
	return summaries, nil
}

func transcriptTitle(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var sessionTitle string
	var firstUserTitle string
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type == "session" && strings.TrimSpace(entry.Name) != "" {
			sessionTitle = strings.TrimSpace(entry.Name)
			continue
		}
		if entry.Type == "session" && strings.TrimSpace(entry.Content) != "" {
			sessionTitle = strings.TrimSpace(entry.Content)
			continue
		}
		if firstUserTitle == "" && entry.Type == "message" && entry.Role == "user" && strings.TrimSpace(entry.Content) != "" {
			title := strings.Join(strings.Fields(entry.Content), " ")
			runes := []rune(title)
			if len(runes) > 80 {
				firstUserTitle = string(runes[:77]) + "..."
			} else {
				firstUserTitle = title
			}
		}
	}
	if sessionTitle != "" {
		return sessionTitle
	}
	return firstUserTitle
}

func (s Store) Find(sessionID string) (Summary, bool, error) {
	summary, ok, err := s.Locate(sessionID)
	if err != nil || !ok {
		return summary, ok, err
	}
	summary.Title = transcriptTitle(summary.Path)
	return summary, true, nil
}

// Locate finds transcript metadata without reading transcript content.
func (s Store) Locate(sessionID string) (Summary, bool, error) {
	var found Summary
	for _, root := range s.readProjectsRoots() {
		candidate, ok, err := locateTranscriptUnder(root, sessionID)
		if err != nil {
			return Summary{}, false, err
		}
		if ok && (found.Path == "" || candidate.ModTime.After(found.ModTime)) {
			found = candidate
		}
	}
	return found, found.Path != "", nil
}

// LocateInProject checks the known project path before callers fall back to a
// global transcript search. Session event reads usually know the task's cwd,
// so this avoids walking every transcript directory for each active session.
func (s Store) LocateInProject(cwd, sessionID string) (Summary, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !IsValidID(sessionID) {
		return Summary{}, false, fmt.Errorf("invalid session id: %s", sessionID)
	}
	var found Summary
	name := sessionID + ".jsonl"
	for _, root := range s.readProjectsRoots() {
		path := filepath.Join(root, ProjectSlug(cwd), name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Summary{}, false, err
		}
		if info.IsDir() {
			continue
		}
		candidate := Summary{SessionID: sessionID, Path: path, ModTime: info.ModTime()}
		if found.Path == "" || candidate.ModTime.After(found.ModTime) {
			found = candidate
		}
	}
	return found, found.Path != "", nil
}

// LatestForProject returns the most recently modified transcript of the
// project that owns cwd. It backs `session locate` without an id, so an agent
// or script inside a project can resolve "the current session" cheaply.
func (s Store) LatestForProject(cwd string) (Summary, bool, error) {
	if s.projectsRoot() == "" {
		return Summary{}, false, fmt.Errorf("session root is empty")
	}
	summaries, err := s.ListForCWD(cwd)
	if err != nil {
		return Summary{}, false, err
	}
	if len(summaries) == 0 {
		return Summary{}, false, nil
	}
	return summaries[0], true, nil
}

func findTranscriptUnder(root, sessionID string) (Summary, bool, error) {
	summary, ok, err := locateTranscriptUnder(root, sessionID)
	if err != nil || !ok {
		return summary, ok, err
	}
	summary.Title = transcriptTitle(summary.Path)
	return summary, true, nil
}

func locateTranscriptUnder(root, sessionID string) (Summary, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\`) || strings.Contains(sessionID, "..") {
		return Summary{}, false, fmt.Errorf("invalid session id %q", sessionID)
	}
	target := sessionID + ".jsonl"
	var found Summary
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() || entry.Name() != target {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		candidate := Summary{
			SessionID: sessionID,
			Path:      path,
			ModTime:   info.ModTime(),
		}
		if found.Path == "" || candidate.ModTime.After(found.ModTime) {
			found = candidate
		}
		return nil
	})
	if os.IsNotExist(err) {
		return Summary{}, false, nil
	}
	if err != nil {
		return Summary{}, false, err
	}
	if found.Path == "" {
		return Summary{}, false, nil
	}
	return found, true, nil
}

func (s Store) Delete(sessionID string) (bool, error) {
	summary, ok, err := s.Find(sessionID)
	if err != nil || !ok {
		return ok, err
	}
	if err := os.Remove(summary.Path); err != nil && !os.IsNotExist(err) {
		return true, err
	}
	// Reclaim snapshot blobs that no surviving transcript references. This runs
	// after the transcript is removed so the deleted session's orphans become
	// unreachable and collectible.
	_, _ = s.GCSnapshots()
	return true, nil
}

// SnapshotGCResult reports the outcome of a snapshot garbage collection pass.
type SnapshotGCResult struct {
	Removed int   `json:"removed"`
	Freed   int64 `json:"freed_bytes"`
}

// GCSnapshots reclaims content-addressed snapshot blobs that are no longer
// reachable from any live transcript. Reachability is computed from every
// transcript's file_change snapshot references, so a blob still needed for a
// possible rewind is never removed.
func (s Store) GCSnapshots() (SnapshotGCResult, error) {
	reachable, err := s.collectSnapshotReferences()
	if err != nil {
		return SnapshotGCResult{}, err
	}
	removed, freed, err := files.GCSnapshots(reachable)
	if err != nil {
		return SnapshotGCResult{}, err
	}
	return SnapshotGCResult{Removed: removed, Freed: freed}, nil
}

// collectSnapshotReferences scans every transcript under the store and returns
// the set of snapshot blob paths still referenced by a file_change event.
func (s Store) collectSnapshotReferences() (map[string]bool, error) {
	summaries, err := s.List()
	if err != nil {
		return nil, err
	}
	reachable := map[string]bool{}
	for _, summary := range summaries {
		entries, err := Load(summary.Path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Type != "file_change" || strings.TrimSpace(entry.Content) == "" {
				continue
			}
			var change fileChangeSnapshot
			if err := json.Unmarshal([]byte(entry.Content), &change); err != nil {
				continue
			}
			if p := strings.TrimSpace(change.BeforeSnapshotPath); p != "" {
				reachable[p] = true
			}
			if p := strings.TrimSpace(change.AfterSnapshotPath); p != "" {
				reachable[p] = true
			}
		}
	}
	return reachable, nil
}

// FileHistoryGCOptions bounds an independent file-history reclamation pass. It
// governs the file-content blob store separately from conversation transcripts:
// blobs outside the retention window are reclaimed even while their transcript
// still references them (rewind into a reclaimed range reports a clear error).
//
//   - MaxTurns: keep blobs referenced within the most recent N turns (turns are
//     delimited by checkpoint entries). 0 = no turn window.
//   - MaxAgeDays: also drop blobs whose most recent reference is older than this.
//     0 = no age limit.
//   - MaxBytes: hard cap; when in-window blobs exceed it, evict oldest-referenced
//     first until under budget. 0 = no byte cap.
//   - DryRun: compute what would be removed without deleting anything.
type FileHistoryGCOptions struct {
	MaxTurns   int
	MaxAgeDays int
	MaxBytes   int64
	DryRun     bool
	Now        time.Time // injectable for tests; zero uses time.Now().UTC()
}

// FileHistoryGCResult reports a file-history reclamation pass.
type FileHistoryGCResult struct {
	Removed    int      `json:"removed"`
	Freed      int64    `json:"freed_bytes"`
	Kept       int      `json:"kept"`
	KeptBytes  int64    `json:"kept_bytes"`
	DryRun     bool     `json:"dry_run"`
	Candidates []string `json:"candidates,omitempty"`
}

// GCFileHistory reclaims file-content blobs that fall outside the retention
// window (turn / age / byte budget), independently of conversation transcripts.
// Unreadable transcripts abort the pass rather than risk reclaiming blobs whose
// references could not be read.
func (s Store) GCFileHistory(opts FileHistoryGCOptions) (FileHistoryGCResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	inWindow, err := s.collectWindowedReferences(opts, now)
	if err != nil {
		return FileHistoryGCResult{}, err
	}
	blobs, err := files.ListBlobs()
	if err != nil {
		return FileHistoryGCResult{}, err
	}
	sizeByPath := make(map[string]int64, len(blobs))
	for _, b := range blobs {
		sizeByPath[b.Path] = b.Size
	}
	// Byte cap: keep the most-recently-referenced in-window blobs until the
	// budget is exhausted; evict the oldest. Without a cap, keep all in-window.
	kept := map[string]bool{}
	var keptBytes int64
	if opts.MaxBytes > 0 {
		type ref struct {
			path string
			size int64
			t    time.Time
		}
		refs := make([]ref, 0, len(inWindow))
		for p, t := range inWindow {
			refs = append(refs, ref{path: p, size: sizeByPath[p], t: t})
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].t.After(refs[j].t) })
		for _, r := range refs {
			if keptBytes+r.size <= opts.MaxBytes {
				kept[r.path] = true
				keptBytes += r.size
			}
		}
	} else {
		for p := range inWindow {
			kept[p] = true
			keptBytes += sizeByPath[p]
		}
	}
	var candidates []string
	var wouldFree int64
	for _, b := range blobs {
		if kept[b.Path] {
			continue
		}
		candidates = append(candidates, b.Path)
		wouldFree += b.Size
	}
	sort.Strings(candidates)
	result := FileHistoryGCResult{Kept: len(kept), KeptBytes: keptBytes, DryRun: opts.DryRun, Candidates: candidates}
	if opts.DryRun {
		result.Removed = len(candidates)
		result.Freed = wouldFree
		return result, nil
	}
	removed, freed, err := files.GCSnapshots(kept)
	if err != nil {
		return FileHistoryGCResult{}, err
	}
	result.Removed = removed
	result.Freed = freed
	return result, nil
}

// collectWindowedReferences returns, per blob path, the timestamp of its most
// recent in-window file_change reference. A reference is in-window when it is
// within the most recent MaxTurns turns and (if set) newer than the age cutoff.
func (s Store) collectWindowedReferences(opts FileHistoryGCOptions, now time.Time) (map[string]time.Time, error) {
	summaries, err := s.List()
	if err != nil {
		return nil, err
	}
	var ageCutoff time.Time
	if opts.MaxAgeDays > 0 {
		ageCutoff = now.Add(-time.Duration(opts.MaxAgeDays) * 24 * time.Hour)
	}
	inWindow := map[string]time.Time{}
	for _, summary := range summaries {
		entries, err := Load(summary.Path)
		if err != nil {
			return nil, fmt.Errorf("file-history gc: read transcript %s: %w", summary.Path, err)
		}
		// v2: the retention window is the most recent N turns of the CURRENT chain
		// (leaf-walk), not raw file order. Blobs referenced only by abandoned
		// branches then fall outside the window and can be reclaimed earlier
		// (content shared with the current chain stays in-window via dedup).
		if IsV2Entries(entries) {
			entries = CurrentChain(entries)
		}
		start := turnWindowStart(entries, opts.MaxTurns)
		for i := start; i < len(entries); i++ {
			entry := entries[i]
			if entry.Type != "file_change" || strings.TrimSpace(entry.Content) == "" {
				continue
			}
			if !ageCutoff.IsZero() && !entry.Timestamp.IsZero() && entry.Timestamp.Before(ageCutoff) {
				continue
			}
			var change fileChangeSnapshot
			if err := json.Unmarshal([]byte(entry.Content), &change); err != nil {
				continue
			}
			for _, p := range []string{change.BeforeSnapshotPath, change.AfterSnapshotPath} {
				if p = strings.TrimSpace(p); p != "" {
					if t, ok := inWindow[p]; !ok || entry.Timestamp.After(t) {
						inWindow[p] = entry.Timestamp
					}
				}
			}
		}
	}
	return inWindow, nil
}

// turnWindowStart returns the entry index at which the most recent maxTurns turns
// begin. Turns are delimited by checkpoint entries. It returns 0 (all entries)
// when maxTurns <= 0 or there are no more turns than the limit.
func turnWindowStart(entries []Entry, maxTurns int) int {
	if maxTurns <= 0 {
		return 0
	}
	checkpoints := make([]int, 0)
	for i := range entries {
		if entries[i].Type == "checkpoint" {
			checkpoints = append(checkpoints, i)
		}
	}
	if len(checkpoints) <= maxTurns {
		return 0
	}
	return checkpoints[len(checkpoints)-maxTurns]
}

func (s Store) Rename(sessionID, name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("session name is required")
	}
	summary, ok, err := s.PrepareForWrite(sessionID)
	if err != nil || !ok {
		return ok, err
	}
	file, err := os.OpenFile(summary.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return true, err
	}
	defer file.Close()
	return true, json.NewEncoder(file).Encode(Entry{Timestamp: time.Now().UTC(), Type: "session", Name: name})
}

func (s Store) Checkpoint(sessionID, name string) (Entry, bool, error) {
	summary, ok, err := s.PrepareForWrite(sessionID)
	if err != nil || !ok {
		return Entry{}, ok, err
	}
	entry := Entry{Timestamp: time.Now().UTC(), Type: "checkpoint", Name: strings.TrimSpace(name)}
	// v2: a checkpoint is a chain node (a rewind anchor), so tag it and chain it
	// onto the active leaf; otherwise a later checkpoint rewind onto it would land
	// off the current chain. (By-id checkpointing is not a concurrent live-recorder
	// operation, so reading the active leaf here is safe.)
	if fileEntries, loadErr := Load(summary.Path); loadErr == nil && IsV2Entries(fileEntries) {
		entry.Schema = SchemaV2
		entry.ID = NewEntryID()
		entry.ParentID = ActiveLeaf(fileEntries)
	}
	file, err := os.OpenFile(summary.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return Entry{}, true, err
	}
	defer file.Close()
	return entry, true, json.NewEncoder(file).Encode(entry)
}

func (r *Recorder) Checkpoint(name, content string) (Entry, error) {
	entry := Entry{ID: NewEntryID(), Timestamp: time.Now().UTC(), Type: "checkpoint", Name: strings.TrimSpace(name), Content: strings.TrimSpace(content)}
	if err := r.Append(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s Store) Rewind(sessionID, checkpoint string) (RewindResult, bool, error) {
	summary, ok, err := s.PrepareForWrite(sessionID)
	if err != nil || !ok {
		return RewindResult{}, ok, err
	}
	entries, err := Load(summary.Path)
	if err != nil {
		return RewindResult{}, true, err
	}
	index := findCheckpoint(entries, checkpoint)
	if index < 0 {
		if strings.TrimSpace(checkpoint) == "" {
			return RewindResult{}, true, fmt.Errorf("session has no checkpoint")
		}
		return RewindResult{}, true, fmt.Errorf("checkpoint not found: %s", checkpoint)
	}
	// v2 checkpoint rewind is non-destructive too: move the active leaf to the
	// checkpoint node via an appended branch_head, restore files by chain diff, and
	// keep the abandoned branch (redoable). v1 keeps the truncating behavior below.
	if IsV2Entries(entries) {
		result, ok, err := s.rewindConversationV2(summary.Path, sessionID, entries[index].Name, entries, index, true)
		if err == nil {
			result.Checkpoint = entries[index].Name
			result.MessageID = ""
		}
		return result, ok, err
	}
	removed := entries[index+1:]
	changes, err := parseFileChanges(removed)
	if err != nil {
		return RewindResult{}, true, err
	}
	rewindEntry := Entry{
		Timestamp: time.Now().UTC(),
		Type:      "rewind",
		Name:      entries[index].Name,
		Content:   "rewound to checkpoint",
	}
	kept := append(append([]Entry(nil), entries[:index+1]...), rewindEntry)
	filesRestored, degraded, err := runFileRestoreTransaction(changes, func() error {
		return writeEntries(summary.Path, kept)
	})
	if err != nil {
		return RewindResult{}, true, err
	}
	return RewindResult{
		SessionID:        sessionID,
		Checkpoint:       entries[index].Name,
		EntriesKept:      len(kept),
		EntriesRemoved:   len(removed),
		FilesRestored:    filesRestored,
		MetadataDegraded: degraded,
	}, true, nil
}

func (s Store) RewindFiles(sessionID, messageID string) (RewindResult, bool, error) {
	return s.rewindFiles(sessionID, messageID, true, true)
}

func (s Store) RewindToMessage(sessionID, messageID string) (RewindResult, bool, error) {
	return s.rewindFiles(sessionID, messageID, false, true)
}

func (s Store) RewindConversationToMessage(sessionID, messageID string) (RewindResult, bool, error) {
	return s.rewindFiles(sessionID, messageID, false, false)
}

func (s Store) rewindFiles(sessionID, messageID string, keepTranscript, restoreFiles bool) (RewindResult, bool, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return RewindResult{}, true, fmt.Errorf("message id is required")
	}
	summary, ok, err := s.PrepareForWrite(sessionID)
	if err != nil || !ok {
		return RewindResult{}, ok, err
	}
	entries, err := Load(summary.Path)
	if err != nil {
		return RewindResult{}, true, err
	}
	index := findCheckpointForMessage(entries, messageID)
	keepThroughIndex := index
	removeFromIndex := index + 1
	if index < 0 {
		index = findEntry(entries, messageID)
		keepThroughIndex = index - 1
		removeFromIndex = index
	}
	if index < 0 {
		return RewindResult{}, true, fmt.Errorf("message not found: %s", messageID)
	}
	// v2 conversation rewind is non-destructive: it moves the active leaf via an
	// appended branch_head instead of truncating the transcript, so the abandoned
	// branch survives and is redoable. The files-only path (keepTranscript) is
	// already non-destructive and shared with v1.
	if !keepTranscript && IsV2Entries(entries) {
		return s.rewindConversationV2(summary.Path, sessionID, messageID, entries, keepThroughIndex, restoreFiles)
	}
	removed := entries[removeFromIndex:]
	var changes []fileChangeSnapshot
	if restoreFiles {
		changes, err = parseFileChanges(removed)
		if err != nil {
			return RewindResult{}, true, err
		}
	}
	if keepTranscript {
		commit := func() error {
			file, err := os.OpenFile(summary.Path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return err
			}
			defer file.Close()
			enc := json.NewEncoder(file)
			if err := enc.Encode(Entry{Timestamp: time.Now().UTC(), Type: "rewind", Name: "files", Content: "restored files to message " + messageID}); err != nil {
				return err
			}
			if latestRecapIndex(entries) >= 0 {
				if err := enc.Encode(recapInvalidatedEntry(messageID)); err != nil {
					return err
				}
			}
			return nil
		}
		filesRestored, degraded, err := runFileRestoreTransaction(changes, commit)
		if err != nil {
			return RewindResult{}, true, err
		}
		return RewindResult{SessionID: sessionID, MessageID: messageID, EntriesKept: len(entries), EntriesRemoved: 0, FilesRestored: filesRestored, MetadataDegraded: degraded, TranscriptKept: true}, true, nil
	}
	rewindEntry := Entry{Timestamp: time.Now().UTC(), Type: "rewind", Name: messageID, Content: "rewound to message"}
	kept := append([]Entry(nil), entries[:keepThroughIndex+1]...)
	kept = append(kept, rewindEntry)
	filesRestored, degraded, err := runFileRestoreTransaction(changes, func() error {
		return writeEntries(summary.Path, kept)
	})
	if err != nil {
		return RewindResult{}, true, err
	}
	return RewindResult{SessionID: sessionID, MessageID: messageID, EntriesKept: len(kept), EntriesRemoved: len(removed), FilesRestored: filesRestored, MetadataDegraded: degraded}, true, nil
}

// rewindConversationV2 performs a non-destructive conversation rewind on a v2
// message-graph transcript: it appends a branch_head redirecting the active leaf
// to the node kept through the target (the auto-checkpoint before the message, or
// the message's predecessor). Nothing is deleted, so the abandoned branch remains
// in the file and can be redone. EntriesRemoved reports how many current-chain
// nodes were set aside, not bytes removed (bytes only grow).
func (s Store) rewindConversationV2(path, sessionID, messageID string, entries []Entry, keepThroughIndex int, restoreFiles bool) (RewindResult, bool, error) {
	if keepThroughIndex < 0 {
		return RewindResult{}, true, fmt.Errorf("cannot rewind before the first message")
	}
	targetLeaf := entries[keepThroughIndex].ID
	if targetLeaf == "" {
		return RewindResult{}, true, fmt.Errorf("rewind target is not a v2 chain node")
	}
	oldChain := ChainToLeaf(entries, ActiveLeaf(entries))
	newChain := ChainToLeaf(entries, targetLeaf)
	keep := make(map[string]bool, len(newChain))
	for _, entry := range newChain {
		keep[entry.ID] = true
	}
	var abandoned []Entry
	for _, entry := range oldChain {
		if !keep[entry.ID] {
			abandoned = append(abandoned, entry)
		}
	}
	var changes []fileChangeSnapshot
	if restoreFiles {
		var err error
		if changes, err = v2FileRestores(oldChain, newChain); err != nil {
			return RewindResult{}, true, err
		}
	}
	commit := func() error {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		enc := json.NewEncoder(file)
		branchHead := Entry{ID: NewEntryID(), Schema: SchemaV2, Timestamp: time.Now().UTC(), Type: EntryTypeBranchHead, LeafID: targetLeaf, Reason: BranchHeadReasonRewind}
		if err := enc.Encode(branchHead); err != nil {
			return err
		}
		if latestRecapIndex(entries) >= 0 {
			if err := enc.Encode(recapInvalidatedEntry(messageID)); err != nil {
				return err
			}
		}
		return nil
	}
	filesRestored, degraded, err := runFileRestoreTransaction(changes, commit)
	if err != nil {
		return RewindResult{}, true, err
	}
	return RewindResult{
		SessionID:        sessionID,
		MessageID:        messageID,
		EntriesKept:      len(newChain),
		EntriesRemoved:   len(abandoned),
		FilesRestored:    filesRestored,
		MetadataDegraded: degraded,
		TranscriptKept:   true,
	}, true, nil
}

// Redo moves the active leaf back onto a previously abandoned branch by appending
// a branch_head, WITHOUT restoring files. Because a non-destructive rewind never
// deletes the old branch, this is simply the inverse pointer move. leafID must be
// a real chain node id (e.g. from Branches). Use RedoToBranch to also replay the
// workspace to the branch's end state (the default for `session redo`).
func (s Store) Redo(sessionID, leafID string) (RewindResult, bool, error) {
	return s.redoBranch(sessionID, leafID, false)
}

// RedoToBranch moves the active leaf back onto a previously abandoned branch AND
// restores the workspace to that branch's end state, the mirror of a
// file-restoring rewind. The file restore and the branch_head append are one
// all-or-nothing unit (runFileRestoreTransaction): if any target file's end state
// is unrecoverable (a tombstone — see afterAsBefore) the whole redo aborts and
// neither the workspace nor the pointer moves, so redo the conversation only with
// Redo when files cannot follow.
func (s Store) RedoToBranch(sessionID, leafID string) (RewindResult, bool, error) {
	return s.redoBranch(sessionID, leafID, true)
}

func (s Store) redoBranch(sessionID, leafID string, restoreFiles bool) (RewindResult, bool, error) {
	leafID = strings.TrimSpace(leafID)
	if leafID == "" {
		return RewindResult{}, true, fmt.Errorf("branch leaf id is required")
	}
	summary, ok, err := s.PrepareForWrite(sessionID)
	if err != nil || !ok {
		return RewindResult{}, ok, err
	}
	entries, err := Load(summary.Path)
	if err != nil {
		return RewindResult{}, true, err
	}
	if !IsV2Entries(entries) {
		return RewindResult{}, true, fmt.Errorf("redo requires a v2 message-graph transcript")
	}
	toChain := ChainToLeaf(entries, leafID)
	if len(toChain) == 0 {
		return RewindResult{}, true, fmt.Errorf("branch leaf not found: %s", leafID)
	}
	fromChain := ChainToLeaf(entries, ActiveLeaf(entries))
	var changes []fileChangeSnapshot
	if restoreFiles {
		if changes, err = v2FileRestores(fromChain, toChain); err != nil {
			return RewindResult{}, true, err
		}
	}
	commit := func() error {
		file, err := os.OpenFile(summary.Path, os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		enc := json.NewEncoder(file)
		branchHead := Entry{ID: NewEntryID(), Schema: SchemaV2, Timestamp: time.Now().UTC(), Type: EntryTypeBranchHead, LeafID: leafID, Reason: BranchHeadReasonRedo}
		if err := enc.Encode(branchHead); err != nil {
			return err
		}
		if restoreFiles && latestRecapIndex(entries) >= 0 {
			if err := enc.Encode(recapInvalidatedEntry(leafID)); err != nil {
				return err
			}
		}
		return nil
	}
	filesRestored, degraded, err := runFileRestoreTransaction(changes, commit)
	if err != nil {
		return RewindResult{}, true, err
	}
	return RewindResult{SessionID: sessionID, MessageID: leafID, EntriesKept: len(toChain), FilesRestored: filesRestored, MetadataDegraded: degraded, TranscriptKept: true}, true, nil
}

// BranchInfo summarizes one branch (one leaf) of a v2 message-graph transcript.
type BranchInfo struct {
	LeafID    string    `json:"leaf_id"`
	Active    bool      `json:"active"`
	Messages  int       `json:"messages"`
	Length    int       `json:"length"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	// ForkPoint is the id of the last node this branch shares with the active
	// branch (its divergence point). Empty for the active branch (the trunk) and
	// for a branch that shares nothing with it.
	ForkPoint string `json:"fork_point,omitempty"`
}

// Branches lists every branch tip (leaf) of a v2 message-graph transcript with a
// short summary of its chain and its fork point (so the branch tree is legible),
// marking the active one. It errors on a v1 linear transcript, which has no
// branches.
func (s Store) Branches(sessionID string) ([]BranchInfo, bool, error) {
	summary, ok, err := s.Find(sessionID)
	if err != nil || !ok {
		return nil, ok, err
	}
	entries, err := Load(summary.Path)
	if err != nil {
		return nil, true, err
	}
	if !IsV2Entries(entries) {
		return nil, true, fmt.Errorf("session %s is a v1 linear transcript (no branches)", sessionID)
	}
	active := ActiveLeaf(entries)
	activeChain := ChainToLeaf(entries, active)
	leaves := Leaves(entries)
	out := make([]BranchInfo, 0, len(leaves))
	for _, leaf := range leaves {
		chain := ChainToLeaf(entries, leaf)
		info := BranchInfo{LeafID: leaf, Active: leaf == active, Length: len(chain)}
		for _, entry := range chain {
			if entry.Type == "message" && (entry.Role == "user" || entry.Role == "assistant") {
				info.Messages++
				info.Preview = previewText(entry.Content)
				info.Timestamp = entry.Timestamp
			}
		}
		if !info.Active {
			if common := commonPrefixLen(activeChain, chain); common > 0 {
				info.ForkPoint = activeChain[common-1].ID
			}
		}
		out = append(out, info)
	}
	return out, true, nil
}

// BranchNode is one node on a branch, projected for comparison output.
type BranchNode struct {
	ID      string `json:"id"`
	Role    string `json:"role,omitempty"`
	Type    string `json:"type"`
	Preview string `json:"preview,omitempty"`
}

// BranchComparison is the divergence between two branches (leaves): their shared
// history length and the nodes unique to each side past the fork point.
type BranchComparison struct {
	LeafA        string       `json:"leaf_a"`
	LeafB        string       `json:"leaf_b"`
	CommonLength int          `json:"common_length"`
	ForkPoint    string       `json:"fork_point,omitempty"`
	OnlyA        []BranchNode `json:"only_a"`
	OnlyB        []BranchNode `json:"only_b"`
}

// CompareBranches diffs two branches of a v2 message-graph transcript: it returns
// their shared prefix length, fork point, and the message/tool nodes unique to
// each side. Either leaf that is not a real chain node is an error.
func (s Store) CompareBranches(sessionID, leafA, leafB string) (BranchComparison, bool, error) {
	summary, ok, err := s.Find(sessionID)
	if err != nil || !ok {
		return BranchComparison{}, ok, err
	}
	entries, err := Load(summary.Path)
	if err != nil {
		return BranchComparison{}, true, err
	}
	if !IsV2Entries(entries) {
		return BranchComparison{}, true, fmt.Errorf("session %s is a v1 linear transcript (no branches)", sessionID)
	}
	chainA := ChainToLeaf(entries, strings.TrimSpace(leafA))
	if len(chainA) == 0 {
		return BranchComparison{}, true, fmt.Errorf("branch leaf not found: %s", leafA)
	}
	chainB := ChainToLeaf(entries, strings.TrimSpace(leafB))
	if len(chainB) == 0 {
		return BranchComparison{}, true, fmt.Errorf("branch leaf not found: %s", leafB)
	}
	common := commonPrefixLen(chainA, chainB)
	cmp := BranchComparison{LeafA: leafA, LeafB: leafB, CommonLength: common}
	if common > 0 {
		cmp.ForkPoint = chainA[common-1].ID
	}
	cmp.OnlyA = branchNodes(chainA[common:])
	cmp.OnlyB = branchNodes(chainB[common:])
	return cmp, true, nil
}

// branchNodes projects the conversation-visible nodes (messages) of a chain
// segment for comparison output, skipping checkpoints and other metadata.
func branchNodes(chain []Entry) []BranchNode {
	out := make([]BranchNode, 0)
	for _, entry := range chain {
		if entry.Type != "message" || (entry.Role != "user" && entry.Role != "assistant") {
			continue
		}
		out = append(out, BranchNode{ID: entry.ID, Role: entry.Role, Type: entry.Type, Preview: previewText(entry.Content)})
	}
	return out
}

// previewText collapses whitespace and truncates to a short one-line preview.
func previewText(content string) string {
	preview := strings.Join(strings.Fields(content), " ")
	runes := []rune(preview)
	if len(runes) > 60 {
		return string(runes[:57]) + "..."
	}
	return preview
}

func (s Store) Fork(sessionID, checkpoint, name string) (ForkResult, bool, error) {
	summary, ok, err := s.PrepareForWrite(sessionID)
	if err != nil || !ok {
		return ForkResult{}, ok, err
	}
	entries, err := Load(summary.Path)
	if err != nil {
		return ForkResult{}, true, err
	}
	checkpoint = strings.TrimSpace(checkpoint)
	copied := append([]Entry(nil), entries...)
	checkpointName := ""
	if checkpoint != "" {
		index := findCheckpoint(entries, checkpoint)
		if index < 0 {
			return ForkResult{}, true, fmt.Errorf("checkpoint not found: %s", checkpoint)
		}
		copied = append([]Entry(nil), entries[:index+1]...)
		checkpointName = entries[index].Name
	}
	id, err := NewID()
	if err != nil {
		return ForkResult{}, true, err
	}
	path := filepath.Join(filepath.Dir(summary.Path), id+".jsonl")
	forkEntry := Entry{
		Timestamp: time.Now().UTC(),
		Type:      "fork",
		Name:      sessionID,
		Content:   checkpointName,
	}
	copied = append(copied, forkEntry)
	if strings.TrimSpace(name) != "" {
		copied = append(copied, Entry{Timestamp: time.Now().UTC(), Type: "session", Name: strings.TrimSpace(name)})
	}
	if err := writeEntries(path, copied); err != nil {
		return ForkResult{}, true, err
	}
	return ForkResult{
		SourceSessionID: sessionID,
		SessionID:       id,
		Path:            path,
		Checkpoint:      checkpointName,
		EntriesCopied:   len(copied),
	}, true, nil
}

func findCheckpoint(entries []Entry, name string) int {
	name = strings.TrimSpace(name)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type != "checkpoint" {
			continue
		}
		if name == "" || entries[i].Name == name {
			return i
		}
	}
	return -1
}

func findCheckpointForMessage(entries []Entry, messageID string) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type != "checkpoint" {
			continue
		}
		if entries[i].Content == messageID || entries[i].Name == "auto-"+messageID {
			return i
		}
	}
	return -1
}

func findEntry(entries []Entry, id string) int {
	for i := range entries {
		if entries[i].ID == id {
			return i
		}
	}
	return -1
}

func latestRecapIndex(entries []Entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "recap_summary" && strings.TrimSpace(entries[i].Content) != "" {
			return i
		}
	}
	return -1
}

func recapInvalidatedEntry(messageID string) Entry {
	metadata := map[string]any{
		"version":    1,
		"source":     "rewind",
		"status":     "invalidated",
		"message_id": strings.TrimSpace(messageID),
		"created_at": time.Now().UTC(),
	}
	raw, _ := json.Marshal(metadata)
	return Entry{
		ID:        NewEntryID(),
		Timestamp: time.Now().UTC(),
		Type:      "recap_summary",
		Role:      "system",
		Content:   "Recap invalidated by files-only rewind. Run /recap to refresh.",
		Metadata:  raw,
	}
}

func ThroughEntry(entries []Entry, id string) ([]Entry, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return entries, true
	}
	index := findEntry(entries, id)
	if index < 0 {
		return nil, false
	}
	out := append([]Entry(nil), entries[:index+1]...)
	return out, true
}

// parseFileChanges extracts the ordered, earliest-per-path file changes from a
// rewound range. The earliest before-state of each path is the correct restore
// target; later changes in the range only move the file further from the goal.
func parseFileChanges(entries []Entry) ([]fileChangeSnapshot, error) {
	earliest := make(map[string]fileChangeSnapshot)
	order := make([]string, 0)
	for _, entry := range entries {
		if entry.Type != "file_change" || strings.TrimSpace(entry.Content) == "" {
			continue
		}
		var change fileChangeSnapshot
		if err := json.Unmarshal([]byte(entry.Content), &change); err != nil {
			return nil, err
		}
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if _, ok := earliest[path]; ok {
			continue
		}
		earliest[path] = change
		order = append(order, path)
	}
	changes := make([]fileChangeSnapshot, 0, len(order))
	for _, path := range order {
		changes = append(changes, earliest[path])
	}
	return changes, nil
}

// v2FileRestores computes the file-restore set for moving the workspace from the
// end of fromChain to the end of toChain (both root→leaf chains). It generalizes
// the rewind restore — where toChain is a prefix of fromChain, so only rule ①
// below fires and the output equals parseFileChanges(abandoned) — to an arbitrary
// branch switch (redo onto a sibling), and is shared by rewind and redo.
//
// Let the shared prefix be the fork point. After the fork two disjoint rules apply:
//
//	① Paths changed only on the branch being left (fromChain's suffix) are undone to
//	   their fork-point state: the earliest before recorded in that suffix
//	   (parseFileChanges semantics).
//	② Paths changed on the branch being entered (toChain's suffix) are replayed to
//	   that branch's final state: the after of the last change to the path there,
//	   projected into a before-shaped restore by afterAsBefore. Rule ② wins over ①
//	   for a path touched on both suffixes.
//
// A path on toChain's suffix whose final after-content was never persisted (a
// superseded lite entry) makes afterAsBefore return a tombstone error, so the
// caller aborts atomically rather than writing an empty or wrong file.
func v2FileRestores(fromChain, toChain []Entry) ([]fileChangeSnapshot, error) {
	common := commonPrefixLen(fromChain, toChain)
	fromSuffix := fromChain[common:]
	toSuffix := toChain[common:]
	forward, forwardPaths, err := forwardRestores(toSuffix)
	if err != nil {
		return nil, err
	}
	undo, err := parseFileChanges(fromSuffix)
	if err != nil {
		return nil, err
	}
	changes := make([]fileChangeSnapshot, 0, len(undo)+len(forward))
	for _, change := range undo {
		if forwardPaths[strings.TrimSpace(change.Path)] {
			continue // rule ② already restores this path to the target's final state
		}
		changes = append(changes, change)
	}
	return append(changes, forward...), nil
}

// forwardRestores returns, per path changed on a branch suffix, a restore that
// reproduces that branch's FINAL state for the path (the after of its last change),
// projected into a before-shaped snapshot via afterAsBefore. It returns a tombstone
// error when a path's final after-content was never persisted.
func forwardRestores(suffix []Entry) ([]fileChangeSnapshot, map[string]bool, error) {
	last := make(map[string]fileChangeSnapshot)
	order := make([]string, 0)
	for _, entry := range suffix {
		if entry.Type != "file_change" || strings.TrimSpace(entry.Content) == "" {
			continue
		}
		var change fileChangeSnapshot
		if err := json.Unmarshal([]byte(entry.Content), &change); err != nil {
			return nil, nil, err
		}
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if _, ok := last[path]; !ok {
			order = append(order, path)
		}
		last[path] = change
	}
	paths := make(map[string]bool, len(order))
	out := make([]fileChangeSnapshot, 0, len(order))
	for _, path := range order {
		restore, err := afterAsBefore(last[path])
		if err != nil {
			return nil, nil, err
		}
		paths[path] = true
		out = append(out, restore)
	}
	return out, paths, nil
}

// afterAsBefore projects a file_change's after-state into a before-shaped snapshot
// so restoreFileChange (which restores only Before* fields) reproduces the
// after-state. Metadata is not projected — no after-metadata is recorded — which is
// an acceptable degradation. It returns a tombstone error for a regular file whose
// after-content was never persisted: no blob, no usable inline body, yet a non-empty
// content hash (or the large-file placeholder) proving real content once existed.
func afterAsBefore(change fileChangeSnapshot) (fileChangeSnapshot, error) {
	path := strings.TrimSpace(change.Path)
	restore := fileChangeSnapshot{
		Path:             path,
		BeforeExists:     change.AfterExists,
		BeforeIsSymlink:  change.AfterIsSymlink,
		BeforeLinkTarget: change.AfterLinkTarget,
		BeforeIsDir:      change.AfterIsDir,
		BeforeMode:       change.AfterMode,
		BeforeModeKnown:  change.AfterModeKnown,
	}
	if !change.AfterExists || change.AfterIsSymlink || change.AfterIsDir {
		return restore, nil // deletion / link / dir carry no file body
	}
	if snapshot := strings.TrimSpace(change.AfterSnapshotPath); snapshot != "" {
		restore.BeforeSnapshotPath = snapshot // blob reference; readability re-checked by precheckRestore
		return restore, nil
	}
	if change.After != "" && change.After != files.CaptureSnapshotPlaceholder {
		restore.Before = change.After // inline body
		return restore, nil
	}
	if strings.TrimSpace(change.AfterSHA256) != "" || change.After == files.CaptureSnapshotPlaceholder {
		return fileChangeSnapshot{}, fmt.Errorf("redo cannot restore %s: its final content on the target branch was not retained (a later same-turn edit superseded the stored snapshot, or its file history was reclaimed). Redo the conversation only with --conversation-only", path)
	}
	restore.Before = "" // genuine empty file
	return restore, nil
}

// applyRestore is the single-file restore step, indirected through a package
// variable so tests can inject a failure at a specific file to exercise the
// rollback path. Rollback always uses restoreFileChange directly.
var applyRestore = restoreFileChange

// orderRestores sorts changes so directories and parents are created before
// their children (ascending path) and children are removed before their parents
// (descending path). This keeps directory create/delete correct without a
// dependency graph.
func orderRestores(changes []fileChangeSnapshot) []fileChangeSnapshot {
	restores := make([]fileChangeSnapshot, 0, len(changes))
	removals := make([]fileChangeSnapshot, 0, len(changes))
	for _, change := range changes {
		if change.BeforeExists {
			restores = append(restores, change)
		} else {
			removals = append(removals, change)
		}
	}
	sort.SliceStable(restores, func(i, j int) bool { return restores[i].Path < restores[j].Path })
	sort.SliceStable(removals, func(i, j int) bool { return removals[i].Path > removals[j].Path })
	return append(restores, removals...)
}

// runFileRestoreTransaction restores every change and then runs commit (the
// transcript rewrite/append) as one all-or-nothing unit. It first prechecks
// feasibility, snapshots the current state of every target, then applies the
// restores. If any file restore or the commit fails, all applied files are
// rolled back to their pre-rewind state, so the workspace and transcript are
// never left partially rewound. Metadata that could not be restored is returned
// as a degradation list rather than failing the transaction.
func runFileRestoreTransaction(changes []fileChangeSnapshot, commit func() error) (int, []string, error) {
	changes = orderRestores(changes)
	// Phase 1: feasibility precheck — mutate nothing if a restore is already
	// known to be impossible (missing snapshot, unwritable directory).
	for _, change := range changes {
		if err := precheckRestore(change); err != nil {
			return 0, nil, err
		}
	}
	// Phase 2: capture the current state of every target for rollback.
	undo := make([]fileChangeSnapshot, len(changes))
	var temps []string
	cleanup := func() {
		for _, tmp := range temps {
			_ = os.Remove(tmp)
		}
	}
	for i, change := range changes {
		record, tmp, err := captureCurrentState(change.Path)
		if err != nil {
			cleanup()
			return 0, nil, fmt.Errorf("rewind could not snapshot current state of %s: %w", change.Path, err)
		}
		if tmp != "" {
			temps = append(temps, tmp)
		}
		undo[i] = record
	}
	// Phase 3: apply restores; roll back everything already applied on failure.
	applied := 0
	rollback := func() {
		for i := applied - 1; i >= 0; i-- {
			_, _ = restoreFileChange(undo[i])
		}
	}
	var degraded []string
	for _, change := range changes {
		d, err := applyRestore(change)
		if err != nil {
			rollback()
			cleanup()
			return 0, nil, fmt.Errorf("rewind failed restoring %s: %w", change.Path, err)
		}
		degraded = append(degraded, d...)
		applied++
	}
	// Phase 4: commit the transcript. A failure rolls the files back so both
	// sides stay consistent.
	if commit != nil {
		if err := commit(); err != nil {
			rollback()
			cleanup()
			return 0, nil, fmt.Errorf("rewind transcript commit failed: %w", err)
		}
	}
	cleanup()
	return len(changes), degraded, nil
}

const journalInlineLimit = 1 << 20 // 1 MiB inline before spilling to a temp file

// precheckRestore validates that a change can be restored without mutating the
// workspace: any referenced snapshot must be readable and the target directory
// (when it already exists) must be writable.
func precheckRestore(change fileChangeSnapshot) error {
	path := strings.TrimSpace(change.Path)
	if path == "" {
		return fmt.Errorf("file change has empty path")
	}
	if change.BeforeExists && !change.BeforeIsSymlink {
		if snapshot := strings.TrimSpace(change.BeforeSnapshotPath); snapshot != "" {
			file, err := os.Open(snapshot)
			if err != nil {
				return fmt.Errorf("rewind snapshot unavailable for %s (its file history may have been reclaimed by a retention/GC policy outside the rewind window; increase files.history.maxTurns/maxBytes to retain longer): %w", path, err)
			}
			_ = file.Close()
		}
	}
	return probeDirWritable(path)
}

func probeDirWritable(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		// The parent will be created during restore; nothing to probe yet.
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("rewind target parent is not a directory: %s", dir)
	}
	probe, err := os.CreateTemp(dir, ".rewind-precheck-*")
	if err != nil {
		return fmt.Errorf("rewind target directory not writable (%s): %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// captureCurrentState records a target's current state as a restore record that
// returns it to exactly that state, plus an optional temp-file path (for large
// files) the caller must clean up after the transaction settles.
func captureCurrentState(path string) (fileChangeSnapshot, string, error) {
	path = strings.TrimSpace(path)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return fileChangeSnapshot{Path: path, BeforeExists: false}, "", nil
	}
	if err != nil {
		return fileChangeSnapshot{}, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return fileChangeSnapshot{}, "", err
		}
		meta := files.CaptureMetadata(path, info, true)
		return fileChangeSnapshot{Path: path, BeforeExists: true, BeforeIsSymlink: true, BeforeLinkTarget: target, BeforeMetadata: metaPtr(meta)}, "", nil
	}
	if info.IsDir() {
		meta := files.CaptureMetadata(path, info, false)
		return fileChangeSnapshot{Path: path, BeforeExists: true, BeforeIsDir: true, BeforeMode: info.Mode().Perm(), BeforeModeKnown: true, BeforeMetadata: metaPtr(meta)}, "", nil
	}
	if !info.Mode().IsRegular() {
		return fileChangeSnapshot{}, "", fmt.Errorf("cannot snapshot non-regular file %s", path)
	}
	meta := files.CaptureMetadata(path, info, false)
	record := fileChangeSnapshot{Path: path, BeforeExists: true, BeforeMode: info.Mode().Perm(), BeforeModeKnown: true, BeforeMetadata: metaPtr(meta)}
	if info.Size() <= journalInlineLimit {
		data, err := os.ReadFile(path)
		if err != nil {
			return fileChangeSnapshot{}, "", err
		}
		record.Before = string(data)
		return record, "", nil
	}
	tmp, err := os.CreateTemp("", "rewind-undo-*")
	if err != nil {
		return fileChangeSnapshot{}, "", err
	}
	tmpPath := tmp.Name()
	in, err := os.Open(path)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fileChangeSnapshot{}, "", err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		_ = in.Close()
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fileChangeSnapshot{}, "", err
	}
	_ = in.Close()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fileChangeSnapshot{}, "", err
	}
	record.BeforeSnapshotPath = tmpPath
	return record, tmpPath, nil
}

func metaPtr(m files.Metadata) *files.Metadata {
	if m.Empty() {
		return nil
	}
	return &m
}

func applyChangeMetadata(change fileChangeSnapshot, path string, isSymlink bool) []string {
	if change.BeforeMetadata == nil {
		return nil
	}
	return files.ApplyMetadata(path, *change.BeforeMetadata, isSymlink)
}

func restoreFileChange(change fileChangeSnapshot) ([]string, error) {
	path := strings.TrimSpace(change.Path)
	// Absent before-state: drop whatever object currently occupies the path.
	if !change.BeforeExists {
		return nil, removePath(path)
	}
	// Directory before-state: recreate the directory and its permissions.
	if change.BeforeIsDir {
		mode := restoreTargetMode(change, path)
		if mode == 0 {
			mode = 0755
		}
		if err := os.MkdirAll(path, mode); err != nil {
			return nil, err
		}
		if err := os.Chmod(path, mode); err != nil {
			return nil, err
		}
		return applyChangeMetadata(change, path, false), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	// Symlink before-state: recreate the link itself rather than its target.
	if change.BeforeIsSymlink {
		if err := removePath(path); err != nil {
			return nil, err
		}
		if err := os.Symlink(change.BeforeLinkTarget, path); err != nil {
			return nil, err
		}
		return applyChangeMetadata(change, path, true), nil
	}
	// Regular before-state: if a non-regular object (symlink or directory) now
	// occupies the path, remove it first so we install a real file rather than
	// writing through the link or into the directory.
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		if err := removePath(path); err != nil {
			return nil, err
		}
	}
	mode := restoreTargetMode(change, path)
	var err error
	if snapshot := strings.TrimSpace(change.BeforeSnapshotPath); snapshot != "" {
		var in *os.File
		in, err = os.Open(snapshot)
		if err != nil {
			return nil, err
		}
		defer in.Close()
		err = atomicReplace(path, in, mode)
	} else {
		err = atomicReplace(path, strings.NewReader(change.Before), mode)
	}
	if err != nil {
		return nil, err
	}
	return applyChangeMetadata(change, path, false), nil
}

func removePath(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// restoreTargetMode resolves the permission bits to apply after restoring
// content. An explicitly recorded before-mode (including a valid 0000) wins;
// otherwise the current file's mode is preserved so legacy transcripts without
// mode data are not forced to a default.
func restoreTargetMode(change fileChangeSnapshot, path string) os.FileMode {
	if change.BeforeModeKnown || change.BeforeMode.Perm() != 0 {
		return change.BeforeMode.Perm()
	}
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0644
}

// atomicReplace writes src to a same-directory temp file, applies mode, fsyncs,
// closes, and renames it over path. Any failure removes the temp file and
// leaves the original target untouched, so an interrupted or failed restore
// never yields a half-written file. Both inline and snapshot restore paths use
// this single helper.
func atomicReplace(path string, src io.Reader, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".rewind-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func writeEntries(path string, entries []Entry) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	for _, entry := range entries {
		if err := enc.Encode(entry); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) Search(query string) ([]Summary, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("session search query is required")
	}
	summaries, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, summary := range summaries {
		if strings.Contains(strings.ToLower(summary.Title), query) {
			out = append(out, summary)
			continue
		}
		entries, err := Load(summary.Path)
		if err != nil {
			continue
		}
		if entriesContain(entries, query) {
			out = append(out, summary)
		}
	}
	return out, nil
}

func (s Store) Clear() error {
	root := s.projectsRoot()
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return os.MkdirAll(root, 0755)
}

func entriesContain(entries []Entry, query string) bool {
	for _, entry := range entries {
		fields := []string{entry.Type, entry.Role, entry.Content, entry.ToolName, entry.Name, entry.Model}
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field), query) {
				return true
			}
		}
	}
	return false
}

// Usage summarises every recorded session using only the built-in Anthropic
// pricing table. Prefer UsageWithRates so configured pricing applies.
func (s Store) Usage() (UsageSummary, error) {
	return s.UsageWithRates(nil)
}

// UsageWithRates summarises every recorded session, pricing each model with the
// caller's configured rates and falling back to the built-in Anthropic table.
// Models with no known price are listed in UnknownPricingModels rather than
// silently contributing $0 (AUDIT-P0-15).
func (s Store) UsageWithRates(rates map[string]Rate) (UsageSummary, error) {
	summaries, err := s.List()
	if err != nil {
		return UsageSummary{}, err
	}
	usage := UsageSummary{Sessions: len(summaries), Models: map[string]ModelUsage{}}
	for _, summary := range summaries {
		entries, err := Load(summary.Path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Type != "usage" {
				continue
			}
			if entry.Turn > 0 {
				usage.TurnUsages = append(usage.TurnUsages, NewTurnUsage(summary.SessionID, entry.Turn, entry.Provider, entry.Model, ReportedUsage{
					InputTokens: entry.InputTokens, CacheCreationInputTokens: entry.CacheCreationInputTokens, CacheReadInputTokens: entry.CacheReadInputTokens,
					CacheCreation5mTokens: entry.CacheCreationEphemeral5mInputTokens, CacheCreation1hTokens: entry.CacheCreationEphemeral1hInputTokens, OutputTokens: entry.OutputTokens,
				}, entry.Estimated, entry.UsageSource))
			}
			// InputTokens here is the whole prompt (uncached + cache tiers); the
			// tiers are kept separately so cost can price them apart.
			inputTokens := entry.InputTokens + entry.CacheCreationInputTokens + entry.CacheReadInputTokens
			usage.InputTokens += inputTokens
			usage.CacheCreationInputTokens += entry.CacheCreationInputTokens
			usage.CacheReadInputTokens += entry.CacheReadInputTokens
			usage.CacheCreationEphemeral1hInputTokens += entry.CacheCreationEphemeral1hInputTokens
			usage.CacheCreationEphemeral5mInputTokens += entry.CacheCreationEphemeral5mInputTokens
			usage.OutputTokens += entry.OutputTokens
			model := entry.Model
			if model == "" {
				model = "unknown"
			}
			modelUsage := usage.Models[model]
			modelUsage.InputTokens += inputTokens
			modelUsage.CacheCreationInputTokens += entry.CacheCreationInputTokens
			modelUsage.CacheReadInputTokens += entry.CacheReadInputTokens
			modelUsage.CacheCreationEphemeral1hInputTokens += entry.CacheCreationEphemeral1hInputTokens
			modelUsage.CacheCreationEphemeral5mInputTokens += entry.CacheCreationEphemeral5mInputTokens
			modelUsage.OutputTokens += entry.OutputTokens
			modelUsage.TotalTokens = modelUsage.InputTokens + modelUsage.OutputTokens
			modelUsage.CostUSD, modelUsage.CostKnown = EstimateCost(model, modelUsage.reportedUsage().Tiers(), rates)
			usage.Models[model] = modelUsage
		}
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	for model, modelUsage := range usage.Models {
		if !modelUsage.CostKnown {
			usage.UnknownPricingModels = append(usage.UnknownPricingModels, model)
			continue
		}
		usage.CostUSD += modelUsage.CostUSD
	}
	sort.Strings(usage.UnknownPricingModels)
	return usage, nil
}

func (m ModelUsage) reportedUsage() ReportedUsage {
	return ReportedUsage{
		InputTokens:              m.InputTokens,
		CacheCreationInputTokens: m.CacheCreationInputTokens,
		CacheReadInputTokens:     m.CacheReadInputTokens,
		CacheCreation5mTokens:    m.CacheCreationEphemeral5mInputTokens,
		CacheCreation1hTokens:    m.CacheCreationEphemeral1hInputTokens,
		OutputTokens:             m.OutputTokens,
	}
}

func ExportMarkdown(entries []Entry) string {
	var out bytes.Buffer
	out.WriteString("# " + product.Name + " Session\n\n")
	for _, entry := range entries {
		switch entry.Type {
		case "message":
			if entry.Role == "" {
				continue
			}
			out.WriteString("## " + strings.Title(entry.Role) + "\n\n")
			out.WriteString(entry.Content + "\n\n")
		case "tool_call":
			out.WriteString("## Tool Call: " + entry.ToolName + "\n\n")
			out.WriteString("```json\n" + entry.Content + "\n```\n\n")
		case "tool_result":
			out.WriteString("## Tool Result: " + entry.ToolName + "\n\n")
			out.WriteString("```\n" + entry.Content + "\n```\n\n")
		case "usage":
			inputTokens := entry.InputTokens + entry.CacheCreationInputTokens + entry.CacheReadInputTokens
			if entry.CacheCreationInputTokens != 0 || entry.CacheReadInputTokens != 0 {
				out.WriteString(fmt.Sprintf("_Usage: input=%d cache_creation=%d cache_read=%d output=%d_\n\n", inputTokens, entry.CacheCreationInputTokens, entry.CacheReadInputTokens, entry.OutputTokens))
				continue
			}
			out.WriteString(fmt.Sprintf("_Usage: input=%d output=%d_\n\n", inputTokens, entry.OutputTokens))
		case "recap_summary":
			if strings.TrimSpace(entry.Content) == "" {
				continue
			}
			out.WriteString("## Recap\n\n")
			out.WriteString("※ recap:\n\n")
			out.WriteString(entry.Content + "\n\n")
		}
	}
	return out.String()
}

func Compact(path string, maxBytes int) (Entry, error) {
	// Summarize the current conversation, not raw file order: a v2 graph's
	// abandoned branches must not leak into the compact summary.
	entries, err := LoadConversation(path)
	if err != nil {
		return Entry{}, err
	}
	return CompactEntries(path, entries, maxBytes)
}

// BuildCompactSummary renders a bounded conversation summary from the given
// logical transcript entries (maxBytes <= 0 uses the default budget). Exposed so
// live-recorder callers can build the summary and append it through the recorder
// (which tags/chains/advances the leaf) rather than via a side-channel write.
func BuildCompactSummary(entries []Entry, maxBytes int) string {
	summary, _ := buildCompactSummary(entries, maxBytes)
	return summary
}

func buildCompactSummary(entries []Entry, maxBytes int) (string, SummaryProvenance) {
	if maxBytes <= 0 {
		maxBytes = 12 * 1024
	}
	return buildSummary(entries, maxBytes)
}

// CompactEntries appends a summary built from the caller's logical transcript.
// This supports resumed continuations whose context is not identical to the
// physical recorder contents. For a v2 message graph it tags and chains the
// summary onto the file's active leaf so resume's leaf-walk includes the context
// reset. Live-recorder callers should append through the recorder instead so its
// in-memory leaf advances too; this path covers by-id compaction with no recorder.
func CompactEntries(path string, entries []Entry, maxBytes int) (Entry, error) {
	var entry Entry
	err := withTranscriptLock(path, func() error {
		summary, provenance := buildCompactSummary(entries, maxBytes)
		metadata, marshalErr := json.Marshal(provenance)
		if marshalErr != nil {
			return marshalErr
		}
		entry = Entry{
			Type:            "compact_summary",
			Content:         summary,
			CompactMetadata: metadata,
			Timestamp:       time.Now().UTC(),
		}
		if fileEntries, err := Load(path); err == nil && IsV2Entries(fileEntries) {
			entry.Schema = SchemaV2
			entry.ID = NewEntryID()
			entry.ParentID = ActiveLeaf(fileEntries)
		}
		return appendEntryToPathLocked(path, entry)
	})
	return entry, err
}

const summaryOmissionNotice = "[older turns omitted to fit the summary budget]\n"

// buildSummary renders a bounded plain-text summary of a transcript. When the
// transcript does not fit the byte budget it keeps the MOST RECENT turns and
// drops the oldest: compaction exists to make room for the conversation to
// continue, so the tail is the part still needed. (This replaces a forward scan
// that kept the oldest maxBytes and discarded everything the user had just said
// — the exact opposite of what "compact" means to a user.)
func buildSummary(entries []Entry, maxBytes int) (string, SummaryProvenance) {
	// Everything up to and including the last previous summary is already
	// condensed. That summary becomes the header and the entries it covers are
	// dropped rather than rendered again.
	header := "Conversation summary:\n"
	sourceStart := 0
	renderStart := 0
	for i, entry := range entries {
		if entry.Type == "compact_summary" {
			header = entry.Content + "\n\nRecent conversation after previous summary:\n"
			sourceStart = i
			renderStart = i + 1
		}
	}
	// Reserve the omission notice up front so the result stays within maxBytes
	// whether or not anything ends up being dropped.
	budget := maxBytes - len(header) - len(summaryOmissionNotice)
	var kept []string
	var referenced []Entry
	dropped := false
	for i := len(entries) - 1; i >= renderStart; i-- {
		line := summaryLine(entries[i])
		if line == "" {
			continue
		}
		if len(line) > budget {
			// The newest entry alone can exceed the whole budget (a large tool
			// result). Keep its head rather than returning a summary with no
			// recent context at all. ToValidUTF8 drops a rune cut in half.
			if len(kept) == 0 && budget > 0 {
				kept = append(kept, strings.ToValidUTF8(line[:budget], ""))
				referenced = append(referenced, entries[i])
			}
			dropped = true
			break
		}
		budget -= len(line)
		kept = append(kept, line)
		referenced = append(referenced, entries[i])
	}
	if sourceStart < len(entries) && entries[sourceStart].Type == "compact_summary" {
		referenced = append(referenced, entries[sourceStart])
	}
	var out bytes.Buffer
	out.WriteString(header)
	if dropped {
		out.WriteString(summaryOmissionNotice)
	}
	// kept was collected newest-first; emit it in conversation order.
	for i := len(kept) - 1; i >= 0; i-- {
		out.WriteString(kept[i])
	}
	return strings.TrimSpace(out.String()), NewSummaryProvenance(entries[sourceStart:], referenced)
}

// summaryLine renders one transcript entry as a single summary line, or "" for
// entry types that carry no conversation content (usage, recaps, metadata).
func summaryLine(entry Entry) string {
	switch entry.Type {
	case "message":
		if entry.Role == "" || entry.Content == "" {
			return ""
		}
		return strings.ToUpper(entry.Role) + ": " + strings.TrimSpace(entry.Content) + "\n"
	case "tool_call":
		return "TOOL CALL " + entry.ToolName + ": " + strings.TrimSpace(entry.Content) + "\n"
	case "tool_result":
		return "TOOL RESULT " + entry.ToolName + ": " + strings.TrimSpace(entry.Content) + "\n"
	}
	return ""
}

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func NewEntryID() string {
	id, err := NewID()
	if err != nil {
		return fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	return id
}

func IsValidID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == strings.ToLower(strings.TrimSpace(id))
}

func ProjectSlug(cwd string) string {
	if cwd == "" {
		cwd = "unknown"
	}
	abs, err := filepath.Abs(cwd)
	if err == nil {
		cwd = abs
	}
	cwd = filepath.ToSlash(filepath.Clean(cwd))
	cwd = strings.TrimPrefix(cwd, "/")
	if cwd == "" || cwd == "." {
		return "root"
	}
	replacer := strings.NewReplacer("/", "-", ":", "", " ", "-")
	return replacer.Replace(cwd)
}
