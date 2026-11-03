# golang-cc 产品体验与能力完善清单

更新时间：2026-08-31

## 2026-08-31 实施进度

本轮已完成并推送的 P0 增量：

- WebUI 阅读密度优化（P1）：消息间距和 Markdown 行距已收紧；composer 默认高度约减半，输入按内容自动增高至 176px 上限，超过上限内部滚动；桌面与 390x844 移动真实截图验收通过。

- WebUI 模型/provider discovery 已修复为合并全局 settings 与项目配置；显式 provider 选择在项目关闭 fallback 时可回退到全局 registry，避免模型下拉框与实际运行配置分裂。

- `TurnUsage`：provider-neutral adapter、tenant ledger 的 `provider/turn/usage_source/cache tiers` migration 与兼容 readback 已完成；Trace/Web/TUI 当前回合统一消费者仍未完成。
- `AgentExecutionOverride`：Runtime、Task、Agent、AgentCreate、batch item、Team member 已统一字段合同并持久化 Team override；provider registry preflight reason code 已完成，健康探活、fallback/cooldown/concurrency 和完整 UI readback 仍未完成。
- `MediaAsset`：合同、内存存储、MySQL `media_assets` migration/仓储、租户/用户/会话授权、过期清理和 mobile presign uploading 记录已完成；对象存储完成回调、派生处理、历史附件 readback、Trace 图片消费者仍未完成。
- P0 真实验收：`go test ./... -count=1`、Web Vitest 163 tests、Web build 已通过；本机 Homebrew MySQL/Redis E2E 已通过，新增真实 readback 覆盖 MediaAsset、跨用户拒绝和 TurnUsage ledger provider/turn/cache 字段；真实 provider 负向矩阵仍需 provider 凭据。

因此，本清单中的五项 P0 仍不能整体标记为 `DONE`；未完成项按影响范围排序为：真实 provider/多租户负向验收（B5）> Trace/Web/TUI TurnUsage 消费（B4）> MediaAsset 对象存储回调与派生生命周期（B4/B5）> 子代理 fallback/健康探活与 UI readback（B4/B5）。

本文把会话体验、多模态、子代理控制和 TUI 后续完善项收敛为一份可执行清单。它描述当前事实、目标架构、依赖顺序、验收证据、观测与回滚边界，不把规划中的能力写成已经完成。

本次只新增规划文档，不修改 runtime、协议、数据库、WebUI 或 TUI 行为。现有全局拓扑节点和因果边仍准确，因此本次 `Topology impact: none`、`Blast radius: B0_LOCAL`。后续实现时必须按具体条目重新评估，不能沿用本次文档变更的半径结论。

## 1. 目标和完成口径

目标不是复刻某个产品的界面，而是让 golang-cc 的通用 Agent runtime 在 Web Agent、Mobile Chat、TUI、Trace 和 subagent 路径上形成一致、可验证、可演进的能力闭环。

状态口径：

| 状态 | 含义 |
| --- | --- |
| `DONE` | 生产路径、用户入口、持久化/恢复和测试证据均存在；已知边界已明确。 |
| `PARTIAL` | 底层协议或局部入口存在，但至少缺少一个用户闭环、消费者、恢复路径或验证面。 |
| `PLANNED` | 当前代码没有达到目标行为；本文已给出明确目标和验收标准。 |

完成一项功能至少同时满足：

1. producer、consumer、gate、persistence、renderer 和 observability 已逐项检查。
2. 正向、负向、恢复、兼容和真实入口测试均有证据。
3. 用户可在目标界面完成操作，不依赖手工编辑内部 JSON 或查看数据库。
4. 协议/API/schema 变化完成迁移和旧消费者兼容验证。
5. 文档、Swagger、拓扑 registry 和专题图按实际影响同步。

## 2. 当前能力基线

### 2.1 会话体验

| 能力 | 当前状态 | 当前证据与边界 |
| --- | --- | --- |
| Thinking 展示控制 | `PARTIAL` | TUI 已支持 `full/summary/hidden`、按回合展开/收起和 resume 详情；Web Agent 已支持三态模式、连续 phase 聚合、耗时/行数指标、消息级展开/收起和页面内 session/turn/phase 状态隔离，provider 级 phase token 归因、状态指标和刷新后持久化仍待完成。 |
| System Prompt 查看 | `DONE` | Prompt Dump 查看器默认只展示 System section 摘要；完整请求必须本机、鉴权后显式打开，未采集时明确提示，且 section 来源/字节数/cache/hash 可审查。 |
| Web 正文宽度 | `DONE` | Web Agent 左右栏支持拖拽和键盘调宽，宽度本地持久化，中心区获得剩余空间。 |
| 每轮 token | `PARTIAL` | Local transcript usage 已带 turn/provider/source 并可通过 `session usage` 汇总读回 `TurnUsage`；tenant ledger、Trace、Web/TUI 当前回合视图和跨 provider 统一消费仍待完成。 |
| 回合导航 | `PARTIAL` | Web Agent 可切换 conversation run，TUI 有 resume/rewind picker；都没有正文级上一回合、下一回合、按回合搜索。 |
| 字号/阅读密度 | `PARTIAL` | WebUI 没有字号控制；TUI 无法控制终端字体字号，只能控制主题、间距、密度和信息层级。 |
| 按会话保存草稿 | `PARTIAL` | Web Agent、Chat Lab、TUI 已有本地草稿存取和单项恢复证据；三端组合隔离、resume、附件失败恢复矩阵仍待完成。服务端草稿明确不在当前范围。 |

