# WebUI Mobile Chat Lab Technical Plan

本文档定义 Go Claude WebUI 一期技术路线。目标不是先做通用管理后台或 ChatGPT clone，而是提供一个可快速验证 API Server / Mobile Chat 全链路的浏览器测试台。

## 目标

P0 WebUI 用于复刻移动端聊天主流程，并把会话、消息、skills、memory、profile、trace 和 telemetry 放在同一个调试界面里，便于快速验证：

- 新建会话、会话列表、消息历史。
- `/mobile/chat/.../messages/stream` SSE 流式聊天。
- 停止生成、regenerate、branch conversation。
- 自动压缩、skills 渐进式加载、prompt cache、token usage 通过 trace/timeline 验证。
- 长期记忆、短期会话上下文、用户画像和 tenant documents。
- skills 列表、effective skills、tenant skill 配置。

## 非目标

- P0 不实现完整登录、SSO、RBAC 管理台。
- P0 不替换现有 `/trace` Go template 页面。
- P0 不 fork Open WebUI 或 LibreChat 的后端产品架构。
- P0 不改变 TUI、slash commands、权限审批、复制、鼠标滚动、图片粘贴等本地交互链路。

## 技术栈

前端独立放在 `web/`：

- Vite + React + TypeScript。
- assistant-ui 作为聊天交互参考和后续 runtime adapter 目标。
- shadcn/ui 风格的本地组件，避免自造基础控件。
- TanStack Query 管理 server state。
- OpenAPI 生成 TypeScript schema，减少接口漂移。

后端保持 `internal/server` Gin API Server 架构。P0 开发态由 Vite proxy 转发：

```text
web dev server -> /api/* -> Go API Server
```

P1 再增加生产构建托管策略：

```text
web build -> static assets -> Go server static mount or release embed copy
```

## API 分层

P0 主链路优先使用 Mobile Chat API：

- `POST /mobile/chat/sessions`
- `GET /mobile/chat/sessions`
- `GET /mobile/chat/sessions/{id}`
- `PATCH /mobile/chat/sessions/{id}`
- `DELETE /mobile/chat/sessions/{id}`
- `GET /mobile/chat/sessions/{id}/messages`
- `POST /mobile/chat/sessions/{id}/messages/stream`
- `POST /mobile/chat/sessions/{id}/messages/{message_id}/cancel`
- `POST /mobile/chat/sessions/{id}/messages/{message_id}/regenerate`
- `POST /mobile/chat/sessions/{id}/branch`
- `POST /mobile/chat/attachments/presign`

Tenant API 作为侧边测试能力：

- `GET/POST /tenant/memories`
- `GET/POST /tenant/profile`
- `GET/POST /tenant/documents`
- `GET /tenant/effective-skills`
- `GET/POST /tenant/skills`
- `GET /tenant/telemetry`

Trace API 作为链路验证能力：

- `GET /trace/api/sessions?source=tenant`
- `GET /trace/api/sessions/{id}?source=tenant`

## 鉴权边界

P0 前端不做登录产品功能，但代码必须预留鉴权接口：

- `IdentityProvider` 提供 tenant/user/device/model。
- `AuthProvider` 负责生成请求 headers。
- 默认 `DevAuthProvider` 使用本地调试配置。
- 如果 server 配置了 `--auth-token`，前端通过 `Authorization: Bearer ...` 访问普通 tenant/trace API。
- 如果 server 配置了 mobile JWT，前端通过用户粘贴的 JWT 访问 `/mobile/chat/*`。
- P0 本地调试可开启 `GOLANG_CLAUDE_CODE_MOBILE_DEV_AUTH=true`，让 `/mobile/chat/*` 在没有 JWT secret 时读取 `X-Tenant-Key`、`X-User-Id`、`X-Device-Id`。该开关默认关闭，不能用于生产。

后续可替换为 JWT 登录、OIDC、企业 SSO 或自建 session，不影响业务页面。

## P0 页面

- Chat Lab：会话列表、创建会话、发送消息、SSE 输出、停止、regenerate、branch。
- Context Panel：base URL、token/JWT、tenant、user、device、model、当前 session。
- Memory Panel：读取/写入长期记忆。
- Profile Panel：读取/写入用户画像。
- Skills Panel：查看 effective skills 和 tenant skills。
- Trace Panel：读取当前 session 的 normalized trace summary，并保留打开 `/trace` 的入口。

## 验收

- `npm --prefix web run build` 通过。
- 关键 TypeScript 类型检查通过。
- Go 相关测试至少运行 `go test ./internal/server -count=1`。
- `git diff --check` 通过。
- 手动验证时启动 API Server 和 WebUI：

```bash
go run ./cmd/golang-cc server --host 127.0.0.1 --port 8080 --auth-token test-token
npm --prefix web run dev
```

浏览器打开 Vite 输出的本地地址，通过 Context Panel 配置 API token/JWT 后测试 mobile chat 主链路。
