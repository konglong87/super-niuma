# API Server

## WebUI 2.0 全局设置中心

以下接口沿用服务端 `Authorization: Bearer <auth-token>` 管理权限，仅修改全局 `settings.json`，不扩展 Mobile JWT 权限。未配置 token 的回环本机模式保留原有行为。

- `GET /runtime/settings`：返回 `{path,exists,doc,masked,revision}`，凭据以占位符脱敏；`masked` 包含数组下标路径。`ETag` 是加双引号的 revision，响应禁止缓存。
- `PUT /runtime/settings`（兼容原 POST）：接受完整原始 Settings 对象，保留请求中的未知字段。可携带 `If-Match`（原始 revision 或带引号 ETag），过期返回 `409 {error:"settings_conflict",revision}`。不携带时兼容旧客户端的覆盖保存。成功返回 `{saved,path,revision,requires_restart:true}`；客户端应 GET 回读。类型、路由与 Responses 状态校验失败返回 400，文件保持不变。
- `POST /runtime/settings/validate`：接受同一 Settings 对象，返回 `{valid,issues:[{field,code,message}]}`，不写文件、不访问网络。非法 JSON、非对象或尾随 JSON 返回 400；字段类型和路由问题以 200 / `valid:false` 返回。主路由按运行时进程环境优先级解析 Provider，沿用 Provider/协议解析器，包括备用路由、Responses stateless / store=false 限制；不声称验证远程模型存在性。空模型允许使用运行时默认值。
- `GET /runtime/settings/effective`：返回 `scope:"server_workspace"`、`workspace`、`global_path`、`file_resolved`、`process_snapshot`、`activation`。`file_resolved.doc` 由统一默认入口解析全局 Settings；默认路径为 `~/.golang-cc/settings.json`，保留 `GOLANG_CC_CONFIG_DIR` 重定位。`sources` 仅包含读取成功的这个全局文件（不存在或解析失败时为空），不扫描 workspace 的 JSON/local/YAML；`route_sources` 仅归因可准确追踪的 model/provider/providerProtocol/Responses 子字段及路由 env 字段。切换 Provider 时同步移除旧路由的来源。该部分不包含进程环境、CLI、Profile 或 Run 覆盖（`includes_process_environment:false`），其他字段不推断来源。
- `POST /runtime/settings/test-provider`：接受 `{doc:Settings,provider?:"named-provider"}`，恢复已保存的脱敏凭据，并按运行时环境优先级解析配置；只请求所选 Provider 的 models 目录，最多 10 秒、不跟随重定向、不调用模型、不写设置。返回 `{ok,kind:"catalog_connection",status_code?,message}`。目录不支持、401、网络错误等均为明确失败结果；目录失败不能推出推理接口不可用，目录成功也不代表模型推理已验证。只有用户显式操作才调用该接口。

`process_snapshot.kind:"startup"` 表示 CLI 启动时捕获的安全摘要；嵌入式服务未提供启动摘要时可为 `status_callback` 或 `unavailable`。现有 StatusFunc 可能每次重新读文件，因此 status_callback 不能标成启动配置。摘要不含凭据，URL 用户信息和查询参数被移除，且 `active_run_config_known:false`。`activation` 为 `{requires_restart:true,existing_runs:"unchanged",new_runs:"runtime_dependent"}`：保存不自动重启，不声称既有 Run 已热更新；新 Run 是否重新加载取决于各入口的配置装配。

脱敏凭据按原位置恢复；有名称的 Provider 按唯一名称恢复，所以删除/排序不会把凭据转移到另一 Provider。重命名携带占位符的 Provider 会拒绝保存，需重新输入凭据。未命名的历史 Provider 按 type/protocol/model/baseURL 组合匹配，只有唯一匹配才能恢复；路由改变或多个匹配会拒绝占位符，需重新输入凭据。未知字段由客户端携带完整原始对象保留；被客户端主动省略的字段仍按完整替换语义移除。

并发保护覆盖同一进程的全部设置 API handler 的读取、比较和原子写入；其他进程及外部编辑器不参与此互斥锁，仍需外部协调。保存后记录不含正文/凭据的 `runtime.settings.saved` 事件，目录测试记录 `runtime.settings.provider_test`；不新增数据库持久化。请求的 provider 配置只用来发起显式目录探测，错误结果不返回远端正文或包含凭据的 URL。

影响评估：`RT-ENTRY -> RT-BOUNDARY -> RT-WIRING/RT-PERSIST -> RT-OUTPUT`，`B5_SHARED_STATE`。正收益是类型/路由错误与陈旧写入在写盘前被拒绝，保留旧客户端协议；代价是新增本地校验与短暂进程互斥，不增加模型 turn/token/tool call。目录探测仅显式发生，增加最多一次 HTTP 请求与 10 秒等待。回归覆盖认证、未知字段、脱敏凭据、路由负向路径、并发冲突、保存回读、工作目录来源、启动摘要脱敏与重定向。回滚可撤回新增 API/UI 并保留既有配置文件，无 schema 迁移。

## 设置环境目录与 Profile 管理

- `GET /runtime/settings/environments`：返回 `{environments:[{id,label,database,tenant_key,user_id,api_path,available}],global_settings_path,global_settings_shared}`。只列出当前环境和该操作身份获准访问的环境；不包含 DSN、密码或授权白名单。需要管理 token；额外环境还要求主库 owner/admin。
- `/runtime/settings/environments/{id}/tenant/agent-profiles` 及其已有子路径：复用原 Profile CRUD、校验、发布、归档、回滚、机器人绑定和对话目录契约。
- `/runtime/settings/environments/{id}/tenant/agent-profile-assignment`：复用原分配接口。
- `GET /runtime/settings/environments/{id}/tenant/channel-accounts`、`GET /runtime/settings/environments/{id}/tenant/messages?session_id=N&limit=100`：只读渠道账号、属于目标用户的会话正文。

所有环境请求仍传操作方 `X-Tenant-Key`、`X-User-Id` 和 Bearer token。服务器用预配置目标身份执行租户服务，浏览器无法指定连接或覆盖目标身份。未知/未授权环境返回 404，主库或目标角色不足返回 403，启动时连接失败返回 503，路径逃逸返回 400；写入成功除目标业务审计外，还在主库记录 `settings.environment.write`。额外环境不开放聊天执行、worker 生命周期或全局配置写入路由。全局模型/JSON 始终使用原 `/runtime/settings` 接口，共享同一文件和条件写保护。

环境在服务器启动时读取 `GOLANG_CC_SETTINGS_ENVIRONMENTS_FILE`；未指定时读取全局 settings 目录的 `settings-environments.json`，不存在则只显示当前环境。JSON 清单和引用的 DSN 文件必须为私有常规文件（0600），相对路径相对于清单目录。注册不会运行 migration 或启动 worker；数据库需要已有项目 schema。配置和回滚示例见 [设置环境架构](architecture/webui_v2_settings_environments.md)。

## WebUI 2.0 会话内容

`GET /tenant/session-control/sessions/{source}/{id}/conversation?cursor=0` 使用 Session key（例如 `sc-...`），不能传成 timeline 的数字 ID。返回 `{data:{schema_version,session,events,cursor,has_more}}`；事件按 ID 升序，每页 200 条，`has_more=true` 时使用返回 cursor 继续读取。只允许当前租户用户有权读取的 managed 会话，Local 仍拒绝。

`POST /tenant/session-control/conversations/stream` 接受 `{"sessions":[{"ref":"tenant:sc-...","cursor":"0"}]}`，最多 32 个不同会话。SSE `conversation` 事件携带同样的 page 对象（无 data 包装），每个会话有独立游标，复用持久化 Task events；断开不会取消任务，重连要带每会话的最后已消费游标。事件查询失败以 SSE error 结束，不吞掉错误。旧状态 SSE 和 Agent Task SSE 协议不变。

创建 Session 可指定 `provider`（命名 Provider）、`model`、`cwd`。Provider 保存于会话 metadata 并传给后续 Run；已固定的 Provider 不允许在发送时悄悄改变。旧会话没有 Provider 时按现有服务端默认路由解析。服务端对创建和执行做本地路由 preflight，不发送付费探测请求。

WebUI 2.0 复用既有 `/tenant/agent-tasks/{taskID}/pending-inputs` 管理当前会话队列。暂停队列拒绝新增待处理输入，Session Control 返回 `409 invalid_state`；相同输入键的内容冲突仍返回 `idempotency_conflict`。排队数量仅统计 `queued`，不将执行中或已完成项计入提示。

`source_refs` 对主 Web 会话提取有界用户请求和完成回复，沿用 Handoff 包、权限与摘要预算；不注入完整 transcript。新版重新生成在原会话追加 Run，并恢复原请求的图片及来源引用；来源会重新鉴权并获取当前摘要，旧回答和旧 Handoff 保留。

请求和完成回复候选分别限制为 512 bytes（保留 UTF-8 边界）。新 message/completed 证据哈希覆盖所选正文的完整内容；旧包仍接受其元数据哈希，继续保留旧校验保证，不代表旧包能够验证正文变化。工具 verified 证据不使用此兼容回退。

Go Claude 的 `server` 命令基于 Gin 暴露本地 HTTP API，优先覆盖 CLI query 主链路和 OpenAI-compatible chat completions。

## 启动

```bash
go run ./cmd/golang-cc server --host 127.0.0.1 --port 8080 --auth-token test-token
```

鉴权与绑定（AUDIT-P1-21）：

- **绑定到非回环地址（`0.0.0.0`、`::`、具体网卡 IP、主机名）时 `--auth-token` 必填**，
  否则 server 拒绝启动。此前空 token 会静默放行全部端点。
- 本机自用（`--host 127.0.0.1`、`localhost`、`::1`，或不传 `--host`）可以不配 token。
- 吐数据的端点只认 `Authorization: Bearer <token>` 头。`?token=` 仅被两个不含会话内容的
  HTML 外壳（`/trace`、`/prompt-dump`）接受 —— 浏览器打开链接时没有别的地方能塞 header，
  而 query 里的 token 会进 access log、浏览器历史和 Referer。

健康检查（AUDIT-P1-22）：

