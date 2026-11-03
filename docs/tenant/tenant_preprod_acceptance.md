# Tenant Preprod Acceptance

本文档用于这次 tenant skills runtime 与 API server 聊天历史持久化上线前验收。

## 本机/测试环境启动

```bash
export GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_preprod?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4'

go run ./cmd/golang-cc tenant migrate up
go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token
```

## 一键验收

server 启动后执行：

```bash
GOLANG_CC_PREPROD_BASE_URL=http://127.0.0.1:18080 \
GOLANG_CC_PREPROD_AUTH_TOKEN=test-token \
GOLANG_CC_PREPROD_MYSQL_DSN="$GOLANG_CC_MYSQL_DSN" \
  scripts/tenant-preprod-acceptance.sh
```

该脚本覆盖：

- main server `/health` 可用。
- 租户 A 写入 tenant skill 和 user override。
- tenant skill 历史版本与 rollback API 可通过单元/golden 测试固定，WebUI 可在 Skills 面板执行 Validate/Rollback。
- 租户 B 不能看到租户 A 的 skill。
- 同一 tenant/user 在 device A 写入 session/message 后，device B 能读回 session 和 messages。
- Mobile WebSocket 双设备订阅同一 session 后都能收到 stream `message_start/delta/message_stop`，由 `scripts/mobile-ws-sync-acceptance.sh` 执行金标测试。
- 配置 MySQL DSN 时直查 `tenant_skills`、`tenant_sessions`、`tenant_session_messages`。

如测试环境已配置真实模型 provider，可单独追加：

```bash
GOLANG_CC_TENANT_SKILLS_SMOKE_QUERY=1 \
GOLANG_CC_TENANT_SKILLS_SMOKE_BASE_URL=http://127.0.0.1:18080 \
GOLANG_CC_TENANT_SKILLS_SMOKE_AUTH_TOKEN=test-token \
GOLANG_CC_TENANT_SKILLS_SMOKE_MYSQL_DSN="$GOLANG_CC_MYSQL_DSN" \
  scripts/tenant-skills-runtime-smoke.sh
```

真实 MySQL opt-in E2E 可直接用本机 MySQL 运行：

```bash
scripts/tenant-mysql-e2e.sh
```

该脚本会重建隔离库 `golang_cc_tenant_e2e`，设置 `GOLANG_CC_MYSQL_E2E_DSN`，并执行 `internal/storage/mysql`、`internal/tenant`、`internal/server` 下所有 `TestMySQLE2E*`。覆盖 tenant CRUD、session/message 持久化、`/query` 落库、mobile chat 落库、tenant skills runtime、goal API、跨租户隔离和 rollback 并发。

## 验收边界

- `tenant-preprod-acceptance.sh` 不依赖真实模型 provider，适合每次部署后快速验证 MySQL、tenant API、skills 热加载配置面和聊天历史持久化。
- `tenant-preprod-acceptance.sh` 默认会执行 Mobile WebSocket 多端同步金标；如只想验证已启动的外部环境，可设置 `GOLANG_CC_PREPROD_SKIP_MOBILE_WS=1` 跳过本地 Go 测试。
- `GOLANG_CC_TENANT_SKILLS_SMOKE_QUERY=1` 会调用 `/query`，需要 server 具备可用模型 provider。
- `scripts/tenant-skills-runtime-smoke.sh` 支持 `/query` 返回 JSON 或 SSE：JSON 模式会检查 `tool_calls` 中的 `Skill` 工具调用，SSE 模式会检查 `message_stop/error/skill_activated` 事件。
- 脚本默认使用 `yutang` 作为租户 A，`tenant-b` 作为租户 B；可通过环境变量覆盖。

## 2026-06-26 本机验收记录

环境：

- MySQL database：`golang_cc_preprod_acceptance`
- Server：`go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token`
- DSN：`root@tcp(127.0.0.1:3306)/golang_cc_preprod_acceptance?...`

执行：

```bash
mysql -uroot -h127.0.0.1 -P3306 -e 'DROP DATABASE IF EXISTS golang_cc_preprod_acceptance; CREATE DATABASE golang_cc_preprod_acceptance CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'

GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_preprod_acceptance?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
  go run ./cmd/golang-cc tenant migrate up

GOLANG_CC_PREPROD_BASE_URL=http://127.0.0.1:18080 \
GOLANG_CC_PREPROD_AUTH_TOKEN=test-token \
GOLANG_CC_PREPROD_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_preprod_acceptance?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
  scripts/tenant-preprod-acceptance.sh
```

