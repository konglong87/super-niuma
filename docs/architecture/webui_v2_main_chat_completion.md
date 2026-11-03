# WebUI 2.0 主对话完整迁移

## 已确认目标

保留 1.0 主对话能力，采用统一圆角输入框和有序消息展示，增加会话拖拽上下文。全站 Settings/Profile 等页面不在本轮范围。用户已批准实现全部下列内容，沿当前 main 开发，不新建 worktree，测试通过分块提交并 push。

## 架构

继续使用 Session Control 和多会话单连接 SSE。运行配置作为一次发送的整体快照传递，backend 校验并持久化，Session 保存后续默认值，Run 保存实际值；已有请求省略配置时兼容默认行为。新字段使用 provider/model/permission_mode/effort/prompt_mode，保持配置的同一所有权边界。运行或队列非空期间不允许改变运行配置。

前端复用既有 Provider/model API、PopoverSelect、图像服务、pending-input API、草稿存储和消息工具函数。事件解析、运行数据、输入交互和页面导航分层，不复制整个 WebAgentPage。只有真实事件或任务 metadata 可以产生统计值，无数据以未知显示。

RT-OUTPUT 消费 RT-BOUNDARY、RT-SESSION-CONTROL、RT-PERSIST 与 RT-EVIDENCE；路由校验复用 RT-WIRING，工具/权限保持既有 RT-PRETOOL 与 RT-TOOLS。视觉 B2_MODE，API 协议和会话配置写入最高 B5_SHARED_STATE。检查 tenant/user 授权、幂等、busy CAS、run 快照与 session 默认值的一致性；不增加默认权限、不改共享 system prompt、不另造模型调用链。

## 功能清单与进度

| 分块 | 范围 | 状态 |
| --- | --- | --- |
| A 运行配置 | Create/Send 配置合同、下一轮切换、快照与默认值、busy 队列约束、Swagger/API 测试 | 完成，真实 Run 与 Session 配置 readback 通过 |
| B 输入框 | 单层圆角、内部加号/权限/模型/强度/上下文/缓存、Enter/IME、Slash 浮层、图片、持久草稿、队列清空隐藏与旁路 | 完成；旁路接管、图片显示、幂等与后续历史恢复通过，旁路读图有下述 Provider 限制 |
| C 消息 | 时间/实时耗时、思考阶段与偏好、工具顺序、compact、下一步建议、生成图片、Inspector 文件/子任务/Trace/Usage/权限 | 完成，真实图片渲染/下载/刷新与桌面/手机检查通过 |
| D 导航与集成 | 工作区分组/校验、Chat/Code、新建/当前运行配置、命令面板、实际生图链路 | 完成，真实模型对话、生图与图片识别通过 |
| E 验收 | 单元/接口/DB/真实多会话与图像/桌面手机/构建/拓扑/提交与远端 readback | 最终全仓 Go、445 前端单测、29 浏览器用例通过；真实链路与供应商限制记录如下 |

共享接口检查：A 产出的 snake_case wire 字段由 D 适配成 camelCase，B 只接收结构化运行控件值；C 消费主 Run events 和任务 metadata，D 装配身份与任务，不新增按消息的网络订阅；B 队列回报 busy 状态给 D，A 仍负责后端拒绝并发变化。输入框移除来源选择器入口但保留 draggable title/handle、标签和清除。运行中含 source_refs 的草稿继续等待空闲后手动发送。

队列 0 条等待时不渲染入口，1+ 条显示数量，消费完自动关闭；失败保留错误/重试渠道，空队列暂停开关从加号菜单可恢复。Slash 和菜单脱离输入框正常文档流，不改变输入区高度；图片发送和上下文标签不互相吞掉 drop 事件。UI 使用一个圆角 shell 的 focus-within，清除 textarea 继承的全局阴影；工具项紧凑圆角，消息按 durable event 顺序更新。

## 验证与成本

配置测试覆盖新建/继续/切换/busy/幂等/越权/未知配置/历史实际值；真实 MySQL readback。UI 覆盖 IME、Enter、队列 0/1/2/消费、粘贴/拖入图片、跨会话拖拽、草稿刷新、Slash 键盘、工具/思考/图像穿插、历史/重连、不串会话。使用 18087 的 e2e profile，即 golang_cc_web_agent_real_e2e / webui-local / webui-local-user，保留原数据。

