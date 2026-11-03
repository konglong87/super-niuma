# Agent Profile WebUI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents are available) or superpowers:executing-plans to implement this plan. Steps use checkbox syntax for tracking.

**Goal:** 在现有多租户 runtime 上增加版本化的 Agent Profile 与 Agent Team 领域模型，并通过 WebUI 让用户安全地创建多个智能体、把它们组成协作团队、绑定到多个飞书机器人和同一个群组，完成可追踪的协作任务。

**Architecture:** 采用方案 B 并扩展为 Profile + Team 两层聚合：新增独立 agent_profiles、agent_profile_assignments、agent_teams、agent_team_members、agent_team_bindings 和 team mailbox 持久化，不复用 tenant_user_profiles 的画像语义。单 agent 请求由 ProfileCatalog -> ProfileResolver -> ProfileValidator -> ProfileApplier 编译为现有 query.Options；群组任务先由 Feishu Adapter -> durable Inbox -> Team Router 归属到 Team，再由 coordinator/parallel orchestrator 调度固定版本的 Profile 成员。内部 agent 消息走持久化 mailbox，外部群组只由受控 bot 输出，避免机器人回声和重复执行；server hard policy 始终优先于 tenant、user、team 和 request。

**Tech Stack:** Go、Gin、GORM、MySQL migration、internal/tenant、Swagger、React + TypeScript + Vite WebUI、tenant auth/role、telemetry/audit。

---

## 1. Scope

### In scope

- 内置 chat-assistant、copywriter、coder 三个 profile。
- tenant shared、user private、builtin 三种 profile scope。
- draft、validate、publish、archive、version、rollback、assignment。
- 一个用户可创建多个 profile；profile 可被多个 Team 复用，但每个 Team 成员固定 profile version。
- 一个 profile 可绑定一个受控飞书机器人账号；多个 profile bot 可绑定同一 Team 和同一飞书群组。
- Team 支持 coordinator、parallel_review 两种 V1 编排模式，保留后续 round_robin 扩展位。
- Team 任务有 durable mailbox、成员状态、预算、超时、取消、幂等和最终汇总。
- WebUI Catalog、Editor、Effective Preview、Versions、Assignments。
- WebUI Teams、成员编排、飞书账号/群组绑定、运行记录和协作回放。
- profile 到 prompt_mode、runtime_profile、tools、permissions、sandbox、query.Options 的编译。
- profile key/version/hash/source 进入 session、trace、audit 的脱敏 metadata。
- 保留现有 prompt_mode、model、max_tokens 等请求兼容字段。

### V1 non-goals

- profile 不能覆盖 auth、tenant/user/session isolation、quota、audit、completion gate。
- 用户不能配置 provider key、MCP secret/header、任意 hook command、任意 cwd/writable root。
- persona/addendum 只能追加受保护 prompt sections，不能替换完整 system prompt。
- 不在本功能中宣称 temperature/top_p 生效；现有 OpenAI 字段尚未贯通 QueryRequest/provider。
- tenant_user_profiles 继续表示用户画像和 prompt context，不迁移成 Agent Profile。
- 不让每个机器人直接消费并回复同一个群消息；所有群组事件必须经过 Team Router 和 bot loop guard。
- 不让 Team 成员默认共享 cwd、Git workspace、MCP secret 或 destructive 权限；编码成员必须显式绑定受控 workspace 和写入策略。

## 2. Baseline and Impact

当前两个正交维度：

| 维度 | 当前值 | 责任 |
|---|---|---|
| prompt_mode | code / chat | system prompt 与本地/tenant context boundary |
| runtime_profile | default / bare | tool set、自动发现、MCP、hooks、后台增强 |

事实锚点：

- internal/promptmode/promptmode.go：prompt mode。
- internal/runtimeprofile/profile.go：runtime profile。
- internal/query/query.go：query.Options 和 code/chat prompt 分支。
- internal/cli/cli.go：newQuerySession 与 tool registry wiring。
- tenant_user_profiles、/tenant/profile：现有用户画像。
- web/src/App.tsx、web/src/components/InspectorPanels.tsx：现有 WebUI 导航和 profile 页面。

影响节点：RT-BOUNDARY、RT-WIRING、RT-PROMPT、RT-PRETOOL、RT-TOOLS、RT-SUBAGENT、RT-PERSIST、RT-OUTPUT、RT-OBSERVE、RT-ENTRY。

- 默认 profile opt-in 变更为 B2_MODE。
- 接入 API、DB、trace/SSE metadata 后为 B4_PROTOCOL。
- Feishu bot/team fan-in、mailbox、outbox 和群组绑定属于 B4_PROTOCOL + B5_SHARED_STATE；必须覆盖重复事件、bot loop、跨租户、跨账号和外部发送 readback。
- 改变无 profile 的默认行为才是 B3_GLOBAL_RUNTIME；首版禁止这样做。
- CLI/TUI 的 profile-less code 主流程不查询 tenant team assignment，也不启动 channel worker；因此 Team 功能是可并行部署的独立控制面。

## 3. Domain Model

### Lifecycle

~~~text
draft -> validating -> published -> archived
             |
             +------> draft (validation_failed)
published edit -> new draft version
rollback -> new version, never mutate history
~~~

runtime 只读取 published。draft/validating 不得执行；archived 不得被新 assignment 使用，但历史 session 保留 version/hash。

### Scope and assignment

| scope | owner_user_id | 可见范围 | 写权限 |
|---|---:|---|---|
| tenant_shared | NULL | 当前 tenant | owner/admin |
| user_private | 当前用户 | 当前用户 | owner；owner/admin 可管理 |
| builtin | NULL | 允许的 tenant | 代码发布，WebUI 只读 |

用户可以拥有多个 profile。assignment 按 surface 而不是全局单值绑定，避免 WebUI 文案、Mobile chat、tenant agent 和本地 code 流程互相覆盖：

~~~text
tenant/user/surface -> agent_profile_assignment -> published agent_profile version
~~~

推荐 surface 常量：`web_chat`、`mobile_chat`、`tenant_agent`、`channel_dm`、`channel_team`。CLI/TUI 默认使用本地 `code` 路径，不受 tenant assignment 影响。

EffectiveAgentProfile 是不可变快照，包含 Identity、PromptPolicy、CapabilityPolicy、ExecutionPolicy、SafetyPolicy、ContextPolicy、Source、ProfileKey、ProfileVersion、RequestedHash、EffectiveHash、BlockedOverrides。

### Agent Team aggregate

Agent Team 是多个固定 profile version 的协作编排，不是另一个可任意覆盖 profile 的 prompt。一个 profile 可以加入多个 Team；同一个 Team 可以让多个 profile bot 加入同一个 Feishu 群组。

~~~text
AgentTeam
├── TeamPolicy (orchestration, budget, timeout, trigger, output)
├── Members[]
│   ├── ProfileKey / ProfileVersion
│   ├── Role (coordinator / researcher / writer / coder / reviewer)
│   ├── FeishuAccountID (one bot identity)
│   └── Tool/Workspace capability ceiling
├── Bindings[]
│   ├── Provider (feishu)
│   ├── AccountID / ExternalChatID / ThreadID
│   └── TriggerPolicy (mention / command / explicit)
└── Run / Mailbox / Evidence / Output policy
~~~

