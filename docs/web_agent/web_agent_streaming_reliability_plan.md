# Web Agent 流式可靠性彻底修复方案

本文档记录 2026-07-01 真机测试发现的 `/webui/agent` 长回复截断问题，并给出可落地的架构修复方案。目标不是临时调大 `limit`，而是把 Web Agent 的实时输出链路改成可恢复、可回放、可观测、长回复不截断的稳定协议。

## 1. 背景与目标

### 1.1 真实问题

真机测试中，Web Agent 页面出现“AI 回复只显示一半”的现象。针对 task `15` 的数据库和接口排查结果：

- `tenant_agent_tasks.status=completed`，不是 `failed` 或 timeout。
- `stop_reason=end_turn`，模型正常结束。
- `duration_ms=84178`，运行约 84 秒，低于默认 20 分钟总超时。
- `output_tokens=2247`，`result_json.response` 已保存完整回复，长度约 5222 字符。
- `tenant_agent_task_events` 共 1832 条事件，其中 `text_delta=1829`，`completed=1`。
- `/tenant/agent-tasks/15/events?limit=200` 只返回最早 200 条事件，最后停在早期 `text_delta`，不包含 `completed`。
- `/tenant/agent-tasks/15/events/stream?limit=200` 也只输出最早 200 条事件，不包含 `completed`。

结论：问题不是模型没生成完，也不是 runner 超时，而是 Web Agent 的事件读取协议被 `ORDER BY created_at ASC LIMIT N` 截断。长回复产生的 delta 数量超过 N 后，前端和 SSE 都无法看到后续事件。

### 1.2 为什么 TUI 没有这个问题

TUI 与 runner 在同一个本地进程内，实时路径接近：

```text
model / runner stream -> TUI renderer
```

TUI 直接消费模型 delta 并追加到屏幕，不依赖数据库列表回放，也没有 `/events?limit=200` 这种截断点。

Web Agent 是浏览器页面，不能直接访问本地 runner 内存对象、工具执行上下文、权限状态和本机 stdout。Web 形态必须有 Go server 作为本地后端桥接层。但当前实现把数据库事件表同时当成“实时流通道”和“历史回放源”，路径变成：

```text
model / runner stream -> tenant_agent_task_events -> list/SSE -> browser
```

这层不是必须以 DB events 为实时通道存在。更合理的 Web 架构是：

```text
model / runner stream
  -> live broker -> SSE/WebSocket -> browser
  -> persistence writer -> tenant_agent_task_events
  -> final result -> tenant_agent_tasks.result_json
```

Web 必须有后端桥接，但实时输出不应该依赖“最早 N 条 DB events”列表查询。

### 1.3 本期目标

1. 长回复、慢回复、多工具回复都必须完整显示，不能只显示前 N 条 delta。
2. 实时体验接近 TUI：runner 产生 delta 后尽快推给页面。
3. 浏览器刷新、SSE 断线、手机切后台、网络抖动后可以从上次 event id 继续补齐。
4. task completed 后，即使事件流漏掉部分 delta，也能用 `result_json.response` 恢复完整 assistant message。
5. `tenant_agent_task_events` 继续作为审计、trace、历史回放和 E2E 证据源。
6. 保持现有 task/message/result API 兼容，避免破坏现有 Web Agent 页面、Observability 和 E2E 脚本。

### 1.4 不做范围

- 不把浏览器前端改成直连模型 provider。
- 不移除 `tenant_agent_tasks` / `tenant_agent_task_events`。
- 不改变 TUI 的 stream-json 路径。
- 不在本期重做 Web Agent 整体 UI 布局。
- 不把 Web Agent 与 `/webui/` 主控制台合并成一个页面。

## 2. 架构原则

### 2.1 后端桥接层必须存在

浏览器不能直接持有本地 agent runtime：

- 浏览器没有本地文件系统和 shell 工具执行权限。
- 浏览器不能安全持有 provider key、tenant context、sandbox/permission 状态。
- Web 页面刷新后内存状态会丢失，需要 server 和 MySQL 保留 durable state。
- 手机真机访问依赖 LAN HTTP/SSE，不能直接进入 TUI 进程内存。

因此 Web Agent 的后端桥接层必须存在。

### 2.2 DB 不是实时流的唯一通道

数据库适合做 durable evidence，不适合做低延迟 token stream 的唯一来源：