计时使用前端单一可见视图时钟，不新增每秒 HTTP 查询。配置目录缓存、事件聚合复用已有请求；队列只查询当前会话，背景会话沿单连接 SSE。真实图片生成只执行明确验收请求并验证下载/鉴权/恢复，不因轮询重复生成。回滚应用提交即可，不删除既有会话/消息/图片记录。

最终需 go test ./... -count=1、前端测试/typecheck/build、相关 Playwright、Swagger 生成、runtime-topology-check 和 git diff --check。全站既有未触及 lint 问题单独说明，已触及代码不新增 lint 错误。

## 集成发现

- 实际权限/强度现在传入 QueryRequest 与 CLI runtime options。请求声明显式模型，避免 named Provider 的默认 model 意外覆盖用户选择。
- SessionControl 私有查询直接投影配置 metadata，避免列表逐会话补查；MySQL JSON 重试按结构比较，保留数字精度，不依赖键顺序或空格。
- Composer runtime value 在点击发送时进入不可变请求和幂等指纹。重试保留原请求，修改配置则更换请求键。
- 元数据查询以身份、会话、Run、生命周期状态为缓存键，Workspace/Inspector 共用；文字 delta 和计时更新不触发查询。上下文、缓存和用量只显示真实观测值。
- 原 Session Control 不允许内联图片，而本地 presign 没有真实对象存储时只有占位上传 URL。UI 预览不能证明模型收到了图片。本轮补既有 MediaAsset/BlobStore 的会话附件归档与执行前读取，SSE、审计与消息仍仅保存 metadata。
- Diff 展示使用现有工具输出中的实际 diff；只有行数/文件事件时不编造正文。未保存的历史配置和用量显示未知。
- 草稿恢复文本和来源引用，浏览器 File 对象不写 localStorage；发送成功的图片经附件存储恢复。
- 配置排队请求在 queue.Add 成功、audit 未完成的极少数崩溃窗口保持 fail closed，保留队列项，客户端可读回确认。详情见 API 文档。
- 消息图片按 durable event 顺序投影：独立 `image_artifact` 与 `GenerateImage`/`EditImage` 的真实 JSON 工具结果使用 task/asset 去重；工具结果更新原工具卡，图片出现在结果事件位置。不存在图片事件且工具结果也没有 artifact 时，不推断生成成功。
- 新会话附件采用 `attachment_id=sc-image-<SHA256>` 与 `/tenant/media/assets/:id`，用户消息复用鉴权 Blob 预览/下载。更早版本仅保存附件名称且未归档 bytes 的内联图片仍不能恢复图像；不从名称或 hash 伪造 URL。
- 旁路会话先克隆候选图片到新会话的独立资源，首次 Send 原子接管仅限未启动的旁路任务。候选与当前输入保留为两条 durable message，后续 Run 的历史包含两条输入。
- 历史任务详情先验证 Session 归属，再按 tenant/user/session 过滤后应用 limit；保留旧 metadata-only 会话链接及 legacy 会话行为。

## 2026-09-06 验收记录

- 最终 `go test ./... -count=1` 全部通过；图片并发归档额外 `-race` 通过。前端 445 项单测、typecheck/build 通过；Playwright 三个视口合计 29 项通过，7 项按视口设计跳过。
- `swag init` 与前端 API types 已重新生成；B5 updated 拓扑检查与 `git diff --check` 通过。全站 lint 剩余一个既有未触及错误，位于 `ProfileConversationsDialog.tsx:84`，不在本轮范围。
- 真实服务采用 `scripts/web-agent-restart.sh e2e`，保留 `golang_cc_web_agent_real_e2e` 中 `webui-local / webui-local-user` 的原有 14 个会话。验证新增 Session 15/16，Run 46/47/48 均完成；双会话并行无串流、队列消费后入口隐藏、busy 配置变更返回 409，A 的 medium 与 B 的 low 经 MySQL 读回一致。
- 浏览器观察到 72 次不同文字长度，确认真实 SSE 增量展示；文本与拖拽来源草稿刷新恢复；旧 Session 8 在 `limit=1&event_limit=1` 时仍返回自身 Task 20。
- Run 49 在主对话真实生成一张图片，Run 50 粘贴该图片后正确识别主体和颜色。图片 1,526,364 bytes，放大、下载、刷新后像素读取及手机无横向溢出通过；未鉴权资源读取返回 401，其他用户被拒绝。
- MySQL readback：输入图资源属于 Session 16，生成图属于 Session 15，输入图真实 SHA 存于 Original，tenant 级可选去重 SHA 留 NULL；上述消息事件没有 inline_data，5 个 Run 均有 completed 且无 failed。
- 本机详细验收记录与截图保留在 `~/.golang-cc/webui-repair/main-chat-completion-result.json` 及同目录 `completion-*.png`。测试脚本没有新增运行时后台轮询或数据库统计服务。
- 旁路 Session 19 的 Task 57 首次 Send 接管原 ready task，重放返回相同 task；保留候选与跟进两条 message，图片复制为新会话资源且浏览器像素正常。Task 58 实际回复同时包含候选与跟进两个标记，验证后续历史没有丢失。验证结束取消本轮测试候选并恢复队列，未删除原用户数据。
- 旁路读图验收未通过语义断言：Task 53/55/57 请求主 Provider 时发生超时，真实 trace 显示两次 primary attempt 后转入 `glm-5.1` fallback，备用模型回复无法看图。小尺寸副本复测也发生相同降级。输入附件已归档、clone、hydrate，query context 记录 attachments=1；相关边界与协议转换测试通过。本轮保留原 fallback 配置，不将此项声称为图片识别通过。普通粘贴识图 Task 50 已通过，图片生成 Task 49 已通过；供应商可用性及 fallback 视觉能力仍影响实际结果。

