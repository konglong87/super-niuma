# Niuma 多 Agent 编排：代码调研与第一版架构方案

- 日期：2026-10-29（Asia/Shanghai）。
- 状态：**用户已于 2026-10-29 确认实施；架构调研正文作为基线保留，当前执行台账见同目录 `2026-10-29-multi-agent-delivery.md`。**
- 调研基线：`main` / `0a4f966`；开始时工作区干净；`git pull --ff-only` 返回 Already up to date。
- 目标：在不破坏单 Agent、现有模型四分区、Computer Use 和桌面宠物的前提下，把已有 Agent Profile / Team 扩展为可恢复、可观测、有明确权限边界的本地多 Agent 工作流。
- 仓库：`../niuma`。不创建分支或 worktree，不修改原始参考项目 `../go-e2e`。
- 事实标记：**现有**表示读到实际代码；**缺口/风险**表示源码分析，不代表已运行复现；**拟议**表示尚未实现。

## 0. 实施台账与确认门

### 0.1 本轮变更台账

| 项目 | 状态 |
|---|---|
| 预先存在的用户修改 | 无；开始时 status/diff 均为空 |
| 本轮唯一预期修改 | 本方案文档 |
| 业务代码、配置、迁移、密钥 | 不修改 |
| 调研 | 后端编排、任务、会话、存储、模型/工具边界、桌面前端并行只读核对 |
| 基线验证 | 见第 17 节；只使用现有单元测试及 fake/mock，不调用真实模型 |
| 方案交叉复核 | 后端与前端只读复核各提出 4 项修订，已补齐并复核通过；这是方案审阅，不是新功能验收 |
| 原生脚本验收 | 本轮未执行：没有新桌面实现，不构建或冒充最终版本验收 |
| 自主模型验收 | 未执行；真实付费模型压测不在本期范围 |
| 提交/推送 | 文档验证后单独提交并推送 origin/main；精确 hash 在交付报告中记录，避免自引用 |

### 0.2 后续按优先级排列的队列

| 优先级 / 切片 | 依赖 | 实施状态 | 自动化门禁 | 原生脚本验收 / 自主模型验收 |
|---|---|---|---|---|
| P0：确认本方案、权限与 MVP 范围 | 用户确认 | 等待确认，禁止开始业务实现 | 文档证据与范围核对 | 不适用 |
| P1a：版本化定义、权限编译、SQLite/SQL 持久化 | P0 | 未实施 | schema/版本分流/权限空集/事务/升级重开测试 | 暂无运行 UI；不调用模型 |
| P1b：本地 DAG 调度、状态、重试、取消、恢复 | P1a | 未实施 | fake executor、fake clock、race、故障注入 | 原生验收留到 P1d；不调用真实模型 |
| P1c：启动/详情/事件 API 与生产组合根 | P1b | 未实施 | HTTP + SQLite + 离线 provider 集成 | 无浏览器 fixture 冒充桌面 |
| P1d：Agent 基本编辑、模板入口、运行详情 | P1c | 未实施 | typecheck、组件、Playwright 离线 E2E | 新构建 `.app` 实点、原生截图；离线端到端 |
| P1e：MVP 综合回归与交付 | P1d | 未实施 | 单 Agent/设置/渠道/Computer Use 回归、必要全量 Go | 同一最终构建取消/失败/重启证据；自主付费模型验收暂缓 |
| P2：人工确认与安全检查点暂停/恢复 | P1e | 显式后置，不宣传为 MVP 已支持 | 审批身份/过期/重复批准/恢复测试 | 原生按钮、重启后审批恢复 |
| P3：受控 Computer Use / 图片执行适配器、转交 | P2 + 独立安全方案确认 | 显式后置 | 能力与授权隔离、不可重放副作用 | 原生能力验收，不放松既有安全门禁 |

每个切片：检查未完成需求 → 测试 → `git diff --check` → 以 konglong <konglong@com> 提交 → push origin/main → 核对 HEAD/远端；不能先开始无关下一项再遗留未提交实现。当前 Computer Use 队列继续以 `../niuma/docs/plans/2026-09-27-computer-use-product-functions.md` 为准；物理第二屏与正式签名/分发仍保持用户明确暂缓。

## 1. 核心结论与架构决策

1. **不是从零开发多 Agent。** 已有文件型 sub-agent、数据库 Agent Profile、Agent Team、任务与事件、Session Control、渠道 Worker、图片 Worker。应补编排语义和执行闭环，而不是再建 Agent 数据中心。
2. **Agent 复用 Profile；Workflow 复用版本化 Team。** Workflow 是产品/领域视图，落在 `agent_teams.schema_version=2`、现有 members 与新版 policy document；不增加重复的 `agents`、`workflows`、`workflow_runs` 主表。
3. **V1/V2 明确隔离。** V1 飞书 Coordinator/Parallel Review 保留原行为；V2 使用无渠道假设的 DAG validator / scheduler。未知版本拒绝，不静默解成 V1。
4. **调度不等于模型循环。** Scheduler 只做依赖、并发、持久状态和预算准入；Executor 复用现有 query/session/provider/tool 运行能力。模型不能决定调度器的权限。
5. **持久状态先于通知。** 状态、序号和控制事件在同一数据库事务提交；SSE/前端是可重连投影，不是事实来源。MVP 不引入 Kafka/Redis 队列、Temporal 集群或新数据库。
6. **首期文本、多步静态 DAG。** 链式、有限并行、依赖 join 共用一套调度器。默认无工具；内置离线示例优先采用 Planner → Researcher → Reviewer。Researcher 处理给定资料，不默认联网。
7. **不复制安全漏洞式兼容。** 不让空 allowlist 变为全权限，不沿用旧 Team 的共享会话标识，不把“DB 写 cancelled”当资源已回收，不把结果文本“成功”当运行成功。
8. **重启可解释，不承诺 exactly-once 外部副作用。** 能回放事件、恢复完成节点和待执行队列；不确定的在途动作标 interrupted，需要明确恢复策略，不自动重放。

## 2. 当前架构：可复用能力及实际缺口

以下路径/行号对应调研基线，不以历史方案代替源码事实。

### 2.1 Agent 定义与单 Agent 引擎

| 现有模块与源码证据 | 已有能力 | 本次使用方式 / 缺口 |
|---|---|---|
| `../niuma/internal/agents/agents.go:14,58,127` | 文件/插件/内置 Agent；名称、prompt、模型、工具 allow/deny、MCP、skills、effort、工作目录来源 | 继续服务 CLI/sub-agent；不是新桌面 Agent CRUD 的第二主库；未来可显式导入 Profile，首期不做双向同步 |
| `../niuma/internal/agentprofile/model.go:67`、`schema.go:33,113` | Profile 版本、发布/归档、独立 persona/system addendum、执行参数、工具、上下文、安全策略 | 复用为 Agent Definition；需补角色注册元信息与受控执行类型，不能用角色名驱动权限 |
| `../niuma/internal/agentprofile/resolver.go:13,73`、`applier.go:28` | RuntimePolicy 求交、blocked overrides、effective hash、Apply 转运行参数 | 复用解析与约束，不允许节点覆盖提升已解析权限 |
| `../niuma/internal/cli/agent_profile_wiring.go:76` | Profile → query options 接线，Session 工具特权按内置身份另行检查 | 提取/复用无渠道参数映射；构造全新节点 options，不继承父级 addendum/工具/历史 |
| `../niuma/internal/agentruntime/runtime.go:50,272,371,479` | 流式子 Agent、独立 transcript、任务、hooks、usage、超时、compact、provider 注入 | 复用能力与测试模式；普通子 Agent 强制移除 ComputerUse，不能简单解除禁令 |
| `../niuma/internal/agentruntime/limits.go:12`、`internal/agentbudget/budget.go:16` | 深度、后台数量、共享 token/cost 熔断 | 沿用限制并叠加 workflow 限制；不能把后台数量上限等同 DAG 全局并发上限 |

### 2.2 Team、Worker、任务、消息、事件

| 现有模块与证据 | 已有能力 | 缺口 / 边界 |
|---|---|---|
| `../niuma/internal/agentteam/model.go:31,38,210` | Coordinator / ParallelReview、MemberRunner、Mailbox、RunRecorder、运行事件常量 | 角色与模式目前有限；无任意静态依赖、node attempt、retry policy、approval 状态机 |
| `../niuma/internal/agentteam/orchestrator.go:89,130,244` | Worker 串行或 semaphore 并发后 Coordinator 汇总、run timeout、partial/cancelled/timed_out | 串行 Worker 均收到原始输入，不交接上游结果；Coordinator 后置，不是 Planner 先执行；并发为每成员起 goroutine，非固定池；MaxRounds 不是已实现循环 |
| 同文件 `:147,183,339` | token 事后检查、事件顺序号 | usage 依赖 adapter；事件 Append 错误被忽略，状态/事件非事务，不具备完整恢复保证 |
| `../niuma/internal/cli/channel_team.go:53,89,114,166,190` | 发布 Team 通过渠道入口运行，Profile → query，成员会话持久化 | loader 未按 schema 分流；成员 session key 不含 TeamRunID/attempt，RunID 固定 member key；adapter 未回传真实 Tokens，未完整执行 member tool/workspace/override；不可原样复用为安全边界 |
| `../niuma/internal/agentteam/validator.go:23` | 发布 Team、Profile、单 coordinator、bot account、触发绑定校验 | 强耦合渠道；V2 不能被要求伪造飞书 account/inbox |
| `../niuma/internal/agenttasks/agenttasks.go:20,46,125`、`controller.go:8` | ready/running/completed/failed/cancelled/timeout、流式 thinking/tool/usage/permission/question、TaskStore、进程内 cancel map | 复用任务与事件；Controller 不跨重启，必须接持久取消和执行认领；不把同一 Task 当多个 attempt |
| `../niuma/internal/server/handlers_agent_tasks.go:450`、`agent_task_reaper.go:146` | HTTP 任务执行、SSE、stale runner 失败回收 | message handler 不适合作节点原子启动接口；reaper 是失联失败回收，不是 workflow checkpoint 恢复 |
| `../niuma/internal/channel/runtime/worker.go:26,41,71` | 轮询、账户 lease、并发、Worker 生命周期 | 参考 lease/poll 模式，不复用渠道账号上下文；分布式 Worker 不是首期要求 |
| `../niuma/internal/channel/runtime/service.go:462,869` | TeamDispatch 与 pending 恢复 | 静态分析发现恢复 workItem 未带 TeamRoute；不能宣称现有渠道恢复已覆盖 Team 计划恢复 |
| `../niuma/internal/imagegen/scheduler.go`、`worker.go` | 专用图片队列、attempt、取消/持久化配套 | 后续图片 Executor 的边界，不并入普通文本重试循环 |