- 长回复会产生上千条 tiny delta，列表查询天然需要分页。
- `LIMIT` 是必要的安全保护，不能无限放大。
- 轮询 DB 会有延迟和压力。
- 如果查询只支持 “first N events”，SSE 会永远卡在 first N。

正确模型是“双写双读”：

- 实时读：browser 从 live broker 收到当前 runner 的 delta。
- 恢复读：browser 用 `after_id` 从 DB 补漏。
- 最终兜底：browser 从 task result 获取完整 response。

### 2.3 事件必须有游标语义

所有 Web Agent event 读取都必须遵守：

- event id 单调递增。
- 客户端保存 `last_seen_event_id`。
- 服务端支持 `after_id`。
- 返回顺序固定为 `id ASC`。
- SSE 关闭前必须 drain 完 `id > last_seen_event_id` 的剩余事件。

不能再用“取最早 200 条再靠 mergeEvents 合并”的方式承载长回复。

## 3. 目标架构

### 3.1 总体链路

```text
                     +------------------------------+
                     | tenant_agent_tasks           |
                     | - status                     |
                     | - result_json.response       |
                     | - trace_id                   |
                     +---------------^--------------+
                                     |
                                     | final result
                                     |
+-------------+      delta      +----+-------------------+
| model/query | --------------> | agent task run sink     |
| runner      |                 | - append live broker    |
+-------------+                 | - batch persist events  |
                                | - update final result   |
                                +----+---------------+----+
                                     |               |
                       live publish  |               | durable events
                                     v               v
                            +--------+---+   +-------+-------------------+
                            | live broker|   | tenant_agent_task_events  |
                            | in-memory  |   | id, task_id, event_type   |
                            | per task   |   | payload_json, trace_id    |
                            +-----+------+   +------------+--------------+
                                  |                       ^
                                  | SSE/WebSocket         | after_id replay
                                  v                       |
                            +-----+-----------------------+----+
                            | browser Web Agent page           |
                            | - live render                    |
                            | - reconnect after_id             |
                            | - final response fallback         |
                            +-----------------------------------+
```

### 3.2 Live Broker 职责

新增 server 内部组件，建议包边界先放在 `internal/server` 或独立 `internal/agentstream`，不要直接耦合 React 前端。

职责：

- 按 `task_id` 管理订阅者。
- 接收 runner sink 产出的 event。
- 将 event 立即广播给 SSE 连接。
- 保留短窗口 ring buffer，用于极短暂重连补发。
- 不承担 durable storage，server 重启后依赖 DB replay。
- task 终态后关闭对应订阅，但必须允许客户端先 drain DB。

建议接口：

```go
type AgentTaskEventPublisher interface {
    Publish(ctx context.Context, event mysqlstore.AgentTaskEvent)
    Subscribe(taskID uint64, afterID uint64) AgentTaskSubscription
}

type AgentTaskSubscription interface {
    Events() <-chan mysqlstore.AgentTaskEvent
    Close()
}
```

注意：如果实现成本较高，第一阶段可以不先做 broker，而是先把 SSE 改为 `after_id` 增量 DB 查询。彻底目标仍然是 broker + DB replay。

### 3.3 Persistence Writer 职责

当前 `agentTaskTextSink.Write` 每收到一个小 delta 就直接 `AppendAgentTaskEvent`。这会导致一条 5000 字回复产生 1800+ 行 event。

建议改为聚合写入：

- 按时间窗口 flush：例如 50-100ms。
- 按字符阈值 flush：例如 200-500 chars。
- 遇到换行、段落边界或 code fence 可提前 flush。
- completed/failed/cancelled 事件必须立即写入。

聚合后事件数会大幅下降，同时仍保留足够细的打字机体验。

## 4. 数据模型与 MySQL 设计

### 4.1 现有表保留

继续使用：

- `tenant_agent_tasks`
- `tenant_agent_task_events`

不需要为了本问题新增主表。

### 4.2 索引要求

当前核心查询会变为：

```sql
SELECT id, task_id, event_type, payload_json, trace_id, created_at
FROM tenant_agent_task_events
WHERE tenant_id = ?
  AND user_id = ?
  AND task_id = ?
  AND id > ?
ORDER BY id ASC
LIMIT ?;
```

需要确认或新增组合索引：

