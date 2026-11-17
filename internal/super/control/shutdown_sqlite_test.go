package control_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

// serveUntil runs the engine until started fires, then performs the graceful
// shutdown the Desktop does on quit (cancel the Serve context and wait).
func serveUntil(t *testing.T, h *sqliteHarness, started <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.engine.Serve(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("work never started")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteIntegrationShutdownMidModelCallIsRetryable(t *testing.T) {
	h := newSQLiteHarness(t)
	started := make(chan struct{}, 1)
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		if err := r.Budget.Reserve(ctx, r.AttemptID+".request", 40, 10); err != nil {
			return execution.Result{}, err
		}
		started <- struct{}{}
		<-ctx.Done()
		return execution.Result{UsageKnown: false}, ctx.Err()
	})
	r := h.start(t, "shutdown-call", h.grant())
	serveUntil(t, h, started)
	got := h.get(t, r.ID)
	if got.Status != control.Failed || got.SafeCode != control.SafeInterrupted || got.Jobs[0].SafeCode != control.SafeInterrupted || control.ReservedTokens(got) != 50 {
		t.Fatalf("status=%s code=%s job=%s budget=%d", got.Status, got.SafeCode, got.Jobs[0].SafeCode, control.ReservedTokens(got))
	}
	h.engine.Executor = sqliteExecutor(sqliteWork)
	if _, err := h.engine.Retry(context.Background(), h.principal, r.ID, got.Version); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		h.step(t, "after-restart")
	}
	sqliteAssertReceipt(t, h, h.get(t, r.ID))
}

type blockingVerifier struct{ started chan struct{} }

func (v blockingVerifier) Verify(ctx context.Context, _ string, _ verification.Policy) (verification.Report, error) {
	v.started <- struct{}{}
	<-ctx.Done()
	return verification.Report{}, ctx.Err()
}

func TestSQLiteIntegrationShutdownMidVerificationReverifies(t *testing.T) {
	h := newSQLiteHarness(t)
	started := make(chan struct{}, 1)
	h.engine.Verifier = blockingVerifier{started: started}
	r := h.start(t, "shutdown-verify", h.grant())
	serveUntil(t, h, started)
	got := h.get(t, r.ID)
	if got.Status != control.Verifying || got.VerificationClaim.Token != "" {
		t.Fatalf("interrupted verification status=%s claim=%v", got.Status, got.VerificationClaim.Token != "")
	}
	h.engine.Verifier = control.FileVerifier{}
	for i := 0; i < 3; i++ {
		h.step(t, "after-restart")
	}
	sqliteAssertReceipt(t, h, h.get(t, r.ID))
}

type interruptedSender struct {
	started chan struct{}
	inner   *sqliteSender
	blocked bool
}

func (s *interruptedSender) Send(ctx context.Context, target control.Target, id, text string) (control.Receipt, error) {
	if !s.blocked {
		s.blocked = true
		s.started <- struct{}{}
		<-ctx.Done()
		return control.DesktopSender{}.Send(ctx, control.Target{ID: target.ID, Provider: control.ProviderDesktop}, id, text)
	}
	return s.inner.Send(ctx, target, id, text)
}

func TestSQLiteIntegrationShutdownBeforeDesktopReceiptRetries(t *testing.T) {
	h := newSQLiteHarness(t)
	started := make(chan struct{}, 1)
	h.engine.Sender = &interruptedSender{started: started, inner: h.sender}
	r := h.start(t, "shutdown-send", h.grant())
	serveUntil(t, h, started)
	got := h.get(t, r.ID)
	if got.Status != control.DeliveryPending || len(got.Notices) != 1 || got.Notices[0].Status != control.DeliveryRetry || got.Notices[0].SafeCode != control.SafeInterrupted {
		t.Fatalf("status=%s notices=%+v", got.Status, got.Notices)
	}
	time.Sleep(time.Until(time.UnixMilli(got.Notices[0].RetryAtMS)))
	h.step(t, "after-restart")
	sqliteAssertReceipt(t, h, h.get(t, r.ID))
}

func TestDesktopSenderCancelledIsDefinitelyNotAccepted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := control.DesktopSender{}.Send(ctx, control.Target{ID: "desktop", Provider: control.ProviderDesktop}, "n", "text")
	var typed *control.SendError
	if !errors.As(err, &typed) || !typed.Retryable || !typed.DefinitelyNotAccepted {
		t.Fatalf("cancelled desktop send must be retryable and not accepted: %v", err)
	}
}
