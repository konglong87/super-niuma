# Trace Viewer WebUI

Trace Viewer 是内置的全链路日志可视化页面，用于查看本地 TUI/CLI transcript 和 API Server tenant session 的执行链路。

## 入口

启动 API Server：

```bash
go run ./cmd/golang-cc server --host 127.0.0.1 --port 8080 --auth-token test-token
```

打开 WebUI：

```text
http://127.0.0.1:8080/trace?token=test-token
```

也可以使用 Bearer token：

```bash
curl -sS 'http://127.0.0.1:8080/trace/api/sessions?source=local' \
  -H 'Authorization: Bearer test-token'
```

## 页面能力

- `Local`：读取本地 transcript session。golang-cc 默认来源为 `GOLANG_CC_TRANSCRIPT_PROJECTS_DIR`、`GOLANG_CC_CONFIG_DIR/projects` 或 `~/.golang-cc/projects/.../*.jsonl`；Trace Viewer 还会以只读兼容方式读取 `CLAUDE_CONFIG_DIR/projects` 下的原版/native transcript，便于诊断，但这不改变 CLI/TUI code mode 的默认写入与 resume namespace。
- `Tenant`：读取 API Server / Mobile / OpenAI-compatible tenant session，底层复用 tenant timeline 聚合链路。
- `Time`：在左侧 session 列表按时间筛选，支持 `All`、`Today`、`24h`、`7d`、`30d` 和自定义 `from/to`。筛选只影响 session 列表；选中 session 后，右侧仍展示该 session 的完整 Conversation、Event Stream 和 Runtime Spans。
- `Overview`：展示 session 标题、来源、skills、tokens、prompt cache、turns、工具数、模型耗时和工具耗时。
- `Conversation`：按 user / assistant 消息展示完整对话内容。
- `Rewind`：展示 transcript 中的 checkpoint、可回退 user message、checkpoint 后续事件数、file change 数，以及已经执行过的 rewind 事件。
- `Event Stream`：展示 message、tool call/result、usage、permission、file change、checkpoint、compact、recap、audit、telemetry、sub-agent task 等事件；支持 All/Recap-only/Errors-only 筛选。`recap_summary` 会以独立样式展示，invalidated marker 会显示 `status=invalidated`，用于说明 rewind 后旧 recap 已失效。
- `Runtime Spans`：展示由 started/finished telemetry 或本地 transcript 时间戳估算出的 model/tool/API/sub-agent 耗时。本地 trace detail 会为缺少 request trace id 的 transcript event 补充稳定 `local:{session_id}`，因此 WebUI 的 Trace 时序视图可以按同一条本地会话展示 message、usage 和 tool 事件。
- `Span Tree`：`/trace/api/sessions/{id}` 会在 `spans` 上返回 `parent_id`、`sequence`、`depth`、`self_duration_ms`，并额外返回 `span_tree`。tenant trace 会把 `api.request`、`mobile.chat.stream`、`query.run`、`model.request`、`model.phase.*`、tool/sub-agent 等 span 按 trace id 和时间包含关系组成调用树，便于判断一次请求耗时集中在哪个父阶段或叶子阶段。
- `Inspector`：点击事件后以浮层查看结构化 payload，不默认占用主阅读区。

## 实现位置

后端和数据归一化逻辑在：

```text
internal/server/trace.go
```

WebUI 模板在：

```text
internal/server/trace_viewer.html
```

`trace_viewer.html` 通过 Go `embed` 编译进二进制：

```go
//go:embed trace_viewer.html
var traceHTML string
```

这样部署时不需要额外复制静态文件，同时避免把大段 HTML/CSS/JS 放在 Go raw string 中，便于编辑、审阅和后续拆分。

## API

- `GET /trace`：返回内置 WebUI。
- `GET /trace/api/sessions?source=local&limit=100`：列出本地 transcript session。
- `GET /trace/api/sessions/{session_id}?source=local`：读取本地 session normalized trace。
- `GET /trace/api/sessions?source=tenant&limit=100`：列出当前 tenant/user session。
- `GET /trace/api/sessions/{id}?source=tenant&limit=200&trace_limit=100&task_limit=500`：读取 tenant session normalized trace。

