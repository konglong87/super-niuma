# Super Niuma 完整控制面：架构与实现方案

日期：2026-11-03；设计基线：`ce58405`；状态：**A0 设计审查通过，按阶段实现；不代表产品验收通过**。

## 1. 目标与完成定义

实现一个 Desktop 内的交付控制面：多渠道消息 → 一个逻辑 Router → 授权工单 → 持久任务队列 → 多 Worker / 多 Subagent → 独立验收 → 原渠道交付及 Desktop 同步。普通会话仍走原路径，不能被 Super 劫持。

“全部完成”必须同时满足代码、自动化、原生 UI 和真实外部集成四种验收。组件存在、单测通过、mock 闭环、工单登记成功均不能单独代表平台完成。未配置的模型、渠道凭据、白名单及验收收件人是显式部署/验收阻塞，不能用假成功替代。

### 首版范围（是顺序拆分，不是缩减最终目标）

- 一个逻辑路由权威；多消费者事件总线与竞争领取任务队列分开。
- Desktop、签名 Webhook、飞书渠道；跨渠道互通须显式绑定同一授权工单，不默认广播。
- 至少两个并行 Worker，可调全局并发；有限 DAG 中的多个 Subagent、独立上下文、资源互斥和根工单共享预算。
- 确定性规则优先，必要时才使用受限模型分类；分类不能授予权限。
- 持久事件、重启重放、消费 ACK、租约/fencing、重试/死信、未知副作用状态。
- 明确授权范围内自动接单和执行；高风险动作停在审批，不伪造自动批准。
- 独立证据验收、有限返工、交付回执，Desktop 可观察、暂停/取消、重试和人工介入。

非目标：更换 Go/Wails 技术栈、重写 Session、引入外部消息中间件或 Kubernetes、先做跨地域 HA、未经授权操控真实账号/支付/发布、把人类审批说成模型自动验收。正式签名/分发和第二显示器仍遵循既有延期决定。

## 2. 参考来源与复用原则

核实的官方项目是 `deepseek-ai/dsh-superagent-prototype`，不是 `dsk-superagent`。设计时固定参考提交 `6337fc823ce20251c8de8085948165c81aaa701e`（2026-10-31；MIT）。

- 架构：https://github.com/deepseek-ai/dsh-superagent-prototype/blob/6337fc823ce20251c8de8085948165c81aaa701e/docs/007-architecture.md
- 域/恢复：https://github.com/deepseek-ai/dsh-superagent-prototype/blob/6337fc823ce20251c8de8085948165c81aaa701e/docs/001-domain.md
- 授权：https://github.com/deepseek-ai/dsh-superagent-prototype/blob/6337fc823ce20251c8de8085948165c81aaa701e/docs/002-authorization.md

借鉴原则：触发、工作意图、授权和执行实例分开；Router 与执行上下文隔离；快规则拦截后才调用慢模型；不可逆副作用需提前记录与恢复策略。不是逐行移植 TypeScript runtime，也不把参考实现的任何局部优化视为安全边界。

本仓库可复用：`internal/agentteam/router.go` 的确定性路由思想；`internal/channel` 的 Adapter / Inbound / Outbound 契约；`internal/channel/runtime` 的 inbox/outbox、lease、delivery_unknown；`internal/agentteam/workflow` 的 DAG、revision/ownership、取消和预算机制；`workflowapp` / `runtimecompose` 的版本化定义及运行配置。具体复用必须以接口审计为准，不能只凭名称。

## 3. 架构与依赖方向

```text
Desktop Wails ───────────┐
Signed Webhook adapter ─┼─> trusted identity + normalized MessageEnvelope
Feishu adapter ─────────┘                |
                                       v
                         Router gates (NO execution here)
                                       |
                        Admission + immutable Grant + WorkOrder
                                       |
                  single transaction: state + jobs + domain events
                     /                         \
             Durable Task Queue           Durable Event Bus
              (one claimant)           (one cursor per consumer)
                     |                    |       |       |
            bounded Worker Pool         UI     audit   notifications
                     |
           Super scheduler + reused query execution kernel
                     |
             isolated Subagent DAG
                     |
        Artifact manifest + independent Verification
                     |
              Delivery Outbox -> adapter -> receipt
```

