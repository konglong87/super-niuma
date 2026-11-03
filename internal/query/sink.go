package query

import (
	"context"

	"github.com/konglong87/super-niuma/internal/compact"
)

// EventSink 是查询执行事件的统一出口，cli/tui/server 各自实现一次，
// 替代在每个入口手写 RunCallbacks 适配。
type EventSink interface {
	OnThinking(ctx context.Context, text string) error
	OnToolCall(ctx context.Context, event ToolCallEvent) error
	OnToolResult(ctx context.Context, trace ToolTrace) error
	OnUsage(ctx context.Context, turn int, usage Usage) error
	OnMessageStop(ctx context.Context, turn int, stopReason string, usage Usage) error
	OnCompact(ctx context.Context, result compact.Result) error
}

// TextAmendSink 是 EventSink 的可选扩展：实现它的 sink（如 TUI）能在接受文本
// 与已直播文本不一致时收到修正/撤回信号；不实现的 sink 保持原样。
type TextAmendSink interface {
	OnTextAmended(ctx context.Context, streamed, final string) error
}

// SinkCallbacks 把 EventSink 适配为 RunWithCallbacks 所需的 RunCallbacks。
func SinkCallbacks(ctx context.Context, sink EventSink) RunCallbacks {
	cb := textAmendCallback(ctx, sink)
	return RunCallbacks{
		OnTextAmended: cb,
		OnThinking:    func(text string) error { return sink.OnThinking(ctx, text) },
		OnToolCall:    func(event ToolCallEvent) error { return sink.OnToolCall(ctx, event) },
		OnToolResult:  func(trace ToolTrace) error { return sink.OnToolResult(ctx, trace) },
		OnUsage:       func(turn int, usage Usage) error { return sink.OnUsage(ctx, turn, usage) },
		OnMessageStop: func(turn int, stopReason string, usage Usage) error {
			return sink.OnMessageStop(ctx, turn, stopReason, usage)
		},
		OnCompact: func(result compact.Result) error { return sink.OnCompact(ctx, result) },
	}
}

func textAmendCallback(ctx context.Context, sink EventSink) func(streamed, final string) error {
	amender, ok := sink.(TextAmendSink)
	if !ok {
		return nil
	}
	return func(streamed, final string) error {
		return amender.OnTextAmended(ctx, streamed, final)
	}
}