### 2.2 多模态

| 能力 | 当前状态 | 当前证据与边界 |
| --- | --- | --- |
| 图片进入模型 | `DONE` | Mobile/OpenAI-compatible/query 已能把 URL 或本地图片转成模型 content block，并支持多模态模型路由。 |
| TUI 图片粘贴 | `DONE` | 支持剪贴板图片、附件托盘、移除/清空和 `[Image #N]` 引用。 |
| Web Chat Lab 上传 | `PARTIAL` | 支持 SHA-256、预签名、S3/metadata 上传和附件发送；移动 presign 在配置 `MediaAssetStore` 时会持久化带授权/过期信息的 MediaAsset，待发送图片附件已有安全 URL 预览，消息历史仍缺少媒体渲染。 |
| 图片发送后立即回显 | `PLANNED` | 未形成 optimistic media message、上传/压缩状态和失败重试的统一状态机。 |
| 后台图片压缩 | `PLANNED` | 当前 auto compact 是上下文压缩，不是图片压缩；没有图像归一化、缩放、分片或派生资产。 |
| Trace 查看图片 | `PLANNED` | Trace 能记录 attachment/hook 元数据，但 Trace WebUI 没有安全图片消费者。 |
| 稳定图片定位 | `PARTIAL` | runtime 能接收附件 ID、名称、URL/path，但没有跨消息、Trace 和模型共享的稳定 media reference 协议。 |
| 超长截图增强 | `PLANNED` | 没有长图切片、重叠窗口、清晰度策略、缩略图/原图双层加载和模型定位映射。 |

### 2.3 子代理控制

| 能力 | 当前状态 | 当前证据与边界 |
| --- | --- | --- |
| 每任务模型 | `PARTIAL` | `Task`/`Agent` 支持 `sonnet/opus/haiku` tier，runtime 可解析具体模型和 `subagentModelTiers`；用户 schema 仍主要暴露 tier。 |
| 每任务 provider | `PARTIAL` | Runtime Request、Task、Agent、AgentCreate 和 Team member 已有统一 provider 字段/覆盖合同；具体 provider registry、凭据和健康检查仍依赖部署接入。 |
| 每任务 reasoning effort | `PARTIAL` | `Task` 支持 `low/medium/high/max`；`Agent` 和 `AgentCreate` 暴露面不一致。 |
| 每任务最大输出 | `PLANNED` | runtime 有全局 `MaxTokens`，调用级 request/schema 没有 `max_output_tokens`。 |
| 参数 readback | `PARTIAL` | Runtime Result 已回读 effective provider/model/effort/max output/max turns/timeout 与 override reason codes；Web/TUI progress 和完整 requested/effective 降级展示仍待完成。 |
| Web/TUI 选择器 | `PARTIAL` | Web 主会话有 provider/model/effort 控件；子代理创建面没有完整选择器，TUI 也没有对应交互入口。 |

### 2.4 TUI 已完成基线

以下能力已经存在，后续工作必须保护，不能在清单实施中弱化或重复建设：

- `DisplayTimeline` 驱动的消息、Thinking、tool、subagent 和状态顺序。
- `RenderBudget` 的 full/compact/minimal 布局降级与窄终端保护。
- 真流式文本、文本撤回修正、阶段边界落入 scrollback、Ctrl+C 取消。
- 默认关闭 mouse tracking，`Ctrl+O` 显式切换滚动模式，保护终端原生选择复制。
- 图片粘贴、附件托盘、移除/清空和附件引用。
- subagent 聚合进度、折叠/展开、失败/运行中优先排序。
- 权限审批、AskUserQuestion、slash suggestions、next-step suggestions。
- Todo 完成摘要归档、usage 友好文案、工具失败与安全保护分离。
- Markdown 标题、列表、表格、代码块、inline code 和受限 rich inline style。
- resume、rewind、redo、branches、recap、Goal、background 状态入口。

现有 TUI 剩余技术债包括：`liveDisplayBlocks` compatibility fallback、`toolActivity` 的多重职责、`app_test.go` 超大测试文件、三个 500 行以上的混合职责文件，以及输入法候选框的终端光标限制。

## 3. 目标架构

### 3.1 分层原则

统一能力必须分成四层，避免 Web、Mobile、TUI 和 Trace 各自实现一套：

| 层 | 责任 | 推荐归属 |
| --- | --- | --- |
| 领域合同 | turn usage、media asset、draft、agent execution override 的稳定结构和校验 | `internal/session`、新增聚焦包或既有 provider-neutral 包 |
| Runtime 服务 | provider 路由、媒体处理、subagent 执行、持久化与恢复 | `internal/query`、`internal/agentruntime`、`internal/server`、`internal/storage` |
| 观测合同 | requested/effective 配置、阶段耗时、错误和 artifact 引用 | `internal/telemetry`、`internal/session`、Trace API |
| 表现层 | Web Agent、Chat Lab、TUI、Trace 的交互和降级渲染 | `web/src`、`internal/tui`、Trace viewer |

