# WebUI Frontend

本文档是 golang-cc WebUI 前端工程的维护入口。技术路线和 API 分层见 [webui_mobile_chat_lab_plan.md](webui_mobile_chat_lab_plan.md)，本文聚焦实际目录、功能范围、运行方式、测试和 P0/P1 交付清单。

## 目录边界

前端工程独立位于：

```text
web/
```

后端仍由 Go API Server 提供：

```text
internal/server/
```

前端不得直接修改 TUI、CLI、query loop、permissions、slash commands 等本地交互链路。WebUI 只能通过 HTTP API 验证 API Server / Mobile Chat / Tenant / Trace 能力。

## 技术栈

- Vite + React + TypeScript。
- assistant-ui 作为聊天交互和 runtime adapter 的主要参考。
- lucide-react 图标。
- TanStack Query 管理 server state。
- 本地 CSS 和小组件先承载 P0；P1 再按 shadcn/ui 风格拆分更完整组件。

## 本地运行

终端 1：启动 Go API Server。

```bash
export GOLANG_CC_MOBILE_DEV_AUTH=true
go run ./cmd/golang-cc server \
  --host 127.0.0.1 \
  --port 8080 \
  --auth-token test-token
```

终端 2：启动前端。

```bash
npm --prefix web install
npm --prefix web run dev
```

默认 Vite dev server 会把 `/api/*` 代理到 `http://127.0.0.1:8080`。如需改后端地址：

```bash
VITE_GO_CLAUDE_API_TARGET=http://127.0.0.1:18080 npm --prefix web run dev
```

显式设置 `VITE_GO_CLAUDE_API_TARGET` 后，开发态 `/webui/agent` 直达页会继续使用 `/api` Vite proxy；生产静态托管仍使用同源根路径 API。

生产或本地静态托管：

```bash
npm --prefix web run build
export GOLANG_CC_WEBUI_DIR=web/dist
go run ./cmd/golang-cc server \
  --host 127.0.0.1 \
  --port 8080 \
  --auth-token test-token
```

打开：

```text
http://127.0.0.1:8080/webui/?token=test-token
```

生产 build 会使用 `/webui/` 作为静态资源 base，Go server 在首次带 `?token=` 访问 `/webui/` 时写入仅限 `/webui/` 路径的鉴权 cookie，后续 `/webui/assets/...` 资源请求不需要重复拼接 token。静态托管模式下 WebUI 默认使用同源根路径调用 `/tenant/*`、`/mobile/*`、`/trace/*` 等 API；Vite dev server 才默认使用 `/api` proxy。
如果浏览器 localStorage 里保留了旧的同源 `/api` 或 `http://127.0.0.1:18080/api` API Base，静态 `/webui/` 模式会在加载 identity 时自动迁移为空，避免租户/用户列表继续请求不存在的 `/api/tenant/*` 路由。

## P0 交付清单

P0 目标是完整复刻移动端主链路，作为 API Server 测试驾驶舱：

