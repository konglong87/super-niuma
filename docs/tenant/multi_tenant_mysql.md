# 多租户 MySQL Schema

本 schema 是 golang-cc 的多租户持久化底座，第一版先覆盖租户、用户、skills、memory、profile、CLAUDE.md/soulmd/userInfo 文档、会话、消息和审计日志。

## 隔离策略

- 所有用户态数据都带 `tenant_id` 和 `user_id`，业务查询必须同时使用租户和用户条件。
- 租户唯一入口是 `tenants.tenant_key`，当前预置 `yutang`、`aihe`、`gongtong` 三个租户。
- 用户在租户内由 `tenant_users.user_key` 唯一标识，同一个外部用户可以在不同租户拥有独立身份、memory、profile 和会话。
- messages 表保存 `trace_id`，用于把 API 日志中的 `traceid` 和数据库会话消息串起来排查。
- audit log 表保存 `trace_id`、actor user、action 和 resource，租户管理动作可以回溯到请求链路。

## 表设计

| 表 | 用途 |
| --- | --- |
| `tenants` | 租户主表，保存租户 key、名称、状态和配置 JSON |
| `tenant_users` | 租户用户表，保存角色、状态、userInfo 和外部元数据 |
| `tenant_skills` | 租户级 skills 内容、版本和启停配置 |
| `tenant_user_skill_overrides` | 用户级 skill 启停和配置覆盖 |
| `tenant_user_memories` | 用户 memory，支持分类、重要度、来源和 embedding 引用 |
| `tenant_user_profiles` | 用户画像快照，按版本追加保存 |
| `tenant_user_documents` | 用户文档，承载 CLAUDE.md、soulmd、userInfo 等 Markdown/JSON 内容 |
| `tenant_sessions` | 会话主表，保存 session key、模型、cwd 和会话状态 |
| `tenant_session_messages` | 会话消息表，保存 turn、role、内容、工具调用、token 和 trace id |
| `tenant_agent_tasks` | sub-agent 任务表，保存 main/subagent 关系、agent、模型、状态和结果 |
| `tenant_agent_task_events` | sub-agent 生命周期事件表，保存 started、turn、tool、completed/failed 等事件 |
| `tenant_audit_logs` | 租户审计日志，保存 actor、action、resource、metadata 和 trace id |

## 索引和性能

- 高频路径均以 `tenant_id` 开头，避免跨租户扫描。
- 用户维度数据采用 `(tenant_id, user_id, ...)` 组合索引，服务层查询时保持同样的条件顺序。
- 会话消息按 `(session_id, turn_index)` 保证单会话 turn 幂等写入。
- sub-agent task 按 `(tenant_id, user_id, started_at)` 和 parent session 建索引，事件按 `(task_id, created_at)` 回放。
- 最近会话和最近消息分别通过 `last_message_at`、`created_at` 索引支持分页。
- audit log 通过 `(tenant_id, created_at)`、actor、action 和 trace 索引支持租户内追踪。
- JSON 字段只用于配置、元数据和非高频过滤内容；需要高频查询的字段应提升为独立列。

## 迁移文件

- `migrations/mysql/000001_multi_tenant.up.sql`
- `migrations/mysql/000001_multi_tenant.down.sql`

## CLI 执行

迁移执行使用 `go-sql-driver/mysql` 和 `golang-migrate`：

```bash
export GOLANG_CC_MYSQL_DSN="user:pass@tcp(127.0.0.1:3306)/claude_code?multiStatements=true"

go run ./cmd/golang-cc tenant migrate up
go run ./cmd/golang-cc tenant migrate version --json
go run ./cmd/golang-cc tenant migrate down --steps 1
```

可选参数：

- `--dsn` 覆盖 `GOLANG_CC_MYSQL_DSN`。
- `--path` 覆盖默认迁移目录 `migrations/mysql`。
- `--steps` 控制 down 回滚步数，默认 1。
- `--all` 回滚全部迁移。

