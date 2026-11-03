# go-claude vs Claude Code 当前根因状态报告

更新时间：2026-07-03

对照对象：

- 当前 Go 仓库：`/path/to/golang-cc`
- 原版源码目录：`$HOME/GolandProjects/claude_code_src_2026`
- 进度主文档：`docs/prompt_logic/prompt_gap_recovery_progress.md`

本文只陈述当前 HEAD 已有证据。旧诊断文档里的不少 P0/P1 已经被后续修复或证伪，不能继续按旧状态引用。

## 结论分层

### P0-0 已部分修复：同模型同任务首轮 request 仍有结构差异

结论：已经拿到原版 Claude Code 2.1.88 的首轮 request-capture，并与 Go 当前 HEAD 的同任务首轮 prompt dump 做了结构对照。早期对照证明两边存在高影响差异：`max_tokens`、system block/cache 边界、Agent/Task 工具名与 schema、workspace guidance 来源。当前 HEAD 已修复默认 `max_tokens` 差异，并新增显式 `Agent` 兼容工具面用于 same-shape 对照；kind-aligned compare 进一步证伪了旧的按位置 system block cache/text 误报。compatible `auto_memory/session guidance` 已从早期 `4428` bytes 扩到当前 memory refine gate 的 `9768` bytes，补齐了原版更有行为含义的用户画像安全、正/负反馈保存、stale-memory 验证和 plan/task vs memory 边界规则；`Agent.description` 已从 `2288` bytes 扩到当前 `5190` bytes，且 `Agent.input_schema_hash` 已对齐原版。Go compatible `Agent` 现在会按当前 cwd 动态列出 user/plugin/project `.claude/agents/*.md`，动态 workspace gate 已证明项目 agent 会进入真实 request；本轮又补齐了原版 request 已暴露的 built-in `Explore`/`Plan`，且单测证明它们能真实 list/load/run，并进一步按原版 read-only built-in agent 语义省略 stale git snapshot。当前 Go 还修正了一个原版同样存在的 tool-surface 规划污染：显式 `Agent,Grep,Read` 且没有 `Glob` 时，`Agent.description` 不再提示模型使用 `Glob`。原版 background Agent 多轮 request/transcript 也已通过仓库脚本可重复捕获；Go 侧同口径 lifecycle gate 进一步证明并修复了多个具体兼容污染点：显式 `subagent_type:"general-purpose"` 曾被 runtime 拒绝，未暴露 `AgentGet` 时 runtime status 曾要求模型调用 `AgentGet`，sub-agent provider error 曾只进日志不进 result preview，AgentStop 预取消窗口曾只写薄 cancelled payload 导致下一轮 request 缺 partial result。Go 当前还新增了受限 opt-in `SendMessage` 同名 wrapper和本地 git `Agent isolation:"worktree"` 最小实现：默认不暴露 `SendMessage`，env opt-in 后父线程可显式暴露，sub-agent 默认不继承；worktree 模式下子 Agent 工具真实在 isolated cwd 执行，无变更自动清理，有变更才向父线程回灌 `worktreePath/worktreeBranch`。原版 long-output Agent 同场景已捕获，证明原版会把完整长 `<result>` 直接带回父线程 request；原版 opt-in `SendMessage` running continuation 已捕获，tool_result 为 queued delivery，stopped resume 边界也已捕获为 no-transcript failure；Go 侧当前长 `AgentGet` 摘要策略在该边界上更省上下文且保留 `output_file`，并已把 detached running task 的当前进程挂接状态送入模型上下文。本轮源码证据进一步证明 `remote` isolation 是原版 ant-only/external 不可达能力，并补齐了 `SubagentStart` hook `additionalContext` 入模、hook-based `WorktreeCreate` Agent isolation，以及 opt-in `SendMessage` plain-text local recipient resolver：task id 优先，唯一 `agent_name/description` 可映射到 Go task store。剩余高影响差异集中在原版额外 header block、未逐字等价的 memory prompt/layout、未实现的 `statusline-setup` 和 fork/team/swarm examples，以及完整 structured teammate/broadcast 等未覆盖场景。

当前修复状态：部分修复。`max_tokens` 已对齐；显式 `--tools Agent,Grep,Read` gate 下 `tool_names` 已对齐；原版 background Agent completion/child-error/killed/long-output notification 与 Go background Agent completed/failed/cancelled/long-output runtime status 均已可重复采集；Go 侧 same-shape gate 已消除“要求调用未暴露 AgentGet”的错误指令，并补齐 failed/cancelled partial result preview；原版 env opt-in `SendMessage` running continuation 已捕获，Go 受限 wrapper 和 prompt gate 已对齐到父线程可显式暴露、默认不暴露，且当前 wrapper 已支持唯一 `agent_name/description` recipient fallback；原版 `isolation:"worktree"` 的本地 git worktree基础语义已用 Go 单测闭环；`SubagentStart` hook additional context 已进入子 agent request。其它 request shape 差异仍需继续收敛或做 Go/原版 side-by-side lifecycle diff 后定责。

运行证据：

- 原版 request-capture：`/tmp/claude-code-upstream-capture-20260703/capture.jsonl`。
- 原版 transcript：`/tmp/claude-code-upstream-capture-20260703/config/projects/-Users-example-GolandProjects-golang-claude-code/11111111-1111-4111-8111-111111111111.jsonl`。
- 历史 Go prompt dump：`/tmp/go-claude-upstream-compare-20260703/go-dump.jsonl`。
- 结构对照报告：`/tmp/go-claude-upstream-compare-20260703/compare.json`。
- compare 命令：`go run ./scripts/promptdump-compare --go /tmp/go-claude-upstream-compare-20260703/go-dump.jsonl --upstream /tmp/go-claude-upstream-compare-20260703/upstream-request-capture.jsonl`。
- 修复后 Go prompt dump：`/tmp/go-claude-default-max-tokens-20260703.jsonl`，三轮 request 均为 `max_tokens=32000`。
- 修复后 compare 命令：`go run ./scripts/promptdump-compare --go /tmp/go-claude-default-max-tokens-20260703.jsonl --upstream /tmp/go-claude-upstream-compare-20260703/upstream-request-capture.jsonl`，结果 `ok=true`，`max_tokens` 不再出现在 differences。
- Agent 兼容 Go prompt dump：`/tmp/go-claude-agent-compat-tools-20260703.jsonl`，显式 `--tools Agent,Grep,Read`，verifier `ok=true`，`tool_count=3`。
- Agent description/schema refine dump：`/tmp/go-claude-agent-compat-tools-post-description-20260703.jsonl`，verifier `ok=true`，request 命中 `When NOT to use Agent`、`Launch multiple agents concurrently`、`Foreground is the default`、`"$schema"`。
- Agent 兼容 compare 命令：`go run ./scripts/promptdump-compare --go /tmp/go-claude-agent-compat-tools-post-description-20260703.jsonl --upstream /tmp/go-claude-upstream-compare-20260703/upstream-request-capture.jsonl`，结果 `ok=true`，`tool_names` 已同为 `Agent,Grep,Read`；当时剩余 warn 为 system block/cache、`tools.Agent.description_bytes` 和 `tools.Agent.input_schema_hash`；后续 model/schema override refinement 已消除 `tools.Agent.input_schema_hash`。
- Go workspace guidance source gate：`scripts/workspace-guidance-source-acceptance.sh --work-dir /tmp/go-claude-guidance-source-20260703 --force`，4/4 case `ok=true`。报告 `/tmp/go-claude-guidance-source-20260703/report.json` 证明 `CLAUDE.md` 优先、`AGENTS.md` 只在同目录无 `CLAUDE.md` 时 fallback。
- 原版 workspace guidance source gate：`scripts/upstream-workspace-guidance-source-capture.sh --work-dir /tmp/upstream-guidance-source-gate-20260703 --force`，4/4 case `ok=true`。报告 `/tmp/upstream-guidance-source-gate-20260703/report.json` 证明原版普通 code session 读取 `CLAUDE_CONFIG_DIR/CLAUDE.md`、项目 `CLAUDE.md`、项目 `.claude/CLAUDE.md`，但不把 `AGENTS.md` 当 fallback。
- strict compatible Go guidance gate：`scripts/workspace-guidance-source-acceptance.sh --work-dir /tmp/go-claude-guidance-source-strict-20260703 --force --prompt-profile claude-compatible-strict`，4/4 case `ok=true`。`project_agents_fallback` 只包含 `User:20`、`workflow_rules=0`，与原版 AGENTS fallback 行为对齐。
- strict compatible post-Read-schema compare：`go run ./scripts/promptdump-compare --go /tmp/go-claude-agent-compat-strict-guidance-post-read-first-20260703.jsonl --upstream /tmp/go-claude-upstream-compare-20260703/upstream-request-capture.jsonl`，`tool_names` 已同为 `Agent,Grep,Read`，且 `Read` schema warning 消失；旧 compare 剩余 warn 包含按位置 system block cache/text mismatch。
- kind-aligned compare：同一命令在当前 compare 实现下结果 `ok=true`；旧的按位置 mismatch 消失，后续最新 compare 的剩余 warn 收敛为 `system_bytes`、`system_block_count`、`system_blocks.kinds`、`system_blocks.kind.auto_memory.text_bytes`、`tools.Agent.description_bytes`。
- compatible memory/session guidance dump：`/tmp/go-claude-agent-compat-strict-memory-guidance-20260703.jsonl`，request gate `ok=true`，命中 `# Session-specific guidance`、`Use the Agent tool with specialized agents`、`subagent_type=general-purpose`、`<types>`、`` `MEMORY.md` is an index``。
- compatible memory/session guidance compare：`/tmp/go-vs-upstream-agent-compat-strict-memory-guidance-20260703.json`，Go `auto_memory` block 从 `4428` bytes 收敛到 `8748` bytes；剩余 warn 仍为 system 总量/block count/kinds、`auto_memory` bytes 和 `Agent` description/schema。
- compatible memory protocol refine dump：`/tmp/go-claude-memory-protocol-refine-20260703.jsonl`，verifier `ok=true`，request 命中 `senior software engineer differently than a student`、`Record from failure and success`、`not just asking about history`、`trust what you observe now rather than acting on the stale memory`；turn 1 `auto_memory` block 为 `9768` bytes。
- compatible Agent description dump：`/tmp/go-claude-agent-compat-strict-agent-description-20260703.jsonl`，request gate `ok=true`，命中 `multiple Agent tool use content blocks`、`Each Agent invocation starts with fresh context`、`you own the synthesis`、`must not modify files`、`Good brief checklist`。
- compatible Agent description compare：`/tmp/go-vs-upstream-agent-compat-strict-agent-description-20260703.json`，Go `Agent.description_bytes` 从 `2288` 收敛到 `3518`，原版为 `6542`。
- compatible Agent model/schema override dump：`/tmp/go-claude-agent-schema-post-model-20260703.jsonl`，request gate `ok=true`，命中 `"name":"Agent"`、`Takes precedence over the agent definition`、`$schema`、`"worktree"`。
- compatible Agent model/schema override compare：`/tmp/go-vs-upstream-agent-schema-post-model-20260703.json`，`ok=true`；Go 和原版 `Agent.input_schema_hash` 同为 `b00793ce...`，剩余 warn 为 system bytes/block count/kinds、`auto_memory` bytes 和 `tools.Agent.description_bytes`。
- cwd-aware Agent description dump：`/tmp/go-claude-agent-description-dynamic-post-20260703.jsonl`，request gate `ok=true`，命中 `specific capabilities`、`If SendMessage is available`、`proactively`、`isolation="worktree"`。
- cwd-aware Agent description compare：`/tmp/go-vs-upstream-agent-description-dynamic-post-20260703.json`，`ok=true`；Go `Agent.description_bytes=4313`，原版 `6542`；schema hash 仍对齐。
- dynamic workspace Agent listing gate：`/tmp/go-claude-agent-description-dynamic-workspace-20260703.jsonl`，verifier `ok=true`；临时 workspace 的 `.claude/agents/reviewer.md` 进入真实 request：`- reviewer: Independent code review (Tools: Read, Grep)`。
- built-in Explore/Plan Agent gate：`/tmp/go-claude-agent-builtins-20260703.jsonl` verifier `ok=true`；真实 request 命中 `Explore`、`Plan`、`Fast agent specialized`、`Software architect agent`。
- built-in Explore/Plan compare：`/tmp/go-vs-upstream-agent-builtins-20260703.json`，`ok=true`；Go `Agent.description_bytes=5190`，原版 `6542`；`Agent.input_schema_hash` 仍同为 `b00793ce...`。
- built-in Explore/Plan 单测证据：`internal/agents/builtin.go` 定义真实 `Explore`/`Plan` prompt、model、denylist；`TestBuiltInAgentsAreListedAndLoadable`、`TestRuntimeLoadsBuiltInExploreAgent`、`TestRuntimeLoadsBuiltInPlanAgent` 证明它们可 list/load/run，且 `Explore` 用 haiku、`Plan` 继承父模型，`Agent/Edit/Write` 不暴露给子 agent。
- built-in Explore/Plan context slimming 证据：原版 `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/runAgent.ts` 对 `Explore`/`Plan` 省略 `claudeMd` 与 stale `gitStatus`；Go 新增 `Agent.OmitGitStatus`，`Explore`/`Plan` 设置为 true，`TestRuntimeFullDumpShowsBuiltInExploreOmitsGitSnapshot` 证明 full dump request system 保留 `# Environment` 和 cwd，但不含 `# Git Snapshot` / `Status:`；`TestRuntimeAddsSubagentEnvironmentDetails` 证明 general-purpose sub-agent 仍保留 git snapshot。
- Agent direct-tool surface precision gate：Go pre-fix `/tmp/go-claude-agent-builtins-20260703.jsonl` 和原版 `/tmp/upstream-agent-final-report-gpt55-rg-20260703/capture.jsonl` 都在 `tools=["Agent","Grep","Read"]` 场景向模型提示 `Glob`；当前 `/tmp/go-claude-agent-direct-tools-20260703.jsonl` verifier `ok=true`，request 命中 `use Read directly`、`try Grep`，并禁止 `Read or Glob`、`Glob or Grep`；compare `/tmp/go-vs-upstream-agent-direct-tools-20260703.json` 仍 `ok=true`，schema hash 对齐。
- 原版 reusable Agent lifecycle gate：`scripts/upstream-agent-lifecycle-capture.sh --work-dir /tmp/upstream-agent-lifecycle-20260703 --force`，报告 `/tmp/upstream-agent-lifecycle-20260703/report.json`，捕获 4 个 `/v1/messages` request：父线程 `Agent` tool_use、子 Agent request、父线程 async launch tool_result 后 request、父线程 `<task-notification>` 后 request。
- 原版 lifecycle transcript：`/tmp/upstream-agent-lifecycle-20260703/config/projects/-Users-example-GolandProjects-golang-claude-code/44444444-4444-4444-8444-444444444444.jsonl`，明确记录 async launch tool_result 中的 `agentId`、`SendMessage`、`output_file`，以及 queued user message `<task-notification>` 中的 `task-id`、`tool-use-id`、`output-file`、`status=completed`、`result`、`usage`。
- Go reusable Agent lifecycle gate：`scripts/go-agent-lifecycle-capture.sh --work-dir /tmp/go-agent-lifecycle-20260703-post-agentget-availability --dump /tmp/go-agent-lifecycle-20260703-post-agentget-availability.jsonl --force`，报告 `/tmp/go-agent-lifecycle-20260703-post-agentget-availability/report.json`，`request_count=4`，`response_classes=["parent_agent_tool_use","child_agent_final","parent_after_agent_tool_result","parent_after_followup_tool"]`。
- Go lifecycle 初次失败证据：`/tmp/go-agent-lifecycle-20260703/cli.log` 中 `unknown subagent_type: general-purpose`，证明 compat description/schema 与 runtime 接受值曾不一致。
- Go lifecycle 修复后证据：`/tmp/go-agent-lifecycle-20260703-post-agentget-availability/report.json` 中 `runtime_mentions_agentget_without_tool=false`，call 4 同时满足 `background_agent_tasks=true`、`completion_notification=true`、`child_done_marker=true`、`output_file=true`、`agent_get_tool_exposed=false`、`call_agent_get_instruction=false`。
- post-commit 双边复跑：`/tmp/upstream-agent-lifecycle-20260703-current/report.json` 与 `/tmp/go-agent-lifecycle-20260703-current/report.json` 均 `ok=true`。原版 call 3/4 request tools 仍为 `Agent,Grep,Read`，但 tool_result 文案含 `SendMessage`；Go call 4 仍满足 `runtime_mentions_agentget_without_tool=false`。