已知边界：未发送 File 草稿不跨刷新保存；从未归档 bytes 的旧图片无法补回；没有真实 diff/usage 的历史记录保持未知。归档成功但后续 Send 拒绝时可能留下会话内未引用图片，当前不新增自动清理。运行配置显示用户选择的路由，实际发生的 fallback 可从 Trace 核对；当前备用模型未具备图片识别能力。全站 Settings/Profile 配置页面另轮讨论。

## 2026-09-07 输入框发送与停止合并

- 输入框保留一个固定 32×32 的主操作按钮：空闲或运行结束显示发送箭头；running、queued、waiting_input、waiting_permission 显示停止方块，单击直接调用既有停止接口。只读会话不提供停止操作。
- Enter 仍可在运行中发送排队消息；Shift+Enter 换行、中文输入法、附件取消上传与来源草稿等待空闲的约束保留。代价是运行中鼠标主按钮只负责停止，排队发送使用 Enter；停止操作不再二次确认。
- Topology impact: none。RT-OUTPUT 继续消费 RT-SESSION-CONTROL 的既有状态与回调，Blast radius: B1_SCENARIO；没有新增接口、存储、SSE 订阅、数据库查询、模型调用或 gate。回滚本次应用提交即可恢复双按钮。
- 验证：475 项前端测试、前端构建、全仓 `go test ./... -count=1` 通过。测试覆盖四种活动状态切换、同一 DOM 按钮复用、单击停止、Enter/IME/换行和忙碌上下文草稿。
- 真实浏览器在旧身份 webui-local / webui-local-user 的验收 Session 26 发送提问请求，观察到发送箭头切换为停止；单击停止后问题取消、会话显示已停止、发送箭头恢复。切换前后按钮尺寸与横坐标一致（32×32，x=1161），截图确认没有重叠或额外确认按钮。

## 2026-09-07 侧栏入口精简

- 顶部新建会话右侧保留一个放大镜，点击后展开并聚焦既有搜索框，再次点击或 Escape 收起并清空条件、恢复完整列表；标题、路径和完整会话引用搜索继续复用原逻辑。底部重复搜索按钮移除，键盘命令面板保留。
- 托管会话不再显示分组标题，辅助技术仍可读取分组名称；本地只读分组提示保留。状态筛选通过 `hidden` 暂时隐藏，原控件代码保留，后续可直接恢复。
- Topology impact: none；Blast radius: B1_SCENARIO；仅 RT-OUTPUT 的侧栏与相邻搜索入口调整，继续消费既有会话列表，不增加接口、订阅、存储或模型成本。正收益是减少重复入口和常驻控件；搜索需先点击图标。回滚本次应用提交即可恢复布局。
- 验收：476 项前端测试、前端构建和全仓 Go 测试通过，覆盖引用导航、搜索、收起清空、焦点恢复、拖拽和侧栏缩放。真实旧身份页面搜索 333 返回 1 条，Escape 后恢复 27 条；桌面和 390×844 手机截图确认图标同排、隐藏项不可见、无横向溢出。