## Go Repository

`internal/storage/mysql.GormRepository` 是 server runtime 默认使用的多租户数据访问实现，基于 GORM MySQL dialector 复用同一套 migration schema。`internal/storage/mysql.Repository` 保留 raw SQL 实现，用作契约测试对照和回退实现。

- `GetTenantByKey` / `UpsertTenant` / `ListTenants` / `ArchiveTenant`：按租户 key 查租户，并支持管理员创建、更新、列出和软删除租户。
- `EnsureUser`：在租户内解析或创建当前用户身份，只恢复软删除状态，不覆盖 email、display_name、role、status、userInfo 等资料字段。
- `UpsertUser` / `ArchiveUser` / `GetUser` / `ListUsers`：更新、软删除、读取当前用户资料，并按租户列出用户，包括 `user_info_json` 与外部元数据。
- `UpsertSkill` / `ListSkills` / `GetSkill`：写入、列出和读取租户级 skill，支持版本和启停配置。
- skill 版本历史直接保存在 `tenant_skills` 的 `(tenant_id, skill_key, version)` 多版本行中；回滚不会覆盖旧版本，而是复制历史版本内容生成新的最新版本。
- `UpsertSkillOverride` / `ListSkillOverrides` / `GetSkillOverride`：写入、列出和读取当前用户对租户 skill 的启停和 config 覆盖。
- `ListEffectiveSkills` / `GetEffectiveSkill`：读取租户 skill 叠加当前用户 override 后的最终 enabled/config。
- API Server runtime 会把 enabled effective skills 作为 tenant skill provider 注入 query：catalog 只暴露 metadata，`Skill` tool 才加载完整 `content_md`；每轮 query 读取 MySQL，支持热加载。
- `UpsertMemory` / `ListMemories`：写入和读取用户 memory。
- `SaveDocument` / `GetActiveDocument` / `ListDocuments`：保存、读取 active 版本、列出 CLAUDE.md、soulmd、userInfo 等用户文档历史。
- `SaveProfile` / `GetProfile`：保存用户画像快照，并按版本或最新版本读取。
- `UpsertSession` / `ListSessions` / `GetSession` / `UpdateSession` / `ArchiveSession`：写入、列出、读取、更新和软删除当前用户会话。
- `UpsertMessage` / `ListMessages`：写入和读取当前用户指定会话的消息历史，消息默认从 context 取 `traceid`。写入前会验证 `session_id` 属于当前 `tenant_id/user_id`，避免猜测其他租户或用户的 session id 后覆盖消息。
- `CreateAgentTask` / `FinishAgentTask` / `CancelAgentTask` / `GetAgentTaskStatus` / `ListAgentTasks`：记录、取消、轮询和读取 sub-agent task 生命周期，支持与 parent session 关联。
- `AppendAgentTaskEvent` / `ListAgentTaskEvents`：追加和回放 sub-agent started、turn_start、tool_call、tool_result、completed、failed、cancelled 等事件。
- Server 暴露 `/tenant/agent-tasks`、`/tenant/agent-tasks/{id}/events`、`/tenant/agent-tasks/{id}/cancel`，CLI 暴露 `tenant agent-tasks list/events/cancel`，用于移动端/管理端/本地命令查看 sub-agent 执行进度、回放事件和取消运行中 task；运行中的 sub-agent 会轮询持久化 cancel 状态并退出。
- `InsertAuditLog` / `ListAuditLogs`：写入和读取租户审计日志。

Repository 日志复用 `internal/observability`，每个方法都会带 `traceid`、`userid`、`tenantkey`、action、function。迁移仍由 `golang-migrate` 和 `go-sql-driver/mysql` 执行，运行期 CRUD 由 GORM repository 承接。

## Go Service

`internal/tenant.Service` 在 repository 之上做业务编排：

