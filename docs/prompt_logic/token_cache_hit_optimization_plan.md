# Go Claude Token Cache Hit Optimization Plan

更新时间：2026-07-12

## 目标

本阶段目标不是机械复刻原版 Claude Code，而是提高 go-claude 在真实 agent 任务里的 token 缓存命中率、可观测性和可解释性。

成功标准：

- 能明确区分“provider 不支持/不上报缓存 usage”和“go-claude 请求结构破坏缓存”。
- Prompt Dump Viewer / API 能指出每轮请求里哪些内容可缓存、哪些内容未缓存、哪些字段导致 cache-safe signature 变化。
- 在支持缓存 usage 的 provider 上，同一 session 多轮请求能观察到 `cache_read_input_tokens` 或 OpenAI-compatible `prompt_tokens_details.cached_tokens` 增长。
- 在不支持或不上报缓存 usage 的 provider 上，也能通过 prompt dump/golden 证明请求 prefix 稳定，且 UI 明确显示“provider usage unavailable”，不误报命中率。

## 当前证据

### 1. 当前真实 prompt dump：system hash 稳定，但 skills catalog 未进入 cache_control

样本：`/tmp/go-claude-tui-prompt.jsonl`

session：`43b738c7-3945-4134-a9ba-38167fa1bca7`

观测：

```text
turn=1 system_hash=ce9ba4d87abc7a6a3e4948c881d8a5fce9de233dcd183a0d11d8a250b46e9230 system_bytes=19416 messages=2 tools=32 blocks=4
  #0 source=identity kind=other bytes=66 cache=false
  #1 source=static_prompt kind=core_prompt bytes=9126 cache=true type=ephemeral scope=global
  #2 source=dynamic_prompt kind=other bytes=2090 cache=false
  #3 source=skills_catalog kind=skills_catalog bytes=8128 cache=false
...
turn=4 system_hash=ce9ba4d87abc7a6a3e4948c881d8a5fce9de233dcd183a0d11d8a250b46e9230 system_bytes=19416 messages=11 tools=32 blocks=4
  #0 source=identity kind=other bytes=66 cache=false
  #1 source=static_prompt kind=core_prompt bytes=9126 cache=true type=ephemeral scope=global
  #2 source=dynamic_prompt kind=other bytes=2090 cache=false
  #3 source=skills_catalog kind=skills_catalog bytes=8128 cache=false
```

结论：

- `system_hash` 多轮稳定，当前低/未知命中率不能归因于 system prompt 每轮随机变化。
- `skills_catalog` 约 8KB，稳定但未加 `cache_control`，是最明显的 ROI 候选。
- `dynamic_prompt` 约 2KB 未缓存是合理默认，因为它可能包含 git/status/memory/runtime 等变化内容。

### 2. 当前 trace usage 未上报缓存 token

Prompt Dump API 对该 session 的摘要：

```text
No prompt-cache read or write tokens were reported.
```

代码证据：`internal/server/trace.go:1595-1603` 在 `cache_read_tokens=0` 且 `cache_creation_tokens=0` 时显示该文案。

结论：

- 当前这条 session 不能证明“缓存命中率低”，只能证明“provider usage 没有上报 prompt-cache read/write”。
- 后续优化必须先把 provider usage 能力边界做成显式诊断，否则无法判断改动是否有效。

### 3. Go 当前 cache 组装路径

主线程请求构造：

- `internal/query/query.go:1227-1260`：先取 `effectiveSystemBlocks()`，再追加 tenant skill、filesystem skills catalog、auto memory。
- `internal/query/query.go:1323-1338`：请求前执行 `toolresult.ApplyMessageBudget` 和 `toolresult.ApplyHistoryBudget`，并传入 `Replacements`、`SeenToolUseIDs`，保持大 tool_result 替换稳定。
- `internal/query/query.go:1339-1345`：追加 runtime status / active skill context 后，调用 `addMessageCacheBreakpoint(...)`。
- `internal/query/query.go:1353-1359`：把 `SystemBlocks`、`Messages`、`Tools` 和 `Thinking` 发给模型 client。
- `internal/query/query.go:6710-6724`：`buildSystemPromptBlocks` 只给 `splitSystemBlock.cacheScope != nil` 的 block 加 `cache_control`。
- `internal/query/query.go:6727-6773`：dynamic boundary 存在时，只有 boundary 前的 `static_prompt` 设置 `cacheScope=global`，boundary 后的 `dynamic_prompt` 不缓存。