Team 成员的 profile version 在 Team publish 时 pin；后续 profile 发布新版本不会悄悄改变正在运行或已发布 Team。Team 必须重新 validate/publish 才能升级成员版本。

### Team execution modes

| mode | 行为 | 外部群组输出 |
|---|---|---|
| `coordinator` | coordinator 接收任务，按成员角色分派，收集 evidence 后汇总 | 默认只有 coordinator 输出最终结果，成员结果不直接刷屏 |
| `parallel_review` | 多个成员并行独立工作，coordinator 负责冲突检查和汇总 | 可配置阶段性摘要；最终只发送一个 accepted result |

V1 不支持机器人之间自由循环对话。所有成员消息都写入 Team mailbox，带 `run_id`、`from_member`、`to_member`、`sequence`、`idempotency_key` 和 `evidence_ref`。外部群组消息只作为输入或受控输出，不作为内部协作总线。

### Coexistence contract

同一用户可以同时拥有并使用多个 profile，例如：

~~~text
CLI/TUI                  -> legacy code path / coder behavior
WebUI web_chat            -> copywriter profile
Mobile mobile_chat        -> chat-assistant profile
Feishu launch-content     -> Team(copywriter bot + researcher bot + reviewer bot)
Feishu coding-room        -> Team(coder bot + reviewer bot)
~~~

这些 surface 通过 `tenant/user/surface` assignment 或 Team binding 独立解析。创建、编辑、发布文案 profile 不会修改 CLI/TUI 的 code prompt、工具 registry、Git context、permission gate 或本地 session；只有请求显式携带 `profile_id`，或 channel event 命中已发布 Team binding 时，Profile/Team resolver 才参与该请求。

### Feishu bot and group binding

现有 `channel_accounts` 已按 tenant/provider/account_key/app_id/credential_ref 管理一个飞书机器人账号。Profile 不保存凭据，只引用 `account_id`；Team binding 再把多个 account 映射到一个 external chat/group。

约束：

- 一个 active profile bot binding 只能指向一个明确的 Feishu channel account；同一账号可在不同 Team 中作为受控成员，但每个 Team 成员必须有唯一 member key。
- 一个 Team 可以绑定多个 Feishu account 到同一 `external_chat_id`；每个 binding 必须声明 trigger policy，不能让所有 bot 对所有消息自动响应。
- 群消息先由一个 inbound account 的 durable Inbox 接收，再由 Team Router 按 account/chat/thread/mention 映射到 Team；不能让多个 Adapter 对同一事件各自创建 query。
- Team 输出必须经 Outbox，按 account/message idempotency key 投递；发送成功后 readback external message id。
- bot 自己和同 Team 其他 bot 发送的消息默认忽略；只有带受信任的 internal coordination envelope 且 correlation/run 校验通过时，才可重新进入 Team mailbox。

## 4. Configuration Contract

schema_version V1 为 1；未知字段拒绝，不静默忽略。核心配置：

~~~json
{
  "schema_version": 1,
  "identity": {"display_name": "文案写手", "description": "中文营销内容助手"},
  "prompt": {
    "mode": "chat",
    "persona": "你是一名关注受众和转化目标的中文文案写手。",
    "output_style": "marketing",
    "language": "zh-CN",
    "system_addendum": "先确认受众、渠道和字数，再给出成稿。"
  },
  "capabilities": {
    "tools": {"allow": ["Read", "WebSearch"], "deny": ["Edit", "Bash", "AgentCreate"]},
    "skills": ["copywriting"],
    "mcp_servers": [],
    "allow_agents": false,
    "allow_attachments": true
  },
  "execution": {
    "model": "", "provider": "", "effort": "medium",
    "max_turns": 3, "max_tokens": 2048,
    "max_parallel_read_only_tools": 2,
    "auto_compact": {"enabled": false}
  },
  "context": {
    "workspace": false, "git": false, "tenant_memory": true,
    "user_memory": true, "knowledge_base": true, "session_history": true
  },
  "safety": {
    "permission_mode": "ask",
    "sandbox": "required",
    "allow_unsandboxed_commands": false
  }
}
~~~

规则：

- tools：effective = requested intersect tenant intersect server minus hard_deny。
- model/provider/MCP/skills/network/roots/tokens/turns 同样做交集或上限裁剪。
- chat 不得开启本地 workspace/git；Mobile 普通用户不能把 chat 提升为 code。
- 用户只能降低 tokens/turns/effort，不能超过 tenant/server ceiling。
- bypassPermissions、关闭审计/quota/completion gate 永远不可配置。
- MCP/hooks 只能引用 server registry key，JSON 不存 secret。

### Agent Team configuration

Team 配置独立于单 profile，成员引用已发布 profile key/version；bot credentials 和群组外部 ID 不进入 profile JSON：

~~~json
{
  "schema_version": 1,
  "team_key": "launch-content-team",
  "display_name": "新品发布协作组",
  "orchestration": {
    "mode": "coordinator",
    "coordinator_member": "editor",
    "max_rounds": 4,
    "max_parallel_members": 3,
    "run_timeout_seconds": 900,
    "max_total_tokens": 24000
  },
  "trigger": {
    "require_mention": true,
    "commands": ["/team", "/launch"],
    "allow_direct_message": true
  },
  "authorization": {
    "require_tenant_member": true,
    "allowed_external_user_ids": [],
    "destructive_action_mode": "ask"
  },
  "output": {
    "final_member": "editor",
    "phase_updates": "coordinator_only",
    "post_member_cards": false
  },
  "members": [
    {"member_key": "editor", "profile_key": "copywriter", "profile_version": 3, "role": "coordinator", "account_id": 21},
    {"member_key": "researcher", "profile_key": "researcher", "profile_version": 2, "role": "researcher", "account_id": 22},
    {"member_key": "reviewer", "profile_key": "coder", "profile_version": 1, "role": "reviewer", "account_id": 23}
  ],
  "bindings": [
    {"provider": "feishu", "account_id": 21, "external_chat_id": "oc_launch", "trigger": "mention"},
    {"provider": "feishu", "account_id": 22, "external_chat_id": "oc_launch", "trigger": "internal_only"},
    {"provider": "feishu", "account_id": 23, "external_chat_id": "oc_launch", "trigger": "internal_only"}
  ]
}
~~~

Team validator 必须拒绝：重复 member key、不存在/未发布 profile version、同一 Team 多个 coordinator、无 bot account 的 Feishu member、同一 binding 的跨 tenant chat、未配置 group actor authorization、`post_member_cards=true` 与 coordinator-only 输出冲突、超过 tenant quota 的 rounds/tokens/parallelism，以及把普通群消息当作 internal coordination 的配置。

## 5. Resolution Pipeline

优先级：

~~~text
server hard policy
  > tenant policy
  > user profile
  > request override
  > builtin defaults
~~~

请求只引用 profile_id、profile_version 和白名单 override，不能直接提交完整 profile JSON。

