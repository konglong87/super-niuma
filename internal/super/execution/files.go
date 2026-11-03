package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/permissions"
	superstate "github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/internal/tools"
)

const maxDirectoryEntries = 256

type workspaceFS struct {
	root                 *os.Root
	workspace, protected string
	reads, writes        []string
}

func (f *workspaceFS) Close() error { return f.root.Close() }

func openWorkspace(r Request) (*workspaceFS, error) {
	if !secureFilesystemSupported() {
		return nil, ErrUnavailable
	}
	if !filepath.IsAbs(r.Workspace) || filepath.Clean(r.Workspace) != r.Workspace || !utf8.ValidString(r.Workspace) {
		return nil, ErrInvalid
	}
	// An explicitly authorized root is still not permission to enter private
	// state/configuration or follow a symlink before binding the directory.
	for _, c := range r.Workspace {
		if unicode.IsControl(c) {
			return nil, ErrInvalid
		}
	}
	if r.Workspace == string(filepath.Separator) {
		return nil, ErrInvalid
	}
	for _, part := range strings.Split(filepath.ToSlash(r.Workspace), "/") {
		if part != "" && forbiddenComponent(part) {
			return nil, ErrAuthority
		}
	}
	real, err := filepath.EvalSymlinks(r.Workspace)
	if err != nil || real != r.Workspace {
		return nil, ErrAuthority
	}
	protected, err := superstate.StateDirectory()
	if err != nil || withinPath(r.Workspace, protected) {
		return nil, ErrAuthority
	}
	root, err := os.OpenRoot(r.Workspace)
	if err != nil {
		return nil, ErrInvalid
	}
	if !verifyRoot(root, r.Workspace) {
		root.Close()
		return nil, ErrAuthority
	}
	return &workspaceFS{root, r.Workspace, protected, r.ReadPaths, r.WritePaths}, nil
}

func forbiddenComponent(name string) bool {
	s := strings.ToLower(name)
	if strings.HasPrefix(s, ".") || strings.TrimSpace(s) != s || s == "" {
		return true
	}
	switch s {
	case "credentials", "credential", "secrets", "secret", "fixtures", "testdata", "acceptance", "policies",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "authorized_keys", "known_hosts", "work-orders.db":
		return true
	}
	for _, p := range []string{"credentials.", "credential.", "secrets.", "secret.", "work-orders.db-"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	switch strings.ToLower(path.Ext(s)) {
	case ".pem", ".key", ".p12", ".pfx", ".db", ".sqlite", ".sqlite3":
		return true
	}
	return false
}
func validRelativePath(name string) bool {
	if name == "" || len(name) > maxPathBytes || !utf8.ValidString(name) || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:*?[]{}") {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if forbiddenComponent(part) {
			return false
		}
	}
	return true
}
func withinPath(name, prefix string) bool {
	// Conservative on case-insensitive macOS volumes as well as Linux.
	n, p := strings.ToLower(filepath.Clean(name)), strings.ToLower(filepath.Clean(prefix))
	return n == p || strings.HasPrefix(n, p+string(filepath.Separator))
}
func allowedPath(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if name == p || strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}
func (f *workspaceFS) permitted(name string, write bool) bool {
	if !validRelativePath(name) || withinPath(filepath.Join(f.workspace, filepath.FromSlash(name)), f.protected) {
		return false
	}
	prefixes := f.reads
	if write {
		prefixes = f.writes
	}
	return allowedPath(name, prefixes)
}

type fileTool struct {
	fs    *workspaceFS
	state *attemptState
	write bool
}

func (t *fileTool) Name() string {
	if t.write {
		return ToolWrite
	}
	return ToolRead
}
func (t *fileTool) Description() string {
	if t.write {
		return "Write bounded UTF-8 content to an explicitly authorized relative file path. No symlinks, hidden files, credentials or verification fixtures."
	}
	return "Read a bounded UTF-8 file or list immediate entries of an explicitly authorized relative directory. No recursion or symlinks."
}
func (t *fileTool) InputSchema() json.RawMessage {
	if t.write {
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)
	}
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
}

// The tool enforces its own strict byte limit; bypass generic transcript disk
// persistence/truncation of tool results (there is no interactive transcript).
func (t *fileTool) SkipToolResultBudget() bool { return true }
func (t *fileTool) Run(ctx context.Context, raw json.RawMessage, _ tools.Context) tools.Result {
	fail := func(err error) tools.Result {
		t.state.fail(err)
		t.state.log(actionToolEnd, errorCode(err))
		return tools.Result{Content: "super tool rejected: " + errorCode(err), IsError: true}
	}
	if err := t.state.authorize(ctx); err != nil {
		return fail(err)
	}
	t.state.log(actionToolStart, codeOK)
	var input struct {
		Path    string  `json:"path"`
		Content *string `json:"content,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > maxResponseBytes || !utf8.Valid(raw) || decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF ||
		(!t.write && input.Content != nil) || (t.write && input.Content == nil) {
		return fail(ErrInvalid)
	}
	if !t.fs.permitted(input.Path, t.write) {
		return fail(ErrAuthority)
	}
	if t.write {
		if !utf8.ValidString(*input.Content) || len(*input.Content) > MaxFileBytes {
			return fail(ErrInvalid)
		}
		if err := t.fs.write(ctx, input.Path, []byte(*input.Content), t.state.authorize); err != nil {
			return fail(err)
		}
		digest := sha256.Sum256([]byte(*input.Content))
		t.state.recordWrite(WriteReceipt{Path: input.Path, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(*input.Content)), AttemptID: t.state.request.AttemptID})
		t.state.log(actionToolEnd, codeOK)
		return tools.Result{Content: "file written"}
	}
	content, err := t.fs.read(ctx, input.Path, t.state.authorize)
	if err != nil {
		return fail(err)
	}
	t.state.log(actionToolEnd, codeOK)
	return tools.Result{Content: content}
}
func newRegistry(fs *workspaceFS, state *attemptState) *tools.Registry {
	registry := tools.NewRegistry()
	for _, write := range []bool{false, true} {
		paths := fs.reads
		if write {
			paths = fs.writes
		}
		if len(paths) == 0 {
			continue
		}
		tool := &fileTool{fs: fs, state: state, write: write}
		registry.Register(tools.Guard(tool, permissions.Policy{Allow: []string{tool.Name()}, DefaultMode: permissions.ModeDeny}))
	}
	return registry
}
