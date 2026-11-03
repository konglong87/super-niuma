//go:build computeracceptance && !windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/konglong87/super-niuma/internal/computerbridge"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

// This test adapter uses the actual Wails host's manager, not a standalone
// helper launched under Terminal/Codex's TCC identity. It is NOT a production
// agent service or an alternative to trusted tenant/session authorization.
const (
	acceptanceSocketFlag = "--computer-acceptance-socket"
	acceptanceHeader     = "X-Go-E2E-Acceptance"
	acceptanceMaxBody    = 32 << 10
	acceptanceTimeout    = 15 * time.Second
)

type acceptanceRequest struct {
	Op            string      `json:"op"`
	TargetID      cu.TargetID `json:"target_id,omitempty"`
	SessionID     string      `json:"session_id,omitempty"`
	Approved      bool        `json:"approved,omitempty"`
	Action        *cu.Action  `json:"action,omitempty"`
	ObservationID string      `json:"observation_id,omitempty"`
	DisplayID     string      `json:"display_id,omitempty"`
	WindowID      string      `json:"window_id,omitempty"`
}
type acceptanceResponse struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func startComputerAcceptance(ctx context.Context, manager *computerManager, args []string) (func(), error) {
	path, err := acceptanceSocketArg(args)
	if err != nil || path == "" {
		return func() {}, err
	}
	if err := validateAcceptanceDirectory(path); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	hostCtx := manager.runtimeContext()
	gate := &acceptanceAdmission{}
	server := &http.Server{Handler: gate.wrap(acceptanceHandler(hostCtx, manager)), ReadHeaderTimeout: time.Second,
		ReadTimeout: acceptanceTimeout, WriteTimeout: acceptanceTimeout, IdleTimeout: time.Second, MaxHeaderBytes: 4096}
	var once sync.Once
	done := make(chan struct{})
	closeServer := func() {
		once.Do(func() {
			close(done)
			gate.close()
			_ = server.Close()
			gate.active.Wait()
			_ = listener.Close()
		})
	}
	go func() { _ = server.Serve(listener); closeServer() }()
	go func() {
		select {
		case <-ctx.Done():
			closeServer()
		case <-done:
		}
	}()
	return closeServer, nil
}

// Stop admission before draining handlers so no queued command can resurrect
// a backend after shutdown has detached the manager.
type acceptanceAdmission struct {
	mu     sync.Mutex
	closed bool
	active sync.WaitGroup
}

func (g *acceptanceAdmission) close() { g.mu.Lock(); g.closed = true; g.mu.Unlock() }
func (g *acceptanceAdmission) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			http.Error(w, "acceptance closed", http.StatusServiceUnavailable)
			return
		}
		g.active.Add(1)
		g.mu.Unlock()
		defer g.active.Done()
		next.ServeHTTP(w, r)
	})
}

func acceptanceSocketArg(args []string) (string, error) {
	var path string
	for i := 0; i < len(args); i++ {
		if args[i] != acceptanceSocketFlag {
			continue
		}
		if path != "" || i+1 >= len(args) || !filepath.IsAbs(args[i+1]) {
			return "", errors.New("acceptance socket requires one absolute path")
		}
		i++
		path = args[i]
	}
	return path, nil
}

func validateAcceptanceDirectory(path string) error {
	dir := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if resolved != filepath.Clean(dir) {
		return errors.New("acceptance directory must not contain symlinks")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("acceptance socket requires an existing owner-only 0700 directory")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("acceptance socket path already exists or cannot be checked")
	}
	return nil
}