对照结果：

| 字段 | 原版 Claude Code 2.1.88 | go-claude 当前 HEAD |
| --- | --- | --- |
| model | `claude-sonnet-4-6` | `claude-sonnet-4-6` |
| max_tokens | `32000` | `32000` |
| system | 4 blocks / 26,732 bytes；kinds 为 `other,attribution,core_prompt,auto_memory` | strict Agent-compatible memory-guidance dump 为 3 blocks / 17,975 bytes；kinds 为 `attribution,core_prompt,auto_memory` |
| tools | `Agent,Grep,Read` | explicit compat gate: `Agent,Grep,Read`; default diagnostic gate may still use Go-native tool surface |
| Agent/Task schema | `Agent` 包含 `description,prompt,subagent_type,model,run_in_background,isolation`；`model` 工具参数优先于 agent frontmatter；`isolation:"worktree"` 创建临时 git worktree，无变更清理，有变更返回路径；description 动态列出可用 agent types；内置 `Explore`/`Plan` 可用 | `Agent` compat 字段集合和 schema hash 已对齐；`model` override 真实传入 sub-agent runtime；本地 git `worktree` isolation 已实现；description bytes `5190` vs 原版 `6542`；项目 `.claude/agents/*.md` 和 built-in `Explore`/`Plan` 可进入 request |
| guidance 命中 | 四场景 gate 已证明普通 code session 不读 `AGENTS.md fallback`；`project_agents_fallback` 只命中 user guidance | Go 默认/普通 compatible 保留 `AGENTS.md` fallback；新增 `claude-compatible-strict` 后，`project_agents_fallback` 也只命中 user guidance |
| Read schema | 原版同任务 compare 不暴露 Go-only `Read` 字段 | strict compatible 已复用 compatible Read schema，不再暴露 `chunk_index,byte_offset,byte_limit,line_numbers` |
| same-kind system content | `core_prompt=11420` bytes，`auto_memory=15166` bytes | `core_prompt=9161` bytes，memory refine gate `auto_memory=9768` bytes；当前 compare 仍会对 `auto_memory` 触发阈值 warning |
| background Agent result protocol | 一次性 user-role `<task-notification>`，含 `task-id`、`tool-use-id`、`output-file`、`status`、`result`、`usage` | request-time `## Background agent tasks`，含 status/result preview/transcript/output_file；`AgentGet` 暴露时要求先取详情，未暴露时不再要求调用 |

源码证据：