- Dev identity / auth context：API base、API token、mobile JWT、tenant、user、device、model。
- 默认 demo identity：前端默认使用 `webui-local` / `webui-local-user`，适合本地 owner/admin telemetry、audit、trace 调试；旧浏览器缓存里的 `yutang` / `web-test-user` 会自动迁移到该 demo identity，避免 owner-only 面板显示 `tenant access forbidden`。
- Settings drawer：左下角只保留齿轮设置入口；点击后打开侧栏二级抽屉，集中放置租户/用户选择、身份恢复、认证状态、token 清理和中英文切换，减少常驻侧栏噪音。
- Auth status：设置抽屉显示 API token 与 Mobile JWT 是否已配置，可一键清除 API token 或 Mobile JWT，避免生产/预发切换时复用旧凭证。
- Tenant/User selector：设置抽屉中的租户选择器通过 `/tenant/tenants?limit=100` 从 API Server 动态读取数据库租户列表；用户选择器会跟随当前租户调用 `/tenant/users?limit=100` 读取该租户用户。切换租户或用户后会保存到本地 identity、清空当前选中会话并刷新页面数据；如果当前用户无 owner/admin 权限或接口不可用，会保留当前租户/用户作为 fallback，不阻塞聊天测试。
- Identity recovery：当租户/用户列表不可用时，侧栏会显示后端返回的具体错误，例如 `tenant access forbidden` 或 `tenant not found`，并提供“恢复本地身份”按钮，一键回到 `webui-local/webui-local-user`。
- Tenant user manager：Run Context 面板可查看当前租户用户、创建/更新用户 display name/email/role/status，并一键切换 WebUI identity。该能力复用 `/tenant/users`，需要当前用户在租户内拥有 owner/admin 权限；权限不足时页面保留当前 identity，不影响聊天测试。
- Test Scenario seed：在 Run Context 面板用烧瓶按钮一键创建当前用户、owner/member demo 用户，写入 memory、profile、`CLAUDE.md`、tenant skill、user override，并创建测试 session。
- Mobile session：列表、创建、选择、更新标题/状态、归档。
- Mobile message：历史、SSE 发送、停止、regenerate、branch。
- Thinking rendering：思考 delta 使用统一边界拼接，避免英文单词粘连；思考内容独立为可折叠、限高、内部滚动的 Markdown 区块。生成中显示“正在思考”，完成/失败/取消/超时显示对应结束状态，不把内部 `thinking` 标记泄漏到完成消息元数据。未配置 thinking 设置时默认摘要折叠；显式 `thinkingMode` 和 legacy `showThinking` 仍按原优先级生效。
- Markdown rendering：`MarkdownLite` 在进入块级解析前执行宽松 Markdown 结构恢复。对行首已确认的无序/有序列表，恢复模型偶发输出在同一行的后续列表标记；代码围栏和行内代码保持原样，单个普通连字符不按列表拆分。该规则属于通用 Markdown 容错，不绑定 A/B/C 等具体文案。
- Mobile attachment upload：Chat Lab 支持选择本地文件，计算 SHA-256 后调用 `/mobile/chat/attachments/presign`，对 HTTP(S) upload URL 执行二进制 `PUT`，并把返回 attachment metadata 注入下一次 mobile stream；非 HTTP(S) upload URL 会显示 metadata-only 状态，不伪装成真实上传。
- Memory：读取、写入长期记忆。
- Profile：读取、写入用户画像。
- Documents：读取、写入 `CLAUDE.md` / 用户文档。
- KB Search：写入 tenant knowledge document，执行知识库检索，并展示 chunk `search_mode` / `score`，用于验证 chat 模式知识召回。
- Scoped Memory：读取/写入 team memory 和 managed memory，用于验证团队/托管规则进入 prompt context 的效果。
- Memory Review：查看显式 remember 和 AutoMem pending 候选，按候选类型、风险状态、来源 session 过滤，展示详情、metadata、来源 session/message/trace，并支持 approve/reject/archive；pending/rejected/archived 不进入 prompt context，批准后转为普通 active memory。
- Skills：读取 effective skills、读取/写入 tenant skills、用户 override。
- Goals：读取、创建、筛选和管理 `/tenant/goals`，支持 stop/resume/run one-turn、预算/usage 摘要和 event timeline，用于验证多租户 Goal Mode API 与 MySQL 持久化。
- Trace：读取当前 tenant session normalized trace summary，跳转内置 `/trace`。
- Trace evidence：在 WebUI 内直接展示 session-scoped runtime timeline、model spans、tool spans、audit/permission events、token usage 和 prompt cache 摘要，用于快速判断当前会话是否真的完成全链路。
- Loops：Observability 下新增 Loops tab，通过 `/runtime/background?kind=loop` 查看本地 `/loop` 任务、最新输出和运行状态；通过 `/runtime/background` 创建 loop，通过 `/runtime/background/{id}` 编辑 loop，通过 `/runtime/background/{id}/run` 立即执行，通过 `/runtime/background/{id}/runs` 查看运行历史，并可调用 `/runtime/background/{id}/stop` 停止指定 loop。
- Telemetry：读取近期 telemetry，用于确认 mobile/chat/model/tool 事件落库。
- 自动压缩和 skills 渐进式加载：通过长会话、skills 触发后在 trace/telemetry 面板验证。
- Lifecycle Validation：聚合会话、消息、memory、profile、documents、skills、telemetry、trace、token usage、prompt cache 的通过/告警/失败状态，作为真实 mobile/chat 全链路测试的第一页检查清单。
- Capability Evidence：从 trace events、trace summary、telemetry properties 和 tenant 数据提取证据，分别标记 mobile stream、自动压缩、skills 渐进加载、长期记忆、短期文档记忆、用户画像和 prompt cache。

## P1 交付清单

P1 目标是把测试台产品化，能作为长期使用的 WebUI：