~~~text
Request boundary
  -> ProfileCatalog.GetEffective(tenant_id, user_id, profile_id)
  -> ProfileResolver.Merge(server, tenant, user, request)
  -> ProfileValidator.Validate(effective)
  -> ProfileApplier.Apply(effective)
  -> query.Options + registry + permission + sandbox
~~~

ProfileApplier 不修改 profile 原对象。未指定 profile_id 时走现有 prompt_mode/runtime_profile 兼容路径。

### Team runtime pipeline

~~~text
Feishu Adapter(account A/B/C)
  -> durable Inbox(account, event, chat, thread)
  -> Channel Scope + Team Router
  -> TeamRun (idempotent, leased, budgeted)
  -> Coordinator / bounded member workers
  -> ProfileResolver(member profile version)
  -> query runtime per member
  -> Team mailbox/evidence ordered commit
  -> coordinator synthesis + completion gate
  -> Outbox(account-specific bot)
  -> Feishu group card/text + delivery readback
~~~

执行不变量：

1. 同一个 `(tenant_id, provider_event_id)` 只能生成一个 TeamRun；重试只能恢复 lease，不能重复启动成员 query。
2. 同一个 TeamRun 的成员并行数、总 tokens、总 turns 和外部发送次数由 TeamPolicy + tenant quota 双重限制。
3. 成员输出先进入 mailbox/evidence；只有 coordinator 的 accepted final 能成为群组最终声明。
4. 成员之间的 `AgentMessage` 使用 Team mailbox envelope，不复用普通 Feishu 文本，不把 bot 发送的群消息重新当作用户输入。
5. coder 成员的 workspace、write roots、Git 操作和 permission prompt 必须是 Team/tenant 显式配置；多人同时写同一 workspace 默认拒绝，需 workspace lock 或串行 writer policy。
6. Team Router 先校验 channel account、external user、tenant membership、group binding 和 trigger policy，再创建 TeamRun；未授权事件只能 ack/ignore，不能进入模型。
7. Team cancel、timeout、member failure、partial evidence 和 external delivery failure 都要持久化状态，并允许从最后一个 checkpoint 恢复或以 blocked/partial 结果结束。

## 6. MySQL Design

新增 migration：

- migrations/mysql/000015_agent_profiles.up.sql/down.sql：profile、surface assignment、profile bot binding。
- migrations/mysql/000016_agent_teams.up.sql/down.sql：team、members、Feishu group bindings、runs、mailbox。

### agent_profiles

~~~text
id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT
tenant_id BIGINT UNSIGNED NOT NULL
owner_user_id BIGINT UNSIGNED NULL
profile_key VARCHAR(64) NOT NULL
scope VARCHAR(24) NOT NULL
display_name VARCHAR(128) NOT NULL
description TEXT NULL
profile_version INT UNSIGNED NOT NULL
status VARCHAR(24) NOT NULL
config_json JSON NOT NULL
requested_hash CHAR(64) NOT NULL
effective_hash CHAR(64) NOT NULL
validation_json JSON NULL
created_by_user_id BIGINT UNSIGNED NOT NULL
updated_by_user_id BIGINT UNSIGNED NOT NULL
published_at TIMESTAMP(6) NULL
created_at TIMESTAMP(6) NOT NULL
updated_at TIMESTAMP(6) NOT NULL
~~~

约束：UNIQUE(tenant_id, owner_user_id, profile_key, profile_version)；按 tenant/scope/status 和 tenant/owner/key/status 建索引；tenant/user 外键；不存 provider key、MCP secret、hook command。

### agent_profile_assignments

~~~text
id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT
tenant_id BIGINT UNSIGNED NOT NULL
user_id BIGINT UNSIGNED NOT NULL
profile_id BIGINT UNSIGNED NOT NULL
assigned_by_user_id BIGINT UNSIGNED NOT NULL
created_at TIMESTAMP(6) NOT NULL
updated_at TIMESTAMP(6) NOT NULL
~~~

UNIQUE(tenant_id, user_id, surface)，只能绑定 published profile；所有读写同时带 tenant_id、user_id 和 surface。

### agent_profile_channel_bindings

用于把一个 published profile version 绑定到一个现有 `channel_accounts` bot account：

~~~text
id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT
tenant_id BIGINT UNSIGNED NOT NULL
profile_id BIGINT UNSIGNED NOT NULL
account_id BIGINT UNSIGNED NOT NULL
provider VARCHAR(32) NOT NULL
binding_key VARCHAR(64) NOT NULL
status VARCHAR(24) NOT NULL
created_by_user_id BIGINT UNSIGNED NOT NULL
created_at TIMESTAMP(6) NOT NULL
updated_at TIMESTAMP(6) NOT NULL
~~

约束：active binding 使用 `UNIQUE(tenant_id, profile_id)`，历史/archived binding 可保留审计；profile 和 account 必须属于同一 tenant；不复制 `app_secret`，只引用 `channel_accounts.credential_ref`。

### agent_teams

~~~text
id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT
tenant_id BIGINT UNSIGNED NOT NULL
team_key VARCHAR(64) NOT NULL
team_version INT UNSIGNED NOT NULL
scope VARCHAR(24) NOT NULL
display_name VARCHAR(128) NOT NULL
description TEXT NULL
status VARCHAR(24) NOT NULL              -- draft/published/archived
schema_version INT UNSIGNED NOT NULL
policy_json JSON NOT NULL
requested_hash CHAR(64) NOT NULL
effective_hash CHAR(64) NOT NULL
validation_json JSON NULL
created_by_user_id BIGINT UNSIGNED NOT NULL
updated_by_user_id BIGINT UNSIGNED NOT NULL
published_at TIMESTAMP(6) NULL
created_at TIMESTAMP(6) NOT NULL
updated_at TIMESTAMP(6) NOT NULL
~~

`UNIQUE(tenant_id, team_key, team_version)`；另建 `(tenant_id, team_key, status)` 索引用于取 latest published；只有 published Team 可以接收新的 TeamRun。Team publish/rollback 创建新版本，不修改历史成员、binding 或 run。

### agent_team_members

~~~text
id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT
tenant_id BIGINT UNSIGNED NOT NULL
team_id BIGINT UNSIGNED NOT NULL
member_key VARCHAR(64) NOT NULL
profile_id BIGINT UNSIGNED NOT NULL
profile_version INT UNSIGNED NOT NULL
role VARCHAR(32) NOT NULL
account_id BIGINT UNSIGNED NULL
tool_policy_json JSON NULL
workspace_policy_json JSON NULL
status VARCHAR(24) NOT NULL
created_at TIMESTAMP(6) NOT NULL
updated_at TIMESTAMP(6) NOT NULL
~~

`UNIQUE(team_id, member_key)`；profile version 必须是 published；Feishu 成员的 account_id 必须与该 profile 的 active channel binding 一致；同一 Team 只能一个 coordinator。

### agent_team_bindings

一个 Team 可把多个 bot account 绑定到同一 Feishu 群组：

