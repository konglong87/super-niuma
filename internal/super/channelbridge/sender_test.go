package channelbridge

import (
	"context"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func senderBridge(t *testing.T, transport transportFunc, authorize func(context.Context, control.Target) error) *Bridge {
	t.Helper()
	b, err := New(Options{Accounts: func() []Account {
		return []Account{{ID: "account", Provider: control.ProviderWebhook, Enabled: true, SecretEnv: "SECRET", WebhookURL: "https://example.invalid/notice"}}
	}, Handler: handleFunc(func(context.Context, router.Envelope) (router.Decision, error) { return router.Decision{}, nil }), Getenv: func(string) string { return fixtureSecret }, HTTPClient: &http.Client{Transport: transport}, AuthorizeTarget: authorize})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func allowedTarget(context.Context, control.Target) error { return nil }
func target() control.Target {
	return control.Target{ID: "target", Provider: control.ProviderWebhook, Account: "account", Chat: "chat", Recipient: "sender"}
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestSenderUsesConfiguredTargetSignsRequestAndRequiresReceipt(t *testing.T) {
	calls := 0
	b := senderBridge(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://example.invalid/notice" || r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != "notice" || r.Header.Get(HeaderSignature) == "" {
			t.Fatal("unsafe request")
		}
		return response(http.StatusOK, `{"message_id":"remote","chat":"chat","delivered_at_ms":1}`), nil
	}, allowedTarget)
	got, err := b.Send(context.Background(), target(), "notice", "safe status")
	if err != nil || got.MessageID != "remote" || calls != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestSenderDeniedTargetsNeverReachNetwork(t *testing.T) {
	b := senderBridge(t, func(*http.Request) (*http.Response, error) { t.Fatal("unauthorized network request"); return nil, nil }, func(context.Context, control.Target) error { return control.ErrDenied })
	_, err := b.Send(context.Background(), target(), "notice", "status")
	var send *control.SendError
	if !errors.As(err, &send) || !send.DefinitelyNotAccepted || send.Retryable {
		t.Fatalf("%v", err)
	}
}
func TestSenderTimeoutRedirectMalformedAndDuplicateReceiptsAreUnknown(t *testing.T) {
	cases := []struct {
		name      string
		transport transportFunc
	}{{"timeout", func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }}, {"redirect", func(*http.Request) (*http.Response, error) {
		r := response(http.StatusFound, "")
		r.Header.Set("Location", "https://attacker.invalid/steal")
		return r, nil
	}}, {"no receipt", func(*http.Request) (*http.Response, error) { return response(http.StatusOK, `{}`), nil }}, {"wrong chat", func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"message_id":"x","chat":"other","delivered_at_ms":1}`), nil
	}}, {"duplicate", func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"message_id":"x","message_id":"forged","chat":"chat","delivered_at_ms":1}`), nil
	}}, {"trailing", func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"message_id":"x","chat":"chat","delivered_at_ms":1} {}`), nil
	}}, {"oversize", func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, strings.Repeat("x", MaxBodyBytes+1)), nil
	}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := senderBridge(t, func(r *http.Request) (*http.Response, error) { calls++; return tc.transport(r) }, allowedTarget)
			_, err := b.Send(context.Background(), target(), "notice", "status")
			var send *control.SendError
			if !errors.As(err, &send) || send.DefinitelyNotAccepted || send.Retryable || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}
func TestURLAndAddressPolicy(t *testing.T) {
	b := &Bridge{}
	for _, raw := range []string{"http://example.com/x", "https://127.0.0.1/x", "https://[::1]/x", "https://169.254.169.254/x", "https://10.0.0.1/x", "https://user:password@example.com/x", "https://example.com/x#secret", "https://example.com/x?url=other"} {
		if _, err := b.authorizedURL(raw); err == nil {
			t.Fatalf("unsafe URL %q", raw)
		}
	}
	b.options.AllowLoopback = true
	if _, err := b.authorizedURL("http://127.0.0.1:8080/notice"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1", "::1", "169.254.169.254", "10.0.0.1", "::ffff:127.0.0.1", "0.0.0.0", "ff02::1"} {
		if allowedIP(netip.MustParseAddr(address), false) {
			t.Fatalf("unsafe IP %s", address)
		}
	}
	if !allowedIP(netip.MustParseAddr("1.1.1.1"), false) {
		t.Fatal("public IP denied")
	}
}
