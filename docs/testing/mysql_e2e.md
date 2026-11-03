# MySQL 多租户全链路验证

验证日期：2026-06-06

## 环境

- MySQL：本机 Homebrew MySQL，`mysql -uroot -e 'SELECT VERSION();'` 返回 `9.5.0`
- 测试库：`golang_cc_e2e`
- API：`127.0.0.1:18080`
- 租户：`yutang`
- 用户：`user-e2e-1`
- Trace：`trace-e2e-002`

## 验证步骤

```bash
brew services start mysql
mysql -uroot -e 'DROP DATABASE IF EXISTS golang_cc_e2e; CREATE DATABASE golang_cc_e2e CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'

go run ./cmd/golang-cc tenant migrate up \
  --dsn 'root@tcp(127.0.0.1:3306)/golang_cc_e2e?multiStatements=true&parseTime=true'

go run ./cmd/golang-cc tenant migrate version \
  --dsn 'root@tcp(127.0.0.1:3306)/golang_cc_e2e?multiStatements=true&parseTime=true' \
  --json
```

迁移结果：

```json
{
  "version": 1,
  "dirty": false,
  "applied": true
}
```

启动 API server：

```bash
GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_e2e?multiStatements=true&parseTime=true' \
  go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token
```

通过 `curl` 验证：

- `GET /tenant/context`
- `PATCH /tenant/user`
- `GET /tenant/user`
- `POST /tenant/memories`
- `GET /tenant/memories?category=profile&limit=5`
- `POST /tenant/documents`
- `GET /tenant/documents?type=CLAUDE.md`
- `GET /tenant/documents?type=CLAUDE.md&history=true&limit=5`
- `POST /tenant/sessions`
- `POST /tenant/messages`
- `GET /tenant/sessions?limit=5`
- `GET /tenant/messages?session_id=1&limit=5`

## 数据库确认

```text
tenants: yutang, aihe, gongtong
tenant_users: tenant_id=1 user_key=user-e2e-1 email=user-e2e@example.com user_info_json={"lang": "go", "tier": "e2e"}
tenant_user_memories: pref.lang profile Go importance=9
tenant_user_documents: CLAUDE.md v1 inactive, CLAUDE.md v2 active
tenant_sessions: sess-e2e-1 E2E Session
tenant_session_messages: turn_index=1 role=user content=hello trace_id=trace-e2e-002
```

## 日志确认

API middleware 日志包含：

- `traceid=trace-e2e-002`
- `userid=user-e2e-1`
- `tenantkey=yutang`
- `action`
- `function`

service/repository 日志也包含相同 trace/user/tenant/action/function 字段，例如 `tenant.Service.SaveCurrentUser`、`mysql.Repository.GetUser`、`tenant.Service.ListDocuments`、`mysql.Repository.ListDocuments`、`tenant.Service.UpsertMessage`、`mysql.Repository.UpsertMessage`。

## 自动化 E2E 测试

普通 `go test ./...` 不会误连本机数据库。需要真实 MySQL 验证时，先创建独立测试库，再显式设置 `GOLANG_CC_MYSQL_E2E_DSN`：

```bash
mysql -uroot -e 'DROP DATABASE IF EXISTS golang_cc_e2e_codex; CREATE DATABASE golang_cc_e2e_codex CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'

GOLANG_CC_MYSQL_E2E_DSN='root@tcp(127.0.0.1:3306)/golang_cc_e2e_codex?multiStatements=true&parseTime=true' \
  go test ./internal/tenant -run TestMySQLE2ETenantServiceLifecycle -count=1

GOLANG_CC_MYSQL_E2E_DSN='root@tcp(127.0.0.1:3306)/golang_cc_e2e_codex?multiStatements=true&parseTime=true' \
  go test ./internal/server -run TestMySQLE2ETenantAPILifecycle -count=1

GOLANG_CC_MYSQL_E2E_DSN='root@tcp(127.0.0.1:3306)/golang_cc_e2e_codex?multiStatements=true&parseTime=true' \
  go test ./internal/server -run TestMySQLE2EMobileChatLifecycle -count=1

GOLANG_CC_MYSQL_E2E_DSN='root@tcp(127.0.0.1:3306)/golang_cc_e2e_codex?multiStatements=true&parseTime=true' \
  go test ./internal/server -run TestMySQLE2ETenantSkillsRuntime -count=1 -v
```

