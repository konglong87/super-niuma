# Agentic Patterns 学习练习索引

这个索引把 21 章拆成可执行练习。读者不需要一次读完所有章节，可以按阶段完成“理论理解 -> 源码定位 -> 机制复述 -> 验证证据 -> 改进思考”的闭环。

## 使用方式

每个练习都按同一格式完成：

1. 阅读对应章节的“书中理论要点”和“源码映射表”。
2. 打开列出的源码或文档路径，确认章节描述不是凭空推断。
3. 画出或复述一条运行链路。
4. 跑章节里的验证命令，或用 `rg` 查找对应符号。
5. 写下一个冲突场景、一个兜底策略和一个可测试的改进点。

## 阶段 1：单 Agent 主链路

目标：理解一个 coding agent 如何从 prompt 进入模型，再通过工具和验证完成任务。

| 练习 | 对应章节 | 必做产物 |
| --- | --- | --- |
| Prompt 加载顺序复盘 | [第 1 章：提示词链](chapter-01-prompt-chaining.md) | 画出 `OverrideSystemPrompt`、默认 system、`SystemAddendum`、code memory 的优先级图。 |
| Code/Chat 路由边界 | [第 2 章：路由](chapter-02-routing.md) | 对比 CLI/TUI code mode 与 OpenAI-compatible chat mode 的上下文差异。 |
| 工具调用生命周期 | [第 5 章：工具使用](chapter-05-tool-use.md) | 画出 `tool_use -> guarded execution -> tool_result -> next turn` 时序。 |
| 规划与 Todo 状态 | [第 6 章：规划](chapter-06-planning.md) | 解释为什么同一时间只能有一个 `in_progress`，并列出冲突处理规则。 |

建议验证：

```bash
go test ./internal/query ./internal/tools ./internal/goal -count=1
git diff --check -- docs/agentic_patterns_learning
```

## 阶段 2：长任务闭环

目标：理解智能体为什么不能只靠一次模型回答，而要依赖证据、状态、预算和恢复机制。

| 练习 | 对应章节 | 必做产物 |
| --- | --- | --- |
| 反思不是自言自语 | [第 4 章：反思](chapter-04-reflection.md) | 把 Goal evaluator、recap、compact、checkpoint 分别归类为“评估、记忆、恢复、证据”。 |
| Goal 状态机 | [第 11 章：目标设定与监控](chapter-11-goal-setting-monitoring.md) | 画出 `active -> complete/blocked/failed` 的状态转换和触发条件。 |
| 恢复边界 | [第 12 章：异常处理和恢复](chapter-12-exception-handling-recovery.md) | 列出 tool error、provider error、resume invalid、max turns 四类失败如何处理。 |
| 资源收口 | [第 16 章：资源感知优化](chapter-16-resource-aware-optimization.md) | 解释 prompt cache、auto compact、budget closing 三者分别优化什么资源。 |

建议验证：

```bash
go test ./internal/goal ./internal/compact ./internal/session -count=1
rg -n "recordCompact|Evaluate|Checkpoint|Budget" internal docs/agentic_patterns_learning
```

## 阶段 3：并行、多 Agent 与通信

目标：理解为什么多 agent 不是简单并发，而是上下文隔离、权限继承、事件回放和结果汇总。

| 练习 | 对应章节 | 必做产物 |
| --- | --- | --- |
| 并行边界设计 | [第 3 章：并行化](chapter-03-parallelization.md) | 判断一个任务应该直接执行、Task 子任务、background job 还是 scheduler。 |
| Sub-agent 配置加载 | [第 7 章：多智能体协作](chapter-07-multi-agent-collaboration.md) | 画出 agent prompt、tools、MCP、skills、memory 的加载和过滤顺序。 |
| A2A 消息回放 | [第 15 章：智能体间通信](chapter-15-inter-agent-communication-a2a.md) | 解释 task event、nested progress、cancel 三者如何被 UI 或 API 观察。 |
| 人机协同裁决 | [第 13 章：人机协同](chapter-13-human-in-the-loop.md) | 列出 AskUserQuestion、PlanMode、permission prompt 的触发差异。 |

