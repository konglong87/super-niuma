# Web Agent Real E2E Usage And Evidence

本文记录 `/webui/agent` 在真实本机环境中的端到端使用方式、验证步骤和截图证据。这里的 E2E 不是 Playwright mock，也不是只看前端页面，而是同时覆盖：

- 真实 Go server。
- 真实 WebUI SPA。
- 真实 MySQL tenant storage。
- 真实浏览器点击流程。
- `tenant_agent_tasks` / `tenant_agent_task_events` 落库回查。
- `tenant_telemetry_events` API 请求证据。

## 运行前提

Recommended scripts:

```bash
# Real provider + MySQL + WebUI server.
scripts/web-agent-start.sh

# Real lifecycle E2E: API, runner, event stream, MySQL, and browser rendering.
scripts/web-agent-real-e2e.sh

# Select a named provider when the workspace disables fallback providers.
GOLANG_CC_WEB_AGENT_PROVIDER=<provider-name> \
GOLANG_CC_WEB_AGENT_MODEL=<provider-model> \
scripts/web-agent-real-e2e.sh

# UI-only long-message scroll/typewriter stub. Do not use this as real model evidence.
scripts/web-agent-scroll-smoke.sh
```

Script details and environment overrides are documented in [web_agent_scripts.md](web_agent_scripts.md).

本地需要 MySQL，示例使用 root 无密码的 Homebrew MySQL：

```bash
mysql -uroot -h127.0.0.1 -P3306 -e 'SELECT VERSION() AS version;'
```

构建 WebUI：

```bash
npm --prefix web run build
```

创建隔离库并执行 migration：

```bash
mysql -uroot -h127.0.0.1 -P3306 -e 'DROP DATABASE IF EXISTS golang_cc_web_agent_e2e; CREATE DATABASE golang_cc_web_agent_e2e CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'

GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_web_agent_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
  go run ./cmd/golang-cc tenant migrate up
```

初始化 WebUI 默认身份。`/webui/agent` 默认使用 `webui-local / webui-local-user`，隔离库第一次使用时需要先创建这个 tenant/user：

```bash
mysql -uroot -h127.0.0.1 -P3306 golang_cc_web_agent_e2e -e "
INSERT INTO tenants (tenant_key, name, status)
VALUES ('webui-local','WebUI Local','active')
ON DUPLICATE KEY UPDATE name=VALUES(name), status=VALUES(status);

SET @tenant_id = (SELECT id FROM tenants WHERE tenant_key='webui-local');

INSERT INTO tenant_users (tenant_id, user_key, email, display_name, role, status)
VALUES (@tenant_id, 'webui-local-user', 'webui-local-user@example.test', 'WebUI Local User', 'owner', 'active')
ON DUPLICATE KEY UPDATE display_name=VALUES(display_name), role=VALUES(role), status=VALUES(status);
"
```

启动带 MySQL tenant storage 的 server：

```bash
GOLANG_CC_WEBUI_DIR=web/dist \
GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_web_agent_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
  go run ./cmd/golang-cc server --host 127.0.0.1 --port 18083 --auth-token test-token
```

启动日志应包含：

```text
Tenant storage: mysql
```

## 访问入口

打开：

```text
http://127.0.0.1:18083/webui/agent?token=test-token
```

`/webui/` 和 `/webui/agent` 是同一个 SPA bundle 的不同路由。`/webui/` 是综合 API/WebUI 工作台，`/webui/agent` 是本地 Web Agent 工作台。两者可以通过页面里的 `Main WebUI` 入口互通，但不应该合并成同一个页面心智。

## 真机操作流程

