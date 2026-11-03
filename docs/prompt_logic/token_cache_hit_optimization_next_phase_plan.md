# Go Claude Token Cache Hit Optimization Next Phase Plan

更新时间：2026-07-13

## 结论先行

下一阶段不建议直接默认把 `skills_catalog` 前移，也不建议给所有 provider 加 `cache_control`。

当前决策：

```text
保持现状。
GO_CLAUDE_STABLE_PREFIX_SKILLS 继续默认关闭。
不默认重排 system prompt。
不对 GLM/OpenAI-compatible 注入 Anthropic cache_control。
不把本地 hash 稳定误当成 provider cache hit。
```

原因：

- 当前可观测链路已经能看到 provider usage、cache hit 口径、stable signature 和 `skills_catalog` 位置。
- A-B-A-B smoke 只能证明 feature gate 会把 `skills_catalog` 从 position 3 前移到 position 2。
- 当前样本不能证明 GLM 5.1 真实 provider cache hit 提升；离线 aggregate 结论是 `candidate_not_supported`。
- prompt block 顺序会影响模型行为语义，不能仅凭“内容稳定”就默认前移。

更稳妥的路线是：

1. 先把缓存指标和 TUI 历史显示做可信，确保看到的 hit rate 代表真实 provider usage。
2. 对 GLM/OpenAI-compatible provider，优先围绕“稳定 prefix 布局”和 provider 返回的 `cached_tokens` 做验证。
3. 对 skills，不机械照搬当前 Go 的 full `skills_catalog` system block，也不机械复刻原版；应先做 feature-gated 实验，再逐步演进到“去重、增量、按需发现”的结构。
4. `cache_control` 只作为 Anthropic-style provider 的 provider-aware 能力，不作为 GLM 5.1 的第一优先级。

## 已知事实

### Go 当前布局

当前 go-claude 的 system block 顺序来自 `internal/query/query.go` 的拼装流程：

```text
identity
static_prompt
dynamic_prompt
skills_catalog
```

原因不是一个明确的缓存优化设计，而是：

- `effectiveSystemBlocks()` 先构造基础 system blocks。
- filesystem skills catalog 在后续代码里追加。
- 追加的 `skills_catalog` 没有 `cache_control`，也没有被纳入 static/dynamic boundary 的决策。

真实 dump 里，`skills_catalog` 是稳定的：

```text
source=skills_catalog
bytes=8128
stable=true
seen_count=5
```

`dynamic_prompt` 在该样本里也稳定，但它的语义不是“永远静态”。它可能承载 session guidance、memory、environment、runtime、git/status、permission mode 等会随场景变化的内容。

### GLM/OpenAI-compatible usage 口径

GLM/OpenAI-compatible provider 的缓存命中数据应以 provider 返回的 `usage.prompt_tokens_details.cached_tokens` 为准。

已完成修正：

- OpenAI-compatible path 保留 `prompt_tokens` provider 口径。
- `cached_tokens` 不再被二次加进 `input_tokens`。
- Anthropic 分列 usage 仍保持 `input + cache_creation + cache_read` 总量口径。

因此后续看 GLM 5.1 时，应优先看：

```text
cached_tokens / prompt_tokens
```

而不是把 `prompt_tokens + cached_tokens` 当作分母。

### 原版 Claude Code 的处理方式

原版不是简单把 skills 放到 system prompt 前缀里。

原版 system prompt 有明确 dynamic boundary：

```text
Everything BEFORE boundary can use scope: global.
Everything AFTER contains user/session-specific content and should not be cached.
```

boundary 后的内容包括：

```text
session_guidance
memory
env_info_simple
language
output_style
mcp_instructions
scratchpad
frc
summarize_tool_results
...
```

原版 skills 主要走 attachment / discovery / Skill tool 机制：

- `skill_listing` 作为 user-side system reminder 注入，不是 system prompt block。
- 通过 `sentSkillNames` 避免每轮重复发同一批 skills。
- resume 时如果 transcript 已有 skill listing，会 suppress 下一次重复 listing。
- compact 后刻意不重发 full skill listing，因为重新注入约 4K tokens/event，收益低。
- 开启 skill-search 时，只把 bundled + MCP 小集合做 listing；user/project/plugin 长尾 skills 走 discovery。

原版给 go-claude 的启发不是“把 skills_catalog 前移即可”，而是：

```text
稳定 system prefix 要小且稳定；
动态和用户/session 相关内容放后面；
skills 长尾不要每轮全量塞进 prompt；
能按需发现/增量注入的，就不要变成长期重复大块。
```

## 问题拆解

### 问题 1：当前 hit rate 是否可信

缓存优化必须先保证可观测可信。

当前仍有一个 TUI 风险：同一条已经完成的历史 `Responded ... hit=...` 如果在后续 turn 被改写，说明 UI meta 归属有 bug。这个问题不影响 provider 请求本身，但会破坏用户对缓存指标的信任。