- 原版 request 拼装：`$HOME/GolandProjects/claude_code_src_2026/src/query.ts:449` 到 `:451` 拼 `fullSystemPrompt`，`:659` 到 `:664` 用 `prependUserContext(messagesForQuery, userContext)` 并传 tools。
- 原版 user context 从 CLAUDE.md 注入：`$HOME/GolandProjects/claude_code_src_2026/src/context.ts:155` 到 `:187`。
- 原版 system block/cache control 入口：`$HOME/GolandProjects/claude_code_src_2026/src/services/api/claude.ts:1357` 到 `:1377`。
- 原版输出 token 默认入口：`$HOME/GolandProjects/claude_code_src_2026/src/services/api/claude.ts:3400` 到 `:3418`。
- Go code-mode 默认 `max_tokens=32000`：`internal/defaults/defaults.go:9` 到 `:13`，`internal/query/query.go:383` 到 `:385`，sub-agent 兜底：`internal/agentruntime/runtime.go:180` 到 `:182`。
- Go chat-mode 默认保持 `4096`，避免 tenant/mobile 普通聊天默认预算意外放大：`internal/query/query.go:412` 到 `:418`。
- Go 每轮 request 构造：`internal/query/query.go:1175` 到 `:1199`。
- Go `claude-compatible` memory/context 组装：`internal/query/query.go:3252` 到 `:3263`、`:1094` 到 `:1097`。
- Go `AGENTS.md` fallback：`internal/memory/memory.go:55` 到 `:68`、`:460` 到 `:468`。
- Go workspace guidance gate：`scripts/workspace-guidance-source-acceptance.sh`，实测 `user_only`、`project_claude_preempts_agents`、`project_agents_fallback`、`project_and_dot_claude_preempt_agents` 四个隔离 case。
- Go strict compatible 实现：`internal/memory/memory.go` 的 `LoadCodeOptions.DisableProjectAgentsFallback` 与 `LoadCodeWithOptions(...)`；`internal/query/query.go` 的 `promptProfileClaudeCompatibleStrict`、`isClaudeCompatibleProfile()` 和 strict-only loader option。
- Go strict Read schema 修复：`internal/tools/fileread/fileread.go` 的 `claudeCompatiblePromptProfile()` 同时识别 `claude-compatible` 和 `claude-compatible-strict`；测试 `TestClaudeCompatibleStrictReadUsesCompatibleSchemaAndOutput`。
- Go prompt dump kind-aligned compare：`internal/promptdump/compare.go` 的 `compareSystemBlocks(...)` 改为同 kind 比较 text/cache，`other` header block 只通过 `system_blocks.kinds` 暴露；测试 `TestComparePromptDumpsAlignsSystemBlocksByKind`。
- Go compatible memory/session guidance：`internal/query/query.go` 的 `compatibleSessionGuidanceSection(...)` 和 `compatibleMemorySystemBlock(...)`；测试 `TestClaudeCompatibleSessionGuidanceMatchesAgentToolSurface`、`TestClaudeCompatiblePromptProfileLoadsMemoryIntoSystemBlock`。
- 原版 typed-memory protocol 来源：`$HOME/GolandProjects/claude_code_src_2026/src/constants/prompts.ts:491` 到 `:500` 用 `systemPromptSection('memory', () => loadMemoryPrompt())` 把 memory 放在 dynamic sections 中，`$HOME/GolandProjects/claude_code_src_2026/src/memdir/memdir.ts:199` 到 `:266` 的 `buildMemoryLines(...)` 生成普通 typed-memory prompt。
- 原版 workspace guidance gate：`scripts/upstream-workspace-guidance-source-capture.sh`，实测同四个隔离 case；该脚本用 Node preload 捕获原版 `/v1/messages` request，不调用真实模型。
- 原版 `CLAUDE.md` 来源：`$HOME/GolandProjects/claude_code_src_2026/src/utils/claudemd.ts:4` 到 `:16`、`$HOME/GolandProjects/claude_code_src_2026/src/context.ts:155` 到 `:187`。原版 `/init` 会读取 `AGENTS.md` 辅助生成 `CLAUDE.md`，但这不是普通 code session 自动注入证据：`$HOME/GolandProjects/claude_code_src_2026/src/commands/init.ts:46`、`:108`。
- Go `Agent` compatibility tool：`internal/tools/agent/agent.go`，字段集合、同步/后台执行、`isolation:"worktree"` 创建与 prompt notice 均在该路径。
- Go `Agent` worktree runtime：`internal/agentworktree/worktree.go` 创建 `.claude/worktrees/<slug>`、检测变更并清理无变更 worktree；`internal/agentruntime/runtime.go` 的 `runTool(...)` 使用 `Request.CWD` 覆盖子工具 cwd，`finishTask(...)` 清理无变更 worktree 并返回清理后的 result；`internal/query/query.go` 的 `agentTaskResultHint(...)` 回灌 `worktree_path/worktree_branch`。
- Go `AgentMessage` worktree resume：`internal/tools/agent/agent.go` 的 `resumeTerminalTask(...)` 现在从 terminal task `ResultJSON/MetadataJSON` 恢复仍存在的 `worktree_path`，设置 successor `Request.CWD/WorktreePath/WorktreeBranch`，并更新 worktree mtime。
- Go `Agent` worktree 单测：`TestCreateAndRemoveGitWorktree`、`TestRuntimeRunsToolsWithRequestCWD`、`TestAgentCompatCleansUnchangedWorktreeIsolation`、`TestAgentCompatKeepsChangedWorktreeIsolation`。
- Go worktree lifecycle prompt gate：`scripts/go-agent-lifecycle-capture.sh --work-dir /tmp/go-agent-lifecycle-20260703-worktree --dump /tmp/go-agent-lifecycle-20260703-worktree.jsonl --scenario completed --agent-isolation worktree --force`，报告 `/tmp/go-agent-lifecycle-20260703-worktree/report.json` 为 `ok=true`；`worktree_notice_captured=true`、`parent_runtime_status_contains_completed_result=true`、`worktree_path_captured=false`，证明无变更 worktree 没有把已清理路径带回父线程历史。
- Go `AgentMessage` worktree resume prompt gate：`scripts/agent-message-resume-acceptance.sh --force --work-dir /tmp/go-claude-agent-message-resume-worktree-20260703 --dump /tmp/go-claude-agent-message-resume-worktree-20260703.jsonl --prompt-profile claude-compatible --worktree-resume`，verifier `ok=true`；resumed sub-agent request 命中 `Current working directory: /tmp/go-claude-agent-message-resume-worktree-20260703/retained-worktree`、`previous finding`、`Agent message from coordinator`。
- 原版 `remote` isolation ant-only gate：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:433` 到 `:478`，external build 下 `"external" === 'ant'` 为 false；schema 也只在 ant build 暴露 `remote`：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:98` 到 `:100`。
- 原版 hook-based worktree：`$HOME/GolandProjects/claude_code_src_2026/src/utils/worktree.ts:902` 到 `:951` 先走 `hasWorktreeCreateHook()` 和 `executeWorktreeCreateHook(slug)`；Go 当前 `internal/agentworktree/worktree.go` 已新增 hook-first `CreateWithHooks(...)`，支持 stdout path 和 `hookSpecificOutput.worktreePath`，hook-based worktree 会保留并返回路径。
- 原版 `SubagentStart` hook additional context 入模：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/runAgent.ts:530` 到 `:555`；最终 message 渲染：`$HOME/GolandProjects/claude_code_src_2026/src/utils/messages.ts:4117` 到 `:4128`。
- Go `SubagentStart` hook additional context 修复：`internal/hooks/hooks.go` 的 `Result.AdditionalContext`，`internal/agentruntime/runtime.go` 的 `runSubagentStartHook(...)` 与 `subagentStartHookContextMessage(...)`；测试 `TestRunnerJSONResult`、`TestRuntimeInjectsSubagentStartHookAdditionalContext`。
- Go `WorktreeCreate` hook-based Agent isolation 修复：`internal/hooks/hooks.go` 的 `WorktreeCreate`/`WorktreeRemove`、`Payload.Name`、`Result.HookSpecificOutput`；`internal/agentworktree/worktree.go` 的 `CreateWithHooks(...)`；`internal/tools/agent/agent.go` 的 `NewCompatWithHooks(...)`；测试 `TestRunnerWorktreeCreatePayloadFields`、`TestCreateWithWorktreeCreateHookUsesStdoutPathWithoutGit`、`TestCreateWithWorktreeCreateHookUsesHookSpecificOutput`、`TestAgentCompatUsesWorktreeCreateHookIsolationWithoutGit`。
- Go `Agent` compat description/schema 和 model 参数传递：`internal/tools/agent/agent.go` 的 `CompatTool.Description()`、`CompatTool.InputSchema()` 与 `CompatTool.Run(...)`。
- Go cwd-aware Agent description：`internal/tools/agent/agent.go` 的 `CompatTool.cwd`、`NewCompatWithHooksAndCWD(...)`、`availableAgentTypesDescription()`；`internal/cli/cli.go` 注册 compatible `Agent` 时传入 `opts.cwd`；测试 `TestAgentCompatDescriptionListsWorkspaceAgents`、`TestExplicitAgentToolRegistersClaudeCompatibleSurface`。
- Go sub-agent model override runtime：`internal/agentruntime/runtime.go` 的 `Request.Model` 与 `resolveSubagentModel(...)`；测试 `TestResolveSubagentModelPriorityAndAliases`、`TestRuntimeRequestModelOverridesAgentFrontmatter`、`TestAgentCompatPassesModelOverride`。
- Go `Agent` compat description refinement：`internal/tools/agent/agent.go` 的 `CompatTool.Description()` 现在包含 parallel single-message、fresh invocation、parent synthesis、research-only no-write 和 brief checklist contract；测试 `TestAgentToolDescriptionsIncludePromptContract`。
- Go 显式 `general-purpose` 修复：`internal/agentruntime/runtime.go:1163` 到 `:1167`；测试 `TestRuntimeAcceptsExplicitGeneralPurposeAgentType`。
- Go `AgentGet` 可用性修复：`internal/query/query.go:3706` 到 `:3773` 由 `runtimeAgentTaskStatus(...)` 根据 `agentGetAvailable()` 生成不同文案；`internal/query/query.go:3820` 到 `:3836` 保留 `AgentGet` 暴露时的旧纪律，未暴露时改用 status/result preview/transcript/output_file。
- Go 显式注册条件：`internal/cli/cli.go:2301` 到 `:2303`，只有 `--tools` 显式包含 `Agent` 才注册兼容工具。
- Go 回归测试：`internal/tools/agent/agent_test.go:130` 到 `:238`、`internal/cli/cli_test.go:3140` 到 `:3173`。

置信度：高，证明“首轮 request 结构确实不同”；高，证明“原版 background Agent completion/child-error/killed/long-output 通过一次性 user-role `<task-notification>` 回灌结果”；高，证明“原版 opt-in SendMessage running continuation 会 queue pending message，stopped resume 存在 no-transcript failure 边界”；高，证明“Go 当前通过 runtime status/result preview/AgentGet summary 能覆盖 completed/failed/cancelled/long-output 关键规划输入”；高，证明“Go 当前本地 git worktree isolation 不再是 schema/运行时断裂，并且 AgentMessage successor 能恢复 retained worktree cwd”；高，证明“原版 external `remote` isolation 不可达，Go 不应把它列为普通 parity 修复目标”；高，证明“Go 当前已把 SubagentStart hook additional context 送入子 agent request”；高，证明“Go 当前已支持 hook-based WorktreeCreate isolation，非 git cwd 可通过 hook 返回目录运行子 Agent”；中等，证明“这是昨天表现差距的唯一或最大原因”。当前还缺同一真实任务下 Go/原版 side-by-side lifecycle diff，以及完整 structured swarm/broadcast 场景。

当前判断：

- `max_tokens` 差异已经完成最小高 ROI 修复，并用 unit test、prompt dump、upstream compare 和 full matrix 复验。它不再是当前未修根因。
- Agent/Task 工具名差异已通过显式兼容 gate 收敛，支持 `Agent,Grep,Read` same-shape 对照；Agent schema hash 已对齐，description 已低风险增强并支持 cwd-aware user/plugin/project agent listing 和真实 built-in `Explore`/`Plan`。原版 background completion/child-error/killed/long-output path 已可重复捕获，证明一次性 `<task-notification>` 与 Go `runtime status + AgentGet/result preview + output_file` 是真实协议形态差异；原版 opt-in SendMessage running continuation 和 stopped resume failure boundary 已可重复捕获；Go 已补齐 failed/cancelled partial result preview，并在长 AgentGet result 场景通过结构化摘要超过原版 raw long-result notification 的上下文预算表现。本地 git worktree isolation、hook-based WorktreeCreate isolation、AgentMessage successor worktree cwd 恢复、detached running process attachment、SubagentStart hook additional context 已补齐最小闭环；原版 `statusline-setup`、structured swarm/broadcast 行为仍不是完整等价，仍是 P0/P1 剩余项。`remote` 已按原版 external 不可达降级，不再作为普通 external parity blocker。
- system block/cache 边界差异需要谨慎设计，不能只把文本拼到一起；kind-aligned compare 后，当前最大同 kind 内容差异是 `auto_memory/session guidance`，不是旧报告里的 position-based cache/text mismatch。该差异已完成第一轮收敛，但仍非逐字等价。
- workspace guidance 来源差异已从疑点升级为可复现事实，并已具备 A/B 控制：Go 默认和普通 compatible 会在无项目 `CLAUDE.md` 时把同目录 `AGENTS.md` 作为 Workflow fallback 注入；原版普通 code session 在同场景只保留 user guidance；Go 新增 `claude-compatible-strict` 后可关闭该 fallback。默认能力不降级，same-shape 对照不再被该差异阻塞。strict profile 下 Read schema 也已继承 compatible 行为，Go-only Read 字段不再污染 strict compare。

### P0-1 已证实：每轮 request context orchestration 是历史最大根因

结论：昨天看到的“同模型、同任务、同工作区表现差别巨大”，最强可证据支持的根因不是单句 system prompt，而是每轮模型请求前的上下文编排差异：runtime status、tool result budget、skills loaded context、Todo/Plan、Agent status、permission/workspace、resume/compact 是否稳定进入下一轮 request。

当前修复状态：已大幅收敛，当前 HEAD 的组合 prompt acceptance matrix 与综合多轮 prompt dump 门禁通过。

证据：

- Go 当前每轮 request 前会执行 message/history budget，然后注入 runtime status 与 active skill context：`internal/query/query.go:1136`、`internal/query/query.go:1150`、`internal/query/query.go:1154`。
- Go runtime status 当前包含 permission、todo、plan、background agent tasks：`internal/query/query.go:3354`、`internal/query/query.go:3357`、`internal/query/query.go:3360`、`internal/query/query.go:3363`。
- 综合多轮门禁：`scripts/final-diagnostic-prompt-acceptance.sh` 通过，dump `/tmp/go-claude-final-diagnostic-20260703.jsonl` 覆盖 active todos、plan mode、Read tool_result、AgentCreate、subagent scoped request、Bash wait、AgentGet result 和 terminal notification 消费。
- 完整矩阵证据仍保留：`/tmp/go-claude-prompt-acceptance-matrix-head-20260703/matrix-report.json`，8/8 ok，覆盖 `code/task-agent/skills/resume-replacement/tool-result-heavy/background-agent/read-tool-result/bash-background`。
- 相关提交：`10646300 fix: harden read tool result budgeting`、`71b7ff23 fix: preserve loaded skill context`、`e9b0c26e feat: expose permission mode in runtime context`、`80f97707 feat: expose additional writable roots in prompt`、`87a263f8 fix: align todowrite progress result`、`519331ef feat: support todowrite active form`、`1e13c0f1 docs: record web agent prompt dump evidence`。

置信度：高。

为什么这是真因候选：它直接改变模型每一轮看到的输入，不是 UI 展示差异。旧版本中任何一个状态缺失都可能导致模型重复探索、忘记计划、误用权限、丢失后台 agent 结果或错误处理大输出。

### P0-2 已证实并修复：tool_result / Read / 大输出治理会影响下一步规划

结论：tool result 的形状、截断、持久化和 Read 默认窗口是高影响根因。它会直接影响模型下一轮规划，而不是只影响日志。

当前修复状态：已通过 targeted tests 和 full matrix。

证据：

- 原版 Read 不受单条 result limit 影响：`$HOME/GolandProjects/claude_code_src_2026/src/tools/FileReadTool/FileReadTool.ts:342` 的 `maxResultSizeChars: Infinity`。
- 原版 Read 默认最多读 2000 行：`$HOME/GolandProjects/claude_code_src_2026/src/tools/FileReadTool/prompt.ts:10`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/FileReadTool/prompt.ts:37`。
- Go 当前在 request 前应用 message/history budget：`internal/query/query.go:1136`、`internal/query/query.go:1143`。
- 进度证据：`docs/prompt_logic/prompt_gap_recovery_progress.md` 记录 `code` 场景曾因 OpenAI tool arguments 粘连、Read result persisted、Read 全文件超过 request raw limit 失败；修复后 `/tmp/go-claude-prompt-acceptance-matrix-post-json-read-window-20260703/matrix-report.json` 8/8 ok。
- 相关提交：`7536b369 test: verify prompt dump tool use inputs`、`10646300 fix: harden read tool result budgeting`。