~~~text
id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT
tenant_id BIGINT UNSIGNED NOT NULL
team_id BIGINT UNSIGNED NOT NULL
provider VARCHAR(32) NOT NULL
account_id BIGINT UNSIGNED NOT NULL
external_chat_id VARCHAR(255) NOT NULL
external_thread_id VARCHAR(255) NOT NULL DEFAULT ''
trigger_policy VARCHAR(24) NOT NULL          -- mention/command/internal_only
status VARCHAR(24) NOT NULL
created_at TIMESTAMP(6) NOT NULL
updated_at TIMESTAMP(6) NOT NULL
~~

`UNIQUE(team_id, provider, account_id, external_chat_id, external_thread_id)`；account、team、tenant 必须一致；普通群消息只能进入一个 Team Router 归属。

### agent_team_runs and agent_team_mailbox

`agent_team_runs` 保存一次群组任务的状态、inbox event、coordinator member、budget、heartbeat、cancel、terminal result 和 profile/team effective hash。

`agent_team_mailbox` 保存内部协作 envelope：`run_id`、from/to member、message kind、sequence、payload_ref、evidence_ref、idempotency_key、status、lease、created/consumed timestamps。payload 可脱敏或加密存储；不能把私密 member output 原文直接写入 Feishu outbox。

两表所有唯一键和查询都必须带 tenant_id；TeamRun 以 `(tenant_id, inbox_event_id)` 幂等，mailbox 以 `(tenant_id, run_id, idempotency_key)` 幂等。

兼容规则：

- 不迁移 tenant_user_profiles。
- builtin profile 由 catalog 提供，不复制到每个 tenant。
- 新 tenant 默认 `web_chat -> chat-assistant`；CLI/TUI 不受 tenant assignment 或 Team 影响。
- 旧请求不传 profile_id 时完全保持旧行为。
- session/message/agent task/channel run/team run metadata 保存 profile key/version/hash 和 team key/run id。

## 7. Backend Modules and API

### Planned modules

| 文件 | 职责 |
|---|---|
| internal/agentprofile/model.go | domain/effective types |
| internal/agentprofile/schema.go | schema、canonical JSON、limits |
| internal/agentprofile/builtin.go | 三个 builtin profile |
| internal/agentprofile/catalog.go | builtin/tenant/user catalog |
| internal/agentprofile/validator.go | safety/dependency validation |
| internal/agentprofile/resolver.go | precedence、assignment、hash |
| internal/agentprofile/applier.go | 编译成 runtime inputs |
| internal/tenant/agent_profiles.go | tenant service |
| internal/storage/mysql/models.go | GORM models |
| internal/storage/mysql/gorm_repository.go | CRUD/version/assignment |
| internal/server/handlers_agent_profiles.go | REST、role、audit |
| internal/server/server.go | QueryRequest profile fields |
| internal/cli/cli.go | profile wiring |
| internal/query/query.go | 只消费已编译 options |
| internal/agentteam/model.go | Team/member/binding/run/mailbox types |
| internal/agentteam/validator.go | Team policy、member pin、bot/group binding validation |
| internal/agentteam/router.go | channel event -> Team route、mention/command/loop guard |
| internal/agentteam/orchestrator.go | coordinator/parallel_review、budget、cancel、retry |
| internal/agentteam/mailbox.go | durable internal coordination envelope and idempotency |
| internal/channel/runtime/service.go | TeamRouter/TeamOrchestrator integration point |
| internal/storage/mysql/channel_repository.go | channel account/group lookup and TeamRun/mailbox persistence |
| internal/server/handlers_agent_teams.go | Team CRUD、publish、members、bindings、runs |

### API

| Method | Path | 作用 | 权限 |
|---|---|---|---|
| GET | /tenant/agent-profiles | list catalog | 登录用户 |
| POST | /tenant/agent-profiles | create draft | owner/admin；member 仅 private self |
| GET | /tenant/agent-profiles/{key} | get version | 当前 tenant |
| PATCH | /tenant/agent-profiles/{key} | update draft/new version | 可编辑者 |
| GET | /tenant/agent-profiles/{key}/versions | versions | 当前 tenant |
| POST | /tenant/agent-profiles/{key}/validate | validate | 可编辑者 |
| POST | /tenant/agent-profiles/{key}/publish | publish | owner/admin；private owner |
| POST | /tenant/agent-profiles/{key}/archive | archive | owner/admin |
| POST | /tenant/agent-profiles/{key}/rollback | clone old version | owner/admin |
| GET | /tenant/agent-profiles/{key}/bot-binding | get safe profile bot binding | 当前 tenant |
| PUT | /tenant/agent-profiles/{key}/bot-binding | bind one published profile version to bot account | owner/admin |
| DELETE | /tenant/agent-profiles/{key}/bot-binding | archive bot binding | owner/admin |
| GET | /tenant/agent-profile-assignment | get assignment | self；admin 可查用户 |
| PUT | /tenant/agent-profile-assignment | assign published | self allowed；admin any user |
| GET | /tenant/agent-profiles/{key}/effective | requested/effective diff | 可编辑者、审计 |

Team API：

| Method | Path | 作用 | 权限 |
|---|---|---|---|
| GET | /tenant/agent-teams | list teams and status | 登录用户 |
| POST | /tenant/agent-teams | create draft team | owner/admin |
| GET | /tenant/agent-teams/{key} | get team and pinned members | 当前 tenant |
| GET | /tenant/agent-teams/{key}/versions | list team versions | 当前 tenant |
| PATCH | /tenant/agent-teams/{key} | update draft | owner/admin |
| POST | /tenant/agent-teams/{key}/validate | validate members/bots/groups | owner/admin |
| POST | /tenant/agent-teams/{key}/publish | publish immutable team snapshot | owner/admin |
| POST | /tenant/agent-teams/{key}/archive | archive team | owner/admin |
| POST | /tenant/agent-teams/{key}/rollback | clone old team version | owner/admin |
| PUT | /tenant/agent-teams/{key}/members | replace member graph | owner/admin |
| PUT | /tenant/agent-teams/{key}/bindings | bind Feishu accounts/groups | owner/admin |
| GET | /tenant/agent-teams/{key}/runs | list team runs | owner/admin；成员只读授权 runs |
| GET | /tenant/agent-teams/{key}/runs/{run_id} | run timeline/mailbox/evidence | tenant/team policy |
| POST | /tenant/agent-teams/{key}/runs/{run_id}/cancel | cancel run | owner/admin；发起人 |
| GET | /tenant/channel-accounts | list safe bot metadata | owner/admin |

runtime request：

~~~json
{
  "prompt": "写一篇新品介绍",
  "profile_id": "copywriter",
  "profile_version": 3,
  "profile_overrides": {"language": "zh-CN"}
}
~~~

解析失败必须在 provider 调用前返回结构化 4xx，不能静默切换用户 profile。

Team run 请求必须携带 `team_key`、`team_version` 或明确的 channel binding，不能只传 external chat id 让服务端猜测团队。普通 Feishu 入站由 Team Router 负责补齐该上下文。

API 实现必须同步 swagger annotations/types、docs/docs.go、docs/swagger.json、docs/swagger.yaml、docs/api_server.md，并运行：

