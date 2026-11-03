# Production Deploy Checklist

本文档用于多租户 API Server / Mobile Chat / WebUI 上线前检查。

## 数据库

- 使用独立 MySQL 数据库和最小权限账号，不复用 root。
- **执行 migration 之前**先按 [mysql_fulltext_ngram.md](mysql_fulltext_ngram.md) 配好 `ngram_token_size` 并重启 MySQL，否则中文知识库检索会静默退化成 LIKE 子串匹配。
- 上线前执行：

```bash
go run ./cmd/niuma tenant migrate version --json
go run ./cmd/niuma tenant migrate up
go run ./cmd/niuma tenant migrate version --json
```

- 确认关键表存在：`tenants`、`tenant_users`、`tenant_skills`、`tenant_sessions`、`tenant_session_messages`、`tenant_audit_logs`、`tenant_telemetry_events`。
- 配置定期备份，至少覆盖 tenant skills、sessions/messages、audit、telemetry 和 goal/agent task 表。

## Server 配置

- `NIUMA_MYSQL_DSN` 或 `MYSQL_DSN` 指向生产 MySQL。
- `--auth-token` 使用高强度 token，不能使用示例值。
- `NIUMA_MOBILE_JWT_SECRET` 或 `MOBILE_JWT_SECRET` 必须配置；生产环境不要开启 `NIUMA_MOBILE_DEV_AUTH=true`。
- 如启用 WebUI，确认 `/webui/` 仅暴露在受控网络或受统一鉴权保护的入口后。
- 生产日志不应记录 JWT、API key、完整聊天正文、附件私有 URL 或语音全文。

## Mobile 与附件

- 配置 mobile policy：允许模型、每分钟限流、日消息配额、日 token 配额。
- 如需要真实上传，配置 S3/OSS/MinIO-compatible presign：
  - `NIUMA_MOBILE_S3_BUCKET`
  - `NIUMA_MOBILE_S3_REGION`
  - `NIUMA_MOBILE_S3_ENDPOINT`
  - `NIUMA_MOBILE_S3_ACCESS_KEY_ID`
  - `NIUMA_MOBILE_S3_SECRET_ACCESS_KEY`
  - `NIUMA_MOBILE_S3_PUBLIC_BASE_URL`
- 当前未内置 APNs/FCM、服务端二进制代理上传、病毒扫描/转码和套餐计费；如生产需要，应接入外部服务后再开放对应产品能力。

## 验收

基础验收：

```bash
NIUMA_PREPROD_BASE_URL=https://your-api.example.com \
NIUMA_PREPROD_AUTH_TOKEN="$AUTH_TOKEN" \
NIUMA_PREPROD_MYSQL_DSN="$NIUMA_MYSQL_DSN" \
  scripts/tenant-preprod-acceptance.sh
```

真实模型 runtime 验收：

```bash
NIUMA_TENANT_SKILLS_SMOKE_QUERY=1 \
NIUMA_TENANT_SKILLS_SMOKE_BASE_URL=https://your-api.example.com \
NIUMA_TENANT_SKILLS_SMOKE_AUTH_TOKEN="$AUTH_TOKEN" \
NIUMA_TENANT_SKILLS_SMOKE_MYSQL_DSN="$NIUMA_MYSQL_DSN" \
  scripts/tenant-skills-runtime-smoke.sh
```

该验收需要 server 已配置可用模型 provider。未配置 provider 时只运行基础验收，不宣称 runtime 模型链路已通过。脚本支持 `/query` JSON 与 SSE 返回形态；JSON 模式会检查 `Skill` tool call 和 tenant skill 正文，SSE 模式会检查 stream 事件。

## 回滚

- 代码回滚：回退到上一稳定 commit 并重启 server。
- 数据回滚：优先使用 MySQL 备份恢复；不要直接手工删除 tenant session/message 行。
- Skill 内容回滚：优先使用 `POST /tenant/skills/rollback` 复制历史版本生成新的最新版本。
- Migration 回滚只在确认 down 脚本安全且有备份后执行。

## 上线后观察

- `/health` 可用。
- `/tenant/telemetry` 中 `api.request.finished`、`mobile.chat.stream.finished`、`query.run` 错误率正常。
- tenant audit 中可看到 skill/session/message/admin 写操作。
- MySQL 慢查询关注 `tenant_sessions`、`tenant_session_messages`、`tenant_telemetry_events` 的分页和搜索查询。
