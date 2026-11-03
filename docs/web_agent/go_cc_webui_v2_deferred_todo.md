# go-cc WebUI 2.0 延期能力 TODO

相关总方案：[go_cc_webui_v2_product_architecture.md](go_cc_webui_v2_product_architecture.md)。

## 文档状态

- 状态：`DEFERRED`
- 优先级：WebUI 2.0 核心 Session 管理与 Handoff 能力稳定后再评估
- 当前决定：首期不实现“大任务自动拆分为多个 Managed Session”
- 记录日期：2026-09-03

## 已确认但延期的能力

总管 Session 可以把一个大型任务规划为多个独立 Managed Session，例如前端、后端和数据层。每个子 Session 拥有独立上下文、Profile、任务范围和验收标准，内部仍可继续使用 Agent/Subagent。

该能力采用两阶段协议：

1. `SessionPlan` 只生成并校验拆分方案，不创建 Session。
2. 用户批准固定的 `plan_id + plan_hash` 后，复用 `SessionCreate` 按依赖顺序幂等创建子 Session。

计划必须包含：

- 总目标与 controller Session。
- 每个 child 的标题、Profile 固定版本、完整 brief 和验收标准。
- `write_scopes`、依赖 DAG、required 标记和 integration owner。
- 最大并发 Session、总 token、派生深度和自动干预次数。

## 未来监督与收敛规则

- 总管 Session 不保持永不结束的模型调用，只在子 Session 完成、失败、等待输入、权限阻塞、证据缺失、scope 冲突或定时任务检测到状态变化时启动离散 supervision Run。
- 状态 fingerprint 未变化时不调用模型，避免周期轮询重复消耗 token。
- 每个 child 的自动干预次数必须有上限；超过上限后转为 `needs_user`。
- 总管不得自动批准权限、扩大写目录、创建 worktree、发布 Profile、commit、push 或无限重试。
- 所有 required children 终态、HandoffPackage ready、blocker 清空且验收标准拥有 verified evidence 后，才能进入 integration。
- 默认由 controller Session 负责最终 diff、测试、readback 和 Git 操作。

## Session 与 Agent 的选择规则

不能只按文件数量决定是否派生 Session。满足至少两个条件时才建议 Managed Session：

- 有独立验收结果。
- 需要长期运行或后续单独追问。
- 预计显著挤占 controller Session 上下文。
- 可以定义独立 workspace/write scope。
- 能与其他任务并行或存在明确依赖。
- 需要独立 Profile、模型或权限策略。

其他任务继续使用现有 Agent/Subagent。

## 实现前置条件

- `golang-cc session` 已具备 Managed Session 的 create/list/get/send/stop/attach/monitor 能力。
- CLI、Session Tool、Web API 和 Scheduler 已统一使用 SessionControlService。
- `SessionHandoffPackage v1` 的确定性事实、模型压缩、证据索引、token 预算和 stale 规则已稳定。
- `tenant_session_links` 已覆盖 spawned/attached/dependency/observed 关系与控制权限。
- `tenant_agent_tasks.idempotency_key` 已覆盖网络重试和 Saga 恢复。
- workspace write scope 冲突、tenant/user 隔离、权限与审计负向路径已经过真实验证。

## 未来验收边界

- 一个包含三个 child 的 Plan 能部分成功并通过相同 idempotency key 只补失败 child。
- dependency DAG、循环依赖、重复 child、未发布 Profile 和超预算 Plan 均被拒绝。
- 同一 workspace 的重叠写范围不会并行执行。
- 子 Session 的失败、取消、超时和 evidence 缺失不会被误判为总任务完成。
- controller 重启后仍可从 Session、links、task events 和 HandoffPackage 恢复进度。
- 未变化的监控轮次不触发模型调用；变化摘要可通过现有 Channel Outbox 投递飞书。

## 拓扑影响预判

- 最高爆炸半径：`B5_SHARED_STATE`
- 涉及节点：`RT-BOUNDARY`、`RT-WIRING`、`RT-PROMPT`、`RT-TOOLS`、`RT-EVIDENCE`、`RT-SUBAGENT`、`RT-SCHEDULER`、`RT-PERSIST`、`RT-OUTPUT`、`RT-OBSERVE`
- 实现时必须重新评估 topology registry、全局图件、授权 gate、prompt bytes、token、turn、tool call、延迟和 cache 影响。