~~~bash
swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency
~~~

## 8. WebUI Design

新增一级导航 Agent Profiles，不放在 Knowledge -> Profile：用户画像是上下文数据，Agent Profile 是可发布执行策略。

二级工作区：

1. Catalog：内置、共享、我的私有 profile。
2. Editor：Basics、Persona、Context、Capabilities、Execution、Safety、Raw JSON。
3. Effective Preview：Requested、Tenant policy、Effective runtime 三列 diff。
4. Versions：diff、发布人、时间、回滚。
5. Assignments：给自己或租户用户分配。

新增 Team 工作区：

1. Team Catalog：团队状态、编排模式、成员数、绑定群数、最近运行。
2. Team Builder：从当前可见 published profile 中选择成员，固定 profile version，设置 coordinator/reviewer/researcher/coder 角色。
3. Bot & Group Binding：选择安全的 Feishu channel account，填写或读取已授权的 external chat/group，设置 mention/command/internal_only trigger。
4. Run Monitor：查看 TeamRun timeline、成员状态、mailbox/evidence 摘要、预算消耗、重试、cancel 和最终卡片。
5. Replay & Diff：对比 Team version、成员 profile version、effective hash 和一次运行的输出证据。

交互要求：

- Catalog 支持 key/name 搜索、scope/status 筛选、version/hash 展示。
- 行操作按角色显示；draft、published、archived 状态清晰可见。
- Editor 有 dirty、validating、valid/invalid、publishing、published 状态。
- Raw JSON 仅管理员调试使用，仍走同一 validator。
- Preview 显示被移除 tool/skill/MCP、被降低 tokens/turns、被拒绝 mode/workspace/git/sandbox、hash 和 reason code。
- assignment 只影响新 session；已有 session 固定原 profile version/hash。
- Team Builder 不能直接输入 app secret；只显示经过权限校验的 bot account metadata（account key、app id 摘要、health、provider）。
- 同一 Feishu 群可以显示多个 bot 成员，但 UI 必须标记唯一 coordinator、internal_only bot 和最终输出 bot，避免用户误以为每个机器人都会独立回答。
- Team 发布前必须展示成员 profile version、bot account、group binding、trigger、预算和 loop guard 预览；未通过 validation 不能 publish。
- Run Monitor 不展示其他成员的未授权私密 prompt 或完整 mailbox payload，只展示按 team policy 脱敏后的 evidence/summary。
- desktop/mobile 不允许控件重叠，保留中英文 i18n。

计划文件：

| 文件 | 职责 |
|---|---|
| web/src/components/AgentProfilesPanel.tsx | 工作台容器 |
| web/src/components/AgentProfileEditor.tsx | 分组表单 |
| web/src/components/AgentProfilePreview.tsx | effective diff |
| web/src/components/AgentTeamsPanel.tsx | Team catalog、builder、run monitor |
| web/src/components/AgentTeamBuilder.tsx | members、roles、bot/group bindings |
| web/src/components/AgentTeamRunPanel.tsx | timeline、mailbox/evidence、budget、cancel |
| web/src/hooks/useAgentProfilePanel.ts | server state/lifecycle |
| web/src/hooks/useAgentTeamPanel.ts | team state/lifecycle |
| web/src/lib/api.ts | typed API client |
| web/src/lib/types.ts | API/domain types |
| web/src/App.tsx | nav/role gating |
| web/src/lib/i18n.tsx | 中英文 |
| web/src/styles/app.css | table/form/diff |
| web/src/components/AgentProfilesPanel.test.tsx | component tests |
| web/e2e/agent-profiles.spec.ts | desktop/mobile smoke |

## 9. Audit and Observability

审计事件：

~~~text
tenant.agent_profile.create/update/validate/publish/archive/rollback/assign/override_blocked
tenant.agent_team.create/update/validate/publish/archive/member_changed/binding_changed/run_started/run_completed/run_failed/run_cancelled/mailbox_blocked/bot_loop_blocked
~~~

metadata 只保存 actor、tenant、key/version/hash、blocked fields/reason code，不保存 persona 全文、token、API key 或 MCP secret。

现有 prompt_context/runtime trace manifest 增加：

~~~text
agent_profile_key
agent_profile_version
agent_profile_source
agent_profile_requested_hash
agent_profile_effective_hash
agent_profile_prompt_mode
agent_profile_runtime_profile
agent_profile_tool_count
agent_profile_blocked_override_count
agent_team_key
agent_team_version
agent_team_run_id
agent_team_orchestration_mode
agent_team_member_count
agent_team_coordinator_member
agent_team_bound_account_count
agent_team_bound_chat_count
agent_team_mailbox_message_count
agent_team_blocked_loop_count
~~~

观测必须区分三个时间面：profile/team 配置版本时间、TeamRun 执行时间、Feishu delivery/readback 时间。不能用 bot 的群消息时间代替 runtime completion；每条外部输出必须关联 `team_run_id`、`member_key`、`account_id`、`outbox_id` 和 provider receipt。

## 10. Delivery Estimate, File Budget and Risk Register

### 10.1 Schedule estimate

估算基于已有 tenant、channel、Feishu Adapter、Inbox/Outbox、query runtime 和 WebUI 基础设施，不包含重新建设认证、计费或消息平台。

| 阶段 | 交付内容 | 单人串行 | 2 后端 + 1 前端 + QA |
|---|---|---:|---:|
| P0 | 基线、接口锁定、测试夹具 | 1-2 天 | 1-2 天 |
| P1 | Profile domain、schema、resolver、applier | 5-8 天 | 3-5 天 |
| P2 | MySQL、tenant service、API、Swagger | 5-8 天 | 3-5 天 |
| P3 | Team 控制面、fake channel、mailbox、orchestrator | 7-12 天 | 5-8 天 |
| P4 | 单飞书 bot、channel binding、真实 Inbox/Outbox 联调 | 5-8 天 | 4-6 天 |
| P5 | 多 bot 同群、loop guard、并行协作、coder workspace | 5-9 天 | 4-7 天 |
| P6 | WebUI Profile/Team 工作台、浏览器测试 | 6-10 天 | 4-7 天 |
| P7 | MySQL/飞书/模型真实验收、灰度和回滚演练 | 5-8 天 | 4-6 天 |

结论：

- 只做 Profile 基础能力：约 2-3 周单人，1-2 周小组。
- Profile + WebUI：约 3-4 周单人，2-3 周小组。
- 完整多 bot Team：约 6-9 周单人，4-6 周小组。
- 如果没有稳定的飞书测试应用、隔离 MySQL 和可用模型 provider，需额外预留 1-2 周环境与联调时间。

以上是工程预算，不是对模型响应耗时或外部平台稳定性的承诺；P3 之前的 Profile 能力可以独立交付，不应等待 Team 完成。

### 10.2 File budget

计划预计约 55-65 个文件触点，按以下预算管理：