该测试会自动执行 migration，并通过 GORM-backed repository + tenant service 验证：

- 当前用户保存/读取。
- memory 写入/列表查询。
- CLAUDE.md 文档保存/active 读取。
- profile 保存/读取，并校验关联 session。
- session/message 写入和 trace id 落库。
- agent task 创建、事件追加、取消和事件回放。
- audit log 写入和 owner/admin RBAC 查询。

`TestMySQLE2ETenantAPILifecycle` 会复用同一套真实 MySQL migration/repository/service，但通过 Gin handler 执行 tenant API 请求，验证：

- `PATCH /tenant/user` 保存 owner 用户。
- `POST /tenant/sessions` 创建真实 session。
- `POST /tenant/messages` 写入真实 message 并保留 trace id。
- `GET /tenant/sessions` / `GET /tenant/messages` 读取刚写入的数据。
- `GET /tenant/audit` 通过 RBAC 读取写操作 audit log。
- `POST /query` 自动创建 session、调用 title function、写入 user/assistant message，并通过 tenant API 读回。

`TestMySQLE2EMobileChatLifecycle` 会通过真实 MySQL + mobile JWT + Gin handler 验证：

- `POST /mobile/chat/sessions` 创建真实 mobile session。
- `POST /mobile/chat/sessions/{id}/messages/stream` 走 SSE 流式响应并写入 user/assistant message。
- `GET /mobile/chat/sessions/{id}/messages` 读回刚写入的数据、message key 和 trace id。

`TestMySQLE2ETenantSkillsRuntime` 会通过真实 MySQL + GORM repository + tenant service + query runtime 验证：

- 写入当前 tenant/user 的 enabled tenant skill。
- chat prompt 只注入 tenant skill metadata，不泄漏 `content_md` 正文。
- `Skill` tool 优先从 tenant effective skill 加载完整 `content_md`。
- tenant skill frontmatter 的 `effort` 会进入 active skill runtime。
- runtime telemetry 会记录 active skill source/version/fallback。
- session message 写入后可通过 service 和 SQL join 读回，并确认 skill content 与 assistant message 均落库。

2026-06-26 本机验证使用隔离库：

```bash
mysql -uroot -e 'DROP DATABASE IF EXISTS golang_cc_tenant_skill_e2e; CREATE DATABASE golang_cc_tenant_skill_e2e CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'

GOLANG_CC_MYSQL_E2E_DSN='root@tcp(127.0.0.1:3306)/golang_cc_tenant_skill_e2e?multiStatements=true&parseTime=true&loc=UTC&charset=utf8mb4' \
  go test ./internal/server -run TestMySQLE2ETenantSkillsRuntime -count=1 -v
```

真实 server curl smoke：

```bash
GOLANG_CC_TENANT_SKILLS_SMOKE_BASE_URL=http://127.0.0.1:18080 \
GOLANG_CC_TENANT_SKILLS_SMOKE_AUTH_TOKEN=test-token \
GOLANG_CC_TENANT_SKILLS_SMOKE_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_webui_local?parseTime=true&loc=UTC&charset=utf8mb4' \
  scripts/tenant-skills-runtime-smoke.sh
```

默认 smoke 不强制跑模型 query；如本地 server 已配置可用模型 provider，可追加 `GOLANG_CC_TENANT_SKILLS_SMOKE_QUERY=1` 验证 `/query` SSE、session 和 message 落库。