应单独修：

```text
applyAssistantMeta 只能更新当前 turn 的未打印 assistant；
不得回写 i < transcriptPrintedCount 的历史 assistant meta。
```

### 问题 2：`skills_catalog` 是否应该前移

从 GLM 隐式 prefix cache 看，前移可能有收益：

```text
当前:
identity -> static_prompt -> dynamic_prompt -> skills_catalog -> messages

实验:
identity -> static_prompt -> skills_catalog -> dynamic_prompt -> messages
```

收益预期：

- `skills_catalog` 约 8KB，当前样本稳定。
- 如果 `dynamic_prompt` 后续变化，当前布局会让后面的 `skills_catalog` 失去 prefix-cache 复用机会。
- 前移后，`skills_catalog` 更接近稳定 prefix，GLM 可能更容易命中。

风险：

- system prompt 顺序是行为语义的一部分，不只是缓存结构。
- `dynamic_prompt` 里可能包含 memory/env/session 约束；把 skills 提前可能改变模型关注顺序。
- 原版没有把 full skills catalog 放到 system stable prefix，因此不能用“原版就是这样”作为依据。

结论：

```text
可以做实验，不应直接默认启用。
```

### 问题 3：是否应该做原版式 skill_listing / discovery

从长期架构看，有必要。

当前 Go 的 `skills_catalog` 是一个每次请求都存在的 system 大块。即使 provider 能缓存，它仍然：

- 占用上下文窗口。
- 依赖 prefix 稳定。
- 让 system prompt 更大。
- 在 skills 很多时可能继续膨胀。

原版式方向更符合长期 ROI：

```text
小集合：turn-0 listing
大集合：按需 discovery
已发送：去重
resume：不重复 listing
compact：不重新注入 full listing
实际调用：Skill tool 再加载完整 SKILL.md
```

但这是产品和 prompt 架构改造，不应和短期 cache hit 修正混在一个 PR 里。

## 推荐路线

### Phase 2A：缓存指标可信化

目标：用户看到的缓存命中率必须稳定、可解释、不可被 UI 历史回写污染。

改动：

1. 修 TUI streaming meta 归属：
   - `Responded ...` 只绑定当前 turn assistant。
   - 禁止后续 turn 改写已打印历史 message 的 meta。
   - 增加回归测试：已完成第一轮的 `hit=xx`，第二轮无 assistant text 或 only usage 结束时，不得改写第一轮 meta。

2. TUI 明确区分：
   - 当前 turn hit。
   - session cumulative hit。
   - last input context usage。

建议 UI 文案：

```text
Responded ... cache read=14080 hit=37.4%   # current turn
Usage ... session cache read=455168 hit=72.0% ctx(last)=37637/200000
```

验收：

```bash
go test ./internal/tui -run 'Usage|Responded|Meta|Stream' -count=1
go test ./internal/server -run 'TestPromptDumpViewerAndAPI|TestTrace' -count=1
go test ./... -count=1
git diff --check
```

### Phase 2B：GLM/OpenAI-compatible 真实命中率基线

目标：先证明当前布局下真实 GLM cache 行为。

做法：

1. 用同一真实 agent 任务跑 3 组 session：
   - 短任务：无工具或少工具。
   - 中任务：多轮读取文件。
   - 长任务：含 tool result、resume、compact。

2. 每组记录：
   - provider `prompt_tokens`
   - provider `cached_tokens`
   - `cached_tokens / prompt_tokens`
   - `system_hash`
   - `tools_hash`
   - `message_prefix_hash`
   - largest uncached stable block

3. 输出 markdown/csv 报告，作为后续布局实验的 baseline。

验收标准：

```text
能说明 cache hit 低是 provider 未命中、prefix 变化、message prefix 变化，还是 UI 统计口径问题。
```

### Phase 2C：feature-gated skills_catalog 前移实验

目标：验证 `skills_catalog` 放到 `dynamic_prompt` 前是否真的提升 GLM/OpenAI-compatible 隐式缓存命中。

开关建议：

```text
GO_CLAUDE_STABLE_PREFIX_SKILLS=1
```

打开后只改变顺序：

```text
identity
static_prompt
skills_catalog
dynamic_prompt
```

不做：

- 不改 skills 文案。
- 不加 Anthropic `cache_control`。
- 不实现 cache_reference/cache_edits。
- 不改变提示词优先级规则中的 override/custom/main-thread-agent 语义。

需要增加 diagnostics：

```text
skills_catalog_position
skills_catalog_hash
skills_catalog_bytes
prefix_before_skills_hash
prefix_through_skills_hash
```

A/B 验收：

