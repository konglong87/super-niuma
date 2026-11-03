# Sub-agent 按难度分级模型（model tiering）方案

## 背景

原版 Claude Code 中，真正按任务难度分配模型的地方是 subagent 和 workflow：主模型在调用时按子任务难度单独指定模型——机械性的批量搜索、格式转换用 haiku；一般的代码探索、实现默认继承会话模型；最难的架构评审、对抗性验证保持最强模型甚至调高 reasoning effort。默认行为保守：不指定就直接继承主会话模型，只有主模型很确定小模型足够时才主动降级。

本文档梳理 go-claude 现状，并给出对齐该模式的方案。本轮只做方案，不修改运行时代码。

## 现状调研

结论先行：**地基已经打好约 80%，真正缺的只有一块——把 `model` 参数暴露给 Task 工具的调用方。**

已经具备的能力：

- `internal/agentruntime/runtime.go:655` 的 `resolveSubagentModel()` 已实现三级优先级：**请求指定 > Agent 定义指定 > 继承父会话**。这正是原版的行为（不指定就保守继承）。
- `internal/agents/agents.go:14` 的 `Agent` 结构体已有 `Model` 和 `Effort` 字段，支持 `haiku`/`sonnet`/`opus` 别名和 `inherit`。内置 Explore agent 已硬编码 `Model: "haiku"`（`internal/agents/builtin.go:122`），"小模型干探索杂活"的模式已经存在。
- LLM client 层（`internal/anthropic/client.go:142` 的 `StreamMessages`）本来就是 per-request 指定模型，且会按模型重排 provider，底层无障碍。
- 后台杂活用小模型已有对应物：AutoCompact 的 `SummaryModel`、Recap 的独立 `Model` 配置（`internal/config/config.go:343`、`:355`）。
- task 落库时已记录 `Model` 字段（`internal/tools/task/task.go:1556`）。

唯一的断点：

- `internal/tools/task/task.go:30` 的 `taskParam` 和第 134-149 行的 InputSchema 中没有 `model` 字段。主模型在调用时**没法**做"这个子任务简单，降级到 haiku"的判断——机制在 runtime 层是通的，但没接到工具接口上。
- 批量模式的 `runBatchItem()`（`task.go:536`）同样没有透传 Model，每个批任务项无法单独指定。

## 方案：三层结构，各管各的

### 第一层：静态分配（已可用，零改动）

Agent 定义 frontmatter 里写 `model: haiku`，适合"这类 agent 天生就该用什么模型"的场景（如 Explore）。这层已经做完。

### 第二层：动态分配（核心缺口，改动很小）

给 Task 工具的 schema 加一个可选 `model` 参数，透传到 `agentruntime.Request.Model`。两个设计细节参照原版：

1. **参数用别名枚举，不用完整模型 ID。** 原版 Agent 工具的 `model` 参数是 `enum: ["sonnet", "opus", "haiku", ...]`。调用方表达的是"能力档位"而非具体版本：模型升级时不用改任何 prompt，也天然防住传错模型名。别名到真实 ID 的映射放 config 层（`KnownModels` 附近），允许用户在 settings 里覆盖。
2. **批量模式的每个 task item 也要能单独指定。** 一批任务里往往难度不均，这正是分级最有价值的地方。

需要改动的位置：

- `internal/tools/task/task.go:30`：`taskParam` 加可选 `Model string`
- `internal/tools/task/task.go:134-149`：InputSchema 为单任务和批任务项添加 `model` 枚举参数
- `internal/tools/task/task.go:221-230`、`:581-590`：构造 `agentruntime.Request` 时透传 `Model`
- config 层：别名 → 真实模型 ID 的映射表，可被 settings 覆盖

runtime 的解析逻辑（`resolveSubagentModel()`）一行都不用动。

### 第三层：判断策略写进工具描述，而不是写进代码

机制通了之后，"什么时候降级"的判断交给主模型，靠 Task 工具的 description 引导。措辞参照原版 Workflow 工具 `opts.model` 的保守风格：

> Optional model override. Default to omitting it — the agent inherits the session model, which is almost always correct. Only set it when you're highly confident a lower tier suffices (mechanical search, format conversion, bulk extraction). When unsure, omit.

要点：默认继承 + 明确列出可降级的白名单场景，比任何自动规则都稳。

## 明确不做的方向

**不在工具层做难度自动推断**（按 prompt 长度、关键词打分选模型）。理由：

- 主模型本身就是最好的难度判断器，Go 代码里的启发式规则（关键词、长度）是二次猜测，既脆弱又和第二层功能重复，属于投机性设计。
- 真要"自动化"，未来更合理的形态是让一个便宜模型做 router，但那是另一个量级的功能，现在不值得。