架构约束：

1. `MediaAsset`、`TurnUsage`、`AgentExecutionOverride` 等核心语义只能有一个 provider-neutral 定义。
2. Web/TUI renderer 不直接决定 provider 路由、图片压缩或 token 计数口径。
3. 原始图片、派生图片、缩略图和长图切片必须共享同一 asset ID 与父子关系。
4. requested 配置与 effective 配置分开记录；fallback、clamp、policy override 不能静默发生。
5. UI 草稿不进入模型 transcript；发送成功后才形成用户消息和审计记录。
6. System Prompt 默认不向远程客户端暴露原文，只提供脱敏摘要和本地受限诊断入口。

### 3.2 关键数据合同

后续设计应收敛到以下四个合同，而不是继续增加松散 metadata 字段：

| 合同 | 最少字段 | 主要消费者 |
| --- | --- | --- |
| `TurnUsage` | session、turn、provider、model、input/output/cache、estimated、source | Web message footer、TUI usage、Trace、quota |
| `MediaAsset` | asset_id、kind、media_type、name、size、sha256、state、original、derivatives、access policy | composer、message、model request、Trace |
| `ConversationDraft` | surface、session、workspace、text、asset_ids、updated_at、schema_version | Web Agent、Chat Lab、TUI |
| `AgentExecutionOverride` | provider、model、effort、max_output_tokens、max_turns、timeout、requested/effective | Task、Agent、AgentCreate、Team、Trace |

具体命名和存储位置必须在各自实施设计中确定。本文只锁定语义边界，不提前制造公共类型。

## 4. 会话体验完善清单

### SESSION-001 Thinking 分段折叠

- [x] TUI Thinking 展示模式为 `full / summary / hidden`：`full` 展示正文，`summary` 折叠为摘要，`hidden` 只保留运行状态；旧的 `showThinking=true/false` 映射到 `full/hidden`。
- [x] Web Agent 已对齐 `full / summary / hidden`，并兼容旧 `webAgentUI.showThinking`：未配置三态字段时 `true/false` 映射为 `full/hidden`。
- [x] Web Agent 将连续 `thinking_delta` 聚合为一条 Thinking phase，折叠态显示回合/phase 锚点；当前已提供消息级摘要卡片，点击“展开思考”显示原文，点击“收起思考”恢复摘要。
- [x] Web Agent 折叠态补齐 phase 耗时、行数和回合级摘要锚点；无法可靠归因的 token 明确显示“token 未单独统计”，不做估算。
- [ ] Web Agent 接入 provider 级 phase token 归因和明确的 phase 状态指标。
- [x] Web 的折叠状态按 `session_id + turn + phase_id` 保存在页面 UI 状态，切换显示模式不丢失且不会串到其他 phase/会话；重新加载后以默认模式恢复，不把 UI 偏好写进模型 transcript。
- [x] TUI 在 phase commit 前按当前模式渲染；进入 terminal scrollback 后不尝试撤回或重写已经打印的内容。
- [x] TUI 的 `summary` 模式在 scrollback 中只固化摘要行；通过 `/thinking show <turn>` 打开该回合的可滚动详情，通过 `/thinking summary <turn>` 或 `Esc` 返回摘要，不使用会抢占原生复制的 mouse tracking。
- [x] TUI 详情视图从 `DisplayTimeline` 和既有 session transcript 的 UI-only archive 读取完整正文；不完整尾部或没有数据时明确报错，不显示伪造内容。
- [x] TUI 折叠、展开和隐藏只改变 renderer 状态，不删除 session transcript/Trace/usage，不改变 provider request，不新增模型 turn、token 或工具调用。
- [x] TUI Thinking 与 assistant/tool 的顺序继续由现有 DisplayTimeline 保证；折叠不会造成重复、乱序或跨回合串联。

折叠/展开语义：

| 场景 | 折叠态 | 展开态 | 不能做的事 |
| --- | --- | --- | --- |
| Web live phase | 摘要卡片 + 运行状态 | 当前 phase 正文实时增长 | 不把正文复制进另一个消息，不改变流事件顺序。 |
| Web completed phase | 摘要卡片 | 当前 phase 完整正文 | 不因展开重新请求模型或重新计算 usage。 |
| TUI live phase | live 区显示摘要行 | live 区显示正文 | 不用 ANSI hack 把光标移到已打印历史，不默认开启 mouse tracking。 |
| TUI committed phase | scrollback 保留摘要 | `/thinking show <turn>` 进入详情视图 | 不撤回、覆盖或重排 terminal scrollback。 |
| `hidden` 模式 | 仅显示“正在思考/已完成”状态 | 无正文展开 | 不删除原始 Thinking 数据。 |

验收：多段 Thinking、Thinking→tool→assistant、取消、provider 无 Thinking、resume 五类场景顺序一致；不增加模型 turn、token 或工具调用。

影响：`RT-OUTPUT`、`RT-OBSERVE`，默认 `B2_MODE`；不得为此修改共享 system prompt。

### SESSION-002 System Prompt 安全摘要