- `GET /livez`：**无需 token**。进程还在响应 HTTP 就返回 200，不探任何依赖。
- `GET /readyz`：**无需 token**。逐个探活已配置的依赖（MySQL、quota Redis、mobile Redis），
  任一不可用返回 503。响应形如 `{"status":"ok","checks":{"mysql":"ok"}}`，`checks` 只有
  `ok` / `unavailable` 两种值，不泄漏错误详情。
- `GET /health`：仍需 token，返回 `ok` 与 `workspace`，用于运维自查。

限流与执行目录（AUDIT-P1-27）：

- 不带 `X-Tenant-Key` / `X-User-Id` 的 `/query` 与 `/v1/chat/completions` 调用不走租户配额，
  改为受按客户端 IP 的兜底限流约束，默认 120 次/分钟，超限返回 429。
  `GOLANG_CC_QUERY_RATE_LIMIT_PER_MINUTE` 可调，负数关闭。计数按 TCP 对端地址，
  **不看 `X-Forwarded-For`**（否则伪造转发头即可绕过）。
- 请求体的 `cwd` 必须落在 workspace 子树内（比较前先解符号链接）。
  `GOLANG_CC_SERVER_ALLOWED_CWD_ROOTS` 可用系统路径分隔符列出额外根目录。

Swagger UI：

- `http://127.0.0.1:8080/swagger/index.html`
- OpenAPI JSON：`docs/swagger.json`
- OpenAPI YAML：`docs/swagger.yaml`

Trace Viewer WebUI：

- `http://127.0.0.1:8080/trace?token=test-token`
- WebUI 可查看本地 TUI/CLI transcript 会话，也可在 `Tenant` tab 查看 API Server / Mobile / OpenAI-compatible 的 MySQL tenant 会话全链路。

Prompt Dump Viewer：

- `http://127.0.0.1:8080/prompt-dump?token=test-token`
- **只服务本机直连的客户端**，并且拒绝带 `X-Forwarded-For` / `X-Real-Ip` / `Forwarded` 的
  请求（AUDIT-P1-21）。它返回完整 prompt 正文，泄漏面比 `/trace` 还大，而它本身就是本机
  调试视图；需要远程查看请开 SSH 隧道。`/prompt-dump/api/records` 另外只认
  `Authorization` 头，不接受 `?token=`。
- `GET /prompt-dump/api/records?session_id=<id>&limit=200&include_request=true`：读取本地 prompt dump JSONL。默认路径为 `/tmp/golang-cc-tui-prompt.jsonl`，如果 server 进程设置了 `GOLANG_CC_DUMP_PROMPT_JSON`，则优先读取该路径。
- `include_request=false` 时只返回 summary/hash/cache-control 边界，不返回完整 raw request；`include_request=true` 用于详情查看完整 system/messages/tools。raw prompt 可能包含敏感上下文，应只在本地受控环境使用。
- 页面会按 `session_id` 聚合 prompt dump 请求，并尝试用同一 session 的本地 Trace usage 补充 cache read/write/hit rate；如果 transcript 未记录 provider usage，则只显示 prompt cache-control 边界。

Mobile Chat Lab 前端：

- 独立前端工程位于 `web/`，开发态通过 Vite proxy 访问本地 API Server。
- 构建后可设置 `GOLANG_CC_WEBUI_DIR=web/dist`，由 API Server 在 `/webui/` 静态托管。
- P0 主要用于验证 `/mobile/chat/*` 完整周期，并在同一页面读取 memory、profile、skills 和 trace 摘要。
- 技术计划见 [webui_mobile_chat_lab_plan.md](webui/webui_mobile_chat_lab_plan.md)。

Runtime loop/background API：

- `GET /runtime/background?kind=loop&tail=4000&limit=100`：列出本机 runtime background jobs，可按 `kind=loop` 过滤，并返回最新日志 tail、run count、last/next run、schedule id、interval、PID 和状态。
- `POST /runtime/background`：创建本机 loop。请求体支持 `prompt`、`cwd`、`interval` 或 `interval_seconds`，默认 interval 为 `10m`，最小有效调度间隔按 scheduler 规则归一到 1 分钟。
- `PATCH /runtime/background/{id}`：更新本机 loop 的 prompt、cwd、interval/model/max turns 等配置；`id` 可传 background id 或 schedule id。
- `GET /runtime/background/{id}/logs?tail=12000`：读取指定 background job 的完整日志和 tail。
- `POST /runtime/background/{id}/run`：立即手动执行一次本机 loop，并写入 scheduler event 与 run history。
- `GET /runtime/background/{id}/runs?limit=50`：读取本机 loop 的运行历史，包含 started/finished time、status、error 和 log bytes。
- `GET /runtime/background/events?offset=0&limit=100`：按 JSONL byte offset 读取 scheduler events。TUI 可用该接口对应的 store 能力 tail `run_started` / `run_finished`，避免只靠日志长度猜测。
- `POST /runtime/background/{id}/stop`：停止 background job 或 schedule；`id` 可传 background id 或 schedule id。服务会同时 disable schedule 并 kill 对应 background job。

这些接口走与其他管理 API 相同的 Bearer token 鉴权。它们管理的是本机 `CLAUDE_CONFIG_DIR` 下的 runtime loop/background 数据，不是 tenant MySQL schedule CRUD。

同步最新 API 文档：

```bash
swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency
```

如果本机没有安装 `swag` CLI，也可以直接运行：

```bash
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/golang-cc/main.go --parseInternal --parseDependency
```

`POST /query` 的 `tool_traces[].file_changes[]` 会返回工具修改文件前后的内容、存在性、snapshot 路径和权限元数据。`before_mode_known` / `after_mode_known` 表示对应 mode 字段是否真实采集；该标志不能用 `mode != 0` 替代，因为 POSIX `0000` 是合法权限。rewind 使用 `before_mode_known=true` 恢复包括 `0000` 在内的准确权限；旧记录没有 known 标志但带非零 `before_mode` 时仍按该 mode 恢复。

启用多租户 MySQL 持久化：

```bash
export GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc?multiStatements=true&parseTime=true'
export GOLANG_CC_MOBILE_JWT_SECRET='replace-with-mobile-hs256-secret'
export GOLANG_CC_MOBILE_ALLOWED_MODELS='claude-sonnet-4-6,claude-haiku-4-5'
export GOLANG_CC_MOBILE_RATE_LIMIT_PER_MINUTE='30'
export GOLANG_CC_MOBILE_DAILY_MESSAGE_QUOTA='1000'
export GOLANG_CC_MOBILE_USAGE_STORE_PATH='./data/mobile_usage.json'
export GOLANG_CC_MOBILE_UPLOAD_BASE_URL='https://uploads.example.test/mobile'
# Optional: S3/OSS/MinIO-compatible presigned PUT upload URLs.
export GOLANG_CC_MOBILE_S3_ENDPOINT='https://s3.example.test'
export GOLANG_CC_MOBILE_S3_REGION='us-east-1'
export GOLANG_CC_MOBILE_S3_BUCKET='mobile-uploads'
export GOLANG_CC_MOBILE_S3_ACCESS_KEY='replace-with-access-key'
export GOLANG_CC_MOBILE_S3_SECRET_KEY='replace-with-secret-key'
export GOLANG_CC_MOBILE_S3_PREFIX='uploads'
export GOLANG_CC_MOBILE_S3_PUBLIC_BASE_URL='https://cdn.example.test/mobile'
go run ./cmd/golang-cc server --host 127.0.0.1 --port 8080 --auth-token test-token
```

配置 DSN 后，server 会打开 GORM-backed MySQL repository；schema migration 仍通过 `tenant migrate` 使用 `golang-migrate` 执行。

### 请求体上限、超时与优雅退出

- **请求体上限**：所有路由统一挂 `http.MaxBytesReader`，默认 10 MiB（`server.Options.MaxRequestBodyBytes`，
  负数关闭）。声明的 `Content-Length` 超限时一个字节都不读就返回 `413`，响应体是
  `{"error":"...","error_type":"request_body_too_large","max_bytes":N}`。
- **读侧超时**：`ReadHeaderTimeout` 15s、`ReadTimeout` 60s、`IdleTimeout` 120s。
- **写侧超时**：不使用 `http.Server.WriteTimeout`（它从请求开始计时，会砍断长跑的 `/query` 和
  SSE）。改为「写空闲」语义：首次写出才武装 60s，每次写出续期；SSE 与 WebSocket 连接彻底清掉。
- **优雅退出**：`SIGINT` 与 `SIGTERM` 都会触发优雅退出（容器场景收到的是 SIGTERM）。退出流程是
  先给每条 SSE 发一条 `event: server_shutdown` 并取消其 request context，再等在途请求排空，
  预算默认 20s（`server.Options.ShutdownTimeout`）；超预算才强制关闭剩余连接。
  SSE 客户端可监听 `server_shutdown` 事件主动重连。

## OpenAI-Compatible Chat

- `GET /v1/models`：返回可用模型列表。
- `POST /v1/chat/completions`：支持普通 JSON 响应。
- `POST /v1/chat/completions` with `"stream": true`：支持 `text/event-stream` SSE 响应，最后输出 `data: [DONE]`。
- 当内部 query result 携带工具调用轨迹且没有最终文本时，普通响应会输出 assistant `tool_calls`，SSE 响应会输出 `delta.tool_calls`，并使用 `finish_reason=tool_calls`。

请求转换规则：

- `system` 和 `developer` message 合并为 query 的 system prompt。
- `user`、`assistant`、`tool` 等历史消息按 role 顺序合并为 query prompt。
- content 支持字符串和 OpenAI content parts 数组，`text`、`input_text`、`image_url` 会转成可读上下文。
- assistant message 的 `tool_calls` 会保留为 `TOOL_CALL <id> <name>: <arguments>`。
- tool message 的 `tool_call_id` 和 `name` 会保留为 `TOOL <tool_call_id> <name>: <content>`。
- `max_tokens` 和 `max_completion_tokens` 都会映射到 query max tokens，优先使用 `max_tokens`。
- `response_format.type=json_object` 会追加 JSON object 输出约束。
- `response_format.type=json_schema` 会把 schema name、description、strict 和 JSON Schema 追加到 system prompt，并透传到 OpenAI-compatible provider 的 `response_format` 字段；Anthropic provider 当前仍使用 prompt 约束作为 fallback。
- `response_format.type=json_schema` 默认进入结构化输出 fast path：单模型调用、禁工具调用、跳过自动标题生成，保留 telemetry、usage ledger 和 tenant session/message 持久化。这样上层应用可用 JSON Schema 做硬约束，同时避免 agent/tool loop 把一次业务决策放大成多次模型调用。
- 结构化 fast path 支持可配置 tenant skill 内联。选择优先级为 `X-Tenant-Skill-Key` header、请求体 `metadata.tenant_skill_key` / `metadata.tenant_skill_keys`、当前 tenant `settings_json.structured_skill_routes`、服务端 `GOLANG_CC_STRUCTURED_SKILL_ROUTES`，最后才使用内置兼容 fallback。
- `structured_skill_routes` 使用数组格式：`[{"schema_name":"teach_decision_v1","skill_key":"teach-v2"}]`。这只配置 schema 到 tenant skill 的路由，不承载调用方业务状态机。
- 兼容 fallback 仍会把 `teach_decision_v1` / `teach_context_v1` 映射到 `teach`，用于未显式配置的旧部署；新租户可配置任意 skill key，例如 AI Study 可配置 `teach-v2`，其他租户仍可继续使用 `teach`。