模块边界：domain/contracts 不依赖 Wails/HTTP/模型；持久层实现 ports；Router 只能提出和登记已授权工作；Worker 只能使用 grant 内能力；Verifier 不能靠 Worker 自报成功；Delivery 只能投向 grant 已绑定目标。原 Session 不依赖 Super。

“一个 Router”指单一逻辑入站权威，不是宣称永不故障的单进程。首版 Desktop 单机一个活动协调器；未来多个协调器须通过相同存储租约及 epoch 竞争领导权，不得重复接单。SQLite 多连接/多进程竞争不等于已实现跨节点 HA。

## 4. 核心契约与状态机

### MessageEnvelope / Grant / WorkOrder

- Envelope：版本、可信来源账户、外部事件 ID、conversation/thread、可信 sender、内容引用、received_at_ms、correlation/causation、reply target。账号/租户由已认证 adapter 绑定，不能相信 body 声称的身份。
- Grant：允许的 workspace 根、工具与网络策略、模型/步数/时间/token 预算、最大子任务数/深度/并行度、允许的接收人、有效期及策略版本。限制只能收紧，子任务不能扩权。
- WorkOrder：目标、验收标准、来源/owner、grant snapshot、执行 run 引用、验收与交付结果。Session 是执行上下文，不是业务状态机。
- 时间：已有 ISO 时间字段不改；新增契约使用毫秒 epoch。所有枚举/默认上限集中定义。

```text
recorded -> awaiting_approval | queued -> running -> verifying
verifying -> delivered_pending -> delivered
verifying -> needs_revision -> queued       (bounded attempts)
any nonterminal -> cancelling -> cancelled
queued/running/verifying -> failed | needs_attention
```

执行、验收、交付三个维度分别记录；任务完成≠验收通过≠通知送达。没有执行器/模型配置时拒绝启动并明确原因；记录工单仍可用。取消时先撤销 ownership/工具授权，再停止运行；迟到回调不能复活已取消工单。

### 两种持久消息机制，禁止混为一谈

1. Task queue：任务只由一个有效 lease/epoch 的 Worker 领取。完成写回 CAS，租约到期或取消后旧持有者无权写入。不可安全重做的在途动作进入 unknown/needs_attention，不自动重复调用。
2. Event bus：事件不可变；每个 subscriber 有独立 cursor/lease/重试状态，至少一次投递。事件消费不能直接重复执行副作用。ACK 只能推进当前有效租约的事件，过期/旧 token ACK 拒绝。新订阅明确从 beginning 或 now 开始，重注册不能重置进度。

## 5. Router：从便宜到昂贵的门禁

按固定顺序：格式/大小/版本 → 来源认证/签名/时间窗口 → 账户/发送者白名单 → 外部 ID 去重/内容冲突 → 回环/机器人/自身回执过滤 → 限流/排队容量 → 指令/提及/线程规则 → 已有工单关联与授权 → 可选模型意图分类 → admission。

- ACK、回执、心跳、重复事件、明确拒绝和明确命令均不调用 LLM。
- 模型只给结构化建议；有 schema、超时、token 上限和置信度阈值；失败/歧义进入待澄清或人工，不默认执行。
- 规则配置有版本，决策记录 gate、reason、耗时与是否用了模型，不把消息全文和凭据写日志。
- 关联不能只看相同标题或相似文本。跨渠道绑定由用户明确建立，按 owner/account/thread 检查；反馈消息携带 origin/correlation 防止再被当新任务。

## 6. 数据与原子性

复用独立 Super SQLite，WAL、FULL synchronous、private dir 0700 / files 0600。不打开旧 Session schema 做业务迁移。schema 升级在事务中执行，仅接受明确支持的 application_id/version；损坏或未来版本拒绝，不静默重建。

目标表（按阶段迁移，不一次创建空壳）：