- Chat workspace：更完整的消息状态、错误恢复、branch 详情、regenerate variants、快捷操作。当前 stream 状态已区分 `idle`、`streaming`、`completed`、`failed`、`cancelled`、`timed_out`，并对 send/regenerate 使用默认 120s stream timeout，避免浏览器请求无限悬挂。会话列表支持标题/key/id 搜索和状态过滤，聊天区顶部展示当前 tenant/user/model/session 上下文；发送、regenerate、branch、归档和保存 session 后会触发 evidence 刷新，并额外延迟一次刷新以等待 telemetry/trace 落库。
- 附件工作流：presign、真实文件上传、附件 metadata、图片/语音/文件上下文验证。当前前端支持生成上传契约、选择文件自动计算 SHA-256、HTTP(S) upload URL 二进制 `PUT`、手动添加附件 URL/transcript，并随 mobile stream 请求发送 `attachments`。
- Memory/Profile/Documents/KB/Memory Review 工作台：编辑、刷新、历史、检索、审批和错误状态。
- Skills 工作台：tenant skill、user override、effective view、启停和配置 JSON。当前 Skills 面板左侧展示 tenant skills 与 effective skills，右侧可选择编辑、新建、保存 tenant skill、单独保存 override、启停和刷新；保存后下一个 `/query`、OpenAI-compatible chat 或 mobile stream 请求即可热加载生效。
- Goals 工作台：一级导航新增 Goals，二级 tab 读取 `/tenant/goals`、`/tenant/goals/{id}/events`、`/tenant/goals/{id}/stop`、`/tenant/goals/{id}/resume` 和 `/tenant/goals/{id}/run`，可创建目标、按状态/关键字筛选、查看预算/usage、运行 one-turn、停止/恢复并查看事件 timeline。
- Observability 工作台：trace detail、spans、events、token usage、prompt cache、agent tasks、loops、audit、telemetry。Agents tab 读取 `/tenant/agent-tasks`、`/events`、`/message`、`/cancel` 和 `PATCH /tenant/agent-tasks/{id}`，可查看 task 状态/事件流、创建管理记录、发送 agent message、标记完成/失败、取消运行中任务并跳转 parent session；当前 Agent cockpit 支持任务状态筛选、关键字搜索、事件类型筛选、自动刷新、parent/subagent/trace/model 摘要以及 metadata/result JSON 详情。Loops tab 读取 `/runtime/background?kind=loop`、`/runtime/background/{id}/logs`、`/runtime/background/{id}/runs`、`/runtime/background/{id}/run`、`/runtime/background/{id}`、`/runtime/background` 和 `/runtime/background/{id}/stop`，可查看本地 loop 的 prompt、cwd、interval、run count、last/next run、PID、最新日志 tail 和 run history，并支持创建、编辑、立即运行和停止指定 loop；该面板管理的是本机 runtime，不是 tenant MySQL schedule CRUD。Trace 会话选择器位于 “Trace 会话” 表头右侧，Telemetry 顶部也可直接选择当前 tenant/user 下的 session，不必回 Chat Lab 切会话；当前 Trace 分区已按 runtime timeline、model requests、tool calls、audit and permissions、token/cache 证据分组展示，并展示 trace ids 与 session-scoped telemetry event 数；Local Trace tab 读取 `/trace/api/sessions?source=local`、`/trace/api/sessions/{id}?source=local` 和 `/status.workspace`，用于在 WebUI 内查看 CLI/TUI 对话，并支持搜索、All/Today/24h/7d 时间范围、当前 workspace 过滤和空详情诊断；Telemetry tab 在选中会话时使用 `/tenant/telemetry?search={session_id}`，避免最近全局 API 请求噪声把当前聊天痕迹淹没。内置 `/trace` 仍作为轻量独立诊断页，保留 Local/Tenant 两类来源的完整检索入口。
- Trace 时序链路图：Trace 面板内新增二级视图，可在选中 session 后继续选择该 session 下的 trace/request id，并按时间顺序展示 API、mobile、query、LLM、tool、skill、message 等步骤、相对开始时间和耗时条，用于定位某次请求耗时集中在哪个环节。前端优先使用 Trace API 返回的 `parent_id`、`depth`、`sequence`、`self_duration_ms` 展示后端归一化调用树；旧响应缺少这些字段时再回退到前端时间包含关系推断。
- 前端路由和布局：采用一级侧边导航 + 二级 workspace tabs。一级为 Chat Lab、Knowledge、Skills、Goals、Observability、Run Context；二级按 Conversation/Validation、Memory/Profile/Documents、Goals、Trace/Telemetry 等具体任务拆分，避免把所有验证面板堆在同一屏。
- 可调侧边栏：桌面端左侧一级导航可通过右边缘拖拽调整宽度，并持久化到 localStorage；键盘聚焦拖拽条后可用 Left/Right/Home/End 调整。移动端自动回退为顶部块状导航。
- 双语国际化：前端内置轻量 i18n provider，右上角提供 `EN` / `中` 语言切换，当前语言持久化到 localStorage。P1 覆盖主导航、二级 tabs、Chat Lab、Run Context、Knowledge/Skills/Observability 面板、Validation 面板、按钮、tooltip、表单 placeholder 和常见状态提示；接口返回的数据内容，例如 session title、skill key、telemetry event name，保持原始值。
- 验证面板：Chat 页面内展示 lifecycle score 和 Capability Evidence。真实链路完成后应至少确认 session、conversation、telemetry、trace、token usage 为 pass；memory/profile/documents/skills/prompt cache/auto compression 依赖具体测试数据，未触发时会显示 warning。
- 生产托管：前端 build 后可通过 `GOLANG_CC_WEBUI_DIR` 由 Go server 在 `/webui/` 静态托管，同时保留 Vite dev proxy。
- 测试：前端单元测试、关键组件测试、Playwright mock 冒烟测试、桌面/移动视觉回归截图 smoke、真实 MySQL + local provider live E2E。