## Tenant Persistence

请求携带 `X-Tenant-Key` 和 `X-User-Id` 时，`/query` 与 `/v1/chat/completions` 会自动创建或更新 tenant session，并写入 user/assistant 两条 message。

- `/query` 请求体可传 `session_key`。
- `/query` 请求体可传 `prompt_mode`：`code` 加载代码开发上下文，`chat` 隔离本地代码上下文；未传时默认 `code`，可用 `GOLANG_CC_SERVER_DEFAULT_PROMPT_MODE=chat` 调整 server 默认。
- `/v1/chat/completions` 可传 `X-Session-Key`。
- `/v1/chat/completions` 和 `/mobile/chat/...` 固定使用 `chat` prompt mode，不加载服务端 cwd 的 `CLAUDE.md`、`.claude/rules`、git branch/status 等代码上下文。
- 未提供 session key 时使用 `X-Trace-Id`，再兜底为 `query`。
- server runtime 会用当前大模型为自动持久化的 session 生成简短标题；生成失败时回退为首条 prompt 截断标题。
- 结构化输出 fast path 会跳过自动标题生成，避免业务 decision 请求在主模型调用后再触发一次 title 模型调用。
- SSE 模式会缓存实际发送的 assistant token，并将完整文本落到 tenant message。
- 如果 assistant 响应包含工具调用，tenant assistant message 会在 `content_json` 中保存 OpenAI-compatible `tool_calls` 和内部 `tool_traces`，并写入首个 `tool_id/tool_name` 便于查询。
- tenant effective skills 会进入 runtime：system prompt 只注入当前 tenant/user enabled skills 的 metadata；模型调用 `Skill` tool 时优先加载 tenant `content_md`，不存在时 fallback 到本地 filesystem skill。
- tenant skills 每轮从 MySQL 读取，不需要重启 server；`POST /tenant/skills` 或 `POST /tenant/skill-overrides` 更新后，下个 `/query`、`/v1/chat/completions` 或 `/mobile/chat/.../messages/stream` 请求即可使用。
- tenant skill package Phase 1 使用本地 artifact store，默认 `~/.golang-cc/tenant-skill-packages`，可用 `GOLANG_CC_TENANT_SKILL_PACKAGE_DIR` 覆盖。`POST /tenant/skill-packages/import` 和 `POST /tenant/skill-packages/render` 可从 server 本地 `source_path` 或 base64 zip 渲染 deterministic `runtime.md` 和 manifest；`POST /tenant/skill-packages/publish` 会保存 `package.skill.zip`、`manifest.json`、`runtime.md`，并把 compiled runtime 写入 `tenant_skills.content_md`，同时写入 `package_ref`、`package_sha256`、`manifest_json`、`runtime_ref`。`POST /tenant/skill-packages/verify-runtime` 会针对指定 `skill_key` 和 `schema_name` 走通用结构化 runtime smoke，返回安全 `tenant_runtime` metadata 并校验 loaded key、version、package hash、bytes；它不承载上层应用业务状态机。Phase 1 只支持 `file://` artifact ref，不接 S3/OSS。
- `POST /tenant/skills/rollback` 会把指定历史版本复制成新的最新版本，不修改历史行；可选 `target_version` 必须大于锁内最新版本，否则返回 400；下一次 runtime 读取最新 effective skill 时立即生效。
- `/v1/chat/completions` 和 `/mobile/chat/...` 的 chat prompt mode 不加载服务端本地 `.claude/skills`，只使用 tenant skills；`/query` 的 code prompt mode 会在 tenant skills 后继续加载本地 skills。
- runtime 可观测字段：`tool.execution.started/finished` telemetry properties 会记录 `active_skill`、`active_skill_source`、`active_skill_version`、`active_skill_fallback`；流式 `skill_activated` event 会记录 `skill_source`、`skill_version`、`skill_fallback`。`active_skill_source=tenant` 表示来自 MySQL tenant skill，`active_skill_fallback=true` 表示启用了 tenant provider 但最终落到本地 filesystem skill。结构化 fast path 会在 `query.prompt_context` telemetry 的 `context_manifest.tenant_skill_inline` 中记录 `selector`、`skill_keys`、`loaded_keys`、`versions`、`package_sha256`、`package_refs`、`runtime_refs`、`bytes` 和加载错误状态，但不会记录 skill 正文。稳定查询路径为 `context_manifest.tenant_runtime.active/resolved/source/skill_keys/loaded_keys/versions/package_sha256/package_refs/runtime_refs/bytes`、`context_manifest.tenant_skill_inline.*` 和 `context_manifest.tenant_context.active`；展开键 `tenant_runtime.active/resolved/loaded_keys/versions/package_sha256`、`tenant_skill_inline.loaded_keys/versions/package_sha256` 也会保留供排障脚本使用。`tenant_skill_inline.active=true` 表示 inline tenant skill 被选中并注入；`tenant_runtime.active=true` / `tenant_runtime.resolved=true` 表示请求已参与 tenant runtime 解析并有可观测 runtime metadata；`tenant_context.active=true` 仅表示 memory/profile/document/knowledge addendum 进入 prompt，不能等同于 tenant runtime 是否解析。非流式 `/v1/chat/completions` 会在成功和 provider 失败响应 JSON 的 `tenant_runtime` 扩展字段，以及 `X-Tenant-Skill-Keys`、`X-Tenant-Skill-Versions`、`X-Tenant-Skill-Package-SHA256`、`X-Tenant-Skill-Package-Refs`、`X-Tenant-Skill-Runtime-Refs`、`X-Tenant-Skill-Selector`、`X-Tenant-Runtime-Active`、`X-Tenant-Runtime-Resolved` headers 中返回安全 runtime 摘要；流式响应会在结束前发送 `object="tenant.runtime"` 的 SSE data event，provider 失败时会在 SSE error 事件前尽量先发送该 runtime event。上述响应 metadata 不包含 skill 正文。WebUI Telemetry 面板会按 skill/source/version/fallback 聚合事件数、错误、耗时和 tokens。
- 结构化 provider retry：`response_format.type=json_schema` 的 OpenAI-compatible structured fast path 如果遇到 provider 错误文本包含 `JSON response_format generation abnormal` 或 `InternalError.Algo.InvalidParameter`，中台会自动用同一请求参数最多 retry 一次，并发出 `openai.structured_retry` telemetry。默认不降级为非 `json_schema`、不移除 `response_format`、不自动切换 provider/model，也不理解调用方业务状态机。

## Agent Profiles and Teams

Agent Profile/Team API 是可选能力。未配置支持新领域接口的 tenant service 时返回 503，不影响旧 API。所有接口复用 Bearer auth、`X-Tenant-Key`、`X-User-Id`、tenant scope、owner/admin role 和 audit。

Profile：

- `GET /tenant/agent-profiles?status=published&limit=100`：读取当前 tenant/user 可见的 builtin、shared、private profiles。
- `GET /tenant/agent-profiles/{key}/conversations?version=1&limit=50`：读取指定 Profile 绑定的安全会话摘要、最近消息预览、运行计数和 Team 参与关系；完整消息继续通过 Chat Lab/Trace 查看。
- Profile 响应包含 `source_kind`、`source_ref`、`source_path` 来源 metadata：数据库 Profile 显示 MySQL 引用，内置 Profile 由 WebUI 标注代码定义位置；数据库存储不会伪造本地文件路径。
- `POST /tenant/agent-profiles`、`PATCH /tenant/agent-profiles/{key}`：创建或保存 draft；body 包含 `profile_key`、`scope`、`display_name`、`description`、`config`。
- `POST /tenant/agent-profiles/{key}/validate`：只校验 schema、prompt boundary、工具/技能/MCP catalog 和预算，不发布、不执行模型。
- `POST /tenant/agent-profiles/{key}/publish?version=1`：发布不可变 profile version；运行时只读取 published。
- `POST /tenant/agent-profiles/{key}/archive?version=1`：归档版本；已绑定版本必须先替换 assignment/binding。
- `POST /tenant/agent-profiles/{key}/rollback?version=1`：从历史版本创建新的 draft，不修改历史行。
- `GET|PUT|DELETE /tenant/agent-profiles/{key}/bot-binding?version=1`：查看、绑定或归档一个 profile version 对应的安全 channel account；请求不接受 app secret。
- `GET|PUT /tenant/agent-profile-assignment?surface=web_chat`：按 `tenant/user/surface` 读取或设置当前 profile，多个 surface 可以同时使用不同 profile。

Team：

