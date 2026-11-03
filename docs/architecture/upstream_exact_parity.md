# Upstream-exact Prompt / Stream JSON / Cache 对照

本文件记录 go-claude 与本地 Claude Code 源码快照的行为对照，用于跟踪借鉴能力的兼容程度，避免笼统地说“官方原版”或“100% 对齐”。

## 对照基准

- 上游源码快照：`$HOME/GolandProjects/claude_code_src_2026`
- 上游包版本：`@anthropic-ai/claude-code` `2.1.88`
- 说明：该目录不是 git repository，但包含 `src/` TypeScript 源码和 `dist/cli.js` 发布产物。本文件以后默认把它作为 upstream-exact 对照源。

重点对照文件：

- Prompt 构建：`src/QueryEngine.ts`、`src/utils/systemPrompt.ts`、`src/constants/prompts.ts`
- Prompt cache：`src/services/api/claude.ts`、`src/utils/api.ts`、`src/utils/forkedAgent.ts`
- Stream event：`src/services/api/claude.ts`、`src/QueryEngine.ts`、`src/cli/print.ts`、`src/cli/structuredIO.ts`
- Go 实现：`internal/query/query.go`、`internal/promptcache/promptcache.go`、`internal/anthropic/client.go`

## 结论

当前 Go 版本已经覆盖 prompt、stream-json、prompt cache 的主链路和大量金标测试；近期补齐了影响功能对齐的核心机制：`stream_event` 外层兼容、prompt cache `scope/global` 与 dynamic boundary、prompt 优先级矩阵、Go 进程内文件工具的 sandbox 写边界、resume interrupted-turn 修复，以及 Bash/PowerShell 高风险 permission classifier 主规则集。

仍不能严谨标记为 upstream-exact 100%，主要原因不是主链路不可用，而是上游实现里还有少量内部或平台绑定细节：例如 `research` 内部字段、上游新实验 prompt section 的持续跟踪、以及 Windows/WSL/PowerShell 真实 OS sandbox。

## Prompt 对照

| 项目 | 上游 2.1.88 | Go 当前实现 | 状态 |
| --- | --- | --- | --- |
| 默认 prompt 来源 | `fetchSystemPromptParts` + `buildEffectiveSystemPrompt`，再进入 API 层追加 attribution / CLI prefix / advisor / chrome 等段落。 | `systemPromptBlocks` 在 Go 内构建静态段落，并通过命名 section registry 解析 output style、language、memory、MCP、scratchpad、FRC、brief/proactive 等动态段。 | 主链路已覆盖，非逐字 exact。 |
| 优先级 | override system prompt > coordinator > main-thread agent > custom system prompt > default；`appendSystemPrompt` 总是尾部追加。 | Go 已实现 override / coordinator / main-thread agent / custom / default / append 优先级矩阵，并有单元测试。 | 主链路对齐。 |
| section registry | 上游使用 `systemPromptSection` / `DANGEROUS_uncachedSystemPromptSection` / `resolveSystemPromptSections` 管理动态段落。 | Go 已引入同构命名 registry：stable section 按 session 缓存，`mcp_instructions` 等 volatile section 每轮重算，并覆盖 `session_guidance`、`memory`、`ant_model_override`、`env_info_simple`、`language`、`output_style`、`mcp_instructions`、`scratchpad`、`frc`、`summarize_tool_results`、`numeric_length_anchors`、`token_budget`、`brief`。feature gate 支持 env、ant-only `CLAUDE_INTERNAL_FC_OVERRIDES`、`featureFlags`、`growthbook.features` 和 `growthbook.url` 远端刷新缓存。 | registry 主链路对齐，继续跟踪上游新增 section。 |
| 动态边界 | 上游有 `SYSTEM_PROMPT_DYNAMIC_BOUNDARY`，用于区分静态可缓存段落和动态段落。 | Go 已引入同名 boundary 概念，按 attribution / CLI prefix / static global / dynamic uncached 切分 system blocks。 | 主链路对齐。 |
| output style | 上游支持默认、用户、项目、插件 output style，且有 force / keep coding instruction 等策略。 | Go 已支持用户/项目/插件 output style、forced plugin、keep coding instructions。 | 主链路对齐。 |
| language | 上游按配置生成 language section。 | Go 已生成 language section。 | 已覆盖主链路。 |
| agent/coordinator prompt | 上游 main-thread agent 可替换或追加 default prompt，coordinator 模式可替换 prompt，proactive/Kairos 可走 autonomous prompt path。 | Go 已支持 main-thread agent prompt 与 coordinator prompt 的运行时优先级，并补充 proactive/Kairos autonomous prompt path 的 feature gate。 | 主链路对齐。 |

Prompt 后续补齐方向：

- 继续跟踪上游新增 prompt section；生产环境可通过 `growthbook.url` 接入真实远端刷新。
- 不直接复制大段上游 prompt 文案；用结构、section 名称和行为金标对齐，避免文案漂移难维护。

## Stream JSON 对照

