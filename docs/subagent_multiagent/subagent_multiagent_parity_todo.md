# Subagent / Multi-Agent 功能复刻 TODO

本文档只跟踪 Claude Code subagent / multi-agent 的功能复刻，不追求源码逐行一致。目标是让 Go Claude 在用户可感知能力、配置语义、运行时隔离、工具权限、后台协作、观测和测试证据上达到功能等价。

## 目标边界

功能等价包含：

- `.claude/agents/*.md` agent 定义字段和语义对齐。
- `Task` / Agent 调用子 agent 的能力对齐。
- 子 agent 独立 prompt、model、tools、MCP、skills、memory、权限和 turn 限制。
- async/background agent、in-process teammate、coordinator 模式和跨 agent 消息能力。
- 子 agent hooks、trace、transcript、stream-json、tenant task 持久化和取消能力。
- 可重复的 deterministic eval、golden、API/TUI/CLI smoke 验证。

不包含：

- 上游私有源码结构逐行复刻。
- Anthropic 内部 feature gate、GrowthBook 私有实验名、内部账号判断的完全复刻。
- 未公开内部模型或私有工具的 1:1 实现；这些只做公开等价行为和清晰降级。

## 当前现状

| 能力 | 当前状态 | 证据 |
| --- | --- | --- |
| `Task` tool | 已有同步单任务和 batch 运行，支持 `subagent_type`、priority、retry、timeout、max concurrency。 | `internal/tools/task/task.go` |
| 子 agent loop | 已有独立 Messages loop、tool_use/tool_result、transcript、task events、cancel check、`SubagentStop` hook。 | `internal/agentruntime/runtime.go` |
| agent 发现 | 已读取 `~/.claude/agents`、项目 `.claude/agents`、plugin agent dirs。 | `internal/agents/agents.go` |
| agent schema | 已支持 `name`、`description`、`tools`、`disallowedTools`、`mcpServers`、`skills`、`initialPrompt`、`maxTurns`、`background`、`memory`、`effort`、`permissionMode`、critical reminder 和未知字段保留。 | `internal/agents/agents.go` |
| tool policy | agent `tools`/`disallowedTools` 可过滤 registry；guarded tools 会执行 agent policy 和 agent permission mode。 | `internal/agentruntime/runtime.go`、`internal/tools/guarded.go` |
| context sources | agent-local MCP tools、skills preload、agent scoped memory 已接入子 agent system prompt 和局部 registry。 | `internal/agentruntime/runtime.go`、`internal/memory/memory.go` |
| task 持久化 | MySQL tenant agent task / event 已有。 | `internal/agenttasks`、`migrations/mysql/000001_multi_tenant.up.sql` |
| CLI/API 管理 | 可 list/events/cancel tenant agent tasks。 | `internal/cli`、`internal/server` |
| eval | deterministic agent eval 已覆盖 subagent 基本链路、subagent/multi-agent parity 和真实 query-loop multi-agent E2E 场景。 | `internal/agenteval` |

## 上游功能面差距矩阵

