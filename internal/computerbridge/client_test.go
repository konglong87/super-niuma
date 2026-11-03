package computerbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const testToken = "test-only-bridge-token"

func testOwner() cu.SessionOwner { return cu.SessionOwner{TenantID: 27, UserID: 93, SessionID: 156} }
func testAction() cu.Action {
	return cu.Action{ID: "action-a", SessionID: "host-session", ObservationID: "observation-a", Kind: cu.ActionClick, Point: &cu.Point{X: 1, Y: 2}}
}
func testCapabilities() cu.Capabilities {
	return cu.Capabilities{ProtocolVersion: cu.ProtocolVersion, Platform: cu.PlatformMacOS, Backend: cu.BackendNativeHost,
		CaptureReadiness: cu.ReadinessReady, InputReadiness: cu.ReadinessReady, PermissionState: cu.PermissionApproved,
		CoordinateSpace: cu.CoordinateSpace{Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels, Width: 32, Height: 32, ScaleFactor: 1}}
}
func testObservation() cu.Observation {
	return cu.Observation{ID: "observation-a", SessionID: "host-session", Width: 32, Height: 32, ScaleFactor: 1, ObservedAt: time.Now(), Capabilities: testCapabilities()}
}
func testReceipt() cu.ActionReceipt {
	a := testAction()
	return cu.ActionReceipt{ActionID: a.ID, SessionID: a.SessionID, BeforeObservationID: a.ObservationID, Outcome: cu.OutcomeExecuted, Verification: cu.VerificationNotChecked}
}

// Keep test filesystem effects inside this package and remove them on cleanup.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".client-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	path, err := filepath.Abs(filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func newUnixClient(t *testing.T, timeout time.Duration, handler http.HandlerFunc) *Client {
	t.Helper()
	path := socketPath(t)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	client, err := NewClient(Config{SocketPath: path, Token: testToken, RequestTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func writeData(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(Response{Data: data}); err != nil {
		t.Error(err)
	}
}
func readRequest(t *testing.T, r *http.Request) Request {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != CommandPath || r.Host != "localhost" {
		t.Errorf("unexpected HTTP request: %s %s %s", r.Method, r.URL.Path, r.Host)
	}
	if r.Header.Get(AuthorizationHeader) != BearerPrefix+testToken {
		t.Error("missing bearer authentication")
	}
	if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
		t.Error("missing JSON headers")
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Accept-Encoding") != "" || r.Header.Get("Idempotency-Key") != "" {
		t.Error("unsafe transport headers")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
	}
	if len(data) > MaxRequestBytes || bytes.Contains(data, []byte(`"approved"`)) || bytes.Contains(data, []byte(`"start"`)) {
		t.Error("unsafe request")
	}
	var req Request
	if decodeStrict(data, &req) != nil || !validRequest(req) {
		t.Errorf("invalid request: %s", data)
	}
	return req
}

func TestClientProtocol(t *testing.T) {
	owner := testOwner()
	var calls atomic.Int32
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		calls.Add(1)
		if req.Owner != owner {
			t.Errorf("owner changed: %+v", req.Owner)
		}
		if req.Op != OpLookup && req.SessionID != "host-session" {
			t.Error("missing session binding")
		}
		switch req.Op {
		case OpLookup:
			writeData(t, w, LookupResponse{SessionID: "host-session"})
		case OpCapabilities:
			writeData(t, w, testCapabilities())
		case OpObserve:
			if req.ObserveRequest == nil || req.ObserveRequest.SessionID != req.SessionID || req.ObserveRequest.DisplayID != "display" || req.ObserveRequest.WindowID != "window" {
				t.Error("observe binding lost")
			}
			o := testObservation()
			o.DisplayID = "display"
			o.WindowID = "window"
			writeData(t, w, o)
		case OpExecute:
			if req.Action == nil || req.Action.ID != testAction().ID || req.Action.SessionID != req.SessionID {
				t.Error("action binding lost")
			}
			writeData(t, w, testReceipt())
		case OpPause, OpResume, OpStop:
			writeData(t, w, SessionResponse{SessionID: req.SessionID})
		default:
			t.Errorf("unexpected op %q", req.Op)
		}
	})
	ctx := context.Background()
	if id, err := client.Lookup(ctx, owner); err != nil || id != "host-session" {
		t.Fatalf("lookup: %q %v", id, err)
	}
	if _, err := client.Capabilities(ctx, owner, "host-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Observe(ctx, owner, cu.ObserveRequest{SessionID: "host-session", DisplayID: "display", WindowID: "window"}); err != nil {
		t.Fatal(err)
	}
	if receipt, err := client.Execute(ctx, owner, testAction()); err != nil || receipt.Outcome != cu.OutcomeExecuted {
		t.Fatalf("execute: %+v %v", receipt, err)
	}
	for _, control := range []func(context.Context, cu.SessionOwner, string) error{client.Pause, client.Resume, client.Stop} {
		if err := control(ctx, owner, "host-session"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 7 {
		t.Fatalf("calls: %d", calls.Load())
	}
}

func TestClientOwnerNotCachedOrRewritten(t *testing.T) {
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		writeData(t, w, LookupResponse{SessionID: fmt.Sprintf("session-%d-%d-%d", req.Owner.TenantID, req.Owner.UserID, req.Owner.SessionID)})
	})
	for _, owner := range []cu.SessionOwner{testOwner(), {TenantID: 44, UserID: 45, SessionID: 46}, {TenantID: 27, UserID: 93, SessionID: 157}} {
		t.Run(fmt.Sprint(owner), func(t *testing.T) {
			t.Parallel()
			id, err := client.Lookup(context.Background(), owner)
			if err != nil || id != fmt.Sprintf("session-%d-%d-%d", owner.TenantID, owner.UserID, owner.SessionID) {
				t.Fatalf("lookup: %q %v", id, err)
			}
		})
	}
}

