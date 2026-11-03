package computerbridge

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const listenerTestWait = 5 * time.Second

type listenerTestHost struct {
	Service
	calls  atomic.Int32
	lookup func(context.Context, cu.SessionOwner) (string, error)
}

func (h *listenerTestHost) Lookup(ctx context.Context, owner cu.SessionOwner) (string, error) {
	h.calls.Add(1)
	if h.lookup != nil {
		return h.lookup(ctx, owner)
	}
	return "approved-session", nil
}

func listenerTestRoot(t *testing.T) string {
	t.Helper()
	if !listenerUnixSupported {
		t.Skip("requires Unix filesystem ownership and sockets")
	}
	// Short paths avoid sockaddr_un limits. All filesystem effects stay in the
	// package, not /tmp, and no test ever writes a token to disk.
	path, err := os.MkdirTemp(".", ".l-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(path); err != nil {
			t.Error(err)
		}
	})
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, listenerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	return path
}

func listenerTestStart(t *testing.T, ctx context.Context, root string, host Host) *Listener {
	t.Helper()
	l, err := StartListener(ctx, root, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func listenerTestClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal("invalid listener client config")
	}
	return client
}

func listenerTestEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("listener left filesystem entries: count=%d error=%v", len(entries), err)
	}
}

func listenerTestDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(listenerTestWait):
		t.Fatal("listener did not stop")
	}
}

func TestListenerRealClientLifecycle(t *testing.T) {
	root := listenerTestRoot(t)
	host := &listenerTestHost{lookup: func(ctx context.Context, owner cu.SessionOwner) (string, error) {
		if owner != testOwner() {
			t.Error("listener changed owner binding")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > DefaultRequestTimeout {
			t.Error("host request missing bounded deadline")
		}
		return "approved-session", nil
	}}
	l := listenerTestStart(t, context.Background(), root, host)
	cfg := l.Config()
	secret, err := base64.RawURLEncoding.DecodeString(cfg.Token)
	if err != nil || len(secret) != listenerTokenBytes {
		t.Fatal("listener token has invalid entropy length")
	}
	if cfg.RequestTimeout != DefaultRequestTimeout || l.socket.Addr().Network() != "unix" {
		t.Fatal("unexpected transport defaults")
	}
	for _, check := range []struct {
		path string
		mode os.FileMode
	}{
		{root, os.ModeDir | listenerDirectoryMode},
		{filepath.Dir(cfg.SocketPath), os.ModeDir | listenerDirectoryMode},
		{cfg.SocketPath, os.ModeSocket | listenerSocketMode},
	} {
		info, err := os.Lstat(check.path)
		if err != nil || info.Mode() != check.mode || !listenerOwnedByUser(info) {
			t.Fatalf("unexpected private endpoint permissions: %v", err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(cfg.SocketPath))
	if err != nil || len(entries) != 1 || entries[0].Name() != listenerSocketName {
		t.Fatal("private endpoint contains non-socket artifacts")
	}
	client := listenerTestClient(t, cfg)
	if id, err := client.Lookup(context.Background(), testOwner()); err != nil || id != "approved-session" {
		t.Fatalf("real handler lookup failed: %v", err)
	}
	cfg.Token = "changed-copy"
	if l.Config().Token == cfg.Token {
		t.Fatal("config was mutable")
	}
	for range 2 {
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	listenerTestEmpty(t, root)
	if _, err := client.Lookup(context.Background(), testOwner()); !errors.Is(err, ErrTransport) {
		t.Fatal("closed endpoint remained reachable")
	}
}

func TestListenerReusesAuthenticationAndBounds(t *testing.T) {
	root := listenerTestRoot(t)
	host := &listenerTestHost{}
	l := listenerTestStart(t, context.Background(), root, host)
	cfg := l.Config()
	bad := cfg
	bad.Token = "wrong-token"
	if _, err := listenerTestClient(t, bad).Lookup(context.Background(), testOwner()); !errors.Is(err, ErrRemote) {
		t.Fatal("wrong token accepted")
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.SocketPath)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: listenerTestWait}
	for _, tt := range []struct {
		name, body, origin, host string
		headerSize, status       int
	}{
		{name: "origin", body: serverLookupJSON, origin: "null", host: "localhost", status: http.StatusUnauthorized},
		{name: "host", body: serverLookupJSON, host: "elsewhere", status: http.StatusUnauthorized},
		{name: "body", body: strings.Repeat("x", MaxRequestBytes+1), host: "localhost", status: http.StatusBadRequest},
		{name: "headers", body: serverLookupJSON, host: "localhost", headerSize: maxHeaderBytes + 8192, status: http.StatusRequestHeaderFieldsTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, commandURL, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tt.host
			req.Header.Set(AuthorizationHeader, BearerPrefix+cfg.Token)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.headerSize != 0 {
				req.Header.Set("X-Large", strings.Repeat("x", tt.headerSize))
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Fatalf("status=%d want=%d", resp.StatusCode, tt.status)
			}
		})
	}
	if host.calls.Load() != 0 {
		t.Fatal("invalid traffic reached host")
	}
	if l.server.ReadHeaderTimeout != listenerHeaderTimeout || l.server.ReadTimeout != listenerReadTimeout ||
		l.server.WriteTimeout != DefaultRequestTimeout || l.server.IdleTimeout != listenerIdleTimeout ||
		l.server.MaxHeaderBytes != maxHeaderBytes {
		t.Fatal("unbounded server configuration")
	}
}

func TestListenerCancelsActiveRequest(t *testing.T) {
	for _, parentCancellation := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "parent"}[parentCancellation], func(t *testing.T) {
			root := listenerTestRoot(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, canceled := make(chan struct{}), make(chan struct{})
			host := &listenerTestHost{lookup: func(ctx context.Context, _ cu.SessionOwner) (string, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				return "", ctx.Err()
			}}
			l := listenerTestStart(t, ctx, root, host)
			client := listenerTestClient(t, l.Config())
			requestDone := make(chan struct{})
			go func() {
				defer close(requestDone)
				if _, err := client.Lookup(context.Background(), testOwner()); err == nil {
					t.Error("canceled request succeeded")
				}
			}()
			listenerTestDone(t, started)
			if parentCancellation {
				cancel()
			} else if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			listenerTestDone(t, l.done)
			listenerTestDone(t, canceled)
			listenerTestDone(t, requestDone)
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			listenerTestEmpty(t, root)
		})
	}
}

