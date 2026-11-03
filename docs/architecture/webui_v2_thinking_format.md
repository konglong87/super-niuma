# WebUI 2.0 思考内容排版

## 方案与影响

Topology impact: none
Blast radius: B2_MODE
Topology reason: 沿现有 RT-OUTPUT / RT-BOUNDARY / RT-PERSIST 的 Task 事件保存、SSE/history 和 Web renderer 链路修复内容保真及排版；没有新增节点、跨层边、协议字段或持久化 schema。

Provider delta 是原文片段，空格、制表符和换行均有语义。Task sink 不再逐片段 TrimSpace，事件序列化保留非空 content，包括纯空白。其他可选元数据仍过滤空白。旧版和新版共用这项修复，鉴权、游标、事件顺序、重连去重保持原契约。

新版 ThinkingMessage 复用旧版 MarkdownLite，隔离思考正文的视觉规则：13px / 1.75、灰色背景、12px 圆角、紧凑阶段/状态/耗时信息。摘要默认折叠，打开后预览约八行，超长才提供展开全部；完整模式无截断，隐藏模式保留原行为。展开状态独立于 SSE 内容更新，代码保留缩进并横向滚动。

正收益：消除新消息的单词粘连，改善长思考阅读。潜在负作用：原先被丢弃的纯空白片段现在会增加对应事件写入及读取量；不增加 SSE 连接、模型 token、turn 或工具调用。正文只在折叠打开后挂载，流式更新复用既有 Markdown 缓存。

## 验证与回滚

- 后端逐片段测试覆盖中英文、空格、制表符、段落和代码围栏，核对保存值与输出一致。
- 前端覆盖历史/实时/重连重叠精确拼接、Markdown、摘要/完整/隐藏、展开状态、阶段时间；真实浏览器验证桌面/手机、明暗主题及新消息刷新回读。
- 运行完整 Go / Web 测试、类型检查、构建、diff 和 topology 检查。
- 无新增日志正文，无数据库迁移；可回退组件及 sink 改动，已保存事件不需变更。

限制：之前已落库且被删除的空格无法可靠还原，不猜测分词、不重写历史。Markdown 只展示模型实际输出的结构，不自动翻译或改写内容。

## 2026-09-07 验收记录

- `go test ./... -count=1` 通过；Web 57 个文件、526 项测试通过，typecheck/build 和本次 TS 文件 Biome 检查通过。Swagger 重新生成无 diff，topology 检查通过。
- 使用 `scripts/web-agent-restart.sh e2e` 重启 Web 服务，沿用 `golang_cc_web_agent_real_e2e / webui-local / webui-local-user`；两个既有 channel screen worker 保持运行。
- 真实浏览器发送验收消息：Run 94（gpt-5.6-sol）、95（glm-5.1）完成；95 保存 29 个 thinking_delta，其中 24 个带前导空白，另有 232 个 text_delta。MySQL、鉴权历史 API、浏览器内容三者核对一致；游标后的 SSE 正常返回剩余 65 个事件。
- 现有验收会话的长思考实测预览 182px、展开 313px，展开/收起/关闭重开状态正确。新思考刷新后仍完整保留英文单词间空格，摘要初始折叠。
- 桌面与 390x844 手机、明暗主题已截图检查，正文 13px / 22.75px 行高，12px 圆角，页面无横向溢出。真实新思考较短；长 Markdown 与连续更新中的展开状态另由组件和 reducer 测试覆盖。
- 供应商边界：sensenova-glm-5.2 首次验收返回配额超限 429，手动停止 Run 93 后改用另外两个已配置供应商完成验收；未更改全局 settings.json。
