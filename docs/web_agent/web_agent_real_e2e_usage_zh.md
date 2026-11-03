# Web Agent 真机 E2E 使用说明与证据

本文是 `/webui/agent` 的中文使用说明和真机端到端验证记录。这里的 E2E 不是 Playwright mock，也不是只看前端页面，而是同时覆盖：

- 真实 Go server。
- 真实 WebUI SPA。
- 真实 MySQL tenant storage。
- 真实浏览器点击流程。
- `tenant_agent_tasks` / `tenant_agent_task_events` 落库回查。
- `tenant_telemetry_events` API 请求证据。

## 运行前提

推荐脚本：

```bash
# 真实 provider + MySQL + WebUI server。
scripts/web-agent-start.sh

# 真实全生命周期 E2E：API、runner、事件流、MySQL、浏览器渲染。
scripts/web-agent-real-e2e.sh

# workspace 禁用 fallback provider 时，显式指定 named provider。
GOLANG_CC_WEB_AGENT_PROVIDER=<provider-name> \
GOLANG_CC_WEB_AGENT_MODEL=<provider-model> \
scripts/web-agent-real-e2e.sh

# 只做长消息滚动和打字机 UI 压测，不是真实模型验收。
scripts/web-agent-scroll-smoke.sh
```

脚本用途、环境变量覆盖和真实/Stub 边界见 [web_agent_scripts.md](web_agent_scripts.md)。

本地需要 MySQL。下面示例使用 root 无密码的 Homebrew MySQL：

```bash
mysql -uroot -h127.0.0.1 -P3306 -e 'SELECT VERSION() AS version;'
```

先构建 WebUI：

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

如果没有配置 `GOLANG_CC_MYSQL_DSN`，页面仍能打开，workspace validate 也能工作，但 `/tenant/agent-tasks` 会返回：

```text
tenant storage is not configured
```

这是环境未启用 tenant storage，不是前端页面假死。

## 访问入口

打开：

```text
http://127.0.0.1:18083/webui/agent?token=test-token
```

`/webui/` 和 `/webui/agent` 是同一个 SPA bundle 的不同路由：

- `/webui/` 是综合 API/WebUI 工作台。
- `/webui/agent` 是本地 Web Agent 工作台。
- 两者可以通过页面里的 `Main WebUI / 主 WebUI` 入口互通。
- 不建议把两个页面合并成一个页面心智，因为 Web Agent 页面需要聚焦 workspace、session、progress、files、permissions 和 composer。

## 真机操作流程

1. 打开 `/webui/agent?token=test-token`。
2. 确认左侧 Workspace 是当前仓库：`/path/to/golang-cc`。
3. 点击 `Validate`，确认 cwd validate 成功。
4. 点击 `New Session`。
5. 输入 title；如不填 first prompt，会创建 `ready` 空白会话。
6. 点击 `Create Session`。
7. 页面切到真实 task，会话列表出现刚创建的 session。
8. 在 composer 输入消息并按 Enter，task 进入 `running`，SSE 追加 `message` / `text_delta` / `completed` 等事件。
9. 对长任务点击 `Cancel`，Cancel 后 composer 恢复 `Send`。
10. 在 completed/cancelled task 上继续输入，会创建 continuation task，而不是写入终态 task。
11. 右侧切换 `Progress` / `Files` / `Permissions` tab。
12. 关闭左右面板，进入 center-only focus mode。

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

如果回复包含 `web agent browser flow ok`，说明 server 仍指向旧的本地 stub provider。这只能作为 UI smoke，不应记录为真实模型证据。

事件流：

```text
tenant_agent_task_events
1 message: 请用一句中文回复：真实后端连通测试。不要使用英文。
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

## 当前产品边界

`POST /tenant/agent-tasks` 当前只创建 sub-agent task 管理记录。`status=ready` 用于空白会话，不追加 `started` event；`status=running` 会追加 `started` event，但真正的模型执行由后续 `POST /tenant/agent-tasks/{id}/message` 触发。

`POST /tenant/agent-tasks/{id}/message` 会把 task 提升为 `running` 并调用 server query runner。runner 文本输出会写入 `text_delta`，最终写入 `completed` / `failed` / `cancelled`。如果当前 server 没有配置 query runner，接口会返回 503 并把 task 收敛为 `failed`。

当前仍未在本轮引入独立 `agent_sessions` / `agent_runs` 双层表；continuation 通过 metadata 关联 `continuation_of_task_id`。文件、命令、权限等结构化工具事件依赖底层 runner/tool progress 是否写入 task event stream，后续可以在专门的 run 模型里继续增强。

## 验收口径

一次合格的 Web Agent 真机验收至少要同时满足：

- 页面能从真实 server 打开，不显示框架错误 overlay。
- `/tenant/agent-tasks` 在 MySQL storage 下返回 200。
- workspace validate 返回真实 cwd、git root 和 workspace name。
- New Session 不填 prompt 时真实写入 `ready` task，页面不显示取消按钮或 0 activity。
- Composer Enter 真实写入 `message` event，SSE 返回 runner `text_delta` 和终态 event。
- Cancel 真实更新 task status，并写入 `cancelled` event。
- completed/cancelled 后继续发送会创建 continuation task，不写入终态 task。
- 消息满屏后发送新消息会自动滚动到最新消息；用户手动查看历史时显示“回到最新”。
- 用户消息靠右，AI 回复靠左，身份区分清晰。
- 右侧 `Progress / Files / Permissions` tab 可操作。
- center-only mode 不留下左右大空白。
- 浏览器 console 没有非预期 error。
- MySQL 能回查 task、events 和 telemetry。
