# Web Agent Observability Integration Plan

## 背景

`/webui/agent` 已经能用真实 tenant storage 创建 Web Agent task、发送 message、接收 SSE 事件、展示 `message/text_delta/completed` 生命周期，并在右侧 Progress 面板显示 status、model、mode、trace 和事件流。

当前缺口是：这些信息还没有像 TUI / tenant chat 那样稳定汇入主 WebUI 的 Observability 体系。尤其是前端每次 API 请求都会自动生成新的 `X-Trace-Id`，导致 task create、message、runner、model telemetry、agent task events 之间可能被不同 trace id 分散，无法稳定按一次 Web Agent run 聚合。

## 目标

1. Web Agent 每个 task/run 有稳定的 durable trace id。
2. `create -> message -> runner -> text_delta -> completed/failed/cancelled -> telemetry` 使用同一个 trace id。
3. Web Agent runner 发出 agent 级 telemetry，主 WebUI Telemetry 和 Trace API 可以按 trace id 搜到。
4. 保留现有 `tenant_agent_tasks` / `tenant_agent_task_events` 作为 Web Agent 运行证据源。
5. 不改变已有 API 响应结构，不破坏 TUI、mobile chat、tenant chat 的可观测链路。

## 实施状态

已完成，代码提交为 `ba8d549 feat: connect web agent observability trace`。

本轮落地内容：

- Web Agent 前端在新建 task 时生成稳定 `web-agent-*` trace id，并同时写入 request body、`X-Trace-Id`、`metadata_json.trace_id` 和 `metadata_json.run_trace_id`。
- Web Agent 后续 message 请求复用 task/run trace id，避免 create request、message request、runner 和 telemetry 使用不同 trace。
- 服务端 create task 会兜底生成 durable trace id，并写回 `tenant_agent_tasks.trace_id` 与 metadata。
- 服务端 message runner 会把同一个 trace id 写入 `message/text_delta/completed/failed/cancelled` task events。
- `QueryRequest.TraceID` 作为内部字段传入 server runner，使 query/model/tool telemetry 继承 Web Agent run trace。
- runner 新增 `agent.run.started` / `agent.run.finished` telemetry，使用 `resource_type=agent_task`、`resource_id=<task_id>` 关联 task。
- `scripts/web-agent-real-e2e.sh` 增加 trace header/body、MySQL task/event/telemetry 回查和 telemetry API 搜索验证。

## 2026-07-01 验证记录

常规验证已通过：

- `go test ./... -count=1`
- `npm --prefix web test`
- `npm --prefix web run build`
- `git diff --check`

全链路验证已通过：

- 启动真实 Go server、真实 MySQL 隔离库和本地 OpenAI-compatible deterministic provider。
- 用 Chromium 打开 `/webui/agent?token=test-token`，在真实页面点击 `New Session`，填写 workspace/title/prompt，选择 `Code` mode，发送消息并等待 completed。
- 本次浏览器对话 trace：`web-agent-746a31d5-a95f-404d-837d-c18e25461b03`。
- Web Agent 页面可见 user bubble、assistant streaming 内容、completed 状态和同一个 trace。
- 主 WebUI 打开 `/webui/?token=test-token`，进入 `Observability -> Agents`，可见 task `#1`、标题 `Browser observability manual run`、状态 `completed`、同一个 trace、metadata 和 result。
- `/tenant/telemetry?search=<trace_id>` 返回 23 条事件，包括 `agent.run.started`、`agent.run.finished`、`query.run.started/finished`、`model.request.started/finished`、`model.phase.*` 和 `api.request.*`。
- MySQL 回查确认：
  - `tenant_agent_tasks.status=completed`
  - `tenant_agent_tasks.trace_id = metadata_json.run_trace_id = web-agent-746a31d5-a95f-404d-837d-c18e25461b03`
  - `tenant_agent_task_events` 同 trace 下 196 条，`COUNT(DISTINCT trace_id)=1`，包含 `message=1`、`text_delta=193`、`completed=1`
  - `tenant_telemetry_events` 同 trace 下包含 `agent.run.started`、`agent.run.finished`、`query.run.finished`、`model.request.finished`、`api.request.finished`

验证边界：

- 本轮真实浏览器验证使用本地 deterministic provider，不冒充外部真实 LLM；目标是验证 WebUI 操作、runner、MySQL、telemetry 和 Observability 链路。
- 当前 `Observability -> Telemetry` 面板默认只展示最近 30 条 telemetry，且没有 trace 搜索框；页面自身刷新会产生新的 API telemetry，可能把指定 Web Agent trace 的 `agent.run.*` 挤出可见列表。稳定验收入口是 `Observability -> Agents` 的 task/trace evidence，以及 `/tenant/telemetry?search=<trace_id>` 和 MySQL 回查。

## 当前链路