| 功能面 | 上游能力 | Go 当前差距 | 优先级 |
| --- | --- | --- | --- |
| Agent schema | `description/tools/disallowedTools/prompt/model/mcpServers/criticalSystemReminder_EXPERIMENTAL/skills/initialPrompt/maxTurns/background/memory/effort/permissionMode` | 字段解析和输出已覆盖；`maxTurns`、`background`、`memory`、`effort` metadata、`permissionMode`、main-thread `initialPrompt` 已接入运行时。 | P0 |
| schema 校验 | zod schema、数组/枚举/继承语义、错误报告。 | YAML frontmatter 已支持数组和对象，未知字段保留；`agents doctor [--json]` 可报告空 prompt、无效 `maxTurns`、无效 `permissionMode`、未知字段、allow/deny 冲突、异常 memory/effort 等 authoring 问题。 | P2 |
| tool policy | async agent、custom agent、coordinator、in-process teammate 各有 allow/deny 集合，阻止递归工具。 | agent allow/deny、permissionMode 和 coordinator allowlist 已接入；执行层会拒绝未暴露工具和被 deny 工具。 | P0 |
| MCP per-agent | agent 可声明 MCP server spec。 | agent-local MCP tools 已局部注册；scalar 引用仅作为 inherited MCP prompt context，MCP resources/read 仍沿用全局入口。 | P0 |
| skills preload | agent 可声明预加载 skills。 | 已加载指定 `SKILL.md` 到子 agent system prompt；skill runtime metadata 不污染 main thread。 | P0 |
| initialPrompt | main-thread agent 可自动提交首轮 prompt，并处理 slash commands。 | `--agent <name>` 会加载 main-thread agent prompt，并把 `initialPrompt` 作为首轮 user prompt 前缀；slash command 专用执行语义仍按普通 prompt 文本进入 query。 | P1 |
| maxTurns | agent 定义可覆盖 max turns。 | 已接入子 agent runtime，task metadata 记录 effective values。 | P0 |
| background | agent 可作为非阻塞后台任务运行。 | `Task` 单任务和 batch 已支持 agent schema `background: true`，返回 task handle 并复用 agent task events/cancel；CLI print-mode background 仍是独立能力。 | P0 |
| agent memory | user/project/local agent memory scope。 | 已支持 `~/.claude/agent-memory/<agent>/MEMORY.md`、`.claude/agent-memory/<agent>/MEMORY.md`、`.claude/agent-memory-local/<agent>/MEMORY.md`。 | P0 |
| effort/thinking | agent 可声明 reasoning effort。 | 已解析并记录 effective metadata / active agent runtime 字段；`SMA-106` 已把 effort 纳入 cache-safe thinking signature；Anthropic provider 已把 agent/skill effort 映射到 SDK thinking config，OpenAI-compatible provider 保持标准 payload 不注入非标准字段。 | P1 |
| permissionMode | agent 可声明工具权限模式。 | 已接入 guarded tools；agent bypass 降级为 allow，不能越过 parent/session deny。 | P0 |
| critical reminder | agent 可追加 critical system reminder。 | 已注入子 agent system prompt。 | P1 |
| Agent tool / async tools | create/get/list/update/send message/task stop 等 agent 管理工具。 | 已支持 `AgentCreate`、`AgentList`、`AgentGet`、`AgentStop`、`AgentMessage`；`update` 留给任务 metadata 扩展。 | P0 |
| coordinator mode | coordinator 只能使用 agent/output/message 管理工具，负责分派与汇总。 | `CoordinatorPrompt` 生效时只暴露 agent/task output/message/clarification/todo 协调工具；普通 Read/Bash/Write 不进入模型工具定义，硬调也会 unknown tool。 | P1 |
| in-process teammates | 部分 agent 同进程协作、pending messages、cron trigger。 | `AgentCreate mode=teammate` 复用同进程 background runtime，task metadata 标识 teammate；支持 message bus、progress summary 和 AgentStop 取消；cron trigger 不属于当前 subagent/multi-agent parity 主干，已有 `/loop` scheduler 覆盖定时后台任务。 | P1 |
| hooks | `SubagentStart`、`SubagentStop` payload 含 `agent_id`、`agent_type`、`agent_transcript_path` 等。 | 已支持 `SubagentStart`；`SubagentStop` 已包含 `agent_id`、`agent_type`、`agent_transcript_path`、`last_assistant_message`、status、duration。 | P0 |
| stream / progress | nested progress、agent ids、parent ids、async task state。 | 已有 nested progress 事件；AgentGet 可返回 task event progress summary；TUI 显示子 agent 状态、task id、usage/cache/message/progress 和 stop id 提示。 | P1 |
| prompt cache | fork/subagent cache-safe params 保持 system/tools/model/message prefix/thinking config 一致。 | 已新增 request-level cache-safe signature，覆盖 model/system/cache control/tools/message prefix/thinking effort；主线程 observability 和 subagent `cache_state` event 可追踪变化，避免误判可复用。 | P1 |
| observability | agent task、message、hooks、permission、usage、cost、trace tree 全链路。 | 已有 task/events/message trace 基础；子 agent 已发 agent/model/tool telemetry、usage/cost task event，Trace API 可展示 agent span tree 和 token 归属；更细前端展示归入 `SMA-108/204`。 | P1 |

## TODO Backlog

### P0: 功能等价主干