| 类型 | 预计数量 | 主要范围 |
|---|---:|---|
| 新增 Profile/Team 生产代码 | 13-16 | `internal/agentprofile`、`internal/agentteam`、tenant/server handlers |
| 新增 migration | 4 | `000015`、`000016` up/down |
| 新增测试和 E2E | 8-12 | domain、repository、runtime、channel、server、WebUI、live E2E |
| 修改现有后端/runtime/channel | 15-20 | query、CLI、server、tenant、channel、storage |
| 修改 WebUI 现有文件 | 6-8 | App、API/types、i18n、styles、现有 inspector/navigation |
| Swagger、拓扑和 API 文档 | 5-8 | generated docs、architecture registry/diagram、api_server |

文件数量是设计预算。实现时允许拆分大文件，但不得为了凑数量新增无职责边界的 wrapper；每个新文件应有单一责任。

### 10.3 Risk register

| 风险 | 等级 | 真实后果 | 主要控制 |
|---|---|---|---|
| 多 bot 回声和重复 fan-out | P0/很高 | 一个群消息启动多个 TeamRun，机器人无限互答 | Team Router 单归属、Inbox 幂等、bot loop guard、coordinator-only output |
| 跨 tenant/profile/channel 越权 | P0/很高 | 用户使用别的租户 profile、bot 或 workspace | 所有 query 带 tenant/user/account scope；负向鉴权测试；server hard policy |
| Profile/Team version 漂移 | P1/高 | 已发布 Team 静默使用新 prompt 或新工具 | profile/team version pin、publish 生成新版本、hash manifest |
| coder 成员并发写 workspace | P0/很高 | 文件覆盖、Git 状态污染、不可恢复 shared state | workspace lock、单 writer policy、默认禁止并发 destructive action |
| 飞书 delivery unknown | P1/高 | 网络超时后重复发送最终卡片或无法确认发送结果 | Outbox idempotency、delivery_unknown、provider receipt/readback、补偿任务 |
| 模型非确定性导致 Team 结论不稳定 | P1/高 | 成员结果冲突、coordinator 过早宣称完成 | structured evidence、completion gate、失败/partial 状态、deterministic fake model |
| Profile 误污染 code 主流程 | P1/高 | CLI/TUI 加载 tenant prompt、工具或群组配置 | profile-less code regression、入口隔离、默认 flag 关闭 |
| migration/旧 API 兼容回归 | P1/高 | 旧客户端、旧 transcript、旧 tenant schema 无法使用 | additive migration、旧字段保留、Swagger/API regression、readback |
| Team mailbox 泄露私密内容 | P0/高 | 不应可见的成员 prompt、凭据或 workspace 输出进入群组 | payload 引用/加密、team policy 脱敏、外部只发 accepted summary |

总体风险为高，但可以通过 P1 Profile 和 P3 Team fake channel 分阶段降低；未通过 P3 的幂等与权限 Gate，不允许接入真实多 bot 群组。

## 11. Implementation Tasks

### Task 1: Domain contract

**Files:** Create internal/agentprofile/model.go, schema.go, builtin.go; tests in internal/agentprofile。

- [ ] 写 defaults、canonical hash、unknown-field、dependency 失败测试。
- [ ] 实现五类 policy、EffectiveAgentProfile、ValidationReport。
- [ ] 实现 chat-assistant、copywriter、coder。
- [ ] Run go test ./internal/agentprofile -count=1。
- [ ] Commit: feat: define agent profile domain contract。

### Task 2: Schema and repository

**Files:** Create migrations/mysql/000015_agent_profiles.up.sql/down.sql and migrations/mysql/000016_agent_teams.up.sql/down.sql; modify internal/storage/mysql/models.go, gorm_repository.go, channel_repository.go, schema_test.go。

- [ ] 测试 profile、surface assignment、profile bot binding、team、member、binding、run、mailbox 的字段、外键、unique/index 和 down 顺序。
- [ ] 实现 version CRUD、publish/archive/rollback、assignment upsert/get。
- [ ] 每次查询带 tenant/user scope；有 DSN 时做 MySQL readback。
- [ ] Commit: feat: persist versioned agent profiles。

### Task 3: Resolver and applier

**Files:** Create internal/agentprofile/catalog.go, resolver.go, applier.go; modify internal/tenant/service.go; create internal/tenant/agent_profiles.go。

- [ ] 实现 builtin/shared/private catalog merge。
- [ ] 实现 precedence、assignment、published-only、capability intersection。
- [ ] 编译成现有 query/runtime/tool/permission/sandbox inputs。
- [ ] 测试 chat/code isolation、bare、denied tools、quota、blocked override。
- [ ] Commit: feat: resolve effective agent profiles。

### Task 4: Team orchestration and Feishu bindings

**Files:** Create internal/agentteam/model.go, validator.go, router.go, orchestrator.go, mailbox.go; modify internal/channel/runtime/service.go, internal/channel/feishu/adapter.go, internal/storage/mysql/channel_repository.go; tests in internal/agentteam and internal/channel/runtime。

- [ ] 实现 Team lifecycle、coordinator/parallel_review policy、member profile version pin 和 Team effective hash。
- [ ] 实现 channel account + external chat/thread -> Team binding、mention/command/internal_only trigger 和 unique route。
- [ ] 实现 durable TeamRun、lease、budget、cancel、timeout、member failure、partial evidence 和 mailbox idempotency。
- [ ] 实现 bot loop guard：忽略自身/同 Team bot 普通消息，仅接受受信 internal coordination envelope。
- [ ] 复用现有 Feishu Inbox/Outbox/Card/Reaction；不复制凭据，不把 internal mailbox payload 写进外部群消息。
- [ ] 测试同群多 bot、重复事件、跨 tenant/account、重复输出、delivery retry/readback、coder workspace lock。
- [ ] Commit: feat: add agent team orchestration over Feishu。

### Task 5: API and Swagger

**Files:** Create internal/server/handlers_agent_profiles.go and handlers_agent_teams.go; modify server routes, server.go, swagger files, docs/api_server.md。

- [ ] 实现 list/create/get/update/validate/publish/archive/rollback/assignment/effective。
- [ ] 实现 profile bot-binding get/put/delete，并校验 channel account、profile version 和 tenant 一致。
- [ ] 实现 Team list/create/get/versions/update/validate/publish/archive/rollback/member/binding/runs/cancel，以及安全 channel account metadata API。
- [ ] 实现 owner/admin、private owner、member read/use 矩阵。
- [ ] 增加 audit、structured errors、tenant isolation、draft invisibility tests。
- [ ] Run swag init and go test ./internal/server ./internal/tenant -count=1。
- [ ] Commit: feat: expose agent profile and team APIs。

### Task 6: Runtime wiring

**Files:** modify internal/cli/cmd_server.go, internal/cli/cli.go, internal/query/promptdump.go, internal/channel/runtime/service.go and tenant/channel session metadata writers。

- [ ] 增加 profile_id/profile_version/profile_overrides，保留旧字段。
- [ ] 在 provider 前解析并拒绝 invalid profile；profile-less 请求兼容。
- [ ] metadata 写 key/version/hash；验证 chat 不读本地 code context。
- [ ] channel TeamRun 写 key/version/hash/team/run/member metadata；CLI/TUI 不查询 Team assignment。
- [ ] Run go test ./internal/query ./internal/cli ./internal/server -count=1。
- [ ] Commit: feat: wire profiles and teams into runtime。

