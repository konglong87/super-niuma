package files

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Workspace file-change capture. Tools such as Bash can modify files through
// arbitrary shell commands, so a targeted before/after diff of the writable
// roots is the only reliable way to record recoverable file_change events.
//
// The capture is deliberately bounded: it only walks the explicitly writable
// roots (never the whole filesystem), skips well-known heavy directories, and
// stops capturing content once a byte budget is exhausted. When the budget is
// exceeded for a file, that file is reported as "skipped" instead of producing
// a change with empty before-content, so rewind never silently truncates it.

const (
	defaultCaptureMaxFiles    = 20000
	defaultCaptureInlineBytes = 256 * 1024
	defaultCaptureTotalBytes  = 128 * 1024 * 1024
	defaultCaptureAfterInline = 256 * 1024
	// CaptureSnapshotPlaceholder marks an after-state whose content was too large
	// to capture in the after-scan; it is a marker, not a recoverable file body,
	// so downstream externalization must leave it inline rather than store it as
	// a blob.
	CaptureSnapshotPlaceholder = "[large file snapshot stored separately]"
	// captureExternalizeThreshold bounds how much before-content is embedded in
	// a transcript entry. Larger before-content is moved to the content-addressed
	// snapshot store (deduped, GC-managed), so repeated edits do not grow the
	// transcript linearly with the file size.
	captureExternalizeThreshold = 8 * 1024
)

// defaultCaptureIgnoreDirs lists directory base names skipped during workspace
// capture to keep scans bounded. .git is always excluded because rewinding git
// internals is unsafe; the rest are large machine-generated trees.
var defaultCaptureIgnoreDirs = map[string]bool{
	".git":          true,
	".hg":           true,
	".svn":          true,
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	".mypy_cache":   true,
	".pytest_cache": true,
}

// CaptureConfig bounds a workspace capture.
type CaptureConfig struct {
	Roots          []string
	MaxFiles       int
	MaxInlineBytes int64
	MaxTotalBytes  int64
	IgnoreDirs     map[string]bool
	SnapshotDir    string
}

func (c CaptureConfig) normalized() CaptureConfig {
	if c.MaxFiles <= 0 {
		c.MaxFiles = defaultCaptureMaxFiles
	}
	if c.MaxInlineBytes <= 0 {
		c.MaxInlineBytes = defaultCaptureInlineBytes
	}
	if c.MaxTotalBytes <= 0 {
		c.MaxTotalBytes = defaultCaptureTotalBytes
	}
	if c.IgnoreDirs == nil {
		c.IgnoreDirs = defaultCaptureIgnoreDirs
	}
	c.Roots = dedupeRoots(c.Roots)
	return c
}

// Change mirrors the recoverable fields of a file_change event. The Bash tool
// maps this to tools.FileChange so the files package stays free of a tools
// dependency.
type Change struct {
	Path               string
	Before             string
	BeforeExists       bool
	After              string
	AfterExists        bool
	BeforeSnapshotPath string
	AfterSnapshotPath  string
	BeforeMode         os.FileMode
	AfterMode          os.FileMode
	BeforeModeKnown    bool
	AfterModeKnown     bool
	ModeChanged        bool
	BeforeIsSymlink    bool
	AfterIsSymlink     bool
	BeforeLinkTarget   string
	AfterLinkTarget    string
	BeforeIsDir        bool
	AfterIsDir         bool
	BeforeMetadata     *Metadata
}

type captureState struct {
	exists       bool
	isSymlink    bool
	isDir        bool
	linkTarget   string
	mode         os.FileMode
	modeKnown    bool
	size         int64
	inline       []byte
	hasInline    bool
	snapshotPath string
	hash         [sha256.Size]byte
	hashed       bool
	captured     bool // before-content recoverable (inline or snapshot)
	metadata     Metadata
}

// Capture holds the before-state of a workspace so Changes can diff against it.
type Capture struct {
	cfg    CaptureConfig
	before map[string]*captureState
	total  int64
}