- `GET /tenant/agent-teams?status=published&limit=100`：读取 Team catalog。
- `POST /tenant/agent-teams`、`PATCH /tenant/agent-teams/{key}`：创建或保存 draft Team。
- `POST /tenant/agent-teams/{key}/validate?version=1`：校验固定 Team version 的 coordinator、成员 profile version、bot account、群组 binding、trigger、预算和 loop guard。
- `POST /tenant/agent-teams/{key}/publish?version=1`：发布固定成员 profile version 的 Team snapshot。
- `POST /tenant/agent-teams/{key}/archive|rollback?version=1`：归档或从历史版本创建新 Team version。
- `GET|PUT /tenant/agent-teams/{key}/members?version=1`：读取或替换成员图；成员只能引用 published profile version。
- `GET|PUT /tenant/agent-teams/{key}/bindings?version=1`：读取或绑定 Feishu account/group；同一群可以绑定多个 bot，但每个 binding 必须有 trigger policy。
- `GET /tenant/agent-teams/{key}/runs?version=1`、`GET /tenant/agent-teams/{key}/runs/{run_id}?version=1`：读取固定 Team version 的 TeamRun、成员状态、mailbox/evidence 脱敏摘要；run detail 缺少 pinned version 或 Team 不匹配时拒绝访问。
- `POST /tenant/agent-teams/{key}/runs/{run_id}/cancel`：取消 TeamRun；运行中的成员按 checkpoint/turn 边界收敛。
- `GET /tenant/channel-accounts?limit=100`：owner/admin 读取不含凭据的 channel account metadata。
- `GET|POST /tenant/agent-provisionings`：读取或创建 tenant-scoped Profile-Agent provisioning session；create 只接受结构化 worker desired state 和一次性 credential input。
- `GET /tenant/agent-provisionings/overview`：读取向导记录和实时 screen worker inventory；“运行中 Worker”按 screen/PID 实时状态统计，不要求 worker 先经过向导创建 provisioning 记录。
- `GET /tenant/agent-provisionings/{id}`：读取脱敏 session、worker observed state 和健康检查结果。
- `POST /tenant/agent-provisionings/{id}/preflight`：执行 provider/Feishu preflight。
- `POST /tenant/agent-provisionings/{id}/start|restart|stop|status`：通过 V1 `screen` supervisor 管理 account worker；不接受任意 shell 命令。

Team 的外部群组输入先经过 durable Inbox 和 Team Router，内部成员协作写入 mailbox，最终只由 coordinator 的 accepted result 进入 Outbox。`team_run_id`、profile/team version/hash、inbox/outbox/provider message id 会进入 trace/audit metadata；bot loop、duplicate TeamRun 和重复最终发送必须由幂等键与 readback 处理。

Team policy body 同时兼容早期 flat 字段和 WebUI 使用的 nested `orchestration`、`trigger`、`authorization`、`output` sections。替换成员或 binding 时服务会先归档旧 active rows，再写入新快照，避免重复更新撞唯一键。

Session lifecycle API：

- `GET /tenant/sessions?limit=100`：读取当前用户 session 列表。
- `POST /tenant/sessions`：按 `session_key` 创建或更新当前用户 session。
- `GET /tenant/sessions/{id}`：读取当前用户指定 session。
- `PATCH /tenant/sessions/{id}` / `PUT /tenant/sessions/{id}`：更新当前用户 session 的 `title`、`status`、`model`、`cwd`、`metadata_json`。
- `DELETE /tenant/sessions/{id}`：软删除当前用户 session，状态置为 `archived` 并写入 `archived_at`。
- `GET /tenant/sessions/{id}/timeline?limit=200&trace_limit=100&task_limit=500`：owner/admin 查看指定 session 的完整链路时间线，聚合 session、messages、trace 关联 audit、telemetry、sub-agent tasks 和 task events，并按时间排序，适合排障和回溯。
- `GET /tenant/messages?session_id=123&limit=100`：读取当前用户指定 session 的消息历史。
- `POST /tenant/messages`：写入 session message。

Session Control API 使用独立的 `/tenant/session-control/...` namespace，不替换上述 legacy Session API。所有响应使用固定 `{ "data": ... }` envelope；错误使用 `{ "error": "...", "code": "..." }`。身份只从 Bearer auth 与服务端解析的 `X-Tenant-Key` / `X-User-Id` 上下文取得，请求体不能覆盖 tenant、user、actor 或 trace。

- `GET /tenant/session-control/sessions?source=tenant|local&limit=100`：按 namespace 读取有界 session snapshot 列表。宿主 Local transcript 默认不可见，只有服务启动环境显式设置 `GOLANG_CC_SESSION_CONTROL_LOCAL_READ=1` 后才允许只读 List/Get/Handoff；tenant 身份本身不会获得宿主文件访问权。
- `POST /tenant/session-control/sessions`：创建 managed session；body 支持 `session_key`、`title`、`cwd`、可选 `initial_text`，以及顶层可选 `model`、`provider`、`permission_mode`、`effort`、`prompt_mode`。创建与首个 Run 使用同一幂等身份恢复，任务与首条 message event 同事务落库。
- `GET /tenant/session-control/sessions/{source}/{id}?include_links=true`：读取 namespaced ref 的有界 snapshot；不返回 transcript 或消息正文。
- `POST /tenant/session-control/sessions/tenant/{id}/messages`：发送输入，body 支持 `content`、安全附件 metadata、`source_refs`，以及与 Create 相同的五个运行配置字段。省略或空字段继承当前 Session 默认值；空闲且队列为空时可以整体预检并切换下一 Run 的 provider/model/权限/强度/prompt mode。空闲 Session 使用 `prepare -> Handoff attach -> ready/running CAS -> launch`；拖入来源只传有界 Handoff 摘要与证据索引，不拼完整 transcript。已有 ready/running Run 或未终结 pending input 时，配置变化返回 `409 invalid_state`，相同配置的文字/附件继续进入 durable pending-input；已有 Run 时追加 `source_refs` 会明确拒绝。
- `POST /tenant/session-control/sessions/tenant/{id}/stop`：停止活动 Run；SSE 断开不会隐式调用此接口。

运行配置也以同名顶层字段出现在 Session snapshot。每个新 Run 的 `metadata_json` 固定保存其实际配置，后续切换不改写历史 Run；Session 默认值与 ready → running claim 同事务更新，保留创建幂等 metadata。路由、配置枚举、Handoff 或并发检查拒绝不会提前更新默认值；配置字段参与幂等请求指纹，复用相同 key 修改配置返回 `idempotency_conflict`。`permission_mode` 复用现有权限模式与别名（如 `ask`、`allow`、`deny`、`acceptEdits`、`bypassPermissions`）；省略时保持宿主原有策略。`effort` 支持 `low/medium/high/max`、`off/none`、现有别名和正整数 token budget；`prompt_mode` 仅接受 `code/chat`。凭据不进入请求、快照或 Run metadata。

排队消息的显式配置依赖已有操作 audit 校验完整请求指纹。极少数进程在队列写入后、audit 完成前中断的情况下，携带配置字段的同 key 重试会返回 `409 invalid_state`（`queued configuration replay requires its operation audit`），不会猜测原配置或删除队列项。客户端应读取 pending-input 列表确认已受理项，避免重新发送形成重复输入；原有省略配置的请求保持此前恢复行为。

Session Control 的 `messages` 支持 `attachments[].inline_data`，值为不含 data URL 头的纯 base64，同时提供 `type=image`、实际 `media_type`、`size_bytes`，以及可选 `name`、`sha256`。服务端验证 PNG/JPEG/GIF/WebP 的格式、尺寸、实际字节数与摘要；每次最多 8 张、单张 25 MiB、单图最多一亿像素。默认仅此消息路由（含 `/api` 前缀）的 body 上限扩大到约 268 MiB，以容纳 8 张图片的 base64 和 metadata；其他路由仍为默认 10 MiB，显式配置的 `MaxRequestBodyBytes` 始终优先，超限返回 413。浏览器当前保留原图，不做压缩。

图片经过 Session 归属校验后复用现有 BlobStore 与 media asset repository 归档，事件与 pending-input 仅保存 `attachment_id=sc-image-...`、`url=/tenant/media/assets/{attachment_id}` 和图片 metadata，不保存 inline bytes。读取 URL 仍需鉴权；执行、队列重试及恢复均按精确 tenant/user/session 范围读取并验证摘要后注入模型请求。摘要存于 `media_assets.original_json.sha256`；这类资源的可选资产级 `sha256` 留空，避免现有 tenant 范围唯一键把不同 Session 的同图绑定到同一个 Session。资源 ID 由 Session 范围和内容摘要确定，同范围同图重试复用 ID。配置了 tenant MySQL storage 时，即使关闭图片生成也保留附件归档能力。

归档先于 Send 的配置、Handoff 与运行事务；若后续拒绝或数据库写入失败，可能留下当前 Session 范围内的未引用资源。同图重试可复用，不会把图片字节写入拒绝响应、事件或队列；当前不新增孤立资源清理策略。side-chat 会把候选图片复制到新 Session 的独立资源，原候选不变。旧入口创建的无幂等标识 `ready` side-chat task，可由首次 Session Control Send 在消息事务内接管，保留候选首事件并追加当前消息；普通 ready/running task 仍遵循原有排队规则。

- `POST /tenant/session-control/sessions/tenant/{id}/attachments`：调用 Session Control Attach，把 `sources` 中的 namespaced session 有界 Handoff 绑定到显式 `target_task_id`；需提供 `target_context_window_tokens`，不会复制完整 transcript。
- `POST /tenant/session-control/sessions/tenant/{id}/monitors`：为 target 和 namespaced sources 建立指定间隔与 channel 的 monitor。当前 `channel=feishu` 必须能从 target Session 解析唯一 active Feishu conversation/account 绑定；否则在 link/schedule 写入前返回 `scheduler_unavailable`。MySQL observed link 是权威记录，本地 Scheduler 文件是可修复投影，二者不伪装成跨存储原子事务。
- `GET /tenant/session-control/sessions/{source}/{id}/events/stream`：输出 `operation`、`run`、`session` 三种 SSE event；支持 query `cursor` 或 `Last-Event-ID` 恢复，query 优先。