func TestListenerRejectsInvalidRoots(t *testing.T) {
	root := listenerTestRoot(t)
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, listenerSocketMode); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, listenerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", ".", filepath.Base(root), root + "/.", root + "\x00", filepath.Join(root, "missing"), file, link, filepath.Join(link, "child")} {
		l, err := StartListener(context.Background(), path, &listenerTestHost{})
		if l != nil {
			_ = l.Close()
			t.Error("accepted invalid root")
		}
		if err == nil {
			t.Error("missing invalid root error")
		}
	}
	for _, mode := range []os.FileMode{0755, 0770, 0701, 0500, 0700 | os.ModeSetgid, 0700 | os.ModeSticky} {
		if err := os.Chmod(child, mode); err != nil {
			t.Fatal(err)
		}
		l, err := StartListener(context.Background(), child, &listenerTestHost{})
		if l != nil {
			_ = l.Close()
			t.Error("accepted insecure root mode")
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("insecure root error: %v", err)
		}
	}
	if err := os.Chmod(child, listenerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	listenerTestEmpty(t, child)
}

func TestListenerSetupFailureLeavesNoArtifacts(t *testing.T) {
	root := listenerTestRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tt := range []struct {
		ctx  context.Context
		host Host
		want error
	}{
		{nil, &listenerTestHost{}, ErrInvalidConfig},
		{context.Background(), nil, ErrInvalidConfig},
		{ctx, &listenerTestHost{}, context.Canceled},
	} {
		l, err := StartListener(tt.ctx, root, tt.host)
		if l != nil || !errors.Is(err, tt.want) {
			t.Fatalf("invalid setup result: %v", err)
		}
		listenerTestEmpty(t, root)
	}
	longRoot := filepath.Join(root, strings.Repeat("a", 100))
	if err := os.Mkdir(longRoot, listenerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	l, err := StartListener(context.Background(), longRoot, &listenerTestHost{})
	if l != nil || err == nil {
		if l != nil {
			_ = l.Close()
		}
		t.Fatal("overlong Unix socket path accepted")
	}
	listenerTestEmpty(t, longRoot)
}

func TestListenerConcurrentLifecycles(t *testing.T) {
	root := listenerTestRoot(t)
	const count = 12
	listeners := make(chan *Listener, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := StartListener(context.Background(), root, &listenerTestHost{})
			if err != nil {
				t.Error(err)
				return
			}
			listeners <- l
		}()
	}
	wg.Wait()
	close(listeners)
	tokens, paths := map[string]bool{}, map[string]bool{}
	for l := range listeners {
		cfg := l.Config()
		if tokens[cfg.Token] || paths[cfg.SocketPath] {
			t.Error("launch reused a secret or socket")
		}
		tokens[cfg.Token], paths[cfg.SocketPath] = true, true
		client := listenerTestClient(t, cfg)
		if _, err := client.Lookup(context.Background(), testOwner()); err != nil {
			t.Error(err)
		}
		for range count {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if l.Config() != cfg {
					t.Error("config changed concurrently")
				}
				if err := l.Close(); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	listenerTestEmpty(t, root)
}

func TestListenerCancellationDuringStart(t *testing.T) {
	root := listenerTestRoot(t)
	for range 40 {
		ctx, cancel := context.WithCancel(context.Background())
		go cancel()
		l, err := StartListener(ctx, root, &listenerTestHost{})
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if l != nil {
			listenerTestDone(t, l.done)
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
		}
		listenerTestEmpty(t, root)
	}
}

func TestListenerSlowHeadersAreClosed(t *testing.T) {
	l := listenerTestStart(t, context.Background(), listenerTestRoot(t), &listenerTestHost{})
	conn, err := net.Dial("unix", l.Config().SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(listenerHeaderTimeout + listenerTestWait)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "POST /command HTTP/1.1\r\nHost: localhost\r\n"); err != nil {
		t.Fatal(err)
	}
	_, err = http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		t.Fatal("incomplete headers received a successful response")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("server did not enforce header timeout")
	}
}

func TestListenerConnectionLimitAndSaturatedShutdown(t *testing.T) {
	root := listenerTestRoot(t)
	started := make(chan struct{}, listenerMaxConnections+1)
	host := &listenerTestHost{lookup: func(ctx context.Context, _ cu.SessionOwner) (string, error) {
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}}
	l := listenerTestStart(t, context.Background(), root, host)
	client := listenerTestClient(t, l.Config())
	var wg sync.WaitGroup
	request := func() { defer wg.Done(); _, _ = client.Lookup(context.Background(), testOwner()) }
	for range listenerMaxConnections {
		wg.Add(1)
		go request()
	}
	for range listenerMaxConnections {
		select {
		case <-started:
		case <-time.After(listenerTestWait):
			t.Fatal("connections failed to reach host")
		}
	}
	wg.Add(1)
	go request()
	select {
	case <-started:
		t.Fatal("connection bound exceeded")
	case <-time.After(100 * time.Millisecond):
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	requestsDone := make(chan struct{})
	go func() { wg.Wait(); close(requestsDone) }()
	listenerTestDone(t, requestsDone)
	listenerTestEmpty(t, root)
}

func TestListenerCleanupPreservesUnrelatedEntries(t *testing.T) {
	root := listenerTestRoot(t)
	l := listenerTestStart(t, context.Background(), root, &listenerTestHost{})
	other := listenerTestStart(t, context.Background(), root, &listenerTestHost{})
	marker := filepath.Join(root, "app-data")
	if err := os.WriteFile(marker, []byte("keep"), listenerSocketMode); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("removed parent app data")
	}
	if _, err := listenerTestClient(t, other.Config()).Lookup(context.Background(), testOwner()); err != nil {
		t.Fatal("closed sibling listener")
	}
	// Do not recursively remove content that does not belong to the listener.
	foreign := filepath.Join(filepath.Dir(other.Config().SocketPath), "foreign")
	if err := os.WriteFile(foreign, []byte("keep"), listenerSocketMode); err != nil {
		t.Fatal(err)
	}
	firstErr := other.Close()
	if firstErr == nil || firstErr != other.Close() {
		t.Fatal("cleanup error not stable")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("removed unrelated file")
	}
}

func TestListenerCleanupRefusesReplacementSocket(t *testing.T) {
	root := listenerTestRoot(t)
	l := listenerTestStart(t, context.Background(), root, &listenerTestHost{})
	path := l.Config().SocketPath
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, path); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("replacement was not refused: %v", err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("removed replacement socket")
	}
}

