# WebUI 2.0 三项真实故障修复

日期：2026-09-07

## 目标与已确认原因

1. 会话 004 的 Run 70/71 引用会话 002：92 个任务事件（其中 88 个 text_delta）全部成为受保护证据，6546 tokens 超过单包 2048 限额。PrepareRun 后 Attach 失败未结束 ready 任务，前端丢失 budget_exceeded 错误语义。
2. 会话 006 的 Run 72 调用 AskUserQuestion，但 server 没有绑定用户问题回调，工具返回 IsError，页面只显示普通工具卡，无法回答并继续原 Run。
3. Run 73 生图成功，后续将受鉴权的相对媒体 URL 作为模型图片输入导致外部模型返回 image download failed。浏览器展示与放大正常。

## 架构与影响评估

沿现有 RT-SESSION-CONTROL / RT-EVIDENCE 提取有用的会话记录，避免将流式传输片段当成独立上下文；保留证据授权、哈希校验和原预算约束。失败的准备任务必须有终态与失败事件。

用户问题沿 RT-WIRING / RT-TOOLS -> RT-PERSIST(task events) -> RT-OUTPUT(现有聚合 SSE) -> RT-BOUNDARY(授权回答接口) -> 原工具回调继续。复用已有等待权限模式，不增加 SSE 连接；刷新和切换会话可以从事件重建问题。等待的原 Run 与现有执行进程同寿命，不承诺服务重启后恢复执行栈。

图片沿 RT-TOOLS / RT-PERSIST(media_assets + blob) -> RT-WIRING / RT-PROVIDER。浏览器资源地址和模型可读取的图片数据分开，维持 tenant/user/session 授权，图片内容不得写入事件、审计或诊断日志。异步 channel 生图合同保持兼容。

兼容性决策：GLM-5.2 官方模型规格为文字输入/文字输出。默认生图只向对话模型返回产物信息并注明未做视觉检查，不要求模型具备看图能力；`imageGeneration.previewInContext=true` 才把经过授权与完整性校验的图片数据作为下一轮视觉输入，适用于用户明确选择的视觉模型。原有用户发送图片入口不变，不根据模型名字做分支，也不增加自动重试或改换用户选定模型。

现有总图中的工具、事件输出、持久化与恢复边仍覆盖本次实现；不新增 RT 节点或跨边路由，registry 增加问题接口的稳定代码锚点，文档展开既有边的语义，图件保持不变。

最高 Blast radius：B5_SHARED_STATE（任务收尾、回答写操作、图片授权读取）；问题事件是向后兼容的协议扩展，覆盖前后端消费者。正收益为三个完整用户工作流可闭环；潜在成本是等待回答占用现有 Run、模型图片编码的内存/请求大小、上下文选择策略的摘要信息损失。无需新增预算 gate、数据库 schema 或 SSE 连接。

## 任务与验证计划

| 任务 | 实现边界 | 必须验证 |
| --- | --- | --- |
| 1 上下文及失败收尾 | handoff source、managed dispatcher、错误映射 | 大量 delta 的原始会话可压入预算；有效请求和回复保留；越权和旧哈希拒绝；Attach/Launch 失败终态；可继续发送 |
| 2 图片后续请求 | imagegen 工具与授权媒体读取 | Generate/Edit 成功；不发送本地 URL；越权和损坏资源拒绝；不破坏异步渠道；真实生图、放大、下载、继续对话 |
| 3 用户问题交互 | server 回调/回答 API、事件映射、UI | 可见问题与选项/自由输入；原 Run 继续；重复回答、空答案、越权、取消；切换会话和刷新恢复；旧工具记录可读 |
| 4 集成验收 | 真实旧库 webui-local/webui-local-user、现有重启脚本 | 浏览器拖入真实来源并发送、问题选择续跑、生图最终完成；真实 SSE 和数据库 readback；Go、Web 测试和拓扑校验 |

共用接口检查：任务 1 和任务 3 都消费 Task status/events，所有新增事件需经既有 SSE 白名单和前端 reducer；任务 2 与任务 3 共用 query 装配但实现文件按所有权协调。三项均不修改全站 Settings/Profile 页面。先补失败回归再实现；各独立块测试通过后提交并 push，最后统一运行全套检查并复核。

