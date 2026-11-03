package observability

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestValuesDefaultsAndOverrides(t *testing.T) {
	ctx := WithRequestValues(context.Background(), "trace-1", "", "")
	if TraceID(ctx) != "trace-1" {
		t.Fatalf("trace = %q", TraceID(ctx))
	}
	if UserID(ctx) != DefaultUserID {
		t.Fatalf("user = %q", UserID(ctx))
	}
	if TenantKey(ctx) != DefaultTenantKey {
		t.Fatalf("tenant = %q", TenantKey(ctx))
	}

	ctx = WithRequestValues(context.Background(), "trace-2", "user-2", "yutang")
	if TraceID(ctx) != "trace-2" || UserID(ctx) != "user-2" || TenantKey(ctx) != "yutang" {
		t.Fatalf("ctx values trace=%q user=%q tenant=%q", TraceID(ctx), UserID(ctx), TenantKey(ctx))
	}
}

func TestCLISessionIDAndRequestPurposeContext(t *testing.T) {
	ctx := WithRequestValues(context.Background(), "trace-1", "user-1", "tenant-1")
	ctx = WithCLISessionID(ctx, " session-uuid ")
	ctx = WithRequestPurpose(ctx, " recap.away ")
	if CLISessionID(ctx) != "session-uuid" || RequestPurpose(ctx) != "recap.away" {
		t.Fatalf("session=%q purpose=%q", CLISessionID(ctx), RequestPurpose(ctx))
	}
	if TraceID(ctx) != "trace-1" || UserID(ctx) != "user-1" || TenantKey(ctx) != "tenant-1" {
		t.Fatalf("request context was overwritten")
	}
}

func TestInfoWritesCLISessionFields(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	ctx := WithCLISessionID(context.Background(), "session-uuid")
	ctx = WithRequestPurpose(ctx, "main")
	Info(ctx, logger, "action", "function", "message")
	if text := out.String(); !strings.Contains(text, `"cli_session_id":"session-uuid"`) || !strings.Contains(text, `"request_purpose":"main"`) {
		t.Fatalf("log = %s", text)
	}
}

func TestInfoWritesStandardFields(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	ctx := WithRequestValues(context.Background(), "trace-1", "user-1", "aihe")
	Info(ctx, logger, "action-a", "pkg.fn", "hello", "extra", "value")
	text := out.String()
	for _, want := range []string{
		`"traceid":"trace-1"`,
		`"userid":"user-1"`,
		`"tenantkey":"aihe"`,
		`"action":"action-a"`,
		`"function":"pkg.fn"`,
		`"extra":"value"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %s: %s", want, text)
		}
	}
}

func TestLevelHelpersWriteExpectedLevels(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := WithRequestValues(context.Background(), "trace-1", "user-1", "aihe")

	Debug(ctx, logger, "action-debug", "pkg.debug", "debug message")
	Error(ctx, logger, "action-error", "pkg.error", "error message", "error", "boom")

	text := out.String()
	for _, want := range []string{
		`"level":"DEBUG"`,
		`"msg":"debug message"`,
		`"action":"action-debug"`,
		`"level":"ERROR"`,
		`"msg":"error message"`,
		`"error":"boom"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %s: %s", want, text)
		}
	}
}

func TestZapLoggerWithOutputPathsWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	logger, err := NewZapLoggerWithOutputPaths("debug", false, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	restore := SetDefaultZapLogger(logger)
	defer restore()
	defer func() { _ = logger.Sync() }()

	ctx := WithRequestValues(context.Background(), "trace-file", "user-file", "tenant-file")
	Debug(ctx, nil, "action-file", "pkg.file", "debug file message")
	_ = logger.Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"level":"debug"`,
		`"msg":"debug file message"`,
		`"traceid":"trace-file"`,
		`"userid":"user-file"`,
		`"tenantkey":"tenant-file"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("zap file log missing %s: %s", want, text)
		}
	}
}

func TestPanicWritesLogAndPanics(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	ctx := WithRequestValues(context.Background(), "trace-1", "user-1", "aihe")

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("expected panic")
		}
		text := out.String()
		for _, want := range []string{
			`"level":"ERROR"`,
			`"msg":"panic message"`,
			`"action":"action-panic"`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("panic log missing %s: %s", want, text)
			}
		}
	}()
	Panic(ctx, logger, "action-panic", "pkg.panic", "panic message")
}
