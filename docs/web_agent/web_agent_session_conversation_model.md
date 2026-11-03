# Web Agent Session / Conversation Model

## 背景

TUI 的 `/resume` 展示的是持久化 session 列表。每个 session 是一次连续对话的根对象，后续消息、工具调用、usage、checkpoint 等都挂在同一个 session transcript 下。

Web Agent 之前的问题是左侧列表直接使用 `tenant_agent_tasks`。一次会话内每次 continuation 都会创建新的 task/run，所以同一个用户语义会话会显示多行，例如：

```text
workspace huyu
  yu001 task 13
  yu001 task 14
  yu001 task 15
  yu001 task 16
```

这和 Codex/TUI 的心智不一致。正确模型应该是：

```text
workspace huyu
  conversation/session yu001
    run/task 13
    run/task 14
    run/task 15
    run/task 16
```

## 决策

Web Agent 使用现有多租户 MySQL session 表作为稳定 conversation 层：

- `tenant_sessions.id` 是 Web Agent 的稳定 `web_agent_session_id` / `conversation_id`。
- `tenant_sessions.session_key` 是可外部引用的稳定 key。
- `tenant_agent_tasks.parent_session_id` 是每次 run/task 到 conversation 的持久化外键语义。
- `tenant_agent_task_events.task_id` 继续挂在具体 run/task 下。
- 前端左侧只展示 conversation/session，不展示裸 task/run。
- 中间对话区和右侧进度区可以读取该 conversation 下的所有 runs/events/messages。

没有选择直接复用 TUI `internal/session.Store` 作为 Web Agent 主存储，因为 Web Agent 是浏览器 API 场景，需要 tenant/user 隔离、Bearer token 鉴权、MySQL 生命周期、审计和可观测性。语义对齐 `/resume`，存储边界保持 Web/API 体系。

## API

新增：

```http
GET /tenant/web-agent/conversations?limit=100
Authorization: Bearer <token>
X-Tenant-Key: <tenant>
X-User-Id: <user>
```

响应：

```json
{
  "data": [
    {
      "id": "session:23",
      "session_id": 23,
      "session_key": "web-agent-...",
      "title": "yu001",
      "cwd": "$HOME/GolandProjects/huyu",
      "workspace_name": "huyu",
      "status": "completed",
      "updated_at": "2026-07-01T04:29:49Z",
      "session": {},
      "latest_task": {},
      "tasks": []
    }
  ]
}
```

详情：

```http
GET /tenant/web-agent/conversations/{id}?limit=100&event_limit=500
Authorization: Bearer <token>
X-Tenant-Key: <tenant>
X-User-Id: <user>
```

`{id}` 使用列表里的 `id`，例如 `session:23` 或 `legacy:13`，URL 中需要 percent-encode。响应在列表 conversation 基础上增加：

```json
{
  "id": "session:23",
  "tasks": [],
  "events": [],
  "messages": [],
  "usage": {
    "input_tokens": 120,
    "output_tokens": 80,
    "total_tokens": 200,
    "context_length": 200000,
    "context_percent": 1,
    "tool_calls": 2,
    "completed_runs": 2,
    "failed_runs": 0,
    "cancelled_runs": 0,
    "timeout_runs": 0,
    "running_runs": 0,
    "total_runs": 2,
    "total_duration_ms": 9000
  }
}
```

服务端聚合规则：

1. 有 `parent_session_id` 的 task 按 `session:<parent_session_id>` 分组。
2. 没有 `parent_session_id` 的历史 task 按 `metadata_json.continuation_of_task_id` 追溯 root task，使用 `legacy:<root_task_id>` 分组。
3. 每组内 `tasks` 按 `started_at,id` 升序排列。
4. `latest_task` 使用最新 `started_at,id`。
5. `updated_at` 优先使用 `latest_task.finished_at`，其次 `latest_task.started_at`，再其次 session 时间。

## 前端行为

`/webui/agent` 启动时优先调用 `/tenant/web-agent/conversations`。如果连接到旧后端或开发代理返回 404/HTML，前端会回退到旧的 `GET /tenant/sessions + GET /tenant/agent-tasks` 组合方式，避免页面直接崩溃。

选中某个 conversation 后，前端调用 `/tenant/web-agent/conversations/{id}`。详情接口成功时：

- 中间对话区使用该 conversation 下所有 run/task events 构造完整对话时间线。
- 右侧 `Runs` 展示该 conversation 下所有 run，可点击切换当前 run。
- 右侧 `Trace` 展示 session 级事件流，带 task id 和 trace id。
- 右侧 `Usage` 展示 session 级 token、上下文占比、工具调用、run 状态统计和总耗时。
- `Progress / Files / Permissions` 仍跟随当前选中的 run，避免把正在运行的 task 状态和历史 run 混在一起。

如果详情接口返回 404、旧开发代理 HTML 或空任务，前端回退到已有逐 task 拉事件逻辑，保证旧环境不白屏。

新建会话流程：

1. 校验 cwd。
2. `POST /tenant/sessions` 创建稳定 conversation。
3. `POST /tenant/agent-tasks` 创建首个 run，并写入 `parent_session_id`。

继续发送流程：

1. 在当前 conversation 下创建新的 continuation task/run。
2. 新 run 继续写入同一个 `parent_session_id`。
3. metadata 保留 `continuation_of_task_id` 作为历史链和调试信息。

## 验证矩阵

必须覆盖：

- 后端单测：4 个同 session continuation task 只返回 1 个 conversation。
- 后端单测：旧数据只有 `continuation_of_task_id` 时仍折成 1 个 legacy conversation。
- 后端单测：详情接口返回 runs、events、messages、usage，并支持 `session%3A23` URL。
- 前端单测：后端 conversation API 返回 1 个 session 时，左侧只渲染 1 行，不调用裸 task list。
- 前端 API 单测：请求路径固定为 `/tenant/web-agent/conversations?limit=100` 和 `/tenant/web-agent/conversations/session%3A17?limit=100&event_limit=500`。
- 前端组件单测：右侧包含 `Runs / Trace / Usage` session 级 tabs。
- Swagger：包含 `/tenant/web-agent/conversations` 和 `/tenant/web-agent/conversations/{id}`。
- 脚本全链路：`scripts/web-agent-real-e2e.sh` 会创建 session/run、发送真实请求、检查 SSE/event/task/MySQL/telemetry/browser，并校验详情 API 包含 run/events/usage。
- 浏览器真机：打开 `/webui/agent?token=test-token`，确认同一会话 continuation 不重复出现在左侧。

## 后续增强

- 若需要更强查询能力，可增加 repository 级 `ListAgentTasksByParentSessionID`，避免大列表扫描。
- 若要暴露 session metadata，需要扩展 `mysqlstore.Session` 的 JSON 字段和 Swagger 类型。
- 当前没有新增独立 `agent_sessions` / `agent_runs` 表；Web Agent 复用 `tenant_sessions` 和 `tenant_agent_tasks.parent_session_id`。如果后续需要跨存储迁移、归档、run 级分页或大规模检索，再引入专用表更合适。