## 测试

前端：

```bash
npm --prefix web run generate:api-types
npm --prefix web run build
npm --prefix web run test
npm --prefix web run test:e2e
```

`test:e2e` 默认包含 mock API 浏览器冒烟测试和桌面/移动 visual-regression smoke，live API 项目在未设置 `GOLANG_CC_WEBUI_LIVE_E2E=1` 时跳过。视觉用例通过 `page.screenshot({ path: testInfo.outputPath(...) })` 把截图写入 Playwright 输出目录，不提交 screenshot baseline。
`generate:api-types` 会把后端 `docs/swagger.json` 从 Swagger 2.0 转换为 OpenAPI 3，再生成 `web/src/lib/generated/api-types.ts`；前端业务类型通过 `web/src/lib/types.ts` 绑定关键 mobile/tenant/trace schema，减少 API 漂移。
如需在指定 Web 端口运行 Playwright，例如本地调试固定使用 `5175`：

```bash
GOLANG_CC_WEBUI_E2E_PORT=5175 npm --prefix web run test:e2e
```

如果该端口已经有当前 WebUI dev server，可显式复用：

```bash
GOLANG_CC_WEBUI_E2E_PORT=5175 \
GOLANG_CC_WEBUI_E2E_REUSE_SERVER=1 \
npm --prefix web run test:e2e
```

后端相关：

```bash
go test ./internal/server -count=1
git diff --check
```

真实 MySQL API / Mobile Chat E2E：

```bash
export GOLANG_CC_MYSQL_E2E_DSN='user:pass@tcp(127.0.0.1:3306)/golang_cc_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4'
go test ./internal/server -run 'TestMySQLE2E' -count=1
```

该测试会执行 migration，并验证 tenant API、`/query` 持久化、mobile session、mobile stream、message 落库和 trace id 关联。WebUI 浏览器冒烟测试使用 mock API，不依赖 MySQL 或模型 provider；真实链路请结合上述 MySQL E2E 和手动打开 WebUI 验证。

真实 WebUI + API Server Live E2E：

```bash
export GOLANG_CC_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/golang_cc_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4'
go run ./cmd/golang-cc tenant migrate up

export GOLANG_CC_MOBILE_DEV_AUTH=true
go run ./cmd/golang-cc server \
  --host 127.0.0.1 \
  --port 8080 \
  --auth-token test-token
```

另开终端运行：

```bash
GOLANG_CC_WEBUI_LIVE_API_BASE=http://127.0.0.1:8080 \
GOLANG_CC_WEBUI_LIVE_API_TOKEN=test-token \
GOLANG_CC_WEBUI_LIVE_TENANT=webui-live \
GOLANG_CC_WEBUI_LIVE_USER=webui-live-user \
npm --prefix web run test:e2e:live
```

Live E2E 会通过浏览器填写 Context Panel、点击 Test Scenario seed、发送短答 SSE 场景消息并刷新 Lifecycle Validation。测试开始前会清空 `localStorage` 和 `sessionStorage`，确保不会先使用历史浏览器身份误打默认 tenant/user。测试还会先用预置 `yutang` 租户下的 `GOLANG_CC_WEBUI_LIVE_BOOTSTRAP_USER` 自举 owner 用户，再通过 `/tenant/tenants` 创建/更新目标测试租户，并在目标租户下把 `GOLANG_CC_WEBUI_LIVE_USER` 保存为 owner，因此隔离空库 migration 后也能直接运行。浏览器端默认使用 `/api` 走 Vite proxy；后验断言仍直连 `GOLANG_CC_WEBUI_LIVE_API_BASE`，避免本地 API Server CORS 配置影响测试结论。随后它会通过 API 后验检查 `/tenant/messages`、`/tenant/memories`、`/tenant/profile`、`/tenant/documents`、`/tenant/effective-skills`、`/tenant/telemetry?search={session_id}` 和 `/trace/api/sessions/{id}`，确认真实 server 与 MySQL 落库可读。它依赖真实 API Server、MySQL migration 和可用模型 provider；没有这些环境时使用默认 mock E2E。自动压缩、skills 渐进加载和 prompt cache 的 pass 状态需要对应 trace/telemetry 证据，例如 `compact_summary`、`query.autocompact.success`、`active_skill`、`cache_summary` 或 cache token 字段。