建议验证：

```bash
go test ./internal/agentruntime ./internal/tools -count=1
rg -n "Task|AgentCreate|AskUserQuestion|Permission" internal docs/agentic_patterns_learning
```

## 阶段 4：记忆、知识和学习

目标：理解 memory、RAG、learning adaptation 不是同一件事，它们进入 prompt 的时机、可信度和冲突处理不同。

| 练习 | 对应章节 | 必做产物 |
| --- | --- | --- |
| 记忆加载优先级 | [第 8 章：记忆管理](chapter-08-memory-management.md) | 画出 managed/user/project/local/team/auto memory 的加载链路。 |
| 经验变成长期规则 | [第 9 章：学习和适应](chapter-09-learning-and-adaptation.md) | 说明哪些反馈能进入长期记忆，哪些必须进入测试或文档，哪些不能学习。 |
| RAG 与 Memory 区分 | [第 14 章：知识检索](chapter-14-knowledge-retrieval-rag.md) | 用表格比较 tenant knowledge、code memory、compact summary 的作用边界。 |

建议验证：

```bash
go test ./internal/memory ./internal/server -count=1
rg -n "LoadCode|memory|knowledge|tenant.*skill|context manifest" internal docs
```

## 阶段 5：外部工具协议、安全和观测

目标：理解智能体能调用外部能力之后，必须把权限、沙箱、审计和 trace 做成硬边界。

| 练习 | 对应章节 | 必做产物 |
| --- | --- | --- |
| MCP 工具适配 | [第 10 章：MCP](chapter-10-mcp.md) | 画出 MCP tool adapter 与普通工具的差异，重点标出 callback permission。 |
| 安全裁决顺序 | [第 18 章：安全护栏](chapter-18-guardrails-safety.md) | 复述 `Bypass -> Deny -> AlwaysAsk -> Allow -> DefaultMode` 的权限顺序。 |
| 评估与监控分层 | [第 19 章：评估和监控](chapter-19-evaluation-monitoring.md) | 区分 unit test、golden test、agent eval、live profile、Trace Viewer 的适用范围。 |

建议验证：

```bash
go test ./internal/permissions ./internal/mcp ./internal/telemetry ./internal/agenteval -count=1
rg -n "CheckRequest|MCP|telemetry|Trace Viewer|golden" internal docs
```

## 阶段 6：高级推理、优先级和探索

目标：理解高质量智能体需要可控推理、明确优先级和证据优先的探索流程。

| 练习 | 对应章节 | 必做产物 |
| --- | --- | --- |
| Thinking 与结构化输出 | [第 17 章：推理技术](chapter-17-reasoning-techniques.md) | 解释 provider thinking、PlanMode、JSON schema、Goal evaluator 分别控制哪类推理。 |
| 多层优先级 | [第 20 章：优先级排序](chapter-20-prioritization.md) | 画出 Goal、Todo、Task batch、background、roadmap 的优先级关系。 |
| Read-first 探索 | [第 21 章：探索和发现](chapter-21-exploration-discovery.md) | 用 `LS -> Glob -> Grep -> Read -> LSP -> Web` 写一次真实排查路线。 |

建议验证：

```bash
go test ./internal/query ./internal/tools ./internal/background ./internal/scheduler -count=1
rg -n "Thinking|schedule_strategy|WebSearch|WebFetch|Glob|Grep" internal docs
```

## 最终综合练习

任选一个真实问题，例如“某个工具为什么被拒绝”“某个 API 为什么没有加载 code memory”“某个长任务为什么没有完成”，按下面步骤写一份小报告：

1. 问题描述：输入、期望、实际结果。
2. 相关章节：至少关联 3 个章节。
3. 源码证据：至少引用 3 个源码或测试路径。
4. 机制图：至少 1 张 Mermaid 图，节点使用 `English / 中文` 双语标签。
5. 优先级与冲突：说明谁优先、冲突时如何裁决。
6. 异常与兜底：说明失败后如何恢复，什么时候应该停下来问人。
7. 验证命令：给出实际可运行的命令。
8. 改进建议：说明改哪里、怎么测、风险是什么。