- [x] 会话 UI 默认只展示 prompt section 名称、来源、字节数、cache scope 和 hash，不展示密钥或完整正文。
- [x] 本地受限入口可以展开完整 prompt；Prompt Dump 继续受 localhost/auth gate 保护，远程请求不能绕过。
- [x] 用户可通过 System section summary 区分 system prompt、memory、skills、tenant context 和 runtime reminder 来源。
- [x] prompt dump 未启用时明确显示“未采集”，不能伪造快照。

验收：脱敏测试覆盖 API key、JWT、完整聊天正文和私有 URL；hash/source 与真实 model request 一致。

影响：`RT-PROMPT`、`RT-CACHE`、`RT-OBSERVE`、`RT-OUTPUT`，`B4_PROTOCOL`。

### SESSION-003 统一每轮 Token

- [x] 已建立 provider-neutral `TurnUsage` 合同：以 session/turn/provider/model 标识一轮，区分非缓存 input、cache tiers、output，并携带 `estimated/source` provenance；query quota settlement 已通过 `UsageFromTurnUsage` 统一转换入口，跨入口落库和消费尚未接通。
- [x] Local transcript `usage` entries now persist an explicit `turn` anchor while retaining legacy fields and call compatibility; tenant ledger/Trace/Web/TUI unified consumption remains open。
- [x] `session.UsageSummary` now exposes provider/source-aware `TurnUsage` readback for anchored usage entries without changing legacy aggregate fields; tenant ledger/Trace/Web/TUI wiring remains open。
- [x] tenant usage ledger 已通过 migration 保存 `provider + turn_index + usage_source`，并持久化非缓存 input、cache read、cache creation 及 5m/1h tiers；旧 ledger 查询列形状仍可读回。
- [ ] Web 回复底部展示该回复对应 turn 的精确值；TUI 支持“本轮/会话”两种视图。
- [ ] provider 未报告 usage 时只显示估算，并明确标识，后续实际 usage 到达后可校正。
- [ ] quota、Trace、Web/TUI 使用同一求和口径，不在 renderer 内自行推导不同总数；quota settlement 与 tenant ledger 已复用统一 adapter，Trace/Web/TUI 当前回合消费者仍未完成。

验收：Anthropic、OpenAI Chat Completions、OpenAI Responses、cache tiers、fallback、无 usage 六类矩阵一致。

影响：`RT-MODEL`、`RT-OBSERVE`、`RT-PERSIST`、`RT-OUTPUT`，`B4_PROTOCOL`。

### SESSION-004 回合导航和搜索

- [ ] Web Agent 提供上一用户回合、下一用户回合、跳到最新和按关键词查找。
- [ ] TUI 提供不抢占原生复制的键盘导航；默认不启用 mouse tracking。
- [ ] 导航锚点使用稳定 message/turn ID，不依赖 DOM index 或 terminal visual row。
- [ ] compact、resume、branch、rewind 后锚点仍可解释；被压缩的回合跳到 compact summary。

验收：长会话 600+ 消息、compact 前后、branch/redo、窗口 resize、中文宽字符均能稳定定位。

影响：`RT-PERSIST`、`RT-OUTPUT`，`B2_MODE`；如新增 API cursor 则升级为 `B4_PROTOCOL`。

### SESSION-005 按会话草稿

- [x] Web Agent 以 `surface + session + workspace` 在浏览器本地保存文本草稿，切换模式和刷新不写入 transcript。
- [x] Chat Lab 以 `surface + session + workspace` 在浏览器本地保存文本和稳定附件 ID，刷新后恢复且不发送消息。
- [x] TUI 以 `surface + session + workspace` 保存文本和附件引用，使用 `0600` 原子文件，重启恢复，清空时删除；三端均不写入 transcript。
- [ ] 完成三端双会话隔离、刷新/异常退出/resume 组合验收，确保不会把 A 会话草稿带到 B 会话；当前 Web Agent、Chat Lab 刷新和 TUI 重启已有单项证据。
- [ ] 完成三端发送成功原子清理、失败保留并标记最近错误的统一矩阵；当前 Web Agent/Chat Lab 已接入，TUI 仍需补附件失败恢复验收。
- [x] 本地草稿文件使用 `0600`；服务端草稿不在本地范围内，暂不引入 tenant/user/session 服务端存储和过期策略。
- [x] 草稿只存文本和附件引用，不复制图片 base64 或私有上传凭据。

验收：双会话来回切换、浏览器刷新、TUI 强制退出、附件上传中断、发送重试和跨租户负向测试。

影响：`RT-PERSIST`、`RT-BOUNDARY`、`RT-OUTPUT`，本地草稿为 `B2_MODE`，服务端草稿为 `B5_SHARED_STATE`。

### SESSION-006 阅读密度

- [ ] WebUI 提供小/标准/大三档阅读字号和舒适/紧凑密度，持久化为用户 UI 偏好。
- [ ] TUI 明确不承诺控制终端字体字号；提供 compact/comfortable 信息密度和高对比主题。
- [ ] 所有档位保持布局预算、代码块、表格、长单词和移动端不溢出。

验收：桌面/移动 Web 截图矩阵；TUI 80×24、96×24、120×32、150×40 PTY 矩阵。

影响：`RT-OUTPUT`，`B2_MODE`。

## 5. 多模态完善清单

### MEDIA-001 统一 MediaAsset 生命周期

