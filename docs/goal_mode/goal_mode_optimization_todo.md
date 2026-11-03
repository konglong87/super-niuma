# Goal Mode Optimization TODO

本文档是 Goal Mode 优化的执行进度表。后续按 ID 逐项实现、验证、更新状态，避免一次性改动过大。

状态约定：

- `TODO`：未开始。
- `IN_PROGRESS`：正在实现。
- `DONE`：代码、测试、文档和必要验证已完成。
- `BLOCKED`：存在明确阻塞，需记录 blocker 和解除条件。

## 当前基线

| 能力 | 状态 | 证据 |
| --- | --- | --- |
| 持久化 Goal record | DONE | `internal/goal.Goal`、`LocalStore`、tenant MySQL goal tables |
| Runner one-turn | DONE | `goal.Runner.RunOnce` |
| Runner continuous CLI/background | DONE | `goal.Runner.RunUntilStop`、`goal run --background` |
| 预算和重复 blocker 保护 | DONE | `DeterministicEvaluator` |
| API one-turn run | DONE | `/tenant/goals/{id}/run` |
| Mobile goal event subscription | DONE | `/mobile/chat/ws` `subscribe_goal` |
| 结构化 plan/steps/criteria/evidence | DONE | `internal/goal` 已新增 GoalPlan、GoalStep、GoalCriterion、GoalDependency、GoalRisk、GoalEvidence domain schema；本地 store 和 tenant MySQL store 均已支持 plan/evidence |
| Evidence-first evaluator | DONE | `EvidenceEvaluator` 会阻止 required criteria 未通过时完成；证据齐全且无失败 evidence 时才允许 complete，protocol line / model classification 仅作为辅助信号 |
| Markdown completion protocol tolerance | DONE | `GOAL_STATUS: complete` 被模型用 Markdown 加粗或与完成标记同一行输出时，默认 goal criterion 仍可通过并完成；自定义 pending criteria 仍保持 evidence-first gate |
| Completion plan status sync | DONE | Goal 被 evaluator 判定 complete 后会把未完成 step 标记为 done 并清空 `current_step_id`；pending required criteria 被拦截时不会改 plan，避免 `goal status` 对完成目标仍展示 active step |

## 实施 Backlog