置信度：高。

### P0-3 已证实并修复：Task/Agent 生命周期和结果回灌曾不稳

结论：Task/Agent 是历史核心差异之一，但当前 HEAD 已经通过 `runtime status + AgentGet/result preview + output_file + resume seed + compact status` 收敛到可用状态。原版 reusable capture 已证明 background completion、child-error、killed 的真实形态是一次性 user-role `<task-notification>`；Go reusable capture 已证明 same-shape `Agent,Grep,Read` 下也能把后台任务状态、完成/失败/取消 marker 和 output file 送入父线程 request。最新修复消除了四个会直接影响下一步规划的污染点：未暴露 `AgentGet` 时不再提示模型调用 `AgentGet`；sub-agent provider/model 错误不再只进入日志，而是写入 failed task 的 `result preview` / `AgentGet` / `output_file`；`AgentStop` 预取消窗口不再只写薄 cancelled payload，而是从已有 `text_delta` 事件合成 cancelled `ResultJSON.content`，让下一轮 request 立即看到 partial result preview；长 Agent result 经 `AgentGet` 时不再整体落入通用 `<persisted-output>`，而是保留小型 `content_preview/content_truncated/content_bytes` 摘要和可见 `output_file`。原版 `SendMessage` 暴露条件也已拆开：普通 external CLI request 不暴露，`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` 后父线程暴露，async child 仍不暴露。Go 仍没有逐字复刻原版 notification queue；更精确的剩余差异是结果回灌协议形态、opt-in continuation 工具面和边界场景。

当前修复状态：主要链路已闭环，Go/原版 completed、child-error/failed、killed/cancelled lifecycle 都有可重复 gate；完整协议仍未 100% 等价。

原版证据：

- 原版 fresh agent prompt 要求带目标、已知事实、排除路径和周边上下文：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/prompt.ts:101`。
- 原版 fork/async 纪律要求不要 peek、不要 race、等待后续 user-role notification：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/prompt.ts:91`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/prompt.ts:93`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/prompt.ts:127`。
- 原版 Agent schema 有 `run_in_background`、`name`、`team_name`、`mode`、`isolation`、`cwd` 等字段：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:82`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:98`。
- 原版 async result tool_result 包含 `output_file` 与 completion notification 纪律：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:1327`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:1329`。

Go 当前证据：

- `AgentCreate` 要求完整 brief，并返回 `output_file`：`internal/tools/agent/agent.go:44`、`internal/tools/agent/agent.go:51`、`internal/tools/agent/agent.go:110`、`internal/tools/agent/agent.go:118`。
- `AgentGet` 返回 sanitized result，省略 sub-agent tool I/O：`internal/tools/agent/agent.go:168`、`internal/tools/agent/agent.go:198`、`internal/tools/agent/agent.go:236`、`internal/tools/agent/agent.go:275`、`internal/tools/agent/agent.go:286`。
- runtime status 明确 running 不要推断；`AgentGet` 暴露时 completed/failed/cancelled 要先 `AgentGet`，未暴露时改用已入模的 status/result preview/transcript_path/output_file：`internal/query/query.go:3706` 到 `:3773`、`:3820` 到 `:3836`。
- runtime status 对 completed result 输出 preview、transcript、output_file、turns：`internal/query/query.go:3842` 后的 `agentTaskResultHint(...)`。
- 综合多轮门禁：`/tmp/go-claude-final-diagnostic-20260703.jsonl` 中 turn 3/4 的 `runtime_status.sections` 包含 `background_agent_tasks`，turn 4 request 含 `completion notification: result is ready` 与 `call AgentGet before using findings`，turn 5 `tool_results_by_tool.AgentGet.count=1` 且最后一个 main request 不再重复 terminal notification。
- 原版 reusable lifecycle gate：`/tmp/upstream-agent-lifecycle-20260703/report.json` 中 `request_count=4`，`response_classes=["parent_agent_tool_use","child_agent_final","parent_after_async_launch","parent_after_agent_notification"]`；call 3 request 含 async launch tool_result 的 `agentId`、`output_file`、`SendMessage`，call 4 request 含 `<task-notification>` 与子 agent result marker。
- 原版 child-error lifecycle gate：`/tmp/upstream-agent-lifecycle-20260703-child-error-v5/report.json` 中 `ok=true`、`provider_error_reported_as_completed=true`；call 4 `<task-notification>` 同时含 `<status>completed</status>` 与 provider 400 error marker，说明原版会把子 Agent provider error 包进 completed notification result。
- 原版 killed lifecycle gate：`/tmp/upstream-agent-lifecycle-20260703-killed-v2/report.json` 中 `ok=true`、`response_classes=["parent_agent_tool_use","child_agent_hanging","parent_task_stop_tool_use","parent_after_task_stop_result","parent_after_agent_notification"]`；call 5 request `task_notification=true`、`killed_status=true`、`child_partial_marker=true`。
- 原版 SendMessage exposure gate：
  - `/tmp/upstream-agent-lifecycle-20260703-sendmessage-default/report.json`：普通 external dist `ok=true`，`send_message_tool_exposed=false`，但 call 3/4 request text 仍含 async launch result 的 `SendMessage` 文案。
  - `/tmp/upstream-agent-lifecycle-20260703-sendmessage-experimental-env/report.json`：设置 `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` 后 `send_message_tool_exposed=true`，calls `[1,3,4]`；child call 2 仍只有 `Grep,Read`。
  - `/tmp/upstream-agent-lifecycle-20260703-sendmessage-agent-teams/run.err`：当前本地原版 dist 拒绝 `--agent-teams`，报 `error: unknown option '--agent-teams'`。
  - `/tmp/upstream-agent-lifecycle-20260703-sendmessage-ant/report.json`：运行时 `USER_TYPE=ant` 不暴露 `SendMessage`；本地源码树存在 build-time 折叠证据，`$HOME/GolandProjects/claude_code_src_2026/src/constants/prompts.ts:617` 到 `:619` 说明 `USER_TYPE` 是 build-time define，`$HOME/GolandProjects/claude_code_src_2026/src/main.tsx:340` 等位置已折叠为 `"external" === 'ant'`。
- 原版 SendMessage continuation gate：
  - `/tmp/upstream-agent-lifecycle-20260703-sendmessage-running-rerun/report.json`：`ok=true`，`request_count=6`，`response_classes=["parent_agent_tool_use","child_agent_hanging","parent_send_message_tool_use","parent_task_stop_tool_use","parent_after_async_launch","parent_after_agent_notification"]`；call 3 tool_use `SendMessage` 输入 `to=<agentId>`、`summary`、plain-text `message`；call 4 tool_result 为 `{"success":true,"message":"Message queued for delivery to <agentId> at its next tool round."}`。
  - `/tmp/upstream-agent-lifecycle-20260703-sendmessage-resume-boundary/report.json`：`ok=true`，`sendmessage_stopped_resume_failure_captured=true`；completed/stopped task 上的 `SendMessage` 会尝试 resume，但本 capture 的 tool_result 为 `success:false`，错误含 `No transcript found for agent ID...`，说明 stopped resume 是真实边界而非已稳定证明的成功路径。
- Go reusable lifecycle gate：`/tmp/go-agent-lifecycle-20260703-post-agentget-availability/report.json` 中 `request_count=4`，`response_classes=["parent_agent_tool_use","child_agent_final","parent_after_agent_tool_result","parent_after_followup_tool"]`；call 4 request `background_agent_tasks=true`、`completion_notification=true`、`child_done_marker=true`、`output_file=true`，且 `runtime_mentions_agentget_without_tool=false`。
- Go child-error lifecycle gate：修复前 `/tmp/go-agent-lifecycle-20260703-child-error/report.json` 为 `failed_status_captured=true` 但 `failed_marker_captured=false`；修复后 `/tmp/go-agent-lifecycle-20260703-child-error-v6/report.json` 为 `ok=true`、`failed_status_captured=true`、`failed_marker_captured=true`、`runtime_mentions_agentget_without_tool=false`。
- Go cancelled lifecycle gate：修复前 `/tmp/go-agent-lifecycle-20260703-cancelled-v2/report.json` 为 `ok=false`，虽有 `cancelled_status_captured=true`、`cancellation_notification_captured=true`，但 `partial_marker_captured=false`；修复后 `/tmp/go-agent-lifecycle-20260703-cancelled-v4/report.json` 为 `ok=true`，call 4 request 命中 `result_preview_partial_marker=true`、`agent_stop_result_captured=true`、`task_notification_xml_absent=true`、`runtime_mentions_agentget_without_tool=false`。
- Go 显式 `general-purpose` 修复证据：失败日志 `/tmp/go-agent-lifecycle-20260703/cli.log` 曾出现 `unknown subagent_type: general-purpose`；当前 `internal/agentruntime/runtime.go:1163` 到 `:1167` 和测试 `TestRuntimeAcceptsExplicitGeneralPurposeAgentType` 已锁住。
- Go failed-result bridge 证据：`internal/agentruntime/runtime.go:451` 在 provider/model error 分支持久化前调用 `recordFailureContent(...)`；`internal/agentruntime/runtime.go:626` 定义失败摘要写入逻辑；`internal/agentruntime/runtime_test.go:934` 的 `TestRuntimePersistsModelErrorInFailedTaskResult` 锁住 result JSON 和 output file 都含失败原因。
- Go cancelled-result bridge 证据：`internal/tools/agent/agent.go:485` 到 `:532` 的 `AgentStop` 预取消 payload 现在写入 structured `agentruntime.Result`；`internal/tools/agent/agent.go:572` 到 `:599` 从已有 `text_delta` 事件提取 partial text；`internal/agentruntime/runtime.go:441` 到 `:455` 和 `:640` 到 `:652` 保证后台完成覆盖时保留已有 partial/cancel reason，但不为空内容制造假 partial；`TestAgentStopStoresPartialTextResult` 与 `TestRuntimePersistsPartialContentInCancelledTaskResult` 锁住两条路径。
- Go long AgentGet result bridge 证据：`internal/tools/agent/agent.go` 的 `agentTaskResultForModel(...)` 对超过 `toolresult.DefaultLimit` 的 Agent result content 输出 `content_preview/content_truncated/content_bytes/content_note`，同时保留 `output_file`；`TestAgentGetLongResultKeepsOutputFileVisible` 证明下一轮 request 不含完整长尾 marker 和 `<persisted-output>`；`scripts/agent-long-output-resume-acceptance.sh --work-dir /tmp/go-claude-agent-long-output-resume-20260703 --dump /tmp/go-claude-agent-long-output-resume-20260703.jsonl --force` verifier `ok=true`，turn 2 `AgentGet` visible result 3,162 bytes、`persisted_output=0`、`raw_over_limit=0`。
- Go shell-output truncation 证据：`internal/toolresult/toolresult.go` 的 `persistedMessage(...)` 现在检测 `[output truncated after `，在 `Preview` 前提示 shell runner 已经截断、saved file 也缺尾部，并指导缩小命令或重定向到 workspace/configured writable directory 后用 `Read offset/limit`；`TestProcessSurfacesShellTruncationBeforePreview` 锁住该摘要位置。真实 prompt dump `/tmp/go-claude-bash-shell-truncation-20260703.jsonl` verifier `ok=true`，turn 2 `Bash.persisted_output=1`，request 命中 `already truncated by the shell runner before persistence`、`saved file also omits the tail`、`Read offset/limit`。
- 原版 long-output Agent lifecycle 证据：`scripts/upstream-agent-lifecycle-capture.sh --work-dir /tmp/upstream-agent-lifecycle-20260703-long-output --scenario long-output --force`，report `ok=true`、`request_count=4`；call 4 `request_text_bytes=190502`，同时命中 `<task-notification>`、`<status>completed</status>`、`output_file`、`UPSTREAM_AGENT_LONG_OUTPUT_BODY` 和 `UPSTREAM_AGENT_LONG_OUTPUT_TAIL_MARKER`，且 `generic_persisted_output=false`、`structured_content_preview=false`。源码对应 `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/agentToolUtils.ts:603` 到 `:629` 直接把 `extractTextContent(agentResult.content, '\n')` 作为 `finalMessage`，`$HOME/GolandProjects/claude_code_src_2026/src/tasks/LocalAgentTask/LocalAgentTask.tsx:247` 到 `:257` 直接拼进 `<result>`。
- Go continuation 复跑证据：`scripts/agent-message-resume-acceptance.sh --force --work-dir /tmp/go-claude-agent-message-resume-current-20260703 --dump /tmp/go-claude-agent-message-resume-current-20260703.jsonl --prompt-profile claude-compatible`，verifier `ok=true`；successor sub-agent request 命中 `previous finding`、`Agent message from coordinator`、`please continue with new evidence`，父线程 tool_result 命中 `resumed_from_task_id`、`source_transcript`。
- Go SendMessage/AgentMessage tool-surface gate：
  - `/tmp/go-claude-sendmessage-surface-compat-20260703.jsonl`：显式 `claude-compatible --tools "Agent,Grep,Read"` verifier `ok=true`，`tool_count=3`，禁止 `Task/AgentCreate/AgentGet/AgentMessage/SendMessage/LS`。
  - `/tmp/go-claude-sendmessage-surface-native-20260703.jsonl`：不传 `--tools` 的 native 默认工具面 verifier `ok=true`，`tool_count=32`，request 命中 `"name":"AgentMessage"`，禁止 `SendMessage`。