```sql
CREATE INDEX idx_agent_task_events_tenant_user_task_id
ON tenant_agent_task_events (tenant_id, user_id, task_id, id);
```

如果当前 migration 已有等价索引，不重复新增；如果没有，走正式 migration。

### 4.3 Result 兜底字段

`tenant_agent_tasks.result_json` 已有 `response`，继续作为完整 assistant 回复的最终来源。

completed event payload 也应包含：

- `response`
- `trace_id`
- `duration_ms`
- `stop_reason`
- `input_tokens`
- `output_tokens`
- `total_tokens`
- `context_length`
- `context_percent`

UI 判断事件不完整时，可以用 task result 或 completed event response 覆盖/补齐 assistant message。

### 4.4 Event payload 兼容

保留现有 `text_delta` payload：

```json
{"source":"runner","content":"..."}
```

新增字段必须向后兼容。例如允许：

```json
{"source":"runner","content":"...","seq":123,"aggregated":true}
```

前端不能依赖 `seq` 才能显示；`id ASC` 是主顺序。

## 5. API 设计

### 5.1 列表回放接口

现有接口：

```http
GET /tenant/agent-tasks/:id/events?limit=200
```

新增可选参数：

```http
GET /tenant/agent-tasks/:id/events?after_id=1124&limit=200
```

语义：

- `after_id` 为空或 0：从最早事件开始返回。
- `after_id > 0`：只返回 `id > after_id` 的事件。
- 固定 `ORDER BY id ASC`。
- `limit` 保持保护上限，默认 100，最大 500 可保留。
- 响应结构保持兼容：

```json
{"data":[...]}
```

可选增强：

```json
{
  "data": [...],
  "next_after_id": 2756,
  "has_more": false
}
```

为避免破坏客户端，第一阶段可以只返回 `data`，前端通过 `items.length === limit` 决定是否继续拉。

### 5.2 SSE 增量流接口

现有接口：

```http
GET /tenant/agent-tasks/:id/events/stream?limit=200
```

新增：

```http
GET /tenant/agent-tasks/:id/events/stream?after_id=1124&limit=200
```

SSE 语义：

1. 先发送 `connected`，包含当前 task id 和 server 当前已知状态。
2. 从 DB replay `id > after_id` 的历史事件，直到追上。
3. 订阅 live broker，继续发送新事件。
4. 如果 broker 不存在或 server 重启后没有 live state，则继续用 DB polling 增量补齐。
5. 发现 task 已终态时，必须再次 drain DB 中当前已可见的 `id > last_sent_id` 事件，
   确保 terminal event 或 task result 已经可读。
6. 完成这次 terminal/result drain 后即可关闭 SSE；之后异步追加的 `next_steps` 不属于
   终态关闭的必需 drain，由客户端按 5.4 的 bounded REST readback best-effort 获取。

终态关闭前必须保证：

- 已发送 completed/failed/cancelled 事件，或
- task result 已可通过 task detail API 读取。

### 5.3 Task detail 接口兜底

现有：

```http
GET /tenant/agent-tasks/:id
```

前端在以下场景必须读取 task detail：

- SSE `onDone`。
- running watchdog 发现无事件超过阈值。
- 页面刷新初始加载。
- events 回放没有 terminal event，但 task status 已 terminal。

如果 `task.status=completed` 且 `result_json.response` 非空，前端必须保证最终 assistant message 使用完整 response。

### 5.4 Completed-first 与可选的 next_steps

Web Agent 的终态协议是 **completed-first**：runner 先写入 `completed` 事件并调用
`FinishAgentTask`，随后才异步生成并追加 `next_steps`。当建议任务已被接收时，
`completed` payload 带有 `next_steps_status: "pending"`；这只是提示，不是把任务
保持在 running，也不应阻塞最终回复。

因此客户端必须把 `next_steps` 当作可选尾部事件处理：

- SSE 看到 `completed` 后允许立即关闭；服务端不会为了等待建议而延长连接。
- 已关闭 SSE 的客户端可在有限窗口内用 `GET /events?after_id=<completed_id>` 做一次
  bounded readback，读取迟到的 `next_steps`，并保持原有 cursor 语义（只返回更大的 id）。
- 不支持 readback 的旧客户端仍可只使用 `completed` 的完整 `result_json.response`，不因
  缺少建议而失败或重试。