### 2.3 会话、持久化、幂等、上下文

- `../niuma/internal/sessioncontrol/ports.go:8,53` 与 `runtimecompose/runtime.go:58` 已有端口组合、scope 校验、operation key 锁、恢复与审计；**优先复用身份/幂等语义、持久化模式和 Handoff**，不新建一套通用 Session Control。
- 但 `../niuma/internal/sessioncontrol/types.go:44,75` 的 Create/Send 没有完整 Agent Profile/工具/工作范围快照。不能仅用 Model/CWD 直接当受限 Agent Executor，也不能让节点自动获得七个 Session 管理工具。
- `../niuma/internal/sessioncontrol/handoff/budget.go:10,63` 已有结构化摘要、去重、预算保护、aggregate context 比例；复用纯数据/预算逻辑。实际跨会话 Attach 需满足 ready target/scope 合同，不能为迁就 DAG 跳过校验。
- `../niuma/internal/storage/mysql/sqlite.go:20,50`：桌面使用同一 GORM Repository 的 SQLite 后端，已有 Profile/Team/Run/Mailbox/Task/Session/Image 表；**不是只有 MySQL，也不需要引入新 DB**。
- 需区分会话后端与管理库：`../niuma/desktop-v2/config.go:20` 默认聊天后端仍是 **jsonl**；`../niuma/desktop-v2/main.go:181` 向本地服务传 SQLite 路径，`../niuma/internal/cli/cmd_server.go:145` 装配 tenant/task/管理存储。新增 Workflow 运行使用现有 SQLite 管理库及独立执行会话，不切换/迁移用户的默认 JSONL 聊天后端。
- 但 SQLite AutoMigrate 当前没有 `gormAgentTeamRunEvent`；只有 MySQL `../niuma/migrations/mysql/000028_agent_team_run_events.up.sql` 定义团队事件表。需补迁移一致性与实际插入/重开测试，不是仅检查 migration 文本。
- `../niuma/migrations/mysql/000016_agent_teams.up.sql:81` 和 `../niuma/internal/storage/mysql/agent_profile_team_repository.go:525`：Run 要求 inbox/source account，存在对应非空 FK；新增桌面 source 必须迁移为可空并按 source 校验，不能用 0/虚假 inbox。
- 同 repository `:560` 的通用 update 写全量状态/时间字段，无 revision CAS；不能复用它处理 V2 取消/完成竞态。`:610` 事件与 run update 分开；Mailbox 有幂等 key，但旧 MySQLMailbox 把正文放 PayloadRef，不能视作“已安全存储 Artifact”。
- `../niuma/internal/tenant/agent_profiles.go:445,496` 的 Team hash/发布并非完整执行快照；V2 必须重新校验成员、固定版本，生成完整 plan/effective policy hash。
- 同文件 `:693` 的 CancelAgentTeamRun **仅改 DB status**，不调用 runner cancel；这不是实际任务终止保证。

### 2.4 模型路由与安全

- `../niuma/internal/config/config.go:61,174`、`internal/anthropic/client.go:111,194,313,1420` 已有主模型/备用 provider、流式 fallback、用户取消不触发 fallback、输出提交后不能任意换 provider 的边界。
- `../niuma/internal/agentruntime/override.go:46` 与 `provider_preflight.go:14` 支持显式子 Agent model/provider/effort/timeout，离线预检配置。该预检不等于完整 text/computer/image 能力注册。
- `../niuma/internal/tools/tool.go:416` 的 `FilterPolicy`：**空 allowlist 表示不做允许集过滤**。新编排层必须构造显式工具集，空集应返回真正空 registry；不能改变旧语义影响单 Agent。
- `../niuma/internal/tools/bash/bash.go:395` 与 `internal/procenv/procenv.go:69` 有子进程环境脱敏，但不构成任意 Bash/文件工具的秘密隔离沙箱。
- `../niuma/internal/telemetry/telemetry.go:612` 按敏感键过滤遥测；这**不能**保证任意 `content`/错误/工具输出中的秘密值已脱敏。新的事件/Artifact 序列化边界还要专门保护。

### 2.5 前端与桌面结构

- 桌面真实前端来自 `../niuma/web/src/v2`；`../niuma/scripts/build-desktop-v2.sh:15` 构建 `web` 后复制到 desktop-v2/frontend/dist；后者不是第二套应编辑的 React 工程。
- `../niuma/web/src/v2/routes.ts:3` 已有 settings profiles / agent / teams；`SettingsCenter.tsx:150` 挂接已有 AgentTeamsPanel，ProfileSettingsPanel 挂接 AgentProfilesPanel。**Agent 列表/编辑和团队模板管理已有，不应重新造完整页面。**
- `../niuma/web/src/components/TeamRunObservability.tsx:364` 已有运行时间线/成员信息呈现；需要升级为节点/attempt、输入/输出/usage/错误/重试投影，不另做一套日志查看器。当前 Team 页面仅打开/刷新读取，没有 Run 实时订阅。
- `../niuma/web/src/components/CollaborationRunConsole.tsx:283` 的 replay adapter 用当前 Team/members/profiles 拼 snapshot，固定 has_more=false；`../niuma/web/src/components/AgentTeamsPanel.tsx:87` 吞掉 timeline 获取错误。V2 必须读取服务端不可变快照、真实分页并显式显示读流失败，不能原样复用此数据 adapter。
- `../niuma/web/src/components/AgentTeamsPanel.tsx:49,166` 的旧列表/builder 未按 schema 分流，会读取 policy.orchestration；V2 Team 不能直接进入旧 builder。保存旧记录本身保留 schema_version，不应误判为总是强制降为 V1。
- 当前 V2 route 没有独立 workflow/run 详情类型；需最小新增两个路由类别，不改 session ref 语义。
- `../niuma/web/src/v2/settings/ModelSettingsPanel.tsx` 及 ModelQuickSetup/ModelProviderSettings/ModelComputerSettings/ModelImageSettings 是现有四分区。**不改版、不复制配置、不迁移密钥、不删除 fallback/Computer Use/图片配置。**
- `../niuma/desktop-v2/local_service.go:24` 管理本地服务子进程；HTTP 服务组合根接入 scheduler 生命周期，Wails 继续复用 HTTP API，不在 Go/Wails 再实现一份调度。

## 3. 目标架构与模块边界

```text
Desktop/Web：已有 Profiles + Teams + 新 Workflow 列表/Run 详情
        │ 已有 tenant/auth API client + GET snapshot + cursor SSE
        ▼
HTTP handlers（参数/鉴权/DTO）
        ▼
Workflow application service（启动幂等、编译快照、取消、恢复命令）
        ├── AgentProfile resolver + Type registry + Capability policy compiler
        ├── Team V1/V2 schema dispatcher → V2 Workflow compiler（纯函数）
        ├── RunStore（GORM SQLite/MySQL，CAS + 状态/事件事务）
        └── LocalScheduler（ready queue + bounded workers + clock）
                 ▼ Executor port / EventSink
           ProfileTextExecutor（runtimecompose，现有 query.Session/factory）
                 ├── 既有 provider/client fallback
                 ├── 既有 tool registry + 强制 PolicyGuard
                 ├── 既有 task/session/transcript/compact
                 └── 后续 ComputerExecutor / ImageExecutor（首期不启用）
```

### 3.1 放置原则（拟议，文件名可在实现前按现有拆分微调）

| 层 | 责任与候选文件 |
|---|---|
| Agent 定义 | 扩展 `../niuma/internal/agentprofile/model.go`、`schema.go`；新增 `types.go` 注册 RoleType/ExecutionKind 描述，不含调度分支 |
| Workflow 纯领域 | 新增 `../niuma/internal/agentteam/workflow.go`、`workflow_validate.go`、`workflow_compile.go`；V1 不塞入新 if/else 大函数 |
| 状态与调度端口 | 新增 `../niuma/internal/agentteam/run_state.go`、`ports.go`、`scheduler.go`、`retry.go`、`recovery.go`；类型只表达语义，不依赖 Gin/Wails |
| 上下文/权限 | 新增 `../niuma/internal/agentteam/context.go`、`execution_policy.go`；复用 profile resolver、Handoff 预算与 tools guard |
| 生产执行组合 | 新增 `../niuma/internal/agentteam/runtimecompose/`；注入 query/session factory、模型解析器、执行器、cleanup；CLI 组合根适配私有 options，不由领域层 import cli |
| 持久化 | 现有 `../niuma/internal/storage/mysql/` 新增 workflow run/node/attempt/transaction 文件，扩展 SQLite 与有序 MySQL up/down migration；不把 repository 全部塞入原大文件 |
| API | 扩展 `../niuma/internal/server/agent_profile_services.go`，新增 workflow/run handlers；`tenant` 做 scope/用例适配，不重新实现调度 |
| UI | 复用 AgentProfilesPanel / AgentTeamsPanel / TeamRunObservability；新增 `../niuma/web/src/v2/workflows/`，API/types 与 router 只做接线 |