detail 响应中的关键 trace 字段：

| 字段 | 说明 |
| --- | --- |
| `spans[].sequence` | 当前 session 内按时间排序后的稳定序号。 |
| `spans[].parent_id` | 后端推断出的父 span，例如 `model.phase.stream.create` 通常挂在 `model.request` 下。 |
| `spans[].depth` | 调用树层级，用于 WebUI 缩进展示。 |
| `spans[].duration_ms` | 当前 span 总耗时。 |
| `spans[].self_duration_ms` | 扣除直接子 span 覆盖区间后的自身耗时，用于判断父阶段剩余时间是否还缺更细埋点。 |
| `span_tree` | 由同一批 spans 构造出的树形结构，供前端和后续报告直接消费。 |
| `rewind.checkpoints[]` | 从 transcript checkpoint 归一化出的可回退锚点，包含关联 message id、message preview、后续事件数和 file change 数。 |
| `rewind.rewinds[]` | 已执行过的 rewind 事件，用于审计会话何时回退到哪个 checkpoint/message。 |

针对模型流式请求，真实 provider 链路中已记录 `model.phase.request.build.finished`、`model.phase.http.request_send.finished`、`model.phase.http.write_request.finished`、`model.phase.http.wait_first_response_byte.finished`、`model.phase.http.stream_ready.finished`、`model.phase.stream.create.finished`、`model.phase.stream.first_event.finished`、`model.phase.stream.first_delta.finished`、`model.phase.stream.read.finished` 等事件。排查 `stream.create` 慢时，应优先看其子/相邻 phase：DNS、连接/TLS、写请求、等待首字节、首 delta 和 stream read 哪个阶段占比最高。

`/trace/api/sessions` 支持时间筛选：

- `from`：RFC3339 起始时间，例如 `2026-06-16T00:00:00+08:00`。
- `to`：RFC3339 结束时间，例如 `2026-06-16T23:59:59+08:00`。

本地 transcript 使用文件 `updated_at` 过滤；tenant session 优先使用 `updated_at`/`last_message_at`，缺失时回退到 `started_at`。WebUI 会把当前时间筛选写入 URL query，刷新页面后仍可恢复。

独立 WebUI 的 Observability / Local Trace tab 还会读取 `/status.workspace`，把 workspace 转为本地 transcript project slug 后默认筛选当前项目；如果 `/status` 不可用，则回退为按最新 local session 推断项目。Local Trace tab 额外支持标题、prompt、session id、项目路径搜索，以及 All/Today/24h/7d 时间范围筛选。

完整 API 说明见 [api_server.md](../api_server.md#trace-viewer)。

## 安全边界

- `/trace` 和 `/trace/api/...` 使用 API Server token 鉴权，支持 query `token` 或 `Authorization: Bearer ...`。
- `source=tenant` 依赖 tenant/user 上下文，tenant timeline 仍按 owner/admin 权限检查。
- telemetry properties 继续使用统一脱敏逻辑，不展示 API key、JWT、完整 prompt、附件私有 URL 等敏感字段。
- recap trace event 只展示 transcript 中已有的 recap preview 和 metadata；`session.recap.*` telemetry 只展示模型、耗时、mode、长度、错误等元数据，不记录完整生成 prompt。

## 已知限制

- 新会话的 local/tenant Trace 已覆盖 query、model、provider、tool、hook、permission、compact、gate、persistence 和 renderer 原生 span；旧 transcript 或尚未迁移的旧埋点继续按 trace id、时间范围和类型优先级推断，因此历史耗时精度低于新数据。
- artifact 导出以最新一次 query run 为边界，不代表整个多轮 resume session；仓内 baseline 区分 runtime completion 与 verified signal，真实任务成功率仍由外部 APG 判断。
- WebUI 当前以内联 CSS/JS 的单 HTML 模板维护；如果页面继续变复杂，可再拆分为独立 CSS/JS 并继续通过 `embed.FS` 打包。
