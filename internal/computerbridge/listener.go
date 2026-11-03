package computerbridge

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/netutil"
)

const (
	listenerDirectoryMode   = 0700
	listenerSocketMode      = 0600
	listenerTokenBytes      = 32
	listenerDirectoryBytes  = 16
	listenerDirectoryPrefix = "b-"
	listenerSocketName      = "s"
	listenerMaxConnections  = 32
	listenerHeaderTimeout   = 2 * time.Second
	listenerReadTimeout     = 5 * time.Second
	listenerIdleTimeout     = time.Second
)

// Listener owns one private Unix endpoint. It must not be copied. Config and
// Close are safe for concurrent use; Config contains a secret and must not be
// logged or persisted. Closing cancels host requests, but does not wait for a
// Host that ignores its context to return.
type Listener struct {
	config Config
	server *http.Server
	socket net.Listener
	files  *listenerFiles
	cancel context.CancelFunc
	served chan error
	done   chan struct{}
	once   sync.Once
	err    error
}

// StartListener serves the existing authenticated handler without granting any
// authority. rootDir must already exist, be absolute and symlink-free, belong to
// the effective user, and have exactly mode 0700. Ancestors must be owned by
// that user or root, and must not be group/other-writable unless sticky. The
// caller must keep the root path stable while the listener runs. No token is written
// to disk, and no TCP fallback is attempted on unsupported platforms.
func StartListener(parent context.Context, rootDir string, host Host) (_ *Listener, err error) {
	if parent == nil || host == nil {
		return nil, ErrInvalidConfig
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if !listenerUnixSupported {
		return nil, fmt.Errorf("computer bridge requires Unix socket permissions: %w", errors.ErrUnsupported)
	}
	files, err := newListenerFiles(rootDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, files.close())
		}
	}()
	token, err := listenerRandom(listenerTokenBytes)
	if err != nil {
		return nil, err
	}
	handler, err := NewHandler(token, host)
	if err != nil {
		return nil, err
	}
	socket, err := net.Listen("unix", filepath.Join(rootDir, files.name, listenerSocketName))
	if err != nil {
		return nil, fmt.Errorf("listen on private computer bridge socket: %w", err)
	}
	// Cleanup uses pinned directory handles and identity checks, not net's
	// pathname-based automatic unlink (which could remove a replacement).
	socket.(*net.UnixListener).SetUnlinkOnClose(false)
	defer func() {
		if err != nil {
			_ = socket.Close()
		}
	}()
	files.socketInfo, err = files.dir.Lstat(listenerSocketName)
	if err != nil {
		return nil, err
	}
	if err = files.dir.Chmod(listenerSocketName, listenerSocketMode); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	l := &Listener{
		config: Config{SocketPath: socket.Addr().String(), Token: token, RequestTimeout: DefaultRequestTimeout},
		socket: netutil.LimitListener(socket, listenerMaxConnections), files: files,
		cancel: cancel, served: make(chan error, 1), done: make(chan struct{}),
	}
	l.server = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCtx, requestCancel := context.WithTimeout(r.Context(), DefaultRequestTimeout)
			defer requestCancel()
			handler.ServeHTTP(w, r.WithContext(requestCtx))
		}),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: listenerHeaderTimeout,
		ReadTimeout:       listenerReadTimeout,
		WriteTimeout:      DefaultRequestTimeout,
		IdleTimeout:       listenerIdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// Panic values and request details must never reach a default logger.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	l.server.SetKeepAlivesEnabled(false)
	go func() {
		l.served <- l.server.Serve(l.socket)
		_ = l.Close()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-l.done:
		}
	}()
	// If cancellation won during setup, let the lifecycle own cleanup; the
	// startup error defer must not close the directory handles a second time.
	if parentErr := parent.Err(); parentErr != nil {
		closeErr := l.Close()
		files = nil
		return nil, errors.Join(parentErr, closeErr)
	}
	return l, nil
}