缓存稳定性观测：

- `internal/promptcache/promptcache.go:61-77`：system block text hash 和 cache_control hash 变化检测。
- `internal/promptcache/promptcache.go:79-103`：request-level cache-safe signature 检测。
- `internal/promptcache/promptcache.go:105-127`：signature 包含 model、system、cache_control、tools、message prefix、thinking。
- `internal/query/query.go:3818-3860`：记录 `prompt.cache.break` 和 `prompt.cache.request_break`。

大 tool_result 稳定性：

- `internal/toolresult/toolresult.go:78-152`：message-level aggregate budget，已支持 known replacement 重放和 seen IDs 冻结。
- `internal/toolresult/toolresult.go:154-224`：history budget，已支持 replacement 重放和 seen IDs 冻结。
- `internal/query/query_test.go:8196-8231`：证明 live seen old result 不会后续重新持久化。
- `internal/query/query_test.go:8365-8401`：证明 transcript replacement resume 后会重放。
- `internal/query/query_test.go:8403-8444`：证明没有 replacement record 的 resume 旧结果不会被重新替换。

结论：

- “Go 缺大工具结果冻结”不是当前主因，已经有实现和测试。
- 更大的缺口在 provider 兼容诊断、未缓存稳定大块、cache_reference/cache_edits、fork/subagent cache-safe 共享。

### 4. OpenAI-compatible provider 的边界

Go 的 OpenAI-compatible 路径：

- `internal/anthropic/client.go:344-354`：OpenAI-compatible provider 会走 `streamOpenAIChatCompletion`。
- `internal/anthropic/client.go:388-394`：只从 `event.Usage.PromptTokensDetails.CachedTokens` 读取缓存命中 token。
- `internal/anthropic/client.go:1011-1032`：`openAIChatCompletionRequest` 把 `effectiveSystemText(req)` 放入普通 system message。
- `internal/anthropic/client.go:1303-1314`：`effectiveSystemText` 会把 `SystemBlocks` 文本 join 成普通字符串。
- `internal/anthropic/client.go:1246-1328`：只有 Anthropic SDK 路径会把 `SystemBlocks` 转为带 `CacheControl` 的 SDK system blocks。
- `internal/anthropic/client_test.go:127-170`：测试确认 OpenAI 请求只看到合并文本，Anthropic SDK 请求保留 `CacheControl`。

结论：

- 对 Sensenova/deepseek 这类 OpenAI-compatible provider，仅给 Anthropic `cache_control` 加字段不一定带来真实 provider cache。
- 如果 provider 不返回 `prompt_tokens_details.cached_tokens`，Go 无法从 usage 层证明命中率。
- 后续应先做 provider cache capability detector 和 normalized metrics，而不是先大改 prompt。

### 5. 原版 Claude Code 的关键策略

原版源码目录：`$HOME/GolandProjects/claude_code_src_2026`

已确认策略：

- `src/services/api/claude.ts:358-373`：`getCacheControl` 统一输出 `type=ephemeral`、可选 `ttl=1h`、可选 `scope=global`。
- `src/services/api/claude.ts:393-405`：`should1hCacheTTL` 会 latch eligibility，避免中途 overage / allowlist 翻转造成 cache_control TTL 变化。
- `src/services/api/claude.ts:3063-3211`：`addCacheBreakpoints` 保证每次请求只有一个 message-level cache marker，并支持 `cache_edits` / `cache_reference`。
- `src/services/api/claude.ts:3213-3236`：`buildSystemPromptBlocks` 只给有 cacheScope 的 system block 加 `cache_control`，并警告不要随意增加缓存 block。
- `src/utils/toolResultStorage.ts:367-411`、`:739-790`：大 tool_result replacement state 按 tool_use_id 冻结，目标是 byte-identical prefix。
- `src/utils/forkedAgent.ts:47-68`：forked agent 明确要求 system prompt、tools、model、message prefix、thinking config 等 cache-critical params 与 parent 一致。
- `src/utils/forkedAgent.ts:646-654`：原版 fork cache hit rate 公式是 `cache_read / (input + cache_creation + cache_read)`。
- `src/services/api/promptCacheBreakDetection.ts:28-68`：原版追踪 system/tools/cache_control/model/betas/effort/extra body 等 cache break 原因。
- `src/services/api/promptCacheBreakDetection.ts:668-690`：cache deletion / compaction 会显式标记为“预期下降”，避免误判。

