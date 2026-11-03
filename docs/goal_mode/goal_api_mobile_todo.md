# Goal Mode API / Mobile TODO

本文档跟踪 Goal Mode 从本地 CLI/TUI 能力扩展到 API Server、MySQL、多端可观测和 Mobile WebSocket 的实施进度。目标是在 macOS 本机可完整验证的范围内，先完成持久化管理面，再接入运行和流式事件。

## 目标边界

本轮目标包含：

- MySQL 持久化 Goal record 和 Goal event，复用 tenant/user 隔离。
- API Server 管理端点：create/list/get/events/stop/resume/run。
- Swagger 和 API 文档同步。
- curl / Go test / MySQL 直查 E2E。
- Mobile WebSocket 控制面能订阅 Goal 事件。
- 后续可接入更丰富 evaluator 和后台 continuous runner。

本轮不包含：

- Windows / Linux runner 平台能力。
- 外部任务队列或分布式调度器。
- 将 Goal mode 变成不受用户控制的自动后台执行。

## 当前基线

| 能力 | 状态 | 证据 |
| --- | --- | --- |
| Local goal store | DONE | `internal/goal.LocalStore` JSONL store |
| CLI commands | DONE | `goal start/status/list/logs/stop/resume/run/unlock` |
| TUI slash commands | DONE | `/goal start/status/stop/resume/logs/unlock` |
| Runner | DONE | `goal.Runner.RunOnce` / `RunUntilStop` |
| Query runner | DONE | CLI `goalQueryRunner` 创建 checkpoint 并执行 query turn |
| API Server | DONE | 已暴露 `/tenant/goals` 管理面和 `/tenant/goals/{id}/run` one-turn 入口 |
| MySQL tenant store | DONE | `tenant_goals` / `tenant_goal_events` migration + repository/service |
| Mobile WebSocket | DONE | `/mobile/chat/ws` 支持 `subscribe_goal` 并广播 Goal event envelope |

## 实施 Backlog

| ID | 状态 | 模块 | 待办 | 验收标准 | 测试 |
| --- | --- | --- | --- | --- | --- |
| GOAL-API-001 | DONE | docs | 建立 Goal API / Mobile TODO 进度文档并同步总 TODO。 | 文档说明目标、边界、顺序和验收命令。 | `git diff --check` |
| GOAL-API-002 | DONE | mysql/tenant | 新增 tenant goal MySQL schema、repository 和 tenant service 方法。 | `tenant_goals` 和 `tenant_goal_events` 支持 create/get/list/update/events，所有查询带 tenant/user 限定。 | `go test ./internal/storage/mysql ./internal/tenant -count=1` |
| GOAL-API-003 | DONE | server | 暴露 `/tenant/goals` 管理 API：create/list/get/events/stop/resume。 | API 复用 Bearer auth 和 tenant headers；stop/resume 写状态和事件。 | `go test ./internal/server -count=1` |
| GOAL-API-004 | DONE | server/runner | 暴露 `/tenant/goals/{id}/run` 单步执行入口。 | `run` 只执行 one turn；失败和状态变化可观测；未配置 query runner 时返回 503 且不修改 Goal。当前 API runner 不创建本地 transcript checkpoint。 | `go test ./internal/server ./internal/goal -count=1` |
| GOAL-API-005 | DONE | mobile | Mobile WebSocket 订阅 Goal events。 | 移动端可按 goal id 订阅并接收 `goal_event`，事件 envelope 带 type/event/goal_id/status/goal_event/tenant_key/user_id/device_id/timestamp。 | `go test ./internal/server -run 'MobileWebSocket.*Goal|MobileStreamRegistryGoal' -count=1` |
| GOAL-API-006 | DONE | docs/swagger | 更新 Swagger、API 文档和 Goal 设计文档。 | Swagger generated docs 已包含 Goal 管理 API 和 one-turn run；Mobile WebSocket 文档已补 `subscribe_goal` 控制消息。 | `swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency`; `git diff --check` |
| GOAL-API-007 | DONE | e2e | 本地 MySQL + API Server curl E2E。 | migration 后 create/list/get/events/stop/resume 全链路通过，直查 MySQL 行符合预期。 | `GOLANG_CLAUDE_CODE_MYSQL_E2E_DSN=... go test ./internal/server -run TestMySQLE2EGoalAPILifecycle -count=1 -v`; curl + MySQL query evidence |

## 推荐实施顺序

1. `GOAL-API-001`：先固定范围，避免 Goal/API/Mobile 一次性失控。
2. `GOAL-API-002`：持久化是 API 和 Mobile 的基础，先做 schema/repository/service。
3. `GOAL-API-003`：管理面 API 先闭环，不先启动真实 runner。
4. `GOAL-API-006`：同步 Swagger/API 文档，防止客户端契约漂移。
5. `GOAL-API-007`：本地 MySQL + curl 验证 create/list/get/events/stop/resume。
6. `GOAL-API-004`：接入 one-turn run，需要谨慎处理模型 provider、checkpoint 和错误。
7. `GOAL-API-005`：最后接 Mobile WebSocket，因为它依赖稳定事件模型。

## 本地验收命令

```bash
go test ./internal/goal ./internal/storage/mysql ./internal/tenant ./internal/server -count=1
go test ./... -count=1
git diff --check
```

涉及 Swagger 时额外运行：

```bash
swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency
```

涉及真实 API/MySQL 时使用隔离库，例如：

```bash
export GOLANG_CLAUDE_CODE_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_goal_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4'
go run ./cmd/golang-cc tenant migrate up
go run ./cmd/golang-cc server --host 127.0.0.1 --port 18082 --auth-token test-token
```

## GOAL-API-007 E2E 证据

验证日期：2026-06-24。

- MySQL：本机 Homebrew MySQL，`mysql -uroot -e 'SELECT VERSION() AS version;'` 返回 `9.5.0`。
- 测试库：`golang_cc_goal_e2e`。
- Migration：`go run ./cmd/golang-cc tenant migrate version --json` 返回 `{"version":4,"dirty":false,"applied":true}`。
- 自动化真实 MySQL 测试：`GOLANG_CLAUDE_CODE_MYSQL_E2E_DSN='root@tcp(127.0.0.1:3306)/golang_cc_goal_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' go test ./internal/server -run TestMySQLE2EGoalAPILifecycle -count=1 -v` 通过。
- 真实 server：`GOLANG_CLAUDE_CODE_MYSQL_DSN=... go run ./cmd/golang-cc server --host 127.0.0.1 --port 18082 --auth-token test-token`，启动日志显示 `Tenant storage: mysql`。
- curl 覆盖：`GET /health`、`PATCH /tenant/user`、`POST /tenant/goals`、`GET /tenant/goals`、`GET /tenant/goals/{id}`、`GET /tenant/goals/{id}/events`、`POST /tenant/goals/{id}/stop`、`POST /tenant/goals/{id}/resume`。
- curl 结果：创建 `goal_3e27315b555ef34cee2c5241`，状态从 `active` 到 `stopped` 再回到 `active`，事件列表为 `goal_started,goal_stopped,goal_resumed`。
- MySQL 直查：`tenant_goals.status=active`，`tenant_goal_events` 聚合 `event_count=3`，`event_types=goal_started,goal_stopped,goal_resumed`。
