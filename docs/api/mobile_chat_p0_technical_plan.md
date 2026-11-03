# Mobile Chat SSE P0 Technical Plan

## 背景与目标

目标是在现有 Go Claude server、tenant service、MySQL session/message 持久化之上，提供 iOS/Android 可直接对接的 ChatGPT-like 移动端聊天后端 P0 能力。

P0 重点不是替换现有 OpenAI-compatible API，而是增加一层稳定的移动端 API contract：移动端使用自己的鉴权、会话、消息、SSE 事件协议和状态模型；后端内部继续复用 query loop、tenant session/message、provider fallback、日志和审计能力。

## 需求范围与不做范围

P0 范围：

- 移动端 Bearer JWT 鉴权，JWT claims 映射为 `tenant_key`、`user_id`、`device_id`。
- 移动端专用 session API：创建、列表、详情、更新、归档。
- 移动端专用 message API：分页读取、发送并 SSE 流式返回。
- SSE 事件协议固定：`message_start`、`delta`、`message_stop`、`error`。
- message 状态模型：`pending`、`streaming`、`completed`、`failed`、`cancelled`。
- 移动端重试幂等：客户端传入 `message_key` 后，重复 stream 请求会复用已落库 assistant message 并重放 SSE。
- cursor 分页：message list 同时支持 `after_turn` 和 `cursor`，响应返回 `next_cursor/has_more`。
- cancel endpoint：停止活跃 stream 并把目标 assistant message 标记为 `cancelled`。
- 附件上传签名契约：服务端生成 `attachment_id/object_key/upload_url`，客户端上传后把返回的 attachment metadata 放入 stream request。
- S3-compatible 对象存储签名：配置 S3/OSS/MinIO endpoint/bucket/key 后用 AWS SDK v2 生成 presigned PUT URL。
- 可持久化限流/配额 store：默认进程内 limiter；配置 file store 后跨进程重启保留计数。
- P1 regenerate / branch conversation：支持 assistant message 重新生成，以及从指定 turn/message fork 新 session。
- 移动端断线后可通过 message list 读取最终状态和历史内容。
- WebSocket 多端在线同步：同一 tenant/user/session 的已订阅设备接收实时 `message_event`，并可通过控制通道 cancel 活跃 stream。
- Redis quota store：多实例 API server 共享 minute/day message/token 原子计数。

暂不做：

- APNs/FCM 推送。
- 内置对象存储二进制代理上传、病毒扫描和 CDN 回源。
- 订阅计费、套餐售卖。

## 业务模块拆解

- Auth：移动端 token 校验与请求上下文注入。
- Chat Session：会话创建、列表、详情、更新标题/状态、归档。
- Chat Message：消息分页查询、用户消息写入、assistant 流式生成、最终落库。
- SSE Stream：稳定事件 envelope、错误事件、完成事件。
- Observability：traceid、tenantkey、userid、device_id、session_id、message_id 日志字段。

## B端/C端功能拆解

C端移动端：

- 查看会话列表。
- 新建会话或进入既有会话。
- 拉取会话消息。
- 发送消息并接收 SSE token。
- 停止/失败后重新拉取消息状态。
- 更新标题、删除/归档会话。

B端管理端：

- 继续复用现有 `/tenant/users`、`/tenant/audit`、tenant RBAC。
- P1 已增加移动端用户模型权限、限流、日配额、Redis 共享 quota、regenerate 和 branch conversation；后续可接入 B 端管理页、套餐和 MySQL billing ledger。

## 数据模型与 MySQL 设计

当前 `tenant_sessions` 已有 `status`、`title`、`archived_at`，可支撑 session lifecycle。

当前 `tenant_session_messages` 已有 `role`、`content`、`content_json`、`trace_id`，P0 先用 `content_json` 保存移动端扩展状态，避免立即做破坏性 migration：