- Go opt-in SendMessage wrapper gate：
  - `/tmp/go-claude-sendmessage-optin-disabled-20260703.jsonl`：显式请求 `Agent,Grep,Read,SendMessage` 但未设置 env opt-in，verifier `ok=true`，`tool_count=3`，禁止 `SendMessage/AgentMessage`。
  - `/tmp/go-claude-sendmessage-optin-enabled-20260703.jsonl`：设置 `GOLANG_CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` 后同一工具列表 verifier `ok=true`，`tool_count=4`，request 命中 `"name":"SendMessage"`，禁止 `AgentMessage`。
  - 本轮 prompt/schema gate：`/tmp/go-claude-sendmessage-recipient-schema-20260703.jsonl` verifier `ok=true`，request `tool_count=4`，命中 `"name":"SendMessage"` 和 `unique agent_name or description`，禁止 `AgentMessage/Task/AgentCreate/AgentGet/LS`；CLI 因 `max_turns=1` 退出，但 request verifier 成功。
  - 原版 broadcast capture：`/tmp/upstream-agent-lifecycle-20260703-sendmessage-broadcast/report.json` 为 `ok=true`，`request_count=5`，response classes 包含 `parent_send_message_tool_use` 和 `parent_after_send_message_result`；transcript 中 `SendMessage` 输入为 `to:"*"`，下一轮 tool_result 为 error：`Not in a team context. Create a team with Teammate spawnTeam first, or set CLAUDE_CODE_TEAM_NAME.`。
  - 原版 structured shutdown capture：`/tmp/upstream-agent-lifecycle-20260703-sendmessage-structured-shutdown/report.json` 为 `ok=true`，`request_count=5`，response classes 包含 `parent_send_message_tool_use` 和 `parent_after_send_message_result`；transcript 中 `SendMessage` 输入为 `to:"reviewer"`、`message.type:"shutdown_request"`，tool_result 为 `success:true`、`request_id:"shutdown-...@reviewer"`；同时写入 `/tmp/upstream-agent-lifecycle-20260703-sendmessage-structured-shutdown/config/teams/default/inboxes/reviewer.json`，证明 structured protocol 有 mailbox side effect。
  - Go `SendMessage` recipient resolver 源码：`internal/tools/agent/agent.go:697` 到 `:820`，schema/description 提醒 task id 优先和唯一 `agent_name/description` fallback；运行时 `sendMessageRecipientTaskID(...)` 先解析 task id，再按 task store 唯一 `agent_name/description` 匹配，broadcast/structured 不进入该路径。
  - 单测 `TestRuntimeDoesNotExposeSendMessageToSubagent` 锁住 sub-agent 默认不继承 `SendMessage`；`TestSendMessageMapsPlainTextToAgentMessage`、`TestSendMessageMapsUniqueAgentNameToAgentMessage`、`TestSendMessageMapsUniqueDescriptionToAgentMessage`、`TestSendMessageRejectsAmbiguousAgentName` 和 `TestSendMessageRejectsUnsupportedShapes` 锁住 wrapper 只支持 plain-text local task continuation，不伪装 broadcast/structured swarm。
  - 全仓库验证：`go test ./... -count=1` 通过。
- Web Agent provider request 级测试：`TestTenantAgentTaskMessageCodeModeInjectsAgentStatusIntoProviderRequest`，提交 `0a4ea19a`。
- 真实 MySQL E2E：`scripts/tenant-mysql-e2e.sh` 通过，测试证据修正提交 `51212d0f`。
- 真实 Web Agent + provider + browser + prompt dump：`/tmp/go-claude-web-agent-real-e2e-prompt-20260703.jsonl`，`runtime_status.sections=["background_agent_tasks"]`，提交 `1e13c0f1`。

置信度：高。剩余边界：Go 未实现原版一次性 notification queue；原版 child-error 把 provider error 标成 completed notification，Go 选择保持 failed 状态并回灌错误文本，这属于有意语义差异；原版 killed 状态名与 Go cancelled 状态名不同，但 partial evidence 已可见；原版 long-output notification 会把完整长 result 放回父线程 request，Go 当前摘要策略在该边界上优于原版；原版 `SendMessage` 是 agent teams opt-in 下的父线程工具，running continuation 已证明为 queued delivery，Go same-shape 兼容路径不暴露 continuation 工具，native 默认路径暴露 `AgentMessage` 增强能力，当前受限 `SendMessage` wrapper 覆盖 plain-text task continuation/resume，并已支持 task id 与唯一 `agent_name/description` recipient fallback；原版 broadcast capture 证明 `to:"*"` 无 team context 时本身返回 team/swarm 边界错误，因此 Go 当前继续拒绝 broadcast 不属于普通 task continuation 退化；原版 structured shutdown capture 证明 structured message 会写入 team mailbox，因此 Go 不能在没有 mailbox/poller/TUI/permission side effect 的情况下假装支持。完整 teammate/broadcast/structured swarm 协议仍未实现。本地 git worktree isolation、hook-based WorktreeCreate isolation、retained-worktree successor resume、detached running process attachment、Go 侧长 AgentGet result 的 output_file 保真已有最小闭环，但 structured swarm/broadcast 仍未完全等价。

### P1-1 已证实并二次加固：真实模型 sub-agent 过度探索会造成预算纪律失控

结论：deterministic stub 门禁通过后，真实复杂只读任务仍暴露出 sub-agent 规划质量缺口。第一类是 narrow 诊断任务中先做无范围大文件 Read，导致子上下文 `raw_over_limit`；第二类是在同模型同工具面 final-report 任务把 `max_tokens` 提到 `32000` 后，sub-agent 生成复杂未配平 Grep 正则，导致 tool error 持续进入后续 prompt history。两类都不是父线程 request orchestration 失败，而是 sub-agent tool planning discipline 不够硬。

修复状态：已修并二次加固。第一轮修复把 sub-agent planning reminder 提前到已有工具上下文后的下一轮；本轮再补 RE2/Grep pattern 纪律，避免多个函数名搜索时用复杂未配平 group。

证据：

- pre-fix 真实 dump `/tmp/go-claude-real-diagnostic-20260703.jsonl`：父线程 final turn `raw_over_limit=0`，但 subagent turn 4/5/6 `raw_over_limit=1`。
- 具体失败调用：subagent `tool_use_id=call_jrSE0GrSfRKHFaH2pb2HZh4X` 调用 `Read`，输入 `{"file_path":"internal/agentruntime/runtime.go"}`，未传 `offset/limit`，结果 72,130 bytes，超过 subagent `limit=50000`。
- Go 旧行为证据来自 pre-fix dump：subagent turn 4 已发生大块 Read，turn 5 才出现 `tool_planning`，说明提醒晚于问题动作。
- Go 当前实现只要 subagent 已有工具上下文就注入 planning，并明确 narrow read-only diagnostics 先用 scoped Grep，再用 `offset`/`limit` 读最小 line range：`internal/agentruntime/runtime.go:718`。
- 新测试 `TestRuntimeAddsSubagentToolPlanningReminderAfterFirstToolResult` 证明 1 个 tool_result 后下一轮 request 即包含 sub-agent planning：`internal/agentruntime/runtime_test.go:422`。
- post-fix 真实 dump `/tmp/go-claude-real-diagnostic-post-subagent-planning-20260703.jsonl` verifier `ok=true`；subagent turn 2 起 `runtime_status.sections=["tool_planning"]`，subagent final max tool result 3,657 bytes，`raw_over_limit=0`。
- 同模型同工具面 16000 final-report gate `/tmp/go-claude-final-report-agent-sameshape-20260703.jsonl` verifier `ok=true`，最终主线程 request 含 `background_agent_tasks` 与 `tool_planning`，最终输出包含 `runtimeStatusText`、`runtimeTodoStatus`、`runtimePlanStatus`、`runtimeAgentTaskStatus`、`subagentToolPlanningStatus` 和 Task/Agent 源码路径。
- 同一任务 32000 复跑 `/tmp/go-claude-final-report-agent-sameshape-32000-20260703.jsonl` verifier `ok=false`：主线程 final request 无 tool error，但 sub-agent turn 3-6 各有 1 个 Grep error，turn 6 还出现 `persisted_output=1`。
- 具体错误证据在 `/tmp/go-claude-final-report-agent-sameshape-32000-20260703.run.log`：sub-agent Grep pattern `func (.*createTask|func (.*finishTask|func (.*Run|...` 触发 RE2 `missing closing )`。
- 原版 clean same-model capture `/tmp/upstream-agent-final-report-gpt55-rg-20260703/capture.jsonl` 无 tool_result error；32000 behavior compare `/tmp/behavior-compare-final-report-rg-32000-20260703.json` 将 Go `tool_result_errors` 标为唯一 error 级差异。
- 本轮修复点：`internal/agentruntime/runtime.go` 的 `subagentToolPlanningStatus(...)` 新增 RE2/Grep pattern 纪律；`TestRuntimeAddsSubagentToolPlanningReminderAfterFirstToolResult` 锁住 `RE2 regular expressions`、`multiple simple Grep calls`、`complex unbalanced groups`。
- post-fix 32000 same-shape gate `/tmp/go-claude-final-report-agent-sameshape-32000-post-re2-20260703.jsonl` verifier `ok=true`：final main request `tool_count=0`，runtime sections 为 `turn_budget/tool_planning/background_agent_tasks`，`tool_result_errors=0`；sub-agent 5 turns / 10 tool calls，无 tool error、无 raw over limit、无 persisted output。
- post-fix prompt compare `/tmp/promptdump-compare-final-report-rg-32000-post-re2-20260703.json` 为 `ok=true`，无 error；当时剩余 warn 仍是 system blocks、auto_memory bytes、Agent description/schema，后续 latest compare 已把 Agent schema hash 收敛，只剩 Agent description bytes。
- post-fix behavior compare `/tmp/behavior-compare-final-report-rg-32000-post-re2-20260703.json` 为 `ok=true`，无 error，剩余 info 为 message/tool order、Go final tools disabled、runtime sections、user text bytes。
- post-fix dump 中主线程残留的 `Grep persisted_output=1` 已进一步定位：turn 2 `tool_use_id=call_KCqHm660Jwr2X0ugtyeQMjKP` 的 Grep 输出 20,589 bytes，主要命中 `.claude/worktrees/agent-djon67usvuf4/...` 历史 worktree 副本，而不是当前源码本身过大。
- 本轮修复点：新增 `internal/tools/searchignore.go` 的 `ShouldSkipSearchDir(...)`，让 `Grep`/`Glob` 默认递归搜索跳过 `.claude/worktrees`；`TestGrepSkipsManagedAgentWorktrees` 和 `TestGlobSkipsManagedAgentWorktrees` 锁住该边界。