```text
A: 当前布局
B: skills_catalog 前移

比较:
- cached_tokens / prompt_tokens
- cache_read total
- 首轮/第二轮/工具轮 hit
- system/tools/message_prefix hash 是否稳定
- 真实任务是否有行为退化
```

建议先把每组真实 session 生成 summary，再用 A/B report 比较：

```bash
go run ./scripts/prompt-cache-diagnostics \
  --dump /tmp/go-claude-tui-prompt.jsonl \
  --session <baseline-session-id> \
  --summary \
  --transcript <baseline-transcript.jsonl> \
  > /tmp/go-claude-cache-baseline-summary.json

go run ./scripts/prompt-cache-diagnostics \
  --dump /tmp/go-claude-tui-prompt.jsonl \
  --session <candidate-session-id> \
  --summary \
  --transcript <candidate-transcript.jsonl> \
  > /tmp/go-claude-cache-candidate-summary.json

go run ./scripts/prompt-cache-ab-report \
  --baseline /tmp/go-claude-cache-baseline-summary.json \
  --candidate /tmp/go-claude-cache-candidate-summary.json \
  --baseline-label current-layout \
  --candidate-label stable-prefix-skills
```

已补充自动化 harness，用于真实 provider 交错 A/B：

```bash
scripts/prompt-cache-stable-prefix-ab.sh \
  --model glm-5.1 \
  --scenario agent \
  --out-dir /tmp/go-claude-cache-stable-prefix-ab
```

默认执行顺序：

```text
baseline:warmup,candidate:warmup,baseline,candidate,candidate,baseline,baseline,candidate
```

设计原因：

- warmup 轮只用于预热 provider 隐式 cache，不进入 aggregate。
- baseline/candidate 交错，降低单次 provider cache 波动造成的误判。
- 每轮保存 `session_id`、prompt dump、运行日志、diagnostics summary。
- 每组非 warmup baseline/candidate 生成 pair report。
- 最终输出 `aggregate-report.json`，包含 `candidate_supported`、`candidate_not_supported` 或 `inconclusive`。

复用既有 summary 做离线验证：

```bash
scripts/prompt-cache-stable-prefix-ab.sh \
  --verify-only \
  --out-dir /tmp/go-claude-cache-ab-verify-harness \
  --sequence baseline,candidate,baseline,candidate
```

当前 A-B-A-B smoke 的离线 aggregate 结果：

```text
decision=candidate_not_supported
candidate_win_rate=0.5
avg_hit_input_delta=-0.0876
avg_cache_read_tokens_delta=-7936
```

这说明现有样本只能证明 `GO_CLAUDE_STABLE_PREFIX_SKILLS=1` 会把 `skills_catalog` 从 position 3 前移到 position 2，不能证明它能提高 GLM 5.1 真实 provider cache hit。因此该 gate 仍应保持默认关闭。

通过条件：

```text
GLM 真实 session cached_tokens 提升明显；
无 prompt 优先级/工具选择/skill 调用行为退化；
Prompt Dump 能解释变化来源。
```

失败条件：

```text
cache hit 无明显提升；
agent 行为变差；
dynamic_prompt 其实很稳定，前移收益可以忽略；
skills_catalog 频繁变化，前移反而污染稳定 prefix。
```

### Phase 2D：原版式 skills listing / discovery 方案设计

目标：从根上减少 full `skills_catalog` 常驻 system prompt 的成本。

建议先设计，不直接实现：

1. skills registry 保持运行时可调用。
2. system prompt 只保留简短 Skill tool 使用规则。
3. turn-0 注入小型 listing：
   - bundled / curated / high-confidence skills。
   - 或 top-N relevant skills。
4. 长尾 skills 走 discovery：
   - 根据用户输入、文件路径、工具结果、项目类型检索。
   - 返回 relevant skill names + descriptions。
5. skill 被调用时再注入完整 `SKILL.md`。
6. 维护 sent-skill state：
   - session 内去重。
   - resume 不重复。
   - compact 不重发 full listing。
   - skill 文件变化时清理相关缓存。

这一步收益可能比单纯前移更大，因为它减少的是：

```text
上下文窗口占用
prefix 体积
每轮重复大块
skills 数量增长带来的线性膨胀
```

但风险也更高：

- 需要 Skill tool / discovery 能力完整。
- 可能影响模型发现 skills 的召回率。
- 需要真实 agent 任务验收，不适合直接默认上线。

### Phase 2E：Anthropic-only provider-aware cache_control

目标：只对支持 Anthropic-style prompt cache 的 provider 使用 `cache_control`。

原则：

```text
GLM/OpenAI-compatible: 不注入 Anthropic cache_control
Anthropic native: 可以 feature-gated 给稳定 block 加 cache_control
未知 provider: 默认关闭
```

候选：

- stable core system prompt。
- feature-gated stable skills catalog。
- message-level breakpoint。

必须保留 guard：