所有写接口要求 `Idempotency-Key` header，最大 128 bytes。相同 key 与相同请求语义返回原操作的 readback 并标记 `replayed`，相同 key 与不同语义返回 `idempotency_conflict`。Local session 所有写入返回 `forbidden`，读取还受上述宿主 opt-in 约束。SSE data 固定为 `schema_version`、`cursor`、`session_ref`、可选 `operation_id` / `run_id`、`status`、`updated_at`，传输故障使用 `stream_error` 且保留最后已知业务状态，不会伪造 `failed`；流中不会包含 content、prompt、task result、Handoff 正文、audit metadata、credential、attachment metadata、source locator 或 secret。SSE 只是优化，重连恢复以 `Get` readback 加事件 cursor 为准。
- `GET /tenant/knowledge/documents?limit=20` / `POST /tenant/knowledge/documents`：读取或写入当前用户 tenant knowledge 文档。写入后服务会拆分 chunks，用于 chat 模式检索注入。
- `POST /tenant/knowledge/search`：按 query 检索当前 tenant/user 可见知识片段。当前实现是 MySQL FULLTEXT + LIKE hybrid scoring，响应 chunk 带 `score` 和 `search_mode`，用于区分 fulltext/like 召回。
- `GET /tenant/memory-review/candidates?limit=50&candidate_type=explicit_remember&risk_status=low_risk&source_session_id=123`：读取统一待审 memory 候选，包含显式 `remember/记住` 生成的 `explicit_pending` 和 AutoMem 生成的 `automem_pending`；可按 `candidate_type=explicit_remember|automem_candidate`、`risk_status`、`source_session_id` 过滤；pending 候选不会注入 prompt context。
- `POST /tenant/memory-review/review`：统一审批 memory 候选，`action=approve` 会创建 active user memory，`action=reject` / `action=archive` 只写 marker，不进入 prompt context。
- `GET /tenant/automem/candidates?limit=50`：读取 AutoMem pending 候选，候选使用 `category=automem_pending`，默认不注入 prompt。
- `POST /tenant/automem/review`：旧版 AutoMem 审批兼容入口，内部复用统一 Memory Review 语义；pending/rejected/archived marker 都不会进入 prompt context。

## Trace Viewer

Trace Viewer 是内置 WebUI 和统一 JSON API，用于展示 TUI、CLI、background、Goal、Loop、本地 transcript，以及 API Server、Mobile、OpenAI-compatible tenant session 的全链路执行过程。

页面结构、模板文件和维护说明见 [trace_viewer.md](observability/trace_viewer.md)。

WebUI：

- `GET /trace`：返回内置可视化页面。页面通过 `/trace/api/...` 读取数据，支持 `Local` 和 `Tenant` 两类 session。HTML/CSS/JS 模板位于 `internal/server/trace_viewer.html`，通过 Go `go:embed` 编译进二进制。

统一 API：

- `GET /trace/api/sessions?source=local&limit=100&from=2026-06-16T00:00:00Z&to=2026-06-16T23:59:59Z`：列出本地 transcript session。golang-cc 默认读取 `GOLANG_CC_TRANSCRIPT_PROJECTS_DIR`、`GOLANG_CC_CONFIG_DIR/projects` 或 `~/.golang-cc/projects/.../*.jsonl`；Trace Viewer 另以只读兼容方式读取 `CLAUDE_CONFIG_DIR/projects` 下的原版/native transcript。`from/to` 可选，按 session 更新时间过滤。
- `GET /trace/api/sessions/{session_id}?source=local`：读取本地 session 的 normalized trace，包含 summary、spans、span tree 和 events。新会话的 `runtime_span` 会提供 query/model/tool/provider 的 native `span_id`、`parent_span_id`、`turn_index`、`started_at` 和真实 duration；同一工具不会再重复生成 timestamp 推断 span。旧 transcript 仍可读取，并对 tool_call/tool_result 使用时间戳估算。
- `GET /trace/api/sessions?source=tenant&limit=100&from=2026-06-16T00:00:00Z&to=2026-06-16T23:59:59Z`：列出当前 tenant/user 的 API Server session。`from/to` 可选，优先按 `last_message_at` 过滤，缺失时回退到 `started_at`。
- `GET /trace/api/sessions/{id}?source=tenant&limit=200&trace_limit=100&task_limit=500`：读取 tenant session 的 normalized trace。底层复用 `/tenant/sessions/{id}/timeline` 聚合 session、messages、audit、telemetry、agent task 和 task events，可展示 model/tool/API 耗时、tokens、permission、active skill、sub-agent 链路和错误。`runtime-trace-v1` telemetry 会额外返回 `span_id`、`parent_span_id`、`turn_index` 和 `started_at`；span tree 优先采用显式父子关系，历史事件继续按 trace id、时间范围和类型推断。
- `GET /trace/api/sessions/{id}/export?source=local|tenant&schema=runtime-trace-v1`：下载 metadata-only artifact。`source` 只接受 `local` 或 `tenant`；无效值返回 400，tenant 鉴权/服务错误保留原状态码。一个 artifact 对应 session 中最新的一次 query run，并只包含该 query 及后代；响应包含耗时分解、质量原始信号、diagnostics、脱敏 spans，以及与 CLI `version --json` 同源的 `run.build_info`。兼容字段 `run.agent_version` 保留且等于 `run.build_info.version`。artifact 不包含 prompt、tool input/output 或聊天正文。`run.status=completed` 只表示 runtime 正常结束，不表示任务验收成功。质量字段同时保留过程和终态：`test_attempts`、`failed_test_attempts`、`test_failure_recovered` 记录测试历史，`tests_passed` 表示最后一个已完成测试调用的结果，`final_verification_passed` 表示 completion evidence 与最终测试共同成立。新消费者优先读取 `final_verification_passed`；旧 artifact 缺失时回退到 `completion_verified && (!tests_run || tests_passed)`。Trace WebUI 对缺失的新字段显示为未知，不把旧数据误报为失败。

CLI/headless 运行可使用 `--runtime-trace-output <path>` 在 session recorder 关闭后原子导出同一 artifact；相对路径按 `--cwd` 解析。CLI artifact 的可选 `configuration` 会记录 `read_only_tool_mode`、请求的 `requested_max_parallel_read_only_tools` 和实际 `effective_max_parallel_read_only_tools`，只包含脱敏运行配置，不包含 prompt 或工具输入。Trace API 从已持久化 session 重建时不能保证恢复原 CLI 开关，因此可能省略该字段；严格 A/B 消费者应在字段缺失时拒绝作优化结论。`--max-parallel-read-only-tools <n>` 控制只读工具 worker 数，`0` 使用默认值 4，负数立即禁用并回退串行。
固定数据集可交给 `go run ./scripts/runtime-trace-baseline --dataset <manifest>`；manifest 中每个 `case_id` 必须恰好各有一个 before/after。报告区分 runtime `completion_rate` 与结构化 `verified_rate`，真实任务成功率由外部 APG 的场景 oracle 计算。

示例：

```bash
curl -sS 'http://127.0.0.1:8080/trace/api/sessions?source=local' \
  -H 'Authorization: Bearer test-token'

curl -sS 'http://127.0.0.1:8080/trace/api/sessions/123?source=tenant' \
  -H 'Authorization: Bearer test-token' \
  -H 'X-Tenant-Key: yutang' \
  -H 'X-User-Id: user-123'

curl -sS 'http://127.0.0.1:8080/trace/api/sessions/123/export?source=tenant&schema=runtime-trace-v1' \
  -H 'Authorization: Bearer test-token' \
  -H 'X-Tenant-Key: yutang' \
  -H 'X-User-Id: user-123' \
  -o runtime-trace-123.json
```

安全边界：

- Trace Viewer 走与管理 API 一致的 Bearer token 鉴权。
- `source=tenant` 依赖 tenant/user 上下文；tenant timeline 仍要求 owner/admin 权限。
- telemetry properties 继续使用统一脱敏逻辑，不持久化或展示 API key、JWT、完整 prompt、聊天全文、附件私有 URL 等敏感字段。

Sub-agent task API：

- `POST /agent/workspaces/validate`：校验 Web Agent workspace cwd，输入 `{ "cwd": "/absolute/path" }`，要求 absolute path、存在且是目录；返回 normalized `cwd`、`workspace_name`、`is_git_repo` 和 `git_root`。该接口只做本地文件系统校验，不创建目录、不写 tenant 数据。
- `GET /tenant/web-agent/conversations?limit=100`：按 Web Agent conversation/session 维度返回左侧列表数据。服务端把同一个 `parent_session_id` 下的多个 continuation task/run 合并成一个 conversation；历史数据没有 `parent_session_id` 时，会按 `metadata_json.continuation_of_task_id` 追溯 root task 并折叠成 `legacy:<root_task_id>`。该接口用于 `/webui/agent` 对齐 TUI `/resume` 的 session 列表心智，避免把同一会话的多次 run 显示成多行。
- `GET /tenant/web-agent/conversations/{id}?limit=100&event_limit=500`：返回单个 Web Agent conversation 详情。`id` 使用列表接口返回的 `session:<tenant_session_id>` 或 `legacy:<root_task_id>`，URL 中需要 percent-encode。响应包含稳定 session、runs/tasks、run events、tenant messages 和 usage 汇总，用于 `/webui/agent` 右侧 `Runs / Trace / Usage` 以及中间完整 conversation timeline。
  `session:<id>` 先验证当前 tenant/user 的会话归属，再按该会话查询任务并应用 `limit`，不会因其他会话新增任务而丢失详情；子任务和历史 metadata 会话关联保持兼容，`legacy:<id>` 继续使用原有扫描规则。