- [x] Provider-neutral `MediaAsset` 合同已定义 `selected/hash_pending/uploading/uploaded/processing/ready/failed` 状态、稳定 asset ID、授权元数据和 legacy attachment 映射；内存/可注入存储与过期清理已接入。
- [x] 状态合同已接入 MySQL `media_assets` migration/仓储和移动 presign 的 uploading 持久化路径；对象存储完成回调/处理 worker 仍待接入。
- [ ] 原图和 thumbnail/normalized/tile 派生资产有稳定父子关系、hash 和可审计来源。
- [x] 内存与 MySQL 读取路径执行 tenant/user/session 隔离并拒绝过期记录；presign 记录带过期时间。
- [x] 老的附件 metadata 可通过 `FromLegacyAttachment` 映射到新合同；真实历史消息 readback 仍待补集成验收。

验收：幂等上传、重复 hash、并发处理、跨租户拒绝、派生失败回退原图、过期 URL 续签。

影响：`RT-BOUNDARY`、`RT-PERSIST`、`RT-MODEL`、`RT-OUTPUT`，`B4_PROTOCOL` 与 `B5_SHARED_STATE`。

### MEDIA-002 立即回显和失败恢复

- [ ] 选择图片后立即使用本地 object URL/terminal metadata 回显，不等待上传完成。
- [ ] 消息发送后保留图片位置和状态；上传/处理失败可原位重试或移除。
- [ ] Web revoke object URL，TUI 清理临时文件，避免泄漏。
- [ ] 服务端最终 readback 能把 optimistic asset 与持久 asset 对齐。

验收：慢网、断网、取消、刷新、重复点击发送、超限、非法媒体类型。

影响：`RT-OUTPUT`、`RT-PERSIST`，`B2_MODE`；接入统一状态协议后为 `B4_PROTOCOL`。

### MEDIA-003 后台规范化与压缩

- [ ] 使用成熟图像库处理旋转、色彩空间、尺寸限制、metadata 清理和格式转换。
- [ ] 原图上传与 UI 回显不被压缩任务阻塞；模型请求等待 `ready` 或按策略使用原图。
- [ ] 记录原始/派生尺寸、压缩比、处理耗时、错误和回退原因。
- [ ] 不放大低分辨率图片，不用有损压缩破坏文字截图。

验收：PNG/JPEG/WebP/HEIC、透明图、EXIF 旋转、超大图、文字截图、处理超时和 worker 重启。

影响：`RT-PERSIST`、`RT-MODEL`、`RT-OBSERVE`，`B3_GLOBAL_RUNTIME`。

### MEDIA-004 长截图切片

- [ ] 超过阈值的长图生成 overview、重叠 tiles 和坐标 manifest。
- [ ] 模型可按 `asset_id + tile_id + 坐标` 引用局部，不靠模糊的“第几张图”。
- [ ] UI 默认显示缩略 overview，按需加载原图或 tile。
- [ ] tile 数、总像素和模型输入成本有硬上限，超限给出可恢复提示。

验收：网页长截图、聊天记录、表格、代码 diff、极窄长图；文字 OCR/视觉定位不因切片边界丢失。

影响：`RT-MODEL`、`RT-CACHE`、`RT-OBSERVE`、`RT-OUTPUT`，`B3_GLOBAL_RUNTIME` 与 `B4_PROTOCOL`。

### MEDIA-005 Trace 图片查看

- [ ] Trace 事件只保存 asset reference 和安全 metadata，不内嵌巨型 base64。
- [ ] Trace WebUI 根据授权获取 thumbnail，用户操作后再加载原图。
- [ ] 导出 artifact 默认不携带私有图片正文；显式包含时给出敏感信息告警。
- [ ] asset 缺失、过期或无权限时显示可诊断状态。

验收：tenant/local trace、导出、过期、无权限、删除后 readback 和超长图 tiles。

影响：`RT-OBSERVE`、`RT-PERSIST`、`RT-OUTPUT`，`B4_PROTOCOL` 与 `B5_SHARED_STATE`。

## 6. 子代理完善清单

### SUBAGENT-001 统一执行覆盖参数

- [x] `Task`、`Agent`、`AgentCreate` 和 Team member 已共享 `agentruntime.AgentExecutionOverride` 语义；Team member 通过 `execution_override_json` 持久化。
- [x] Runtime `Request`、Task、Agent、AgentCreate 及 batch item 已支持 provider、具体 model、effort、max_output_tokens、max_turns 和 timeout；Team API 已接收并保存同一覆盖 JSON。
- [ ] 保留 tier alias 作为便捷层，但 tier 最终解析成具体 provider/model 后再执行。
- [ ] 未指定字段继承父会话或 Agent Profile；显式字段优先级和 policy clamp 固定。

验收：三种工具单任务/后台/batch、Profile、Team、非 Anthropic provider 和嵌套 subagent。

影响：`RT-TOOLS`、`RT-SUBAGENT`、`RT-WIRING`、`RT-MODEL`，`B4_PROTOCOL`。

### SUBAGENT-002 Provider registry 与 client ownership