## 加分项（可选，随主改动一起或后续做）

1. **Effort 透传**：`Agent.Effort` 字段已存在，可和 `model` 一起暴露成 Task 的可选参数（同样保守默认继承）。难度分级其实是二维的：模型档位 × 思考预算。
2. **成本可观测性**：task 落库已记录 `Model`，在 task 汇总输出里带上各子任务使用的模型和 token 数，让用户验证分级策略确实省了钱，也为调整 description 措辞提供反馈回路。

## 实施清单

```text
1. config 层加别名映射（haiku/sonnet/opus → 真实模型 ID，settings 可覆盖）
   → 验证: 单测覆盖别名解析与 settings 覆盖
2. taskParam / InputSchema 加可选 model 枚举参数（单任务 + 批任务项）
   → 验证: schema 校验拒绝非法值；合法别名透传到 agentruntime.Request.Model
3. runBatchItem / runSingle 透传 Model
   → 验证: 单测确认 resolveSubagentModel 收到请求级 Model 且优先级正确
4. Task 工具 description 补充保守降级引导措辞
   → 验证: 人工 review 措辞与原版语义一致
5. （可选）Effort 同路径透传；task 汇总输出附各子任务模型与 token 用量
```

## 关键位置速查

| 概念 | 文件 | 行号 | 关键结构/函数 |
|------|------|------|--------------|
| 子代理请求 | internal/agentruntime/runtime.go | 57-76 | `Request`（含 `Model` 字段） |
| 模型解析 | internal/agentruntime/runtime.go | 655-667 | `resolveSubagentModel()` 三级优先级 |
| Agent 定义 | internal/agents/agents.go | 14-35 | `Agent`（含 `Model`/`Effort`） |
| 内置 Explore | internal/agents/builtin.go | 102-145 | `Model: "haiku"` |
| Task 工具参数 | internal/tools/task/task.go | 30-37 | `taskParam`（目前无 `model`） |
| Task InputSchema | internal/tools/task/task.go | 134-149 | 工具接口定义（目前无 `model`） |
| 批量任务执行 | internal/tools/task/task.go | 536-606 | `runBatchItem()`（未传 Model） |
| 已知模型列表 | internal/config/config.go | 18-27 | `KnownModels` |
| API 请求构建 | internal/anthropic/client.go | 40-51 | `MessagesRequest`（含 `Model`） |
| Provider 选择 | internal/anthropic/client.go | 142-194 | `StreamMessages()` 按模型重排 |

## 已实现 — provider 感知的别名解析 + 配置档位映射

起因：非 Anthropic provider（如 GLM，会话模型 `glm5.1`）下跑内置 Explore（`Model: "haiku"`）时，子代理被解析成 `claude-haiku-4-5` 并原样发给 GLM 端点——TUI 显示 `(claude-haiku-4-5)`，实际请求也带这个模型名，而不是用户配置的 `glm5.1`。根因：`resolveSubagentModelSpec` 把 tier 别名（sonnet/opus/haiku）无条件展开成 Anthropic 具体模型（查 `KnownModels`），未考虑当前 provider。

### 第一层修复：provider 感知（启发式）

`resolveSubagentModelSpec`（`internal/agentruntime/runtime.go`）的 tier 分支改为：

- 父模型是 Anthropic（`isAnthropicModel()` — 名字含 `claude`）或为空 → 保持原行为，展开成 `defaultModelForFamily(tier)`。
- 否则（非 Anthropic）→ 见第二层；无配置则**继承父模型**，不再跳到 provider 不提供的 Anthropic 模型。

provider 判定用纯字符串启发式（Anthropic 模型皆 `claude-*`），不必把 provider 配置一路 plumbing 进解析器。优先级语义（请求 > agent > 父）不变。

### 第二层实现：可配置的档位映射（settings 可覆盖）

落地了本文档 line 36 设想的「别名→模型映射放 config 层、settings 可覆盖」，作用域限定在非 Anthropic provider：

- `config.Settings.SubagentModelTiers map[string]string`（`json:"subagentModelTiers"`），跨 user/project/local 按 `mergeMap` 逐键合并。
- 访问器 `config.SubagentModelTiers(cwd)`；调用点 `runtime.go` 的 `resolveSubagentModel(...)` 第四参传入。
- 命中 tier key → 用配置模型；未配置 → 继承父模型（即 `glm5.1`）。

settings.json 示例（让 Explore 的 haiku 档走更便宜的 GLM 模型）：