结论：

- 原版强在“稳定 prefix + 能解释 cache break”，不是只靠一段 prompt 文案。
- Go 已覆盖一部分主链路，但缺少 cache_reference/cache_edits 和更细的 break diagnosis。

## 根因候选与优先级

### P0：缓存命中率不可判定，因为 provider usage 边界不清

证据：

- 当前 session trace 显示 `No prompt-cache read or write tokens were reported.`。
- OpenAI-compatible 路径只从 `prompt_tokens_details.cached_tokens` 读取命中 token。
- Anthropic `cache_control` 在 OpenAI-compatible 请求中会被合并成普通 system message，不会原样发出。

影响：

- 如果 provider 不支持或不上报缓存，任何 prompt 改动都无法用 token usage 证明收益。
- UI 当前只显示 cache read/write/hit，不足以解释“未上报”和“未命中”的区别。

置信度：高。

### P0：稳定但未缓存的大块存在，skills_catalog 是当前最高 ROI 候选

证据：

- 当前 dump 中 `skills_catalog` 8128 bytes，多轮稳定，`cache=false`。
- `internal/query/query.go:1246-1255` 追加 filesystem skills catalog。
- `internal/query/query.go:6710-6724` 只有带 `cacheScope` 的 split block 才会加 `cache_control`；追加的 `skills_catalog` 没有 cache scope。

影响：

- 在支持 Anthropic-style prompt cache 的 provider 上，每轮 8KB 级稳定内容不进缓存，会拉低可缓存比例。
- 在 OpenAI-compatible provider 上，需要先确认 provider 是否支持 system prefix 缓存；若支持，稳定 join 后的 system message仍可能由 provider 自行缓存，但 Go 无法通过 `cache_control` 控制。

置信度：中高。缺少同 provider usage 对照，不能直接量化提升。

### P1：message prefix 变化诊断不够可视化

证据：

- Go 已有 `RequestTracker`，但 Prompt Dump Viewer 尚未展示每轮 `model/system/cache/tools/message_prefix/thinking` 哪个变了。
- 原版 `promptCacheBreakDetection` 会追踪更细粒度 break 原因。

影响：

- 当用户看到 cache hit 低时，无法快速判断是 tools schema、thinking config、message prefix、dynamic context 还是 provider usage。

置信度：高。

### P1：缺少 cache_reference/cache_edits 协议能力

证据：

- 原版 `addCacheBreakpoints` 支持 `cache_reference` 和 `cache_edits`。
- Go 当前 `anthropic.ContentBlock` 只有 `CacheControl`，没有 `cache_reference` 或 `cache_edits` 字段。

影响：

- 对支持 Anthropic cached message editing 的 provider，Go 不能在保留 cached prefix 的同时删除/引用旧 tool_result。
- 对 OpenAI-compatible provider，多数情况下无直接收益，但也需要 capability gating。

置信度：中。需要 provider capability 验证，不适合第一步大改。

### P2：fork/subagent cache-safe 参数还未完整产品化

证据：

- 现有 docs 已标注 fork/subagent cache-safe params 未 exact。
- Go 有 request-level cache-safe signature 和 subagent `cache_state` event，但缺少像原版 `CacheSafeParams` 那样围绕 fork 共享 parent prefix 的完整策略。

影响：

- 并行 agent / forked summarizer 场景可能损失缓存复用。

置信度：中。

## 技术方案

### 阶段 1：先补观测和验收闭环

目标：让“为什么命中率低/未知”可解释。

改动：