- [x] subagent runtime 已接收 provider-neutral `ClientResolver`，按单次运行选择 client，并保留 cleanup ownership；各工具的 provider 参数 wiring 和 registry preflight 仍待完成。
- [x] Runtime 提供可注入 `ProviderPreflight`，并在 client resolver 前拒绝 preflight 错误；`NewProviderPreflight` 已覆盖 provider registry、model 支持和凭据存在性 reason code，网络健康探活仍可由部署注入。
- [ ] fallback 是否允许由明确策略决定；Runtime 已记录实际 provider/model，但跨 provider fallback 策略和冷却/并发上限仍待接入。
- [ ] client 生命周期、连接池、冷却和并发上限在 runtime 层统一管理。

验收：命名 provider、同模型多 provider、fallback、stream 已开始后失败、并发 batch 和 client cleanup。

影响：`RT-WIRING`、`RT-SUBAGENT`、`RT-MODEL`，`B3_GLOBAL_RUNTIME`。

### SUBAGENT-003 Requested/Effective Readback

- [ ] started/progress/finished 事件包含脱敏后的 requested/effective 配置和 override reason。
- [ ] Web/TUI progress 显示 provider、model、effort、max output 和预算消耗。
- [ ] clamp、inherit、fallback、policy deny 均有稳定 reason code。
- [ ] task store、Trace、CapabilityLoop 和 Goal consumer 保持兼容。

验收：resume、detached、failed、cancelled、timeout、fallback、compact 后仍能读回。

影响：`RT-SUBAGENT`、`RT-EVIDENCE`、`RT-PERSIST`、`RT-OUTPUT`，`B4_PROTOCOL`。

### SUBAGENT-004 用户选择器

- [ ] Web 主会话和 subagent 创建面复用 provider/model/effort/max output 控件。
- [ ] TUI 提供 `/provider`、`/model`、`/effort`、`/max-output` 的查看和修改入口，显示只作用于当前会话还是全局默认。
- [ ] 不允许用户选择当前部署不可服务的组合。
- [ ] 选择变化在执行前显示 effective preview。

验收：键盘操作、窄终端、无 provider API、配置热更新、当前会话与新会话作用域。

影响：`RT-ENTRY`、`RT-WIRING`、`RT-OUTPUT`，`B2_MODE`；协议共用部分为 `B4_PROTOCOL`。

## 7. TUI 专项完善清单

### TUI-001 回合导航与会话内搜索

- [ ] 为 DisplayTimeline segment 建立稳定 turn/message anchor 索引。
- [ ] 支持上一/下一用户回合、跳到最新、按关键词定位和清除搜索。
- [ ] 不接管终端原生 `Cmd/Ctrl+F`；使用 slash 命令或不冲突的显式快捷键。
- [ ] compact/rewind/resume 后显示真实 anchor 状态。

验收：10/100/600 回合、中文、表格、工具卡、resize、scroll mode/copy mode。

### TUI-002 Resume/Rewind Picker 搜索

- [ ] picker 支持标题、CWD、session ID、时间和预览文本过滤。
- [ ] 过滤结果保持键盘、滚轮和双击行为一致。
- [ ] 打开 picker 前当前草稿先保存，取消 picker 后恢复。

验收：空结果、千条 session、中文输入、鼠标临时开启后关闭并恢复 copy mode。

### TUI-003 每轮 usage 详情

- [ ] 当前底部 usage 保留 session 汇总，并增加当前 turn 详情入口。
- [ ] 显示 actual/estimated、provider/model、cache tiers、stop reason 和成本未知状态。
- [ ] 完成 turn 的 usage 跟随 turn anchor 进入 transcript display，不制造 model-visible 消息。

验收：多 provider、tool-use 多轮、无 usage、取消、失败恢复、窄终端 compact mode。

### TUI-004 草稿与附件恢复

- [ ] textarea、附件 asset ID、光标位置和更新时间按 session/CWD 保存。
- [ ] `/resume`、异常退出和进程重启后恢复；发送成功后清理。
- [ ] 临时图片文件有引用计数/过期清理，不因草稿恢复泄漏 `/tmp`。
- [ ] 草稿文件使用 golang-cc owned 目录和 `0600` 权限。

验收：强杀进程、多个 CWD、多个 session、图片草稿、附件被外部删除和版本迁移。

### TUI-005 运行时配置面板

- [ ] 提供当前 provider/model/effort/max output/permission/sandbox 的只读摘要。
- [ ] 可服务字段通过 slash picker 修改；安全字段仍走现有 permission/config gate。
- [ ] 配置热更新与手动选择冲突时显示来源和优先级，不静默覆盖。

验收：配置文件修改、provider 下线、会话继承、错误组合和恢复默认。

### TUI-006 多模态反馈

- [ ] 附件 tray 显示 hash/upload/process 状态和失败重试。
- [ ] 支持终端能力探测后的可选 Kitty/iTerm2/Sixel 图片预览；不支持时稳定降级为 metadata。
- [ ] 长截图显示 overview/tile 数和模型实际使用的派生资产。
- [ ] 预览绝不默认开启 mouse tracking，也不破坏复制和 scrollback。

验收：三类图像协议与纯文本终端、tmux、SSH、Codex Desktop、无图片能力降级。

### TUI-007 外部编辑器