- 服务关闭时取消 next-steps worker；取消中的建议不会改变已经完成的 task 状态。

本协议变更直接触达现有 `RT-BOUNDARY`、`RT-ENTRY`、`RT-OUTPUT` 节点；`RT-MODEL`、`RT-PERSIST`、
`RT-OBSERVE` 是受影响的下游协议链路。爆炸半径为 `B4_PROTOCOL`；拓扑 registry 保持
不变。正收益是终态可快速交付、建议生成故障与断线可隔离；代价是建议可能丢失，
客户端需要把 readback 视为 best-effort。

## 6. 后端分层技术设计

### 6.1 Model / Constant

新增或确认：

- `after_id` query 参数解析。
- event terminal 类型集合：`completed`、`failed`、`cancelled`。
- stream reconnect metadata：`last_event_id` / `after_id`。

不新增 task status。

### 6.2 Repository

新增方法，避免改坏现有调用方：

```go
ListAgentTaskEventsAfter(ctx context.Context, tenantID, userID, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
```

实现：

- `WHERE tenant_id=? AND user_id=? AND task_id=? AND id > ?`
- `ORDER BY id ASC`
- `LIMIT normalizeLimit(limit)`

保留旧方法：

```go
ListAgentTaskEvents(ctx, tenantID, userID, taskID, limit)
```

旧方法可以内部调用 `afterID=0`，保证行为兼容。

### 6.3 Tenant Service

新增方法：

```go
ListAgentTaskEventsAfter(ctx context.Context, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
```

权限边界与现有 `ListAgentTaskEvents` 一致：

- 通过 `ResolveContext` 确定 tenant/user。
- 只允许读取当前 tenant/user 的 task events。

### 6.4 Server Handler

`tenantAgentTaskEventsHandler`：

- 解析 `after_id`。
- 调用 service after 查询。
- 继续支持无 `after_id` 的老请求。

`tenantAgentTaskEventsStreamHandler`：

- 初始化 `lastID = after_id`。
- 每轮调用 after 查询，而不是每轮取最早 N 条。
- 每发一个 event 更新 `lastID`。
- task terminal 后执行 drain loop：
  - 反复查 `id > lastID`。
  - 直到返回空或不足 limit。
  - 再关闭连接。

伪代码：

```go
lastID := parseAfterID(r.URL.Query().Get("after_id"))
for {
    events := svc.ListAgentTaskEventsAfter(ctx, taskID, lastID, limit)
    for _, event := range events {
        writeSSE(event)
        lastID = event.ID
    }

    task := svc.GetAgentTask(ctx, taskID)
    if isTerminal(task.Status) {
        if len(events) == 0 {
            return
        }
        continue
    }

    waitForBrokerOrTicker()
}
```

### 6.5 Runner Sink

短期：

- 保持现有 DB 写入。
- 但确保 SSE after 查询能拿到所有后续事件。

中期：

- `agentTaskTextSink` 写入 live broker。
- DB 写入使用 batch/flush。
- completed 事件先 flush 所有 pending delta，再写 completed。

关键顺序：

```text
flush pending text_delta -> append completed event -> finish task completed
```

否则前端可能看到 completed 但漏掉前面的尾部 delta。

### 6.6 Live Broker

初始版本可以是进程内：

- `map[uint64]*taskHub`
- 每个 taskHub 有订阅 channel 集合。
- channel 必须有小 buffer，慢消费者超过 buffer 后关闭连接，让前端通过 after_id 重连补齐。
- server shutdown 时关闭全部订阅。

降级：

- broker 不保证跨进程。
- server 重启后，前端通过 DB `after_id` replay 恢复。

后续如果部署多实例，再考虑 Redis Stream / MySQL binlog / pubsub。本项目当前本地 Web Agent 以单机为主，不应过早引入分布式消息系统。

## 7. 前端技术设计

### 7.1 Event Store 状态

`WebAgentPage` 当前通过 `events` 数组和 `mergeEvents` 合并。需要增加显式游标：

- `newestEventIDRef`
- `loadedAllEvents`
- `streamAfterID`
- `eventBackfillInFlight`

规则：

- 初始加载时从 `after_id=0` 分页拉，直到返回不足 limit 或达到 terminal event。
- 运行中 SSE 使用 `after_id=newestEventIDRef.current`。
- SSE 收到 event 后更新 `newestEventIDRef`。
- SSE 断线后自动用最新 id 重连或触发 backfill。