1. Prompt Dump API 增加 cache diagnostics：
   - session 级：
     - `provider_cache_usage_state`: `reported` / `not_reported` / `unavailable`
     - `provider_cache_hit_ratio_anthropic`: `cache_read / input`
     - `provider_cache_hit_ratio_total_input`: `cache_read / (input + cache_creation + cache_read)`
     - `system_hash_stable`
     - `cache_control_hash_stable`
     - `tools_hash_stable`
     - `message_prefix_hash_stable`
     - `thinking_hash_stable`
   - record 级：
     - `cacheable_system_bytes`
     - `uncached_system_bytes`
     - `largest_uncached_system_blocks`
     - `cache_control_block_count`
     - `message_cache_marker_count`
     - `largest_message_blocks`
     - `request_signature_delta_from_previous`

2. Prompt Dump Viewer 增加 Cache Diagnostics tab：
   - 顶部显示 provider usage 状态，而不是只显示 0% 或空。
   - 列出最大未缓存块：例如 `skills_catalog 8128 bytes`。
   - 列出本轮与上一轮的 cache-safe diff：model/system/cache_control/tools/message_prefix/thinking。
   - 显示两种命中率公式：
     - Go trace 当前公式：`cache_read / input_tokens`
     - 原版 fork-compatible 公式：`cache_read / (input + cache_creation + cache_read)`

3. 增加 acceptance script：
   - 输入 prompt dump jsonl 和可选 trace session。
   - 输出：
     - session cache usage state
     - system hash stability
     - cacheable/uncached bytes
     - top uncached blocks
     - cache-safe signature changes
   - 放在 `scripts/prompt-cache-diagnostics` 或 `scripts/promptdump-compare` 下，优先复用现有 promptdump parser。

测试：

- `go test ./internal/server -run 'TestPromptDumpViewerAndAPI|TestTrace' -count=1`
- `go test ./internal/promptcache ./internal/promptdump -count=1`
- 新增 golden：构造两轮 prompt dump，第一轮 `skills_catalog` 未缓存，诊断必须指出 top uncached block。
- 如果改 Swagger 类型：`swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency`。

验收：

- 用当前 `/tmp/go-claude-tui-prompt.jsonl` 打开 viewer，能明确显示：
  - provider cache usage 未上报。
  - system hash 稳定。
  - `skills_catalog` 是最大未缓存稳定 system block。

### 阶段 2：缓存稳定 skills catalog，但必须 feature-gated

目标：减少稳定 skills catalog 对多轮 system prompt 的重复成本。

推荐实现：

1. 增加可配置策略：
   - env/config：`GOLANG_CLAUDE_CODE_CACHE_SKILLS_CATALOG=auto|on|off`
   - 默认建议：`auto`
   - `auto` 行为：
     - Anthropic provider：允许给稳定 skills catalog 加 cache_control。
     - OpenAI-compatible provider：不注入 Anthropic cache_control；只做诊断，因为请求会被 join 成普通 system message。

2. 为 `appendSystemBlockWithSource(..., "skills_catalog")` 增加 cache scope 能力，而不是在 promptdump 层伪造：
   - 方案 A：新增 `appendCachedSystemBlockWithSource(blocks, text, source, scope)`。
   - 方案 B：扩展 `anthropic.SystemBlock.Source` 之外的内部 metadata 不合适，因为 `Source` 不序列化，cache scope 需要进入 request。
   - 优先方案 A，改动小、可测。

3. cache block 数量限制：
   - 原版 `buildSystemPromptBlocks` 注释提醒不要随意增加 system cache blocks。
   - Go 当前真实请求已有：
     - static system block cache_control
     - message-level cache_control
   - 增加 skills catalog 后常见为 3 个 cache markers，仍需测试不超过 provider 限制。
   - 若 provider 或 SDK 限制 4 个 cache_control blocks，必须 guard：
     - static prompt
     - skills catalog
     - message breakpoint
     - 可选未来 cache_edits

4. 稳定性 guard：
   - 仅当 skills catalog 文本 hash 在同 session 连续稳定，或 catalog source 明确 deterministic 时启用。
   - 如果每轮根据 prompt 动态过滤 skills，则 catalog 可能随用户输入变化；这种情况下 caching 仍可创建当前 prefix，但命中率不一定提升。
   - 诊断需要显示 `skills_catalog_hash` 和 `skills_catalog_changed`。