```json
{
  "mobile": {
    "message_key": "msg_xxx",
    "status": "completed",
    "device_id": "ios-device-1",
    "error": "",
    "attachments": [
      {
        "type": "image",
        "media_type": "image/png",
        "name": "screenshot.png",
        "url": "https://cdn.example.test/screenshot.png",
        "size_bytes": 123456
      }
    ]
  }
}
```

后续 migration 可把 `message_key/status/device_id` 和 attachment 元数据提升为独立列，增加 `(tenant_id,user_id,session_id,created_at)` 游标索引和唯一消息键。

## 分层技术设计

- API/controller：`internal/server` 新增 `/mobile/chat/...` handlers。
- Auth helper：`internal/server` 解析 HS256 JWT，校验 `exp`，读取 `tenant_key/user_id/device_id`。
- Service：复用 `tenant.Service` 的 session/message 方法。
- Repository：P0 复用现有 GORM/raw repository。
- Stream：移动端 SSE 使用独立 envelope，不复用 OpenAI chunk schema。

## API 设计

所有移动端 API 使用：

```http
Authorization: Bearer <JWT>
```

JWT HS256 claims：

```json
{
  "tenant_key": "yutang",
  "user_id": "user-123",
  "device_id": "ios-abc",
  "exp": 1799999999,
  "allowed_models": ["claude-sonnet-4-6"],
  "rate_limit_per_minute": 30,
  "daily_message_quota": 1000,
  "daily_token_quota": 200000
}
```

Endpoints：

- `POST /mobile/chat/sessions`
- `GET /mobile/chat/sessions?limit=50`
- `GET /mobile/chat/sessions/{id}`
- `PATCH /mobile/chat/sessions/{id}`
- `DELETE /mobile/chat/sessions/{id}`
- `POST /mobile/chat/sessions/{id}/branch`
- `GET /mobile/chat/sessions/{id}/messages?limit=50&cursor=0`
- `POST /mobile/chat/sessions/{id}/messages/stream`
- `POST /mobile/chat/sessions/{id}/messages/{message_id}/cancel`
- `POST /mobile/chat/sessions/{id}/messages/{message_id}/regenerate`
- `POST /mobile/chat/attachments/presign`
- `GET /mobile/chat/ws`

Stream request：

```json
{
  "content": "hello",
  "model": "claude-sonnet-4-6",
  "message_key": "client-generated-id",
  "attachments": [
    {
      "type": "voice",
      "media_type": "audio/m4a",
      "name": "voice.m4a",
      "url": "https://cdn.example.test/voice.m4a",
      "transcript": "voice transcript"
    }
  ]
}
```

SSE event envelope：

```json
{"type":"message_start","session_id":1,"message_id":2,"message_key":"msg_xxx","status":"streaming"}
{"type":"delta","delta":"hello"}
{"type":"message_stop","session_id":1,"message_id":2,"status":"completed"}
{"type":"error","status":"failed","error":"..."}
```

## 核心流程与状态流转

发送消息：

1. 校验 JWT，注入 tenant/user/device 到 context。
2. 校验 session 属于当前 tenant/user。
3. 校验模型权限、限流、日消息配额、日 token 配额和附件策略。
4. 写入 user message，状态 `completed`，附件 metadata 保存在 `content_json.mobile.attachments`。
5. 写入 assistant placeholder，状态 `streaming`。
6. 调用 query stream，边生成边发送 `delta`。
7. 成功后更新 assistant message 为 `completed` 和完整 content。
8. 如果 session 尚无标题，后台异步生成标题并回写 session。
9. 失败后更新 assistant message 为 `failed`，返回 `error` SSE。
10. 客户端重复提交同一个 `message_key` 时，server 不重复写 user/assistant message，而是重放既有 assistant SSE。

状态流转：

- user message：`pending -> completed`。
- assistant message：`pending -> streaming -> completed`。
- 异常：`pending/streaming -> failed`。
- 主动停止：`streaming -> cancelled`，cancel endpoint 会调用进程内 stream registry 的 cancel func；如果 stream 已结束，也会把目标 message 落库标记为 `cancelled`。

