# 可靠性飞轮交接文档（2026-07-16）

> 用途：跨机器续接。上一会话完成了黄金工作流回归评测（可靠性飞轮第一圈），本文档 = 执行台账 + 下一会话启动提示词。
> 计划原文：[../superpowers/plans/2026-07-15-reliability-flywheel-golden-evals.md](../superpowers/plans/2026-07-15-reliability-flywheel-golden-evals.md)
> 规约：[../pending-fixes/README.md](../pending-fixes/README.md)

## 执行台账（原 .superpowers/sdd/progress.md，该目录 gitignored 不随仓库走）

Task 1: complete (commits ae6ee18..8e805ed, review clean)
  Minor(deferred): metrics_test.go Turns==2 缺少解释注释; budget 测试可加 Metrics 非空断言; env-var ordering 为继承模式(pre-existing)
Task 2: complete (commits 8e805ed..4dd664a, fix + re-review clean)
  Minor(deferred): gitOutput 用 CombinedOutput 混入 stderr(对现用命令无害); withRemoteDivergence=false 测试可补 repo 侧断言
Task 3: complete (commits 4dd664a..1340cbc, review approved)
  ⚠️已解决: 控制器诊断实测 gates=2, 4步+2次gate重试=6 Bash调用, 7轮 — 算术自洽, 重试机制与指标均正确; 实现者报告"0 gates"为误报
  Minor(deferred): bashToolInput 忽略 marshal error(实际不可失败); 测试未覆盖 checkByName 缺失名的行为
Task 4: complete (commit cb5ba43, review approved; 注意 0c3bfca 是并行落的 quota 修复, 非本计划产物)
  Minor(deferred): 测试注释里文档路径被软换行, grep 路径字符串会漏; ExpectContains 覆盖已由控制器确认无问题
Task 5: complete (commits cb5ba43..f8d25c7, review approved)
  Minor(deferred): TestRunGoldenSuite 未清 CLAUDE_CODE_MODEL(姊妹测试不对称); agenteval 层无 JSON 序列化覆盖(CLI 层已覆盖)
Task 6: complete (commits f8d25c7..5117651 含 TODO-053 表格位置修复, review approved)
  控制器裁决: TODO-044→053 编号(todo.md 并行增长)与 help.txt 金标更新(Task 5 配套)均正确
全部 6 任务完成; 待整分支终审。范围内提交: 8e805ed f56f6c5 4dd664a 1340cbc cb5ba43 f8d25c7 07ef35f 5117651 (0c3bfca 为并行 quota 工作, 不在本计划范围)
整分支终审(fable): Ready to merge = Yes. 0 Critical, 0 Important; 8 项延期 Minor 全部裁定不阻塞; 新增 5 项 Minor 建议(rebase-apply 探测、fixture 加 rebase.backend=merge、--suite/--dataset 冲突报错、Report.Environment 记录 suite、marker 常量待 query 解冻后导出)
执行完毕 2026-07-16。计划全部 6 任务 DONE, 黄金套件 2/2 passed。

## 下一会话启动提示词（在新电脑上直接粘贴）

项目是 Go Claude（golang-cc 仓库，Go 1.26，trunk-based 直接提交 main）。上一轮完成了"可靠性飞轮"：黄金工作流回归评测已落地（`eval agents --suite golden`，2/2 通过），git 冲突卡死事故已转为永久回归案例，事故→评测规约在 docs/pending-fixes/README.md。

当前进度：
- 已完成：CaseMetrics 指标 + expect_max_turns 预算、git_commit_push / git_conflict_recovery 两个真实 git 案例、--suite golden CLI、规约文档（TODO-053）。
- 实测基线：gate 摩擦 = 每次 commit+push 多烧 2 轮（git_commit_push 案例 7 轮/2 次 gate preflight；git_conflict_recovery 10 轮）。

⚠️ 更正（2026-07-16 续会）：原"首选任务 = 方案 2（auto-preflight 成功后自动执行原命令）"已**否决，勿再执行**。
- 出处更正：git-conflict-analysis.md **并无"第四节方案 2"**；第四节是"已实施方案"，其 §4.2 恰恰**反对**自动重放。原提示词引用有误。
- 否决理由：自动重放会让 commit/push 在模型未检视 preflight 输出时无条件执行，等于保留 gate 成本却架空其价值（push 还可能重踏"推入已分叉远程"事故）；且"Turns 7→5、GatePreflights 2→0"是黄金案例 scriptedStreamer 依赖 marker 句式回退重试的构造性结果，非真实收益。详见 git-conflict-analysis.md §七（已同步更正）。
- 若仍要降 commit+push 轮次的安全正解：在发起前先做范围/状态核查（提示词/任务契约侧），让 gate 首次即放行、零重试，**不改 gate 授权**。此项未做，留待另立方案评估。

本次续会已完成：整分支终审 5 项新增 Minor（rebase-apply 探测、fixture rebase.backend=merge、--suite/--dataset 冲突报错、Environment 记录 suite、导出 query.GatePreflightMarker 单一来源）+ 8 项延期 Minor 中的 5 项（T1a/T1b/T2b/T4a/T5a），跳过 T1c/T2a/T3a/T3b/T5b（pre-existing / 无害 / 不可能失败的死代码 / 收益低 / CLI 层已覆盖）。黄金套件仍 2/2，go build+vet+test 全绿。本机 go 为 /usr/local/go/bin/go（1.25.0，满足 go.mod 的 1.24.2）；文档中 $HOME/go/go1.26.4/bin/go 是另一台机器的路径。