测试：

- `TestSkillsCatalogCanBeCachedWhenEnabled`
- `TestSkillsCatalogCacheOffByDefaultForOpenAICompatibleProvider`
- `TestPromptDumpReportsCachedSkillsCatalogBytes`
- `TestPromptCacheHashChangesWhenSkillsCatalogCacheControlChanges`

验收：

- 在 Anthropic-compatible provider 或 mock usage 下，第二轮开始 `cache_creation/cache_read` 不为 0。
- Prompt dump 里 `skills_catalog` 显示 `has_cache_control=true`。
- OpenAI-compatible provider 上不误报：viewer 显示 provider usage 是否上报。

### 阶段 3：增强 request cache break diagnosis

目标：用户能直接看到是哪类请求参数破坏了缓存。

实现：

- 将 `promptcache.RequestReport` 序列化进 prompt dump summary 或 trace telemetry：
  - `model_changed`
  - `system_changed`
  - `cache_changed`
  - `tools_changed`
  - `message_changed`
  - `thinking_changed`
- 记录前后 hash 的短 hash，但不记录完整 prompt 正文。
- Viewer 在 Requests 列表显示 cache break badges，例如 `tools changed`、`message prefix changed`。

测试：

- 构造两轮请求，仅 tools schema 变化，诊断必须命中 `tools_changed=true`。
- 构造两轮请求，仅 message prefix 增长，诊断必须命中 `message_changed=true`。

验收：

- 真实多轮 session 中，每个 request 能解释 cache-safe signature 是否变。

### 阶段 4：评估 cache_reference/cache_edits

目标：对支持 Anthropic cached message editing 的 provider，减少长历史 tool_result 对缓存的破坏。

前置条件：

- 阶段 1 的 diagnostics 已完成。
- 阶段 2 能证明 system/cache_control 层已稳定。
- 已确认 provider 支持对应 beta/header；不支持时必须关闭。

设计：

- 扩展 `anthropic.ContentBlock`：
  - `CacheReference string json:"cache_reference,omitempty"`
  - 支持 `cache_edits` block 类型。
- 在 `addMessageCacheBreakpoint` 后，给 last cache_control marker 之前的 `tool_result` 增加 `cache_reference=tool_use_id`。
- 对被 microcompact / toolresult budget 删除的 cached blocks，插入 pinned `cache_edits`。
- 必须有 provider capability gate，默认 off。

风险：

- API 兼容风险高。
- OpenAI-compatible provider 大概率不支持。
- block 顺序错误会直接 400。

测试：

- 单元测试 cache_reference 只出现在 last cache_control 之前。
- cache_edits 去重和 pinned replay 测试。
- Anthropic mock body golden。

### 阶段 5：fork/subagent cache-safe 优化

目标：并行 agent / forked summarizer 共享 parent prefix。

设计：

- 为 parent -> subagent/fork request 生成 cache-safe contract：
  - model
  - tools signature
  - system hash
  - message prefix hash
  - thinking hash
  - content replacement state hash
- subagent runtime event 输出 `cache_state`：
  - `shared_prefix_candidate`
  - `break_reason`
  - `cache_read/cache_creation` if provider reports
- 对 fork 场景优先复用 parent `contentReplacementState` 的 clone，Go 已有 toolresult replacement map，可以继续沿用。

验收：

- 同一 parent session 创建多个相似 subagent/fork 任务，diagnostics 显示 shared prefix 稳定。

## 推荐执行顺序

1. P0-A：Prompt Dump cache diagnostics + viewer tab + acceptance script。
2. P0-B：provider usage capability detector，明确 Anthropic vs OpenAI-compatible 的可观测边界。
3. P0-C：feature-gated skills_catalog cache_control，先只对 Anthropic-compatible provider 开启。
4. P1：request cache break diagnosis 可视化。
5. P1/P2：cache_reference/cache_edits，仅在 provider capability 确认可用后做。
6. P2：fork/subagent cache-safe sharing。