- `GET /tenant/agent-tasks?limit=100`：读取当前用户 sub-agent task 列表，包含 parent session、agent、model、status、result、trace。
- `POST /tenant/agent-tasks`：创建 sub-agent task 元数据行，支持 `parent_session_id`、`subagent_session_key`、`agent_name`、`description`、`prompt`、`status`、`model`、`result_json`、`metadata_json`、`trace_id`。`status=running` 时会写入 `started` event；`status=ready` 用于 Web Agent 空白会话，不写入 `started`，也不会直接启动模型运行。
- `GET /tenant/agent-tasks/{id}`：读取当前用户单个 sub-agent task。服务层和 repository 都使用 tenant/user/id 限定，避免跨租户读取。
- `PATCH /tenant/agent-tasks/{id}`：更新 task `status`、`result_json`、`metadata_json`。`completed`、`failed`、`cancelled` 会设置 `finished_at` 并追加对应 terminal event；仅 metadata/result 更新不会改变完成时间。
- `GET /tenant/agent-tasks/{id}/events?limit=100&after_id=0`：按 event id 顺序回放 task 的 started、turn_start、text_delta、thinking_delta、tool_call、tool_result、usage、message_stop、compact_summary、permission_request、permission_resolved、completed、failed、cancelled 事件。`after_id` 可选，传入后只返回 `id > after_id` 的事件，用于 Web Agent 长回复分页回放和 SSE 断线补漏。`tool_result` payload 携带 `input`（截断 600 字符）、`preview`（截断 160 字符）和 `output`（截断 2000 字符），流式与非流式路径一致，WebUI 消息内命令展开块用 `output` 展示工具结果。
- `POST /tenant/agent-tasks/{id}/message`：向 task event stream 追加 `message` event，并通过 server query runner 异步执行一次 Web Agent turn。`ready` task 会先提升为 `running`；runner 输出会以 `text_delta`、`thinking_delta`、`tool_call/tool_result`、`usage/message_stop` 写入 SSE/event store，auto compact 成功时写入 `compact_summary`，完成后写入 `completed`，失败写入 `failed`，取消写入 `cancelled`。请求支持 `attachments` 图片数组；`content` 可为空但必须至少有一张合法图片。图片支持 `url` 或 `inline_data`，后者只在本次运行内存中转成模型 image content block，不写入 Agent event、日志或 telemetry。服务端限制最多 8 张、单张最多 25 MiB，仅接受 `image/*`。`completed`、`failed`、`cancelled` 终态 task 会返回 409 `agent task is not accepting messages`，前端应创建 continuation task；server 未配置 query runner 时返回 503 `agent runner is not configured` 并把 task 收敛为 `failed`。
- `GET|POST /tenant/agent-tasks/{id}/pending-inputs`：读取或确认加入当前 Web Agent 会话的等待输入。`POST` 请求包含 `client_input_id`、`content`、可选 `direction` 和 `attachments`，返回 `202 Accepted`；同一会话按 `client_input_id` 幂等。运行中只入队，空闲时由服务端 coordinator 自动按 FIFO 消费，浏览器关闭不影响执行。
- `PATCH|DELETE /tenant/agent-tasks/{id}/pending-inputs/{input_id}`：编辑正文/方向或删除仍可修改的候选。`POST .../move-up` 上移顺序，`PATCH .../direction` 单独调整方向，`POST .../retry` 将 `failed` 候选增加 attempt 后重新入队。
- `POST /tenant/agent-tasks/{id}/pending-inputs/{input_id}/side-chat`：从候选创建独立分支 session 和 `ready` task，返回 `session_id`、`task_id`、`source_pending_input_id`；重复请求优先返回已有侧聊，原候选保持不变。
- `GET|PATCH /tenant/agent-tasks/{id}/pending-input-settings`：读取或开关当前会话排队。关闭后保留已有候选且 coordinator 不会静默发送，重新开启后可继续处理。
- 等待输入生命周期沿现有 task event/SSE 输出 `input_queued`、`input_reordered`、`input_updated`、`input_running`、`input_sent`、`input_failed`、`input_cancelled`、`input_retried`。payload 仅允许 `input_id`、`status`、`sequence`、`attempt`、`dispatched_task_id`、`error_code`，不包含正文、方向、client ID、附件 metadata、inline data 或私有 URL。
- `PATCH /tenant/agent-tasks/{id}/permissions/{request_id}`：解析 Web Agent runner 发出的待审批 `permission_request`。请求体 `{ "allowed": true|false, "reason": "...", "destination": "...", "rule": "..." }`，服务端只会 resolve 当前进程内仍 pending 的 request，并追加 `permission_resolved` event；非 pending request 返回 404，未配置 registry 返回 409。

- `PATCH /tenant/agent-tasks/{id}/questions/{request_id}`：回答 `AskUserQuestion`，body 为 `{ "answer": "Architecture diagram" }`。答案去除首尾空白后不得为空或超过 16384 bytes；非法 JSON、额外字段、多个 JSON 值返回 400。任务先按当前 tenant/user 授权，再匹配等待中的 task/request；回答事件持久化成功后才唤醒原 Run。相同 request 的相同答案可幂等重试，改变答案、已取消、已过期或不匹配的等待请求返回 409；无权读取的 task 按已有规则返回 403/404，未鉴权返回 401。审计只记录操作和任务 ID，不记录问题或答案正文。

问题通过已有 Task SSE 和 Session conversation SSE/history 传输：`user_question_request` 包含 `request_id/tool_id/question/choices/expires_at`，`user_question_resolved` 包含 `request_id/status`，`status=answered` 时另带 `answer`；其他终结状态为 `cancelled/expired`。Session snapshot 派生状态为 `waiting_input`，数据库原 task 仍为 running，回答在同一 Run 内继续，不创建新任务。等待不触发流式 idle timeout，但仍受原 Run 总超时和最多十分钟提问等待时间约束。刷新或切换页面可以重建问题；服务进程重启不恢复执行栈，与既有 detached Run 生命周期一致。旧版和新版 Web Agent 均消费问题事件。

`text_delta` / `thinking_delta` 的 `content` 按原始流片段保存，包括独立空格、制表符和换行。客户端应原样拼接，不逐片段 trim；SSE 和历史回放使用相同内容与既有游标去重。早期已丢失空格的历史不会自动修复。

上下文来源提取按有效消息与结果选择证据，不把 `text_delta/thinking_delta/usage` 等传输事件逐条塞入 Handoff。预算约束保持不变，超限返回 `422 budget_exceeded` 并由 WebUI 显示上下文过大。Prepare 后 Attach/Launch 的启动失败必须通过 ready 状态 CAS 写入 failed 终态及失败事件，释放会话；不会把已运行任务误标为失败。

同步 GenerateImage/EditImage 的浏览器产物仍返回受鉴权媒体引用。默认仅向对话模型返回产物信息和未做视觉检查的说明，兼容纯文字对话模型；配置 `imageGeneration.previewInContext=true` 可为支持图片输入的对话模型开启预览，服务端按同一 tenant/user/session 读取并验证图片后内联编码，不把 `/tenant/media/assets/...` 相对地址交给外部模型下载。该设置不影响用户上传图片及异步渠道生图。生成结果与后续模型回复状态分开显示；后续回复失败时已经生成的图片仍可查看和下载。
- `POST /tenant/agent-tasks/{id}/cancel`：取消当前 server 进程内仍在运行的 task，并写入持久化 cancelled 状态和 event；如果 task 不在当前进程运行，返回 409。CLI 可用 `tenant agent-tasks cancel <id>` 写入持久化取消状态，运行中的 sub-agent 会在 turn/tool 边界轮询并退出。

Goal API：

- `GET /tenant/goals?active=true&status=active&limit=100`：读取当前用户 Goal 列表，支持 active/status 过滤。
- `POST /tenant/goals`：创建 Goal，支持 `objective`、`session_id`、`cwd`、`model`、`turn_budget`、`token_budget`，并写入 `goal_started` event。该接口只创建持久化管理记录，不直接启动 runner。
- `GET /tenant/goals/{id}`：读取当前用户单个 Goal。
- `PATCH /tenant/goals/{id}`：更新 Goal 状态、预算、usage、blocker、checkpoint、reason、next_action 和 error 等字段；状态变化会追加 `status_changed` event。
- `GET /tenant/goals/{id}/events?limit=100`：按时间顺序读取 Goal event。
- `GET /tenant/goals/{id}/plan`：读取当前用户 Goal 的结构化 plan，包括 current step、steps、acceptance criteria、dependencies 和 risks；plan 不存在时返回 404。
- `GET /tenant/goals/{id}/evidence?limit=100`：读取当前用户 Goal 的结构化 evidence，默认按存储顺序返回，支持 limit 限制返回数量。
- `POST /tenant/goals/{id}/stop`：按 CLI `goal stop` 语义把 Goal 置为 `stopped`，记录 `goal_stopped` event。
- `POST /tenant/goals/{id}/resume?force=true`：按 CLI `goal resume` 语义把 Goal 置为 `active`，记录 `goal_resumed` event；`complete` 不可恢复，`failed` 默认需要 `force=true`。
- `POST /tenant/goals/{id}/run`：默认通过 server query runner 执行 exactly one turn，写入 `turn_started` / `turn_finished` 或 `turn_failed` event，并由 evaluator 推进 `active/complete/blocked/failed` 状态。可通过 `?continuous=true&max_turns=5` 启用受控同步 continuous runner，逐轮复用 Goal lock、预算、stop、审计和事件广播；`max_turns` 默认 5，上限 20，达到上限仍未结束时会 blocked 为 `continuous_max_turns_reached`。该接口不创建 detached server background job，避免绕过 tenant/request 上下文。默认使用 deterministic evaluator；可通过 `?evaluator=model` 对没有显式 `GOAL_STATUS` 的模糊结果启用 model-classified evaluator，模型分类失败时回退 deterministic 结果。Goal 带有合法本地 `session_id` 时，API runner 会在执行前创建与 CLI runner 同名的 per-turn 本地 transcript checkpoint，并把 `last_checkpoint` 和事件 `checkpoint` 写成 `goal:<goal_id>:turn:<n>`；未配置 query runner 时返回 503 且不修改 Goal。
- Mobile WebSocket 可订阅 Goal event，见下方 Mobile Chat API 的 WebSocket 控制通道和 [goal_mode/goal_api_mobile_todo.md](goal_mode/goal_api_mobile_todo.md)。

## Mobile Chat API

移动端不要直接依赖内部 `/query` 或 OpenAI-compatible schema。P0 提供 `/mobile/chat/...` API，使用 HS256 Bearer JWT 作为用户登录态：

移动端 API 使用 Gin 原生 route group：`/mobile/chat` group 统一挂载 JWT middleware、完整链路 access log、请求 body 摘要日志和 route params。access log 会记录 `mobile.request.start` / `mobile.request.finish`，包含开始时间、结束时间、总耗时、状态码、tenant_id/tenant_key/user/device/session、route、method 等字段；body 日志只输出 `body_bytes/model/session_key/content_chars/attachment_count` 等摘要，不打印 JWT、聊天全文、附件 URL 或语音内容。

```json
{
  "tenant_key": "yutang",
  "user_id": "user-123",
  "device_id": "ios-device-1",
  "exp": 1799999999,
  "allowed_models": ["claude-sonnet-4-6"],
  "rate_limit_per_minute": 30,
  "daily_message_quota": 1000,
  "daily_token_quota": 200000
}
```

server 通过 `GOLANG_CC_MOBILE_JWT_SECRET` 或 `MOBILE_JWT_SECRET` 配置 JWT secret。未配置时移动端 API 返回 503。模型权限、限流和配额可由 server 默认环境变量配置，也可由 JWT claims 覆盖到单个用户。