### 7.2 初始加载与刷新

不能只调用一次 `listAgentTaskEvents(identity, taskID, 200)`。

建议函数：

```ts
async function listAllAgentTaskEvents(identity, taskID, limit = 500) {
  let afterID = 0;
  const all = [];
  for (;;) {
    const items = await listAgentTaskEvents(identity, taskID, { afterID, limit });
    all.push(...items);
    if (items.length < limit) break;
    afterID = Math.max(...items.map((item) => item.id));
  }
  return all;
}
```

保护：

- 单次 refresh 最多拉取一定页数，例如 20 页，避免异常任务无限拉。
- 如果超过页数仍未结束，显示“历史事件较多，继续加载”。
- 对 active running task 优先走 SSE，不在 UI 主线程里一次性拉海量历史。

### 7.3 SSE 重连

`streamAgentTaskEvents` 增加 `afterID` 参数：

```ts
streamAgentTaskEvents(identity, taskId, callbacks, signal, afterID)
```

URL：

```ts
`/tenant/agent-tasks/${taskId}/events/stream?after_id=${afterID}&limit=500`
```

断线策略：

- 正常 terminal close：refresh task detail + backfill events。
- 非正常错误：指数退避重连，带最新 `after_id`。
- 重连前先调用 list events after_id 补漏，避免 broker 中间丢事件。

### 7.4 Completed Response 兜底

构建 conversation message 时，需要识别完整 response：

1. 如果 events 里有完整 text_delta，则按 events 拼。
2. 如果 task completed 且 `result_json.response` 非空：
   - 若当前 assistant message 内容为空，直接使用 response。
   - 若当前内容是 response 前缀，则用 response 覆盖。
   - 若当前内容与 response 不一致，保留 events message，并在右侧 Progress 标记“event replay mismatch”，同时主显示使用 response。

判断：

```ts
if (task.status === "completed" && result.response) {
  assistant.content = reconcileAssistantContent(assistant.content, result.response)
}
```

这能保证即使 SSE/DB replay 有缺口，用户也不会看到半截最终答案。

### 7.5 渲染性能

长回复不应每个 token 都触发全量 Markdown parse：

- streaming 中先以 plain/pre-wrap 或轻量 Markdown 增量渲染。
- requestAnimationFrame 或 50ms throttle 合并 state update。
- terminal 后再做一次完整 Markdown parse。
- events 数组可以保存所有事件，但 conversation content 应使用 memoized aggregated content。

## 8. 核心流程

### 8.1 新消息实时输出

```text
1. Browser POST /tenant/agent-tasks/:id/message
2. Server 创建 message event，启动 runner
3. Browser 以 after_id=lastSeen 打开 SSE
4. Runner 产生 delta
5. Sink 立即 publish live broker
6. Sink 批量 append text_delta 到 DB
7. SSE 从 broker 推 delta；若 broker 不可用则 DB after_id polling
8. Runner 完成
9. Sink flush pending delta
10. Server append completed event
11. Server finish task completed，result_json.response 写完整答案
12. SSE drain id > lastSent，发送 completed 后关闭
13. Browser refresh task detail，用 result_json.response 校准最终 message
```

### 8.2 刷新页面恢复

```text
1. Browser 读取 task list
2. Browser 选中 task
3. Browser GET /events?after_id=0&limit=500
4. 如果返回满页，继续 after_id=lastID 拉下一页
5. Browser GET /tenant/agent-tasks/:id
6. 如果 task running，打开 SSE after_id=lastID
7. 如果 task completed，用 result_json.response 兜底完整内容
```

### 8.3 SSE 断线恢复

```text
1. Browser 记录 latest event id
2. SSE error/close
3. Browser GET /events?after_id=latestID&limit=500 补漏
4. 如果 task still running，重新打开 SSE?after_id=newLatestID
5. 如果 task terminal，GET task detail 并用 result_json.response 校准
```

## 9. 兼容性与降级策略

### 9.1 API 兼容

- 老客户端不传 `after_id`，仍可获得从头开始的 events。
- 响应 `data` 字段不变。
- `limit` 最大值保护保留。
- 新增字段只做 additive change。

### 9.2 Server 重启