不建议直接先做：

- 不建议先把所有 system blocks 都加 cache_control。原版明确限制 cache blocks 数量，dynamic block 本来就可能变化。
- 不建议在 OpenAI-compatible provider 上假装 Anthropic cache_control 生效。当前代码会把 system blocks join 成普通 system message。
- 不建议只看总 cache hit ratio。必须同时看 provider usage 是否上报、system hash、cache_control hash、tools hash、message prefix hash。
- 不建议把 `49%` 当成稳定基线；当前这条 session 的 trace 已显示 provider 未上报缓存 token。

## 验收命令

基础检查：

```bash
go test ./internal/server -run 'TestPromptDumpViewerAndAPI|TestTrace' -count=1
go test ./internal/promptcache ./internal/promptdump ./internal/query -run 'Cache|PromptDump|ToolResult' -count=1
git diff --check
```

全量检查：

```bash
go test ./... -count=1
git diff --check
```

真实 viewer 验收：

```bash
GOLANG_CLAUDE_CODE_DUMP_PROMPT_JSON=/tmp/go-claude-tui-prompt.jsonl \
go run ./cmd/golang-cc server --host 127.0.0.1 --port 8080 --auth-token test-token

open 'http://127.0.0.1:8080/prompt-dump?token=test-token'
```

真实 TUI 采样：

```bash
GOLANG_CLAUDE_CODE_DUMP_PROMPT_JSON=/tmp/go-claude-tui-prompt.jsonl \
GOLANG_CLAUDE_CODE_DUMP_PROMPT_FULL=true \
go run ./cmd/golang-cc/* --cwd $HOME/GolandProjects/superPM --model sensenova-6.7-flash-lite
```

注意：不要把 API key 写入文档、日志或 prompt dump。

## 下一会话实施提示词

```text
你在 /path/to/golang-cc 工作。先阅读 AGENTS.md，然后阅读 docs/prompt_logic/token_cache_hit_optimization_plan.md。

目标：按文档第一阶段开始实现 go-claude token 缓存命中率优化的可观测闭环。不要直接大改 prompt 文案，也不要先实现 cache_reference/cache_edits。

优先级：
1. 实现 Prompt Dump cache diagnostics：
   - provider_cache_usage_state
   - 两种 cache hit ratio
   - system/cache_control/tools/message_prefix/thinking 稳定性
   - cacheable_system_bytes / uncached_system_bytes
   - largest_uncached_system_blocks
   - request_signature_delta_from_previous
2. 在 Prompt Dump Viewer 增加 Cache Diagnostics tab，能直接指出 provider 是否上报 usage、最大未缓存块、cache-safe signature 哪项变化。
3. 增加 acceptance script 或 focused tests，使用构造 prompt dump 验证 diagnostics。
4. 用当前 /tmp/go-claude-tui-prompt.jsonl 验收：必须能显示当前 session provider 未上报 cache usage，system hash 稳定，skills_catalog 8128 bytes 是最大未缓存稳定 system block。

证据文件：
- internal/query/query.go
- internal/promptcache/promptcache.go
- internal/promptdump/promptdump.go
- internal/server/prompt_dump.go
- internal/server/prompt_dump_viewer.html
- internal/anthropic/client.go
- internal/toolresult/toolresult.go
- $HOME/GolandProjects/claude_code_src_2026/src/services/api/claude.ts
- $HOME/GolandProjects/claude_code_src_2026/src/services/api/promptCacheBreakDetection.ts
- $HOME/GolandProjects/claude_code_src_2026/src/utils/forkedAgent.ts

验证：
- go test ./internal/server -run 'TestPromptDumpViewerAndAPI|TestTrace' -count=1
- go test ./internal/promptcache ./internal/promptdump ./internal/query -run 'Cache|PromptDump|ToolResult' -count=1
- go test ./... -count=1
- git diff --check
- 如果改 Swagger 类型，运行 swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency

完成后按仓库规则提交并 push。最终回复要明确：实现了哪些 diagnostics、当前真实 session 的缓存边界是什么、下一步是否应该做 feature-gated skills_catalog cache_control。
```