本地 WebUI 调试可临时设置 `GOLANG_CC_MOBILE_DEV_AUTH=true`，在未配置 mobile JWT secret 时允许 `/mobile/chat/*` 从 `X-Tenant-Key`、`X-User-Id`、`X-Device-Id` 读取 dev identity。该开关只用于本地开发和测试，不应在公网或生产环境启用。

- `POST /mobile/chat/sessions`：创建 session，可传 `session_key/title/model/cwd/metadata_json`，不传 `session_key` 时服务端生成。
- `GET /mobile/chat/sessions?limit=50`：读取当前用户 session 列表。
- `GET /mobile/chat/sessions/{id}`：读取 session 详情；如果该 tenant session 已有 `role=recap` 消息，响应会带只读 `latest_recap` 字段。
- `PATCH /mobile/chat/sessions/{id}`：更新 session 标题、状态、模型、cwd 或 metadata。
- `DELETE /mobile/chat/sessions/{id}`：归档 session。
- `POST /mobile/chat/sessions/{id}/branch`：从当前会话 fork 新 session，可传 `until_turn` 或 `until_message_id`。
- `GET /mobile/chat/sessions/{id}/messages?after_turn=0&cursor=0&limit=50`：读取消息，`after_turn` 和 `cursor` 用于移动端增量同步；响应包含 `next_cursor/has_more`。
- `POST /mobile/chat/sessions/{id}/messages/stream`：发送消息并接收移动端 SSE。
- `POST /mobile/chat/sessions/{id}/messages/{message_id}/cancel`：停止活跃 stream，并把 assistant message 标记为 `cancelled`。
- `POST /mobile/chat/sessions/{id}/messages/{message_id}/regenerate`：基于目标 assistant message 前一条 user message 重新生成 assistant variant。
- `POST /mobile/chat/attachments/presign`：生成附件上传契约，返回 `attachment_id/object_key/upload_url/attachment`。

`message_key` 是移动端重试幂等键。客户端为每次发送生成稳定 key；如果网络断开后重复提交同一个 `message_key`，server 会重放既有 assistant SSE，不会重复创建 user/assistant message。

移动端消息支持附件 metadata；图片、语音/音频和文件通过 `attachments` 随用户消息落入 `content_json`，并作为附加上下文传给模型：

```json
{
  "content": "帮我总结这张图和这段语音",
  "model": "claude-sonnet-4-6",
  "attachments": [
    {
      "type": "image",
      "media_type": "image/png",
      "name": "screenshot.png",
      "url": "https://cdn.example.test/screenshot.png",
      "size_bytes": 123456,
      "sha256": "..."
    },
    {
      "type": "voice",
      "media_type": "audio/m4a",
      "name": "voice.m4a",
      "url": "https://cdn.example.test/voice.m4a",
      "transcript": "语音转写文本"
    }
  ]
}
```

默认附件策略：单附件最大 25MB、最多 8 个，类型为 `file/image/audio/voice`。可用 `GOLANG_CC_MOBILE_MAX_ATTACHMENT_BYTES`、`GOLANG_CC_MOBILE_MAX_ATTACHMENTS`、`GOLANG_CC_MOBILE_ALLOWED_ATTACHMENTS` 调整。

上传前先请求 presign：

```json
{
  "type": "image",
  "media_type": "image/png",
  "name": "screenshot.png",
  "size_bytes": 123456,
  "sha256": "..."
}
```

响应中的 `attachment` 可直接放进后续 stream request。未配置 `GOLANG_CC_MOBILE_UPLOAD_BASE_URL` 时，server 返回 `mobile-upload://...` 占位 URL；配置 `GOLANG_CC_MOBILE_S3_BUCKET`、S3 endpoint/region/access key 后，server 会使用 AWS SDK v2 生成 S3-compatible presigned PUT `upload_url`，可对接 S3、MinIO、OSS 兼容网关等对象存储。`GOLANG_CC_MOBILE_S3_PUBLIC_BASE_URL` 用于生成上传后的 CDN/公开访问 URL。

S3-compatible 上传签名环境变量：

- `GOLANG_CC_MOBILE_S3_ENDPOINT` / `MOBILE_S3_ENDPOINT`
- `GOLANG_CC_MOBILE_S3_REGION` / `MOBILE_S3_REGION`
- `GOLANG_CC_MOBILE_S3_BUCKET` / `MOBILE_S3_BUCKET`
- `GOLANG_CC_MOBILE_S3_ACCESS_KEY` / `MOBILE_S3_ACCESS_KEY` / `AWS_ACCESS_KEY_ID`
- `GOLANG_CC_MOBILE_S3_SECRET_KEY` / `MOBILE_S3_SECRET_KEY` / `AWS_SECRET_ACCESS_KEY`
- `GOLANG_CC_MOBILE_S3_SESSION_TOKEN` / `MOBILE_S3_SESSION_TOKEN` / `AWS_SESSION_TOKEN`
- `GOLANG_CC_MOBILE_S3_PREFIX` / `MOBILE_S3_PREFIX`
- `GOLANG_CC_MOBILE_S3_PUBLIC_BASE_URL` / `MOBILE_S3_PUBLIC_BASE_URL`
- `GOLANG_CC_MOBILE_S3_PATH_STYLE` / `MOBILE_S3_PATH_STYLE`
- `GOLANG_CC_MOBILE_S3_PRESIGN_TTL_SECONDS` / `MOBILE_S3_PRESIGN_TTL_SECONDS`

限流/配额默认使用进程内 limiter。配置 `GOLANG_CC_MOBILE_USAGE_STORE_PATH` 后，server 使用 file-backed usage store，重启后仍保留当天 message/token 计数；配置 `GOLANG_CC_MOBILE_REDIS_ADDR` 后会优先使用 Redis Lua 原子计数，适合多实例 API server 共享移动端 minute/day message/token 配额。

租户级 quota 依赖 MySQL tenant storage 保存配置、账本和每日汇总。实时 QPS、每日 token/message、并发计数默认使用进程内 store；多实例部署时配置 `GOLANG_CC_QUOTA_REDIS_ADDR` 使用 Redis Lua 原子 reserve/settle。租户可以单独开启或关闭 quota：`quota_enabled=false` 或某个 limit 字段为 `null` 表示不限制，例如租户 A 可以每天 100 万 token，租户 C 可以不限制。

Redis quota 相关环境变量：

- `GOLANG_CC_MOBILE_REDIS_ADDR` / `MOBILE_REDIS_ADDR`
- `GOLANG_CC_MOBILE_REDIS_PASSWORD` / `MOBILE_REDIS_PASSWORD`
- `GOLANG_CC_MOBILE_REDIS_DB` / `MOBILE_REDIS_DB`
- `GOLANG_CC_MOBILE_REDIS_PREFIX` / `MOBILE_REDIS_PREFIX`
- `GOLANG_CC_QUOTA_REDIS_ADDR` / `QUOTA_REDIS_ADDR`
- `GOLANG_CC_QUOTA_REDIS_PASSWORD` / `QUOTA_REDIS_PASSWORD`
- `GOLANG_CC_QUOTA_REDIS_DB` / `QUOTA_REDIS_DB`
- `GOLANG_CC_QUOTA_REDIS_PREFIX` / `QUOTA_REDIS_PREFIX`

租户 quota API：

- `GET /tenant/quota/config`：查看当前租户 quota 配置。
- `PUT /tenant/quota/config`：创建或更新当前租户 quota 配置，需要 `owner` 或 `admin`。
- `GET /tenant/usage/daily`：查看当前租户每日 token/message/request 汇总。
- `GET /tenant/usage/ledger`：查看当前租户每次请求的用量账本。
- `GET /tenant/quota/events`：查看限流拒绝、配置变更等 quota 事件。

配置示例：

```json
{
  "quota_enabled": true,
  "qps_limit": 10,
  "daily_token_limit": 1000000,
  "daily_message_limit": null,
  "max_concurrent_requests": 10,
  "timezone": "Asia/Shanghai",
  "reserve_output_tokens": 4096,
  "status": "active"
}
```

WebUI 的 Observability -> Quota 页面支持查看和修改这些配置，并查看 daily usage、ledger 和 quota events。

首轮成功响应后，如果 session 还没有标题，server 会异步调用 `SessionTitleFunc` 生成标题并回写 session；标题生成失败不影响 SSE 主链路。

移动端也可以连接 `GET /mobile/chat/ws` 作为多端同步控制通道。连接后先收到 `connected`，发送 `{"type":"subscribe","session_id":5}` 可订阅会话；同一 tenant/user 下该会话的 `message_start`、`delta`、`error`、`message_stop` 会以 `{"type":"message_event","event":"delta",...}` 形式广播给所有已订阅设备。发送 `{"type":"subscribe_goal","goal_id":"goal_abc"}` 可订阅 Goal events；后续 `goal_started`、`status_changed`、`goal_stopped`、`goal_resumed`、`turn_started`、`turn_finished`、`turn_failed` 会以 `{"type":"goal_event","event":"goal_stopped","goal_id":"goal_abc","status":"stopped","goal_event":{...}}` 形式广播。Goal plan/evidence 更新也复用 `goal_event` envelope，`event` 分别为 `goal_plan_updated` 和 `goal_evidence_added`，并带 `goal_plan`、`goal_evidence`、`step_id`、`step_status` 等字段，方便 Mobile/WebUI 增量刷新当前 step、criteria 和 evidence。发送 `{"type":"cancel","session_id":5,"message_id":88}` 可取消活跃 stream；取消、订阅和广播都按 mobile JWT 的 tenant/user 做隔离。

移动端 SSE 固定输出 envelope：

```text
event: message_start
data: {"type":"message_start","session_id":5,"message_id":88,"message_key":"client-msg-1","status":"streaming"}

event: delta
data: {"type":"delta","delta":"hello","status":"streaming"}

event: message_stop
data: {"type":"message_stop","session_id":5,"message_id":88,"status":"completed"}
```

## Tenant Admin And Audit