- live broker 内存状态丢失是可接受的。
- Browser 重连后通过 DB `after_id` replay。
- 如果 task 已 terminal，通过 task result 校准最终内容。

### 9.3 DB 压力

- after_id 查询必须走 `(tenant_id,user_id,task_id,id)` 索引。
- SSE fallback polling 间隔保持 1s 或根据空轮询退避。
- runner delta 写入聚合后再落库，减少写放大。

### 9.4 慢消费者

- SSE channel buffer 满时关闭连接。
- Browser 使用 last seen id 重连补齐。
- 不为了慢客户端阻塞 runner。

### 9.5 超时

现有超时仍保留：

- `GOLANG_CLAUDE_CODE_AGENT_TASK_RUN_TIMEOUT_SECONDS`，默认 20 分钟。
- `GOLANG_CLAUDE_CODE_AGENT_TASK_IDLE_TIMEOUT_SECONDS`，默认 2 分钟。

本问题的修复不依赖调大超时。超时只处理 provider/runner 不再产出的问题；长回复截断由 after_id/broker/result fallback 解决。

## 10. 渐进式开发计划

### P0：DB 游标回放和 SSE drain

目标：先彻底消除“只读最早 200 条”的截断。

后端：

- repository 增加 `ListAgentTaskEventsAfter`。
- service 增加 after 查询。
- `/events` 支持 `after_id`。
- `/events/stream` 改为 after 增量查询。
- terminal 后 drain 剩余事件再关闭。
- 增加 server/repository 单测。

前端：

- `listAgentTaskEvents` 支持 `{ afterID, limit }`。
- 初始加载和 refresh 改为分页拉完整 events。
- SSE URL 带 `after_id=newestEventID`。
- SSE 关闭后 backfill + task detail refresh。

验收：

- 构造 1200 条 `text_delta` 的 task，页面刷新后显示完整回复。
- SSE 从 `after_id=0` 能收到 terminal event。
- SSE 从中间 `after_id` 重连能补齐后半段。

### P1：completed response 兜底

目标：即使 events 有缺口，最终页面也能显示完整答案。

后端：

- completed payload 确认包含完整 `response`。
- result_json 保持完整 `response`。

前端：

- conversation builder 接入 task result。
- 如果 event 拼接内容是 result response 的前缀，用完整 response 覆盖。
- 如果不一致，记录状态提示，不阻断显示。

验收：

- 人为只返回前 200 条 events，但 task result 有完整 response，UI 最终显示完整 response。
- 刷新 completed task 不再显示半截。

### P2：Live Broker 直推

目标：让 Web 实时体验接近 TUI，减少 DB polling 延迟。

后端：

- 新增 in-memory broker。
- runner sink publish live event。
- SSE 优先消费 broker，同时用 DB after_id 补漏。
- 慢消费者断开，不阻塞 runner。

验收：

- 真实 provider 输出时，页面 delta 延迟稳定低于 200ms-500ms。
- broker 断连/重启后 DB replay 可恢复。

### P3：delta 聚合写入

目标：减少 DB 写放大和前端高频 render。

后端：

- `agentTaskTextSink` 增加 buffer。
- 50-100ms 或 200-500 chars flush。
- completed 前强制 flush。

前端：

- streaming render throttle。
- terminal 后完整 Markdown render。

验收：

- 5000 字回复 event 数显著下降，例如从 1800+ 降到 100-300。
- 用户看到的打字机体验仍连续。

## 11. 测试与验收清单

### 11.1 单元测试

后端：

- `ListAgentTaskEventsAfter` 返回 `id > after_id` 且 `id ASC`。
- `after_id=0` 行为等价从头读取。
- `limit` 仍受 normalizeLimit 保护。
- SSE handler 不再重复发送旧 event。
- terminal task drain 完 `completed` 后关闭。
- server restart 场景下 DB replay 可返回完整 events。

前端：

- `listAllAgentTaskEvents` 多页合并且不丢序。
- SSE 重连使用最新 event id。
- completed result response 覆盖前缀 partial content。
- events 和 result mismatch 时主显示完整 response，并保留诊断状态。
- 长 Markdown 回复完整渲染，最后一段可见。

### 11.2 集成测试