- [ ] 长 prompt 可显式打开 `$VISUAL`/`$EDITOR`，返回后恢复 textarea 和附件。
- [ ] 编辑器启动前暂停 TUI renderer，退出后完整恢复 terminal 状态。
- [ ] 临时文件为 `0600`，异常退出时可清理，不写入 transcript 直到发送。

验收：vim/nano/VS Code wait 模式、编辑器失败、取消、中文、多行和附件草稿。

### TUI-008 用户手动命令协议

- [ ] 仅在完成独立安全设计后支持 `! <command>`；复用 Bash permission、sandbox、hooks 和审计。
- [ ] stdout/stderr/exit code 以用户执行结果进入 transcript，模型不能假装已执行。
- [ ] 交互登录、2FA、浏览器授权给出明确的终端所有权和超时行为。
- [ ] 不与 slash command 或模型调用 Bash tool 混淆。

验收标准沿用 `docs/todo.md` 的 `TODO-047`，实现前必须单独评审 `B5_SHARED_STATE` 和凭据泄漏风险。

### TUI-009 输入法与可访问性

- [ ] 输入法候选框问题只通过 renderer 感知的光标机制或输入组件升级解决，禁止写帧后的 ANSI 光标 hack。
- [ ] 高对比、无色彩、窄终端和 screen reader 友好文案有独立模式。
- [ ] 所有快捷键在 UI 内可发现，并提供冲突检查和用户覆盖机制。

验收：真实 macOS/Linux 终端中文输入法、streaming、权限弹窗、长输入换行和原生复制。

### TUI-010 架构与测试债务

- [ ] 删除 `liveDisplayBlocks` 对正常路径的兼容 fallback，只保留明确 adapter。
- [ ] 拆分 `app_test.go`，测试归属与源文件职责对应，跨模块测试放入显式 integration 文件。
- [ ] 将 `welcome.go`、`agent_progress.go`、`live_display.go` 的状态机与 renderer 分离。
- [ ] 评估删除包内 `min/max`，不混入行为功能提交。

验收：不修改既有断言语义；`go test -race ./internal/tui -count=1` 和 PTY matrix 通过。

### TUI-011 Thinking 折叠与展开交互

- [x] 增加 `/thinking`、`/thinking full`、`/thinking summary`、`/thinking hide` 和 `/thinking show <turn>` 命令，命令补全和帮助文案明确作用域。
- [x] `summary` 模式只在主 transcript 显示摘要；`show <turn>` 使用独立可滚动详情，不污染主 transcript、不改变模型可见消息。
- [x] phase commit 后只允许进入详情视图，不能假装可以修改已写入的 scrollback。
- [x] 详情视图显示回合、phase 和完整正文；摘要显示行数、耗时与 token 可用状态，没有完整正文时给出确定错误。
- [x] 与 `Ctrl+T` 工具详情、`Ctrl+Y` Todo/subagent 展开、`Ctrl+O` copy/scroll 模式不冲突；默认不启用 mouse tracking。
- [x] TUI 配置热更新和 `/resume` 后恢复模式，但不恢复上一个会话的展开选中项。

验收：短思考、长思考、多 phase、Thinking→tool→assistant、取消、resume、窄终端、tmux、SSH、纯文本终端和原生复制矩阵；折叠/展开前后模型请求、token、turn、tool call 和 transcript 字节保持不变。

影响：`RT-OUTPUT`、`RT-OBSERVE`、`RT-PERSIST`，默认 `B2_MODE`；若新增详情 API 或 sidecar schema 则升级为 `B4_PROTOCOL`。

## 8. 分期与依赖顺序

| 批次 | 优先级 | 范围 | 依赖 | 退出条件 |
| --- | --- | --- | --- | --- |
| 0. 合同冻结 | P0 | `TurnUsage`、`MediaAsset`、`ConversationDraft`、`AgentExecutionOverride` 的设计和兼容策略 | 无 | 字段、ownership、迁移、权限、观测和消费者清单评审通过。 |
| 1. 会话高 ROI | P0 | 草稿、回合导航、每轮 token、Thinking 折叠/展开 | 批次 0 的 turn/draft 合同 | Web/TUI 核心路径可用，未触碰图片处理和 provider client ownership。 |
| 2. 子代理控制 | P0 | provider/model/effort/max output、resolver、readback | 批次 0 的 execution contract | Task/Agent/AgentCreate/Team 行为一致，跨 provider 验收通过。 |
| 3. 多模态闭环 | P1 | 立即回显、统一 asset、后台处理、Trace 图片 | 批次 0 的 media contract | Web/Chat/TUI/Trace 共用 asset ID，失败可恢复。 |
| 4. 长截图增强 | P1 | overview、tiles、坐标 manifest、成本上限 | 批次 3 | 真实长截图清晰度和模型定位优于原图直传且成本可控。 |
| 5. TUI 产品化 | P1/P2 | 搜索、picker、runtime panel、外部编辑器、可选图片协议、IME | 批次 1/2/3 按功能依赖 | PTY/真实终端矩阵通过，不破坏 copy/scroll。 |
| 6. 技术债治理 | P2/P3 | compatibility fallback、测试拆分、职责拆分、min/max | 功能行为稳定 | 纯重构提交与行为提交分离，测试语义不变。 |

推荐并行边界：