| 表 | 关键约束/用途 |
| --- | --- |
| work_orders + execution_runs | 业务意图及执行投影，保留已有工单/幂等记录；运行状态与 intake 不混淆 |
| super_inbox | UNIQUE(scope, account, provider_event_id)，相同 key 不同内容为冲突 |
| super_events | 全局递增 seq；UNIQUE(scope,event_id)；版本、topic、aggregate、因果链、时间、受限 JSON |
| super_subscriptions | UNIQUE(scope,name)，cursor、当前事件、lease token、epoch、deadline、retry_at、attempts |
| super_dead_letters | subscriber + event + 最终安全错误码，可审计，不隐藏丢失 |
| super_jobs / attempts | owner/epoch、deadline、根预算、依赖、有限重试、claim CAS |
| super_grants / approvals | scope/version、能力和预算快照、批准人/拒绝原因，不存密钥 |
| super_artifacts / verifications | 内容 hash、来源 run/attempt、验证标准及证据，过期证据不能通过 |
| super_outbox | target + logical message id 唯一；pending/sending/sent/retry/unknown/dead |

同一 SQLite 事务提交：接单状态 + 初始 jobs + domain event；取消状态 + ownership 撤销 + event；验收结论 + 交付 outbox + event。不可事务化的远端发送使用 outbox，远端不支持幂等且确认丢失时记 unknown，不承诺 exactly-once。

首版 event bus 每个 subscriber 一条在途消息，保证其顺序；不同 subscriber 并行。任务并发来自 task queue，不通过多个消费者争抢同一通知。慢消费者不会阻塞生产事务；生产只追加，不同步等待通知。容量达到上限施加 backpressure，不悄悄删审计或未消费事件。

## 7. Worker / Subagent / Verifier

- 复用 query.New / RunWithCallbacks 执行内核及可选 TextExecutor；Super 自有持久调度。审计发现 Team workflow.Compile 拒绝所有工具且仅静态文本 DAG，不能直接用作工具型 Worker 调度器；不引入其 MySQL Team/User 耦合，不写旧会话选择和生命周期。
- 根任务预算共享，先预留再调度，避免多个子任务各花全额预算；缺少实际成本回报视为 unknown，不计零成本继续花费。
- 有界 DAG 校验：无环、节点/深度/扇出上限、依赖齐全、工具/grant 子集；Planner 输出不是可直接执行的 shell 命令。
- 等子任务的父任务释放 Worker 槽位；避免两个 Worker 都等待自己的子任务导致死锁。
- 同一 workspace 默认单写者；并行只允许显式 disjoint write-set 或只读。不能未经用户允许创建 git worktree/分支。
- 工具动作仍走既有权限/审批机制；不能通过“后台 Worker”绕过 Computer Use 或高风险操作审批。
- Verifier 独立于执行结论：可信 argv 配置的测试/类型检查/构建、产物 hash、原生截图证据、必要时独立模型审阅。未知、缺失、过期证据失败闭合。LLM 评价不能覆盖真实失败的测试。
- 重启恢复重新确认权威 lease/epoch 和副作用状态；不能把旧回调当新 attempt 的结果。

## 8. 渠道与交付

首版 Adapter 集：Desktop（本地认证用户）、signed Webhook（HMAC+nonce/time-window、固定绑定 scope）、Feishu（复用现有 adapter，通过独立 Super inbound/outbound ports 接入）。单个外部事件只归一个 admission owner；旧聊天入口与 Super 入口使用显式 binding，不抢同一消息。

ChannelBinding 管理 allowlist、目标 thread、接单策略和允许回执类型；密钥引用 OS/key/config store，不落工单/事件。默认只回原渠道；跨渠道订阅通知须显式授权，工单关联不等于可转发敏感内容。相同内容不作为跨账户去重键。

消息链路：ack/clarification/progress/verification/delivery 有明确类型；progress 合并限速；terminal receipt 不丢。未知发送状态显式展示并人工核实，避免重复发消息/重复副作用。

## 9. Desktop 与可观测性

在现有 Super pane 中逐步增加：渠道/Router 状态、工单状态与因果时间线、Workers/Subagents、预算、审批、证据、交付回执/死信、暂停/取消/重试。保留普通会话入口和草稿；不新建用户必须访问的 localhost 页面。

Wails 返回限量快照与带 cursor 的事件页；广播只是唤醒，断线后按 seq 补读。前端不成为 bus 权威，不保存凭据，迟到响应不能覆盖新选择。能力缺失、配置未完成和真实执行失败明确显示，不用“执行中”掩盖未实现。