下一步：实施"安全版降摩擦"方案（软约束线），方案已写好并推送：docs/superpowers/plans/2026-07-16-closure-gate-friction-reduction.md。另有正交的"push 分叉保护"硬规则线（PreToolUse hook），与本方案分属两层、可并行、别混做一次改动（分工见方案 §十）。

---

## 下一会话启动提示词（定稿·实施降摩擦软约束方案，直接粘贴）

> ✅ **已执行完毕（2026-07-16，commit d6c02951）**，勿重复执行。落地记录与实测（GatePreflights 2→0、轮次未降、诚实边界）见方案 §十一。
> 下一步已定稿为两份底稿：真实触发率度量口径（docs/superpowers/plans/2026-07-16-gate-trigger-rate-measurement.md，含 07-10~07-13 真实基线 55 条软规则摩擦）与机制 C 候选方案（docs/superpowers/plans/2026-07-16-closure-gate-evidence-injection.md，决策门=度量结果，拿到真实数据前不实施）。新会话从度量文档 §七 决策门入手。
>
> **续（2026-07-16 当日）**：度量工具已全部落地——被动统计 `scripts/gate-trigger-rate.py`（实测基线触发率 70.5%）+ 主动探针 `eval agents --profile golden-probe`（真实模型跑黄金 git fixture）。首测两模型（deepseek-v4-flash / glm-5.1）均**部分遵从**：工作流全部完成但各吃过 1 次 gate。曾误判的「glm-5.1 网关 404」已排除——根因是执行 shell 的 ANTHROPIC_BASE_URL 遮蔽 settings.env（跑探针用 `env -u ANTHROPIC_BASE_URL` 或显式 `--provider`）。**下周执行清单（含唯一前置操作：重启 18087 server）见度量文档 §十，新会话照 §十 逐条执行即可。**

```
项目：Go Claude（golang-cc 仓库，github.com:konglong87/golang-cc，Go 1.24.2，trunk-based 直接提交 main）。
本机 go 在 /usr/local/go/bin/go（1.25.0，满足 go.mod）；文档里 $HOME/go/go1.26.4/bin/go 是另一台机器的路径，按本机实际 go 执行。
注意：该仓库有并行会话用同一 git 身份 konglong87 <developer@example.com> 在提交并 push；动手前先 git fetch，push 前先 git rev-list --left-right --count @{u}...HEAD 查分叉，有分叉先 pull --rebase，绝不盲推/force-push。commit 消息禁止任何 AI 署名。

任务：实施「Closure Gate 降摩擦（安全版·软约束）」。方案已写好，先读再动手：
docs/superpowers/plans/2026-07-16-closure-gate-friction-reduction.md

红线（勿重蹈覆辙）：
- 「方案 2」（gate 拦截后系统自动重放原命令）已否决，勿走——会让 commit/push 在模型未检视 preflight 输出时无条件执行，架空 gate；push 还可能重踏「推入已分叉远程」事故。依据见 docs/pending-fixes/git-conflict-and-transcript-locating/git-conflict-analysis.md §4.2/§七。
- 本方案只做「提示/契约侧前置核查」，让 gate 首次即放行、零重试。【不改 internal/query/closure_gate.go 的 gate 授权逻辑】；destructive_shared_state / external_action_authorization 硬拦截原样保留。
- 「push 分叉保护」是另一条正交的硬规则线（PreToolUse hook），不在本方案范围，别混做（分工见方案 §十）。

改动点：
- internal/query/query.go 系统提示模板：为共享状态任务补显式序列指令——发起 git commit 前先跑 git status --short && git diff --name-status && git diff --cached --name-status 确认暂存范围；发起 git push 前先跑 git status --short --branch && git rev-parse HEAD 及只读 upstream 检查（git rev-parse @{u} / git branch -vv / git ls-remote）确认远程未分叉。措辞与 gate 期望证据（preCommitScopeVerified / prePushGitVerified）对齐。
- internal/agenteval/golden.go：scriptedStreamer 步骤表体现「先核查 → 再 commit/push」的正确工作流；据实收紧 expect_max_turns，期望 Metrics.GatePreflights 降到 0。
- 不改 closure_gate.go 授权判定。

先确认开放问题（方案 §九）再动手：
1. query.go:4482「编辑后核查」在真实模型上是否已常态满足 commit gate？若已满足，commit 侧边际收益主要落在脚本对齐 + push 侧 upstream 指令补全。
2. shared_state_git_tag（普通 tag 创建，可逆）是否一并降级（删/移 tag 已由 destructive 分支硬拦，此处仅指创建）。

验收：
- 黄金套件仍 2/2：/usr/local/go/bin/go run ./cmd/golang-cc eval agents --suite golden
- 回归网：/usr/local/go/bin/go test ./internal/agenteval ./internal/query ./internal/cli -count=1
- destructive gate 回归测试全绿（证明硬边界未动）。
- 【诚实边界·必须 log】黄金案例是脚本，改脚本让它「先核查」再对齐轮次预算，只证明「若模型遵从则摩擦消失」，不证明真实模型一定遵从；真实收益要在 live-profile 或真实 transcript 上看 gate 触发率是否下降。别把脚本轮次下降当真实可靠性证据（这正是方案 2 的坑）。

工作流：先细化实施步骤 → 逐步实施 → 每步用黄金套件 + go test 验证 → 完成据实汇报（含诚实边界）。
```