// NewCapture records the current before-state of the configured roots.
func NewCapture(cfg CaptureConfig) (*Capture, error) {
	c := &Capture{cfg: cfg.normalized(), before: map[string]*captureState{}}
	if len(c.cfg.Roots) == 0 {
		return c, nil
	}
	for _, root := range c.cfg.Roots {
		if err := c.scan(root, func(path string, st *captureState) { c.before[path] = st }); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Changes walks the roots again and returns the recoverable diff. Files whose
// before-content could not be captured within budget are returned separately as
// "skipped" so callers can surface an honest degradation notice instead of
// emitting a change that would wipe the file on rewind.
func (c *Capture) Changes() (changes []Change, skipped []string, err error) {
	if c == nil {
		return nil, nil, nil
	}
	after := map[string]*captureState{}
	var afterTotal int64
	for _, root := range c.cfg.Roots {
		scanErr := c.scanWith(root, &afterTotal, false, func(path string, st *captureState) {
			after[path] = st
		})
		if scanErr != nil {
			return nil, nil, scanErr
		}
	}

	paths := unionKeys(c.before, after)
	referenced := map[string]bool{}
	for _, path := range paths {
		b := c.before[path]
		a := after[path]
		if stateEqual(b, a) {
			continue
		}
		// Before-content required but not captured (budget exhausted): do not
		// emit a destructive change; report it as skipped instead.
		if b != nil && b.exists && !b.isSymlink && !b.captured {
			skipped = append(skipped, path)
			continue
		}
		change := c.buildChange(path, b, a)
		c.externalizeBefore(&change)
		if change.BeforeSnapshotPath != "" {
			referenced[change.BeforeSnapshotPath] = true
		}
		changes = append(changes, change)
	}

	// Reclaim before-snapshots for files that did not change; keep referenced
	// ones alive for rewind reachability.
	for _, st := range c.before {
		if st.snapshotPath != "" && !referenced[st.snapshotPath] {
			_ = os.Remove(st.snapshotPath)
		}
	}
	return changes, skipped, nil
}

// Discard removes every before-snapshot created by this capture. Callers use it
// when the diff will not be consumed (for example when the tracking budget was
// exceeded before the command ran).
func (c *Capture) Discard() {
	if c == nil {
		return
	}
	for _, st := range c.before {
		if st.snapshotPath != "" {
			_ = os.Remove(st.snapshotPath)
		}
	}
}

// externalizeBefore moves large inline before-content (or a per-capture temp
// snapshot) into the content-addressed store so the transcript entry stays
// small and identical content is deduped. On any failure it leaves the change
// unchanged so the before-state is still recoverable inline/temp.
func (c *Capture) externalizeBefore(change *Change) {
	if !change.BeforeExists || change.BeforeIsSymlink || change.BeforeIsDir {
		return
	}
	if change.BeforeSnapshotPath == "" {
		if len(change.Before) <= captureExternalizeThreshold {
			return
		}
		path, err := StoreSnapshotContentAddressed(c.cfg.SnapshotDir, strings.NewReader(change.Before))
		if err != nil {
			return
		}
		change.BeforeSnapshotPath = path
		change.Before = ""
		return
	}
	// Re-store a per-capture temp into the content-addressed store for dedup;
	// the temp is then reclaimed because the change references the shared blob.
	f, err := os.Open(change.BeforeSnapshotPath)
	if err != nil {
		return
	}
	path, serr := StoreSnapshotContentAddressed(c.cfg.SnapshotDir, f)
	_ = f.Close()
	if serr == nil {
		change.BeforeSnapshotPath = path
	}
}

func (c *Capture) buildChange(path string, b, a *captureState) Change {
	change := Change{Path: path}
	if b != nil && b.exists {
		change.BeforeExists = true
		if meta := b.metadata; !meta.Empty() {
			metaCopy := meta
			change.BeforeMetadata = &metaCopy
		}
		switch {
		case b.isSymlink:
			change.BeforeIsSymlink = true
			change.BeforeLinkTarget = b.linkTarget
		case b.isDir:
			change.BeforeIsDir = true
			change.BeforeMode = b.mode
			change.BeforeModeKnown = b.modeKnown
		default:
			change.BeforeMode = b.mode
			change.BeforeModeKnown = b.modeKnown
			if b.hasInline {
				change.Before = string(b.inline)
			} else if b.snapshotPath != "" {
				change.BeforeSnapshotPath = b.snapshotPath
			}
		}
	}
	if a != nil && a.exists {
		change.AfterExists = true
		switch {
		case a.isSymlink:
			change.AfterIsSymlink = true
			change.AfterLinkTarget = a.linkTarget
		case a.isDir:
			change.AfterIsDir = true
			change.AfterMode = a.mode
			change.AfterModeKnown = a.modeKnown
		default:
			change.AfterMode = a.mode
			change.AfterModeKnown = a.modeKnown
			if a.hasInline {
				change.After = string(a.inline)
			} else {
				change.After = CaptureSnapshotPlaceholder
			}
		}
	}
	change.ModeChanged = change.BeforeModeKnown && change.AfterModeKnown && change.BeforeMode.Perm() != change.AfterMode.Perm()
	return change
}

func (c *Capture) scan(root string, emit func(string, *captureState)) error {
	total := c.total
	err := c.scanWith(root, &total, true, emit)
	c.total = total
	return err
}

func (c *Capture) scanWith(root string, total *int64, persistLarge bool, emit func(string, *captureState)) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	count := 0
	return filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if path != abs && c.cfg.IgnoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// The scan roots themselves are not tracked as objects; only nested
			// directories are, so create/delete/chmod of a directory is captured.
			if path == abs {
				return nil
			}
		}
		if count >= c.cfg.MaxFiles {
			return filepath.SkipAll
		}
		count++
		st := captureFileState(path, c.cfg.MaxInlineBytes, c.cfg.MaxTotalBytes, total, c.cfg.SnapshotDir, persistLarge)
		if st != nil {
			emit(path, st)
		}
		return nil
	})
}