接口采用小端口：`WorkflowCompiler.Compile`、`Scheduler.Start/Stop`、`Executor.Execute(ctx, ExecutionRequest, Sink)`、`RunRepository.CreateOrGet/Claim/Transition/RequestCancel/ListEvents`、`Clock.Now/After`、`CapabilityResolver.Resolve`。同步等待是 application service 在同一持久 Run 上 Wait，异步是 Create 后返回 202；不做两套引擎。生产先仅开放异步 API，单元测试可同步驱动。

### 3.2 Go 包依赖方向（必须无环）

现有 `agentteam` 的 MySQLMailbox/MySQLRunRecorder 与 `agentprofile/mysql_source.go` 已 import `storage/mysql`，不能把它们假定为完全无存储依赖的纯包。**采用外层 adapter 映射方案**：新领域端口/请求结果在 agentteam，新 GORM 方法只接受 storage 自己的 record/DTO；`agentteam/runtimecompose/RunStoreAdapter` 显式转换两者。

允许方向：组合根/`agentteam/runtimecompose` → agentteam、agentprofile、query、storage/mysql；agentteam 旧 adapter → storage/mysql；agentprofile 旧 source → storage/mysql。**禁止 storage/mysql 反向 import agentteam/agentprofile/runtimecompose；禁止 agentteam import 自己的 runtimecompose。** 新 scheduler/compiler 不调用旧 MySQL adapter，不为此任务大规模搬动旧包。存储与领域各有少量 DTO 由一个 adapter 转换，不让 SQL 模型泄漏到新 scheduler 端口。

编译门禁：新增 adapter 的 focused Go test、`go list -deps`/相关 package compile；若新增 import 引入环，先修边界再继续，不靠复制调度器或 interface{} 绕过。

`Scheduler` 可替换；未来远程 Worker 只替换 Executor/claim transport，执行 DTO 从一开始不含进程指针、provider secret 或本机 cancel func。分布式队列、跨机文件传输、心跳协议暂不实现，不能仅凭接口声称已支持分布式。

## 4. 核心领域模型

| 模型 | 拟议字段与权威存储 | 不变量 |
|---|---|---|
| Agent / AgentDefinition | 复用 Profile：key/version/name、role_type、system/persona、model/provider ref、ExecutionKind、工具声明、workspace/context policy | 固定版本；角色只提供默认模板/展示，不提升权限；不存 provider credentials |
| AgentTypeDescriptor | key、i18n label、description、默认提示模板、默认输出约定 | registry 注册 Planner/Researcher/Coder/Reviewer/Executor/General；新增类型不改 scheduler switch；未知类型拒绝发布，不静默赋全权 |
| ExecutionKind | typed `text` / `computer` / `image`，与 RoleType 分离 | 新执行能力必须有注册适配器+预检；MVP 只启用 text |
| Workflow | Team schema V2：key/version、nodes、outputs、limits、default retry/context policy | 静态 DAG；依赖存在、无环；同 Agent 可被多个独立 node 引用 |
| WorkflowNode | node_id、agent_ref（Team member key）、depends_on、input bindings、timeout、retry、tool/workspace 限制；编译快照另存解析后的 profile_ref/hash | Profile 绑定以 member 的固定版本为唯一配置来源；同 Agent 可用于多个 node；只消费声明依赖产物，不能越权 |
| Run | 复用 TeamRun：UUID、owner/scope、source、request fingerprint、plan snapshot/hash、status/revision、deadline、cancel flag、next_event_seq、lease epoch | 同幂等键+同 fingerprint 返回同 Run；终态不能被旧 worker 覆写 |
| Task / NodeRun | 新 node 状态行；run/node、dependencies、current_attempt、状态、输出 refs | 一个 workflow node 是一个逻辑 Task；不同于一次引擎调用 |
| Attempt | run/node/attempt_no、engine_task_id/session_ref、model route、epoch、start/end、usage、failure、安全重试标记 | **一次 attempt 对应一个独立 tenant_agent_task**；retry 不改旧 attempt 历史 |
| Message | 复用 Mailbox 的受控边交接：run/from/to、sequence、schema、artifact refs、去重 key | 不当通用聊天总线；非声明边不得互发；正文不再冒充 PayloadRef |
| Artifact | run/node/attempt、kind/media/ref/hash/size、摘要、访问 scope | 输出与大内容分离；仅授权依赖可读；二进制优先复用 media；新增最小文本 artifact 存储 |
| Approval | 后置实体：run/node、request hash、授权人、decision/expiry、revision | 精确绑定批准的输入/policy；重复批准幂等；内容变化需重新批准 |
| ExecutionEvent | 复用 TeamRunEvent：schema version、run/node/attempt/trace、sequence、kind、redacted payload | 持久单调序号；投影幂等；token usage 不放遥测敏感字段绕过滤器 |
| RetryPolicy | max_attempts、backoff、retryable_codes、replay_safety | 不跟 provider 内部 fallback 相乘失控；同一 attempt 的 provider 请求另记统计 |
| Cancellation | run cancel_requested_at/reason/actor + node ack/cleanup | 请求取消与已取消分离；取消提交后不可新发节点/重试/备用模型请求 |

Agent 新元信息采用版本化文档扩展；旧 Profile V1 解码/规范化/hash 保持原行为。新增 Profile V2 才携带 role/execution kind/workspace 约束；数据库仍使用同一 Profile 表。新编排允许引用 V1 Profile，但必须经 V2 policy compiler 转成更严的执行快照；不直接修改旧 Profile 的权限。

## 5. Workflow / DAG 执行模型

### 5.1 模式归一化

- 顺序 A → B → C：三节点依赖链。
- 并行 A/B：两个 ready 节点，共用 Run 并发额度。
- 依赖 join：Reviewer.depends_on = [worker-a, worker-b]；全部 required 上游成功才可 ready。
- Planner → Workers → Reviewer：Planner 先形成有限任务说明，Workers 静态预定义；**Planner 不动态创建节点、不自行选工具权限**。多个 worker 从 Planner 的结构化输出中读取各自声明字段。
- MVP 必交付两个可从 UI 运行的内置模板：三节点 Planner → Researcher → Reviewer，以及 Planner → [Researcher-A, Researcher-B] → Reviewer。第二个模板直接覆盖有限并行与 join 原生验收，不依赖用户手工拼图。
- 没有任何条件分支、循环或嵌套子 workflow；schema 不预埋可执行脚本。未来变体通过 compiler/模式 registry 扩展。

### 5.2 调度算法

1. 启动前编译 DAG；检查版本、Profile 可见性、节点/边数量、循环、权限和模型能力，形成不可变快照。
2. 单事务创建 Run、NodeRun 与首批事件，事务外唤醒 scheduler；即使唤醒丢失，轮询也能发现 queued Run。
3. 用稳定拓扑顺序维护 ready queue；固定 worker 取得进程/Run 执行额度后，仅取已满足依赖且未取消/过期的节点；不能先把无执行额度节点批量标 running。
4. 认领时 CAS node revision + run epoch，在同一 repository 事务创建 Attempt、预备 engine Task 身份和 task_created/认领事件；agent_started 仅在实际进入 Executor 时记录，不得先调用模型再落库。Executor 采用这个 Task（prepared/adopted task），不再另建第二个同名任务。会话预备使用唯一 execution key 可幂等重取；若现有 query factory 不支持 adopted Task，则先加最小内部接线及测试，不能绕过此原子性门禁。
5. 使用已取得的进程总额度 + Run 额度执行。固定 worker pool，不为所有等待节点起 goroutine；重试 backoff 不占执行 slot。
6. attempt 结果必须同时检查 Go error、引擎终态、context、persistence health；纯文本“完成”或 `err=nil,status=failed` 都不代表成功。
7. 由 RunRepository 的唯一 CommitAttempt 事务写 engine Task/Task 终态事件、Attempt 终态、Artifact 元数据、Node 状态、Run 必要事件，并释放/结算预算。已完成的依赖快照不会因重试另一个节点被修改。
8. 必选节点失败则阻断下游；等待已经运行的兄弟节点取消/清理后 Run 才终结为 failed。MVP 不做“失败了仍标 partial 成功”。
9. 仅全部 required nodes 成功且最终结果/事件持久化成功，Run 才 completed。Worker panic 在 adapter 边界转结构化失败，回收 slot、资源和 lease。

### 5.3 拟议默认上限（集中常量 + 服务端硬上限；不是现有事实）

| 限制 | MVP 默认 / 硬上限建议 |
|---|---|
| 每 Run 节点数 | 8 / 16 |
| 每 Run 唯一 Agent 数 | 8 / 16 |
| DAG 最大路径深度 | 8 / 16；与既有 sub-agent recursion depth 分开 |
| 每 Run 并发 | 2 / 4 |
| 进程总执行并发 | 4 / 8；涵盖本编排所有 Run，不能被每 Run 独立额度绕过 |
| 同一用户活跃 Run / 待执行 Run | 2 / 20；超载返回明确错误，不堆无界队列 |
| Run 总时限 | 10 min / 30 min；含等待/重试，审批阶段也不能无限挂起 |
| 单 attempt 时限 | 120 s / 300 s，且不超过 Run 剩余时限 |
| Retry | max_attempts=2 / 3；**包含首次**；1s 指数退避、上限 10s，可注入时钟 |
| nested agent tool | MVP 默认禁止；未来启用仍受既有 DefaultMaxSubagentDepth=2 及共享预算限制 |