日志字段：component、action、scope、work_order_id、run/attempt_id、event_id、epoch、safe_code、duration_ms；消息内容/密钥不打日志。指标区分 router gate rate、LLM fallback rate、queue lag、lease expiry、retry/dead-letter、verification fail、delivery unknown。

## 10. 实现顺序和写入边界

所有步骤在 main 分块实现/测试/提交/push，不开分支/worktree。修改 legacy composition 须单独审核并更新精确 hash registry；不能放宽整个 baseline guard。

| 阶段 | 内容和主要路径 | 依赖 | 完成门禁 |
| --- | --- | --- | --- |
| A0 | 本方案 + 接口复用审计 + 两个独立方案 review + 修订记录 | 无 | P0/P1 方案问题清零；不留未决安全前提 |
| A1 | `internal/super/eventbus` + `sqlitestore` v1→v2 migration，intake事件同事务写 bus | A0 | restart/replay、ACK fencing、retry/DLQ、scope isolation、v1 data preservation、race |
| A2 | `internal/super/admission` / `router` + inbox/policy/grant契约 | A1 | 快路径零 LLM、签名/去重/回环/限流、取消授权、来源隔离 |
| A3 | `internal/super/execution` 接现有 workflow/store/executor；运行状态迁移 | A2 | 一个真实受限任务可执行/取消/恢复；旧会话 baseline 不变 |
| A4 | 多 Worker、多 Subagent DAG、共享预算和 workspace 排他 | A3 | 两个 Workers 并行、子任务 fan-out/join、无死锁、旧 epoch 结果拒绝 |
| A5 | 独立 verifier、有限返工、artifact manifest、outbox | A4 | 失败/过期证据不能交付；重复完成不重复通知；unknown 不盲重试 |
| A6 | Desktop + Webhook + Feishu adapter 接入与授权互通 | A2/A5 | 两种外部 transport 契约、真实渠道权限及回执验收，不冒充已认证 |
| A7 | 原生 Desktop 完整控制面及恢复/监控 | A1–A6 | 实际 app 点击、事件补读、状态/审批/证据/通知截图，旧会话回归 |
| A8 | 故障注入、真实模型/渠道、文档、发布迁移审查 | A7 | 完整矩阵通过后才称全部完成 |

## 11. A1 冻结候选接口（review 后实施）

`eventbus` 仅契约/校验；`sqlitestore.Store` 实现：

- `AppendEvent(ctx, Event) (Event, created, error)`：ID 幂等；同 scope/ID 不同内容冲突。生产者只记录最小业务引用。
- `RegisterSubscription(ctx, Subscription) error`：scope/name、topics、beginning|now、maxAttempts；已有同配置幂等，不允许重置 cursor。
- `ClaimEvent(ctx, scope, name, owner, leaseDuration) (Delivery, found, error)`：一次一个 event；lease token 和 epoch；过期可接管。
- `AckEvent(ctx, DeliveryToken) error`：核对 scope/name/sequence/owner/token/epoch 和未过期 lease；过期或旧持有者不能推进 cursor。
- `NackEvent(ctx, DeliveryToken, safeCode, retryDelay) error`：同样 fencing；达到 maxAttempts 时持久化 dead letter 并推进 cursor；否则保留当前事件，等 retry_at。
- `ReadEvents(ctx, scope, afterSeq, limit)` 与 `ReadDeadLetters(ctx, scope, subscriber, limit)`：有界结果、严格 scope。

新增时间使用 int64 毫秒。A1 初始 topics 为 `work_order.recorded` / `work_order.cancelled`；确定性 event_id 防重复；scope 固定为已认证单用户本地 scope，未来 adapter 必须由服务端映射授权 scope。payload 只有 work_order_id，不含目标文本。

集中默认上限：单 payload 16 KiB、每次读取100、最多32 topics/订阅、256订阅、100000 retained events、lease<=5min、maxAttempts<=10。普通接单超过软容量拒绝且回滚；取消走§17有界控制容量，不能被普通流量阻断；无静默修剪。消费/过期/退避使用可注入 clock 测试，生产使用 UTC。A1 不运行 Worker、不声称有多渠道总线接通。

