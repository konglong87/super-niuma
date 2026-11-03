package observability

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap/zapcore"
)

// LevelSilent 高于所有真实级别，用它建的 logger 一条也不写。
// CLI 在用户没有显式开启诊断时用的就是这个级别。
const LevelSilent = zapcore.FatalLevel + 1

// LogLevelEnvVars 是被识别的日志级别环境变量，按优先级排列。
var LogLevelEnvVars = []string{"NIUMA_LOG_LEVEL", "LOG_LEVEL"}

// LogLevelNames 是 ParseLevel 接受的全部拼写，用于错误消息和文档。
var LogLevelNames = []string{"debug", "info", "warn", "error", "dpanic", "panic", "fatal", "silent"}

// ParseLevel 把级别名解析成 zapcore.Level。空串沿用 Info（服务端默认），
// 无法识别的名字必须报错 —— 旧实现悄悄落回 Info，导致 `=fatal` 这类合法级别
// 反而喷出最吵的输出。
func ParseLevel(level string) (zapcore.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "":
		return zapcore.InfoLevel, nil
	case "debug":
		return zapcore.DebugLevel, nil
	case "info":
		return zapcore.InfoLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	case "dpanic":
		return zapcore.DPanicLevel, nil
	case "panic":
		return zapcore.PanicLevel, nil
	case "fatal":
		return zapcore.FatalLevel, nil
	case "silent", "off", "none":
		return LevelSilent, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("unknown log level %q: must be one of %s", level, strings.Join(LogLevelNames, ", "))
	}
}

// EnvLogLevel 返回用户显式设置的日志级别，以及是否设置过。
// "是否设置过" 决定 CLI 走安静模式还是诊断模式，不能只看解析结果。
func EnvLogLevel() (string, bool) {
	for _, name := range LogLevelEnvVars {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, true
		}
	}
	return "", false
}