不把硬限制交给 LLM 修改。总 token 预算使用准入预留与实际 usage 结算（保留 input/output/cache 维度）；未知费用显示 unknown，不伪装 0 元。硬 monetary cap 不宣称精确限制 provider 未返回/额外计费；限制并发、attempt、输出 token、wall-time，披露边界。

## 6. 状态机与控制语义

### 6.1 Run（MVP）

```text
queued → running → completed
   │        ├── failed
   │        ├── timed_out
   │        ├── interrupted（进程失联/未确定副作用）
   └────────┴── cancel_requested → cancelled
interrupted → queued（显式恢复命令 + 安全检查 + epoch/revision 更新）
```

- cancel_requested 是持久意图，不是“已停”；收到后禁止任何新派发。运行中节点全部 ack/安全隔离后才 cancelled；清理不确定则 interrupted，并显示原因。
- cancelled/failed/completed/timed_out 为不可逆终态；重跑创建新 Run，节点 retry 只能在当前 Run 非终态且策略允许时产生新 Attempt。
- completed 与 cancellation 并发由同一事务/CAS 决定：先成功提交终态的结果胜出；后来的取消返回已终结，不重写为 cancelled。取消先提交则迟到结果只记 attempt 事实，不能将 Run 标 completed。
- interrupted 不自动算成功；恢复复用已成功节点，不能把整个 workflow 当全新请求隐式重跑。

### 6.2 NodeRun / Attempt（MVP）

`pending(dependencies)` → `ready` → `running` → `completed`；失败可进入 `retry_wait` → `ready`，或 `failed/timed_out/cancelled/interrupted`；上游终败则 `blocked`。状态与显示 phase 分开：thinking / tool_running / waiting_dependency 不作为多个互斥持久主状态。

Attempt 终态不可变；持久化 `(run_id,node_id,attempt_no)` 唯一约束。恢复后的 worker 结果必须通过 epoch/attempt/revision 认证，旧结果不能完成新 attempt。

### 6.3 workflow-owned Task 的终态权威与旧入口隔离

Attempt 表对 engine_task_id 施加唯一绑定；Task 附 owner metadata 仅便于展示，权限判定必须查不可由普通 PATCH 修改的 Attempt 绑定。**Task/Attempt/Node/Run 控制事件的唯一终态提交者是 orchestration service 的 CommitAttempt 事务。** Executor 只提交结果提案，不能调用旧 FinishAgentTask 独立完成任务；workflow TaskStore/Controller adapter 只允许带 run/node/attempt/epoch 的安全 progress，不让旧 runtime 自行落终态。

旧 Task PATCH/message 不得重启或改写 workflow-owned Task；cancel/Session Control Stop 若命中该绑定，转交 Run cancellation（MVP 不提供独立取消节点），或明确返回 workflow_owned_task_conflict，不能绕过 Run 状态机。只读 Task/Get/Event 仍按 scope 提供。stale reaper 对此类 Task 不直接写 failed，交 workflow recovery 判定；裸 Task.completed 不是恢复成功依据，必须存在匹配 epoch 的 CommitAttempt checkpoint/Artifact。

验收加入旧 PATCH/message/cancel/Session Stop/reaper 与 CommitAttempt 并发，以及 Task 结果、Node、Run Event 同事务回滚。现有 `../niuma/internal/storage/mysql/gorm_repository.go:2106` 的无 CAS FinishAgentTask 仅留给原流程，V2 不复用其终态写法。

### 6.4 人工确认、暂停/恢复（P2，不伪装成 MVP 能力）

- 审批是显式 workflow node 或节点前 gate，进入 waiting_approval；approved 才进入 ready，rejected/expired 产生明确终态。复用已有 permission/question 展示与 broker 模式，但持久化独立 Approval，不能把内存 channel 当恢复依据。
- Pause 为安全检查点暂停：暂停新派发，等待在途只读节点收尾后 paused；不是挂起任意模型 TCP 或 OS 操作。等待超出 stop grace 则 interrupted。
- Resume 重新校验 actor、policy hash、deadline、资源与 capability；恢复已有节点，不从头重放工具。
- P1 UI 不放无效“暂停/批准”按钮；通过 capability 字段决定按钮可见性。若用户要求第一期也交付审批/暂停，需先调整范围与门禁再实现。

## 7. 消息、上下文与长任务

### 7.1 ExecutionEnvelope

新节点输入只包含：Run objective、用户约束、node instruction、明确绑定的输入、直接依赖的输出摘要/ArtifactRef、来源和可信级别、必要恢复 checkpoint。

- 系统层：平台固定安全合同 + 当前 Agent 的已解析 system/persona。上游输出永远作为带来源的 user/tool 数据，不进入 system 提示词。
- 默认隔离：每个 node/attempt 新会话、新 message slice、新 options/registry 视图；ID 使用 run/node/attempt（或 UUID），不沿用 `team:{conversation}:{member}`。
- 共享仅限：显式全局目标/约束、声明依赖产物和只读 workspace snapshot；不广播所有 Agent transcript、不自动共享租户/用户记忆、不继承父 Agent API Key、MCP 配置或系统 addendum。
- 输出含显式摘要、结构化结论、ArtifactRef 与未决事项。引用 Artifact 需 scope + 内容 hash 校验；未授权节点不能凭猜测 ref 读取。
- Agent 间输入视为不可信数据；引用中的指令不能扩大工具权限，也不能由 Reviewer 替其他 Agent 批准动作。

### 7.2 预算与压缩

复用 Handoff 的目标/约束/证据保护、重复项去除、单包/aggregate budget 算法；aggregate limit 使用其 `AggregateBudgetLimit`（目标窗口 15%，当前上限 6144），窗口来自既有配置/受控 model metadata，不假定所有模型同一窗口。编排层总 prompt 还必须计入当前 Agent system、工具 schema、输入及输出预留。

没有可信 context window 时使用保守配置下限并在 UI 解释；无法保留目标/约束/证据时显式 context_budget_exceeded，不静默截掉安全要求。大文本、文件、图片存 Artifact，只发送有上限的摘要/引用；后续读取仍走权限。按 dependency output hash 去重，同一摘要不每轮重发。

长运行沿用现有 compact 与 transcript；跨节点首期只做确定性摘要骨架/截断和结构化输出，不额外调用收费模型压缩。Compressor/Memory 为后续可选端口，不引入向量数据库、全局自动记忆或无界聊天历史。

## 8. 模型路由与工具权限

### 8.1 模型路由

有效优先级：允许的 node 执行 override → 固定 Profile model/provider → Run 启动时的全局主模型公开配置快照。由服务端解析已存在的 provider 引用，UI 不提交 raw API Key。

- 全局主模型/备用模型设置仍是唯一配置来源；不向 Profile 复制密钥，不改四分区。
- 明确 Provider 时只能使用该 provider 已配置且能力匹配的模型；不能把 A 的任意 model 名配给 B。Fallback 候选也必须过 kind/tools/vision/context 预检；不自动探测所有能力。
- 复用现有 Client 的 transient fallback 和提交输出后的限制；取消不触发 fallback。attempt retry 只针对已耗尽 provider 内部重试后仍安全可重试的结果。
- 数据快照存 provider name、model、公开参数、路由版本，不含 secrets 或 secret-derived hash。凭据只在 adapter 内即时解析；失效报 credential_unavailable，不输出原始 provider 响应头。
- `capability_mismatch`、`provider_not_configured`、`model_not_supported`、`context_budget_exceeded`、`credential_unavailable` 为 typed error；UI 说明缺失的能力，不给出“随便换模型”的误导。
- text/computer/image 是不同 Executor 能力，不是角色。P1 对 computer/image workflow node 在发布/启动预检时清晰拒绝；既有 Computer Use / 图片单 Agent 流程不受影响。

### 8.2 权限编译与逐工具防线

`effective_tools = platform capability ∩ actor/workspace policy ∩ Profile allow ∩ node restriction`，再减各层 deny。模型路由/role 均不能增加权限。

- 编排 P1 空 allow 是空 registry，`*` 默认拒绝用于 workflow profile；不能直接把空 slice 交给旧 `FilterPolicy`。工具 schema 注册和每次 dispatch 都做检查，防止 hooks/skills/MCP/工具别名绕过。
- 默认禁用 Agent/AgentMessage/SendMessage、Session 管理、MCP 和动态 skills；未实现受控委派前，不能让 Worker 自建隐藏调用树。
- **生命周期 hooks 不是工具，空 registry 不能阻止其执行命令。** `../niuma/internal/cli/cli.go:726,826` 的 factory 会构建/运行 hooks；query 还可执行 UserPromptSubmit/Stop。P1 必须在 factory 任何初始化副作用之前注入不可被 Profile/node 覆盖的受限 runtime policy：RunHooks=false，workspace/plugin/skill/agent/MCP 自动发现、StartupUpdate、BackgroundEnrichment、自动 memory root 全关，并传空 hooks runner。不能仅选已有 bare 就宣称无工具：bare 的最小集合仍包含 Read/Edit/Bash，必须额外构造明确空 registry。
- 受限 policy 在实际 query 运行、compact/Stop/cleanup 仍保持；测试配置带 SessionStart/UserPromptSubmit/Stop/PreCompact 命令 sentinel，确认从创建到收尾均无命令/自动网络执行，不只断言工具列表。
- 初始三角色模板不需要 Bash/任意文件/联网工具；只消费用户提供的资料与受限 Artifact reader。Coder 若未来需要文件修改，单独设计 workspace guard 与并发写冲突，不在本期用“角色=Coding”隐式授权。
- 工作范围在服务端取 canonical realpath，拒绝越界、符号链接逃逸、任意绝对路径、未绑定 workspace；工具命令字符串前缀校验不能替代 OS sandbox。P1 不开放任意 Bash，因此不声称已有路径策略可防任意命令外泄。
- secrets/config/credential 目录在 workspace read/write 中强制 deny；Artifact reader 无法读配置；provider credentials 永不进入 prompt/events/DTO。复用子进程环境清洗，但不把它当作完整沙箱。
- Computer Use 后续必须显式授予 capture 和 desktop_action 两个能力，绑定用户授权、目标窗口和当前 runtime；普通 sub-agent 的禁止继承规则保留。图片路由仅接图片服务，不授予桌面操作。