A1 migration：保留现有 work_orders/work_order_events；新增 bus 表和版本元数据；旧 audit 按时间/sequence 回填最小事件，建立稳定 ID，重复打开不重放插入。旧工单/事件和幂等键不改变。取消/创建随后在同一事务写旧审计和 bus；A3 扩展状态时再按审核后的迁移消除重复 authority，不能提前伪造执行状态。

## 12. 验收矩阵与风险

```text
contract tests -> transactional store/race -> router/worker integration
 -> real bounded execution -> two channel transports -> native Desktop
 -> crash/lease/failure injection -> real configured model + recipients
```

必须覆盖：重复入站/内容冲突、跨 scope/thread 越权、bot回环、队列饱和、掉线重连、取消与完成并发、lease丢失、旧进程迟到写回、父等子死锁、预算预留并发、worker崩溃、磁盘/事务失败、发布成功但确认丢失、失败证据/旧证据、通知失败、重启全链路恢复。

实测指标报告观测值，不先编造吞吐/延迟承诺。必须证明确定性规则路径模型调用计数为0；多个workers是真的并发；subagent有独立执行记录；消息由bus和outbox实际投递，而非UI伪造。

最高风险：权威重复、预算透支、取消后仍执行、跨渠道泄漏、验收自证、未知副作用重放、同包标识误选原生窗口。每项都有拒绝/审批/unknown边界和测试。正式安装身份选择仍属发布迁移，不在本轮偷偷更换产品 bundle ID。

## 13. 独立 Review 记录

- 架构/可靠性 reviewer Turing：首轮 2 P0 / 3 P1；§17 R01–R05 修订后复核通过，无未关闭阻断。
- 安全/授权/验收 reviewer Laplace：首轮 6 P1；§16 S01–S06 修订后复核通过，无未关闭阻断。
- 两位 reviewer 均只读，不是本方案作者；已明确允许 A1 先实施。后续阶段须遵守§16/17，新增实现不能绕过验收门禁。
- A1 特别门禁：满配额取消、超限历史迁移、在途有效lease不可提前DLQ、crash claim计次、now注册水位并发、旧token拒绝、事务回滚。

## 14. 接口审计后的收敛决策（实现约束）

### 执行复用，不复用旧调度权威

- `query.New(client, registry, Options)` + `RunWithCallbacks(ctx, prompt, sink, callbacks)`：每 attempt 新建执行上下文；使用 `OverrideSystemPrompt` 固化 profile、显式 RunID、上限与 deadline。
- `runtimecompose.TextExecutor` 可作只读计划/文字审阅；不是通用工具 executor。`workflowapp.New` 依赖旧 GORM 模型，不注入 Super。
- `agentruntime.Run` 已有工具循环，但创建 task 失败会继续运行，后台 RunBackground 使用 WithoutCancel；首版不直接调用其脱离控制面的后台调度。Super 的 delegate tool 只登记子任务，不直接启动 goroutine。
- 空 allowlist 在旧 Registry 意味着无限制。Super 必须从空 Registry 显式添加授权工具并逐个 Guard；不暴露原 Agent/Task/worktree/任意 MCP/本地 hooks。不能把裸执行器当成 OS sandbox。
- 根预算在 Super SQLite 事务内预留每个 attempt 的最大 tokens/steps；执行回报结算。超时或丢失回报保留预留、标记未知，不把未知成本按零退还。配置一个 hard cap；worker/child 不各自复制完整预算。

### 渠道复用，不复用旧 Session 管线

- `channel.Adapter` 的 Start/Stop/Deliver/ResolveThread/Health 是传输 port；仅 Feishu 有真实实现，DingTalk 等枚举不等于支持。
- 不调用旧 `channel/runtime.Service`、SessionResolver、ConversationRunner。Super 注册账户决定 scope/account，外部 sender 需显式映射 owner；不将所有用户映射到固定 userID。
- Feishu Adapter.Start 复用其内置规范化（没有公共 Normalize）；先做已知 bot/self/outbound echo 拦截，动态获取 bot identity，再做线程解析。无法证明根/线程的消息 fail closed，不回落到 root。
- 新通知发送始终经 Super outbox；旧 create 没有应用幂等键且 sending 没有完整恢复，故不继承其 retry 算法。网络超时/崩溃期间发送，若不能确认 provider 幂等，置 unknown，禁止自动再发。
- 不用旧 SQLite fallback 的“路径派生密钥”。配置只保存凭据引用；secret 来自显式环境/本地私有配置，不进事件、数据库、日志和 Git。
- channel.Scope 不含租户；Super scope 必须来自可信注册和独立存储谓词，不能拿其 hash 当作完整租户边界。

