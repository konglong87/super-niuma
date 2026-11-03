package runtimecompose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
)

func TestRedactorKnownSecretsAndShapes(t *testing.T) {
	var r Redactor
	r.Register("short-secret", "short-secret-long", "private\nmultiline\ncredential", "")
	for _, secret := range []string{"short-secret-long", "short-secret", "private\nmultiline\ncredential", "sk-unconfiguredCredential999", "github_pat_abcdef0123456789", "ghp_abcdefghij123456", "AKIAABCDEFGHIJKLMNOP", "xoxb-1234-abcdef", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig123", "Bearer unknownBearer123", "Basic abcdefg123=", `"apiKey":"unknownApiKey"`, `auth_token = 'unknown token'`, `password=hunter2123`, `https://username:private@host.example`, "-----BEGIN RSA PRIVATE KEY-----\nABC123\nXYZ789\n-----END RSA PRIVATE KEY-----"} {
		got := r.Redact("before " + secret + " after")
		if strings.Contains(got, secret) || !strings.Contains(got, Redacted) {
			t.Errorf("secret not scrubbed: input=%q output=%q", secret, got)
		}
	}
	if got := r.Redact("do not redact ordinary prose"); got != "do not redact ordinary prose" {
		t.Fatalf("ordinary prose changed=%q", got)
	}
	if got := r.Redact("short-secret-long"); got != Redacted {
		t.Fatalf("overlapping secret suffix leaked=%q", got)
	}
	if got := r.Redact(r.Redact(`api_key="short-secret"`)); got != r.Redact(`api_key="short-secret"`) {
		t.Fatalf("redaction not idempotent=%q", got)
	}
}
func TestSafeWriterBoundsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &safeWriter{ctx: ctx}
	n, err := w.Write([]byte(strings.Repeat("x", workflow.MaxOutputBytes)))
	if err != nil || n != workflow.MaxOutputBytes {
		t.Fatalf("n=%d err=%v", n, err)
	}
	_, err = w.Write([]byte("x"))
	requireCode(t, err, CodeBudget)
	cancel()
	_, err = w.Write([]byte("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
