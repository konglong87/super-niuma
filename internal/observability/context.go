package observability

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type contextKey string

const (
	traceIDContextKey        contextKey = "traceid"
	userIDContextKey         contextKey = "userid"
	tenantKeyContextKey      contextKey = "tenantkey"
	cliSessionIDContextKey   contextKey = "cli_session_id"
	requestPurposeContextKey contextKey = "request_purpose"

	DefaultUserID    = "anonymous"
	DefaultTenantKey = "default"
)

var (
	zapLoggerMu sync.RWMutex
	zapLogger   = mustNewZapLoggerFromEnv()
)

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDContextKey, strings.TrimSpace(traceID))
}

func WithUserID(ctx context.Context, userID string) context.Context {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		userID = DefaultUserID
	}
	return context.WithValue(ctx, userIDContextKey, userID)
}

func WithTenantKey(ctx context.Context, tenantKey string) context.Context {
	tenantKey = strings.TrimSpace(tenantKey)
	if tenantKey == "" {
		tenantKey = DefaultTenantKey
	}
	return context.WithValue(ctx, tenantKeyContextKey, tenantKey)
}

func WithCLISessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, cliSessionIDContextKey, strings.TrimSpace(sessionID))
}

func WithRequestPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, requestPurposeContextKey, strings.TrimSpace(purpose))
}

func WithRequestValues(ctx context.Context, traceID, userID, tenantKey string) context.Context {
	ctx = WithTraceID(ctx, traceID)
	ctx = WithUserID(ctx, userID)
	return WithTenantKey(ctx, tenantKey)
}

func TraceID(ctx context.Context) string {
	if value, ok := ctx.Value(traceIDContextKey).(string); ok {
		return value
	}
	return ""
}

func UserID(ctx context.Context) string {
	if value, ok := ctx.Value(userIDContextKey).(string); ok && value != "" {
		return value
	}
	return DefaultUserID
}

func TenantKey(ctx context.Context) string {
	if value, ok := ctx.Value(tenantKeyContextKey).(string); ok && value != "" {
		return value
	}
	return DefaultTenantKey
}

func CLISessionID(ctx context.Context) string {
	value, _ := ctx.Value(cliSessionIDContextKey).(string)
	return value
}

func RequestPurpose(ctx context.Context) string {
	value, _ := ctx.Value(requestPurposeContextKey).(string)
	return value
}

func LogAttrs(ctx context.Context, action, function string) []any {
	return []any{
		"traceid", TraceID(ctx),
		"userid", UserID(ctx),
		"tenantkey", TenantKey(ctx),
		"cli_session_id", CLISessionID(ctx),
		"request_purpose", RequestPurpose(ctx),
		"action", action,
		"function", function,
	}
}

func Info(ctx context.Context, logger *slog.Logger, action, function, message string, attrs ...any) {
	log(ctx, logger, zapcore.InfoLevel, action, function, message, attrs...)
}

func Debug(ctx context.Context, logger *slog.Logger, action, function, message string, attrs ...any) {
	log(ctx, logger, zapcore.DebugLevel, action, function, message, attrs...)
}

func Warn(ctx context.Context, logger *slog.Logger, action, function, message string, attrs ...any) {
	log(ctx, logger, zapcore.WarnLevel, action, function, message, attrs...)
}

func Error(ctx context.Context, logger *slog.Logger, action, function, message string, attrs ...any) {
	log(ctx, logger, zapcore.ErrorLevel, action, function, message, attrs...)
}

func Panic(ctx context.Context, logger *slog.Logger, action, function, message string, attrs ...any) {
	log(ctx, logger, zapcore.PanicLevel, action, function, message, attrs...)
	if logger != nil {
		panic(message)
	}
}

func DefaultZapLogger() *zap.Logger {
	zapLoggerMu.RLock()
	defer zapLoggerMu.RUnlock()
	return zapLogger
}

func SetDefaultZapLogger(logger *zap.Logger) func() {
	if logger == nil {
		logger = zap.NewNop()
	}
	zapLoggerMu.Lock()
	previous := zapLogger
	zapLogger = logger
	zapLoggerMu.Unlock()
	return func() {
		zapLoggerMu.Lock()
		zapLogger = previous
		zapLoggerMu.Unlock()
	}
}

func ConfigureDefaultZapLogger(level string, development bool) error {
	logger, err := NewZapLogger(level, development)
	if err != nil {
		return err
	}
	SetDefaultZapLogger(logger)
	return nil
}

func NewZapLogger(level string, development bool) (*zap.Logger, error) {
	return NewZapLoggerWithOutputPaths(level, development, []string{"stderr"})
}

func ConfigureDefaultZapLoggerWithOutputPaths(level string, development bool, outputPaths []string) (func(), error) {
	logger, err := NewZapLoggerWithOutputPaths(level, development, outputPaths)
	if err != nil {
		return nil, err
	}
	return SetDefaultZapLogger(logger), nil
}