### 协议与安全上限

- Grant 不得由外部 body/model 自行填写；Desktop 本地操作创建 profile/grant，外部绑定仅引用已激活 profile。
- Router 每次接单匹配 server-owned binding；指令 /run 创建，/status 查询，/cancel 撤销，未知自然语言只记录待澄清。可选 LLM 分类是配置开启的非权威建议端口，不默认花钱。
- 所有状态变更、审批、关联、重试都携带 scope+expected version。工作目录 realpath 后固定，验收文件拒绝 symlink/越界；产物 size/hash 与 attempt 绑定。验收 argv 由可信配置提供，不从模型输出执行 shell。
- 首版写工具只给指定 workspace 内的读/写路径能力；通用 shell/network/computer use 必须显式配置批准，不默认启动。权限快照不能声称能阻止获授权 shell 的全部系统调用。
- 子任务采用“计划 → 有界 DAG → 调度 → 汇总”模式，不在执行中的父模型循环里同步等待；根计划节点完成后释放槽位。动态 delegate 如加入，必须 checkpoint parent，父任务 waiting_children 不占 worker。
- 自动验收不认可 worker 自报；verification job 使用独立 verifier port，默认执行可信 argv/文件证据。没有验收策略即 needs_attention，不自动送达。
- 发送回执不等于人类阅读；cancel 不能撤销已发生外部动作，保留 unknown/receipt 并展示风险。

## 15. 分阶段可执行交付契约