2026-06-17 真实环境验证：

```bash
mysql -uroot -e 'DROP DATABASE IF EXISTS golang_cc_webui_live_e2e; CREATE DATABASE golang_cc_webui_live_e2e CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'

GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_webui_live_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
  go run ./cmd/golang-cc tenant migrate up

GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_webui_live_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
GOLANG_CC_MOBILE_DEV_AUTH=true \
  go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token

GOLANG_CC_WEBUI_LIVE_E2E=1 \
GOLANG_CC_WEBUI_LIVE_API_BASE=http://127.0.0.1:18080 \
GOLANG_CC_WEBUI_LIVE_API_TOKEN=test-token \
GOLANG_CC_WEBUI_LIVE_TENANT=webui-live \
GOLANG_CC_WEBUI_LIVE_USER=webui-live-user \
GOLANG_CC_WEBUI_LIVE_DEVICE=playwright \
GOLANG_CC_WEBUI_LIVE_MODEL=gpt-5.5 \
  npm --prefix web run test:e2e:live
```

结果：MySQL 9.5.0、本地 API Server `127.0.0.1:18080`、真实模型 provider 下，`chromium-live` 通过；数据库可读回 mobile session、user/assistant messages、`mobile.chat.stream.finished` telemetry、trace summary、memory、profile、`CLAUDE.md` 文档、tenant skill 和 user skill override。测试过程中修复了 GORM repository 在 MySQL duplicate-key update 路径下 `LAST_INSERT_ID` 未回填 struct id 导致的 `last insert id is zero`，以及 WebUI seed 重复执行时 profile/document 固定版本冲突的问题。

2026-06-26 真实环境验证：

```bash
mysql -uroot -h127.0.0.1 -e 'DROP DATABASE IF EXISTS golang_cc_webui_full_e2e; CREATE DATABASE golang_cc_webui_full_e2e CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'

GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_webui_full_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
  go run ./cmd/golang-cc tenant migrate up

GOLANG_CC_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_webui_full_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
GOLANG_CC_MOBILE_DEV_AUTH=true \
  go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token

GOLANG_CC_WEBUI_LIVE_E2E=1 \
GOLANG_CC_WEBUI_LIVE_API_BASE=http://127.0.0.1:18080 \
GOLANG_CC_WEBUI_LIVE_API_TOKEN=test-token \
GOLANG_CC_WEBUI_LIVE_TENANT=webui-live \
GOLANG_CC_WEBUI_LIVE_USER=webui-live-user \
GOLANG_CC_WEBUI_LIVE_DEVICE=playwright \
GOLANG_CC_WEBUI_LIVE_MODEL=glm-5.1 \
  npm --prefix web run test:e2e:live
```

结果：MySQL 9.5.0、本地 API Server `127.0.0.1:18080`、本地配置 provider `custom` / model `glm-5.1` 下，`chromium-live` 通过。隔离库直查可见 `tenant_sessions=1`、`tenant_session_messages` 中 user/assistant 各 1 条、`tenant_telemetry_events=184`，包含 `mobile.chat.stream.finished` 与 `mobile.phase.*`；同时有 memory、profile、`CLAUDE.md` document 和 tenant skill 各 1 条。注意 server 启动前必须先执行 `tenant migrate up`，server 不会自动创建空库 schema。

手动真实链路建议先在 Run Context 面板点击 “Seed validation scenario” 图标按钮，确认 status 显示 `Seeded scenario session ...` 后，再发送 scenario prompt。该 seed 只调用现有 tenant/mobile API，不需要额外后端接口。

本地复用 `webui-local` demo 数据时，优先使用固定脚本，避免漏掉 MySQL DSN 或 mobile dev auth：

```bash
scripts/webui-dev.sh
```

Web Agent 启动分为两个明确 profile，避免重启时切换数据库后误以为历史会话丢失：

```bash
# 手工验收：复用 golang_cc_webui_local / yutang / feishu-e2e-user。
scripts/web-agent-restart.sh manual

# 隔离真实 E2E：使用 golang_cc_web_agent_real_e2e / webui-local / webui-local-user。
scripts/web-agent-real-e2e.sh
```

`scripts/web-agent-start.sh` 也支持 `manual` / `e2e` 参数。启动日志会打印实际 profile、数据库、租户和用户；
显式 MySQL DSN 的数据库名与 profile 不一致时会直接拒绝启动。

启动后运行 smoke check：

```bash
scripts/webui-smoke.sh
```