func NewZapLoggerWithOutputPaths(level string, development bool, outputPaths []string) (*zap.Logger, error) {
	parsed, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	cfg := zap.NewProductionConfig()
	if development {
		cfg = zap.NewDevelopmentConfig()
	}
	if len(outputPaths) == 0 {
		outputPaths = []string{"stderr"}
	}
	cfg.OutputPaths = append([]string(nil), outputPaths...)
	cfg.ErrorOutputPaths = append([]string(nil), outputPaths...)
	cfg.Level = zap.NewAtomicLevelAt(parsed)
	cfg.EncoderConfig.TimeKey = "time"
	cfg.EncoderConfig.LevelKey = "level"
	cfg.EncoderConfig.MessageKey = "msg"
	cfg.EncoderConfig.CallerKey = "caller"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	return cfg.Build()
}

// NewCLILogger 建交互/CLI 路径用的 logger。与服务端的区别只有两点：
// 未显式设级别时完全静默（用户看到的应该只有那一行错误），以及
// debug 之外不带 stacktrace —— Go 调用栈对 CLI 用户是纯噪音。
func NewCLILogger() (*zap.Logger, error) {
	level, explicit := EnvLogLevel()
	if !explicit {
		return zap.NewNop(), nil
	}
	parsed, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	if parsed >= LevelSilent {
		return zap.NewNop(), nil
	}
	logger, err := NewZapLogger(level, false)
	if err != nil {
		return nil, err
	}
	if parsed > zapcore.DebugLevel {
		logger = logger.WithOptions(zap.AddStacktrace(LevelSilent))
	}
	return logger, nil
}

// ConfigureCLILogger 把默认 logger 换成 CLI logger，返回恢复函数。
func ConfigureCLILogger() (func(), error) {
	logger, err := NewCLILogger()
	if err != nil {
		return nil, err
	}
	return SetDefaultZapLogger(logger), nil
}

func log(ctx context.Context, logger *slog.Logger, level zapcore.Level, action, function, message string, attrs ...any) {
	if loggingDisabled(ctx) {
		return
	}
	if logger != nil {
		logSlog(ctx, logger, level, action, function, message, attrs...)
		return
	}
	zapLoggerMu.RLock()
	loggerZap := zapLogger
	zapLoggerMu.RUnlock()
	if loggerZap == nil {
		loggerZap = zap.NewNop()
	}
	switch level {
	case zapcore.DebugLevel:
		loggerZap.Debug(message, zapFields(ctx, action, function, attrs...)...)
	case zapcore.InfoLevel:
		loggerZap.Info(message, zapFields(ctx, action, function, attrs...)...)
	case zapcore.ErrorLevel:
		loggerZap.Error(message, zapFields(ctx, action, function, attrs...)...)
	case zapcore.PanicLevel:
		loggerZap.Panic(message, zapFields(ctx, action, function, attrs...)...)
	default:
		loggerZap.Info(message, zapFields(ctx, action, function, attrs...)...)
	}
}

func logSlog(ctx context.Context, logger *slog.Logger, level zapcore.Level, action, function, message string, attrs ...any) {
	values := append(LogAttrs(ctx, action, function), attrs...)
	switch level {
	case zapcore.DebugLevel:
		logger.DebugContext(ctx, message, values...)
	case zapcore.ErrorLevel, zapcore.PanicLevel:
		logger.ErrorContext(ctx, message, values...)
	default:
		logger.InfoContext(ctx, message, values...)
	}
}

func zapFields(ctx context.Context, action, function string, attrs ...any) []zap.Field {
	fields := []zap.Field{
		zap.String("traceid", TraceID(ctx)),
		zap.String("userid", UserID(ctx)),
		zap.String("tenantkey", TenantKey(ctx)),
		zap.String("cli_session_id", CLISessionID(ctx)),
		zap.String("request_purpose", RequestPurpose(ctx)),
		zap.String("action", action),
		zap.String("function", function),
	}
	for i := 0; i < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok || strings.TrimSpace(key) == "" {
			key = "attr"
		}
		if i+1 >= len(attrs) {
			fields = append(fields, zap.Any(key, attrs[i]))
			continue
		}
		value := attrs[i+1]
		if err, ok := value.(error); ok {
			fields = append(fields, zap.Error(err))
			if key != "error" {
				fields = append(fields, zap.String(key, err.Error()))
			}
			continue
		}
		fields = append(fields, zap.Any(key, value))
	}
	return fields
}

func mustNewZapLoggerFromEnv() *zap.Logger {
	level, _ := EnvLogLevel()
	logger, err := NewZapLogger(level, false)
	if err != nil {
		// 包初始化没法报错；无法识别的级别退回默认结构化日志，
		// 真正的报错留给 CLI 入口（ConfigureCLILogger）给出人话提示。
		logger, err = NewZapLogger("", false)
		if err != nil {
			return zap.NewNop()
		}
	}
	return logger
}