- API middleware 会记录 `api.request.started` / `api.request.finished`。
- `tenant_agent_tasks.trace_id` 会落库；没有显式 trace id 时 repository 回退到当前 request trace id。
- `tenant_agent_task_events.trace_id` 会落库；没有显式 trace id 时 repository 回退到当前 request trace id。
- Web Agent message runner 写入 `message`、`text_delta`、`completed/failed/cancelled` 事件。
- Web Agent Progress 面板读取 task/events 并展示当前 task trace。

## 缺口

- Web Agent 前端 create 和 message 是不同 HTTP request，默认生成不同 `X-Trace-Id`。
- message body 通常不传 `trace_id`，runner 使用的 `input.TraceID` 可能为空或只等于 message request trace，而不是 task trace。
- runner 没有专门发 `agent.run.started` / `agent.run.finished` telemetry。
- completed result 中没有 `trace_id`，DB 回查时需要跨 task/event 表才能拼出运行 trace。
- 主 Observability 已有 trace/telemetry 基础，但 Web Agent task 还未形成稳定的一次 run 关联键。

## 设计

### Trace ID 规则

- 创建 task 时，服务端确定一个 durable task trace id：
  - 优先使用请求 body 的 `trace_id`。
  - 否则使用当前 request context 的 trace id。
  - 如果仍为空，生成 `web-agent-<timestamp>`。
- 服务端把该 trace id 写回 task：
  - `tenant_agent_tasks.trace_id`
  - `metadata_json.run_trace_id`
- Web Agent 前端在新建会话时生成 `web-agent-*` trace id，并同时写入：
  - request body `trace_id`
  - request header `X-Trace-Id`
  - `metadata_json.trace_id`
  - `metadata_json.run_trace_id`
- Web Agent 后续 message 请求复用同一个 run trace id，同时写入 body `trace_id` 和 `X-Trace-Id`，避免 API telemetry 与 runner telemetry 分裂。
- 后续 message 处理时统一使用 `agentTaskRunTraceID(task, input.TraceID, ctx)`：
  - 优先 task.trace_id。
  - 再 fallback 到 `metadata_json.run_trace_id`。
  - 再 fallback 到 message body `trace_id`。
  - 再 fallback 到 request context trace id。
- runner goroutine 使用 `observability.WithTraceID(runCtx, runTraceID)`，确保 query/model/tool telemetry 继承同一 trace id。

### Telemetry 规则

Web Agent runner 增加 agent 级 telemetry：

- `agent.run.started`
  - category: `agent`
  - source: `server.runAgentTaskMessage`
  - status: `started`
  - resource_type: `agent_task`
  - resource_id: task id
  - model、trace_id、cwd、prompt_mode、permission_mode、effort

- `agent.run.finished`
  - status: `ok/error/blocked`
  - duration_ms
  - input/output tokens
  - model、tool_calls、context_percent
  - error/cancelled/timeout when applicable

这些 telemetry 会通过 request context 中的 tenant telemetry recorder 落到 `tenant_telemetry_events`，并能被 `/tenant/telemetry?search=<trace_id>` 查到。

### Result/Event 规则

- `message`、`text_delta`、`completed/failed/cancelled` 事件统一写入 run trace id。
- completed/failed/cancelled payload 写入 `trace_id`。
- completed payload 保留 token、context、transcript_path、duration 等现有字段。

## UI 接入

本轮优先不改主 WebUI 大布局，只保证现有 Observability 能按 trace id 查到 Web Agent evidence：

- Web Agent Progress 继续显示 task trace。
- 主 WebUI Telemetry 可搜索该 trace id。
- `api.request.*`、`agent.run.*`、query/model/tool telemetry 使用同一个 trace id。
- Trace API 能在 telemetry span 中看到 `agent.run`。

后续可以把 Web Agent task 列表作为 Observability 的一等入口，支持从 task id 直接打开 trace/timeline。

## 验收

单元测试：

- 创建 Web Agent task 时会回填 `trace_id` 和 `metadata_json.run_trace_id`。
- message 未传 `trace_id` 时复用 task trace id。
- runner 写入 message/text_delta/completed 的 trace id 一致。
- runner 发出 `agent.run.started` 和 `agent.run.finished` telemetry。
- invalid prompt mode 回退 chat 的行为不变。

真实全链路：

- 启动真实 server + MySQL。
- 通过真实浏览器打开 `/webui/agent`，新建 code mode 会话并发送消息。
- DB 回查：
  - `tenant_agent_tasks.trace_id` 非空。
  - `metadata_json.run_trace_id` 等于 task trace id。
  - `tenant_agent_task_events` 同 task 的事件 trace id 全部一致。
  - `tenant_telemetry_events` 能按 trace id 查到 `agent.run.started` / `agent.run.finished`。
- Web Agent 页面 Progress 显示该 trace 和 completed 状态。
- 移动端/桌面无 console error。