func TestListenerRejectsWritableAncestor(t *testing.T) {
	root := listenerTestRoot(t)
	child := filepath.Join(root, "private")
	if err := os.Mkdir(child, listenerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0777); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, listenerDirectoryMode) }()
	l, err := StartListener(context.Background(), child, &listenerTestHost{})
	if l != nil {
		_ = l.Close()
		t.Fatal("accepted a replaceable app-data root")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unsafe ancestor error: %v", err)
	}
	listenerTestEmpty(t, child)
	// Sticky shared ancestry is safe for an owned child; test the predicate
	// without extending the already short Unix socket pathname.
	if err := os.Chmod(root, 0777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil || !listenerTrustedAncestor(info) {
		t.Fatal("rejected owned sticky shared ancestor")
	}
}

func TestListenerServeExitCleansUp(t *testing.T) {
	root := listenerTestRoot(t)
	l := listenerTestStart(t, context.Background(), root, &listenerTestHost{})
	if _, err := listenerTestClient(t, l.Config()).Lookup(context.Background(), testOwner()); err != nil {
		t.Fatal(err)
	}
	if err := l.socket.Close(); err != nil {
		t.Fatal(err)
	}
	listenerTestDone(t, l.done)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	listenerTestEmpty(t, root)
}

func TestListenerCleanupPinsOriginalRoot(t *testing.T) {
	root := listenerTestRoot(t)
	l := listenerTestStart(t, context.Background(), root, &listenerTestHost{})
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if err := os.Mkdir(root, listenerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	listenerTestEmpty(t, root)
	listenerTestEmpty(t, moved)
}

func TestListenerCloseDoesNotWaitForUncooperativeHost(t *testing.T) {
	root := listenerTestRoot(t)
	started, release, hostDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	host := &listenerTestHost{lookup: func(context.Context, cu.SessionOwner) (string, error) {
		defer close(hostDone)
		close(started)
		<-release
		return "approved-session", nil
	}}
	l := listenerTestStart(t, context.Background(), root, host)
	client := listenerTestClient(t, l.Config())
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = client.Lookup(context.Background(), testOwner())
	}()
	listenerTestDone(t, started)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	}()
	listenerTestDone(t, closed)
	listenerTestDone(t, requestDone)
	listenerTestEmpty(t, root)
	select {
	case <-hostDone:
		t.Fatal("host should still be waiting for release")
	default:
	}
}