| ID | 状态 | 模块 | 待办 | 验收标准 | 测试 |
| --- | --- | --- | --- | --- | --- |
| SMA-001 | DONE | agents | 引入完整 AgentDefinition 结构，解析 `disallowedTools`、`mcpServers`、`skills`、`initialPrompt`、`maxTurns`、`background`、`memory`、`effort`、`permissionMode`、`criticalSystemReminder_EXPERIMENTAL`。 | `.claude/agents/*.md` 支持 YAML frontmatter 复杂数组/对象；旧 `tools: Read, Grep` 写法兼容；未知字段保留在 `unknown_fields`，供后续 strict/warning 模式使用。 | `go test ./internal/agents -count=1` |
| SMA-002 | DONE | agents | 增加 agent schema 校验与 show/list 输出扩展。 | `agents show` 能显示所有已解析字段；解析错误包含 agent 文件路径；JSON 输出有 golden 覆盖；`agents doctor [--json]` 输出 authoring diagnostics。 | `go test ./internal/agents ./internal/cli -count=1` |
| SMA-003 | DONE | agentruntime | agent 级 `maxTurns`、`model: inherit`、`effort`、critical reminder 接入子 agent request。 | 子 agent runtime 按 agent 字段覆盖运行参数；result 和 task metadata 记录 effective values；`effort` 会生成 provider thinking config 并参与 cache-safe signature。 | `go test ./internal/agentruntime ./internal/tools/task -count=1` |
| SMA-004 | DONE | tools/policy | 实现 agent tool policy resolver：parent tools、agent `tools` allow、agent `disallowedTools` deny、async/custom/coordinator 默认 deny 集合按优先级合并。 | Agent `tools` 和 `disallowedTools` 会共同过滤 sub-agent registry；guarded tools 也会再次执行 agent allow/deny。coordinator allowlist 已在 `SMA-103` 接入，async/custom 默认集合仍随后续 teammate 语义补齐。 | `go test ./internal/tools ./internal/agentruntime -count=1` |
| SMA-005 | DONE | permissions | agent 级 `permissionMode` 接入 tool context。 | 子 agent 可覆盖为 ask/deny/allow/auto/plan/bypassPermissions；agent bypass 被降级为 allow，不会越过 parent/session 明确 deny；permission audit source 标出 agent default mode。 | `go test ./internal/tools ./internal/agentruntime -count=1` |
| SMA-006 | DONE | mcp | agent 级 `mcpServers` 合并到子 agent registry 和 prompt context。 | agent-local MCP tools 只在该 agent 可见；registry clone 避免污染 parent；scalar MCP 引用作为 inherited context 提示。 | `go test ./internal/agentruntime ./internal/tools -count=1` |
| SMA-007 | DONE | skills | agent 级 `skills` preload。 | 子 agent system prompt 包含指定 skills 的 `SKILL.md` context；skill allowed tools/model/agent metadata 不污染 main thread。 | `go test ./internal/agentruntime ./internal/skills -count=1` |
| SMA-008 | DONE | memory | agent scoped memory：user/project/local。 | 支持 `~/.claude/agent-memory/<agent>/MEMORY.md`、`.claude/agent-memory/<agent>/MEMORY.md`、`.claude/agent-memory-local/<agent>/MEMORY.md`；只注入目标 agent。 | `go test ./internal/memory ./internal/agentruntime -count=1` |
| SMA-009 | DONE | background | agent schema `background: true` 非阻塞运行。 | Task 调用 background agent 返回 `task_id`/`session_id`/`status=running`；复用 agent task store、events、cancel check；后台完成后 result 持久化。 | `go test ./internal/agentruntime ./internal/tools/task -count=1` |
| SMA-010 | DONE | hooks | 补齐 `SubagentStart`，扩展 `SubagentStop` payload。 | hook payload 包含 `agent_id`、`agent_type`、`agent_transcript_path`、`last_assistant_message`、status、duration；hook payload JSON 有测试覆盖。 | `go test ./internal/hooks ./internal/agentruntime -count=1` |
| SMA-011 | DONE | eval | 建立 subagent/multiagent parity eval suite。 | 默认 eval 覆盖 schema/context sources、skills preload、agent memory、background、cancel、hooks、permission mode、AgentCreate/List/Get/Stop、AgentMessage、真实 query-loop multi-agent E2E 和 trace evidence；MCP per-agent 由 runtime 单元测试覆盖。 | `go test ./internal/agenteval -count=1`; `go run ./cmd/golang-cc eval agents --json` |

### P1: 多 agent 协作和长尾语义