置信度：高。剩余边界：这类 prompt discipline 不能数学保证模型永不生成非法 regex，但同任务 32000 post-fix gate 已证明本次观测到的 error 级差异被清零。主线程 Grep persisted output 的已观测来源是 managed worktree 搜索噪声，已用递归搜索忽略规则修复；后续仍可继续评估 system block/auto_memory/Agent description warning。

### P1-2 已部分修复：compatible auto memory protocol/layout 仍非逐字等价，但关键行为规则和进入顺序已补齐

结论：同任务 same-shape compare 仍显示 Go `auto_memory` system block 小于原版。直接比较 `/tmp/go-auto-memory-final-report-post-re2-20260703.txt` 与 `/tmp/upstream-auto-memory-final-report-gpt55-rg-20260703.txt` 后，差异不再是“Go 没有 auto memory”，而是三类更窄的问题：Go block 曾混入 `# Git Snapshot`/`# Environment` 且 memory 位于 Environment 后；Go typed-memory taxonomy 比原版短；原版对用户画像安全、正/负反馈保存、stale memory 验证和 memory/plan/task 边界的说明更完整。当前 HEAD 已补齐第三类高影响行为规则，并把 compatible `auto_memory` 前移为 `session_guidance -> auto_memory -> env_info_simple -> gitStatus` 的 named dynamic/system-context layout，保留 Go 的只读任务不写 memory 保护。

修复状态：部分修复。`compatibleMemorySystemBlock(...)` 已扩充高影响 memory 行为规则，并由 request-level test 与 prompt dump verifier 锁住。`defaultSystemPromptParts(...)` 现在在 compatible profile 下把 `auto_memory` 作为 named dynamic section 放到 `session_guidance` 后、`env_info_simple` 前；`compatibleGitStatusSystemContext(...)` 把 Go 的 git snapshot 转成原版风格 `gitStatus:` 并放到 `# Environment` 后；`assembleContextMessages(...)` 在空工作区也注入 auto-memory protocol，并用 duplicate guard 避免 fallback 追加第二份 memory block。原版更长示例文本是否影响真实任务表现仍未证明。

证据：

- 原版 dynamic section 顺序：`$HOME/GolandProjects/claude_code_src_2026/src/constants/prompts.ts:491` 到 `:500` 依次加入 `session_guidance`、`memory`、`env_info_simple`。
- 原版普通 typed-memory prompt：`$HOME/GolandProjects/claude_code_src_2026/src/memdir/memdir.ts:199` 到 `:266` 的 `buildMemoryLines(...)`。
- Go compatible memory 入口：`internal/query/query.go` 的 `assembleContextMessages(...)` 与 `compatibleMemorySystemBlock(...)`。
- Go block 合并入口：`internal/query/query.go` 的 `appendCompatibleMemoryToSystemBlocks(...)`；当前 compatible auto memory source 已从 fallback `dynamic_prompt+auto_memory` 收敛为 named `dynamic_prompt`。
- 差异文本证据：Go `/tmp/go-auto-memory-final-report-post-re2-20260703.txt` 为 `8931` bytes，曾包含 `# Session-specific guidance`、`# Git Snapshot`、`# Environment`、`# auto memory`；原版 `/tmp/upstream-auto-memory-final-report-gpt55-rg-20260703.txt` 为约 `15154/15206` bytes，包含 `# Session-specific guidance`、`# auto memory`、`# Environment`。
- 本轮 request gate：`/tmp/go-claude-memory-protocol-refine-20260703.jsonl` verifier `ok=true`，新增规则文本全部入模；turn 1 `system_bytes=18995`，`auto_memory` block `9768` bytes。
- layout refine gate：`/tmp/go-claude-agent-memory-layout-rerun-20260703.jsonl` verifier `ok=true`；turn 1 block 2 为 `kind=auto_memory`、`source=dynamic_prompt`、`text_bytes=10794`；pre-gitStatus refinement marker 顺序为 `# Session-specific guidance=9207`、`# auto memory=10048`、`# Git Snapshot=19172`、`# Environment=19720`。
- gitStatus layout refine gate：`/tmp/go-claude-compatible-gitstatus-layout-20260703.jsonl` verifier `ok=true`；turn 1 block 2 为 `kind=auto_memory`、`source=dynamic_prompt`、`text_bytes=10738`；marker 顺序为 `# Session-specific guidance=9207`、`# auto memory=10048`、`# Environment=19172`、`gitStatus:=19455`、`# Git Snapshot=-1`。
- gitStatus compare：`/tmp/go-vs-upstream-compatible-gitstatus-layout-20260703.json` 为 `ok=true`；当时剩余 warn 为 system bytes/block count/kinds、`auto_memory` bytes、Agent description/schema，后续 latest compare 已把 Agent schema hash 收敛，只剩 Agent description bytes。
- memory prompt post-layout gate：`/tmp/go-claude-memory-prompt-post-layout-rerun-20260703.jsonl` verifier `ok=true`；block 2 为 `kind=auto_memory`、`source=dynamic_prompt`、`text_bytes=9768`；`# auto memory=9547`、`# Environment=18694`。
- memory post-gitStatus regression：`/tmp/go-claude-memory-prompt-post-gitstatus-layout-20260703.jsonl` verifier `ok=true`；`/tmp/go-claude-memory-behavior-post-gitstatus-layout-20260703/summary.json` 为 `ok=true`。
- memory behavior gate：`scripts/memory-behavior-acceptance.sh --out-dir /tmp/go-claude-memory-behavior-post-layout-v3-20260703 --force` 通过；recall、readonly-no-write、explicit-remember 三场景 `ok=true`，明确 remember 场景把 `MEMORY_WRITE_ACCEPTANCE_PREF` 写入隔离项目 memory，且未触发 writable-root denial。
- low-value explicit-save gate：`scripts/memory-behavior-acceptance.sh --out-dir /tmp/go-claude-memory-behavior-pr-summary-20260703 --force` 通过；新增 `pr-summary-no-write` 场景，显式要求保存 PR 列表且 `Read,Grep,Write,Edit` 暴露时，memory 文件快照保持不变，同时 `explicit-remember` 仍能写入长期偏好。
- stale memory claim gate：`scripts/memory-behavior-acceptance.sh --out-dir /tmp/go-claude-memory-behavior-stale-claim-rerun-20260703 --force` 通过；新增 `stale-claim-verifies` 场景，memory 声称函数 `STALE_MEMORY_ACCEPTANCE_FUNC` 存在但当前 workspace 不包含该函数，模型使用 `Grep` 验证后输出 `stale-verified`，memory 快照保持不变。
- ignore-memory branch-pollution gate：`scripts/memory-behavior-acceptance.sh --out-dir /tmp/go-claude-memory-behavior-ignore-memory-20260703 --force` 通过；新增 `ignore-memory-no-branch-pollution` 场景，request 中含 hostile marker `MEMORY_IGNORE_BRANCH_POLLUTION_MARKER`，但用户明确要求忽略 memory 后 stdout 只命中 `ignore-memory-ok`，未提该 marker，memory 快照保持不变。
- 测试：`TestClaudeCompatiblePromptProfileLoadsMemoryIntoSystemBlock` 现在锁住 `senior software engineer differently than a student`、`Record from failure and success`、`not just asking about history`、`trust what you observe now rather than acting on the stale memory`。
- 测试：`TestClaudeCompatiblePromptProfileLoadsMemoryIntoSystemBlock` 现在同时锁住原版 `WHAT_NOT_TO_SAVE_SECTION` 中的 `PR list or activity summary` 与 `surprising or non-obvious` 规则；`scripts/memory-prompt-acceptance.sh --dump /tmp/go-claude-memory-prompt-pr-summary-20260703.jsonl --force` 的 request verifier `ok=true`。
- 测试：`TestClaudeCompatiblePromptProfileLoadsMemoryIntoSystemBlock` 现在同时锁住 `If the memory names a file path: check the file exists.` 与 `If the memory names a function or flag: grep for it.` 两个具体 stale-claim 验证动作。
- 测试：`TestClaudeCompatiblePromptProfileLoadsMemoryIntoSystemBlock` 现在同时锁住 `If the user says to ignore or not use memory`、`proceed as if MEMORY.md were empty`、`Do not apply remembered facts` 三个 branch-pollution 防护 cue。
- 测试：`TestClaudeCompatiblePromptProfileLoadsMemoryIntoSystemBlock` 现在同时锁住 `# auto memory` 在 `# Environment` 前，且 source 为 `dynamic_prompt`；`TestClaudeCompatiblePromptProfileInjectsAutoMemoryWithoutWorkspaceDocs` 锁住空工作区也注入 auto-memory protocol，且不生成 `# claudeMd` workspace user-context message。
- 测试：`TestClaudeCompatiblePromptProfileFormatsGitStatusAfterEnvironment` 用真实 `git init` 工作区锁住 compatible request 使用 `gitStatus:`、位于 `# Environment` 后，并且不包含 Go-only `# Git Snapshot`。

置信度：中高。当前能证明 Go 已补齐原版 memory protocol 的关键行为规则，并已把 compatible auto memory 的进入顺序前移到 Environment 前，同时把 Git Snapshot heading/order 收敛为原版 capture 中的 `gitStatus:` 形态；不能证明原版更长示例文本或剩余字节差异会单独导致复杂代码任务表现差异。下一步若继续处理该项，应做 memory write/recall 专项 eval，或进入 Agent description 的行为定责，而不是继续按字节追加提示词。

### P1-3 已证实并修复：真实最终报告质量需要独立验收

结论：request orchestration 和 sub-agent budget 修好后，仍必须验证最终回答是否真正综合了函数级证据。否则模型可能已经看到正确上下文，却在最终报告里承认“未完全闭环”或缺少 Task/Agent 工具本体证据。

修复状态：已新增真实模型 final-report quality gate，并用同任务 dump/log 复验通过。

证据：