- 从 context 读取 `tenantkey` 和 `userid`，缺失时拒绝写入持久化数据，避免所有请求落到 anonymous/default。
- 先按 `tenantkey` 查租户，再按 `userid` 在租户内 ensure 用户身份，得到数值型 `tenant_id/user_id`，解析身份时不会覆盖已有 userInfo。
- 对外提供 tenant admin、current user、tenant users、tenant skill、user skill override、effective skill、memory、document、profile、session、message、audit 的业务方法，调用方不用直接拼 `tenant_id/user_id`。
- `RequireRole` 支持 owner/admin RBAC；当前用于租户管理、租户用户管理、租户级 skill 写入和 audit 查询。`/tenant/tenants`、`/tenant/users`、`/tenant/audit` 列表支持 offset cursor 分页和搜索过滤，便于管理端审计与回溯。
- 每个 service 方法都通过 `internal/observability` 打印 `traceid`、`userid`、`tenantkey`、action、function。

## 真实 MySQL E2E

本机有 MySQL 时可执行：

```bash
scripts/tenant-mysql-e2e.sh
```

脚本会重建隔离库并运行所有 `TestMySQLE2E*`，覆盖 migration、tenant service lifecycle、API server session/message 和 `/query` 持久化、mobile chat 持久化、tenant skill runtime、goal API、跨租户隔离和 rollback 并发。

## API 端点

配置 `GOLANG_CC_MYSQL_DSN` 或 `MYSQL_DSN` 后，`server` 命令会自动打开 GORM-backed MySQL repository 并挂载多租户 API：