回滚：回退本轮应用提交，保留已有任务/媒体/事件数据；新事件可由旧客户端忽略，不执行数据库清理，不修改用户 settings。观测复用 trace、任务状态、事件顺序和现有日志，记录安全错误码与耗时，不记录正文或凭据。

## 进度

- 三项修复及接口、前端、数据库回归完成；未扩展全站 Settings/Profile 页面。
- Handoff 在生成包身份前按 canonical package 预算选择证据；覆盖长会话引用、32 条消息、40 条工具记录和原始大量 delta 场景。Prepare 后 identity/Attach/Launch 失败通过带独立两秒 deadline 的 ready CAS 收尾；Create/Send 幂等重试保留准备阶段错误。
- 新旧 WebUI 共用问题卡片和回答接口，支持选项/自由输入、刷新/切换恢复、取消/过期、409 关闭及网络错误重试。新事件关联 tool_id，旧无 tool_id 事件有兼容投影，历史失败工具仍可读。手机实测发现全局 input 样式撑大单选框，已限定控件尺寸并复查布局。
- 图片默认元数据上下文和显式视觉预览均有测试；旧生图后续失败会明确说明图片仍可查看和下载。准备阶段只有错误码的历史事件使用已有原因文案，避免仅显示 failed。

## 真实验收记录

环境：2026-09-07，`scripts/web-agent-restart.sh e2e`，本地服务 `18087`，复用 `golang_cc_web_agent_real_e2e` 的 `webui-local / webui-local-user` 身份。桌面 Edge 真实点击、拖拽、回车发送与 390x844 视口验收；所有模型调用均使用真实 Provider。

| 链路 | 实际结果 |
| --- | --- |
| 会话 002 拖入新会话 26 后发送 | Run 75 写入 handoff，13 秒完成；回复正确概括来源最后的看图请求及结论 |
| 问题显示、刷新、切换、回答 | Run 76 显示颜色选项；刷新并切换到生图会话后仍可回答；选择蓝色后原 Run 回复“验收颜色：蓝色”并 completed |
| 并行生图会话 27 | Run 77 在 Run 76 等待回答时生成图片并 completed，21 秒完成；Run 78 后续普通对话 2 秒 completed |
| 图片放大与下载 | 浏览器显示 1254x1254 PNG；下载 795972 bytes，SHA-256 与数据库一致，产物 img-6a1a2234c77ae659335f8db8 |
| 等待问题时停止 | Run 79 的 question resolved=cancelled、task=cancelled；页面关闭回答控件并恢复普通输入，无重复问题卡片 |
| 准备失败及重试 | Run 80 引用不存在的来源，立即 failed 并写入 session_control_prelaunch/not_found；相同幂等键重试仍 404/not_found，仅一个任务。页面显示“找不到该会话”；随后从浏览器发起 Run 81，3 秒内 completed，确认失败后可继续对话。验收 Run 无遗留 ready/running |
| 真实 HTTP 与 SSE | 两会话聚合 SSE 回放含 handoff、text/thinking delta、question request/resolved、image_artifact 和 completed；同答案重试 200，已取消回答 409，未鉴权 401，其他用户读取图片/回答 404 |
| 手机布局 | 390x844 问题卡片、图片及输入区无横向溢出；单选按钮固定 16px，中文选项正常横排；验收后恢复桌面视口 |

测试：`go test ./... -count=1` 通过；最后 Create 重试修复后重新通过 sessioncontrol/server 包测试；后端问题接口 race 测试通过。前端最终 53 文件/471 测试、typecheck 和生产 build 通过；Swagger/API types 重新生成，拓扑检查及 `git diff --check` 通过，channel-worker 脚本回归通过。全站 lint 的既有 `ProfileConversationsDialog.tsx:84` noUselessFragments 错误不属于本轮，未修改该文件。

边界：等待回答最多十分钟且受原 Run 总超时限制，不跨服务重启恢复原执行栈；纯文字对话模型不会因生图而自动具备视觉能力，只有 `previewInContext=true` 且选择支持图片输入的模型才注入视觉预览。历史 Run 的终态保持真实记录，不改写既有失败数据。
