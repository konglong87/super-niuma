package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/query"
)

type cleanupComputerBridge struct {
	runtimeComputerBridge
	stopCalls      int
	stoppedOwner   cu.SessionOwner
	stoppedSession string
	stopContextErr error
	stopDeadline   time.Time
	stopErr        error
	blockStop      bool
	approved       bool
	revocations    int
	actions        int
	afterLookup    func()
	grants         map[string]bool
}

func (b *cleanupComputerBridge) Lookup(ctx context.Context, owner cu.SessionOwner) (string, error) {
	id, err := b.runtimeComputerBridge.Lookup(ctx, owner)
	if b.afterLookup != nil {
		b.afterLookup()
	}
	return id, err
}

func (b *cleanupComputerBridge) Stop(ctx context.Context, owner cu.SessionOwner, id string) error {
	b.stopCalls++
	b.stoppedOwner, b.stoppedSession = owner, id
	b.stopContextErr = ctx.Err()
	b.stopDeadline, _ = ctx.Deadline()
	if b.blockStop {
		<-ctx.Done()
		return ctx.Err()
	}
	if b.stopErr != nil {
		return b.stopErr
	}
	if b.grants != nil {
		if _, ok := b.grants[id]; !ok {
			return errors.New("original grant no longer available")
		}
		b.grants[id] = false
	}
	if b.approved && id == computerRuntimeHostSession {
		b.approved = false
		b.revocations++
	}
	return nil
}

func (b *cleanupComputerBridge) Execute(context.Context, cu.SessionOwner, cu.Action) (cu.ActionReceipt, error) {
	b.actions++
	return cu.ActionReceipt{}, errors.New("expired observation")
}

func cleanupComputerOptions(t *testing.T, bridge *cleanupComputerBridge, providerURL string) options {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	cfg := computerRuntimeConfig()
	cfg.Settings.Provider, cfg.Settings.BaseURL, cfg.Settings.APIKey = cfg.Provider, providerURL, "test-only"
	if err := config.SaveGlobalSettings(cfg.Settings); err != nil {
		t.Fatal(err)
	}
	var screenshot bytes.Buffer
	if err := png.Encode(&screenshot, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	bridge.sessionID, bridge.image, bridge.approved = computerRuntimeHostSession, screenshot.Bytes(), true
	opts := computerRuntimeOptions(bridge)
	opts.cwd, opts.model, opts.maxTurns, opts.noPersistence = t.TempDir(), computerRuntimeModel, 4, true
	opts.toolsSpecified, opts.enabledTools, opts.permissionMode = true, []string{"ComputerUse"}, "allow"
	return opts
}

func writeComputerCleanupToolStream(w http.ResponseWriter, input string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_cleanup\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n", computerRuntimeModel)
	_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_cleanup\",\"name\":\"ComputerUse\",\"input\":{}}}\n\n")
	_, _ = fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\n", input)
	_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
}