func acceptanceHandler(hostCtx context.Context, manager *computerManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" || r.Header.Get(acceptanceHeader) != "1" || r.Host != "localhost" {
			http.Error(w, "acceptance client required", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/command" || r.Method != http.MethodPost {
			http.Error(w, "POST /command required", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, acceptanceMaxBody)
		defer r.Body.Close()
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req acceptanceRequest
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid acceptance request", http.StatusBadRequest)
			return
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			http.Error(w, "one request required", http.StatusBadRequest)
			return
		}
		data, err := dispatchAcceptance(hostCtx, r.Context(), manager, req)
		out := acceptanceResponse{Data: data}
		if err != nil {
			out.Error = err.Error()
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}

func dispatchAcceptance(hostCtx, ctx context.Context, m *computerManager, r acceptanceRequest) (any, error) {
	// Backend construction must use host lifetime, never the HTTP request's
	// lifetime (which ends after every response and would kill the helper).
	switch r.Op {
	case "capabilities":
		return m.capabilities(hostCtx)
	case "runtime_status":
		if m.remote != nil {
			return m.remote.RuntimeStatus(ctx, computerRuntimeSupervisionOwner)
		}
		return m.runtimeStatus(), nil
	case "runtime_disconnect_bridge":
		if m.remote == nil || m.remote.SocketPath() == "" {
			return nil, errors.New("standalone computer runtime is unavailable")
		}
		if err := os.Remove(m.remote.SocketPath()); err != nil {
			return nil, err
		}
		return map[string]string{"state": "bridge_disconnected"}, nil
	case "runtime_stop":
		if err := m.stopRuntime(ctx); err != nil {
			return nil, err
		}
		return map[string]string{"state": "stopped"}, nil
	case "start":
		if !r.Approved {
			return nil, errors.New("explicit acceptance session approval required")
		}
		return m.startWithLifetime(ctx, hostCtx, ComputerSessionStartInput{Approved: true})
	case "launch_target":
		return m.launchTarget(ctx, r.SessionID, r.TargetID)
	case "observe":
		return m.observeTarget(ctx, r.SessionID, cu.ObserveRequest{SessionID: r.SessionID, DisplayID: r.DisplayID, WindowID: r.WindowID})
	case "pause":
		return m.control(ctx, r.SessionID, cu.ActionPause)
	case "resume":
		return m.control(ctx, r.SessionID, cu.ActionResume)
	case "stop":
		return m.control(ctx, r.SessionID, cu.ActionStop)
	case "snapshot", "execute", computerbridge.OpExecuteTurn, "image":
		switch r.Op {
		case "snapshot":
			return m.snapshot(ctx, r.SessionID)
		case "execute", computerbridge.OpExecuteTurn:
			if r.Action == nil || r.Action.SessionID != r.SessionID {
				return nil, errors.New("action/session binding required")
			}
			atomicLaunch := r.Op == computerbridge.OpExecuteTurn && r.Action.Kind == cu.ActionLaunchApp
			if !r.Action.Kind.IsInput() && r.Action.Kind != cu.ActionWait && !atomicLaunch {
				return nil, errors.New("input, wait, or atomic launch action required")
			}
			if r.Op == computerbridge.OpExecuteTurn {
				// The observer gets the committed atomic result and its already
				// associated PNG, never legacy execute/image/posthoc reconstruction.
				result, err := m.executeTurn(ctx, *r.Action)
				response := computerbridge.ExecuteTurnResponse{Result: result}
				if result.ScreenshotState == cu.ScreenshotReady && result.Observation != nil && result.Screenshot != nil {
					response.ScreenshotData = base64.StdEncoding.EncodeToString(result.ScreenshotData)
					response.ScreenshotMediaType = result.Screenshot.MediaType
					response.ScreenshotExpiresAt = result.Observation.ExpiresAt
				}
				return response, err
			}
			return m.execute(ctx, *r.Action)
		default:
			if strings.TrimSpace(r.ObservationID) == "" {
				return nil, errors.New("observation id required")
			}
			data, media, err := m.observationImage(ctx, r.SessionID, r.ObservationID)
			if err != nil {
				return nil, err
			}
			return map[string]string{"image_data": base64.StdEncoding.EncodeToString(data), "media_type": media}, nil
		}
	default:
		return nil, fmt.Errorf("unsupported acceptance operation")
	}
}