## 9. 事件与可观测性

### 9.1 事件目录（类型/常量集中定义）

沿用已发布 Team/Task 事件值，新增 typed V2 事件并版本化 envelope，不到处拼字符串：

| 语义 | V2 类型建议 | UI |
|---|---|---|
| 编排受理/开始 | run_queued / run_started | 队列/运行中 |
| 节点创建/等待依赖 | task_created / task_waiting_dependency | 依赖 chips、未完成节点 |
| Agent 开始 | agent_started | 当前节点高亮、attempt |
| 输出/工具/usage | agent_output_delta / agent_phase / agent_usage | 正在生成/工具执行；不展示私有思维链 |
| Agent 完成/失败 | agent_completed / agent_failed | 摘要、耗时、错误码、输出 refs |
| 重试 | task_retry_scheduled / task_retry_started | 第 N 次、原因、backoff |
| 人工确认（P2） | approval_requested / approval_resolved | 待确认与授权按钮 |
| 取消 | run_cancel_requested / task_cancelled / run_cancelled | 停止中与已取消分开 |
| 终态 | run_completed / run_failed / run_timed_out / run_interrupted | 不以 generic run_finished 掩盖失败 |
| 恢复 | run_recovered | 恢复说明、保留节点/需重试节点 |

### 9.2 数据流与恢复

1. 控制/终态事件和相关 state mutation 同事务；per-Run sequence 由 DB revision/next_seq 分配，不能仅靠进程 mutex。
2. 高频 delta 合并小批写入，有序 flush；终态事务前必须处理已接收 delta。允许丢失明确标记的未提交增量，但最终 Artifact/状态必须一致；流不完整显式 stream_gap。
3. SSE 推送只读取已提交事件；无需另建消息中间件。进程内通知仅加速，轮询补漏。GET snapshot 返回 last_sequence，随后订阅 after_sequence，重复事件按 sequence 去重，断线补历史。
4. UI 状态以 snapshot + ordered reducer 为准，不靠“最后一段文字”推断成功。run/node/attempt 维度查看输入摘要、完整授权产物、耗时、usage、route、error、retry。
5. 复用 observability trace/span、Task thinking/tool/usage 事件适配；thinking 仅显示状态/时长，不要求或持久化模型私有思维链。没有 delta 时显示“等待模型响应”，不伪称“思考中”。
6. SSE 有限 replay window、分页/心跳、慢消费断开可重连，不能无限积压内存。retention 后 cursor 过旧返回 typed cursor_expired，UI 重取 snapshot。

### 9.3 V2 RecordingPolicy：不能只在 SSE 上脱敏

现有 `../niuma/internal/query/query.go:1490,2697,2721` 会独立向 Recorder 写原始 user/assistant/tool/error、thinking/signature/provider continuation，且多处忽略 Append 错误。它绕过 workflow EventSink，**不可原样接入 V2**。

P1 选用明确的 restricted recording 路径：factory 在任何 Recorder 创建/首次写入前应用 workflow-only RecordingPolicy，关闭默认 raw transcript/continuation 持久化、promptdump、自动 memory 写入，query 的原始 Recorder 保持禁用；不能仅传 nil 然后被 factory 自动创建。原流程默认不变。需要保留的用户输入、可见正文、工具结果/错误、Task result 由 workflow adapter 的安全 journal/EventSink 先脱敏再保存；thinking/signature 只在内存完成本次 provider 协议，既不落盘也不展示，重启后不得依赖它们续跑在途请求。

持久化分两类：**恢复必需**的 Run/Task/Attempt/checkpoint/Artifact/控制事件写失败，必须阻断 completed、上报 persistence_unavailable；若 DB 整体不可用，不声称已写入 interrupted，待存储恢复后依据已提交的 claim/Attempt checkpoint 收敛到可核对状态；**可选诊断**的额外安全 transcript 若启用，失败可降级为 diagnostic_degraded，不伪装丢失主结果。安全 journal 汇总首个写入错误返回 Executor，不依赖旧 query 忽略错误的 Append。MVP 可以不生成额外 transcript 文件，以 SQLite 事件/Artifact 为恢复依据。

测试直接读取临时 transcript/Task result 文件与数据库，确认没有秘密 sentinel、thinking/signature/provider 原文；故障注入覆盖写入/flush/close，区分必要记录与可选诊断。不能只检查 API/SSE 看起来已脱敏。

### 9.4 脱敏

所有 provider errors、tool output、Message、Artifact、输入/输出展示在**写入持久层前**经过 workflow redaction boundary；不存请求头、auth、模型完整配置、进程环境。敏感键拒绝 + 已配置秘密值匹配 + 常见凭据形态过滤，多 delta 跨块 secret 用有界滚动匹配，前端/日志走同一安全 DTO。

已存在 telemetry sanitizer 仅用于统计，不拿它处理完整任务正文。秘密值匹配器不把原值放日志或错误。对任意用户文本的“绝对脱敏”不可证明，因此关键防线是工具/数据源隔离与最小权限；测试覆盖直接值、嵌套 JSON、错误体、跨 delta、URL query、case/encoding 常见形式。输出中禁止可执行 HTML，遵循现有安全 Markdown 渲染。

## 10. 持久化：沿用 SQLite / MySQL GORM

### 10.1 最小 schema 增量

- **不新增重复 Agent/Workflow/Run 主表。** Profile/Team 沿用原表；V2 definition 存 policy_json + members，schema_version 分派。
- `agent_team_runs`：补 owner_user_id、source_kind、idempotency_key_hash、request_fingerprint、plan_snapshot_json/hash、revision、next_event_seq、deadline、lease_owner/epoch/expiry；inbox/source account 改可空，只为 channel source 保留必填合同/唯一 inbox 语义。
- 新 `agent_team_run_nodes`：PK/run+node 唯一；status/revision/current attempt、deps/input snapshot/output refs、next_retry_at、failure metadata。小 DAG 依赖存快照 JSON，不再建边表。
- 新 `agent_team_run_attempts`：run+node+attempt 唯一、engine task/session ref、epoch、真实 usage/route/timing/error、replay safety 与持久化健康。
- 新 `agent_team_artifacts`：文本/结构化结果及引用元数据；二进制使用现有 media。Task result/transcript 复用，不把所有大内容复制到 run/event 表。
- `agent_team_run_events`：补 SQLite 建表/唯一 `(tenant,run,sequence)`、新增 node/attempt/envelope version（兼容旧行默认值）。
- Mailbox 优先存有权限的 ArtifactRef，正文由 Artifact 管理；不沿用旧 PayloadRef 字段装任意长明文。V2 envelope 明确 from_node/to_node/from_attempt，member key 仅作 Agent 来源展示；边消息幂等 key 包含 run/source-node/attempt/target-node/output-hash，不能把同 Agent 的不同节点消息合并。
- Approval 表、pause-specific 状态在 P2 再加；不为了未来画布/循环预建大量空表。

### 10.2 事务、索引和兼容

- 唯一 `(tenant,user,idempotency_key_hash)`，fingerprint 覆盖目标、Team/Profile 固定版本、公开执行配置和输入 hash；同 key 不同语义 409。只存 scoped key hash，不记录 raw key。
- queued claim、NodeRun CAS、Attempt 创建、取消意图、终态写入和关联事件有专用事务方法，不调用旧全字段 Update 覆盖。
- 认领事务只做 DB 操作，模型/工具永远在事务外；SQLite 保持当前 immediate tx 模式并加锁冲突回归；MySQL 使用现有 repository 方言，不把 SQLite 测试当 MySQL FK/迁移验证。
- 索引：run(owner/status/created_at)、node(run/status/next_retry_at)、attempt(engine_task_id)、events(run/sequence)、artifact(run/node)。SQLite/MySQL 都做实际创建/重开测试。
- V1 hash/发布/loader 不改行为；V2 decode/validate/publish/run 全部分派。渠道 loader 不得把 V2 JSON 静默当 V1；MVP 没有 V2 channel adapter 时明确跳过/拒绝绑定。
- 新增 migration 编号在实施时取仓库最新编号，不提前固定 000029 造成并发冲突。up 兼容已有数据；down 遇到 V2 记录/非渠道 Run 需拒绝破坏性回滚，提供备份/导出步骤。
- 新 feature off 时不注册 worker/入口；旧单 Agent 数据/配置不迁移。老版本二进制不保证能读 V2 数据，回滚采用禁用新功能而不是直接降二进制。

## 11. 失败、重试、取消与恢复

### 11.1 错误分类与重试

可重试：明确瞬时网络/429/服务暂不可用，且无已提交工具副作用/未决输出的只读 attempt。不可自动重试：权限拒绝、能力不匹配、非法 DAG/版本、密钥缺失、context budget、取消、policy changed、未知副作用。