- `SESSION-004/006` 可以在合同评审后作为 Web/TUI 局部工作并行。
- `SUBAGENT-002` 必须先于完整 provider 选择器，不能由 UI 直接拼 client。
- `MEDIA-003/004/005` 共享 asset contract，不能三套 ID 和状态机并行生长。
- TUI 架构债务只能在相邻行为有测试锁定后处理，不应阻塞高 ROI 用户功能。

## 9. 回归与验收矩阵

### 9.1 Go/runtime

```bash
go test ./internal/session ./internal/query ./internal/agentruntime ./internal/tools/task ./internal/tools/agent -count=1
go test ./internal/server ./internal/storage/mysql -count=1
go test ./internal/tui -count=1
go test -race ./internal/tui ./internal/agentruntime -count=1
go test ./... -count=1
```

### 9.2 Web

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
npm --prefix web run test:e2e
```

### 9.3 TUI 真实终端

```bash
scripts/tui-display-order-acceptance.sh
scripts/tui-render-budget-acceptance.sh
scripts/tui-ten-turn-acceptance.sh
scripts/tui-session-replay-acceptance.sh
TUI_VISUAL_SOP_FULL=1 scripts/tui-visual-regression-sop.sh
```

人工矩阵至少覆盖：80×24、96×24、120×32、150×40；Terminal.app、iTerm2、tmux、SSH 和 Codex Desktop；copy mode 与 scroll mode；中文输入和长 Markdown。

### 9.4 API/协议

涉及 Mobile/OpenAI/Web API、SSE、DB 或 Swagger 时额外执行：

```bash
go test ./internal/server -count=1
swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency
go run ./scripts/runtime-topology-check --base HEAD --working-tree --impact updated --blast-radius B4_PROTOCOL --reason '<具体协议变更原因>'
```

真实 server + curl 需要覆盖鉴权、附件预签名、上传、流式事件、错误码、幂等、cancel/regenerate、Trace readback 和数据库落库。

## 10. 观测指标

| 领域 | 必备指标 |
| --- | --- |
| 会话 | draft restore/sent/expired、turn navigation success、actual/estimated usage ratio、prompt summary bytes |
| 多模态 | selected→echo、hash、upload、processing、ready 各阶段 p50/p95；压缩比；tile 数；失败/重试/回退率 |
| 子代理 | requested/effective provider/model/effort/max output；override/fallback/clamp reason；turn/token/tool/duration/cost |
| TUI | render duration p50/p95、throttled refresh、phase flush、frame width/height violation、cancel latency |
| 全局成本 | prompt bytes、input/output/cache token、model turns、tool calls、p50/p95 duration、cache hit、外部存储与处理成本 |

不得把图片原文、完整 prompt、JWT、API key、私有 URL 或完整聊天正文写入普通日志。

## 11. 发布、回滚和 Definition of Done

发布原则：

1. 新协议先兼容读、双写或 shadow，再切 consumer，最后清旧字段。
2. 新图片处理先按 tenant/surface feature flag 灰度；原图直传作为可控回退。
3. 子代理 override 先记录 requested/effective shadow 数据，再允许非默认 provider。
4. Thinking、字号/密度、图片预览等纯展示能力必须有用户开关。
5. TUI 新交互默认不启用 mouse tracking，不改变现有复制/粘贴/滚动快捷键。

回滚路径：

- UI：关闭 feature flag，继续消费旧 message/usage/attachment 结构。
- Media：停止生成派生资产，模型回退到合规原图；不删除原 asset 和审计记录。
- Subagent：忽略调用级 override，恢复继承父 provider/model；保留 readback 便于诊断。
- TUI：回退 renderer/command adapter，不回退 transcript、permission、sandbox 或 query runtime。

单项只有在以下条件全部满足时才能标记 `DONE`：

- [ ] 目标行为、非目标和安全不变量均有实现证据。
- [ ] 相关单元、集成、E2E、真实入口与负向测试通过。
- [ ] requested/effective、错误、恢复和成本可观测。
- [ ] 文档、Swagger、拓扑 registry/图件按实际变化同步。
- [ ] 旧会话、旧附件、旧 task result 和旧客户端兼容验证通过。
- [ ] 回滚已演练或至少完成 feature flag/readback 验证。
- [ ] 最终回复诚实说明仍未覆盖的 provider、终端、对象存储或真机边界。

## 12. 事实来源

- `docs/architecture/global_runtime_topology.md`
- `docs/architecture/runtime_topology.yaml`
- `docs/architecture/go_claude_agent_capability_boundaries.md`
- `docs/architecture/module_capability_assessment.md`
- `docs/todo.md`
- `docs/usage/tui.md`
- `docs/tui/tui_display_timeline_architecture_plan.md`
- `docs/tui/tui_render_budget_architecture_fix_plan.md`
- `docs/tui/tui_streaming_render_perf_fix_plan.md`
- `docs/tui/subagent_progress_ux_fix_plan.md`
- `docs/tui/tui_todo_usage_status_ux_fix_plan.md`
- `web/src/components/WebAgentPage.tsx`
- `web/src/components/ChatLab.tsx`
- `internal/query/query.go`
- `internal/agentruntime/runtime.go`
- `internal/tools/task/task.go`
- `internal/tools/agent/agent.go`
- `internal/tui/`