| ID | 状态 | 模块 | 待办 | 验收标准 | 测试 |
| --- | --- | --- | --- | --- | --- |
| SMA-101 | DONE | tools/agent | 新增 Agent tool 或等价 async agent management tools：create/get/list/update/stop。 | 主 agent 可创建后台 agent、查询状态、停止任务；输出结构稳定。`update` 将随 message bus/任务元数据扩展继续补齐。 | `go test ./internal/tools/agent ./internal/query ./internal/cli -count=1` |
| SMA-102 | DONE | messaging | agent message bus：send message / pending user messages。 | `AgentMessage` 将消息持久化为 `tenant_agent_task_events.message`；运行中的目标 agent 每轮开始读取 pending messages 并作为 user turn 注入；Session 内按 event id 去重；message event 保留 trace evidence。 | `go test ./internal/agentruntime ./internal/tools/agent ./internal/query ./internal/cli -count=1` |
| SMA-103 | DONE | coordinator | coordinator mode。 | coordinator 只暴露 `Task`、`TaskOutput`、`AgentCreate/List/Get/Stop/Message`、`AskUserQuestion`、`TodoRead/TodoWrite`；普通文件/Bash 工具不会进入 tools definitions，硬调也按 unknown tool 处理。 | `go test ./internal/query ./internal/tools -count=1` |
| SMA-104 | DONE | teammate | in-process teammate runner。 | `AgentCreate mode=teammate` 启动同进程 background runtime；沿用 registry clone/agent policy 隔离工具状态；AgentGet 汇总最近 task events 为 progress summary；AgentStop 可取消。 | `go test ./internal/agentruntime ./internal/tools/agent ./internal/query -count=1` |
| SMA-105 | DONE | prompts | `initialPrompt` main-thread agent 语义。 | `--agent` 加载 agent prompt/model/maxTurns/permissionMode/tools/disallowedTools；`initialPrompt` 会 prepend 到首轮 user prompt；用户显式 `--model` 和 `--permission-mode` 优先。 | `go test ./internal/cli ./internal/query -count=1` |
| SMA-106 | DONE | cache | fork/subagent cache-safe params。 | `promptcache.RequestTracker` 生成 request-level signature；主线程在模型请求前记录 cache-safe hash；子 agent 每轮写入 `cache_state` task event，覆盖 system/tools/model/message prefix/thinking effort 变化。 | `go test ./internal/promptcache ./internal/query ./internal/agentruntime -count=1` |
| SMA-107 | DONE | telemetry | per-agent usage/cost/span tree。 | 子 agent runtime 发 `agent.run`、`agent.model.request`、`agent.tool.execution` telemetry；task event 写入 usage/cost；Trace API span tree 可把 agent/model/tool 挂到 parent query 下并汇总 agent usage。 | `go test ./internal/agentruntime ./internal/server ./internal/telemetry -count=1` |
| SMA-108 | DONE | tui | TUI nested agents UX。 | TUI Sub-agents 面板展示 running/completed/failed/cancelled、task id、agent/model、progress、usage/cache/message、session/elapsed，并对运行中 agent 显示 stop task id 提示。 | `go test ./internal/tui ./internal/cli -count=1` |

### P2: 生态和兼容增强

| ID | 状态 | 模块 | 待办 | 验收标准 | 测试 |
| --- | --- | --- | --- | --- | --- |
| SMA-201 | DONE | plugins | plugin-provided agents 的完整 schema 支持。 | plugin agent 定义复用本地 agent 完整 schema parser；agent 列表暴露 source/plugin；冲突优先级固定为 project agents > plugin agents > user agents。 | `go test ./internal/plugins ./internal/agents -count=1` |
| SMA-202 | DONE | docs | agent authoring guide。 | 文档给出 schema、示例、限制、迁移说明、常见错误，并同步 docs index。 | `git diff --check`; `go test ./... -count=1` |
| SMA-203 | DONE | api | API Server agent task management 增强。 | HTTP API 已支持 create/get/list/update/cancel/message/events；get/update 走 tenant/user/id 限定；Swagger 和 API 文档同步。create 只创建管理元数据，不直接启动模型运行。 | `go test ./internal/server ./internal/tenant ./internal/storage/mysql -count=1`; `swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency` |
| SMA-204 | DONE | webui | WebUI multi-agent cockpit。 | Observability 新增 Agents tab，可列出/选中 task、查看 events、创建管理记录、发送 message、标记 completed/failed、cancel、跳转 parent session；E2E 覆盖 mocked API 流。 | `npm --prefix web run test`; `npm --prefix web run build`; `npm --prefix web run test:e2e -- --project chromium-mock --grep "renders chat and validation"` |

## 推荐实施顺序

### Phase 1: Agent schema 和安全边界

先做 `SMA-001` 到 `SMA-005`，但建议拆成两批提交：第一批只做 `SMA-001` / `SMA-002` 的 schema 和 CLI 展示，第二批再做 `SMA-003` 到 `SMA-005` 的 runtime、安全边界和权限接线。这一步 ROI 最高，因为后续所有 multi-agent 能力都依赖准确的 agent 定义、工具过滤和权限模式。完成后 Go Claude 至少能正确读取上游 agent 配置的大部分字段，并以安全方式执行。

建议提交范围：

- `internal/agents`
- `internal/agentruntime`
- `internal/tools`
- `internal/permissions`
- `internal/cli`
- `docs/subagent_multiagent`