结果：

- PASS：`tenant-preprod-acceptance.sh`
- PASS：tenant A `yutang` 写入 `preprod-tenant-skill` 后可在 effective skills 读到。
- PASS：tenant B `tenant-b` effective skills 不包含 tenant A 的 `preprod-tenant-skill`。
- PASS：device A 创建 `history-session-1782450815` 并写入 user/assistant 两条 message，device B 可读回同一 session 和 messages。
- PASS：`mobile-ws-sync-acceptance.sh` 多设备 WebSocket 同步金标。
- DB 直查：`yutang` 有 1 条 skill，`tenant-b` 有 0 条 skill；`yutang/preprod-user` 有 1 个 session；`history-session-1782450815` 有 2 条 message。

本次未执行 `GOLANG_CC_TENANT_SKILLS_SMOKE_QUERY=1` 的真实模型 `/query` 验收；该项需要本地配置可用模型 provider。

## 2026-06-26 隔离修复后回归记录

环境：

- MySQL database：`golang_cc_todo_full`
- Server：`go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token`

结果：

- PASS：`scripts/tenant-preprod-acceptance.sh`
- PASS：tenant A skill 写入和 effective skills 读取。
- PASS：tenant B 不可见 tenant A skill。
- PASS：device A 创建 `history-session-1782453051` 并写入 2 条 message，device B 可读回。
- PASS：Mobile WebSocket 多设备同步金标。
- 运行日志确认 `tenant.Service.UpsertMessage` 与 `mysql.GormRepository.UpsertMessage` 写入 message 前均执行了 `session.get` ownership 检查。

## 2026-06-26 MySQL E2E 金标补充

新增并通过：

- PASS：`TestMySQLE2ETenantDataIsolation`
- PASS：`TestMySQLE2ETenantSkillRollbackConcurrent`

覆盖点：

- tenant B 不能读取或覆盖 tenant A 的 session message。
- 同一 tenant 内 user C 不能覆盖 user A 的 session message。
- tenant B 不能读取 tenant A skill，effective skills 不泄露。
- DB 直查确认越权写入后原 message 行未被修改。
- 并发 rollback 同一历史 skill 版本时，真实 MySQL 事务锁生成连续版本 `1,2,3,4`。

## 2026-06-26 本地配置 provider 真实模型验收

环境：

- MySQL database：`golang_cc_local_provider_e2e`
- Server：`go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token`
- Provider：项目本地配置文件，`provider=custom`、`model=glm-5.1`、`ANTHROPIC_BASE_URL=https://ai-gateway.example.com/v1`

执行：

```bash
GOLANG_CC_TENANT_SKILLS_SMOKE_QUERY=1 \
GOLANG_CC_TENANT_SKILLS_SMOKE_BASE_URL=http://127.0.0.1:18080 \
GOLANG_CC_TENANT_SKILLS_SMOKE_AUTH_TOKEN=test-token \
GOLANG_CC_TENANT_SKILLS_SMOKE_USER=local-provider-smoke-user-2 \
GOLANG_CC_TENANT_SKILLS_SMOKE_SKILL=local-provider-smoke-skill-2 \
GOLANG_CC_TENANT_SKILLS_SMOKE_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_local_provider_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
  scripts/tenant-skills-runtime-smoke.sh
```

结果：

- PASS：`tenant skills runtime smoke passed`
- PASS：`/query` 使用 `glm-5.1` 返回 `ok`。
- PASS：`tool_calls` 包含 `Skill`，且 input 命中 `local-provider-smoke-skill-2`，output 包含 MySQL tenant skill 正文 `Smoke tenant skill`。
- PASS：MySQL `tenant_sessions` 写入 `local-provider-smoke-session-*`，model 为 `glm-5.1`。
- PASS：MySQL `tenant_session_messages` 写入 user/assistant 两条消息，assistant content 为 `ok`。
- PASS：MySQL `tenant_telemetry_events` 写入 `query.run.finished`，状态 `ok`。