| 项目 | 上游 2.1.88 | Go 当前实现 | 状态 |
| --- | --- | --- | --- |
| API 原始事件 | `src/services/api/claude.ts` 对每个 API stream part yield `{ type: "stream_event", event: part }`，`message_start` 还带 `ttftMs`。 | Go 默认保留当前友好事件；`--include-stream-events` 输出 upstream-compatible `{type:"stream_event",event:{...},session_id,parent_tool_use_id:null,ttftMs}` 外层。 | 主链路对齐。 |
| message_start | 上游保存 partialMessage、usage、ttftMs，并可捕获内部 `research` 字段。 | Go 输出 `message_start`，并可额外输出带 `ttftMs` 的 `stream_event.message_start`；`research` 是 ant 内部字段，当前仍 omit。 | 主链路对齐。 |
| content_block_delta | 上游按 `input_json_delta`、`text_delta`、`thinking_delta`、`signature_delta`、`citations_delta`、connector text 等更新 block。 | Go 覆盖 text/thinking/input_json_delta/signature_delta/citations_delta/connector_text_delta，tool input JSON 已按固定 chunk 分片，并在 stream_event 外层同步输出。 | 公开 delta 主链路对齐。 |
| content_block_stop | 上游在 stop 时 yield assistant message，并在后续 `message_delta` 通过引用回写 usage/stop_reason。 | Go 在 message stop 阶段统一输出 message_delta/message_stop。 | 行为近似，时序非 exact。 |
| includePartialMessages | 上游 `includePartialMessages` 会输出原始 `stream_event` SDK 消息。 | Go 保留 `--include-partial-messages` 的 accumulated partial message，并新增 `--include-stream-events` 承载原始事件兼容语义。 | 兼容覆盖。 |
| hook events | 上游 hook / permission / updatedInput 分布在 toolExecution、toolHooks、StructuredIO 控制协议中。 | Go 已输出 `hook_start` / `hook_result`，带 `permissionDecision` / `updatedInput`；已覆盖 `Notification`、`Stop`、`SubagentStop`、`UserPromptSubmit` 等长尾事件的主链路。 | 主链路覆盖，私有控制协议未 exact。 |
| result/error | 上游 SDK 最终 result 包含 duration、cost、usage、modelUsage、permission_denials、fast_mode_state、errors 等。 | Go 现在输出 upstream-style `result` envelope，含 success/error subtype、duration、turns、result/errors、stop_reason、usage、modelUsage、permission_denials、session_id，再保留兼容 `done`。 | 主字段对齐，成本/fast_mode_state 暂为零值或 omit。 |

Stream JSON 后续补齐方向：

- 继续补 ant 内部 `research` 字段和新增实验 stream 字段；result envelope 已有字段矩阵和金标，缺字段采用零值或 omit 策略。

## Prompt Cache 对照

| 项目 | 上游 2.1.88 | Go 当前实现 | 状态 |
| --- | --- | --- | --- |
| 开关 | `getPromptCachingEnabled` 受 env、模型、用户类型、provider 等影响。 | Go 支持全局 disable、Haiku/Sonnet/Opus env disable，并补齐 user-type / subscriber / overage / Bedrock opt-in / querySource allowlist 的 1h TTL 决策。 | 主链路对齐。 |
| cache control | `getCacheControl` 输出 `type: ephemeral`，可按 allowlist 带 `ttl: 1h`，可带 `scope: global`。 | Go 支持 `type: ephemeral`、条件化 `ttl: 1h`、`scope: global`，并映射到 Anthropic SDK extra fields。 | 已对齐。 |
| block 划分 | `splitSysPromptPrefix` 按 attribution、CLI prefix、static/dynamic boundary、MCP/tool cache marker 输出最多 3-4 个 block。 | Go 已实现 splitSystemPromptPrefix，覆盖 attribution / CLI prefix / static global / dynamic uncached / org fallback。 | 主链路对齐。 |
| message cache | 上游可给 user/assistant message 最后一个 content block 添加 cache_control。 | Go 已在请求侧为最后一个 eligible message content block 添加单一 cache breakpoint，避免污染 transcript。 | 主链路对齐。 |
| forked agent | 上游 `CacheSafeParams` 要求 system prompt、tools、model、message prefix、thinking config 等一致，保证 fork 共享 prompt cache。 | Go fork/sub-agent 已隔离运行，但 cache-safe 参数一致性策略未完整建模。 | 未 exact。 |
| usage | 上游 usage 包含 `cache_creation_input_tokens`、`cache_read_input_tokens`、`cache_creation.ephemeral_1h_input_tokens`、`cache_creation.ephemeral_5m_input_tokens`、service tier、geo、speed 等。 | Go 已解析、merge、stream、记录和汇总这些字段。 | 主链路对齐。 |
| telemetry | 上游记录 prompt cache 命中、fork cache hit rate、GrowthBook allowlist 等。 | Go 有 prompt cache hash 稳定/破坏日志。 | 观测粒度不同。 |

Prompt cache 后续补齐方向：

- GrowthBook 远端动态配置源已接入；本地等价 env allowlist 已覆盖 querySource 与用户 eligibility 行为。
- 继续把 forked agent `CacheSafeParams` 细化到 thinking config、tool schema cache marker、skipCacheWrite 等更长尾场景。

## 当前优先级

P0：

- Windows/WSL/PowerShell 真实 OS sandbox。
- Linux sandbox 真机 CI/e2e golden。
- ant 内部 `research` 字段、新增实验 stream 字段和 result/error envelope 长尾字段。

P1：

- GrowthBook 新增 prompt section 和远端 feature 字段继续跟踪。

P2：

- 上游 feature-gated prompt section、analytics、internal-only 字段、GrowthBook 远端动态实验完全复刻。