- cache_control block 数量限制。
- TTL/scope session latch。
- provider capability 检测。
- diagnostics 显示 cache_control hash 是否变化。

## 不推荐路线

### 不推荐：直接默认前移 `skills_catalog`

原因：

- 原版没有这么做。
- system 顺序可能影响行为。
- 当前只证明它稳定，不证明前移一定提高真实 hit。

### 不推荐：给所有 provider 加 `cache_control`

原因：

- GLM/OpenAI-compatible 不需要 Anthropic cache_control。
- 有些网关会忽略，有些可能报错。
- 容易制造“看起来改了，实际没收益”的假优化。

### 不推荐：本地按 hash 稳定自行计算 cache hit

原因：

- 本地只能判断 cache candidate。
- 真实命中必须以 provider usage 为准。
- 否则会把“稳定但未命中”误报成“已命中”。

## 当前进度

已完成：

- TUI `Responded` 历史 hit 回写风险修复。
- 当前 turn hit 与 session cumulative hit 标签澄清。
- OpenAI-compatible / GLM usage 口径修正：`cached_tokens` 不再二次加到 `input_tokens`。
- Prompt Dump cache diagnostics 与 Viewer 展示。
- `skills_catalog` 的 position/hash/bytes/stability diagnostics。
- `GO_CLAUDE_STABLE_PREFIX_SKILLS=1` feature gate，默认关闭。
- `prompt-cache-diagnostics` session summary。
- `prompt-cache-ab-report` 单组 A/B 比较。
- `prompt-cache-stable-prefix-ab.sh` 真实 provider 交错 A/B harness。

当前真实样本结论：

```text
stable-prefix skills 前移：已验证 gate 生效。
真实 cache hit 提升：未证明。
当前策略：保持默认关闭，不继续扩大影响面。
```

## TODO

### P0：保持现状并积累真实数据

- 使用 `scripts/prompt-cache-stable-prefix-ab.sh` 跑更多真实 agent 场景：
  - 短任务：少工具/无工具。
  - 中任务：多轮 `LS/Grep/Read`。
  - 长任务：tool result 多、上下文更长、包含 resume/compact。
- 每组至少保留：
  - prompt dump。
  - transcript。
  - summary JSON。
  - pair report。
  - aggregate report。
- 如果多组 aggregate 仍是 `candidate_not_supported` 或 `inconclusive`，停止投入 `skills_catalog` 前移。

### P1：provider capability 矩阵

- 明确每类 provider 的 usage 来源：
  - GLM/OpenAI-compatible：`usage.prompt_tokens_details.cached_tokens`。
  - Anthropic native：`cache_read_input_tokens` / `cache_creation_input_tokens`。
  - 未上报 provider：只显示 `not_reported`，不估算命中率。
- 在文档和 diagnostics 中继续区分：
  - provider reported hit。
  - locally stable cache candidate。
  - provider unsupported / not reported。

### P1：skills listing / discovery 方案设计

- 目标不是让 full `skills_catalog` 更容易缓存，而是减少它常驻 prompt。
- 设计 session-level sent-skill state：
  - session 内去重。
  - resume 不重复 listing。
  - compact 后不重发 full listing。
  - skill 文件变化时清理相关状态。
- 设计按需 discovery：
  - 只把小型 listing 或 top-N relevant skills 注入上下文。
  - 长尾 skills 通过检索/Skill tool 发现。
  - 真正调用时再加载完整 `SKILL.md`。

### P2：Anthropic-only provider-aware cache_control

- 只对明确支持 Anthropic-style prompt cache 的 provider 开启。
- 必须 feature-gated。
- 必须 guard cache_control block 数量、TTL/scope 稳定性和 provider capability。
- 不对 GLM/OpenAI-compatible 默认注入 Anthropic `cache_control`。

## 最小下一步

先保持现状，不改默认 prompt 布局。

如果继续优化命中率，优先做两件事：

1. 用 A/B harness 跑更多真实任务，确认 stable-prefix skills 是否有稳定收益。
2. 开始设计 skills listing / discovery，减少 full `skills_catalog` 每轮常驻 prompt。

## 决策建议

当前最优 ROI 顺序：

1. **保持默认关闭**：`GO_CLAUDE_STABLE_PREFIX_SKILLS` 只作为实验开关。
2. **扩大真实 A/B 样本**：确认真实 hit 随 turn/tool/result 怎么波动。
3. **优先减少 full catalog 常驻成本**：如果前移收益不稳定，转向 skills listing/discovery。
4. **provider-aware cache_control 后置**：只做 Anthropic native，不泛化到 GLM/OpenAI-compatible。

一句话：

```text
短期保持现状，不默认搬 prompt；只有 A/B 证明收益稳定，才考虑扩大 stable-prefix skills。
长期不要让 full skills_catalog 永久常驻 system prompt；应向原版的去重、增量、按需发现方向演进。
```