func TestClientInvalidConfig(t *testing.T) {
	for _, cfg := range []Config{{}, {SocketPath: "relative", Token: testToken}, {SocketPath: "/a\x00", Token: testToken}, {SocketPath: "/a"}, {SocketPath: "/a", Token: "a\r\nb"}, {SocketPath: "/a", Token: "a b"}, {SocketPath: "/a", Token: strings.Repeat("a", maxTokenBytes+1)}, {SocketPath: "/a", Token: testToken, RequestTimeout: -1}} {
		if _, err := NewClient(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("want invalid config, got %v", err)
		}
	}
	c, err := NewClient(Config{SocketPath: "/not-required-to-exist", Token: testToken})
	if err != nil || c.http.Timeout != DefaultRequestTimeout {
		t.Fatalf("default config: %v", err)
	}
}

func TestClientRejectsLocally(t *testing.T) {
	var calls atomic.Int32
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	ctx := context.Background()
	for _, owner := range []cu.SessionOwner{{}, {TenantID: 1, UserID: 2}, {TenantID: math.MaxUint64, UserID: 2, SessionID: 3}, {TenantID: 1, UserID: math.MaxUint64, SessionID: 3}, {TenantID: 1, UserID: 2, SessionID: math.MaxUint64}} {
		if _, err := client.Lookup(ctx, owner); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("owner accepted: %+v %v", owner, err)
		}
	}
	for _, id := range []string{"", " ", " padded", strings.Repeat("x", maxIDBytes+1), "a\n"} {
		if err := client.Stop(ctx, testOwner(), id); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("ID accepted: %q", id)
		}
	}
	for _, mutate := range []func(*cu.Action){func(a *cu.Action) { a.ID = "" }, func(a *cu.Action) { a.ObservationID = "" }, func(a *cu.Action) { a.Kind = cu.ActionResume }, func(a *cu.Action) { a.Point = nil }, func(a *cu.Action) { a.Text = strings.Repeat("x", cu.MaxInputBytes+1) }} {
		a := testAction()
		mutate(&a)
		receipt, err := client.Execute(ctx, testOwner(), a)
		if !errors.Is(err, ErrInvalidRequest) || receipt.Outcome != "" {
			t.Fatalf("local rejection: %+v %v", receipt, err)
		}
	}
	req := Request{Op: OpExecute, Owner: testOwner(), SessionID: "other", Action: ptrAction(testAction())}
	if _, attempted, err := client.call(ctx, req); !errors.Is(err, ErrInvalidRequest) || attempted {
		t.Fatalf("binding rejection: %v %v", attempted, err)
	}
	req = Request{Op: OpObserve, Owner: testOwner(), SessionID: "other", ObserveRequest: &cu.ObserveRequest{SessionID: "host-session"}}
	if _, attempted, err := client.call(ctx, req); !errors.Is(err, ErrInvalidRequest) || attempted {
		t.Fatal("observe binding accepted")
	}
	if calls.Load() != 0 {
		t.Fatalf("local rejection contacted server %d times", calls.Load())
	}
}
func ptrAction(a cu.Action) *cu.Action { return &a }

