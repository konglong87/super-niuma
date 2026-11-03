package channelbridge

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type handleFunc func(context.Context, router.Envelope) (router.Decision, error)

func (f handleFunc) Handle(ctx context.Context, e router.Envelope) (router.Decision, error) {
	return f(ctx, e)
}

const fixtureSecret = "test-only-32-byte-webhook-secret-0123456789"

var fixtureNow = time.Unix(1800000000, 0)

func webhookFixture(t *testing.T, handler InboundHandler) *Bridge {
	t.Helper()
	b, err := New(Options{Accounts: func() []Account {
		return []Account{{ID: "account", Provider: control.ProviderWebhook, Enabled: true, SecretEnv: "TEST_SECRET"}}
	}, Handler: handler, Getenv: func(string) string { return fixtureSecret }, Now: func() time.Time { return fixtureNow }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func body(text string) []byte {
	data, _ := json.Marshal(map[string]string{"event_id": "event", "message_id": "message", "chat": "chat", "thread": "", "sender": "sender", "text": text})
	return data
}
func signedRequest(payload []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://test"+WebhookPrefix+"account", bytes.NewReader(payload))
	timestamp := strconv.FormatInt(fixtureNow.Unix(), 10)
	r.Header.Set(HeaderVersion, ProtocolVersion)
	r.Header.Set(HeaderAccount, "account")
	r.Header.Set(HeaderKeyID, "account")
	r.Header.Set(HeaderTimestamp, timestamp)
	r.Header.Set(HeaderNonce, "nonce")
	r.Header.Set(HeaderSignature, hex.EncodeToString(signatureMAC(fixtureSecret, http.MethodPost, r.URL.Path, "account", "account", timestamp, "nonce", payload)))
	return r
}
func TestWebhookAuthenticatesBeforeTrustAndRoutesOnlySafeDTO(t *testing.T) {
	calls := 0
	b := webhookFixture(t, handleFunc(func(_ context.Context, e router.Envelope) (router.Decision, error) {
		calls++
		if !e.Authenticated || !e.ThreadKnown || !e.Mentioned || e.Account != "account" || e.Provider != control.ProviderWebhook || e.Nonce != "nonce" || e.Bot || e.Self {
			t.Fatalf("authority=%+v", e)
		}
		return router.Decision{Action: router.ActionRun, Code: router.CodeAccepted, RunID: "run"}, nil
	}))
	recorder := httptest.NewRecorder()
	b.WebhookHandler().ServeHTTP(recorder, signedRequest(body("/run private objective")))
	if recorder.Code != http.StatusOK || calls != 1 || strings.Contains(recorder.Body.String(), "private objective") || strings.Contains(recorder.Body.String(), fixtureSecret) {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}
func TestWebhookRejectsSignatureReplayInputsBeforeHandler(t *testing.T) {
	tests := []struct {
		name   string
		change func(*http.Request)
	}{{"unsigned", func(r *http.Request) { r.Header.Del(HeaderSignature) }}, {"body tamper", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(string(body("/cancel other")))) }}, {"account", func(r *http.Request) { r.Header.Set(HeaderAccount, "other") }}, {"key", func(r *http.Request) { r.Header.Set(HeaderKeyID, "other") }}, {"method", func(r *http.Request) { r.Method = http.MethodPut }}, {"path", func(r *http.Request) { r.URL.Path = WebhookPrefix + "other" }}, {"query", func(r *http.Request) { r.URL.RawQuery = "account=other" }}, {"encoded path", func(r *http.Request) { r.URL.RawPath = WebhookPrefix + "%61ccount" }}, {"old", func(r *http.Request) {
		r.Header.Set(HeaderTimestamp, strconv.FormatInt(fixtureNow.Add(-SignatureWindow-time.Second).Unix(), 10))
	}}, {"future", func(r *http.Request) {
		r.Header.Set(HeaderTimestamp, strconv.FormatInt(fixtureNow.Add(SignatureWindow+time.Second).Unix(), 10))
	}}, {"duplicate header", func(r *http.Request) { r.Header.Add(HeaderNonce, "nonce") }}, {"nonce separator", func(r *http.Request) { r.Header.Set(HeaderNonce, "nonce\nvalue") }}, {"nonce too long", func(r *http.Request) { r.Header.Set(HeaderNonce, strings.Repeat("a", 129)) }}, {"malformed hex", func(r *http.Request) { r.Header.Set(HeaderSignature, strings.Repeat("z", 64)) }}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := webhookFixture(t, handleFunc(func(context.Context, router.Envelope) (router.Decision, error) {
				t.Fatal("unauthenticated handler")
				return router.Decision{}, nil
			}))
			r := signedRequest(body("/run safe"))
			tc.change(r)
			w := httptest.NewRecorder()
			b.WebhookHandler().ServeHTTP(w, r)
			if w.Code < 400 {
				t.Fatalf("accepted %d", w.Code)
			}
		})
	}
}
func TestWebhookRejectsTrustedFlagsDuplicateKeysTrailingAndOversize(t *testing.T) {
	base := string(body("/run safe"))
	for _, payload := range []string{strings.TrimSuffix(base, "}") + `,"Authenticated":true}`, strings.TrimSuffix(base, "}") + `,"scope":"victim"}`, strings.TrimSuffix(base, "}") + `,"origin":"system"}`, strings.TrimSuffix(base, "}") + `,"event_id":"another"}`, base + ` {}`, strings.Replace(base, `,"thread":""`, "", 1), strings.Repeat("a", MaxBodyBytes+1)} {
		b := webhookFixture(t, handleFunc(func(context.Context, router.Envelope) (router.Decision, error) {
			t.Fatal("invalid DTO reached handler")
			return router.Decision{}, nil
		}))
		w := httptest.NewRecorder()
		b.WebhookHandler().ServeHTTP(w, signedRequest([]byte(payload)))
		if w.Code < 400 {
			t.Fatalf("bad DTO accepted: %d", w.Code)
		}
	}
}
func TestWebhookRotatedSecretAndDisabledAccountFailClosed(t *testing.T) {
	calls := 0
	secret := fixtureSecret
	enabled := true
	b, err := New(Options{Accounts: func() []Account {
		return []Account{{ID: "account", Provider: control.ProviderWebhook, Enabled: enabled, SecretEnv: "SECRET"}}
	}, Handler: handleFunc(func(context.Context, router.Envelope) (router.Decision, error) {
		calls++
		return router.Decision{Code: router.CodeAccepted, Action: router.ActionRun}, nil
	}), Getenv: func(string) string { return secret }, Now: func() time.Time { return fixtureNow }})
	if err != nil {
		t.Fatal(err)
	}
	secret = fixtureSecret + "rotated"
	w := httptest.NewRecorder()
	b.WebhookHandler().ServeHTTP(w, signedRequest(body("/run x")))
	if w.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatal("old key accepted")
	}
	secret = fixtureSecret
	enabled = false
	w = httptest.NewRecorder()
	b.WebhookHandler().ServeHTTP(w, signedRequest(body("/run x")))
	if w.Code != http.StatusForbidden || calls != 0 {
		t.Fatal("disabled account accepted")
	}
}
func TestWebhookRejectsAfterClose(t *testing.T) {
	b := webhookFixture(t, handleFunc(func(context.Context, router.Envelope) (router.Decision, error) {
		t.Fatal("closed handler")
		return router.Decision{}, nil
	}))
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	b.WebhookHandler().ServeHTTP(w, signedRequest(body("/run x")))
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
}