- `GET /tenant/tenants`、`POST/PATCH /tenant/tenants`、`DELETE /tenant/tenants?tenant_key=...` 需要当前用户 role 为 `owner` 或 `admin`。
- `POST/PATCH /tenant/tenants` 可用 `tenant_key` 创建或更新租户 name、status、settings_json，并恢复软删除租户。
- `DELETE /tenant/tenants?tenant_key=...` 会软删除租户并把租户状态置为 `archived`。
- `GET /tenant/users`、`POST/PATCH /tenant/users`、`DELETE /tenant/users?user_key=...` 需要当前用户 role 为 `owner` 或 `admin`。
- `POST/PATCH /tenant/users` 可用 `user_key` 创建或更新租户内任意用户的 email、display_name、role、status、user_info_json、metadata_json。
- `DELETE /tenant/users?user_key=...` 会软删除并把用户状态置为 `archived`。
- 租户写操作会记录 audit log，包含 action、resource_type、resource_id、trace_id 和 actor user。
- `GET /tenant/tenants?limit=100&cursor=0&search=...` 需要 `owner/admin`，按租户 key/name/status 搜索并返回 `next_cursor/has_more`。
- `GET /tenant/users?limit=100&cursor=0&search=...` 需要 `owner/admin`，按 user key/email/display name/role/status 搜索并返回 `next_cursor/has_more`。
- `POST /tenant/skills` 写入租户级 skill，需要 `owner/admin`；普通用户可继续读取 effective skills 和维护自己的 skill override。
- `POST /tenant/skills/rollback` 回滚租户级 skill，需要 `owner/admin`，请求体示例：`{"skill_key":"go-review","version":1}`。可选 `target_version` 用于指定新版本号，必须大于当前最新版本，否则返回 400。响应包含 `id`、`skill_key`、`from_version`、`version`，其中 `version` 是复制后的新最新版本。WebUI Skills 面板会展示版本历史，并提供只读 Validate 与 Rollback 按钮。
- `GET /tenant/audit?limit=100&cursor=0&search=...` 需要 `owner/admin`，按 action/resource/trace 搜索当前租户 audit log，并返回 `next_cursor/has_more`。

## Tenant Images

当全局 image generation service 已配置时，以下接口复用 provider-neutral 图片服务，并按当前认证上下文隔离 `tenant_id + user_id + session_id`：

- `GET /tenant/images/capabilities` 返回当前配置中脱敏的图片 provider/model 能力目录，不返回 endpoint 或凭据。
- `POST /tenant/sessions/:id/images/generations` 使用 JSON `{"prompt":"...","provider":"agnes","model":"agnes-image-2.5-flash","resolution":"2K","aspect_ratio":"16:9"}` 生成图片；旧的 `model/size/quality` 字段继续兼容，响应只返回 artifact 元数据和受控 asset URL。
- `POST /tenant/sessions/:id/images/edits` 使用 multipart 表单编辑/重绘，字段包括 `prompt`、可选 `image`、`source_asset_id`、`mask` 及图片参数。
- `GET /tenant/sessions/:id/images` 返回当前会话的生成记录。
- `GET /tenant/media/assets/:asset_id` 在鉴权后流式读取图片 blob，服务端不会暴露文件系统路径或 base64。

所有写入接口都会先验证会话归属；跨用户、跨租户或跨会话访问返回 403/404。图片服务未配置时生成和读取接口返回 409。

## Feishu Images

`channels run` 会复用相同的 `imageGeneration` 配置和 tenant-scoped media store。飞书消息可直接发送自然语言让 Agent 调用 `GenerateImage` / `EditImage`，也支持快捷命令：

- `/image <提示词>`：生成并发送图片。
- `/image edit <asset_id> <提示词>`：编辑当前租户、用户和会话拥有的图片并发送。

图片发送在 Outbox 投递阶段读取授权 asset，通过 Lark SDK 内存上传后发送 `msg_type=image`；Inbox/Run/Outbox 只保存 asset metadata 和幂等键。首次启用图片发送需要飞书租户权限 `im:resource`，可运行 `golang-cc channels authorize feishu --scope im:resource` 完成增量授权。

### Channel 异步图片任务

当 `imageGeneration.asyncChannelEnabled=true` 时，只有通过 `imageGeneration.asyncChannelAccountKeys` 灰度筛选的 Feishu account 走异步路径；空列表表示当前配置的全部 account，非空列表按 account key 精确匹配。channel 图片工具和 `/image` 命令只负责把每张图片写入 MySQL `image_generations` 队列，返回 `queued` receipt；provider 工作与 channel run deadline 脱钩，query 仍可能继续另一个模型 turn，随后正常收敛。重复的 runtime tool invocation 复用同一 receipt，不会重复创建逻辑任务。

独立 worker 逐条 claim 任务并使用独立的 180 秒 attempt timeout、heartbeat lease 和最多三次退避重试（约 30 秒、2 分钟、5 分钟，带抖动）。每张图片独立收敛，完成后通过 `image_completion_outbox` 再创建既有 `channel_messages/channel_outbox`，因此图片可能在受理卡片之后延迟到达；投递失败只重试投递，不重新生成。批次卡片显示 accepted/rejected 和完成进度，支持 `/image status <generation_id>`、`/image cancel <generation_id>`、`/image retry <generation_id>`。人工 retry 创建新 generation 并保留 `retry_of_generation_id`。

任务状态包括 `queued`、`running`、`retry`、`completed`、`failed`、`outcome_unknown`、`cancelled`、`dead`；错误分类区分 `caller_cancelled`、`caller_deadline_exceeded`、`provider_timeout`、`provider_rate_limited`、`provider_unavailable`、`provider_rejected` 和 `provider_outcome_unknown`。所有查询、取消、重试和资产读取继续校验 `tenant_id + user_id + session_id`，日志和 outbox 不保存凭据、完整 prompt 或私有 URL。

本阶段 WebUI、CLI、TUI 以及同步 tenant image HTTP API 的请求和 Artifact JSON 契约保持不变；它们不会因为 channel 异步开关自动改为 HTTP 202。回滚时关闭异步开关并停止 image worker，保留新增 migration 和队列数据，恢复后的 channel 走旧同步路径并承受原有超时边界。

## Tenant Telemetry

Telemetry 是面向排障、性能分析、成本统计和产品运营的结构化事件体系。它和普通日志不同：事件名、分类、状态、模型、工具、token、耗时、trace、tenant/user/session 等字段是稳定结构，便于后续接入后台面板、告警、成本报表或分析平台。

已内置事件包括：

- `api.request.started` / `api.request.finished`
- `query.run.started` / `query.run.finished`
- `model.request.started` / `model.request.finished`
- `tool.execution.started` / `tool.execution.finished`
- `permission.decision`
- `mobile.chat.stream.started` / `mobile.chat.stream.finished`

事件 sink：

- `LoggerSink`：默认输出到 `internal/observability` 统一日志。
- `RecorderSink`：当请求已通过 server/mobile 鉴权，且存在 `X-Tenant-Key` / `X-User-Id` 或 mobile JWT tenant/user context 时，写入 MySQL `tenant_telemetry_events`。
- `HTTPSink`：配置 `GOLANG_CC_TELEMETRY_EXPORT_URL` 后，把脱敏后的事件 POST 到外部 analytics、日志平台或 OTel collector HTTP bridge；`GOLANG_CC_TELEMETRY_EXPORT_TOKEN` 会作为 Bearer token。`GOLANG_CC_TELEMETRY_EXPORT_FORMAT` 支持空值/`json`、`otel`、`datadog`，`GOLANG_CC_TELEMETRY_EXPORT_SERVICE` 可设置 service name。`GOLANG_CC_TELEMETRY_EXPORT_API_KEY` / `DATADOG_API_KEY` / `DD_API_KEY` 会按 format 映射为 `DD-API-KEY` 或 `x-api-key`；`GOLANG_CC_TELEMETRY_EXPORT_HEADERS` 可传 JSON object 追加自定义 header。
- `MemorySink`：测试用内存事件收集器。

为了避免泄露敏感内容，telemetry 会对 properties 做脱敏：不会保存 API key、JWT、Authorization、token、password、secret、prompt、content、transcript、private URL 等字段；字符串值会截断。

API：

- `GET /tenant/telemetry?limit=100&cursor=0&search=...` 需要 `owner/admin`，按 event name、category、source、status、trace、resource、model、tool 搜索当前租户 telemetry events，并返回 `next_cursor/has_more`。
- `POST /tenant/telemetry` 可写入外部或自定义 agent 事件，仍使用当前 tenant/user context，适合自定义 agent 底座扩展自己的业务事件。

## Observability

Gin middleware 会把 `X-Trace-Id`、`X-User-Id`、`X-Tenant-Key` 注入 request context，并在 API、query、tool、service、repository 日志中输出 `traceid`、`userid`、`tenantkey`、`action`、`function`。

日志统一走 `internal/observability`，默认 zap JSON 输出，支持 `debug/info/error/panic` 级别。可用环境变量调整：

```bash
export GOLANG_CC_LOG_LEVEL=debug
```
# 常用提示词

WebUI 2.0、桌面 v2 (`go-e2e`) 及 legacy Web Agent 使用 `/tenant/prompt-templates` 管理当前租户/用户的提示词模板。支持 `GET`（可选 `search`、`category`、`limit`）、`POST` 保存、`PATCH` 按 `id` 更新，以及 `/tenant/prompt-templates/:id` 的 `DELETE`。模板只进入 Composer 草稿，不会自动发送，也不会注入 runtime system prompt。

- `POST` 不带 `id` 时按当前 tenant/user/title 原子 upsert；重复或并发保存同名模板保留同一 ID。带 `id` 时仅更新已有且属于当前用户的记录。
- `PATCH` 必须提供非零 `id`、非空 `title/content`，否则返回 `400`；`pinned=false`、`sort_order=0` 和空 `category` 会实际落库。它是完整编辑表单保存，不是任意字段的局部 merge。
- 更新不存在或其他 tenant/user 的 ID 返回 `404`，不会插入新记录。改名与当前用户另一模板重名返回 `409`，原记录保持不变。
- 删除不存在或其他 tenant/user 的 ID 返回 `404`；成功返回 `204`。所有操作仍需服务鉴权及可信 tenant/user context。
- 查询和保存成功返回 JSON；错误返回 `text/plain`，不是 JSON 字符串。删除成功无响应体。

两个 Web UI 使用同一后端和 tenant/user 时共享目录；desktop-v2 使用自己的本地 SQLite
文件，不与 WebUI 或 legacy WebUI 跨库复制或迁移提示词数据。legacy WebUI 接入仅新增
API 消费入口，不改变请求/响应或鉴权契约。
