# WebUI 2.0 对话体验补齐

用户已批准补齐本轮列出的全部 1.0 对话差距。沿用当前分支；不新增工作树，不修改用户密钥和无关改动。

## 架构与影响

复用 Task event、Session Control、pending-input API 和 1.0 现有渲染组件。SSE 层保存完整事实，展示层独立控制动画；输入、消息渲染、队列管理各自独立组件，App 负责会话身份和操作装配。

Topology impact: updated。RT-OUTPUT 新增已有事件/队列/图片/命令服务的 WebUI 2.0 消费者，经 RT-BOUNDARY 调用 RT-SESSION-CONTROL 和 RT-PERSIST。界面行为 B2_MODE，已有写接口的消费者和提交推送最高 B5_SHARED_STATE。不改模型循环、system prompt、默认权限或数据库 schema。

正收益：逐字输出与真实流状态一致，输入反馈不再由 running 推测队列，跨会话引用通过既有授权装配。风险：动画重复、输入法误发送、拖拽误切换、队列缓存跨身份污染、重新生成重复提交。验证覆盖历史/重连/切换、IME、剪贴板、真实鼠标拖拽、队列状态和错误路径。

## 实施分块

1. 消息：打字机、复制/重新生成、思考模式与状态、结构化工具、Provider/model/耗时/token、图片附件与生成图片、更多历史。
2. 输入：Enter/Shift+Enter/IME、粘贴和拖入图片、斜杠命令、会话标题或拖拽柄拖入与输入框高亮。
3. 队列：真实数量达到 2 才展开提示，单条通过紧凑入口管理；编辑、删除、上移、重试及已有队列控制。当前执行不计入排队数。
4. 装配与验收：会话级运行标识、SSE 连接状态、重新生成路由、浏览器/接口验证，构建、回归、提交和 push readback。

运行中允许拖入上下文草稿，但带上下文的消息等待当前 Run 结束后手动发送；复用后端现有拒绝边界，不丢弃 source_refs，不增加自动发送或后台队列 schema。

## 验收中发现的补修

- 暂停队列拒绝新增输入属于既有契约，Session Control 补充将 `ErrQueueDisabled` 映射为 `invalid_state` / HTTP 409，保留幂等冲突原有语义。
- 重新生成沿用 1.0 的后续 Run 语义，保留旧回复；恢复原始文字、图片引用和该 Run 的来源会话引用。来源重新鉴权并捕获当前快照，不声称逐字复现过去的上下文快照。损坏 handoff 明确失败，不静默丢弃来源。
- 真实 Web 源会话仅有 Task events 时，旧 Handoff 只含标题、状态和事件哈希，不能回答来源问答。限定于拥有的主 Web 任务，从既有 message/completed 事件提取受预算约束的请求与回复摘要；继续标记为 context，不能作为工具执行或权限授权。沿用原 schema、授权和聚合预算，非 Web 源行为保持原样。
- 请求/完成回复候选分别限制为 512 bytes，保留 UTF-8 字符边界。新证据哈希覆盖所选正文全文；旧 reported 消息/完成证据保留元数据哈希兼容和原有较弱保证，旧包不改写，verified 工具证据不走回退。
- 冷启动多页 SSE 先于 HTTP 历史完成时，不标记 liveRevision；只有完整历史加载完成后的新事件允许打字机动画。
- HTTP 历史与 SSE 的缓存合并按任务 ID、事件 ID、时间和终态进展保留最新摘要，防止迟到快照把已完成回复恢复成运行中；SSE 终态显式清除旧 activeRunID，完整历史和动画进度继续保留。
- 长历史 CSS Grid 的自动行收缩会裁掉工具 details，造成看得到摘要却点不到；消息轨道按内容高度布局，滚动控制绑定实际消息滚动容器。

## 验证与回滚

各独立功能块先运行相关 Vitest，再全量前端测试和 build；涉及 runtime/API 时运行全量 Go、拓扑检查和必要 Swagger 生成。浏览器覆盖桌面/手机、真实 SSE 和 A/B 隔离；真实数据库验证队列及上下文 readback。使用本地 18087 e2e 服务，避免影响其他 channel workers。所有模型请求使用明确测试消息，不授予额外工具权限。

动画和队列不增加模型 turn 或 tool schema；用户引用来源会话时，既有上下文包增加有预算上限的请求/回复候选。动画只在可见回复运行；当前会话队列每 5 秒读取，展开才读取设置，事件变更另以 300ms 合并刷新；历史每次展示 60 条。关注连接重建与额外查询次数，不建设指标平台。回滚应用提交即可，保留已有会话、队列、Task events。

## 进度

- 消息展示、输入交互、队列管理：已实现并完成分块测试。
- 真实浏览器：逐步打字机、完整复制、两条排队编辑/排序/删除、会话标题拖入通过；上下文 Run 33 和重新生成 Run 34 均正确回答来源标记，刷新恢复正常。
- 浏览器受控数据：长历史、思考模式、工具输入/输出展开、文字/图片/文字顺序、图片预览通过；桌面与手机视口无横向溢出。生成图片展示使用受控图片事件验证，未额外请求付费图片生成。
- 输入真机浏览器：Shift+Enter、真实 slash API 查询和键盘选择、原生剪贴板 PNG 粘贴与移除通过；IME 使用组件回归，未宣称 Safari 或实体手机输入法验收。
- MySQL `golang_cc_web_agent_real_e2e`：Session 13 的 Run 33/34 均含各一条 message、session_handoff、completed，来源引用与正确答案落库；Run 31 的两条测试队列项均 cancelled。Run 35 的暂停队列请求通过 curl 验证 `409 invalid_state`，拒绝输入未落库，队列恢复启用。
- 最终自动验证：`go test ./... -count=1`、396 项前端测试（48 个文件）、TypeScript/生产 build、WebUI 2.0 lint 错误检查、拓扑检查和 `git diff --check` 通过；Swagger 已重新生成，无 wire schema 差异。
- 全站 lint 仍有 3 个既有错误，位于未修改的 `ProfileConversationsDialog.tsx` 和 `components/agent/MessageList.tsx`；生产构建保留原有大 chunk 提示，不影响启动。
- 验收完成，服务已重启到最新后端并加载最新前端构建。提交和远端 readback 记录以本轮 Git 提交为准。

持久浏览器回归为 `web/e2e/message-experience.webui-v2.spec.ts`（桌面与手机通过）。本机真实服务脚本、截图、结构化结果在 `/Users/example/.golang-cc/webui-repair/`，包括 `parity-context-result.json`、`queue-disabled-result.json`、`composer-smoke-result.json`；不写入密钥。
