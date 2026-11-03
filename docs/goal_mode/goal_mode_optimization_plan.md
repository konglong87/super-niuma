# Goal Mode Optimization Plan

本文档记录 Goal Mode 下一阶段优化方案。它基于当前代码事实，不把 Goal Mode 描述成“保证目标必达”的机制；目标是把现有“持久化状态机 + 连续 query turn”升级为更可控、更省 token、更容易验收的目标执行系统。

## 当前基线

当前实现已经具备合理的 P0/P1 基础：

- `internal/goal.Goal` 持久化 objective、status、session、预算、usage、last blocker、checkpoint 和错误信息。
- `goal.Runner.RunOnce` 每轮加锁、读取 active goal、创建 turn event、生成 checkpoint、执行一个普通 query turn、累计 usage、评估状态并写回事件。
- `goal.Runner.RunUntilStop` 只在 goal 仍为 `active` 且 context 未取消时继续下一轮。
- 默认 `DeterministicEvaluator` 会处理 turn/token budget、硬阻塞、重复软失败、`GOAL_STATUS: complete|blocked` 协议线。
- CLI/TUI/API 已有 start/status/list/logs/stop/resume/run 管理面；API 当前是 one-turn run。

这些机制解决了“有状态、可停止、可观察、不会无限无预算运行”的基础问题。

## 主要不足

### 1. 缺少结构化目标拆分

证据：

- `internal/goal.Goal` 只有 `Objective`、`Status`、`TurnBudget`、`TokenBudget`、`LastReason`、`LastNextAction` 等字段，没有 `steps`、`current_step`、`acceptance_criteria`、`dependencies`、`evidence`。
- `BuildTurnPrompt` 只注入 objective、当前状态、预算、最近事件和通用规则，没有固定的阶段计划或验收清单。

影响：

- 长目标会退化成“每轮继续努力”，缺少可追踪的阶段边界。
- 不能明确区分探索、设计、实现、验证、提交、推送等步骤。
- 用户很难从 status/logs 中判断目标离完成还有多远。

### 2. 可达路径分析不足

证据：

- 当前 hard blocker 主要靠错误文本包含 `permission denied`、`authentication`、`api key`、`quota`、`account`、`external dependency` 等关键词。
- Goal 创建后没有先分析“本地是否具备完成条件”“是否需要网络、凭证、外部服务、用户输入、真实数据库、部署权限”等。

影响：

- 不可达目标可能先消耗多轮尝试后才 blocked。
- 对依赖外部系统的目标，无法提前向用户暴露阻塞条件和替代路径。

### 3. `next_action` 没有充分闭环

证据：

- `Decision.NextAction` 会写入 `Goal.LastNextAction`。
- 但 `BuildTurnPrompt` 没有注入 `LastNextAction`。
- recent events 在 prompt 中只包含 `turn/type/status/reason`，没有包含 `next_action`、`blocker_key`、`error`。

影响：

- evaluator 产出的下一步信号没有稳定影响下一轮执行。
- 连续运行容易重复探索，尤其是失败重试或多步骤任务。

### 4. 完成判断偏依赖自报

证据：

- deterministic evaluator 遇到响应里的协议线 `GOAL_STATUS: complete` 即标记 complete。
- model evaluator 可选启用后，会基于 turn response 进行分类；它没有读取结构化验收证据字段。
- `TurnResult` 记录 response、usage、tool errors、checkpoint、error，但没有标准化的 test result、git status、diff status、artifact evidence、acceptance checklist。

影响：

- “完成”更像 agent 自报，而不是由可验证证据驱动。
- 对必须测试、提交、推送、curl、MySQL 直查的目标，缺少统一验收入口。

### 5. Token 控制是事后截断，不是事前规划

证据：

- token budget 在 evaluator 阶段检查，此时本轮 query 已经完成并累计 usage。
- CLI goal turn 会先通过 `loadResumeMessages` 读取 session transcript，再运行 query。
- query 层有 auto compact，但 Goal 本身没有根据剩余 token 选择短上下文、跳过模型分类、降低输出、优先执行验证或停止。

影响：

- 能防止无限消耗，但不保证高 ROI。
- 复杂目标后期可能把 token 花在重复上下文和重复判断上。

### 6. 阻塞判定粒度偏粗

证据：

- 软失败用 `blockerKey(error text)` 聚合，相同 blocker 连续达到阈值才 blocked。
- hard blocker 关键词覆盖有限。
- 没有把 blocker 分类为 `needs_user_input`、`missing_credential`、`external_service_down`、`ambiguous_requirement`、`unsafe_destructive_action`、`local_test_failure` 等。

影响：

- 用户看到 blocked 时需要再读事件才能判断怎么解。
- 对可自动恢复的失败和必须人工处理的失败区分不够清晰。

### 7. API 与 CLI 行为边界不完全一致

证据：

- CLI 支持 `RunUntilStop` 和 background goal run。
- tenant API 目前暴露 `/tenant/goals/{id}/run` one-turn run，不提供服务端 continuous runner。
- API runner 的 checkpoint 行为依赖 session id 是否能映射本地 transcript；相关 TODO 里也标注过 API runner checkpoint 边界。

影响：

- 不同入口的能力模型不完全一致，后续 WebUI/Mobile 如果要驱动长期目标，需要明确是客户端轮询 one-turn，还是新增受控后台 runner。

### 8. 文档和进度承载需要归拢

证据：

- Goal 基线设计和 API/Mobile TODO 已统一归入 `docs/goal_mode/`。
- Goal 相关后续优化如果继续写到顶层 `docs/`，会让入口再次分散。

影响：

- 后续执行者难以判断应该看哪个文档。
- TODO 和设计方案容易漂移。