1. 打开 `/webui/agent?token=test-token`。
2. 确认左侧 Workspace 是当前仓库：`/path/to/golang-cc`。
3. 点击 `Validate`，确认 cwd validate 成功。
4. 点击 `New Session`。
5. Enter a title; leaving the first prompt empty creates a `ready` blank session.
6. 点击 `Create Session`。
7. 页面切到真实 task，会话列表出现刚创建的 session。
8. Type in the composer and press Enter. The task moves to `running`, and SSE appends `message`, `text_delta`, and terminal events.
9. For a long-running task, click `Cancel`; after cancellation the composer returns to `Send`.
10. Sending again from a completed/cancelled task creates a continuation task instead of writing to the terminal task.
11. Switch the right panel through `Progress` / `Files` / `Permissions`.
12. Collapse both side panels to verify center-only focus mode.

## 真实后端证据

后端 API 在 MySQL storage 下返回 200：

```bash
curl -sS 'http://127.0.0.1:18083/tenant/agent-tasks?limit=10' \
  -H 'Authorization: Bearer test-token' \
  -H 'X-Tenant-Key: webui-local' \
  -H 'X-User-Id: webui-local-user'
```

真实落库结果：

```text
tenant_agent_tasks
id: 1
agent_name: web-agent
description: Real provider smoke test
status: completed
model: glm-5.1
cwd: /path/to/golang-cc
result_json.response: 真实后端连通测试已成功完成，系统运行正常。
```

If a response contains `web agent browser flow ok`, the server is still pointed at an old local stub provider. That is useful only for UI smoke checks and must not be recorded as real model evidence.

事件流：

```text
tenant_agent_task_events
1 message: Read README.md and summarize it
2 text_delta: 真实后
3 text_delta: 端连通
4 text_delta: 测试已成功完成
5 completed
```

Telemetry 请求证据：

```text
tenant_telemetry_events
GET  /tenant/agent-tasks              200
GET  /tenant/agent-tasks/1/events     200
POST /tenant/agent-tasks/1/cancel     200
POST /tenant/agent-tasks/1/message    200
```

## 截图证据

真实 task 加载后：

_Historical screenshot omitted from this source migration pending privacy review._

取消后：

_Historical screenshot omitted from this source migration pending privacy review._

Center-only focus mode：

_Historical screenshot omitted from this source migration pending privacy review._

桌面视觉基线：

_Historical screenshot omitted from this source migration pending privacy review._

移动端主画布：

_Historical screenshot omitted from this source migration pending privacy review._

## Current Product Boundary

`POST /tenant/agent-tasks` only creates the sub-agent task management record. `status=ready` is used for blank sessions and does not append a `started` event. `status=running` appends `started`, but model execution is triggered by the subsequent `POST /tenant/agent-tasks/{id}/message` call.

`POST /tenant/agent-tasks/{id}/message` promotes the task to `running` and calls the server query runner. Runner text is persisted as `text_delta`, then the task converges to `completed`, `failed`, or `cancelled`. If the server has no query runner configured, the API returns 503 and marks the task failed.

This round still does not add separate `agent_sessions` / `agent_runs` tables. Continuations are linked through `metadata_json.continuation_of_task_id`. Structured file, command, and permission events depend on the underlying runner/tool progress path writing task events and can be expanded with the dedicated run model.

## Acceptance Criteria

A valid Web Agent live E2E pass must verify:

- The page opens from the real server without a framework error overlay.
- `/tenant/agent-tasks` returns 200 with MySQL tenant storage enabled.
- Workspace validation returns the real cwd, git root, and workspace name.
- New Session without a prompt creates a real `ready` task and does not show Cancel or zero-value activity.
- Composer Enter writes a real `message` event, and SSE returns runner `text_delta` plus a terminal event.
- Cancel updates task status and appends a `cancelled` event.
- Sending again from completed/cancelled creates a continuation task instead of writing to the terminal task.
- A full conversation auto-scrolls to the newest message, while manual history review shows a jump-to-latest control.
- User messages are right-aligned and assistant replies are left-aligned.
- The right panel `Progress / Files / Permissions` tabs remain usable.
- Center-only mode does not leave large side gaps.
- Browser console has no unexpected errors.
- MySQL can verify tasks, events, and telemetry.
