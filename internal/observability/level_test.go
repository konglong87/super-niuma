package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestParseLevelAcceptsEveryDocumentedName(t *testing.T) {
	cases := map[string]zapcore.Level{
		"":        zapcore.InfoLevel,
		"debug":   zapcore.DebugLevel,
		"info":    zapcore.InfoLevel,
		"warn":    zapcore.WarnLevel,
		"warning": zapcore.WarnLevel,
		"error":   zapcore.ErrorLevel,
		"dpanic":  zapcore.DPanicLevel,
		"panic":   zapcore.PanicLevel,
		// fatal 以前不被识别，静默落回 Info —— 用户想要更安静，反而拿到最吵的输出。
		"fatal":  zapcore.FatalLevel,
		"FATAL":  zapcore.FatalLevel,
		"  info": zapcore.InfoLevel,
		"silent": LevelSilent,
		"off":    LevelSilent,
		"none":   LevelSilent,
	}
	for input, want := range cases {
		got, err := ParseLevel(input)
		if err != nil {
			t.Fatalf("ParseLevel(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParseLevelRejectsUnknownName(t *testing.T) {
	_, err := ParseLevel("fatalx")
	if err == nil {
		t.Fatal("ParseLevel(\"fatalx\") must fail instead of silently falling back to info")
	}
	for _, name := range LogLevelNames {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q must list the valid level %q", err, name)
		}
	}
}

func TestLogLevelNamesAreAllParseable(t *testing.T) {
	for _, name := range LogLevelNames {
		if _, err := ParseLevel(name); err != nil {
			t.Fatalf("documented level %q is not parseable: %v", name, err)
		}
	}
}

func TestEnvLogLevelReportsWhetherItWasSet(t *testing.T) {
	for _, name := range LogLevelEnvVars {
		t.Setenv(name, "")
	}
	if value, explicit := EnvLogLevel(); explicit {
		t.Fatalf("EnvLogLevel() = (%q, true) with no env set, want explicit=false", value)
	}
	t.Setenv("LOG_LEVEL", "warn")
	if value, explicit := EnvLogLevel(); !explicit || value != "warn" {
		t.Fatalf("EnvLogLevel() = (%q, %v), want (\"warn\", true)", value, explicit)
	}
	t.Setenv("NIUMA_LOG_LEVEL", "debug")
	if value, _ := EnvLogLevel(); value != "debug" {
		t.Fatalf("NIUMA_LOG_LEVEL must win over LOG_LEVEL, got %q", value)
	}
}

// CLI 默认必须一条不写：没设级别时，连 Error 都不该出现在 stderr。
func TestNewCLILoggerIsSilentWithoutExplicitLevel(t *testing.T) {
	for _, name := range LogLevelEnvVars {
		t.Setenv(name, "")
	}
	// logger 必须在替换 os.Stderr 之后构造：zap 在 Build 时就把 stderr sink 绑死了。
	captured := captureStderr(t, func() {
		logger, err := NewCLILogger()
		if err != nil {
			t.Error(err)
			return
		}
		logger.Error("boom")
		_ = logger.Sync()
	})
	if captured != "" {
		t.Fatalf("CLI logger must stay silent by default, wrote:\n%s", captured)
	}
}

func TestNewCLILoggerHonorsExplicitLevelWithoutStacktrace(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("NIUMA_LOG_LEVEL", "error")
	captured := captureStderr(t, func() {
		logger, err := NewCLILogger()
		if err != nil {
			t.Error(err)
			return
		}
		logger.Info("chatter")
		logger.Error("boom")
		_ = logger.Sync()
	})
	if strings.Contains(captured, "chatter") {
		t.Fatalf("level=error must drop info lines, got:\n%s", captured)
	}
	if !strings.Contains(captured, "boom") {
		t.Fatalf("level=error must keep error lines, got:\n%s", captured)
	}
	// 这正是 AUDIT-P1-28 的第二半：显式设 error 之后仍然喷 Go 调用栈。
	if strings.Contains(captured, "stacktrace") {
		t.Fatalf("CLI logger must not emit stacktraces outside debug, got:\n%s", captured)
	}
}

func TestNewCLILoggerKeepsStacktraceAtDebug(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("NIUMA_LOG_LEVEL", "debug")
	captured := captureStderr(t, func() {
		logger, err := NewCLILogger()
		if err != nil {
			t.Error(err)
			return
		}
		logger.Error("boom")
		_ = logger.Sync()
	})
	if !strings.Contains(captured, "stacktrace") {
		t.Fatalf("debug level is the opt-in for full diagnostics, got:\n%s", captured)
	}
}

func TestNewCLILoggerRejectsUnknownLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("NIUMA_LOG_LEVEL", "verbose")
	if _, err := NewCLILogger(); err == nil {
		t.Fatal("an unknown log level must be reported, not silently downgraded")
	}
}

// 服务端那套结构化日志不能被 CLI 的安静化削弱：默认仍是 info + stacktrace。
func TestServerLoggerKeepsInfoAndStacktrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	logger, err := NewZapLoggerWithOutputPaths("", false, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("api request finish")
	logger.Error("api request failed")
	_ = logger.Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{`"level":"info"`, "api request finish", `"level":"error"`, "stacktrace"} {
		if !strings.Contains(out, want) {
			t.Fatalf("server logging lost %q:\n%s", want, out)
		}
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	fn()
	os.Stderr = original
	_ = writer.Close()
	out := <-done
	_ = reader.Close()
	return out
}