### Task 7: WebUI

**Files:** create AgentProfilesPanel.tsx, AgentProfileEditor.tsx, AgentProfilePreview.tsx, AgentTeamsPanel.tsx, AgentTeamBuilder.tsx, AgentTeamRunPanel.tsx, useAgentProfilePanel.ts, useAgentTeamPanel.ts and tests; modify App.tsx, api.ts, types.ts, i18n.tsx, app.css。

- [ ] 实现 Catalog、Editor、Preview、Versions、Assignments。
- [ ] 实现 Team Catalog、Builder、Bot/Group Binding、Run Monitor、Replay/Diff。
- [ ] 添加 role-gated actions、dirty/validation/publish 状态和 i18n。
- [ ] Run npm --prefix web run test、build、test:e2e。
- [ ] Commit: feat: add profile and team WebUI。

### Task 8: Acceptance and rollout

- [ ] 增加 manifest、audit、blocked override assertions。
- [ ] 覆盖 create -> validate -> publish -> assign -> query -> readback。
- [ ] 增加跨租户、draft execution、tool expansion、code escalation、sandbox bypass negative tests。
- [ ] 增加 Team negative tests：bot loop、duplicate fan-out、unpublished member、wrong group/account、mailbox replay、multiple writer workspace。
- [ ] 覆盖同一群组中 copywriter/researcher/coder 多 bot 协作，验证 coordinator-only final output 和 member evidence readback。
- [ ] 新增 runtime package/causal edge 后同步更新 docs/architecture/runtime_topology.yaml、global_runtime_topology.md 和对应 Mermaid 图；若只复用已登记 channel/query 节点，记录 `Topology impact: updated` 的具体理由。
- [ ] 测量 prompt bytes、tokens、turns、tool calls、p50/p95 latency、cache break。
- [ ] AGENT_PROFILES_ENABLED=false、AGENT_TEAMS_ENABLED=false 默认关闭，先内部 tenant 灰度。
- [ ] Commit: test: add profile and team acceptance coverage。

## 12. Acceptance Matrix

验收采用逐 Gate 放行，禁止跳过前置 Gate 直接做多 bot 真实联调。

### Gate 0: Baseline freeze

- 记录当前 `go test ./... -count=1`、`git diff --check`、WebUI test/build 的基线结果。
- 记录 code/chat prompt manifest、工具数量、默认 max tokens/turns、主要 latency 和 cache 指标。
- 确认 Profile/Team feature flag 默认关闭时，CLI/TUI/API 旧路径行为不变。
- 产出隔离 MySQL、fake provider、fake channel、测试 tenant/user/account 的固定夹具。

### Gate 1: Profile domain and runtime

- canonical JSON 对 key 顺序和空白稳定；未知字段、非法 enum、超限值和非法依赖拒绝。
- requested tools 始终与 tenant/server policy 求交集；deny/hard policy 优先。
- `chat` profile 看不到 cwd、Git、CLAUDE.md、local skills；`coder` profile 只能访问显式 roots。
- profile-less CLI/TUI 和旧 API 的 prompt、tool registry、permission gate 不回归。
- draft/validating profile 永远不能进入 provider；published version/hash 可回读。

### Gate 2: Persistence and API

- profile、surface assignment、bot binding、Team、member、group binding、run、mailbox 全部带 tenant scope。
- publish 单调递增；rollback 创建新版本，不修改历史；assignment 只能绑定 published version。
- 401、403、404、跨租户 key、错误 owner、错误 account、错误 external chat 全部有稳定错误响应。
- OpenAPI/Swagger、API docs、audit event 和 session metadata 同步。
- 隔离 MySQL 直查确认 version、hash、assignment、audit 的真实落库值。

### Gate 3: Team fake-channel integration

- 一个 `(tenant_id, provider_event_id)` 最多生成一个 TeamRun；重试只恢复 lease，不重复启动成员 query。
- mailbox 的 `run_id/from/to/sequence/idempotency_key` 顺序和 CAS 状态正确。
- coordinator/parallel_review 的成员并行数、总 tokens、turns、timeout、cancel 和 quota ceiling 生效。
- 成员失败、超时、取消、partial evidence、coordinator synthesis 失败都能收敛到明确终态。
- 只有 coordinator 的 accepted final 可成为最终声明；成员输出不能绕过 completion gate。

### Gate 4: Single-bot Feishu integration

- mention/command/DM trigger、tenant membership、group actor authorization 和 account ownership 正确。
- Feishu Inbox/Outbox/Card/Reaction/permission callback 在重试和进程重启后可恢复。
- delivery retry、delivery_unknown 和 provider receipt/readback 不产生重复最终消息。
- bot 自己发送的普通消息不会重新创建 TeamRun；internal coordination envelope 必须通过 run/correlation 校验。

### Gate 5: Multi-bot same-group integration

- 同一群可以绑定多个 bot，但一个 inbound event 只归属一个 TeamRouter/TeamRun。
- `internal_only` bot 不响应普通群消息；非 coordinator 不直接发布最终结果。
- copywriter/researcher/coder 多 bot 协作时，外部只看到受控阶段摘要或一个 accepted final。
- bot loop、duplicate query、duplicate outbox、未授权 member output、mailbox replay 均为 0。
- coder Team 的 workspace lock/单 writer policy 阻止并发文件写入和未授权 Git shared-state 操作。

### Gate 6: WebUI and live acceptance

- owner/admin 可以创建、校验、发布、回滚 Profile/Team，绑定 bot/group，查看 run 和 replay。
- member 只能操作授权的 private profile 或被授权 Team；raw JSON 不能绕过 validator。
- desktop/mobile 关键操作无重叠；刷新、网络失败、重复提交、离开 dirty 页面状态正确。
- 真实 MySQL + API Server + 测试飞书应用 + 可用模型完成完整链路：

~~~text
create profile -> publish profile -> create Team
-> pin members -> bind bots/group -> mention group
-> TeamRun -> member evidence -> coordinator final
-> Feishu Outbox -> provider receipt/readback -> audit/trace/DB readback
~~~

### 12.1 Hard invariants and metrics

以下安全和一致性指标必须为 0：

- 跨 tenant/user/account/profile/team 访问。
- draft 或 archived version 被执行。
- 未授权 tool expansion、sandbox/permission bypass。
- 同一 inbound event 重复创建 TeamRun。
- bot loop、duplicate member fan-out、duplicate final external message。
- 未经 coordinator/completion gate 的 accepted final。
- 未授权 mailbox/prompt/credential/workspace 内容进入外部群组。

以下字段必须 100% 可追溯：

~~~text
tenant_id
profile_key/version/requested_hash/effective_hash
team_key/version/effective_hash
team_run_id/member_key
channel_account_id/external_chat_id
inbox_event_id/outbox_id/provider_message_id
~~~

