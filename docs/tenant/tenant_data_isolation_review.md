# Tenant Data Isolation Review

本文档记录 2026-06-26 对多租户数据隔离的专项检查结果。

## 范围

- Tenant skills：`tenant_skills`
- User skill overrides：`tenant_user_skill_overrides`
- Sessions/messages：`tenant_sessions`、`tenant_session_messages`
- Mobile chat session/message API
- `/query` 与 `/v1/chat/completions` 自动落库路径
- Audit/telemetry：`tenant_audit_logs`、`tenant_telemetry_events`
- Goal / agent task tenant API

## 结论

- `tenant_skills` 的 list/get/upsert/rollback 均带 `tenant_id`；rollback 在事务内锁当前租户最新版本后插入新版本。
- `tenant_user_skill_overrides` 的 list/get/upsert 均带 `tenant_id + user_id`，并 join 当前 tenant skill。
- `tenant_sessions` 的 list/get/update/archive 均带 `tenant_id + user_id`。
- `tenant_session_messages` 的 list 带 `tenant_id + user_id + session_id`。
- `tenant_audit_logs` 与 `tenant_telemetry_events` 的读取均按 `tenant_id` 限定，写入从当前 request context 解析 tenant/user。
- Mobile chat、`/query`、`/v1/chat/completions` 都通过 `tenant.Service` 进行 session/message 持久化。

## 已修复问题

### Message 写入前缺少 session ownership 验证

风险：

- `tenant_session_messages` 的唯一键是 `(session_id, turn_index)`。
- 旧实现会把当前 `tenant_id/user_id` 写进 message，但没有先验证传入 `session_id` 是否属于当前 tenant/user。
- 如果调用方猜到其他租户或用户的 `session_id`，理论上可能通过相同 `turn_index` 触发 upsert 覆盖目标 session 的 message 行。

修复：

- `tenant.Service.UpsertMessage` 写入前调用 `repo.GetSession(ctx, tenantID, userID, sessionID)`。
- `mysql.Repository.UpsertMessage` 和 `mysql.GormRepository.UpsertMessage` 也增加同样的 session ownership 防御，避免未来内部调用绕过 service。
- 非当前 tenant/user 的 session 返回 `mysqlstore.ErrNotFound`，不执行 message insert/upsert。

覆盖测试：

- `TestServiceUpsertMessageUsesResolvedIDs`
- `TestServiceUpsertMessageRejectsForeignSession`
- `TestRepositoryUpsertMessageUsesContextTraceID`
- `TestRepositoryUpsertMessageRejectsForeignSession`
- `TestGormRepositoryUpsertMessageUsesContextTraceID`
- `TestGormRepositoryUpsertMessageRejectsForeignSession`

### 真实 MySQL 跨租户隔离 E2E

新增 `TestMySQLE2ETenantDataIsolation`，使用真实 MySQL migration + GORM repository + HTTP handler 覆盖：

- tenant A 创建 skill、session、message。
- tenant B 读取 tenant A `session_id` 的 messages 返回空列表，不泄露内容。
- tenant B 使用 tenant A `session_id` 写 message 返回 404。
- tenant A 下另一个 user 使用 owner user 的 `session_id` 写 message 返回 404。
- tenant B 读取 tenant A skill 返回 404，effective skills 不包含 tenant A skill。
- DB 直查确认 tenant A 原 message 行没有被覆盖，tenant B 没有生成 tenant A 的 skill 行。

新增 `TestMySQLE2ETenantSkillRollbackConcurrent`，使用两个 goroutine 同时 rollback 同一个历史 skill 版本，验证事务锁下最终生成连续版本 `1,2,3,4`，且 v3/v4 都复制自目标历史版本。

## 剩余风险

- `FinishAgentTask(ctx, taskID, ...)` 是运行时内部 completion path，目前只按 task id 更新；外部 API 的 get/update/cancel/list/events 都带 `tenant_id + user_id`。该内部路径依赖 task id 由当前 runtime 创建并传递，不应暴露给 HTTP 调用方。
- `tenant_audit_logs` 和 `tenant_telemetry_events` 是租户级管理员可见资源，不按普通 user 隔离；这符合当前 owner/admin 审计模型。
- 真实生产仍需配合 MySQL 账号最小权限、备份、日志脱敏和 mobile JWT secret 管理。

## 验证命令

```bash
go test ./internal/storage/mysql -run 'Test.*UpsertMessage|TestGormRepositoryListMessages|TestRepositoryListMessages' -count=1
go test ./internal/tenant -run 'TestServiceUpsertMessage|TestServiceListSessionsAndMessages|TestServiceSessionLifecycle' -count=1
go test ./internal/server -run 'TestTenantSession|TestTenantMessages|TestMobileChat|TestOpenAI|TestQueryEndpointPersistsTenantSession' -count=1
scripts/tenant-mysql-e2e.sh
```