- 新脚本 `scripts/final-report-quality-prompt-acceptance.sh` 要求真实复杂只读任务最终输出包含 `runtimeStatusText`、`runtimeTodoStatus`、`runtimePlanStatus`、`runtimeAgentTaskStatus`、`subagentToolPlanningStatus` 以及 Task/Agent 相关源码路径。
- 新脚本 `scripts/code-mode-prompt-acceptance.sh` 支持 `--run-log`、`--require-output-text`、`--forbid-output-text` 和 `--forbid-tool`，把最终回答质量和暴露工具边界纳入可重复门禁。
- prompt dump verifier 当前同时搜索 JSON request 形态与 decoded request text，避免 `output_mode:"content"` 被 JSON escape 造成假阴性：`internal/promptdump/verify.go:460`。
- prompt dump verifier 当前通过 `ForbidTool` 检查 `request.tools[].name`，避免 Read 到源码里的 `"name":"Bash"` 片段误报为工具暴露：`internal/promptdump/verify.go:447`。
- sub-agent prompt 当前明确“文件列表不是 implementation evidence”，并要求 Grep 定位后 scoped Read：`internal/agentruntime/runtime.go:1157`、`internal/agentruntime/runtime.go:1178`。
- 真实模型 dump `/tmp/go-claude-final-report-quality-rerun-20260703.jsonl` 与 run log `/tmp/go-claude-final-report-quality-rerun-20260703.run.log` 复验 `ok=true`：最终输出含函数级证据，subagent final turn `tool_count=0`、`runtime_status.sections=["turn_budget","tool_planning"]`，main final `raw_over_limit=0`，且 forbidden output 文本未出现。
- side-by-side 自动化 gate：`scripts/final-report-side-by-side-compare.sh` 串联原版 `final-report` capture、Go same-shape `Agent,Grep,Read` 真实 gate、`promptdump-compare` 与 `behavior-eval-compare`；本轮 `/tmp/go-claude-final-report-side-by-side-20260703/summary.json` 为 `ok=true`。Go final-report dump `/tmp/go-claude-final-report-side-by-side-20260703/go-final-report.jsonl` verifier `ok=true`，main 6 turns、subagent 4 turns / 9 tool calls、final `tool_count=0`，且 `tool_result_errors=0`、`persisted_output=0`、`raw_over_limit=0`。原版 capture `/tmp/go-claude-final-report-side-by-side-20260703/upstream/report.json` 为 `ok=true`，5 requests，transcript 存在。compare 剩余均为 warn/info：system/auto_memory/Agent description bytes、main request count、final tool/message order、runtime sections、user text bytes，无 error 级行为差异。
- matrix integration：`scripts/prompt-acceptance-matrix.sh` 新增可选 `final-report-side-by-side` 场景；`/tmp/go-claude-prompt-acceptance-matrix-side-by-side-20260703/matrix-report.json` 为 `ok=true`，对应 summary `/tmp/go-claude-prompt-acceptance-matrix-side-by-side-20260703/final-report-side-by-side/summary.json` 为 `ok=true`。`--verify-only` 也已用 `/tmp/go-claude-matrix-side-by-side-verify-20260703/matrix-report.json` 证明可读取既有 side-by-side summary 复核，不重新跑模型。

置信度：高。剩余边界：这证明 Go 当前真实任务可产出函数级证据，并且同任务双边自动化 gate 暂未发现 error 级行为差异；仍不能证明所有任务都已超过原版。剩余 system/auto_memory/Agent.description 字节差、最终 request/message/tool order 和 runtime sections 差异需要继续用专项 eval 定责，不能仅按字节判断优劣。

### P1-3a 已证实并修复：Explore/Plan 只读子 Agent 携带 stale git snapshot

结论：原版 Claude Code 对 built-in `Explore`/`Plan` 做了明确的上下文瘦身：只读 search/planning agent 不处理父线程 CLAUDE.md 的 commit/PR/lint 规则，也不需要 parent-session-start 的 stale `gitStatus`；需要 git 信息时应自行获取 fresh 状态。Go 当前不会把父 CLAUDE.md 直接注入 sub-agent runtime，但旧实现会给所有 sub-agent 无条件拼入 `gitcontext.Snapshot(...)`。对 `Explore`/`Plan` 来说，这是可证据支持的无关上下文。

修复状态：已修复。新增 agent 定义字段 `OmitGitStatus`，built-in `Explore`/`Plan` 设置为 true；runtime 根据该字段跳过 sub-agent system 里的 git snapshot，普通/general-purpose sub-agent 保持原行为。

证据：

- 原版省略逻辑：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/runAgent.ts` 在 `Explore`/`Plan` 场景移除 `gitStatus`，并说明 stale parent-session-start gitStatus 对 read-only search agents 是 dead weight。
- 原版 agent 定义字段：`$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/loadAgentsDir.ts` 的 `omitClaudeMd?: boolean` 注释说明 read-only `Explore`/`Plan` 会省略 CLAUDE.md hierarchy。
- Go 定义层：`internal/agents/agents.go` 支持 `OmitGitStatus`、`omitGitStatus` 和 `omit_git_status`。
- Go built-ins：`internal/agents/builtin.go` 为 `Explore`/`Plan` 设置 `OmitGitStatus: true`。
- Go runtime：`internal/agentruntime/runtime.go` 通过 `subagentEnvOptions{OmitGitStatus: agent.OmitGitStatus}` 控制是否拼 `gitcontext.Snapshot(...)`。
- 测试：`TestRuntimeLoadsBuiltInExploreAgent`、`TestRuntimeLoadsBuiltInPlanAgent` 证明 `Explore`/`Plan` system prompt 保留 `# Environment` 和 cwd，但不包含 `# Git Snapshot` / `Status:`。
- prompt dump 级测试：`TestRuntimeFullDumpShowsBuiltInExploreOmitsGitSnapshot` 证明 full dump 的真实 sub-agent request system 命中 `READ-ONLY exploration task`、`# Environment`、cwd，且不含 stale git snapshot。
- 回归边界：`TestRuntimeAddsSubagentEnvironmentDetails` 证明普通 sub-agent 仍保留 git snapshot；`TestLoadAgentDefinitionSchemaFields` 和 `TestLoadAgentDefinitionSnakeCaseAliases` 锁住配置字段解析。

置信度：高。影响判断：中等偏高。它不会解释所有历史表现差距，但能降低只读内置 agent 的 token/注意力噪声和 cache 波动，是朝“综合能力不低于原版”的真实能力优化。剩余边界：Go 仍未实现原版 `statusline-setup`、remote isolation、完整 team/swarm mailbox，因此不把这些内容写入 tool description 或 agent 能力。

### P1-3b 已证实并修复：Agent description 提示未暴露的 Glob 工具

结论：same-shape code-mode request 只暴露 `Agent,Grep,Read` 时，Go 旧 `Agent.description` 仍提示 “use Read or Glob directly” 和 “try Glob or Grep first”。这会让模型基于不存在的工具规划下一步。原版同任务 capture 也有类似问题：tools 为 `Agent,Grep,Read`，但 description 仍提 `Glob tool`。因此本项不是机械复刻，而是 Go 在当前真实工具面下对原版问题做更精确的修复。

修复状态：已修复。`CompatTool.Description()` 现在根据 registry 里的真实 enabled tools 动态生成 when-not-to-use 段；没有 `Glob` 时不提 `Glob`，有 `Glob` 时才保留对应提示。

证据：

- Go pre-fix request：`/tmp/go-claude-agent-builtins-20260703.jsonl` 中 `tool_names=["Read","Grep","Agent"]`，但 `Agent.description` 命中 `Read or Glob`、`Glob or Grep`。
- 原版同类问题：`/tmp/upstream-agent-final-report-gpt55-rg-20260703/capture.jsonl` 中 `tool_names=["Agent","Grep","Read"]`，但 `Agent.description` when-not-to-use 段仍提示 `Glob tool`。
- Go 注册路径：`internal/cli/cli.go` 显式 `--tools Agent,Grep,Read` 时把当前 registry 传给 `agenttool.NewCompatWithHooksAndCWD(...)`。
- Go 修复点：`internal/tools/agent/agent.go` 的 `whenNotToUseDescription()` 用 `compatToolAvailable("Read"|"Grep"|"Glob")` 生成 direct-tool 提示。
- 测试：`TestAgentCompatDescriptionMatchesEnabledDirectTools` 证明 `Read/Grep` 场景命中 `use Read directly`、`try Grep first`，且不出现 `Read or Glob`、`Glob or Grep`；`Glob` 真实启用时会恢复 `Read or Glob`、`Grep or Glob`。
- prompt gate：`/tmp/go-claude-agent-direct-tools-20260703.jsonl` verifier `ok=true`，真实 request 命中 `"name":"Agent"`、`use Read directly`、`try Grep`，禁止旧 `Read or Glob`、`Glob or Grep`。
- compare：`/tmp/go-vs-upstream-agent-direct-tools-20260703.json` 为 `ok=true`；`Agent.input_schema_hash` 仍同为 `b00793ce...`；`Agent.description_bytes=5173` vs 原版 `6542`。

置信度：高。影响判断：中等。它不会改变工具 schema 或生命周期，但能减少模型调用/计划未暴露工具的概率，是同工具面下明确优于原版的 prompt-surface 精确化。剩余边界：原版 `Agent.description` 中的 examples/statusline/team/swarm 文本仍未等价；Go 继续不暴露未实现能力。

### P1-4 已证实并修复：skills progressive loading / compact preservation

结论：skill 只作为普通历史消息会在 compact/resume 后丢失，是明确行为风险。当前已修为 active skill context request-time reinjection。

原版证据：

- 原版 remote skill load 后调用 `addInvokedSkill(...)` 让 transformed skill content survive compaction：`$HOME/GolandProjects/claude_code_src_2026/src/tools/SkillTool/SkillTool.ts:963`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/SkillTool/SkillTool.ts:1083`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/SkillTool/SkillTool.ts:1088`。
- 原版 skill prompt 说明匹配 skill 时是 blocking requirement：`$HOME/GolandProjects/claude_code_src_2026/src/tools/SkillTool/prompt.ts:188`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/SkillTool/prompt.ts:190`。

Go 当前证据：

- Skill tool result 的 context messages 被记录并在 request 构建前补回：`internal/query/query.go:2139`、`internal/query/query.go:2140`、`internal/query/query.go:2183`、`internal/query/query.go:2198`。
- MySQL tenant skill E2E 现在扫描全部 provider request messages 确认 loaded body 可见：`internal/server/mysql_e2e_test.go:960`、`internal/server/mysql_e2e_test.go:976`、`internal/server/mysql_e2e_test.go:986`。
- 真实 CLI auto-compact gate：`scripts/skill-compact-prompt-acceptance.sh` 使用隔离 `$HOME/.go-claude/settings.json` 启用 auto compact，真实驱动 `Skill -> compact summary -> compacted main request -> Read -> final`。运行 `/tmp/go-claude-skill-compact-20260703.jsonl` 证明 turn 2/3 `compact_summaries=1`，compact 后 request 同时含 `Conversation summary so far` 和 `MATRIX_SKILL_ACTIVE`，provider report 中 `max_active_skill_reminder_count=1`。
- 原版 runtime capture：`scripts/upstream-agent-lifecycle-capture.sh --scenario skill-compact` 已捕获原版 `parent_skill_tool_use -> compact_summary -> parent_after_skill_compact`；`/tmp/upstream-skill-compact-verified-20260703/upstream/report.json` 为 `ok=true`，post-compact request 同时命中 continuation summary 与 `MATRIX_SKILL_ACTIVE`。这是证据探针，不继续扩展为机械复刻原版 compact 内部 message shape 的目标。
- 矩阵入口：`scripts/prompt-acceptance-matrix.sh --out-dir /tmp/go-claude-prompt-acceptance-matrix-skill-compact-20260703 --scenarios skill-compact --force`，`matrix-report.json` 为 `ok=true`。
- 相关提交：`71b7ff23 fix: preserve loaded skill context`、`51212d0f test: verify tenant skill request context in mysql e2e`。

置信度：高。剩余边界：Go 侧真实 CLI request gate 和原版 runtime capture 都已证明 Skill/compact 保留链路成立；但这不等于两边 compact message grouping、attachment encoding、TUI transcript 或所有 resume/compact 分支完全等价。按当前优先级，不继续机械追平这些内部协议，下一步转向 go-claude 自身 Agent Capability Loop。

### P1-5 已证实并修复：TodoWrite result 语义和 stale completed todo

结论：tool result 文案会影响模型下一步规划。Go 旧行为只报告保存文件，弱于原版行为性 reminder；all-completed 还会留下 stale todo。当前已修。

原版证据：

- 原版 TodoWrite all done 时将 app state todo 清空：`$HOME/GolandProjects/claude_code_src_2026/src/tools/TodoWriteTool/TodoWriteTool.ts:69`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/TodoWriteTool/TodoWriteTool.ts:70`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/TodoWriteTool/TodoWriteTool.ts:88`。
- 原版 tool_result 是行为提醒：`$HOME/GolandProjects/claude_code_src_2026/src/tools/TodoWriteTool/TodoWriteTool.ts:104`、`$HOME/GolandProjects/claude_code_src_2026/src/tools/TodoWriteTool/TodoWriteTool.ts:105`。

Go 当前证据：

- Go `TodoWrite` 支持 `activeForm`，all-completed 持久化为空，并返回行为提醒：`internal/tools/todowrite/todowrite.go:95`、`internal/tools/todowrite/todowrite.go:151`、`internal/tools/todowrite/todowrite.go:167`。
- runtime todo in_progress 优先显示 activeForm：`internal/query/query.go:3561`。
- 相关提交：`87a263f8 fix: align todowrite progress result`、`519331ef feat: support todowrite active form`。

置信度：中高。

### P1-6 已证实并修复：permission/workspace hidden context

结论：工具层知道当前 permission mode 和额外 writable roots，但模型不知道，会导致无效工具调用或错误规划。当前已修为 code-mode runtime status。

证据：

- Go runtime permission status 输出 mode 和 additional writable directories：`internal/query/query.go:3372`、`internal/query/query.go:3384`、`internal/query/query.go:3392`。
- 相关提交：`e9b0c26e feat: expose permission mode in runtime context`、`80f97707 feat: expose additional writable roots in prompt`。
- 真实 prompt dump：`/tmp/go-claude-add-dir-permission-context-20260703.jsonl`，verifier 命中 `## Permission context`、`- mode: deny`、additional writable directory。

