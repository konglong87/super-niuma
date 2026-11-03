package server

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

// backgroundPanicStackLimit 限制记录的调用栈长度：栈只含函数名与 file:line，
// 不含请求数据，但仍需截断以免撑爆遥测载荷。
const backgroundPanicStackLimit = 4000

// goSafe 启动一个脱离请求生命周期的后台 goroutine。gin.Recovery 只覆盖 HTTP
// 请求链，覆盖不到这里，所以任何 panic 必须在本 goroutine 内部收住，否则会打死
// 整个进程。source 用调用方的 "包.函数" 名，props 只放标识性字段（ID、名称），
// 禁止放 prompt 正文、响应正文或 token。
func goSafe(ctx context.Context, source string, props map[string]any, fn func()) {
	go func() {
		defer recoverBackgroundPanic(ctx, source, props)
		fn()
	}()
}

// recoverBackgroundPanic 以 defer 调用：吞掉 panic 并留下可排查的记录。
func recoverBackgroundPanic(ctx context.Context, source string, props map[string]any) {
	rec := recover()
	if rec == nil {
		return
	}
	reason, stack := describeBackgroundPanic(rec)
	logAttrs := []any{"panic", reason, "stack", stack}
	for key, value := range props {
		logAttrs = append(logAttrs, key, value)
	}
	observability.Error(ctx, nil, "server.background.panic", source, "background goroutine panicked", logAttrs...)
	emitBackgroundPanic(ctx, source, reason, stack, props)
}

// describeBackgroundPanic 把 recover 值和当前调用栈整理成可记录的字符串。
func describeBackgroundPanic(rec any) (reason string, stack string) {
	return fmt.Sprintf("panic: %v", rec), truncateAgentTaskEventText(string(debug.Stack()), backgroundPanicStackLimit)
}

// emitBackgroundPanic 把 panic 记成遥测事件。telemetry.Emit 会走
// SanitizeProperties，properties 里的敏感键（prompt / content / token 等）会被丢弃；
// 这里也只传标识性字段，不依赖脱敏兜底。
func emitBackgroundPanic(ctx context.Context, source, reason, stack string, props map[string]any) {
	properties := make(map[string]any, len(props)+2)
	for key, value := range props {
		properties[key] = value
	}
	properties["goroutine"] = source
	properties["stack"] = stack
	telemetry.Emit(ctx, telemetry.Event{
		Name:       "server.background.panic",
		Category:   telemetry.CategorySystem,
		Source:     source,
		Status:     telemetry.StatusError,
		Error:      reason,
		Properties: properties,
	})
}