- 模型 fallback 在一个 Attempt 内，由既有 client 控制；总请求次数另记录，限制 fallback candidates 与 provider 内部尝试，不能 `node attempts × 无界 fallback`。
- MVP 不做自动转交到权限不同 Agent，也不在失败时自动改角色；保留显式 fallback profile/transfer 端口，后续需固定快照、权限不扩张与额外预算。
- Fake clock 驱动 timeout/backoff；不使用长 sleep 使测试不稳定。日志只给 error code、安全 message、trace，不直接拼 provider 原始 body。

### 11.2 取消与清理

取消 API 在事务中写 flag + event；通知 Run controller（进程内 map）只是加速；Scheduler/Executor 在每个派发、重试、工具调用、fallback 前再次检查 context 与持久意图。服务关闭先拒绝新 Run，cancel root、等待有限 grace、flush 事件/attempt 结果，然后释放 lease/连接。取消后的终态写入使用独立且有严格超时的 cleanup context，不能复用已取消 ctx 导致终态永远落不下，也不能用它继续执行模型/工具。

禁止 workflow executor 使用 `RunBackground` 中脱离父取消的语义启动节点；节点 ctx 必须是 Run ctx 子级。已派发的工具必须支持 ctx/cleanup；不响应的 executor 被标 interrupted/隔离，不把资源未释放伪装为 cancelled。子进程清理复用受控 process ownership，不按未经验证 PID 误杀进程。

### 11.3 重启恢复

- 服务启动读取 queued/running/cancel_requested/interrupted；单机仍用 durable claim/epoch 防同机重复 worker，不引入分布式租约服务。
- queued 重建 ready queue；completed 节点复用原 Artifact，不重新执行。
- running 的旧 attempt 先核对 engine task terminal/Artifact/event checkpoint：确定成功才落 completed；确认未启动才可回 ready；不确定则 interrupted，不从 transcript 猜成功。
- cancel_requested 优先收敛取消，不重试；Run deadline 已过则 timed_out。
- P1 提供 interrupted 的显式恢复命令及 UI 说明：只允许用户确认后重试可证明无副作用的节点；新的 epoch 和 attempt，不修改旧记录。其他节点保留 interrupted 并给操作建议。
- 不把现有 stale task reaper 当恢复调度；协调 reaper 与 workflow attempt 的权威终态映射，避免旧 Task 已 failed、Run 却标 completed。
- 故障注入点包括“DB 接受未唤醒”“节点返回但结果未提交”“提交后通知丢失”“取消后迟到成功”“恢复时旧 worker 返回”。

## 12. API 设计（复用 tenant 边界）

现有 Profile CRUD/validate/publish/rollback/assignment 接口继续复用。Team 管理仍用原资源，增加 schema-aware DTO；不新建 `/agents` 平行入口。

| 接口（拟议新增/扩展） | 合同 |
|---|---|
| `GET /tenant/agent-types` | 只读 registry/ExecutionKind 可用性；服务端来源，不由浏览器决定权限 |
| `GET/POST /tenant/agent-teams`、`.../:key` | 已有管理接口扩展 V2 Workflow DTO；写入先完整校验 |
| `POST /tenant/agent-teams/:key/validate`、`.../publish` | V2 显式分流，完整 nodes/profile/policy hash 校验，版本不可变 |
| `POST /tenant/agent-teams/:key/runs` | **新增启动语义**；现有 handler 当前只列 runs。要求固定 version、input、Idempotency-Key；202 + run_id/location；重复同请求返回已有 Run |
| `GET /tenant/agent-teams/:key/runs` | 已有列表，补 owner/status/cursor 分页，不先做全量查询 |
| `GET /tenant/agent-teams/:key/runs/:run_id` | 已有详情扩展 snapshot：nodes/attempts/artifacts summary/last_sequence/control capabilities |
| `GET .../:run_id/events?after_sequence=N` | 已有 JSON timeline 扩展增量分页，旧参数仍兼容 |
| `GET .../:run_id/events/stream?after_sequence=N` | 新 SSE；统一鉴权，断线补漏，Last-Event-ID 支持 |
| `POST .../:run_id/cancel` | 已有路径升级 V2 为 durable cancel；终态幂等；不改 V1 状态含义冒充执行停止 |
| `POST .../:run_id/recover` | P1 interrupted 安全恢复；要求 expected_revision + operation key；先 scope/policy/replay safety 检查 |
| `GET .../:run_id/artifacts/:artifact_id` | 有 scope、大小、MIME 与安全渲染约束；不是任意本地路径下载器 |
| `POST .../:run_id/pause` / `resume` / `approvals/:id/resolve` | P2；首期不注册或返回明确 unsupported，不做假成功 |

HTTP handler 不直接起裸 goroutine；调用 application service。scope 从认证上下文解析，不能相信 body 的 tenant/user；所有 Run/Task/Artifact/Event 读取校验 owner 与 Team 关系，既有 tenant 边界仍保留，多租户产品化本期不扩展。

错误统一 `{code,message,run_id,node_id,attempt_no,retryable,trace_id}` 安全 DTO；使用 400/403/404/409/422/429/503 区分语义。新时间字段使用毫秒时间戳，已有接口时间字段保持原格式，避免无关兼容改造。

## 13. UI：最小可扩展，不做画布

### 13.1 Agent 管理

复用 settings/profiles 的 AgentProfilesPanel，增加基本表单（名称、RoleType、提示词、已有 provider/model 选择、工具显式列表、scope）和发布校验提示；原 JSON 高级编辑仍可保留。旧 Profile 不删除/覆盖；新 Agent 默认 General/text/无工具。可显示“继承全局主模型”，密钥仍只在原四分区设置。

不要把 settings/agent（默认 Profile 分配）误当 Agent CRUD；沿用现有职责。角色表单选项来自 registry，不在多处复制数组。

`../niuma/web/src/lib/agentProfiles.ts:61` 当前本地 validator 只接受 schema V1，AgentProfilesPanel 在保存/远端校验前会调用它。因此必须同时增加 Profile V1/V2 discriminated DTO、parser、default document、版本化校验，不能只改 Go 或表单。验收覆盖 V2 创建→保存→发布→重读不丢 role/kind/workspace 字段，以及旧 V1 编辑/复制/回滚保持原语义。

### 13.2 Workflow 模板与新建运行

复用 Teams 的管理资产，新增 V2 Workflow 列表视图与轻量路由 `/webui/v2/workflows`。V1 团队保留设置/渠道管理，V2 展示步骤卡片、依赖 chips、每节点 Agent 和预算；只提供增删步骤、选择依赖、调整顺序的基础表单，不画自由拖拽画布。

从模板点击运行 → 目标/资料输入 → 检查 Agent/模型/权限/节点数/预算 → 生成请求 key → 启动。key 在本次提交意图内稳定，重复点击不生成多个 Run；明确“新运行”才换 key。内置示例可使用 demo/offline provider，但生产入口不能由 query param 开启 mock 或绕过授权。

### 13.3 Run 详情

新增 `/webui/v2/workflows/:key/runs/:run_id`（version 在 snapshot 中固定）；扩展 V2 typed router，不引入完整新路由框架。

壳层按 route kind 互斥显示 Workflow/会话正文/Inspector；当前 `../niuma/web/src/v2/WebUIV2App.tsx:401` 仅按 selectedRef 挂会话，新增页面不能让两套正文同时出现。保留 shell 会话订阅与切回会话上下文，Workflow 编辑器拥有独立 dirty/busy 与离开保护。RunClient 单独 typed adapter，复用 `../niuma/web/src/lib/api.ts:544` 的 fetch/SSE 传输与断线 readback 模式，不混用 Session reducer。

- 顶栏：目标、Workflow/version、总状态、耗时、usage（未知即未知）、停止/取消中/安全恢复。
- 步骤卡：Planner → Researcher → Reviewer；当前 Agent、高亮 phase、依赖、attempt、route、error/retry。
- 时间线：只复用 TeamRunObservability 的视觉组件；**新增独立纯函数 Workflow ordered reducer**，按 run/node/attempt + sequence 投影、显式状态映射，不复用旧组件按 member_key/事件名称猜状态的逻辑（`../niuma/web/src/components/TeamRunObservability.tsx:510`）。覆盖同 Agent 多节点、旧 attempt 迟到、retry_wait、interrupted、cancel_requested，不把每个 delta 渲染成单独大卡。
- 详情抽屉：输入摘要、输出、安全 Artifact、耗时/usage、错误/重试历史；不默认展示系统提示/任何密钥，也不跨 Agent 展开无关 transcript。
- 等待依赖、等待模型响应、工具执行、等待用户、停止中分别可见；失败/超时/取消不只靠颜色。
- i18n 复用当前中英文策略，tokens/colors 用现有 design tokens；浅/深色、键盘焦点，时间线折叠而非挤出窗口。浏览器响应式门禁覆盖 360/768/桌面宽度；Wails 原生窗口当前最小 1024×700（`../niuma/desktop-v2/config.go:25`），原生按此最小值与正常桌面尺寸验收，不为本功能顺手改变全局最小尺寸。
- P2 才出现真实的批准/拒绝、暂停/恢复按钮；不存在的能力不放可点击假按钮。

不重新设计 ModelSettingsPanel，不改桌面宠物与品牌，不用浏览器 fixture 截图宣称 Wails 验收。

## 14. 第一阶段明确不做