| 阶段 | 文件边界 | 最小实现/测试切片 |
| --- | --- | --- |
| A1 | eventbus/*.go; sqlitestore/schema.go, bus*.go, create.go, cancel.go | 先写 v1 迁移/事务回滚、双 subscriber、scope/fencing/重启/DLQ 测试，再实现；go test -race ./internal/super/... |
| A2 | control/domain.go, router/*.go; sqlitestore/control*.go | 固定 Binding/Grant/Envelope/Decision 契约；格式/认证/dedup/bot/限流/指令逐层测试；接单+inbox+grant+job+event 原子事务 |
| A3 | execution/*.go; control/ports.go | Executor port，fresh query.Session，最小授权 registry，取消/超时/零工具/预算测试；真实模型配置才允许启动 |
| A4 | scheduler/*.go; sqlitestore/jobs*.go | 竞争 claim/renew/finish fencing，根预算 reservation，DAG ready/child result，2 workers 并发/取消/崩溃/共享 workspace 互斥测试 |
| A5 | verification/*.go, delivery/*.go; sqlitestore/result*.go | verifier 独立 job，产物 manifest/hash、失败有限返工、outbox；事务 rollback、unknown不重试、receipt持久化测试 |
| A6 | channelbridge/*.go; httpapi/外部签名handler | trusted account registry；HMAC/timestamp/nonces + Feishu adapter；授权绑定、回环、跨账户隔离、scope/thread重放、签名轮换测试 |
| A7 | desktop-v2/super_*.go; web/src/v2/super/* | 组合服务+Wails快照、事件页、profile启动/审批/取消/证据/渠道；focused UI/typecheck + build + 原生点击截图 |
| A8 | integration tests; docs/deployment/* | 故障注入+真实受限模型产物+独立测试验收+两传输互通+原生窗口；所有证据按性质标注，不把 fixture 当生产 |

每个切片验收后提交/push，账本记录 SHA、测试和剩余项。预先存在的未跟踪两份方案是本任务所有；原 niuma 不改。工具/模型凭据如缺失，仅阻塞对应真实验收，不阻塞无依赖实施；最终必须逐项声明。

## 16. 安全 Review 第一轮修订（S01–S06，P1 必修）

1. **逐操作授权 S01。** Principal 必须由 Desktop 本地身份或 server-owned binding 构造。首版授权粒度为独立 principal scope（不把多人装进同一可读 scope）；账户与外部 sender 的显式映射才能进入该 scope。read/status/events/artifacts/DLQ/cancel/retry 必须检查 owner，不能只检查 tenant。只有本地管理员能编辑 profile、批准 grant、建立/撤销目标绑定；外部用户只可对映射的本人工单接单、查询、取消。approve 持久记录 action+参数摘要+grant version+expiry+nonce，单次 CAS 消费；模型不能批准自己。scope/owner字段不接受外部请求覆盖。
2. **实时撤销 S02。** 有效权限=不可变快照上限∩当前启用绑定/授权∩父权限。claim/renew/工具调用/子节点派发/发送前检查 revision、撤销与到期；每个运行关联 grant digest，任何变更需重新授权，不能静默扩大。配置撤销必须触发运行 context 取消并阻断 outbox。外发默认只有状态/工单ID/安全结果摘要；源码、工具输出、文件和其他渠道上下文不默认外发。精确 target（provider/account/chat/thread/recipient）和 notice kind 均在授权内；link 不扩大可见数据域。
3. **入站签名和重放 S03。** Webhook v1 固定签名字符串为 newline 分隔的 version、uppercase method、固定 canonical path、account/key ID、timestamp、nonce、SHA256(raw body)；HMAC-SHA256 常量时间比较，UTC ±300s、64KiB body、128-byte nonce，拒绝含分隔符/非canonical path。nonce、account/event-id 和 account/message-id 分别持久唯一，内容不同冲突；并发/重启重放返回既有收据不再产生业务动作。旧 key 只在明确有效期内接收；nonce 不因轮换清空。Feishu 无法确定 bot identity 时 fail closed/health degraded；外部自填 origin 不豁免鉴权。
4. **执行时能力边界 S04。** 用 Go `os.Root`/root-relative 文件操作绑定已授权根目录，逐次检查允许路径和工具；不使用启动时 realpath 检查后裸 os.WriteFile。禁止 .git、Super 状态、凭据、验收策略/fixture 路径；写许可为显式路径集合，不自动授权整台机器。控制面内持久 parent/root/grant hash，claim/recovery 再验证。零工具 Registry 不能发现 hooks/MCP/Agent/Task。通用 shell/network/CU 不默认暴露；需要时必须有单独经用户批准的隔离执行器，不把普通子进程说成沙箱。
5. **独立验收 S05。** 首版自动验收采用控制面私有 profile 内的结构化断言（必需产物、内容/schema/结果约束）和只读内容快照；策略不可由 Worker 写入。Worker 输出只作候选，verifier 自行读取/计算 hash，并绑定 run/attempt/policy digest。默认不执行仓库 npm/make/shell；加入命令验收必须显式批准隔离环境、最小env/无凭据和可信 fixture。Worker 所能修改的测试执行成功只能算辅助证据，不能替代可信断言。截图必须带可信采集 receipt；上传图片不能升级为原生UI验收通过。
6. **贯通验收 S06。** 账本拆分 implementation / automated / native scripted / autonomous model / external transport。最终至少一例同一工单贯通真实接单、模型执行、多个worker/subagent、独立验收、原渠道回执与Desktop展示，并用 correlation/run/attempt/build/window/receipt/hash对应。手工注入/模拟provider只标测试，不计真实模型/渠道通过；缺配置为具体阻塞，不宣称全平台验收完成。

## 17. 架构/可靠性 Review 第一轮修订（R01–R05）

- **R01 / P0 控制容量。** 100000 是普通事件/新接单软上限；取消/撤权/结算属于已入站实体的有界控制义务，不受普通流量配额拒绝。A1 每工单至多一条取消事件（固定 ID/唯一约束）；仅受信内部取消事务可用控制通道，公共 AppendEvent 不可自行升级为 control。历史迁移完整保留所有 audit；历史超过软上限允许迁移/读取/取消，只背压新普通接单。后续每个根工单的最大事件/节点/attempt预先有界；新接单额度为后续结算预留空间。磁盘物理失败仍可能无法落盘，须本机立即停止新执行并取消在途上下文，报告故障，不声称凭“预留”能抗磁盘损坏。
- **R02 / P0 副作用与全局资源权威。** 所有副作用启动前在权威 Store 检查 grant/claim token/epoch/expiry；取消立即使后续操作失败。不可原子化的正在进行的文件/远端动作不能被撤回，保留 unknown/receipt。workspace 占用属于全 Store 全 scope 资源，不是 root 内字段；调度在同一数据库事务内验证互斥/领取。旧 owner 失租却不能证明停止时不自动释放写锁，不调度新 writer；进入 needs_attention，等受控停止证明或人工核实。读与写互斥；只读之间可并行；Verifier 对同一候选版本持有读取保护。并行写暂只支持明确分离的工作根，不用路径字符串猜测目录不重叠。
- **R03 / P1 消费者崩溃。** Claim 本身原子增加尝试次数；无NACK的过期claim同样消耗次数。达到上限后下一Claim在同一事务写DLQ并推进cursor，不再派发该事件。只可跳过不匹配topic；不能跨过未结算匹配事件。now 注册同一事务捕获scope水位；重注册校验固定配置，不改cursor。DLQ保留全event reference/safe code；重投必须新受控订阅/显式操作，不重置旧cursor冒充首次处理。
- **R04 / P1 顺序纠正。** A3 必须先有单worker持久claim、lease renew/fencing、取消watcher、资源互斥和预算预留，才能首次真实执行；A4只扩展多worker/DAG/children。§10/15中旧“复用workflow engine”的表述以§14审计后契约为准，不能接入文本DAG充当工具worker。
- **R05 / P1 模型请求边界预算。** 每一次实际 StreamMessages 调用前，原子预留根预算（消息/工具schema编码字节上界+固定framing reserve+max output），持久request identity；成功后按完整usage结算，无usage/超时保留预留。多轮、Planner、Verifier模型、返工与所有children共根预算。默认关闭隐式重试/fallback/自动压缩/额外发现，避免未经reserve的调用；若底层无法证明无隐式调用则preflight拒绝真实执行。reserve失败不调用网络；actual超出保守预留即budget violation并停止根任务，不继续花费。

### 物理存储的可维护实现

在逻辑域边界不变前提下，A2–A5可采用**有界根aggregate JSON + CAS revision**简化首版：grant快照、有限节点/attempt/verification/outbox同根原子更新。硬上限32节点、单aggregate 1MiB、列表100条；超限拒绝而非截断。不可变字段和状态机在store边界二次校验。inbox、binding、event/subscription与**全局workspace资源占用**仍独立持久，不可埋入单root规避跨root互斥。scope/owner/id/revision/status等索引列与JSON一致性验证；状态变更+事件+inbox收据在同一SQLite事务，禁止双写第二权威。采用该物理策略必须补并发CAS、全Store资源互斥和故障回滚测试。

## 18. Integration review closure (2026-11-03)

- **R06: failed roots with unresolved claims.** A completed failure is not evidence that every sibling process stopped. Expired outstanding claims must be durably classified as unknown, including when another node already failed. Recovery must not silently free unknown resources or admit a retry; the existing explicit local-operator stop resolution remains the only release path. Transition guards and real SQLite restart tests own this invariant.
- **R07: local operator across channel scopes.** An external scope with manual approval must not create invisible, unapprovable orders. The trusted native Desktop operator may enumerate and manage persisted scoped runs through a distinct administrative repository interface. Lookup must be unique; an ambiguous run ID fails closed. The principal is derived from the stored run, never from a caller-provided owner. Router/channel principals retain their existing scope-bound limits. Removing a current binding must not hide older work from cancellation/recovery.
- **R08: accounting is independent of output acceptance.** A complete, strictly validated usage report remains chargeable when output is truncated, empty or otherwise rejected. Such an output still cannot execute tools or satisfy a task. Typed diagnostics preserve only bounded numbers and enumerated stages; no bodies, reasoning, URLs, credentials or raw provider errors are durable diagnostics.
- **R09: cancellation settles every in-flight notice.** A first receipt cannot finalize a root while another notice is still sending. Accepted receipts survive cancellation; ambiguous outcomes stay unknown and are not automatically resent. Terminal state cannot be revived by a late response. SQLite concurrency/restart regressions cover this boundary.

These are refinements of the approved control-plane boundaries, not a replacement scheduler or a change to legacy Session semantics. Product acceptance remains split into automated regression, native interaction, real-model execution, and live external transport evidence.