## 目标架构

下一阶段建议把 Goal Mode 拆为四层：

```text
Goal Record
  objective, status, budgets, usage, session, timestamps

Goal Plan
  acceptance_criteria, steps, dependencies, risks, current_step

Goal Evidence
  command results, tests, git status, docs touched, API/curl evidence, artifacts

Goal Runner
  one-turn execution, evaluator, checkpoint, blocker handling, budget policy
```

设计原则：

- Goal Record 继续保持轻量，兼容现有 JSONL/MySQL/API。
- Goal Plan 作为新增结构，不直接替代原有 objective。
- Evidence 必须是结构化、可序列化、可在 CLI/API/WebUI 展示的对象。
- Evaluator 优先使用结构化 evidence，再使用协议线和模型分类。
- API continuous runner 必须受预算、租户隔离、锁、审计、停止控制约束。

## 结构化数据建议

### GoalPlan

```go
type GoalPlan struct {
    GoalID             string
    Version            int
    Summary            string
    AcceptanceCriteria []GoalCriterion
    Steps              []GoalStep
    Dependencies       []GoalDependency
    Risks              []GoalRisk
    CurrentStepID      string
    CreatedAt          time.Time
    UpdatedAt          time.Time
}
```

### GoalStep

```go
type GoalStep struct {
    ID          string
    Title       string
    Status      string // pending|active|done|blocked|skipped
    Rationale   string
    DependsOn   []string
    EvidenceIDs []string
}
```

### GoalCriterion

```go
type GoalCriterion struct {
    ID          string
    Description string
    Required    bool
    Status      string // pending|passed|failed|waived
    EvidenceIDs []string
}
```

### GoalEvidence

```go
type GoalEvidence struct {
    ID        string
    GoalID    string
    Type      string // command|test|git|api|db|doc|artifact|manual
    Summary   string
    Command   string
    ExitCode  int
    Passed    bool
    Payload   json.RawMessage
    CreatedAt time.Time
}
```

本阶段不要求一次性实现所有字段，但 schema 要先稳定，避免后续 API/WebUI 反复迁移。

## 执行流程优化

建议把 `goal run` 变成以下阶段：

```text
1. Load goal.
2. If no plan exists, create or refresh GoalPlan.
3. Run reachability analysis:
   - local repo state
   - missing credentials
   - external dependencies
   - destructive action risk
   - ambiguity requiring user input
4. Select the next active step.
5. Build a compact turn prompt from objective + current step + acceptance criteria + recent evidence.
6. Execute one query turn.
7. Extract structured evidence from command/tool/query result.
8. Update step and criteria statuses.
9. Evaluate complete/blocked/failed/continue.
10. Persist goal, plan, evidence, events and telemetry.
```

关键变化：

- 下一轮不再只看 objective 和 recent events，而是看 current step 和未通过的验收项。
- 如果剩余 token/turn 很少，runner 应优先验证、总结或 blocked，而不是继续展开。
- 如果目标不可达，尽早 blocked，并给出可操作 blocker 类型和解除条件。

## Prompt 精简策略

当前 prompt 每轮都包含 objective、状态、预算、最近事件和规则。优化后建议：

- 只注入当前 step、未完成的 acceptance criteria、最近 3-5 条关键 evidence。
- 对已经完成的步骤只注入摘要，不重复完整事件。
- 当 `remaining_turns <= 2` 或 `remaining_tokens` 低于阈值时，进入 closing policy：只允许验证、总结、blocked 或 complete。
- model evaluator 默认只在 deterministic evidence 无法判断时调用；低预算时跳过模型分类。

## 完成判定策略

建议完成判定顺序：

1. 所有 required acceptance criteria 为 `passed` 或明确 `waived`。
2. 当前 goal policy 要求的命令已通过，例如 `go test ./... -count=1`、`git diff --check`、curl 或 MySQL 直查。
3. 如果要求提交和 push，必须有 git evidence。
4. 没有未解决 blocker。
5. 最后才接受 `GOAL_STATUS: complete` 或 model classification。

这样可以保留当前协议线兼容性，同时把它降级为辅助信号。

## 兼容和迁移

- 本地 JSONL store 可以先把 plan/evidence 放到 owned global root 下的独立文件：`~/.go-claude/goals/plans/<goal-id>.json`、`~/.go-claude/goals/evidence/<goal-id>.jsonl`。
- MySQL 可以新增表，不破坏 `tenant_goals` 和 `tenant_goal_events`：
  - `tenant_goal_plans`
  - `tenant_goal_steps`
  - `tenant_goal_criteria`
  - `tenant_goal_evidence`
- API 先新增只读 plan/evidence 端点，再开放 plan refresh 和 controlled run。
- 旧 goal 没有 plan 时，第一次 run 自动生成 plan，或保持 legacy mode 并在 status 中标注。

## 验证要求

每个优化阶段都要有可证据化验收：

- 单元测试：plan schema、step 状态转移、criteria 判定、evidence 存取。
- runner 测试：无 plan 自动生成、next_action 进入下一轮 prompt、预算 closing policy。
- evaluator 测试：required criteria 未过时不能 complete；证据齐全时 complete。
- API 测试：tenant 隔离、plan/evidence CRUD、run 事件广播。
- 文档同步：更新 `docs/goal_mode/goal_mode_optimization_todo.md` 和相关 API 文档。

## 不建议做的事

- 不把 goal mode 改成不受控无限后台执行。
- 不把所有状态塞进 prompt 而不持久化。
- 不只依赖模型总结判断完成。
- 不一次性重构整个 docs 目录。
- 不在没有 plan/evidence schema 的情况下先做 WebUI 大面板。