func TestClientRequestBound(t *testing.T) {
	var calls atomic.Int32
	c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	a := testAction()
	a.Kind = cu.ActionType
	a.Text = strings.Repeat("\x00", cu.MaxInputBytes)
	id := strings.Repeat(`"`, maxIDBytes)
	a.ID = id
	a.SessionID = id
	a.ObservationID = id
	a.DisplayID = id
	a.WindowID = id
	a.Button = id
	a.Expected = &cu.ExpectedState{WindowID: id, Hash: id}
	receipt, err := c.Execute(context.Background(), testOwner(), a)
	if !errors.Is(err, ErrRequestTooLarge) || receipt.Outcome != "" || calls.Load() != 0 {
		t.Fatalf("request bound: %+v %v calls=%d", receipt, err, calls.Load())
	}
}

func TestClientRejectsResponses(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       error
	}{
		{"empty", "", 200, ErrInvalidResponse}, {"null", "null", 200, ErrInvalidResponse},
		{"missing data", `{}`, 200, ErrInvalidResponse}, {"null data", `{"data":null}`, 200, ErrInvalidResponse},
		{"trailing JSON", `{"data":{"session_id":"s"}} {}`, 200, ErrInvalidResponse},
		{"unknown envelope", `{"data":{"session_id":"s"},"secret":"raw-secret"}`, 200, ErrInvalidResponse},
		{"unknown data", `{"data":{"session_id":"s","secret":"raw-secret"}}`, 200, ErrInvalidResponse},
		{"duplicate", `{"data":{"session_id":"s","session_id":"other"}}`, 200, ErrInvalidResponse},
		{"alias", `{"Data":{"session_id":"s"}}`, 200, ErrInvalidResponse},
		{"wrong type", `{"data":{"session_id":3}}`, 200, ErrInvalidResponse},
		{"remote", `{"error":"raw-secret"}`, 200, ErrRemote},
		{"data and remote", `{"data":{"session_id":"s"},"error":"raw-secret"}`, 200, ErrRemote},
		{"unauthorized", "raw-secret", 401, ErrRemote}, {"server failure", "raw-secret", 500, ErrRemote},
		{"invalid lookup", `{"data":{"session_id":""}}`, 200, ErrInvalidResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			id, err := c.Lookup(context.Background(), testOwner())
			if id != "" || !errors.Is(err, tc.want) || strings.Contains(err.Error(), "raw-secret") {
				t.Fatalf("got %q %v", id, err)
			}
		})
	}
}

func TestClientResponseBounds(t *testing.T) {
	for _, knownLength := range []bool{false, true} {
		t.Run(fmt.Sprint(knownLength), func(t *testing.T) {
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
				if knownLength {
					w.Header().Set("Content-Length", fmt.Sprint(MaxResponseBytes+1))
				} else {
					w.(http.Flusher).Flush()
				}
				block := strings.Repeat(" ", 1<<16)
				for sent := 0; sent <= MaxResponseBytes; sent += len(block) {
					if _, err := io.WriteString(w, block); err != nil {
						return
					}
				}
			})
			if _, err := c.Lookup(context.Background(), testOwner()); !errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("bound: %v", err)
			}
		})
	}
	// The exact limit is accepted, including trailing JSON whitespace.
	c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		body := `{"data":{"session_id":"s"}}`
		_, _ = io.WriteString(w, body+strings.Repeat(" ", MaxResponseBytes-len(body)))
	})
	if id, err := c.Lookup(context.Background(), testOwner()); err != nil || id != "s" {
		t.Fatalf("exact bound: %q %v", id, err)
	}
}

func TestClientBindingAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
		run  func(*Client) error
	}{
		{"control session", SessionResponse{SessionID: "other"}, func(c *Client) error { return c.Stop(context.Background(), testOwner(), "host-session") }},
		{"observe session", func() cu.Observation { o := testObservation(); o.SessionID = "other"; return o }(), func(c *Client) error {
			_, err := c.Observe(context.Background(), testOwner(), cu.ObserveRequest{SessionID: "host-session"})
			return err
		}},
		{"observe ID", func() cu.Observation { o := testObservation(); o.ID = ""; return o }(), func(c *Client) error {
			_, err := c.Observe(context.Background(), testOwner(), cu.ObserveRequest{SessionID: "host-session"})
			return err
		}},
		{"capabilities", cu.Capabilities{}, func(c *Client) error {
			_, err := c.Capabilities(context.Background(), testOwner(), "host-session")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) { writeData(t, w, tc.data) })
			if err := tc.run(c); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("invalid response accepted: %v", err)
			}
		})
	}
}

func TestExecuteUnknownOnAmbiguousResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"disconnect", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}},
		{"bad JSON", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"data":"secret"`) }},
		{"wrong action", func(w http.ResponseWriter, r *http.Request) {
			receipt := testReceipt()
			receipt.ActionID = "other"
			writeData(t, w, receipt)
		}},
		{"wrong session", func(w http.ResponseWriter, r *http.Request) {
			receipt := testReceipt()
			receipt.SessionID = "other"
			writeData(t, w, receipt)
		}},
		{"invalid outcome", func(w http.ResponseWriter, r *http.Request) {
			receipt := testReceipt()
			receipt.Outcome = "invented"
			writeData(t, w, receipt)
		}},
		{"invalid verification", func(w http.ResponseWriter, r *http.Request) {
			receipt := testReceipt()
			receipt.Verification = "invented"
			writeData(t, w, receipt)
		}},
		{"server failure", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret", 500) }},
		{"contradictory error", func(w http.ResponseWriter, r *http.Request) {
			data, _ := json.Marshal(testReceipt())
			_ = json.NewEncoder(w).Encode(Response{Data: data, Error: "secret"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); tc.handler(w, r) })
			receipt, err := c.Execute(context.Background(), testOwner(), testAction())
			assertUnknown(t, receipt, err)
			if calls.Load() != 1 {
				t.Fatalf("replayed action: %d", calls.Load())
			}
		})
	}
}
func assertUnknown(t *testing.T, r cu.ActionReceipt, err error) {
	t.Helper()
	a := testAction()
	if err == nil || r.ActionID != a.ID || r.SessionID != a.SessionID || r.BeforeObservationID != a.ObservationID || r.Outcome != cu.OutcomeUnknown || r.Verification != cu.VerificationUnknown || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unknown receipt: %+v %v", r, err)
	}
}

func TestExecuteSanitizesRemoteReceipt(t *testing.T) {
	for _, outcome := range []cu.Outcome{cu.OutcomeRejected, cu.OutcomeFailed, cu.OutcomeNotStarted, cu.OutcomeUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
				receipt := testReceipt()
				receipt.Outcome = outcome
				if outcome == cu.OutcomeUnknown {
					receipt.Verification = cu.VerificationUnknown
				}
				receipt.ErrorMessage = "secret"
				receipt.ErrorCode = "secret"
				receipt.RedactedActionSummary = "secret"
				writeData(t, w, receipt)
			})
			receipt, err := c.Execute(context.Background(), testOwner(), testAction())
			if receipt.Outcome != outcome || (outcome != cu.OutcomeExecuted && !errors.Is(err, ErrRemote)) || (outcome == cu.OutcomeExecuted && err != nil) {
				t.Fatalf("outcome: %+v %v", receipt, err)
			}
			if receipt.ErrorMessage != "" || strings.Contains(receipt.ErrorCode, "secret") || strings.Contains(receipt.RedactedActionSummary, "secret") {
				t.Fatalf("untrusted error leak: %+v", receipt)
			}
		})
	}
}

func TestClientTimeoutAndCancellation(t *testing.T) {
	c := newUnixClient(t, 30*time.Millisecond, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	receipt, err := c.Execute(context.Background(), testOwner(), testAction())
	assertUnknown(t, receipt, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt, err = c.Execute(ctx, testOwner(), testAction())
	if !errors.Is(err, context.Canceled) || receipt.Outcome != "" {
		t.Fatalf("pre-dispatch cancellation: %+v %v", receipt, err)
	}
}

func TestClientNoRedirectCompressionOrReplay(t *testing.T) {
	for _, mode := range []string{"redirect", "compression"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode == "redirect" {
					w.Header().Set("Location", commandURL)
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				w.Header().Set("Content-Encoding", "gzip")
				_, _ = io.WriteString(w, `{"data":{"session_id":"s"}}`)
			})
			if _, err := c.Lookup(context.Background(), testOwner()); err == nil || calls.Load() != 1 {
				t.Fatalf("unsafe transport: %v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestExecuteRejectsSuccessWithReceiptError(t *testing.T) {
	for _, field := range []string{"code", "message"} {
		t.Run(field, func(t *testing.T) {
			c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
				receipt := testReceipt()
				if field == "code" {
					receipt.ErrorCode = "secret"
				} else {
					receipt.ErrorMessage = "secret"
				}
				writeData(t, w, receipt)
			})
			receipt, err := c.Execute(context.Background(), testOwner(), testAction())
			assertUnknown(t, receipt, err)
		})
	}
}

func TestExecutePreservesBoundFailureReceiptAlongsideError(t *testing.T) {
	c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		receipt := testReceipt()
		receipt.Outcome = cu.OutcomeRejected
		receipt.ErrorMessage = "secret"
		data, _ := json.Marshal(receipt)
		_ = json.NewEncoder(w).Encode(Response{Data: data, Error: "secret"})
	})
	receipt, err := c.Execute(context.Background(), testOwner(), testAction())
	if receipt.Outcome != cu.OutcomeRejected || receipt.ActionID != testAction().ID || !errors.Is(err, ErrRemote) || receipt.ErrorMessage != "" {
		t.Fatalf("failure acknowledgement: %+v %v", receipt, err)
	}
}

func TestExecuteFailureDoesNotPreventStopOrReplayAction(t *testing.T) {
	var executes, stops atomic.Int32
	c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		if req.Op == OpStop {
			stops.Add(1)
			writeData(t, w, SessionResponse{SessionID: req.SessionID})
			return
		}
		executes.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	})
	receipt, err := c.Execute(context.Background(), testOwner(), testAction())
	assertUnknown(t, receipt, err)
	if err := c.Stop(context.Background(), testOwner(), "host-session"); err != nil {
		t.Fatal(err)
	}
	if executes.Load() != 1 || stops.Load() != 1 {
		t.Fatalf("unexpected dispatch: execute=%d stop=%d", executes.Load(), stops.Load())
	}
}

func TestStopIndependentOfInFlightExecute(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	c := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		if req.Op == OpStop {
			writeData(t, w, SessionResponse{SessionID: req.SessionID})
			return
		}
		close(started)
		<-release
		writeData(t, w, testReceipt())
	})
	done := make(chan error, 1)
	t.Cleanup(func() { close(release); <-done })
	go func() { _, err := c.Execute(context.Background(), testOwner(), testAction()); done <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Stop(ctx, testOwner(), "host-session"); err != nil {
		t.Fatal(err)
	}
	// Stop did not queue behind the blocked input request. Cleanup releases input.
	select {
	case err := <-done:
		t.Fatalf("execute unexpectedly completed: %v", err)
	default:
	}
}

func TestClientLaunchAppBindsAllowlistedWorkBuddyWindow(t *testing.T) {
	owner := testOwner()
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		if req.Op != OpLaunchApp || req.Application != string(cu.ApplicationWorkBuddy) || req.SessionID != "host-session" {
			t.Fatalf("launch request=%+v", req)
		}
		writeData(t, w, cu.LaunchReceipt{
			Application: cu.ApplicationWorkBuddy,
			BundleID:    cu.WorkBuddyBundleID,
			Window:      cu.WindowRef{ID: "window-7", OwnerPID: 84, BundleID: cu.WorkBuddyBundleID},
			Outcome:     cu.OutcomeExecuted,
			Duration:    time.Millisecond,
			CompletedAt: time.Now(),
		})
	})
	receipt, err := client.LaunchApp(context.Background(), owner, "host-session", string(cu.ApplicationWorkBuddy))
	if err != nil || receipt.Outcome != cu.OutcomeExecuted || receipt.Window.ID != "window-7" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestClientLaunchAppPreservesControlledRemoteFailureReceipt(t *testing.T) {
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		if req.Op != OpLaunchApp {
			t.Fatalf("op=%q", req.Op)
		}
		receipt := cu.LaunchReceipt{Application: cu.ApplicationWorkBuddy, Outcome: cu.OutcomeRejected, ErrorCode: cu.ErrorCodeLaunchTimeout, Duration: time.Millisecond, CompletedAt: time.Now()}
		data, _ := json.Marshal(receipt)
		_ = json.NewEncoder(w).Encode(Response{Data: data, Error: "host operation failed"})
	})
	receipt, err := client.LaunchApp(context.Background(), testOwner(), "host-session", string(cu.ApplicationWorkBuddy))
	if err == nil || receipt.Outcome != cu.OutcomeRejected || receipt.ErrorCode != cu.ErrorCodeLaunchTimeout {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestClientExecutePreservesControlledHostErrorCode(t *testing.T) {
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		receipt := testReceipt()
		receipt.Outcome = cu.OutcomeRejected
		receipt.ErrorCode = cu.ErrorCodeSelfTarget
		writeData(t, w, receipt)
	})
	receipt, err := client.Execute(context.Background(), testOwner(), testAction())
	if err == nil || !errors.Is(err, ErrRemote) || receipt.ErrorCode != cu.ErrorCodeSelfTarget {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestClientObservePreservesControlledHostErrorCode(t *testing.T) {
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		_ = readRequest(t, r)
		_ = json.NewEncoder(w).Encode(Response{Error: "host operation failed", ErrorCode: cu.ErrorCodeFocusChanged})
	})
	_, err := client.Observe(context.Background(), testOwner(), cu.ObserveRequest{SessionID: "host-session", WindowID: "window"})
	if err == nil || !errors.Is(err, ErrRemote) {
		t.Fatalf("observe error = %v, want remote error", err)
	}
	var coded interface{ Code() string }
	if !errors.As(err, &coded) || coded.Code() != cu.ErrorCodeFocusChanged {
		t.Fatalf("observe error = %v, want controlled code %q", err, cu.ErrorCodeFocusChanged)
	}
}

func TestClientRuntimeStatusRoundTrip(t *testing.T) {
	service := &serverSpy{runtimeActive: true, runtimeState: cu.RuntimeFaulted, runtimeFault: &cu.RuntimeFault{State: cu.RuntimeFaulted, ErrorCode: cu.RuntimeFaultWorkerExited, Recoverable: false, OccurredAt: time.Now()}}
	listener := listenerTestStart(t, context.Background(), listenerTestRoot(t), service)
	client, err := NewClient(listener.Config())
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.RuntimeStatus(context.Background(), testOwner())
	if err != nil {
		t.Fatal(err)
	}
	if !status.ActiveSession || status.State != cu.RuntimeFaulted || status.Fault == nil || status.Fault.ErrorCode != cu.RuntimeFaultWorkerExited || service.calls != 1 || service.owner != testOwner() {
		t.Fatalf("status=%+v calls=%d owner=%+v", status, service.calls, service.owner)
	}
}

func TestClientStartLocalAndSnapshotRoundTrip(t *testing.T) {
	service := &serverSpy{localSnapshot: testSessionSnapshot()}
	listener := listenerTestStart(t, context.Background(), listenerTestRoot(t), service)
	client, err := NewClient(listener.Config())
	if err != nil {
		t.Fatal(err)
	}
	started, err := client.StartLocal(context.Background(), testOwner(), true)
	if err != nil {
		t.Fatal(err)
	}
	if started.SessionID != "local-session" || !service.startApproved {
		t.Fatalf("started=%+v approved=%t", started, service.startApproved)
	}
	got, err := client.Snapshot(context.Background(), testOwner(), "local-session")
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "local-session" || got.State != cu.SessionReady {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestClientStopRuntimeRoundTrip(t *testing.T) {
	service := &serverSpy{runtimeStopOK: true}
	listener := listenerTestStart(t, context.Background(), listenerTestRoot(t), service)
	client, err := NewClient(listener.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.StopRuntime(context.Background(), testOwner()); err != nil {
		t.Fatal(err)
	}
	if service.runtimeStopCalls != 1 || service.owner != testOwner() {
		t.Fatalf("calls=%d owner=%+v", service.runtimeStopCalls, service.owner)
	}
}