成本与性能需要记录基线及变化：prompt bytes、input/output tokens、turns、tool calls、Team member parallelism、mailbox backlog、p50/p95 duration、Feishu delivery latency、cache hit/break、quota usage。

### 12.2 Test layers

| 层级 | 测试内容 | 通过标准 |
|---|---|---|
| Domain unit | schema、canonical hash、policy merge、validator、builtin profile | 规则和错误码稳定，边界全覆盖 |
| Repository/migration | migration text、GORM CRUD、并发 publish、assignment、mailbox CAS | tenant scope 正确，历史版本不可变 |
| API/security | auth、role、tenant isolation、draft/published、bot/group ownership | 所有负向路径拒绝且可审计 |
| Runtime deterministic | scripted model、ProfileApplier、Team orchestrator、completion gate | tool/prompt/evidence/终态符合预期 |
| Channel contract | fake Feishu SDK、mention normalization、Inbox/Outbox、callback、receipt | 重试、重启、delivery_unknown 可恢复 |
| WebUI component | 表单校验、preview diff、role gating、dirty/error/loading | 不能绕过后端 validator，状态无丢失 |
| Browser E2E | desktop/mobile catalog/editor/team builder/run monitor | 关键 workflow 可操作且无布局重叠 |
| Live E2E | 隔离 MySQL、真实 API Server、测试飞书 bot、真实 provider | 完整链路和 readback 成功 |
| Regression | 旧 code/chat、CLI/TUI、Mobile/OpenAI、channel 既有测试 | profile/team flag 关闭时无行为变化 |

推荐执行顺序是先跑 Domain/Repository，再跑 API/Runtime fake，再跑 Channel contract，最后才跑 Live E2E；不能用单一 fake model 结果替代真实 provider 验收。

- canonical JSON 对 key 顺序/空白稳定；未知字段、invalid enum、超限值拒绝。
- requested tools 始终与 tenant/server policy 求交集；deny/hard policy 优先。
- chat profile 看不到 cwd、Git、CLAUDE.md、local skills。
- coder profile 只能访问显式 roots，并保留 permission/gate。
- draft 不进入 runtime catalog；publish 单调递增；rollback 不修改历史。
- assignment 只能指向 published；archive 前必须替换 assignment。
- assignment 唯一键包含 surface；多个 profile 可同时存在且互不覆盖。
- Team member 固定 published profile version；profile 新版本不会隐式改变已发布 Team。
- 同一 Feishu group 可绑定多个 bot，但一个 inbound event 只能归属一个 TeamRun。
- Team coordinator-only final、mailbox idempotency、bot loop guard、Outbox delivery/readback 必须通过。
- 所有 list/get/update/assignment/team/binding/run/mailbox 查询带 tenant context。
- owner/admin 可完整管理；member 只能使用可见 published/private self profile。
- Preview 显示 blocked reason；desktop/mobile 无控件重叠。

验证命令：

~~~bash
go test ./internal/agentprofile ./internal/agentteam ./internal/channel/... ./internal/tenant ./internal/storage/mysql ./internal/server ./internal/query ./internal/cli -count=1
go test ./... -count=1
git diff --check
npm --prefix web run test
npm --prefix web run build
npm --prefix web run test:e2e
~~~

隔离 MySQL 还需执行 tenant migrate up 和 `TestMySQLE2EAgentProfile`、`TestMySQLE2EAgentTeamFeishu`，直查 profile/team version、surface assignment、member/binding、TeamRun/mailbox、channel outbox、session metadata、audit 和 provider receipt。

## 13. Rollout, Rollback, Completion Gate

1. 先发布 profile/team schema 和只读 catalog/API，`AGENT_PROFILES_ENABLED`、`AGENT_TEAMS_ENABLED` 默认关闭。
2. 内部 tenant 开启 builtin profiles；profile-less 请求不变。
3. owner/admin create/validate/publish 通过后再开放 member private profile。
4. profile assignment-based default 只对明确 opt-in tenant/user/surface 生效；Team 必须单独 opt-in channel binding。
5. 先启用单 bot Team，再启用同群多 bot、parallel_review 和 coder workspace；每一步观察 bot loop、duplicate fan-out 和 delivery retry。
6. 监控 resolution error、blocked override、TeamRun failure/timeout、mailbox backlog、prompt bytes、token/turn/tool cost、latency、cache break、Feishu delivery/readback。
7. 回滚只关闭对应 flag 并停止新 TeamRun；正在运行的 TeamRun 按 cancel/blocked 状态收敛，新请求回到旧路径，profile/team 数据保留，不在应用回滚中 drop tables。

完成必须同时满足：

- tenant admin 在 WebUI 完成创建、校验、发布、分配、回滚。
- session/TeamRun manifest 证明 profile/team key/version/hash，外部消息可回查 outbox/provider receipt。
- chat 不访问本地 code context；coder 保持预期工具和 safety gate。
- 跨租户、draft execution、tool expansion、sandbox bypass negative tests 通过。
- 多 bot 同群协作不发生 bot loop、重复 query、未授权 member output 或重复外部发送。
- MySQL readback、Swagger、API docs、compatibility matrix、WebUI tests 同步。
- Go 全量测试、git diff check、WebUI test/build/e2e 通过。

## 14. Decisions Before Implementation

已锁定：独立 profile/team 持久化；旧画像保留；仅 published 执行；能力求交集；surface assignment；Team 成员 pin profile version；Feishu bot credentials 只留在 channel account；内部协作走 mailbox；首版 profile/team opt-in；WebUI 结构化表单 + admin raw JSON。

实现 Task 4 前确认：

- member 首版是否允许 private profile。
- 安全 MCP registry key 和 provider/model allowlist 来源。
- assignment 只影响新 session（推荐）；Team binding 只影响新的 TeamRun。
- archived version/audit 保留期限。
- 同一 Feishu 群多 bot 的 coordinator bot 是否固定，还是允许 TeamPolicy 在每轮选举（V1 推荐固定）。
- coder Team 是否允许真实 Git shared-state 操作（V1 推荐只读或显式 permission card + workspace lock）。
- Team mailbox payload 的加密密钥来源、保留期限和成员可见性。

## 15. Execution Status (2026-08-25)

- [x] Task 1-6 已按独立提交实现并推送到 `main`；profile-less code/chat 兼容路径保留。
- [x] Task 7 WebUI 已实现 Profile Catalog/Editor/Preview/Versions/Assignments/Bot binding，以及 Team Catalog/Builder/Bindings/Run Monitor/Replay；Vitest、build、mock Playwright 和 desktop/mobile smoke 通过。
- [x] Task 8 deterministic hardening 已实现：nested Team policy decode、replace 先归档、TeamRun pinned version check、token ceiling、timeout terminal state、actor authorization/bot account/output conflict validator、started/finished usage metadata。
- [x] Task 8 acceptance/compatibility/topology 文档已同步：见 `docs/architecture/agent_profile_team_acceptance.md`。
- [ ] Task 8 真实 MySQL/provider/Feishu Gate：当前环境缺 `GOLANG_CC_MYSQL_E2E_DSN`、测试飞书应用和可用 provider，不能宣称真实多 bot readback 已通过。