## 渐进式开发计划

P0.1：

- 文档和移动端 API contract。
- JWT 校验 helper。
- session lifecycle mobile endpoints。
- message list mobile endpoint。

P0.2：

- message stream endpoint。
- SSE envelope。
- message status 写入 `content_json`。
- 单元测试覆盖 auth、session list/get/update/delete、message stream。

P0.3：

- cancel endpoint、cursor pagination、幂等 message key。
- 附件上传 presign API。
- 可选 file-backed usage store，用于跨进程重启持久化 rate/quota 计数。

P1.1：

- 异步 AI 标题生成，不阻塞 SSE 主链路。
- 用户模型权限、每分钟限流、日消息配额、日 token 配额。
- 附件 metadata 支持图片、语音/音频和普通文件，随消息持久化并注入模型上下文。
- regenerate assistant message。
- branch conversation 到新 session。
- WebSocket 多端同步：同一 tenant/user/session 的已订阅设备会收到 `message_event`，并可通过 WS 控制通道取消活跃 stream。
- S3-compatible presign：`upload_url` 可由 AWS SDK v2 生成真实对象存储 PUT 签名，`attachment.url` 可指向 CDN/public base URL。

当前状态：

- P0.1/P0.2/P0.3：已实现并有 `internal/server` 单元测试覆盖。
- P1.1：异步标题、用户模型权限/限流/配额、附件 metadata、S3-compatible presign、regenerate、branch、WebSocket 多端协同已实现并有单元测试覆盖。
- 仍未纳入当前移动端 P0：APNs/FCM、服务端二进制代理上传、病毒扫描、套餐计费。

## 风险点、兼容性与降级策略

- JWT secret 未配置时移动端 API 应返回 503，避免误把内部 `--auth-token` 当用户登录态。
- `content_json` 承载状态是兼容性优先方案；后续独立列 migration 时需兼容旧 JSON。
- SSE 断线时服务端仍可能完成生成，客户端应重新拉取 messages。
- AI title 生成不应阻塞主聊天链路；失败时回退 prompt title。
- 默认限流/配额是 server 进程内运行时策略；配置 `GOLANG_CLAUDE_CODE_MOBILE_USAGE_STORE_PATH` 后会使用 file-backed store 持久化计数。多实例强一致仍建议接 Redis/MySQL counter。
- 当前附件能力提供上传签名契约和 metadata 注入；`GOLANG_CLAUDE_CODE_MOBILE_UPLOAD_BASE_URL` 可指定 upload URL 前缀。真实 S3/OSS SDK 签名、病毒扫描、对象转码属于后续存储层增强。

## 测试与验收清单

- 无 JWT、错误 JWT、过期 JWT 返回 401。
- 正确 JWT 注入 tenant/user/device，上下文隔离生效。
- session 创建、列表、详情、更新、归档只作用于当前用户。
- message list 只读取当前 session。
- stream 输出 `message_start`、`delta`、`message_stop`。
- stream 失败输出 `error`，并把 assistant message 标记为 `failed`。
- 空标题 session 在 stream 成功后异步回写 AI 标题。
- 禁止模型返回 403，限流返回 429，配额耗尽返回 402。
- 图片/语音/文件附件写入 `content_json`，并传入 query prompt。
- `message_key` 重复提交不会重复生成。
- `cursor/next_cursor/has_more` 分页行为正确。
- cancel endpoint 标记 `cancelled`，活跃 stream 会收到 context cancel。
- regenerate 从上一条 user message 重新生成 assistant variant。
- branch 按 `until_turn` 或 `until_message_id` 复制历史消息到新 session。
- attachment presign 返回 `attachment_id/object_key/upload_url`。
- file-backed usage store 重启后仍能识别日配额。
- `go test ./internal/server ./internal/tenant ./internal/storage/mysql` 通过。
