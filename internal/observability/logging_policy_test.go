package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const loggingPolicyIterations = 100

func TestWithoutLoggingPreservesParentAndContextValues(t *testing.T) {
	parent, cancel := context.WithTimeout(WithTraceID(context.Background(), "parent-trace"), time.Minute)
	defer cancel()
	silent := WithoutLogging(parent)
	child := WithRequestPurpose(WithoutLogging(silent), "restricted-runtime")
	if loggingDisabled(parent) || loggingDisabled(context.Background()) || loggingDisabled(nil) {
		t.Fatal("unmarked context was silenced")
	}
	if !loggingDisabled(silent) || !loggingDisabled(child) {
		t.Fatal("policy was not inherited by descendants")
	}
	if TraceID(child) != "parent-trace" || RequestPurpose(child) != "restricted-runtime" {
		t.Fatal("policy lost parent context values")
	}
	if deadline, ok := parent.Deadline(); ok {
		if got, exists := child.Deadline(); !exists || !got.Equal(deadline) {
			t.Fatal("policy lost deadline")
		}
	}
	cancel()
	if child.Err() != context.Canceled {
		t.Fatal("policy lost parent cancellation")
	}
}

func TestWithoutLoggingConcurrentZapAndSlogIsolation(t *testing.T) {
	for _, backend := range []string{"zap", "slog"} {
		t.Run(backend, func(t *testing.T) {
			core, capture := observer.New(zapcore.DebugLevel)
			loggerZap := zap.New(core)
			restore := SetDefaultZapLogger(loggerZap)
			defer restore()
			var out bytes.Buffer
			var loggerSlog *slog.Logger
			if backend == "slog" {
				loggerSlog = slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
			}
			parent := WithTraceID(context.Background(), "shared-parent")
			silent := WithRequestPurpose(WithoutLogging(parent), "silent-child")
			normal := WithRequestPurpose(parent, "normal-child")
			writes := []func(context.Context, *slog.Logger, string, string, string, ...any){Debug, Info, Warn, Error}
			var wg sync.WaitGroup
			start := make(chan struct{})
			for _, request := range []struct {
				ctx   context.Context
				label string
			}{{silent, "silent-sentinel"}, {normal, "normal-sentinel"}} {
				wg.Add(1)
				go func(ctx context.Context, label string) {
					defer wg.Done()
					<-start
					for range loggingPolicyIterations {
						for _, write := range writes {
							write(ctx, loggerSlog, "policy-test", "concurrent-context", label, "request", label)
						}
					}
				}(request.ctx, request.label)
			}
			close(start)
			wg.Wait()
			if DefaultZapLogger() != loggerZap {
				t.Fatal("request policy replaced the global logger")
			}
			expected := loggingPolicyIterations * len(writes)
			if backend == "zap" {
				if capture.Len() != expected {
					t.Fatalf("captured %d entries, want %d normal-only entries", capture.Len(), expected)
				}
				for _, entry := range capture.All() {
					if entry.Message != "normal-sentinel" || entry.ContextMap()["request"] != "normal-sentinel" {
						t.Fatalf("silent log emitted: %+v", entry)
					}
				}
			} else {
				if capture.Len() != 0 {
					t.Fatal("explicit slog calls were redirected to the global logger")
				}
				if got := strings.Count(out.String(), `"msg":"normal-sentinel"`); got != expected {
					t.Fatalf("slog normal entries=%d want=%d", got, expected)
				}
				if strings.Contains(out.String(), "silent-sentinel") {
					t.Fatal("silent explicit slog request was emitted")
				}
			}
			// Parent and unrelated contexts remain live after the silent request ends.
			Info(parent, nil, "policy-test", "parent", "parent-still-logs")
			if capture.FilterMessage("parent-still-logs").Len() != 1 {
				t.Fatal("policy leaked into parent context")
			}
		})
	}
}

type forbiddenLogValue struct{}

func (forbiddenLogValue) Error() string { panic("silent error must never be formatted") }

func TestWithoutLoggingDropsBeforeFormattingOrDispatch(t *testing.T) {
	core, capture := observer.New(zapcore.DebugLevel)
	restore := SetDefaultZapLogger(zap.New(core))
	defer restore()
	var out bytes.Buffer
	explicit := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := WithoutLogging(context.Background())
	for _, logger := range []*slog.Logger{nil, explicit} {
		for _, write := range []func(context.Context, *slog.Logger, string, string, string, ...any){Debug, Info, Warn, Error} {
			write(ctx, logger, "secret-action", "secret-function", "secret-message", "error", forbiddenLogValue{})
		}
	}
	if capture.Len() != 0 || out.Len() != 0 {
		t.Fatal("silent logging reached a backend")
	}
}