- `GET /tenant/context`：解析当前 `X-Tenant-Key` + `X-User-Id`。
- `GET /tenant/tenants?limit=100`：管理员列出租户。
- `POST /tenant/tenants` / `PATCH /tenant/tenants`：管理员按 `tenant_key` 创建或更新租户的 `name`、`status`、`settings_json`。
- `DELETE /tenant/tenants?tenant_key=acme`：管理员软删除租户，状态置为 `archived`。
- `GET /tenant/user`：读取当前用户资料，包括 `email`、`display_name`、`role`、`status`、`user_info_json`、`metadata_json`。
- `POST /tenant/user` / `PATCH /tenant/user`：保存当前用户资料；未提交的字段会保留现有值，适合只更新 userInfo。
- `GET /tenant/users?limit=100`：列出当前租户用户，用于验证和管理一个租户下多个用户；需要 `owner/admin`。
- `POST /tenant/users` / `PATCH /tenant/users`：管理员创建或更新租户内指定 `user_key` 的用户资料。
- `DELETE /tenant/users?user_key=user-two`：管理员软删除租户用户，状态置为 `archived`。
- `GET /tenant/skills?enabled=true&limit=100`：读取当前租户 skill 列表。
- `GET /tenant/skills?key=go-review&version=2`：读取当前租户指定 skill；`version` 可省略，默认取最新版本。
- `POST /tenant/skills`：写入或更新当前租户 skill，必填 `skill_key`、`name`、`content_md`，可选 `version`、`enabled`、`config_json`。
- `POST /tenant/skills/rollback`：复制指定历史版本生成新的最新版本，必填 `skill_key`、`version`，可选 `target_version`；`target_version` 必须大于当前最新版本，否则返回 400；需要 `owner/admin`。
- `GET /tenant/skill-overrides?limit=100`：读取当前用户的 skill 覆盖列表。
- `GET /tenant/skill-overrides?skill_key=go-review&version=2`：读取当前用户对指定 skill 的覆盖；`version` 可省略。
- `POST /tenant/skill-overrides`：写入或更新当前用户对某个 skill 的覆盖，必填 `skill_key`，可选 `version`、`enabled`、`config_json`。
- `GET /tenant/effective-skills?enabled=true&limit=100`：读取租户 skill 叠加当前用户 override 后的最终列表。
- `GET /tenant/effective-skills?key=go-review&version=2`：读取指定 skill 对当前用户的最终 enabled/config；`version` 可省略。
- `GET /tenant/memories?category=&limit=`：读取当前用户 memory。
- `POST /tenant/memories`：写入当前用户 memory。
- `GET /tenant/memory-review/candidates?limit=50&candidate_type=explicit_remember&risk_status=low_risk&source_session_id=123`：读取显式 remember 和 AutoMem 的 pending 候选，支持按候选类型、风险状态和来源 session 过滤；pending 不进入 prompt。
- `POST /tenant/memory-review/review`：approve/reject/archive 待审 memory；approve 后写入 active user memory。
- `GET /tenant/documents?type=CLAUDE.md`：读取当前用户指定类型的 active 文档。
- `GET /tenant/documents?type=CLAUDE.md&history=true&limit=100`：读取当前用户指定类型的文档历史；省略 `type` 时列出当前用户所有文档类型。
- `POST /tenant/documents`：保存当前用户文档。
- `GET /tenant/profile?version=2`：读取当前用户画像；`version` 可省略，默认读取最新版本。
- `POST /tenant/profile`：保存当前用户画像快照，必填 `profile_json`，可选 `profile_version`、`summary`、`generated_from_session_id`。
- `GET /tenant/sessions?limit=100`：读取当前用户 session 列表。
- `POST /tenant/sessions`：创建或更新当前用户 session。
- `GET /tenant/sessions/{id}`：读取当前用户指定 session。
- `PATCH /tenant/sessions/{id}` / `PUT /tenant/sessions/{id}`：更新当前用户 session 的 title、status、model、cwd、metadata_json。
- `DELETE /tenant/sessions/{id}`：软删除当前用户 session，状态置为 `archived`。
- `GET /tenant/sessions/{id}/timeline?limit=200&trace_limit=100&task_limit=500`：owner/admin 查看指定 session 的完整链路时间线，按 trace 关联消息、审计、遥测、sub-agent task 和 task event。
- `GET /tenant/messages?session_id=123&limit=100`：读取当前用户指定 session 的消息历史。
- `POST /tenant/messages`：写入 session message，默认继承 `X-Trace-Id`。
- `GET /tenant/tenants?limit=100&cursor=0&search=...`：管理员分页搜索租户列表。
- `GET /tenant/users?limit=100&cursor=0&search=...`：管理员分页搜索当前租户用户。
- `GET /tenant/audit?limit=100&cursor=0&search=...`：管理员分页搜索当前租户 audit log。

所有端点都复用 `X-Trace-Id`、`X-User-Id`、`X-Tenant-Key`，并按相同字段输出结构化日志。

`POST /query` 和 `/v1/chat/completions` 在带 `X-Tenant-Key` 和 `X-User-Id` 时，会自动创建/更新 tenant session，并写入 user/assistant 两条 message。`/query` 请求体可选传 `session_key`；OpenAI-compatible 接口可传 `X-Session-Key`；未传时使用 `X-Trace-Id` 作为 session key。server runtime 会用当前大模型为自动持久化的 session 生成简短标题，失败时回退为首条 prompt 截断标题。SSE 流式响应会缓存已发送 token，并将实际流出的 assistant 文本落到 message content。

同一运行链路会读取当前 tenant/user 的 enabled effective skills：

- 租户级 skill 写入 `/tenant/skills`，用户级启停覆盖写入 `/tenant/skill-overrides`。
- runtime 按 `skill_key` 暴露和加载 tenant skill；frontmatter `name` 不改变 tenant API 调用名，避免跨租户或版本歧义。
- chat mode 使用 tenant skills，不读取服务端本地 `.claude/skills`；code mode 会继续保留本地 filesystem skills 作为补充。
- 更新 skill 或 override 后不需要重启 server，下个请求会重新读取 MySQL effective skills。