// Match runServerQuery's deferred cleanup around both Run entry points. The
// provider exercises real parsing/tool dispatch; only the trusted host is fake.
func TestDesktopComputerUseCleanupFinalExits(t *testing.T) {
	for _, tc := range []struct {
		name                                                           string
		providerFail, cancel, maxTurns, modelStop, stopFail, callbacks bool
		wantError                                                      string
	}{
		{name: "model neglects stop"},
		{name: "provider invalid JSON after rejected hotkey", providerFail: true, wantError: "unexpected end of JSON input"},
		{name: "provider failure with callbacks", providerFail: true, callbacks: true, wantError: "unexpected end of JSON input"},
		{name: "canceled request", cancel: true, wantError: "context canceled"},
		{name: "max turns", maxTurns: true, wantError: "max turns reached"},
		{name: "model stop then cleanup", modelStop: true},
		{name: "stop failure preserves provider failure", providerFail: true, stopFail: true, wantError: "unexpected end of JSON input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge := &cleanupComputerBridge{}
			if tc.stopFail {
				bridge.stopErr = errors.New("host stop unavailable")
			}
			var requests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				switch {
				case tc.modelStop && n == 1:
					writeComputerCleanupToolStream(w, fmt.Sprintf(`{"action":"stop","session_id":%q}`, computerRuntimeHostSession))
				case tc.modelStop:
					writeAnthropicTextStream(t, w, "stopped")
				case n == 1:
					writeComputerObserveStream(w)
				case tc.providerFail && n == 2:
					writeComputerCleanupToolStream(w, fmt.Sprintf(`{"action":"hotkey","session_id":%q,"observation_id":%q,"keys":["META","A"]}`, computerRuntimeHostSession, computerRuntimeObservation))
				case tc.providerFail:
					writeComputerCleanupToolStream(w, `{"action":`)
				case tc.cancel:
					cancel()
				default:
					writeAnthropicTextStream(t, w, "done without stop")
				}
			}))
			defer provider.Close()
			opts := cleanupComputerOptions(t, bridge, provider.URL)
			if tc.maxTurns {
				opts.maxTurns = 1
			}
			result, runErr := func() (query.Result, error) {
				session, cleanup, err := newQuerySession(ctx, opts, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				if tc.callbacks {
					return session.RunWithCallbacks(ctx, "Observe the desktop.", io.Discard, query.RunCallbacks{})
				}
				return session.Run(ctx, "Observe the desktop.", io.Discard)
			}()
			if tc.wantError == "" {
				if runErr != nil {
					t.Fatalf("run error: %v", runErr)
				}
			} else if runErr == nil || !strings.Contains(runErr.Error(), tc.wantError) {
				t.Fatalf("result=%+v error=%v, want %q", result, runErr, tc.wantError)
			}
			if tc.cancel && !errors.Is(runErr, context.Canceled) {
				t.Fatalf("lost cancellation: %v", runErr)
			}
			if tc.providerFail && (requests.Load() != 3 || bridge.actions != 1) {
				t.Fatalf("requests=%d actions=%d; expected observe/rejected hotkey/invalid JSON", requests.Load(), bridge.actions)
			}
			wantStops := 1
			if tc.modelStop {
				wantStops++
			}
			if bridge.stopCalls != wantStops {
				t.Fatalf("Stop calls=%d want=%d", bridge.stopCalls, wantStops)
			}
			assertComputerCleanupBinding(t, bridge)
			if !tc.stopFail && (bridge.approved || bridge.revocations != 1) {
				t.Fatalf("approval=%v revocations=%d", bridge.approved, bridge.revocations)
			}
		})
	}
}

func assertComputerCleanupBinding(t *testing.T, bridge *cleanupComputerBridge) {
	t.Helper()
	if len(bridge.owners) != 1 || bridge.stoppedOwner != computerRuntimeOwner || bridge.stoppedSession != computerRuntimeHostSession {
		t.Fatalf("lost original binding: lookups=%v stop owner=%+v session=%q", bridge.owners, bridge.stoppedOwner, bridge.stoppedSession)
	}
	if bridge.stopContextErr != nil || bridge.stopDeadline.IsZero() {
		t.Fatalf("cleanup must have a live bounded context: err=%v deadline=%v", bridge.stopContextErr, bridge.stopDeadline)
	}
}