验收：

```bash
go test ./internal/agents ./internal/agentruntime ./internal/tools/task ./internal/permissions ./internal/cli -count=1
git diff --check
```

### Phase 2: Context 能力

做 `SMA-006` 到 `SMA-008`，补 agent 级 MCP、skills preload、agent memory。完成后子 agent 不再只是“换 prompt 和 tools”，而是真正有自己的上下文边界。

验收：

```bash
go test ./internal/mcp ./internal/skills ./internal/memory ./internal/agentruntime ./internal/query -count=1
git diff --check
```

### Phase 3: Async/background 和 hooks

做 `SMA-009`、`SMA-010`、`SMA-101`。完成后具备非阻塞后台 agent、可查询/取消/观测的 multi-agent 基础。

验收：

```bash
go test ./internal/background ./internal/agenttasks ./internal/tools ./internal/agentruntime ./internal/hooks ./internal/query ./internal/cli -count=1
git diff --check
```

### Phase 4: Coordinator / teammate

做 `SMA-102` 到 `SMA-105`。这一步会明显扩大架构面，建议在 Phase 1-3 有足够测试后再做。`SMA-102` 已完成，消息持久化复用 agent task event stream，运行态按 event id 去重投递。`SMA-103` 已完成 coordinator 工具白名单和执行层过滤。`SMA-104` 已完成同进程 teammate mode、progress summary 和取消复用。`SMA-105` 已完成 main-thread agent 与 initialPrompt 拼接。

验收：

```bash
go test ./internal/query ./internal/agentruntime ./internal/agenttasks ./internal/tools ./internal/cli -count=1
git diff --check
```

### Phase 5: Observability / UI / API

做 `SMA-106` 到 `SMA-108`、`SMA-203`、`SMA-204`。这是产品化和排障能力，不应早于核心语义。`SMA-203` 已完成 API Server create/get/list/update/cancel/message/events 管理面；`SMA-204` 已完成 WebUI Agents cockpit。

验收：

```bash
go test ./internal/query ./internal/agentruntime ./internal/server ./internal/telemetry ./internal/tui ./internal/cli -count=1
npm --prefix web run test
npm --prefix web run build
git diff --check
```

## 最终验证记录

`SMA-001` 到 `SMA-011`、`SMA-101`、`SMA-102`、`SMA-103`、`SMA-104`、`SMA-105`、`SMA-106`、`SMA-107`、`SMA-108`、`SMA-201`、`SMA-202`、`SMA-203`、`SMA-204` 已完成。2026-06-24 已完成 subagent / multi-agent 主干全链路验证：

- `go test ./... -count=1`
- `go run ./cmd/golang-cc eval agents --json`
- `go run ./cmd/golang-cc eval agents --profile anthropic-thinking --json`，有 Anthropic auth 时验证真实 thinking/signature。
- `go run ./cmd/golang-cc eval agents --profile live-agent-api --json`，有 API Server / MySQL DSN 时验证真实 `/tenant/agent-tasks` 和持久化 events。
- `npm --prefix web run test`
- `npm --prefix web run build`
- `npm --prefix web run test:e2e -- --project chromium-mock --grep "renders chat and validation"`
- `git diff --check`

统一验收入口：

```bash
scripts/subagent-multiagent-acceptance.sh
```

该脚本默认运行 `go test ./... -count=1`、deterministic `eval agents --profile local` 和 `git diff --check`，并把 JSON 报告写入 `reports/subagent-multiagent/<timestamp>/`。配置 `GOLANG_CLAUDE_CODE_EVAL_LIVE_API_BASE` 后会追加 `live` 与 `live-agent-api` profiles；配置 `ANTHROPIC_API_KEY` 或 `ANTHROPIC_AUTH_TOKEN` 后会追加 `anthropic-thinking` profile。可用 `SUBAGENT_MULTIAGENT_ACCEPTANCE_GO_TEST=0` 或 `SUBAGENT_MULTIAGENT_ACCEPTANCE_DIFF_CHECK=0` 跳过对应本地步骤。

2026-06-24 长尾收口新增：agent/skill `effort` 到 Anthropic SDK thinking config 的 provider 映射、`agents doctor [--json]` authoring diagnostics、默认 eval 的 `multiagent_e2e` 真实 query-loop 工具链路、Anthropic thinking live profile、以及 API Server / MySQL `live-agent-api` profile。当前 subagent / multi-agent 功能复刻主干已完成，剩余工作进入持续跟随上游未知字段和非公开实验语义跟踪。