- 构造 1500 条 text_delta + completed event 的 task。
- 调用 `/events?after_id=0&limit=500` 拉 4 页，确认完整。
- 调用 `/events?after_id=<middle>&limit=500`，确认只返回后续。
- 调用 `/events/stream?after_id=0&limit=500`，确认最终包含 `completed`。
- 模拟 SSE 中途断开，再用 after_id 重连，确认无重复、无丢失。

### 11.3 真机 E2E

使用真实 Web Agent 启动脚本：

```bash
GOLANG_CLAUDE_CODE_WEB_AGENT_HOST=0.0.0.0 \
GOLANG_CLAUDE_CODE_WEB_AGENT_PORT=18087 \
GOLANG_CLAUDE_CODE_WEB_AGENT_AUTH_TOKEN=test-token \
./scripts/web-agent-start.sh
```

验收流程：

1. 手机访问 `/webui/agent?token=test-token`。
2. 发送一个明确要求长回复的 prompt，目标输出至少 3000-5000 字。
3. 观察流式输出不断追加，不停在前 200/500 条 delta。
4. 等待 task completed。
5. 刷新页面。
6. 确认完整回复仍在，不退化成半截。
7. 切换 session 再切回来，确认完整回复仍在。
8. DB 回查：
   - task `completed`
   - event 包含 `completed`
   - `/events` 分页总数等于 DB event 总数
   - `result_json.response` 与 UI 最终 assistant 内容一致或 UI 内容包含完整 response。

### 11.4 必跑命令

```bash
go test ./internal/server -count=1
go test ./internal/storage/mysql -count=1
go test ./internal/tenant -count=1
npm --prefix web test -- WebAgentPage.test.tsx
npm --prefix web test
npm --prefix web run build
git diff --check
```

如果涉及 Swagger 注释或 API 文档变化：

```bash
swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency
```

并同步：

- `docs/api_server.md`
- `docs/docs.go`
- `docs/swagger.json`
- `docs/swagger.yaml`

## 12. 验收标准

本问题只有同时满足以下条件才算彻底修复：

1. 任意长度回复不会因为 event 数量超过 200/500 而截断。
2. SSE live 输出能持续收到新 event，不会卡在最早 N 条。
3. 页面刷新后能分页回放完整历史事件。
4. SSE 断线后能用 `after_id` 补漏并继续。
5. task completed 后 UI 以 `result_json.response` 校准最终完整答案。
6. DB 写入量可控，不因 tiny token delta 造成明显性能问题。
7. 真机手机测试、桌面浏览器测试、MySQL 回查、前后端单测全部通过。
8. completed-first、终态后 bounded `after_id` readback、shutdown cancellation 和旧客户端
   不读回兼容性均有回归覆盖。

## 13. 关键代码位置

后端：

- `internal/server/server.go`
  - `tenantAgentTaskEventsHandler`
  - `tenantAgentTaskEventsStreamHandler`
  - `runAgentTaskMessage`
  - `agentTaskTextSink`
- `internal/storage/mysql/gorm_repository.go`
  - `ListAgentTaskEvents`
  - 新增 `ListAgentTaskEventsAfter`
- `internal/storage/mysql/repository.go`
  - SQL repository 的同名能力
  - `normalizeLimit`
- `internal/tenant/service.go`
  - event list service wrapper

前端：

- `web/src/lib/api.ts`
  - `listAgentTaskEvents`
  - `streamAgentTaskEvents`
- `web/src/components/WebAgentPage.tsx`
  - refresh/initial load
  - SSE effect
  - running watchdog
  - `buildConversationMessages`
  - completed result fallback
- `web/src/components/WebAgentPage.test.tsx`
  - 长回复、多页 events、断线重连和 result fallback 单测

文档：

- `docs/web_agent/web_agent_quality_todo.md`
- `docs/web_agent/web_agent_lifecycle_runner_fix_plan.md`
- `docs/web_agent/web_agent_observability_integration_plan.md`
- 本文档

## 14. 决策结论

不建议用“把 `limit=200` 改成 `limit=5000`”作为修复。那只是延后问题，并且会放大 DB/API/前端渲染压力。

正确方向是：

```text
P0: DB after_id cursor + SSE drain
P1: completed result response fallback
P2: live broker direct push
P3: delta aggregation and render throttle
```

其中 P0 + P1 是彻底消除半截回复的最低闭环；P2 + P3 让 Web Agent 的实时体验更接近 TUI，并让长任务在性能上可靠。