- 可视化流程画布、任意条件/循环、动态生成 Agent/节点、递归子 workflow。
- 分布式调度/队列、多租户新权限产品化、团队即时聊天/广播总线。
- 复杂记忆、向量库、自动摘要收费调用、自动发现所有模型能力。
- 真实付费模型压测；不为验收发送用户资料到外部模型。
- 任意 Bash/Coder 共享目录并发写；第一版 Researcher 默认不联网。
- 自动 Computer Use/图片节点调度、跨权限转交、审批与暂停在 P1 中先不实现（设计与后续门禁已明确）。
- 不修复所有旧渠道 Team 问题；只做 V2 安全版本分流与必要公共边界修补，旧缺口单独登记，不能悄悄扩大到重写整个 channel runtime。

## 15. 可提交实施切片与文件/验证范围

### P1a：定义与持久化基础

- [ ] 新增 AgentType registry、Profile 文档版本解析，保持 V1 canonical hash。
- [ ] 新增 Team V2 Workflow DTO、纯编译/校验、不可变完整快照；所有 publish/load/validate 按版本分流，V2 禁止误入旧渠道引擎。
- [ ] 实现最小 RunRepository 事务方法、node/attempt/artifact 模型与两方言迁移；修正桌面 event 表缺失。
- [ ] 增加 deny-by-default policy compiler；确认空列表、成员限制/override、不可共享可变 registry。
- [ ] 验证后独立提交/推送，台账记录未实现调度/UI，不能声称多 Agent MVP 已可用。

候选修改：第 3.1 节 domain/storage 文件、现有 profile schema 测试、SQLite 升级/重开测试、MySQL migrations/约束测试、V1 channel loader 版本回归。

### P1b：调度内核与故障控制

- [ ] fake Executor 先跑链式、并行、join；固定池/总配额/稳定排序。
- [ ] 实现单 Agent 与 Run deadline、Attempt 策略、依赖失败、panic fence、真实终态检查。
- [ ] 实现持久取消 + Controller + 子 ctx/cleanup，完成取消竞态与重复请求测试。
- [ ] 实现启动扫描、epoch/CAS、interrupted 与显式安全恢复，不自动重放未知动作。
- [ ] 事件与状态事务、delta 批处理、输出 Artifact、usage 结算，持久化失败不得 completed。
- [ ] focused + race 通过后提交/推送；此阶段无真实模型调用。

### P1c：生产执行适配与 API

- [ ] 复用 query Session factory/Profile resolver、provider fallback、task/session/compact，不新写模型循环；先落实无环 RunStoreAdapter、factory 副作用前受限 policy 和 workflow-only RecordingPolicy，不原样复用 raw Recorder。
- [ ] 强制唯一 run/node/attempt session，构建全新 prompt/options/registry；member policy/overrides 真正进入编译快照。
- [ ] 同事务准备 Task 身份并绑定 Attempt，采用 prepared Task；终态由 CommitAttempt 同事务提交。拦截 workflow-owned Task 的旧 PATCH/message/cancel/Session Stop/reaper，不能通过旧 handler 绕过编排。
- [ ] 在本地 HTTP 服务生命周期注册 scheduler，保留单 Agent 入口；后端 auth、幂等启动、详情、Artifact、SSE、cancel/recover。
- [ ] 使用 loopback httptest provider 验证真实生产接线，网络白名单只允许测试端点，拒绝公网出口。
- [ ] 相关 API/schema 生成类型按仓库现有命令更新；验证后提交/推送。

### P1d：最小 UI 与离线 E2E

- [ ] 先补 Profile V1/V2 前端 DTO/parser/default/validator，回归 V1 编辑/复制/回滚；再复用 Agent 编辑能力、新增 V2 模板选择/运行表单、详情路由/时间线、失败/取消/安全恢复。
- [ ] 独立 Workflow cursor reducer、断线补漏、重复提交、隐藏不支持操作、输入/输出脱敏；显式处理同 Agent 多节点与旧 attempt 迟到。
- [ ] 中英文/深浅/窄屏实际渲染测试；单 Agent、默认 Profile 分配、模型设置四分区回归。
- [ ] 为新增 workflow spec 更新 `../niuma/web/playwright.config.ts:33` 的 V2 desktop/narrow/mobile testMatch，避免命令运行却没有选中测试。
- [ ] 离线 API E2E 验证完成后提交/推送；构建最终源码对应 `.app` 再做原生验收，不复用旧进程。

### P1e：原生与整体交付

- [ ] 使用 `../niuma/scripts/build-desktop-v2.sh` 生成真实 macOS `.app`；按当前工具链补 PATH，不修改用户全局 Go 配置。
- [ ] 结束/退出旧应用并核对新 PID、可执行路径、build manifest 与提交；只操作新构建窗口。
- [ ] 离线 mock endpoint + 隔离应用配置，实点创建 Agent、保存/启动两个必交付模板；双分支场景看到两分支在途→join 等待→全部成功后 join 开始。检查失败/重试、取消、服务重启后恢复说明；窗口按 1024×700 与正常桌面尺寸，不混用浏览器 360/768 门禁。
- [ ] 用原生窗口截图保存 `../niuma/desktop-v2/build/validation/YYYYMMDD/multi-agent-orchestration/`；日期为实际验收日。证据 manifest 记录源码 commit/build/PID、场景、截图及 mock 网络范围；无密钥/真实数据。
- [ ] 清理临时 DB/日志/fixture/孤儿进程，仅保留用户要求的截图与安全验收摘要；被忽略的截图不强制加入 Git。
- [ ] 必要全量回归、最终文档与交付 commit/push；HEAD 必须等于 origin/main，否则明确报告阻塞。

## 16. 测试策略与验收矩阵（尚待实现的门禁）

| 范围 | 必须验证 |
|---|---|
| 纯领域 | 类型注册、未知类型/版本、空/重复节点、悬空依赖、环、节点/深度限制、固定版本与完整 hash |
| DAG 调度 | 链式 A 输出给 B、并行峰值不超限、join 等全部成功、稳定拓扑顺序、不为 pending 创建无界 goroutine |
| 状态 | nil error+failed 仍失败、失败不成功、terminal CAS、取消/完成竞态、持久失败、panic 回收；旧 PATCH/message/cancel/Session Stop/reaper 与 CommitAttempt 并发、Task/Attempt/Node/Event 原子性 |
| Retry/超时 | transient vs policy errors、max_attempts 包含首次、backoff 不占 slot、deadline 含等待、parent cancel 阻断 fallback/child、无第二层重试倍增 |
| 幂等 | 并发重复启动同 Run、同 key 不同 fingerprint 409、重复 cancel/recover、提交后进程退出/响应丢失不重复执行 |
| 恢复 | queued 重建、completed 不重跑、in-flight interrupted、cancel intent 保留、epoch 拒旧结果、DB reopen/重启 UI 状态与事件一致 |
| 权限 | 空工具集、deny 优先、member policy/override 生效、跨 run prompt 隔离、MCP/skills/alias 不越权、workspace/symlink、普通 Agent 无 ComputerUse/Session 特权；恶意生命周期 hooks sentinel 从 factory 到 Stop/compact 均不执行 |
| 脱敏 | API Key/Auth Token/authorization、嵌套 JSON、错误/工具/Artifact/事件/SSE、跨 delta secret、前端安全渲染；直接查 transcript/Task result 文件与 DB 无秘密/私有 thinking；必要与可选 recorder 错误区分；只用虚构 sentinel |
| 存储 | SQLite 升级/重开/事务锁、MySQL 非空/FK 迁移、scope 索引、节点/attempt/event 唯一约束、更新 revision、旧数据/V1兼容 |
| 前端 | 组件类型、顺序/并行/依赖 timeline reducer、SSE重复/断线、失败/重试/取消/恢复、双击运行、i18n/themes/窄窗真实渲染 |
| 原生 | 最终 Wails build 实点，不是 DOM；确认新 PID/build、离线完成/失败/取消/重启、原生截图 |
| 回归 | 单 Agent chat/tools、Profile 默认分配、V1 Team、模型四分区、Computer Use 权限/Stop、不影响图片服务/宠物 |

拟执行命令（按切片增量，不在文档阶段宣称已执行全部）：

```sh
cd ../niuma
go test ./internal/agentprofile ./internal/agentteam ./internal/agenttasks ./internal/agentruntime
go test -race ./internal/agentteam ./internal/agenttasks ./internal/agentruntime
go test ./internal/storage/mysql ./internal/sessioncontrol/... ./internal/tenant
# 新增 handler/组合根测试先 -run 指定范围，再按影响范围扩大
go test ./internal/server ./internal/cli
go test ./...    # P1e 或影响面要求时
npm --prefix ../niuma/web run typecheck
npm --prefix ../niuma/web test
npm --prefix ../niuma/web run test:e2e -- <新增离线编排spec>
bash ../niuma/scripts/build-desktop-v2.sh
```

P1a 只有 migration 文本/SQLite 测试而没有可用 MySQL 验证环境时，应明确标记 MySQL 集成门禁未过；不运行仓库生产 DSN，不把数据库/真实凭据带进 Git。浏览器渲染证据与原生验收分目录标记，绝不混用。

## 17. 本轮实际执行的基线验证

本轮仅调研与文档，没有新增业务行为；以下通过不代表新调度器/原生 MVP 已验收。

1. 原始 `go test ...` 被环境阻塞：默认 `go` 自动验证 Go 1.26.0 工具链时报 `checksum database disabled by GOSUMDB=off`。**没有修改 go env、go.mod 或全局配置。** 改为直接使用本机已有对应版本工具链并设置命令级 GOTOOLCHAIN=local。
2. 以下六包基线全部通过：

```sh
GOTOOLCHAIN=local ~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.darwin-arm64/bin/go test \
  ./internal/agentprofile ./internal/agentteam ./internal/agenttasks \
  ./internal/agentbudget ./internal/agentruntime ./internal/agents
```