func TestDesktopComputerUseCleanupKeepsBindingAndIsOnce(t *testing.T) {
	const replacementSession = "different-new-approval"
	for _, oldGrantRetained := range []bool{true, false} {
		t.Run(fmt.Sprintf("old_grant_retained=%t", oldGrantRetained), func(t *testing.T) {
			bridge := &cleanupComputerBridge{}
			opts := cleanupComputerOptions(t, bridge, "http://unused.invalid")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, cleanup, err := newQuerySession(ctx, opts, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			// A new host grant replaces Lookup's answer before the old run ends.
			// Test both retained old controllers and an adapter that has forgotten it.
			bridge.sessionID = replacementSession
			bridge.grants = map[string]bool{replacementSession: true}
			if oldGrantRetained {
				bridge.grants[computerRuntimeHostSession] = true
			}
			cancel()
			cleanup()
			cleanup()
			if bridge.stopCalls != 1 {
				t.Fatalf("Stop calls=%d", bridge.stopCalls)
			}
			assertComputerCleanupBinding(t, bridge)
			if !bridge.grants[replacementSession] || bridge.grants[computerRuntimeHostSession] {
				t.Fatalf("cleanup must leave replacement approval intact: grants=%v", bridge.grants)
			}
		})
	}
}

func TestDesktopComputerUseCleanupCapturesServiceAndOwner(t *testing.T) {
	bridge := &cleanupComputerBridge{runtimeComputerBridge: runtimeComputerBridge{sessionID: computerRuntimeHostSession}, approved: true}
	replacement := &cleanupComputerBridge{approved: true}
	opts := computerRuntimeOptions(bridge)
	_, cleanup := configureDesktopComputerUse(context.Background(), computerRuntimeConfig(), computerRuntimeModel, &opts)
	opts.desktopComputerBridge, opts.computerUseService = replacement, replacement
	opts.tenantID, opts.tenantUserID, opts.tenantSessionID = 101, 102, 103
	cleanup()
	assertComputerCleanupBinding(t, bridge)
	if bridge.approved || replacement.stopCalls != 0 || !replacement.approved {
		t.Fatal("cleanup followed a replacement service/owner instead of the acquired binding")
	}
}

func TestDesktopComputerUseCleanupConstructionFailure(t *testing.T) {
	bridge := &cleanupComputerBridge{}
	opts := cleanupComputerOptions(t, bridge, "http://unused.invalid")
	opts.jsonSchema = "{"
	_, _, err := newQuerySession(context.Background(), opts, nil, nil)
	if err == nil {
		t.Fatal("expected invalid schema construction failure")
	}
	if bridge.stopCalls != 1 || bridge.approved {
		t.Fatalf("Stop calls=%d approved=%v", bridge.stopCalls, bridge.approved)
	}
	assertComputerCleanupBinding(t, bridge)
}

func TestDesktopComputerUseCleanupBoundedWhenStopBlocks(t *testing.T) {
	bridge := &cleanupComputerBridge{blockStop: true}
	opts := cleanupComputerOptions(t, bridge, "http://unused.invalid")
	_, cleanup, err := newQuerySession(context.Background(), opts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		cleanup()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(computerUseCleanupTimeout + 3*time.Second):
		t.Fatal("cleanup did not bound the blocking Stop")
	}
	if bridge.stopCalls != 1 {
		t.Fatalf("Stop calls=%d", bridge.stopCalls)
	}
	assertComputerCleanupBinding(t, bridge)
	if time.Now().Before(bridge.stopDeadline) {
		t.Fatal("blocking Stop did not wait for cleanup deadline")
	}
}

func TestDesktopComputerUseCleanupCanceledDuringLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge := &cleanupComputerBridge{
		runtimeComputerBridge: runtimeComputerBridge{sessionID: computerRuntimeHostSession},
		afterLookup:           cancel,
		approved:              true,
	}
	opts := computerRuntimeOptions(bridge)
	guidance, cleanup := configureDesktopComputerUse(ctx, computerRuntimeConfig(), computerRuntimeModel, &opts)
	if guidance != "" || opts.computerUseService != nil || opts.computerUseProfile {
		t.Fatal("canceled lookup must not enable tools")
	}
	cleanup()
	if bridge.approved || bridge.stopCalls != 1 {
		t.Fatalf("successful lookup raced cancellation: approved=%v stops=%d", bridge.approved, bridge.stopCalls)
	}
	assertComputerCleanupBinding(t, bridge)
}

func TestDesktopComputerUseCleanupDoesNotAcquireAuthority(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mutate       func(*options, *config.Config, *cleanupComputerBridge)
		wantGuidance bool
	}{
		{name: "no bridge", mutate: func(o *options, _ *config.Config, _ *cleanupComputerBridge) { o.desktopComputerBridge = nil }},
		{name: "missing owner", mutate: func(o *options, _ *config.Config, _ *cleanupComputerBridge) { o.tenantUserID = 0 }},
		{name: "tools disabled", mutate: func(o *options, _ *config.Config, _ *cleanupComputerBridge) { o.disableTools = true }},
		{name: "image route denied", wantGuidance: true, mutate: func(_ *options, c *config.Config, _ *cleanupComputerBridge) { c.Settings.ComputerUse = nil }},
		{name: "lookup denied", mutate: func(_ *options, _ *config.Config, b *cleanupComputerBridge) {
			b.lookupErr = errors.New("approval required")
		}},
		{name: "empty session", mutate: func(_ *options, _ *config.Config, b *cleanupComputerBridge) { b.sessionID = " " }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &cleanupComputerBridge{runtimeComputerBridge: runtimeComputerBridge{sessionID: computerRuntimeHostSession}}
			opts, cfg := computerRuntimeOptions(bridge), computerRuntimeConfig()
			tc.mutate(&opts, &cfg, bridge)
			guidance, cleanup := configureDesktopComputerUse(context.Background(), cfg, computerRuntimeModel, &opts)
			cleanup()
			if (guidance != "") != tc.wantGuidance || opts.computerUseService != nil || bridge.stopCalls != 0 {
				t.Fatalf("denied lookup acquired cleanup authority: guidance=%q stops=%d", guidance, bridge.stopCalls)
			}
		})
	}
}