```json
{
  "model": "glm5.1",
  "subagentModelTiers": { "haiku": "glm-4-flash", "opus": "glm-4-plus" }
}
```

作用域说明：档位映射目前只在非 Anthropic provider 生效；Anthropic 会话仍走 `KnownModels` 家族默认（`claude-haiku-4-5` 等）。

### 验证

- `TestResolveSubagentModelPriorityAndAliases`（新增 GLM 继承 / 配置档位 / 缺 key 继承 / 具体模型透传 / 请求别名覆盖 / Anthropic 作用域 等用例，并保留原 4 例回归）。
- `TestLoadSettingsMergesSubagentModelTiers`（user→project 逐键覆盖 + 访问器）。

## 已实现 — Task 工具动态 model 参数（第二层的另一半）

补齐了本文档「第二层：动态分配」的核心缺口：主模型现在可在调用 Task 时按子任务难度指定档位。

改动全部落在 `internal/tools/task/task.go`（runtime 解析逻辑一行未动，请求 > agent > 父的优先级由既有 `resolveSubagentModel` 保证）：

- `taskParam` 与顶层 `Run` 参数结构体加可选 `Model string`。
- InputSchema 顶层与 `tasks` 数组项各加 `model` 枚举参数（`["sonnet","opus","haiku"]`），措辞采用保守默认继承 + 明确降级白名单场景。
- 四处 `agentruntime.Request` 构造（单任务前台/后台、批量前台/后台）透传 `Model`。
- 工具 description 补充保守降级引导（默认省略、只对机械性子任务降到 haiku）。

别名经 `resolveSubagentModel` 解析：Anthropic 会话映射到家族模型（如 opus→`claude-opus-4-8`）；非 Anthropic（GLM）命中 `settings.subagentModelTiers` 则用配置模型，否则继承会话模型。与第一层/静态档位映射天然衔接。

验证：
- 全链路（Task 输入 JSON → `Request.Model` → 解析 → 实际 `StreamMessages` 请求模型）：`TestTaskToolSingleModelOverrideReachesRequest`（opus→`claude-opus-4-8`）、`...InheritsOnNonAnthropicProvider`（GLM 继承）、`...UsesConfiguredTierOnNonAnthropicProvider`（配置档位→`glm-4-flash`）、`TestTaskToolBatchPerItemModelReachesRequest`（批量逐项）。
- Schema/描述：`TestTaskToolSchemaExposesModelOverride`、`TestTaskToolDescriptionGuidesModelOverride`。
- 既有 golden（批量响应结构）不变，`go test ./...` 全绿。

## 已实现 — 加分项（Effort 透传 + 成本可观测性）

两个加分项均已落地。

### Effort 透传

难度分级的第二维（模型档位 × 思考预算）现已打通：

- `agentruntime.Request` 加 `Effort` 字段；`runtime.Run` 用 `firstNonEmpty(req.Effort, agent.Effort)` 解析，请求级覆盖 agent frontmatter（与 model 同优先级语义）。
- Task 工具 `taskParam` 与顶层参数加 `Effort`；InputSchema 顶层与 `tasks` 数组项各加 `effort` 枚举（`["low","medium","high","max"]`），四处 `Request` 构造透传。
- description 补充保守引导（默认继承、仅对困难子任务提高）。
- effort 经既有 `anthropic.ThinkingConfigFromEffort` 转成 thinking 预算注入请求。

验证（全链路：Task 输入 → `Request.Effort` → `result.Effort` → 实际 `StreamMessages` 请求的 `Thinking.Effort`）：`TestTaskToolSingleEffortOverrideReachesRequest`、`TestTaskToolBatchPerItemEffortReachesRequest`、`TestTaskToolSchemaExposesEffortOverride`、`TestTaskToolDescriptionGuidesEffortOverride`。

### 成本可观测性（批量汇总）

批量响应现在带上每个子任务的实际模型与 token 用量，形成"分级是否省钱"的反馈回路：

- `batchResult` 加 `model` / `input_tokens` / `output_tokens` / `cost_usd`（`omitempty`），从各子任务 `agentruntime.Result`（含 `Usage`/`CostUSD`）填充。
- `batchSummary` 加 `total_input_tokens` / `total_output_tokens` / `total_cost_usd` 汇总。
- 作用域限批量 `汇总输出`；单任务仍返回子代理原始答案，不污染正文。

验证：`TestTaskToolBatchReportsModelAndTokenUsage`（每任务 model + tokens + summary 合计）；三份既有 batch golden 相应更新（新增 `model` 字段）。`go test ./...` 全绿。