// Config returns a value copy for NewClient. It remains immutable after Close;
// callers must treat the token as a secret even after the endpoint is gone.
func (l *Listener) Config() Config { return l.config }

// Close cancels requests, closes all connections, joins the serving loop, and
// removes only this listener's socket and empty private directory. Repeated
// calls return the same result. It never removes the caller's app-data root.
func (l *Listener) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		defer close(l.done)
		l.cancel()
		serverErr := l.server.Close()
		socketErr := l.socket.Close()
		if errors.Is(socketErr, net.ErrClosed) {
			socketErr = nil
		}
		serveErr := <-l.served
		if errors.Is(serveErr, http.ErrServerClosed) || errors.Is(serveErr, net.ErrClosed) {
			serveErr = nil
		}
		l.err = errors.Join(serverErr, socketErr, serveErr, l.files.close())
	})
	return l.err
}

// Directory handles confine cleanup even if a caller renames its root. Identity
// checks refuse to delete replacement entries; Remove (not RemoveAll) preserves
// any unrelated files introduced into our otherwise empty directory.
type listenerFiles struct {
	root, dir           *os.Root
	name                string
	dirInfo, socketInfo os.FileInfo
}

func newListenerFiles(path string) (_ *listenerFiles, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf("private computer bridge root must be an absolute clean path: %w", ErrInvalidConfig)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect private computer bridge root: %w", err)
	}
	if !info.IsDir() || info.Mode() != os.ModeDir|listenerDirectoryMode || !listenerOwnedByUser(info) {
		return nil, fmt.Errorf("computer bridge root must be an owner-owned 0700 directory: %w", ErrInvalidConfig)
	}
	// Reject symlinks in every component, not just the final directory. Require
	// a canonical app-data path rather than silently following an alias.
	// An untrusted writable ancestor could otherwise replace our validated root
	// before the pathname-based Unix bind. Sticky shared ancestors are allowed.
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		ancestor, err := os.Lstat(parent)
		if err != nil {
			return nil, err
		}
		if !ancestor.IsDir() || !listenerTrustedAncestor(ancestor) {
			return nil, fmt.Errorf("computer bridge root contains a symlink or untrusted ancestor: %w", ErrInvalidConfig)
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	f := &listenerFiles{root: root}
	defer func() {
		if err != nil {
			err = errors.Join(err, f.close())
		}
	}()
	opened, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || opened.Mode() != os.ModeDir|listenerDirectoryMode || !listenerOwnedByUser(opened) {
		return nil, fmt.Errorf("computer bridge root changed during setup: %w", ErrInvalidConfig)
	}
	random, err := listenerRandom(listenerDirectoryBytes)
	if err != nil {
		return nil, err
	}
	name := listenerDirectoryPrefix + random
	if err := root.Mkdir(name, listenerDirectoryMode); err != nil {
		return nil, err
	}
	f.name = name
	f.dirInfo, err = root.Lstat(name)
	if err != nil {
		return nil, err
	}
	f.dir, err = root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	// Restore exactly 0700 even under an unusually restrictive process umask.
	if err := f.dir.Chmod(".", listenerDirectoryMode); err != nil {
		return nil, err
	}
	return f, nil
}

func listenerRandom(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate private computer bridge secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (f *listenerFiles) close() error {
	if f == nil {
		return nil
	}
	var err error
	if f.dir != nil {
		err = errors.Join(listenerRemoveOwned(f.dir, listenerSocketName, f.socketInfo), f.dir.Close())
	}
	return errors.Join(err, listenerRemoveOwned(f.root, f.name, f.dirInfo), f.root.Close())
}

func listenerRemoveOwned(root *os.Root, name string, expected os.FileInfo) error {
	if expected == nil {
		return nil
	}
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(current, expected) {
		return fmt.Errorf("computer bridge cleanup refused a replaced entry: %w", ErrInvalidConfig)
	}
	return root.Remove(name)
}