置信度：中高。

### P1-7 已证实并修复：Skill 工具 schema/result surface 与原版不同

结论：Skill 渐进式加载是长任务能力的重要链路。原版 `SkillTool` 的模型可见 schema 使用 `skill,args`，inline tool_result 是 `Launching skill: <name>`，并在 description 中强调 slash command 即 skill、匹配 skill 是阻塞要求、不要只提 skill 不调用。Go 旧实现使用 `name,prompt`，tool_result 为 `Skill <name> loaded...`，description 缺这些关键选择规则。该差异会影响同模型是否先调用 Skill、用哪个参数名、调用后是否继续按 skill 指令执行。当前已在 compatible profile 下修复并用 side-by-side gate 验证。

修复状态：已修复。Go 原生路径仍保留 `name,prompt`，compatible profile 下对齐原版 `skill,args` schema、`$schema`、description 关键规则和 inline result surface。

证据：

- 原版 schema/result 源码：`$HOME/GolandProjects/claude_code_src_2026/src/tools/SkillTool/SkillTool.ts` 的 `inputSchema` 定义 `skill,args`；`mapToolResultToToolResultBlockParam(...)` 返回 `Launching skill: ${result.commandName}`。
- 原版 compact 保留源码：`$HOME/GolandProjects/claude_code_src_2026/src/services/compact/compact.ts` post-compact 阶段调用 `createSkillAttachmentIfNeeded(context.agentId)`。
- Go 修复源码：`internal/tools/skill/skill.go` 的 compatible `Description()` / `InputSchema()` / `Run(...)`。
- 单测：`internal/tools/skill/skill_test.go` 的 `TestSkillToolClaudeCompatibleSchemaAndInput` 锁住 compatible schema/description/result/input alias。
- Go prompt dump gate：`/tmp/go-claude-skill-compatible-schema-20260703.jsonl` verifier `ok=true`；turn 1 tool input 为 `{"skill":"matrix-skill"}`，turn 2 tool_result 为 `Launching skill: matrix-skill`，request 命中 active skill marker。
- 原版/Go side-by-side：`/tmp/go-claude-skill-load-side-by-side-post-schema-20260703/summary.json` 为 `ok=true`；原版 response classes 为 `parent_skill_tool_use -> parent_after_skill_load`；修复后 `tools.Skill.description_bytes` 和 `tools.Skill.input_schema_hash` warning 消失。
- matrix 集成：`/tmp/go-claude-prompt-acceptance-matrix-skill-side-by-side-20260703/matrix-report.json` 为 `ok=true`，新增可选场景 `skill-load-side-by-side`。

置信度：高。剩余边界：Go active skill 仍通过 `ContextMessages` 重注入，原版通过 `newMessages`/invoked skill attachment 进入上下文；side-by-side behavior compare 仅剩 final `message_count` 和 `user_text_bytes` info，不是 error/warning。后续若要继续处理，应做 compact/resume 后的 side-by-side，而不是再按 Skill description 字节追平。

### P2 已否定或降级的根因

#### Web Agent / tenant Agent task 只进数据库或 WebUI，不入模

状态：已否定，置信度高。

证据：

- server handler 会从 task metadata 解析 prompt mode，空值默认 code：`internal/server/server.go:4155`、`internal/server/server.go:4590`。
- provider request test 证明 `/tenant/agent-tasks/:id/message` code-mode request 含 `## Background agent tasks`、completed task preview 和 output_file：提交 `0a4ea19a`。
- `scripts/tenant-mysql-e2e.sh` 通过：提交 `51212d0f`。
- `scripts/web-agent-real-e2e.sh` 真实 provider/browser/prompt dump 通过：`/tmp/go-claude-web-agent-real-e2e-prompt-20260703.jsonl`，提交 `1e13c0f1`。

#### Todo/Plan/progress 完全不进模型上下文

状态：已否定，置信度中高。

证据：

- runtime status 加载 todos/plan/background tasks：`internal/query/query.go:3357`、`internal/query/query.go:3360`、`internal/query/query.go:3363`。
- 相关测试：`TestPromptModeCodeInjectsRuntimeStatusAsRequestContext`、`TestTodoWriteResultAndRuntimeTodosEnterNextRequest`。
- 综合 prompt dump：`/tmp/go-claude-final-diagnostic-20260703.jsonl` 的 turn 1/2/3/4/5 均含 `active_todos` 与 `plan_mode` runtime sections，full request 命中 `## Active todos`、`completed: 1 hidden`、`## Plan mode`。

#### TUI 展示差异是主要根因

状态：降级。TUI 会影响人类观察和交互决策，但不是当前最强模型行为根因。

证据：

- 当前核心验收都看 provider request / prompt dump，而非 TUI：`/tmp/go-claude-prompt-acceptance-matrix-head-20260703/matrix-report.json`、`/tmp/go-claude-web-agent-real-e2e-prompt-20260703.jsonl`。
- TUI 改动可以解释历史体感差距，但如果模型 request 已缺状态，修 TUI 不会修模型行为；反之，当前主要链路已经以 request gate 验证。

## 当前仍无法证明的内容

没有昨天两边的完整 transcript/request dump，仍不能严格证明“昨天具体是哪一次 token、哪一个 tool_result、哪一轮 compact/resume 导致表现分叉”。当前只能证明以下结构性根因曾经真实存在并已被修复或收敛。

还需要采集：

- 原版 Claude Code 同任务完整 request dump：system blocks、messages、tools、tool_use input、tool_result、attachments、compact boundary。
- go-claude 同任务修复前 dump。如果只有当前修复后 dump，只能证明现在通过，不能还原昨天失败现场。
- 两边启动参数：model id、max tokens、max turns、permission mode、prompt mode、workspace/cwd、provider/OpenAI-compatible adapter、是否 resume/compact。
- 原版 `<task-notification>` 的完成态、child-error、killed、long-output transcript 已通过 reusable gate 捕获；`SendMessage` 暴露条件和 running continuation 已证明，stopped resume no-transcript boundary 也已捕获。Go 侧受限 opt-in `SendMessage` wrapper 已有 request gate 和单测，本轮补齐 task id + 唯一 `agent_name/description` recipient fallback；本地 git worktree isolation、hook-based WorktreeCreate isolation、AgentMessage retained-worktree successor resume、detached running process attachment、SubagentStart hook additional context、长 AgentGet result output_file 保真已有单测和 prompt dump/请求捕获闭环；但仍缺完整 structured swarm/broadcast 和同一真实任务下双边 request/transcript。

## 当前建议路线

1. 不再优先修 Web Agent task 可见性。
   已有 fake provider、MySQL E2E、真实 provider/browser/prompt dump 三层证据否定这个根因。

2. 显式 `Agent,Grep,Read` 工具面已对齐，双边 workspace guidance source gate 已通过，`AGENTS.md fallback` 和 strict Read schema 已有 same-shape 控制。
   当前 Go 侧已能在兼容 gate 下暴露原版同名 `Agent`，并增强了 Agent description/schema contract；latest compare 进一步证明 Agent schema hash 已对齐。双边 guidance matrix 证明 `AGENTS.md fallback` 是 Go 的有边界增强，也是和原版普通 code session 的明确差异。现在该差异可通过 `claude-compatible-strict` 排除；strict compare 已确认 `Read` schema warning 消失。kind-aligned compare 进一步把下一阶段收敛目标缩小到 `auto_memory/session guidance` 和 `Agent` lifecycle/description，而不是旧的按位置 cache mismatch；其中 `auto_memory/session guidance` 和 `Agent.description` 已完成第一轮收敛。`SendMessage` 最新证据显示普通 external 原版不暴露，env opt-in 才暴露；Go 当前已实现受限 opt-in wrapper，默认不污染 same-shape `Agent,Grep,Read`，并补齐 plain-text local recipient name fallback；完整 teammate/broadcast/structured swarm 协议仍不伪装支持。

3. 优先做 go-claude 自身 Agent Capability Loop，而不是继续机械补原版内部协议。
   当前最高 ROI：子 agent 输出结构化 `evidence / assumptions / unknowns / verification / next action`；父 agent 接收后把证据和风险结构化送入下一轮 request/runtime status；用 prompt dump、golden、acceptance script 和真实 Task/Agent 任务验证字段存在且能影响下一步规划。

4. 保持每个改动的闭环标准。
   最小修复 -> targeted tests -> prompt dump verifier -> full matrix -> `go test ./... -count=1` -> `git diff --check` -> 文档 -> commit/push。

## 当前总体判断

如果只能基于本地证据给“哪个因素导致差别巨大”的排序：

1. 最高置信：每轮 request context orchestration 差异，包含 runtime status、tool_result、skills、todo/plan、Agent status、permission/workspace、resume/compact。
2. 次高置信：tool_result / Read / 大输出治理差异，导致模型下一步基于残缺或过量证据规划；真实 subagent 过度 Read 已用 post-fix dump 收敛，managed worktree 搜索噪声导致的 Grep persisted output 已定位并修复。
3. 次高置信：Task/Agent 生命周期契约和结果回灌差异，尤其是 fresh context、不要编造结果、completion notification、output_file、AgentGet summary；当前 description 已补强，剩余重点是原版多轮 transcript 对照和真实 lifecycle 能力差异。
4. 中高置信：system prompt/core prompt 文案强度与 workspace guidance source 差异。它重要，但不是唯一或最强根因；当前 kind-aligned 证据显示 `auto_memory/session guidance` 是同 kind system bytes 差距大头，且 `AGENTS.md fallback` 已被双边隔离 gate 定位并通过 strict profile 变成可控变量。Go 已补强 compatible session guidance 和 memory protocol，但剩余原版多轮 Agent 证据仍更关键。
5. 已降级：TUI 展示、Web Agent 数据库可见性。它们影响体验或曾是疑点，但当前证据不支持把它们列为主要模型行为根因。