该检查会验证 `/mobile/chat/sessions`、`/tenant/sessions` 和 `/trace/api/sessions/{id}` 都可读；如果 mobile 鉴权模式切错，例如误开了 JWT secret 而浏览器没有匹配 JWT，会直接报错而不是让页面看起来像“没有会话”。

Trace timeline 依赖 MySQL `TIMESTAMP` 和 Go telemetry `occurred_at` 使用同一时区语义。`scripts/webui-dev.sh` 默认 DSN 固定 `loc=UTC` 和 `time_zone='+00:00'`；仓库打开 MySQL 时也会为未显式设置这些参数的 DSN 自动补齐 UTC 默认值，避免 Observability Trace 出现 `+28800000ms` 这类 8 小时时区偏移假耗时。

本地 demo 数据可以直接通过 WebUI 的 seed 按钮生成，也可以使用现有 HTTP API 写入 `webui-local` 租户。推荐保留 `webui-local-user` 为 owner，以便 Observability tab 能动态读取 `/tenant/telemetry` 和 `/trace/api/sessions/{id}`。示例：

```bash
curl -sS -X POST 'http://127.0.0.1:18080/tenant/memories' \
  -H 'Authorization: Bearer test-token' \
  -H 'X-Tenant-Key: webui-local' \
  -H 'X-User-Id: webui-local-user' \
  -H 'X-Device-Id: web-browser' \
  -H 'Content-Type: application/json' \
  -d '{"memory_key":"demo.preference.ui","category":"demo","content":"User wants a polished WebUI with grouped navigation and full mobile lifecycle testing.","importance":9,"source":"manual-demo-seed"}'
```

2026-06-17 本地 demo seed 验证：在 API Server `127.0.0.1:18080`、MySQL 库 `golang_cc_webui_local` 中，通过现有 API 为 `webui-local/webui-local-user` 写入 demo memory、profile、`CLAUDE.md`、tenant skill、skill override、mobile session、tenant messages 和 telemetry；随后通过 `/tenant/messages?session_id=3&limit=10` 和 `/tenant/telemetry?limit=5&search=3` 回读成功。

涉及 CLI server option 或环境变量时补跑：

```bash
go test ./internal/cli -run 'TestServer|TestAuthLoginWithAuthToken' -count=1
```

如果执行全量：

```bash
go test ./... -count=1
```

当前本机如果存在 OAuth token 配置，`internal/cli TestAuthStatus` 可能优先识别 `oauth_token` 而非测试期望的 `api_key`。这属于环境污染，需要隔离 HOME/auth env 后再跑全量。

## 当前状态

截至 P0/P1 收口：

