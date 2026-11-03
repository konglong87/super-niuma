# go-claude Provider 中立化清理方案

## 背景

go-claude 借鉴了 Claude Code 的工具契约与内置 agent 定义（见 [AGENTS.md](../AGENTS.md)），`opus/sonnet/haiku` 等 Anthropic 词汇作为「工具契约 + 内置 agent 定义」照搬自原版。它们在设计上是**抽象能力档位**，只在 `resolveSubagentModel` 一处翻译成具体模型；子代理档位映射已通过 `settings.subagentModelTiers` + provider 感知解析做到 provider 中立（见 [subagent_model_tiering_plan.md](subagent_multiagent/subagent_model_tiering_plan.md)）。

本文件清点**剩余的 Anthropic 硬耦合**，分级给出清理方案。README 定位是「Claude Code 是参考系不是终点」，因此这些属于"参考系痕迹"，非缺陷；按影响度选择性清理。

## 残留耦合清单（实测）

| # | 位置 | 现状 | 对非 Anthropic provider 的影响 |
|---|------|------|------------------------------|
| ① | `internal/server/handlers_openai.go:283` | `/v1/models` 无 `opts.Models` 时兜底 `config.KnownModels` | OpenAI 兼容客户端拿到 `claude-*` 列表，用户真实模型缺席 |
| ② | `internal/session/store.go:1747 modelRates` | 计价表只认 `claude-*` | 非 Anthropic → `EstimateCostUSD` 返回 0 → `cost_usd` 恒为 0 |
| ③ | `internal/config/config.go:16 builtinDefaultModel` = `claude-sonnet-4-6` | 无配置时默认 | 低：用户通常已配 `model` |
| ④ | `internal/config/config.go:18 KnownModels` + `internal/agentruntime/runtime.go:725 defaultModelForFamily` | 档位→模型走 Anthropic 清单 | 已被 `isAnthropicModel` 门控，只在父模型 Anthropic 时走。**已隔离** |
| ⑤ | `internal/query/query.go:7085` `DISABLE_PROMPT_CACHING_*` + `internal/anthropic/client.go:1352` `cache_control` | 缓存开关按模型名；cache_control 照发 | Anthropic 兼容网关多能接受/忽略；自定义网关需验证 |
| — | `internal/config/config.go:360 ModelContext` | 上下文窗口 model→tokens | **已是配置驱动**，无需清理 |

## 分级方案

### P0 — 真实影响，✅ 本轮已实现

**① `/v1/models` 反映真实 providers** ✅
- `handlers_openai.go`：`opts.Models` 为空时兜底 `config.ConfiguredModels(opts.Workspace)`（返回主模型 + 各 fallback provider model），仍空再退 `KnownModels`。
- 验证：`TestOpenAIModelsFallsBackToConfiguredProviders`（无显式 Models 时列出配置模型、不含 claude-*）；`TestServerGoldenOpenAIModels`（显式 Models 路径 golden 不变）。

**② 计价配置化（补全成本可观测性）** ✅
- `config.Settings.ModelPricing map[string]ModelPrice`（`ModelPrice{Input, Output float64}`，每百万 token 单价），跨 settings 源逐键合并；`config.ModelPricing(cwd)` 访问器。
- `session`：新增 `Rate` 类型与 `EstimateCostUSDWithRates(model,in,out,rates)`——命中配置单价即用，否则回退内置 Anthropic 表；`EstimateCostUSD` 保持不变（向后兼容）。
- `agentruntime.Run`：`subagentCostRates(req.CWD)` 载入配置单价，三处成本计算改用配置感知版本。
- 作用域：子代理成本（含批量 `cost_usd` / `total_cost_usd`）。session transcript 用量汇总（`store.go`）暂留内置表——它无就近 cwd，且非成本反馈主链路，作为已知残留记录。
- 验证：`TestLoadSettingsMergesModelPricing`、`TestEstimateCostUSDWithRatesPrefersConfiguredRate`、`TestTaskToolBatchCostUsesConfiguredPricing`（配置 GLM 单价 → 批量 `total_cost_usd` 正确）。

settings.json 示例（让 GLM 成本可见）：

```json
{
  "model": "glm-5.1",
  "modelPricing": { "glm-5.1": { "input": 2, "output": 10 } }
}
```

### P1 — 收尾 ✅ 已实现

**③ 出厂默认模型去 Anthropic** ✅
- `ResolveModel`：在 explicit / project / env / 顶层 `model` 都为空时，新增 `firstConfiguredProviderModel(cwd)`——取 selected provider 或首个 fallback provider 的 model，再退 `DefaultModel()`（`builtinDefaultModel`）。
- 有 providers 但无显式 model 的非 Anthropic 会话，不再默认到它无法服务的 `claude-sonnet-4-6`。
- 验证：`TestResolveModelFallsBackToConfiguredProviderModel`；既有 `TestResolveModelPriority` 全部不变（都在更早层解析命中）。

### P2 — 兼容细节（已验证，结论：不做一刀切）

**⑤ 缓存控制按 provider 门控**

实测（2026-07-23）向脱敏后的示例网关 `ai-gateway.example.com/v1/messages` 直发请求：

| 请求 | 结果 |
|------|------|
| 无 `cache_control` | HTTP 200 |
| 带 `cache_control`（system + message 块 ephemeral） | HTTP 200，无报错 |

- 响应为标准 Anthropic 格式，`usage` 带 `cache_creation_input_tokens` / `cache_read_input_tokens`；真实大上下文会话曾观测到 `cache_read=90752`。
- 即该网关**接受并 honor** Anthropic 缓存协议。原设想「非 Anthropic 一律不发 cache_control」对它是**反效果**（丢缓存命中、变贵变慢）。

**决策（2026-07-23，已定）：(a) 不做，关闭这条线。**

不做任何 `cache_control` 门控——目前无任何已知网关因它报错（实测本网关返回 200 且受益于缓存）。当前对所有 provider 一律照发 `cache_control`。`DISABLE_PROMPT_CACHING_*` 保留（Anthropic-only 开关，无害）。

若将来遇到确实拒绝 `cache_control` 的网关，再单独立项做 per-provider opt-out 开关（如 provider 配置 `disableCacheControl: true`），不预先实现。

## 状态

provider 中立化 P0①②、P1③ 已完成并发布（tag `v0.1.34-go`）；P2⑤ 决策不做（本节）；P3 明确不做。**本清理方案到此收口。**

### P3 — 明确不做

**tier 词汇（sonnet/opus/haiku）泛化成可配置命名**：不做。工具 schema、prompt、agent frontmatter、skills 全引用这三词，泛化高 churn、零功能收益；词汇作为能力档位抽象已够用，provider 中立性由解析层 + `subagentModelTiers` 保证。

## 实施顺序

P0 `① → ②`（本轮）；P1/P2 视需要；P3 只记录决策。每项 TDD、`go test ./...` 全绿再提交，文档同步。