3. 存储 focused 测试通过：

```sh
GOTOOLCHAIN=local ~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.darwin-arm64/bin/go test ./internal/storage/mysql \
  -run 'Test(AgentProfileTeamMigrationsDeclareTenantScopedContracts|AgentProfileRepositoryRejectsInvalidInput|AgentTeamRepositoryRejectsInvalidInput|UpdateAgentTeamRunPersistsFinishedAt|SQLiteDesktopRepositorySupportsProfilesAndSkills|SQLiteSessionControlStopRecoveryRoundTrip)$' -count=1
```

4. Session Control/Handoff/组合根/遥测/环境清洗五包全部通过：

```sh
GOTOOLCHAIN=local ~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.darwin-arm64/bin/go test \
  ./internal/sessioncontrol ./internal/sessioncontrol/handoff \
  ./internal/sessioncontrol/runtimecompose ./internal/telemetry ./internal/procenv
```

5. 前端 focused 基线通过：4 个文件、53 个测试；Node 输出一条现有 localstorage-file 路径 warning，不影响测试结果。

```sh
npm --prefix ../niuma/web test -- \
  src/components/TeamRunObservability.test.tsx \
  src/v2/settings/profile-settings.test.tsx \
  src/v2/routes.test.ts src/v2/settings/globalSettingsDraft.test.tsx
```

6. `npm --prefix ../niuma/web run typecheck` 通过（tsc -b --force）；文档中的当前架构绝对文件引用/引用起始行已用只读脚本检查存在与行号范围。

7. 未运行：新 scheduler tests（尚无实现）、全量 Go、前端 E2E、Wails 构建、真实原生点击/截图、真实模型调用。**本轮没有新桌面截图路径，不用旧截图填补验收。**

## 18. 开源复用取舍、风险、回滚

### 18.1 复用优先 / 外部调研边界

首选仓库中已有 Profile/Team/Task/Session Control/Handoff/GORM/query/client；DAG 是小型静态图，拓扑校验与 ready queue 足够，无需先引入新框架。

2026-10-29 通过 GitHub 官方 repository API 只读核对 `langchain-ai/langgraph`（MIT、主要 Python）与 `temporalio/sdk-go`（MIT、Go）。这仅证明语言/许可证候选，不代表已验证其全部运行语义；本期不引入 Python 服务或分布式工作流基础设施。检索通道未返回可用文档正文，因此不把外部框架细节当方案证据。若未来需要跨机持久执行，再做受控 PoC/成本评估，而不是让 MVP 依赖未验证外部系统。

参考定位：`https://api.github.com/repos/langchain-ai/langgraph`、`https://api.github.com/repos/temporalio/sdk-go`；未下载/执行第三方代码，未新增依赖。

### 18.2 主要风险与约束

| 风险 | 控制与剩余边界 |
|---|---|
| 现有 Team 并非渠道无关/可恢复引擎 | V2 严格分流、独立调度，不直接复用旧 validator/start/update/会话键 |
| schema 版本被 loader 静默忽略 | 所有保存/发布/路由/加载/恢复入口识别版本；未知版本 fail closed |
| query 大循环、hooks 与原始 Recorder 接线耦合 | 小 factory/adapter 注入；初始化前受限 policy、禁 raw recorder、终态只由 RunStore 提交；不整包重构，不让 storage 反向 import 领域造成环 |
| 并发工具产生不可逆效果 | P1 默认无工具/只读 Artifact，副作用 Executor 后置，未知不重试 |
| 停止不等于远端立刻停止计费 | ctx/持久取消阻止后续请求；已发送 provider 请求不能保证远端撤销，UI 披露 |
| token 预算未知或事后返回 | 准入预留、真实 usage 结算、并发/输出/时间硬上限；成本 unknown 不伪报 0 |
| 秘密出现在用户输入或任意工具输出 | 数据源隔离 + 明确 allow + redaction；不声称任意文本绝对无泄漏 |
| SQLite 写竞争/高频事件膨胀 | 控制事件事务、delta 批处理、短事务、分页/retention、busy/race 测试 |
| 事件丢失/终态落库失败 | fail closed、可恢复 journal/attempt 核对；不返回伪成功 |
| 旧 Team 历史缺陷范围扩张 | 仅必要接线/安全分流修补；旧渠道取消/usage/恢复问题单独列工作，不宣称同步修好 |

### 18.3 回滚

默认 feature gate 控制 Workflow V2 UI、API 写入口与 worker 启动，只有完成离线/原生门禁后开启。关闭功能先拒绝新 Run，再取消/收敛在途 Run，保留只读运行历史。旧单 Agent/Profile/Computer Use 继续可用。

数据库迁移加法优先；nullable channel source/新增 CHECK 先备份和验证，存在 V2 数据时禁止盲目 down。不开自动删表/删事件/删 Profile 的回滚脚本。若调度有问题，禁用新调度，不回退已批准安全边界，不把 V2 送进旧 Coordinator 引擎。

## 19. 架构问题逐项回答与待确认项

| 问题 | 答案 |
|---|---|
| 是否解耦？ | 是：定义/纯编译/调度/执行/模型工具/存储/API/UI 分界；复用现有端口，不在 controller 塞调度循环 |
| 能否扩展 Agent 类型？ | 是：RoleType registry + Profile；角色是元数据/默认模板，调度不 switch Planner/Coder；执行能力另注册 |
| 能否替换调度器？ | 是：application service 依赖 Scheduler port，LocalScheduler 是首个实现；状态权威留 Repository |
| 能否同步和异步？ | 是：同 Run/事件事实，Wait 适配同步，202 异步为生产首期；不双写双引擎 |
| 能否接分布式 Worker？ | 接口与 durable identity/epoch 可扩展，但网络队列/远端认证/资源传输尚未实现，不承诺即插即用 |
| 是否方便测试？ | compiler/reducer/state 为纯函数，Executor/Clock/Store 注入，mock provider 验证真实接线，无付费调用 |
| 是否破坏单 Agent？ | 目标是不破坏：新入口 feature gate，V1 runtime/provider/FilterPolicy 默认行为不变；须回归证明 |
| 是否影响模型设置？ | 不重新设计四分区，不复制/迁移密钥；只引用既有全局配置，模型不兼容清晰失败 |
| 是否影响 Computer Use？ | 不改变普通 sub-agent 禁止继承桌面权限；现有 route/bridge/Stop 保持，未来独立授权 adapter |
| 状态/事件能否恢复？ | 设计上持久 CAS + 事务事件 + epoch + snapshot；未知动作 interrupted，不靠内存/最后文本冒充恢复 |

**推荐用户确认以下基线后再进入实现：**

- Agent=Profile、Workflow=Team V2，保留所有 V1 管理与单 Agent 行为。
- P1 交付文本静态 DAG、有限并行/依赖、重试/超时/取消、可观测详情与安全重启恢复。
- 默认无工具；Researcher 基于给定资料，不默认联网；Computer Use/图片调度、人工确认、暂停/恢复在后续切片。
- 每切片验证后提交/推送 origin/main；真实桌面验收只针对最终新构建，暂缓项不以 mock/旧截图冒充通过。

**确认前的停止点：本方案文档及调研交付，不继续写业务代码。**

## 20. 实现落地记录（2026-10-29）

本文前面的候选文件/适配方案是设计输入；交付采用以下实际边界，优先满足无环、
最小权限和独立测试，而不是为保持候选命名强行搬迁 V1：

- 纯领域/编译器/调度器位于 `internal/agentteam/workflow/` 子包，
  Store/Executor 为小接口。SQL 可依赖这个纯子包，不能反向依赖父 agentteam
  或 runtimecompose，因此不会形成现有 V1 adapter 的循环 import。
- `workflowapp` 组合既有 Profile/Team 资源；SQL 投影复用主 Run 身份并提供
  CAS/事务。HTTP/Wails 不拥有第二份状态机；历史与 SSE 均来自同一 journal。
- `runtimecompose.TextExecutor` 直接创建隔离的 query.Session，不走带初始化
  副作用的 CLI factory；关闭工具/hooks/发现/记忆/原始 transcript 和全局遥测，
  而不是先继承所有权限再寄希望于提示词约束。上下文只含用户目标、当前步骤
  与声明的直接依赖产物；完整 usage 与未知保守预留分离。
- Agent role registry 是可扩展描述信息，不参与权限分支。Execution kind 与
  capability 验证独立；首期只接受 text/no-tool，desktop/image 明确拒绝。
- Service 使用现有全局模型配置与已有 client fallback；固定公开模型路由指纹，
  不把密钥写进 Plan/hash。恢复拒绝模型路线漂移，允许凭据轮换。
- 调度器在原服务生命周期内运行，不停止/接管 screen 管理的 channel Worker。
  V1 Team 入口拒绝/跳过 V2；旧 Task 写接口无法越过 durable task binding。
- UI 使用独立 workbench、卡片/依赖列表/时间线，不改已有四分区模型设置。
  角色和能力既有定义继续复用，不新增第二套模型密钥设置。
- P2 durable approval/暂停继续，P3 Computer/Image executor、分布式执行、任意
  条件/循环/自动 Agent/记忆继续暂缓；没有用按钮或 mock 冒充这些能力。

验证/提交/未闭环原生验收以
`docs/plans/2026-10-29-multi-agent-delivery.md` 为准；新手实际操作说明见
`docs/usage/desktop-team-workflows.md`。本机锁屏时不绕过安全机制，原生点击和
截图必须等用户解锁后补齐，不能以浏览器截图替代。