- P0 已完成：dev identity、动态租户/用户选择器、租户用户管理、Test Scenario seed、mobile session 全周期、mobile SSE 消息、停止、regenerate、branch、memory、profile、documents、skills、telemetry、trace 验证。
- P1 已完成：附件 presign/metadata/真实上传 UX、Goal Workbench、Agent/Subagent Cockpit 产品化、生产认证状态与清理、消息状态展示、stream timeout/cancel/error 状态、Chat 会话搜索/状态过滤/上下文条、操作后 evidence 刷新、Chat/Knowledge/Skills/Goals/Observability/Run Context 分区导航、KB Search、Scoped Memory、Memory Review、Lifecycle Validation、Capability Evidence、Trace evidence、桌面/移动视觉回归 smoke、`/webui/` 静态托管、前端 API helper 单测和 validation 单测。
- 已验证：`npm --prefix web run test`、`npm --prefix web run build`、`npm --prefix web run test:e2e`、`GOLANG_CC_WEBUI_LIVE_E2E=1 ... npm --prefix web run test:e2e:live`、`go test ./internal/server -count=1`、`go test ./... -count=1`、`git diff --check`。
- 2026-06-17 P1 硬化验证：新增 stream timeout 单测、mock Playwright Trace evidence 断言、live E2E browser storage 隔离和空库 tenant/user bootstrap；本轮已通过 `npm --prefix web run test`、`npm --prefix web run build`、`npm --prefix web run test:e2e`，并在本地 MySQL 隔离库 `golang_cc_webui_p1_e2e` + API Server `127.0.0.1:18080` 上通过 `npm --prefix web run test:e2e:live`。
- 2026-06-17 UI polish：WebUI 改为 data-dense dashboard 风格，新增一级/二级导航、runtime context strip、控制台化侧边栏、统一按钮/卡片/状态色和 responsive 断点；本轮已通过 `npm --prefix web run test`、`npm --prefix web run build`、`npm --prefix web run test:e2e`。
- 2026-06-17 租户/用户选择器：左下角固定租户和用户展示改为数据库动态下拉框，分别使用 `/tenant/tenants?limit=100` 与 `/tenant/users?limit=100` 读取列表，并在权限不足时 fallback 到当前 identity；mock Playwright 覆盖租户/用户切换后 welcome runtime 更新。
- 2026-06-18 P0/P1 闭环：Run Context 新增租户用户管理和 demo seed 用户准备；Chat 新增会话搜索、状态过滤、tenant/user/model/session 上下文条和操作后 evidence follow-up refresh。
- 2026-06-19 UI polish：继续收口 WebUI 工作台视觉层级，弱化侧栏与主内容之间的硬分割线，重做 Welcome 居中工作台状态，统一面板、表单、按钮、列表、空态和 Trace/Telemetry 证据块的半径、间距、字号和边框；Knowledge、Skills、Run Context 表单补充明确 label，长运行上下文和 metric 文本保留 hover title，移动端 Welcome 状态 pill 避免遮挡主标识；设置面板改为侧栏内文档流展开，不再用浮层覆盖一级导航或右侧主内容。该轮只调整前端视觉和文案，不改变 API、TUI、权限、快捷键或 slash command 行为。
- 2026-06-18 Identity recovery：租户/用户列表错误不再只显示“暂不可用”，会展示具体 API 错误并支持一键恢复 `webui-local/webui-local-user`，同时清理 `webui-alt` 等 mock identity 残留。
- 2026-06-18 Observability 链路修复：真实 API 验证 mobile chat 会话 `#5` 可回读 messages、`mobile.chat.stream.finished`、`model.request.finished` 和 trace events；前端 Telemetry tab 改为按当前 session id 搜索，Trace evidence 增加 trace ids 与 telemetry events 显示，并修复 live E2E 进入 Run Context/欢迎页状态卡后的操作路径。
- 2026-06-18 Settings drawer：左下角租户/用户与语言切换收纳进齿轮设置二级抽屉，保留身份错误详情和一键恢复能力。
- 2026-06-18 Observability session selector：Trace/Telemetry 面板新增 session selector，可在观测页内直接切换 session 并刷新 trace、messages、session-scoped telemetry 和 validation evidence。
- 2026-06-18 Observability token 修复：mobile stream 完成时把 `input_tokens`、`output_tokens` 和 prompt-cache token 同步写入 assistant message 与 `mobile.chat.stream.finished` telemetry；Trace summary 会从 tenant message、telemetry 顶层字段和 telemetry properties 归一读取 token，并对同一次 assistant/model/mobile 响应按 trace 去重，避免 Observability token 一直为 0 或重复累加。
- 2026-06-18 本地启动护栏：新增 `scripts/webui-dev.sh` 固定 MySQL DSN 与 mobile dev auth 启动方式，新增 `scripts/webui-smoke.sh` 验证 mobile/tenant/trace 三条链路；server 启动时打印 tenant storage 与 mobile auth 模式，Chat 会话列表遇到 mobile 鉴权错误时展示明确原因和 dev-auth 修复提示。
- 2026-06-18 Trace 时序链路图：Trace 面板新增 Evidence overview / Sequence timeline 子 tab；Sequence timeline 支持按 session 下的 trace/request id 选择一次请求，并以 waterfall 形式按时间展示 LLM、tool、skill、mobile/query/api/message 的顺序和耗时。
- 2026-06-18 Trace header polish：Trace 面板的 session selector 移入 “Trace 会话” 表头右侧，主内容区优先留给 summary、evidence 和 sequence timeline；移动端自动折叠为上下布局。
- 2026-06-18 Trace UI polish：在不改变 Trace 数据流和交互行为的前提下，优化 summary metrics、Evidence overview 分组、二级 tabs、trace/request 当前选择提示和 Sequence timeline waterfall 样式；步骤类型、状态、相对开始时间与 duration 更适合快速扫描完整请求链路耗时。
- 2026-06-22 Prompt context visual testing：Knowledge 区新增 KB Search、Scoped Memory、AutoMem 三个面板，可直接写入/检索 tenant KB，管理 team/managed memory，审批 AutoMem 候选，并结合 Trace/Telemetry 查看 `query.prompt_context` manifest。
- 2026-06-22 Memory Review：`Knowledge -> AutoMem` 升级为 `Knowledge -> Memory Review`，统一展示显式 remember 与 AutoMem pending candidate，支持 approve/reject/archive，并显示 risk status、source session/message/trace；增强项新增 candidate type、risk status、source session 过滤，详情面板可查看完整 metadata 和来源消息；pending/marker 不进入 prompt context，approve 后才转为 active memory。
- 2026-06-18 Trace summary density：Trace 顶部 turns/messages/tools/tokens/cache/skills 六个 summary metrics 在桌面端压缩为同一行展示，长 cache/skills 值使用省略号保持布局稳定，中小屏继续折行为 3 列或 1 列。
- 2026-06-18 Trace timezone fix：MySQL tenant storage 默认补齐 `parseTime=true`、`loc=UTC` 和 `time_zone='+00:00'`，`scripts/webui-dev.sh` 同步固定 UTC DSN，避免 message timestamp 与 telemetry occurred_at 混用时区导致 Sequence timeline 出现 `+28800000ms` 假耗时。
- 2026-06-18 Trace phase breakdown：Mobile chat stream 主链路新增 `mobile.phase.*.finished` telemetry，覆盖 request bind、session load、message persist、SSE start/stop 和 query run；Sequence timeline 会把 phase 展示为 `api.request` / `mobile.chat.stream` 内部阶段，并显示内部阶段合计与未解释耗时。
- 2026-06-18 Trace phase persistence：Mobile dev-auth 请求在解析 `X-Tenant-Key` / `X-User-Id` 后会重新绑定 tenant telemetry recorder，保证 WebUI 本地模式下 `mobile.phase.*` 事件落入 MySQL 并可被 Trace API 查询；本地实测 session #10 返回 10 个 phase，耗时主要集中在 `query.run`。
- 2026-06-18 Model stream phase breakdown：`s.client.StreamMessages(...)` 内部新增 `model.phase.*.finished` telemetry，覆盖 request build、stream create、first event、first delta 和 stream read；Sequence timeline 会把这些 phase 展示在 `model.request` 下，用于区分 provider 请求建立、首事件等待、首 token/首 delta 等待和完整流读取耗时。
- 2026-06-19 Trace P1 timeline UX：Sequence timeline 增加耗时归因 summary、stream create / first delta / stream read 占比、阶段 tooltip、层级缩进引导线和父节点展开/折叠；本地实测 session #11 可见 bottleneck、stream 阶段和折叠控制。
- 2026-06-19 Stream create HTTP breakdown：`stream.create` 继续拆分 HTTP 子阶段 telemetry，覆盖 get connection、DNS、TCP connect、TLS、request send、write request、wait first response byte、SDK stream ready 和 HTTP round trip；Sequence timeline 可用 `http wait first response byte` 判断 provider 远端排队、路由、prefill 或首 token 前处理是否是主要耗时。
- 2026-06-19 Trace slow-path UI polish：仅调整前端展示，Sequence timeline 增加 Slow path 诊断卡，突出 remote first byte / DNS / stream read 的耗时对比，并强化 `stream.create`、HTTP 子阶段和 bottleneck 行的层级缩进与颜色提示。
- 2026-06-18 Trace summary tooltip：Trace summary metric 值进一步减小字号，并为 metric 增加原生 hover title，cache/skills 等长值被截断时可通过鼠标悬浮查看完整内容。
- 2026-06-18 Shell UI polish：弱化 sidebar 与 main body 之间的硬分隔线和常驻 resize 线；Logo active 状态不再显示卡片框；一级导航改为轻量 indicator active；Settings 抽屉改为与底部入口衔接的内联展开态，减少重复卡片和截断噪音。
- 2026-06-19 Main workspace UI polish：在不改变 WebUI 功能和 API 数据流的前提下，继续收敛主工作区视觉层级；二级 tabs、panel header、Chat session list、context strip、message bubbles、attachment composer 和输入区改为更克制的诊断工作台风格。Trace summary 六项指标改为一体化仪表带，Evidence overview 降低卡片拼贴感，Sequence timeline 收敛类型色、耗时条和移动端换行/裁切，保持 cache/skills 长值可通过 hover 查看完整内容。
- 2026-06-19 Telemetry UI polish：Telemetry 面板改为专业事件诊断列表；session selector 收纳进表头，顶部展示 telemetry events、trace ids、total duration、tokens 和 ok/warn/fail 状态汇总，事件行按 category/status/duration/trace/session/model/tool/tokens 分层展示，properties/error 作为低权重详情，桌面和移动端都保持可扫描布局。
- 2026-06-26 WebUI production pass：完成 Goals 一级工作台、ChatLab 文件选择 + SHA-256 + presign + HTTP(S) PUT 上传 UX、Settings 认证状态/清理、Agent cockpit 搜索/筛选/自动刷新/详情面板、Playwright 桌面/移动 visual-regression smoke，并用 Browser/IAB 验证桌面与移动渲染无 console error；真实 MySQL + local provider `glm-5.1` live E2E 通过。

## 提交规则

- 每完成一个可验证功能块，先更新本文档和相关专题文档。
- 每个功能块运行对应测试。
- 每个功能块单独 commit，并 push 到当前分支。
- 不提交 `web/node_modules/`、`web/dist/`、`.claude/`、`gocc1`。