| ID | 状态 | 模块 | 待办 | 验收标准 | 建议测试 |
| --- | --- | --- | --- | --- | --- |
| GOAL-OPT-001 | DONE | docs | 建立 Goal 优化技术方案和 TODO 进度文档。 | `docs/goal_mode/goal_mode_optimization_plan.md` 和本 TODO 文档存在，说明现状、不足、目标架构和实施顺序。 | `git diff --check` |
| GOAL-OPT-002 | DONE | goal/domain | 新增 GoalPlan、GoalStep、GoalCriterion、GoalDependency、GoalEvidence domain types。 | 类型字段覆盖 objective 拆分、验收标准、依赖、风险、证据；保持 JSON 兼容；不破坏现有 Goal。 | `go test ./internal/goal -count=1` |
| GOAL-OPT-003 | DONE | goal/store | 为本地 JSONL store 增加 plan/evidence 存储接口。 | 旧 goal 可继续读取；新 plan/evidence 可 save/get/append/list；plan 使用独立 JSON 文件，evidence 使用独立 JSONL 文件，事件和 evidence 分开保存。 | `go test ./internal/goal -count=1` |
| GOAL-OPT-004 | DONE | mysql/tenant | 增加 tenant goal plan/evidence MySQL migration、repository 和 tenant service 方法。 | `000006_tenant_goal_plan_evidence` migration 增加 plan/evidence 表；raw SQL 和 GORM repository 均支持 tenant/user 限定的 save/get/append/list；tenant service 实现 `goal.PlanStore`；不破坏现有 goal API。 | `go test ./internal/storage/mysql ./internal/tenant -count=1` |
| GOAL-OPT-005 | DONE | planner | 新增轻量 deterministic planner：从 objective 生成初始 steps 和 acceptance criteria。 | 没有 plan 的 goal 第一次 run 前自动生成最小 plan；支持用户传入明确验收项时保留原文；CLI/local store 和 tenant API runner 均通过 `PlanStore` 接入。 | `go test ./internal/goal -run Planner -count=1` |
| GOAL-OPT-006 | DONE | reachability | 增加可达路径分析。 | 能识别 cwd 不可用、缺少凭证、外部依赖、需要用户输入、危险操作、预算不足等 blocker/risk 类型；结果写入 plan dependencies/risks；高风险或缺失必需依赖会在 query 前 blocked。 | `go test ./internal/goal -run Reachability -count=1` |
| GOAL-OPT-007 | DONE | runner | `BuildTurnPrompt` 注入 current step、未通过 criteria、最近 evidence、last next_action。 | 下一轮 prompt 可证明包含上一轮 next_action；prompt 包含 current step、未通过 criteria、最近 evidence；recent events 包含 next_action/blocker/error，不再只依赖 reason。 | `go test ./internal/goal -run 'Prompt|Runner' -count=1` |
| GOAL-OPT-008 | DONE | runner | 增加 evidence extraction。 | query tool traces 会转换为 GoalEvidence；命令/test/git/API/DB 等结果可结构化保存；失败工具结果保留 output payload；CLI 和 tenant API runner 均写入 PlanStore。 | `go test ./internal/goal ./internal/cli -run Evidence -count=1` |
| GOAL-OPT-009 | DONE | evaluator | 改造 evaluator 为 evidence-first。 | required criteria 未通过时不能 complete；证据齐全时可 complete；协议线只作为辅助信号。 | `go test ./internal/goal -run Evaluator -count=1` |
| GOAL-OPT-010 | DONE | budget | 增加预算 closing policy。 | 剩余 turn/token 很少时，runner 优先验证/总结/blocked，不继续扩展大任务；model evaluator 可按预算跳过。 | `go test ./internal/goal -run Budget -count=1` |
| GOAL-OPT-011 | DONE | cli | CLI 增加 plan/evidence/status 展示。 | `goal status` 能展示 current step、criteria 进度、关键 evidence；`goal logs` 不被详细 evidence 淹没。 | `go test ./internal/cli -run Goal -count=1` |
| GOAL-OPT-012 | DONE | api/swagger | 增加 tenant goal plan/evidence API。 | API 支持读取 plan、criteria、steps、evidence；Swagger 和 `docs/api_server.md` 同步。 | `go test ./internal/server -run Goal -count=1`; `swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency` |
| GOAL-OPT-013 | DONE | mobile/websocket | Mobile/WebUI goal events 增加 plan/evidence 事件类型。 | 订阅者能收到 step/status/evidence 更新；tenant/user/device 隔离保持不变。 | `go test ./internal/server -run 'MobileWebSocket.*Goal|MobileStreamRegistryGoal' -count=1` |
| GOAL-OPT-014 | DONE | background/api | 评估并实现受控 API continuous runner。 | 明确是否支持服务端 continuous；如果支持，必须有锁、预算、审计、stop、tenant 隔离和后台 job 可观测性。 | `go test ./internal/server ./internal/background ./internal/goal -count=1` |
| GOAL-OPT-015 | DONE | docs | 收敛 Goal 文档入口。 | `docs/goal_mode/` 成为 Goal 基线设计、后续方案和进度主目录；`goal_mode_design.md` 已归入目录内并作为基线设计保留。 | `git diff --check` |
| GOAL-OPT-016 | DONE | runner/evaluator | 修复真实模型把 `GOAL_STATUS: complete` 包在 Markdown 或与 `GOAL_SMOKE_DONE` 同行时无法闭环的问题。 | 只识别独立 `GOAL_STATUS:` 协议键；支持 Markdown 包裹和 trailing marker；默认 `crit_objective_satisfied` 可生成 manual evidence 并通过；自定义 pending criteria 不被绕过。 | `go test ./internal/goal -count=1`; real smoke `/tmp/go-claude-goal-fix-K2ZWla` |
| GOAL-OPT-017 | DONE | runner/cli | Goal complete 后同步 plan step 状态，避免 CLI/TUI/resume 继续看到 active current step。 | evaluator 真实判定 complete 后，pending/active/blocked steps 变为 done、`current_step_id` 清空；skipped step 保持 skipped；未满足 required criteria 的 continue 路径不改 active plan。 | `go test ./internal/goal -run 'CompletesDefaultPlanFromMarkdownProtocolLine|CompletesCustomPlanClearsCurrentStep|UsesEvidenceFirstCompletionGate' -count=1`; `go test ./internal/cli -run Goal -count=1` |

## 推荐实施顺序

1. `GOAL-OPT-001`：先固定问题和方案。
2. `GOAL-OPT-002`、`GOAL-OPT-003`：先做本地 domain/store，风险最小。
3. `GOAL-OPT-005`、`GOAL-OPT-007`：让 runner 真的使用 plan 和 next_action。
4. `GOAL-OPT-008`、`GOAL-OPT-009`：把完成判断改成 evidence-first。
5. `GOAL-OPT-006`、`GOAL-OPT-010`：补可达性和预算 ROI 策略。
6. `GOAL-OPT-011`：让 CLI 可读可操作。
7. `GOAL-OPT-004`、`GOAL-OPT-012`、`GOAL-OPT-013`：扩展到 MySQL/API/Mobile。
8. `GOAL-OPT-014`：最后再决定是否做 API continuous runner。
9. `GOAL-OPT-015`：随着实现推进逐步收敛文档入口。

## 每阶段通用验收

常规代码阶段至少运行：

```bash
go test ./internal/goal -count=1
git diff --check
```

触达 CLI：

```bash
go test ./internal/cli -run Goal -count=1
go test ./internal/goal -count=1
git diff --check
```

触达 API/MySQL/Mobile：

```bash
go test ./internal/goal ./internal/storage/mysql ./internal/tenant ./internal/server -count=1
swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency
git diff --check
```

最终合并前：

```bash
go test ./... -count=1
git diff --check
```

## 当前决策

- Goal 相关新增方案和 TODO 统一放在 `docs/goal_mode/`。
- Goal 基线设计已归入 `docs/goal_mode/goal_mode_design.md`，后续不再把 Goal 文档散落到顶层 `docs/`。
- 优先优化本地 runner 和 evaluator，再扩展 API/WebUI。
- 完成判断必须逐步从自报协议线迁移到 evidence-first，但保留协议线兼容。