// captureFileState records a single entry's state, honoring the byte budget.
// Only regular files and symlinks are tracked; other object types (sockets,
// devices, fifos) are ignored. When persistLarge is true (before-scan) large
// files are copied to the snapshot store so their content is recoverable;
// otherwise (after-scan) they are only hashed for change detection.
func captureFileState(path string, inlineLimit, budget int64, total *int64, snapshotDirOverride string, persistLarge bool) *captureState {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return nil
		}
		st := &captureState{exists: true, isSymlink: true, linkTarget: target, captured: true}
		if persistLarge {
			st.metadata = CaptureMetadata(path, info, true)
		}
		return st
	}
	if mode.IsDir() {
		st := &captureState{exists: true, isDir: true, mode: mode.Perm(), modeKnown: true, captured: true}
		if persistLarge {
			st.metadata = CaptureMetadata(path, info, false)
		}
		return st
	}
	if !mode.IsRegular() {
		return nil
	}
	st := &captureState{
		exists:    true,
		mode:      mode.Perm(),
		modeKnown: true,
		size:      info.Size(),
	}
	if persistLarge {
		st.metadata = CaptureMetadata(path, info, false)
	}
	if info.Size() <= inlineLimit && *total+info.Size() <= budget {
		data, err := os.ReadFile(path)
		if err != nil {
			return st // metadata still useful (mode), but content not captured
		}
		st.inline = data
		st.hasInline = true
		st.hash = sha256.Sum256(data)
		st.hashed = true
		st.captured = true
		*total += info.Size()
		return st
	}
	if *total+info.Size() > budget {
		// Over budget: keep metadata but leave content uncaptured so a change is
		// reported as skipped (before-scan) or compared by size (after-scan).
		return st
	}
	if persistLarge {
		snapshotPath, hash, err := copyToSnapshot(path, snapshotDirOverride)
		if err == nil {
			st.snapshotPath = snapshotPath
			st.hash = hash
			st.hashed = true
			st.captured = true
			*total += info.Size()
		}
		return st
	}
	if hash, err := hashFile(path); err == nil {
		st.hash = hash
		st.hashed = true
		*total += info.Size()
	}
	return st
}

func hashFile(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func copyToSnapshot(path, dirOverride string) (string, [sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	dir := dirOverride
	if strings.TrimSpace(dir) == "" {
		d, err := snapshotDir()
		if err != nil {
			return "", sum, err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", sum, err
	}
	in, err := os.Open(path)
	if err != nil {
		return "", sum, err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, "file-snapshot-*.bin")
	if err != nil {
		return "", sum, err
	}
	outPath := out.Name()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		_ = out.Close()
		_ = os.Remove(outPath)
		return "", sum, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(outPath)
		return "", sum, err
	}
	copy(sum[:], h.Sum(nil))
	return outPath, sum, nil
}

func stateEqual(a, b *captureState) bool {
	aExists := a != nil && a.exists
	bExists := b != nil && b.exists
	if aExists != bExists {
		return false
	}
	if !aExists {
		return true
	}
	if a.isSymlink != b.isSymlink || a.isDir != b.isDir {
		return false
	}
	if a.isSymlink {
		return a.linkTarget == b.linkTarget
	}
	if a.isDir {
		// Directory mtime changes whenever its children change, so only a mode
		// change counts as a directory change; content lives in child entries.
		return a.mode.Perm() == b.mode.Perm()
	}
	if a.modeKnown && b.modeKnown && a.mode.Perm() != b.mode.Perm() {
		return false
	}
	if a.hashed && b.hashed {
		return a.hash == b.hash
	}
	// Fall back to size when content was not hashed on one side.
	return a.size == b.size
}

func unionKeys(a, b map[string]*captureState) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// dedupeRoots removes empty, duplicate, and nested roots so a file is only
// walked once even when a writable root sits inside the working directory.
func dedupeRoots(roots []string) []string {
	cleaned := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		cleaned = append(cleaned, abs)
	}
	sort.Strings(cleaned)
	out := make([]string, 0, len(cleaned))
	for _, root := range cleaned {
		nested := false
		for _, parent := range out {
			if root == parent || strings.HasPrefix(root, parent+string(os.PathSeparator)) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, root)
		}
	}
	return out
}
