# Agent Full Loop Closure 全场景闭环技术方案

更新时间：2026-07-08

状态：`PHASE_2_PLUS_IMPLEMENTED`

## 2026-07-08 P3 Closure Gate Acceptance 记录

本轮新增 deterministic real CLI acceptance，覆盖 CompletionGate 在真实流式 CLI 链路中的行为，而不只依赖 fake-model unit tests：

- 新增 `scripts/closure-gate-acceptance.sh`。
- 脚本启动本地 OpenAI-compatible stub provider、隔离 fixture git repo 和临时 config。
- 场景串联两个 gate：
  1. stub 先输出 premature final：声称已读 `README.md` 并且 checks passed。
  2. runtime 必须触发 `Read Scope Gate`，不把 premature candidate 作为 accepted final。
  3. stub 调用 `Read README.md` 后再次声称 `Checks passed`。
  4. runtime 必须触发 `Final Claim Gate`。
  5. stub 调用 `Bash(git diff --check && printf CLOSURE_GATE_DIFF_CHECK_OK)`。
  6. final 只有在 read evidence 和 verification evidence 都出现后才接受。
- acceptance 还检查 CLI 输出中不能出现 `CLOSURE_GATE_PREMATURE`，防止被 gate 拦截的 candidate final 在流式输出中泄漏给用户。
- 为支持该验收，`Session.run` 改为按 turn 缓冲 assistant text：tool-use 轮文本在工具执行前 flush；final 轮只有通过 CompletionGate 后才 flush。`RunResult.Response`、accepted transcript 和真实 CLI 输出现在都不会包含被 gate 拦截的 premature final。

新增验证：

```bash
scripts/closure-gate-acceptance.sh --force
go test ./internal/query -run 'TestCompletionGateBlockedCandidateIsNotRecordedAsAcceptedAssistant|TestReadOnlyClosure|TestFinalClaimGate|TestQueryGoldenToolTranscript' -count=1
```

边界：

- 该 acceptance 是 deterministic 小矩阵的第一条链路，覆盖 read-scope + final-claim 串联；还没有覆盖 commit/push/tag/external 的真实 CLI stub 场景。
- WebUI/TUI 专用 closure event 可视化仍未实现。

## 2026-07-08 P1 Shared-State Workflow 加固记录

本轮在 P0 ShellIntent 分类层之上继续补齐 shared-state operation 的最小闭环，不自动执行完整 release/deploy 流程，先把 commit / push / tag 的高风险状态声明收紧为可验证事实：

- `TaskContract` 增加 `SharedStateOperations`，对 `commit`、`push`、`tag`、`release/publish`、`deploy` 形成内部表达，供 transcript / trace 后续观察。
- `git push` 前置 gate 从“branch + HEAD”提升为“branch + HEAD + remote/upstream evidence”。允许的只读证据包括 `git rev-parse @{u}`、`git branch -vv`、`git remote -v`、`git ls-remote`。
- `git tag` mutation 前置 gate 要求 branch/object evidence 之外，还必须看到 existing tag-state evidence，例如 `git tag --list`、`git show-ref --tags`、`git rev-parse refs/tags/<tag>`、`git ls-remote --tags`。
- Final Claim Gate 增加 post-commit verification：声称 committed 时，成功 `git commit` 后还必须看到 `git status --short` + `git log -1` 或 `git rev-parse HEAD`。
- Final Claim Gate 增加 post-tag verification：声称 created/updated tag 时，成功 mutating `git tag` 后必须看到 HEAD 与 tag object 的只读验证，例如 `git rev-parse HEAD && git rev-parse refs/tags/<tag>`。

新增 targeted tests：

```bash
go test ./internal/query -run 'TestPushClaimGateRequiresPostPushVerification|TestCommitClaimGateRequiresPostCommitVerification|TestTagMutationRequiresTagStatePrecheck|TestTagClaimGateRequiresPostTagVerification|TestPreCommitScopeGateAllowsExistingStagedScopeAfterVerification' -count=1
go test ./internal/query -run 'TestPostActionDeltaGate|TestPreCommitScopeGate|TestPushClaimGate|TestExternalActionGate|TestClosureEvents|TestFinalClaimGate|TestReadOnlyClosure|TestTrivialAnswer|TestCompletionGateBlockedCandidateIsNotRecordedAsAcceptedAssistant|TestReadOnlyGitTagList|TestDestructiveGitGate|TestShellIntent|TestUnknownRiskyBash|TestCommitClaimGate|TestTag' -count=1
```

边界：

- P1 仍不自动移动、删除或覆盖远端 tag；这类 destructive shared-state operation 仍要求明确授权，并且还要满足 shared-state precheck。
- Release/deploy 的完整 preview -> execute -> readback workflow 仍未产品化；当前只通过 External Action Gate 和 ShellIntent dry-run/preview 做保守约束。
- Final answer 的结构化拆分仍主要靠 gate 阻止错误声明，尚未引入强制 final response schema。

## 2026-07-08 P0 ShellIntent 加固记录

本轮把 Phase 2+ 中分散的 Bash / Git / 外部动作判断收敛为 `ShellIntent` 分类层，仍限定在 `internal/query`，目标是降低误判和为后续 P1 shared-state workflow 铺路：

- 新增 `internal/query/shell_intent.go`，分类输出 `readonly`、`workspace_write`、`verification`、`git_commit`、`git_push`、`git_tag_read`、`git_tag_mutate`、`git_destructive_shared_state`、`external_write`、`external_write_dry_run_or_preview`、`unknown_risky`。
- `preToolClosureGate`、read evidence 收集、action record 分类改为通过 `ShellIntent` 判断，减少散落字符串分支。
- `npm publish --dry-run`、preview/plan/check 类命令归为 dry-run/preview，不触发未授权 external write 阻断。
- `./script.sh`、`bash script.sh`、`sh script.sh` 等 wrapper 归为 `unknown_risky`：不直接阻断执行，但如果后续 final 声称完成，会走 Post-Action Delta Gate，要求补 `git status` / `git diff` / 语义验证。
- 保留底层 git tag / destructive git helper 作为 `ShellIntent` 的解析部件，并用 table test 固定 `git tag --list 'v*'`、`git tag -a`、`git tag -f`、`git push --force-with-lease` 等边界。

新增 targeted tests：

```bash
go test ./internal/query -run 'TestShellIntentClassifiesHighRiskBashCommands|TestUnknownRiskyBashRequiresPostActionVerificationBeforeCompletion|TestPostActionDeltaGate|TestDestructiveGitGate|TestReadOnlyGitTagList' -count=1
```

边界：

- `ShellIntent` 是保守规则分类器，不解析任意 shell AST、变量展开、alias、subshell 或脚本内部副作用。
- `unknown_risky` 的策略是“执行后必须补证据才能声称完成”，不是执行前全面禁止。
- 完整 force push / tag move / release workflow 仍属于 P1，不在本轮自动放行。

## 2026-07-08 Phase 2+ 完整化补充

在 Phase 2+ 最小程序化闭环基础上，本轮继续补齐此前实现边界中 ROI 最高的几个缺口：

- Read Scope Gate 现在同时识别带扩展名的文件路径和目录型路径，例如 `internal/query`、`docs/prompt_logic`。用户要求读取/总结目录时，如果 final 先声称已检查但本轮没有 `Read` / `Grep` / `Glob` / `LS` / read-only `Bash` evidence，会被 CompletionGate 拦截。
- Git shared-state gate 不再把只读 `git tag --list` / `git tag -l` 当作 tag mutation；真正创建/移动 tag、`git push` 等共享状态操作仍要求前置 branch/object evidence。
- Pre-commit scope gate 支持“提交用户已经 staged 的历史改动”的保守闭环：没有本轮 delta 时，只要当前会话已有 `git status --short` + `git diff --cached/--staged` 的 staged scope evidence，就允许 commit；有本轮写入或 `git add` 时仍要求最新 delta/stage 后重新验证 scope。
- 新增 Destructive Shared-State Gate：未获用户明确授权时，阻断 `git push --force` / `--force-with-lease`、远端 tag 删除、`git tag -f` 等 force/move/delete 类共享状态操作。授权词仍需明确指向 force/delete/move/overwrite tag，而不是普通 push。
- Final Claim Gate 增加 tag creation/update claim 检查：声称创建或更新 tag 时，必须有成功的 mutating `git tag` evidence。

新增 targeted tests：

```bash
go test ./internal/query -run 'TestReadOnlyClosureBlocksDirectorySummaryWithoutReadEvidence|TestReadOnlyGitTagListIsNotBlockedAsSharedStateMutation|TestDestructiveGitGateBlocksUnauthorizedForcePush|TestPreCommitScopeGateAllowsExistingStagedScopeAfterVerification' -count=1
go test ./internal/query -run 'TestPostActionDeltaGate|TestPreCommitScopeGate|TestPushClaimGate|TestExternalActionGate|TestClosureEvents|TestFinalClaimGate|TestReadOnlyClosure|TestTrivialAnswer|TestCompletionGateBlockedCandidateIsNotRecordedAsAcceptedAssistant|TestReadOnlyGitTagList|TestDestructiveGitGate|TestPreCommitScopeGateAllowsExistingStagedScope' -count=1
```

剩余边界：

- Bash 副作用分类仍是 best-effort，不声称能完整解析任意 shell、脚本或 wrapper 的副作用；复杂命令仍依赖后验 `git status` / `git diff` / 目标系统只读查询闭环。
- Force push、移动/删除远端 tag 的具体执行工作流仍不会自动放行；即使用户授权，也仍需要 shared-state precheck 和执行后验证。
- Phase 6 结构化事件已经写入 transcript，但 WebUI 专用展示面板仍未实现。

## 2026-07-08 Phase 2+ 实施记录

本轮在 Phase 1 基础上继续完成 Phase 2+ 的最小程序化闭环，仍限定在 `internal/query`，不做跨 UI 的大重构：

- Phase 2：`ToolTrace` 记录本轮 `FileChange`，CompletionGate 从工具历史推导 DeltaLedger；`Edit` / `Write` / `MultiEdit` / write-like `Bash` 后，如果 final 声称 fixed/completed/done，必须在最新 delta 后看到 scope、metadata、semantic checks。
- Phase 3：Bash 执行前增加 `block_tool` gate；未在最新写入/暂存后完成 scope verification 时阻断 `git commit`；未做 branch/object precheck 时阻断 `git push` / `git tag`。
- Phase 4：扩展 Final Claim Gate，覆盖 searched/inspected、full repo/all files、post-push verification、external deployed/published/sent 等高置信声明。
- Phase 5：新增保守 External Action Gate；当模型尝试通过 Bash 执行 deploy/publish/send/release 类外部副作用，而用户当前请求未显式授权时，工具调用会被阻断并返回 tool_result 错误。
- Phase 6：新增 transcript 结构化事件：`task_contract`、`action_record`、`evidence_record`、`delta_record`、`completion_gate`。这些事件供 trace/inspection 使用，并已加入 resume sanitizer 的 metadata 白名单，不进入模型上下文、不打断 `tool_call` / `tool_result` 配对。

新增/更新 tests 覆盖：

```bash
go test ./internal/query -run 'TestPostActionDeltaGate|TestPreCommitScopeGate|TestPushClaimGate|TestExternalActionGate|TestClosureEvents|TestFinalClaimGate|TestReadOnlyClosure|TestTrivialAnswer' -count=1
UPDATE_GOLDEN=1 go test ./internal/query -run TestQueryGoldenToolTranscript -count=1
go test ./internal/query -count=1
```

实现边界：

- Bash 分类仍是 best-effort，不声称能完整解析所有 shell 副作用；复杂 shell 仍依赖后验 `git status` / `git diff` 证据闭环。
- Pre-commit gate 当前要求 current-session scope evidence；提交既有 staged 改动时必须先观察当前 staged scope。
- Push/tag gate 当前覆盖 precheck 与 final post-push claim verification；force-push、移动远端 tag、删除远端 tag 已有未授权阻断，但不会自动执行完整发布工作流。
- Phase 6 已记录结构化事件，但还没有做 WebUI 专用展示面板；现有 trace 可读这些事件。

## 2026-07-08 Phase 1 实施记录

本轮已完成 Phase 1 的最小高 ROI 落地，范围限定在 `internal/query`：

- 新增轻量 `TaskContract` / Closure Level 内部表达，覆盖 L0 trivial、L1 no-tool、L2 read-only、L3 investigation/audit、L4 local change、L5 shared-state change。
- 新增 `GateSeverity` 内部表达，第一阶段实际使用 `allow` 与 `block_continue`。
- 新增最小 CompletionGate，复用现有 candidate final nudge 路径，在 final answer 被接受前检查高置信声明。
- 新增 Read Scope Gate：用户指定读取文件并总结/解释/对比时，final 前必须看到 `Read` / `Grep` / `Glob` / `LS` / read-only `Bash` evidence；读错文件会拦截；只读片段却声称全文会拦截。
- 新增 Final Claim Gate：先覆盖高置信 `tests passed`、`committed`、`pushed`、本地 read/search、`no issues found` 等声明，缺少对应 evidence 时注入 blocking reminder 继续下一轮。
- 调整 run loop：无 tool 的 candidate final 先经过 gate；被 gate 拦截时从 `RunResult.Response` 中移除，且不作为 accepted assistant message 写入 transcript。
- L0/L1 简单任务保持轻闭环，不注入 repo audit / repair / post-edit / git strategy，不要求工具。

已补 targeted tests 覆盖：

```bash
go test ./internal/query -run 'TestTrivialAnswer|TestReadOnlyClosure|TestFinalClaimGate|TestCompletionVerificationNudgeRequiresRequestedCommands|TestRuntimeTaskClassifierMatrix|TestSessionInjectsSpecificAuditStrategies|TestSessionDoesNotInject|TestRepairStrategy' -count=1
go test ./internal/query -count=1
go test ./... -count=1
git diff --check
```

实现边界：

- 本轮没有实现 Phase 2 的统一 DeltaLedger / Post-Action Delta Gate，只保留既有 post-edit reminder 行为。
- 本轮没有实现 Phase 3 的 pre-commit scope hard gate，也没有阻断 `git commit` / `git push` 工具调用；只在 final claim 层阻止无证据的 committed / pushed 声明。
- Bash 只做 best-effort read-only evidence 识别，不声称完整 shell 语义分析。
- 还没有把 ledger 写入 session trace 的结构化 `task_contract` / `evidence_record` 事件；当前主要通过 internal contract + existing tool traces 完成 Phase 1 gate。

## 背景与结论

当前 go-claude 已经有多条局部闭环能力：

- `repo_health_audit` / `release_readiness_audit` / `entrypoint_audit` 会注入审计策略、证据清单和 ROI 排序合同。
- `repair_*` 任务会注入修复安全策略。
- `Edit` / `MultiEdit` / `Write` / write-like `Bash` 后会追加 post-edit verification reminder。
- 用户显式要求测试命令时，final answer 前有 completion verification nudge。
- Task / Agent 结果有 `capability_loop`，可携带 evidence、unknowns、verification、risks、next_action。

这些能力有效，但不是“全场景闭环”。它们仍然是分散的策略卡和 reminder：

- read-only 总结类任务没有统一的“回答是否覆盖用户指令”验证。
- 文件读取、搜索、诊断、编辑、提交、推送、外部动作之间没有统一的 action ledger。
- final answer 没有通用 evidence-to-claim gate，模型仍可能把未验证内容说成事实。
- commit/push 等关键动作目前主要靠提示约束，不是硬性程序闸门。

因此，全场景闭环不能继续靠堆 prompt。需要新增一个轻量、可测试、可渐进落地的 **Task Closure Engine**：

```text
User Request
  -> Task Contract
  -> Action Ledger
  -> Evidence Ledger
  -> Verification Planner
  -> Completion Gate
  -> Final Answer Contract
```

核心目标不是让 agent 对所有任务都跑重型流程，而是让每类任务都有合适粒度的闭环：简单任务轻闭环，高风险任务硬闭环。

## 全场景闭环定义

一个任务只有满足以下条件，才算闭环：

1. **Intent Closure**
   agent 明确当前用户到底要求什么、不要求什么、输出格式是什么、是否允许修改或外部动作。

2. **Scope Closure**
   agent 的工具动作覆盖了必要范围，也没有越过用户授权范围。

3. **Evidence Closure**
   最终回答中的关键事实都能追溯到工具结果、文件内容、命令输出、用户输入或明确假设。

4. **Action Closure**
   如果执行了写入、提交、推送、发布、删除、发送消息等动作，必须验证实际状态与意图一致。

5. **Verification Closure**
   根据任务风险执行最小充分验证；无法验证时必须明确边界。

6. **Answer Closure**
   final answer 只能声明已完成且已验证的内容；未做、失败、跳过、需要用户确认的内容必须拆开说明。

## 非目标

- 不复刻原版 Claude Code 私有协议。
- 不把所有任务都变成重型审计。
- 不强制每个 read-only 问答都跑 Bash 或全仓库扫描。
- 不允许为了闭环自动扩大权限、force push、删除远端 tag、发送外部消息或修改共享状态。
- 不用一次大重构替换现有 prompt assembly；必须渐进接入。

## 设计原则

### 1. 轻任务轻闭环，高风险任务硬闭环

全场景闭环不等于全场景重流程。闭环强度必须跟任务风险和可验证性匹配：

| Closure Level | 典型任务 | 需要工具 | 闭环要求 | 禁止事项 |
| --- | --- | --- | --- | --- |
| L0 `trivial_answer` | `1+1=?`、简单翻译、简单概念解释 | 否 | 直接回答；答案自洽；不伪称读取文件或执行验证 | 不要为了闭环调用工具或注入重型 reminder |
| L1 `no_tool_answer` | 一般知识解释、代码概念说明、无需当前仓库事实的问题 | 否，除非用户要求最新/本地事实 | 回答基于模型知识或明确说明假设；不能声称检查了本地状态 | 不要编造本地证据 |
| L2 `read_only_scoped` | 查看文件并总结、解释函数、对比几个文件 | 是，读取指定对象 | 确认读取范围、输出覆盖用户维度、部分读取要披露 | 不能没读指定对象就总结 |
| L3 `investigation_audit` | 排查原因、项目审计、找风险 | 是，搜索/聚合证据 | 记录搜索范围、证据链、未知项和 ROI/结论依据 | 不能把未搜索范围说成“没有问题” |
| L4 `local_change` | 修改文件、修 bug、实现功能 | 是，写入和验证 | diff scope、metadata、targeted tests 或替代验证 | 不能跳过写后验证就宣称完成 |
| L5 `shared_state_change` | commit/push/tag/release/deploy/外部发送 | 是，高风险 | 操作前授权与 scope gate，操作后状态验证 | 不能在未确认范围/远端状态时宣称完成 |

因此，用户问 `1+1=?` 时，完整闭环就是回答 `2`，不需要 ledger、Bash、git、测试或额外自检；这类任务如果触发重型流程，反而是不合理的。全场景闭环的正确含义是：每个场景都有合适强度的闭环，而不是每个场景都走同一套重流程。

例如“读取 README 并总结”不应强迫跑 git status，但必须确保：

- 真的读取了 README。
- 回答只基于已读取内容。
- 总结覆盖用户指定维度。
- 如果只读了部分内容，要说明范围。

而“修复 P0 并提交推送”必须硬性要求：

- diff scope 检查。
- metadata/mode 检查。
- targeted tests。
- `git status --short --branch`。
- commit 与 push 状态拆分。

### 2. 证据链优先于模型自信

模型不能仅凭“我认为完成了”宣称闭环。runtime 应维护结构化 ledger：

- 做过什么工具动作。
- 得到了什么证据。
- 改了哪些文件。
- 跑了哪些验证，结果是什么。
- 还有哪些 open questions 或 remaining risks。

### 3. 硬闸门只放在不可逆或高风险边界

所有任务都做硬闸门会降低效率，也容易误伤普通问答。硬闸门优先覆盖：

- final answer 声称完成但缺少必要验证。
- commit 前 diff scope 未确认。
- push/tag/release 前 remote/local 对象关系未确认。
- write-like Bash 后未识别变更范围。
- 外部副作用动作前缺少用户授权。

### 4. 闭环合同要可测试

每个 task type 都要能通过 unit/golden/fake model 测试证明：

- 正确生成 task contract。
- 正确记录 evidence/action。
- 缺少验证时 final gate 会阻断或提醒。
- 普通任务不会被高风险流程误触发。

### 5. 闭环必须服务用户目标，而不是服务流程本身

闭环系统的工程目标是降低假完成、越权修改、漏验证和错误声明，不是增加仪式感。所有 gate 都必须满足下面的 ROI 判断：

- 对 L0/L1 简单任务，闭环成本必须接近零。
- 对 L2 read-only 任务，只验证用户指定范围和回答声明，不扩展成仓库审计。
- 对 L4/L5 写入或共享状态任务，宁可多做一次 scope/status 验证，也不能把未验证状态说成完成。
- 如果 gate 不能提供明确风险降低，默认不进入 P0。

### 6. Gate 分级：提醒、阻断、拒绝要分开

不同闭环失败不能统一处理：

| Gate Result | 使用场景 | 行为 |
| --- | --- | --- |
| `allow` | 所需 evidence/check 已满足，或 L0/L1 无需工具 | 允许 final answer |
| `warn` | 可接受的不完整边界，例如用户只要求快速建议且无副作用 | final answer 必须披露未验证边界 |
| `block_continue` | 缺少可补齐的证据或验证，例如没读指定文件、没跑用户要求测试 | 注入 reminder，继续下一轮工具调用 |
| `block_tool` | 即将执行高风险动作但缺少前置 gate，例如未确认 scope 就 commit | 阻断该工具调用，返回可执行 next checks |
| `refuse` | 用户要求越权、危险或安全策略禁止动作 | 不执行动作，说明原因和安全替代方案 |

第一版不要滥用 `refuse`。大多数闭环失败应是 `block_continue` 或 `block_tool`，让 agent 有机会补证据完成任务。

## 目标架构

```text
internal/query
  TaskClassifier
    -> TaskContract
  ToolRunner
    -> ActionLedger
    -> EvidenceLedger
    -> DeltaLedger
  RuntimeStatus
    -> RequiredNextChecks
    -> OpenQuestions
  CompletionGate
    -> ClaimVerifier
    -> FinalAnswerContract
```

### TaskContract

`TaskContract` 是当前 turn 的闭环合同，由用户 prompt、conversation state、已有工具结果、permission mode 和 task classifier 共同生成。

建议结构：

```go
type TaskContract struct {
    Type              TaskType
    Intent            string
    Scope             ScopeContract
    ClosureLevel      ClosureLevel
    AllowedActions    []ActionKind
    RequiredEvidence  []EvidenceRequirement
    RequiredChecks    []VerificationRequirement
    OutputContract    OutputContract
    CompletionClaims  []ClaimKind
    RiskLevel         RiskLevel
    HardGates         []GateKind
}
```

关键点：

- `TaskContract` 不要求模型输出；它是 runtime 内部状态。
- 简单任务也有 contract，但 required checks 很轻。
- L0/L1 任务的 contract 可以是零工具、零 ledger 的轻闭环，只约束 final answer 不伪称外部证据。
- 多轮任务中 contract 可以更新，但不能丢掉未完成 verification。

### ActionLedger

记录工具动作和其副作用类别：

```go
type ActionRecord struct {
    ToolName       string
    InputSummary   string
    ActionKind     ActionKind
    Paths          []string
    ExternalTarget string
    StartedAt      time.Time
    FinishedAt     time.Time
    Success        bool
    ErrorSummary   string
}
```

ActionKind 建议：

| ActionKind | 示例 | 风险 |
| --- | --- | --- |
| `read_file` | Read, NotebookRead | 低 |
| `search_local` | Grep, Glob, LS | 低 |
| `bash_readonly` | git status, rg, find, wc | 低到中 |
| `write_file` | Edit, MultiEdit, Write, NotebookEdit | 中到高 |
| `bash_write` | sed -i, mv, cp, chmod, generator | 高 |
| `test_verify` | go test, npm test, pytest | 中 |
| `git_commit` | git commit | 高 |
| `git_push` | git push, tag push | 高 |
| `external_read` | WebFetch, WebSearch | 中 |
| `external_write` | publish, send message, deploy | 极高 |
| `agent_delegate` | Task/Agent | 中 |

### EvidenceLedger

记录 final answer 可引用的事实来源：

```go
type EvidenceRecord struct {
    ID          string
    SourceKind  EvidenceSourceKind
    ToolCallID  string
    Path        string
    Range       string
    Complete    bool
    Truncated   bool
    Digest      string
    Command     string
    Summary     string
    Claims      []string
    Confidence  EvidenceConfidence
    Freshness    time.Time
}
```

EvidenceSourceKind：

- `user_input`
- `file_content`
- `command_output`
- `git_state`
- `test_result`
- `tool_error`
- `subagent_result`
- `external_source`
- `assumption`

关键规则：

- final answer 中的强声明必须能映射到 evidence。
- 没有 evidence 的内容只能作为建议、假设或未验证边界。
- 大 tool result 被预算压缩时，必须保留 evidence summary，而不是只保留尾部文本。
- read-only 证据必须能区分“完整读取”和“部分读取”。如果 `Complete=false` 或 `Truncated=true`，final answer 不能声称已检查全文、整个目录或全仓库，除非另有等价证据。

### DeltaLedger

专门记录变更：

```go
type DeltaRecord struct {
    Path          string
    BeforeExists  bool
    AfterExists   bool
    BeforeMode    os.FileMode
    AfterMode     os.FileMode
    ModeChanged   bool
    ContentChanged bool
    ChangeKind    ChangeKind
    Verified      bool
    Verification  []string
}
```

现有 `tools.FileChange` 已经有 before/after content 和 mode 字段，可作为第一阶段数据源。

### VerificationPlanner

根据 `TaskContract + ActionLedger + DeltaLedger` 生成最小充分验证要求。

例子：

| 场景 | Required checks |
| --- | --- |
| 读单文件总结 | 文件已读；回答覆盖用户指定维度；如内容被截断需披露 |
| 多文件对比 | 所有指定文件已读；输出逐项对比；缺失文件需说明 |
| repo audit | 仓库事实表；claim verification；ROI ranking |
| 修复代码 | diff scope；targeted tests；失败测试恢复 |
| 修改 hook/script | diff summary；mode/executable；syntax/dry-run |
| 修改 package metadata | metadata/docs/source-of-truth consistency |
| commit | git status；diff scope；verification passed；commit only scoped files |
| push/tag/release | branch state；local/remote object relation；no force unless confirmed |
| 外部发布/发送 | explicit user authorization；preview；post-action confirmation |

### CompletionGate

`CompletionGate` 在 assistant final answer 前运行。它不是替代模型，而是判断是否需要插入 blocking reminder 或限制 final claim。

输入：

- task contract
- tool call history
- action/evidence/delta ledger
- candidate final text

输出：

```go
type CompletionGateResult struct {
    AllowFinal     bool
    Severity       GateSeverity
    BlockingReason []string
    Reminder       string
    RequiredNextActions []string
}
```

第一阶段可以继续沿用现有 nudge 模式：

```text
candidate final claims completion
  -> missing required evidence/check
  -> append system-reminder
  -> continue another turn with tools enabled
```

后续再把部分高风险场景升级为硬阻断。

### Final 输出拦截点

CompletionGate 必须在用户可见 final answer 之前生效。正确实现顺序是：

```text
assistant produces candidate final text
  -> buffer candidate text internally
  -> run CompletionGate
  -> if allow/warn: emit final answer, with required boundary text when warn
  -> if block_continue: do not emit candidate final as completed answer; inject reminder and continue
  -> if refuse: emit refusal/boundary answer only
```

不能先把“已完成/测试通过/已推送”等 premature final 流式输出给用户，再在下一轮补救。否则用户已经看到了错误闭环声明，gate 只能变成事后纠错。第一阶段如果底层 streaming 暂时无法完全 buffer，也必须至少保证最终 `RunResult.Response` 和 transcript 中的 accepted final answer 不包含被 gate 判定为未验证的完成声明。

### Claim 抽取原则

第一版 `ClaimExtractor` 只做高置信、低误伤规则，不做完整自然语言理解：

- completion claims: `done`、`fixed`、`completed`、`已完成`、`已修复`。
- verification claims: `tests passed`、`验证通过`、`go test passed`。
- git claims: `committed`、`pushed`、`tag pushed`、`已提交`、`已推送`。
- read/search claims: `read`、`inspected`、`searched`、`全文`、`全仓库`、`没有发现`。
- external claims: `deployed`、`published`、`sent`、`已发布`、`已部署`、`已发送`。

对低置信表达不要阻断；要求模型在 final answer 中自然拆分 `Verified` / `Not verified` / `Boundary`，由明确 claim 触发 gate。

## Task Type 与闭环合同

### 1. `trivial_answer` / `no_tool_answer`

典型 prompt：

- `1+1=?`
- “把这句话翻译成英文”
- “解释一下什么是 goroutine”

闭环要求：

- 不需要工具、不需要 git、不需要 ledger。
- 直接回答用户问题，保证答案和问题匹配。
- 不能声称“我看了文件 / 我运行了测试 / 当前仓库状态是...”。
- 如果问题涉及最新事实、本地仓库、当前运行状态或高风险建议，则升级到对应 task type，不继续留在 L0/L1。

验收：

- `1+1=?` 不注入 read/audit/repair/git strategy，不调用工具，直接答 `2`。
- 普通概念解释不触发 repo audit。
- final answer 中若声称读取了本地文件但没有对应 evidence，应被 Final Claim Gate 提醒。

### 2. `clarification_needed`

典型 prompt：

- “帮我处理一下那个问题”
- “发给他”
- “部署一下”

闭环要求：

- 当目标对象、环境、账号、文件、外部系统或破坏性动作不明确时，必须先问清楚。
- 可以做只读澄清性检查，但不能执行写入、提交、推送、发布、发送消息等副作用动作。
- final answer 的闭环是“已明确提出阻塞问题”，而不是假装完成。

验收：

- 模糊外部动作不执行。
- 模糊文件修改不猜文件。
- 用户补充目标后，contract 更新到具体 task type。

### 3. `blocked_or_refusal`

典型 prompt：

- 缺少权限、凭据、网络、外部系统访问。
- 用户要求执行不允许或高风险但未授权的动作。
- 安全策略要求拒绝的内容。

闭环要求：

- 明确 blocked/refused 的具体原因。
- 说明已完成的只读检查和未完成边界。
- 如果存在安全替代方案，给出可执行下一步。
- 不能把 blocked 状态说成完成。

验收：

- 无凭据时不能声称部署成功。
- 权限不足时必须说明缺少什么。
- 拒绝类任务不应继续调用危险工具。

### 4. `read_answer`

典型 prompt：

- “查看这个文件并总结”
- “解释这个函数”
- “这个配置是什么意思”

闭环要求：

- 必须读取用户指定文件/范围。
- 如果文件较大且只读了片段，回答必须说明片段边界。
- final answer 不能声称检查了未读取的文件或全仓库。
- 输出必须覆盖用户要求的维度，例如“总结”“指出风险”“解释原因”。

验收：

- fake model 只读错文件后 final claim 被 reminder 纠正。
- 读取 README 总结不会触发 repo audit 或 post-edit checks。
- 大文件部分读取时，final answer 若说“全文”应被提醒披露范围。

### 5. `search_investigation`

典型 prompt：

- “找一下 X 在哪里实现”
- “排查为什么 Y 发生”
- “这个错误从哪里来的”

闭环要求：

- 必须记录搜索路径和关键证据。
- 如果结论依赖多个文件，final answer 要区分直接证据和推断。
- 如果没有找到，必须说明搜索过的范围，不能说“仓库没有”除非搜索范围足够。

验收：

- 只 grep 一个目录却声称全仓库没有，触发 completion reminder。
- 多证据链诊断 final answer 包含 evidence references 或路径。

### 6. `repo_health_audit`

复用现有 `agent_roi_audit_routing_plan.md`：

- 仓库事实表。
- Claim Verification Loop。
- ROI Ranking Contract。

新增全场景要求：

- audit final answer 中每个 P0/P1 finding 必须有 evidence record。
- 如果某个 checklist 因权限/时间未覆盖，必须列为 boundary。

### 7. `repair_change`

复用现有 `agent_repair_safety_closure_plan.md`：

- mode preservation。
- post-edit verification。
- release closure。

新增全场景要求：

- commit 前必须经过 `pre_commit_scope_gate`。
- final answer 不能把 reminder 中未完成的 check 说成完成。
- 如果用户要求“修复并提交”，提交状态、分支状态、push 状态必须拆开。

### 8. `feature_change`

典型 prompt：

- “实现功能 X”
- “新增 API”
- “增加 UI 控件”

闭环要求：

- 修改范围与需求对应。
- 有最小 targeted tests 或说明无法自动验证。
- API/schema/UI 变更要检查文档、生成文件、兼容性。
- final answer 包含 changed + verified + boundary。

硬闸门：

- 涉及 API、数据库、权限、SSE、移动端接口时，必须按 AGENTS.md 的对应验证要求生成 required checks。

### 9. `test_debug`

典型 prompt：

- “修复这个测试失败”
- “让 go test 通过”

闭环要求：

- 失败输出是 source of truth。
- 修复后必须重跑同一个失败测试。
- 不能通过删测试、改弱断言、跳过测试来伪造闭环，除非用户明确要求。

现有 `appendVerificationFailureRecoveryReminder` 可升级为该 task type 的默认策略。

### 10. `git_operation`

典型 prompt：

- “提交并推送”
- “创建 tag”
- “发版”

闭环要求：

- 操作前：`git status --short --branch`。
- commit 前：diff scope 和 staged scope 必须符合用户授权。
- push 前：branch remote relation 必须明确。
- tag 前：tag object、HEAD、origin/main、VERSION/package source of truth 必须明确。
- 操作后：再次验证本地/远端状态。

硬闸门：

- 未确认 diff scope 时不允许 commit。
- 未确认远端对象关系时不允许宣称 tag/release 完成。
- force push、删除远端 tag、移动已发布 tag 必须用户显式确认。

### 11. `external_action`

典型 prompt：

- “发布到生产”
- “发消息给某人”
- “改线上配置”
- “创建 PR”

闭环要求：

- 必须确认目标系统、账号/tenant、环境、影响范围。
- 必须区分 preview/dry-run 和真实执行。
- 真实执行前需要明确用户授权，除非用户请求已明确包含该具体动作。
- 执行后必须验证外部状态。

硬闸门：

- 模糊目标不能执行。
- 高影响动作不能只凭本地成功输出宣称完成。

### 12. `multi_agent`

典型 prompt：

- “让子 agent 并行分析”
- 大型审计/重构拆分任务。

闭环要求：

- parent 必须读取子任务结果。
- 子任务 `capability_loop` 的 unknowns/verification/risks 不能丢。
- parent final answer 必须综合、去重、标注冲突和剩余验证。

现有 runtimeAgentEvidenceStatus 可以作为第一阶段数据源。

## 通用状态机

```text
INIT
  -> CLASSIFY_TASK
  -> BUILD_CONTRACT
  -> PLAN_ACTIONS
  -> EXECUTE_TOOL
  -> RECORD_ACTION
  -> RECORD_EVIDENCE
  -> UPDATE_REQUIRED_CHECKS
  -> VERIFY_IF_NEEDED
  -> COMPLETION_GATE
  -> FINAL_ANSWER or BLOCK_WITH_NEXT_ACTION
```

状态转换规则：

- 每次 tool result 后都更新 ledger。
- 每次写操作后都生成 delta verification requirements。
- 每次 candidate final answer 前都跑 completion gate。
- 如果 gate 失败，runtime 注入 reminder，并允许模型继续执行必要工具。

## Prompt 注入策略

全场景闭环不能把所有规则塞进 system prompt。建议分三层：

### Layer 1：稳定全局原则

放在基础 code prompt 中，短而稳定：

```text
For every task, close the loop: understand the user's requested scope, gather enough evidence, verify actions that change state, and clearly separate verified results from unverified boundaries.
```

### Layer 2：Task Strategy Card

由 `TaskContract` 动态生成，只针对当前任务类型：

- read_answer summary contract
- investigation evidence contract
- repair safety contract
- git operation closure contract
- external action authorization contract

### Layer 3：Runtime Reminder / Gate

由 ledger 触发：

- read scope incomplete
- post-edit verification required
- commit scope gate required
- external action confirmation required
- final claim lacks evidence

### Runtime 注入预算

为了保持高效，runtime status 只注入“当前必须影响下一步决策”的内容：

- L0/L1：默认不注入 closure strategy，除非 candidate final 出现无证据的本地/工具/测试声明。
- L2：只注入 read scope 和 output coverage reminder，不注入 git/diff/test。
- L3：注入 evidence/search boundary，不注入写后 delta checks。
- L4：注入 post-write pending checks 和 targeted verification。
- L5：注入 hard gate blockers，例如 commit/push/tag/external action 前置条件。

Ledger 保存在内部结构和 session trace 中；prompt 里只放 pending blockers、required next checks、最新 override/boundary 摘要，避免 token 膨胀。

## 硬闸门设计

### P0 Hard Gates

第一阶段必须实现：

1. **Final Claim Gate**
   final answer 声称“完成/已修复/测试通过/已提交/已推送”时，必须有对应 evidence。

2. **Pre-Commit Scope Gate**
   运行 `git commit` 前，必须在当前 session 中观察到，并且这些检查必须发生在最后一次写入、最后一次 `git add` / staging 之后：
   - `git status --short`
   - `git diff --name-status`
   - staged diff 或明确 staged files
   - 本轮 changed files 与用户授权范围一致
   如果检查后又发生 `Edit` / `Write` / write-like `Bash` / `git add`，之前的 scope verification 立即失效，必须重新验证。

3. **Post-Write Delta Gate**
   已部分实现为 reminder。下一步要把 unresolved post-edit requirements 纳入 final gate。

4. **Explicit Verification Gate**
   用户明确要求的验证命令必须成功出现，否则不能 final。
   现有能力可保留并纳入统一 gate。

5. **Read Scope Gate**
   对“总结指定文件/目录/对比多个文件”这类 read-only 任务，final answer 前检查指定对象是否已读取；如果只读取了片段，必须披露边界。

### P1 Hard Gates

1. **Git Push/Tag Gate**
   push/tag/release 操作后，要求验证 local/remote object relation。

2. **External Side Effect Gate**
   真实外部写动作前确认目标和授权。

3. **No-Tool Claim Gate**
   L0/L1 无工具任务允许直接回答，但 final answer 不能伪称本地检查、工具执行、测试或最新外部事实。

### P2 Hard Gates

1. **Generated File Ownership Gate**
   生成文件被手改时要求运行生成器或说明原因。

2. **UI/API Contract Gate**
   API/UI/schema 变更要求相关文档/Swagger/client compatibility checks。

## 与现有代码的集成点

### `internal/query/query.go`

已有可复用点：

- `runtimeTaskStrategySection(...)`
- `runtimeStatusText(...)`
- `runTool(...)`
- `appendPostEditVerificationReminder(...)`
- `completionVerificationNudge(...)`
- `runtimeRecentAgentEvidenceStatus(...)`
- `runtimeAgentEvidenceFollowUpGateStatus(...)`

建议新增：

- `TaskContractBuilder`
- `ActionLedger`
- `EvidenceLedger`
- `CompletionGate`
- `ClaimExtractor`
- `VerificationPlanner`

第一阶段不要移动现有逻辑，只把现有局部机制挂到统一结构：

```text
runtimeTaskStrategySection -> TaskContract.Strategy
appendPostEditVerificationReminder -> DeltaLedger + VerificationPlanner
completionVerificationNudge -> CompletionGate
Agent capability_loop -> EvidenceLedger
```

### `internal/tools`

需要每个 tool 提供轻量 metadata：

```go
type ToolRiskDescriptor interface {
    ActionKind(input json.RawMessage) ActionKind
    EvidenceKind(result tools.Result) EvidenceSourceKind
}
```

第一阶段可不改所有 tool，先在 query 层按 tool name 和 input 做映射。

### Bash 分类边界

Bash 是最难分类的工具。第一版只能做 best-effort action classification，不能声称 100% 静态识别所有副作用：

- 明确只读命令：`git status`、`git diff`、`git rev-parse`、`rg`、`find`、`wc`、`ls`、`test -x`、`bash -n` 等，可归为 `bash_readonly`。
- 明确写命令：`sed -i`、`perl -pi`、`mv`、`cp`、`rm`、`chmod`、`git add`、`git commit`、`git tag`、`git push`、生成器命令等，归为 `bash_write` 或更具体的 git/shared-state action。
- 不确定命令：包含 shell 脚本执行、复杂管道、重定向、`make`、`npm run`、自定义脚本时，按风险上浮处理；如果后续可能修改文件，要求 post-command `git status --short` / `git diff --name-status` 识别实际变化。

也就是说，Bash 的闭环不依赖“完美预测”，而依赖“保守分类 + 后验状态验证”。如果静态分类不确定，但命令后 `git status` 显示工作区变化，则必须生成 delta verification requirements。

### User Override 结构

用户可以显式要求跳过某些验证，但 override 不能是全局免死牌。每次 override 必须结构化记录：

```go
type ClosureOverride struct {
    Scope        string // e.g. "skip_tests", "allow_commit_without_full_go_test"
    AppliesTo    []string // paths/actions/claims
    Reason       string
    RequestedBy  string
    CreatedAt    time.Time
    ExpiresAfter string // e.g. next commit, current turn, current task
}
```

规则：

- “跳过测试直接提交”只关闭 test verification gate，不关闭 diff scope gate。
- “不用 push”只关闭 push requirement，不允许 final answer 声称已推送。
- force push、删除远端 tag、移动已发布 tag、外部发送/发布仍需具体动作级确认，不能被泛化 override 覆盖。
- final answer 必须披露被 override 的验证项。

### `session.Recorder`

建议把 ledger 作为 session trace 的结构化事件写入，便于 resume 和 WebUI inspection：

- `task_contract`
- `action_record`
- `evidence_record`
- `delta_record`
- `completion_gate`

## 分阶段实施计划

### Phase 0：文档与基线

目标：明确边界，不改行为。

- 新增本文档。
- 梳理现有局部闭环能力。
- 标注不是全场景闭环的当前边界。

验收：

- `git diff --check`

### Phase 1：TaskContract + Minimal Completion Gate + Read-Only Closure

目标：建立所有任务共用的最小闭环骨架，并覆盖用户举例的“查看文件并总结”。

实现：

- 新增 Closure Level：L0 trivial、L1 no-tool、L2 read-only、L3 investigation/audit、L4 local change、L5 shared-state change。
- 新增 GateSeverity：`allow`、`warn`、`block_continue`、`block_tool`、`refuse`。
- 新增最小 Final Claim Gate：先只识别高置信 claim，例如 completed/fixed/tested/committed/pushed/read/summarized/no issues found。
- candidate final answer 必须先经过 CompletionGate，再成为 accepted final；被 gate 拦截的完成声明不能作为最终 response。
- 新增 `read_answer` / `search_investigation` classifier。
- 生成 read-only strategy card。
- 记录 Read/Grep/Glob/LS/Bash readonly evidence。
- final answer 前检查指定文件是否已读取。
- 如果用户要求总结/对比/解释，final answer 必须覆盖 requested output dimensions。
- L0/L1 任务不调用工具、不注入重型 strategy，只防止伪称本地证据。

测试：

- `1+1=?` 不调用工具、不注入 audit/repair/git strategy，直接回答。
- `1+1=?` 不产生 ActionLedger/EvidenceLedger prompt 注入，也不触发 gate reminder。
- “读取 README.md，总结项目做什么”必须读取 README 后才能 final。
- fake model 读错文件并总结时触发 reminder。
- 只读部分大文件时，声称全文总结触发 reminder。
- 无工具回答若声称“我已读取文件/运行测试”，触发 Final Claim Gate。
- candidate final 被 gate 拦截时，最终 response 不包含 premature completion claim。
- 普通简短问答不触发重型检查。

### Phase 2：Unified Post-Action Delta Closure

目标：把 repair reminder 升级为统一 delta gate。

实现：

- `FileChange` -> `DeltaLedger`。
- write-like Bash -> pending delta scope requirement。
- final gate 检查 unresolved delta requirements。
- mode change、delete、rename、generated file change 纳入 gate。

测试：

- Edit 后未跑 `git diff --summary` 却 final claim 完成，触发 reminder。
- 修改 hook 后未验证 executable，触发 reminder。
- 普通 markdown 文案修改只要求轻量 diff scope，不强制脚本验证。

### Phase 3：Pre-Commit / Push Hard Gate

目标：覆盖“提交之前检查是否应该修改这些文件”。

实现：

- Bash `git commit` 前检查 ledger 中是否已有 scope verification。
- 如果没有，阻断或返回 tool error/reminder。
- scope verification 必须晚于最后一次 delta 和最后一次 staging；否则视为过期。
- Bash `git push` / `git tag` 后要求 remote verification。
- staged files 与 delta files 对比。

测试：

- 未跑 diff scope 直接 commit，被阻断。
- diff scope 后又修改或重新 staging，再直接 commit，被阻断。
- staged 包含 unrelated file，被阻断。
- push 后未验证 remote state，final claim 被提醒。

### Phase 4：Expanded Claim Verification Gate

目标：在 Phase 1 最小 gate 基础上扩展更多关键声明和证据映射。

实现：

- 扩展 claim extractor：覆盖 inspected、searched、deployed、published、sent、no regression、all files、full repo 等声明。
- claim -> evidence requirement mapping。
- 缺 evidence 时插入 completion reminder。

测试：

- 没跑测试却说 tests passed，被阻断。
- 没 push 却说 pushed，被阻断。
- 没读指定文件却说已总结，被阻断。

### Phase 5：External Action Closure

目标：外部副作用动作不误执行、不假完成。

实现：

- external tool action kind。
- target/tenant/environment confirmation requirement。
- post-action verification requirement。

测试：

- 模糊“发一下”不能执行。
- “部署到生产”必须明确目标和授权。
- dry-run 不能说 production deployed。

### Phase 6：Observability / WebUI

目标：让用户能看到闭环状态。

实现：

- Local trace 展示 task contract、pending checks、verified claims。
- TUI/WebUI 显示 unresolved blockers。
- mode-only change 和 external action gate 可见。

## 验收矩阵

### Unit Tests

```bash
go test ./internal/query -run 'TestTaskContract|TestReadOnlyClosure|TestCompletionGate|TestPreCommitScopeGate' -count=1
go test ./internal/tools/bash -run 'TestBashCommitGate|TestBashPushGate' -count=1
go test ./internal/tools/fileedit -count=1
```

### Golden Tests

- runtime status 中 read-only task contract 稳定出现。
- repair strategy 不污染 README summary。
- final gate reminder 文案稳定。

### Synthetic Fixtures

1. `readme_summary_fixture`
   - README + EXTRA.md。
   - 用户只要求 README。
   - fake model 读 EXTRA 后总结 README，应被阻断。

2. `scope_commit_fixture`
   - 本轮修改 A。
   - 工作区已有 unrelated B。
   - fake model 直接 commit，应被阻断。

3. `release_tag_fixture`
   - local tag 与 remote tag 指向不同。
   - fake model 声称 release fixed，应被阻断。

4. `external_action_fixture`
   - fake deploy tool dry-run。
   - final claim production deployed，应被阻断。

### Real Replay

第一批 replay：

- README 总结任务。
- 函数解释任务。
- repo health audit。
- P0 repair + commit。
- release tag repair。

每个 replay 只验证该任务类型的闭环，不外推到 open-world 100%。

## 风险与对策

### 风险 1：过度闭环导致简单任务变慢

对策：

- TaskContract 按风险分层。
- read-only summary 只检查指定文件是否读取和回答维度，不跑 git。

### 风险 2：硬闸门误伤合法高级操作

对策：

- 明确 user override 机制，但 override 也要记录。
- force push / tag rewrite 仍需显式确认。

### 风险 3：ledger 太重，影响 token 和性能

对策：

- ledger 存结构化摘要，不把完整 tool output 重复注入。
- runtime status 只注入 pending blockers 和必要 next checks。

### 风险 4：claim extraction 不可靠

对策：

- 第一阶段只识别高置信 claim：done/fixed/tested/committed/pushed/read/summarized/no issues found。
- 不做自然语言全解析，先覆盖高风险假完成。

### 风险 5：多轮任务 contract 漂移

对策：

- session-level task contract 持久化。
- 用户新指令可更新 contract，但 unresolved blockers 必须显式关闭、满足或由用户取消。

### 风险 6：final streaming 先泄漏错误完成声明

对策：

- candidate final 先进入内部 buffer，CompletionGate 通过后再作为 accepted final 输出。
- 如果现有流式架构短期无法完全 buffer，至少保证最终 `RunResult.Response`、transcript accepted answer 和用户可见完成态不包含被 gate 拦截的完成声明。
- 对 blocked candidate 只保留为 internal diagnostic，不作为成功回答。

### 风险 7：Bash 副作用静态分类不完整

对策：

- 第一版明确是 best-effort，不宣传 100% shell 语义识别。
- 对不确定 Bash 命令风险上浮，要求后验 `git status` / `git diff --name-status`。
- 一旦后验发现文件变化，就补 DeltaLedger 和 post-action verification requirements。

### 风险 8：用户 override 过宽导致 gate 失效

对策：

- override 必须结构化记录 scope、action、path、过期条件。
- override 只关闭被用户明确点名的 gate，不级联关闭其他 gate。
- final answer 必须披露 override 过的验证项和风险。

## 完成定义

全场景闭环第一版完成时，必须满足：

1. L0/L1 简单任务能直接轻量闭环，不调用工具、不注入重型 strategy，也不伪称本地证据。
2. read-only 总结类任务能验证读取范围和回答范围。
3. investigation 类任务能说明搜索范围、证据和未覆盖边界。
4. repair/change 类任务在 final 前检查 unresolved post-edit requirements。
5. commit 前有程序化 scope gate，且 scope verification 必须晚于最后一次写入和 staging。
6. push/tag/release final claim 必须有 local/remote 状态证据。
7. final answer 中 “完成/修复/测试通过/已提交/已推送/已总结/没发现问题” 等关键声明必须有 evidence 或明确边界。
8. 被 CompletionGate 拦截的 candidate final 不会作为 accepted final answer 泄漏给用户。
9. Bash 写入和不确定副作用命令会通过后验状态验证进入 delta closure。
10. 用户 override 有结构化范围和过期条件，不会关闭未授权 gate。
11. 普通简单任务不会被 repo audit、repair safety、git checks 误触发。

## 推荐下一步

优先实现 Phase 1 + Phase 3 的最小闭环：

1. Closure Level + 最小 Final Claim Gate，保证 `1+1=?` 这类任务轻闭环，而“已读取/已测试/已推送”等声明必须有证据。
2. `read_answer` contract，解决“查看文件并总结是否闭环”。
3. `pre_commit_scope_gate`，解决“提交前是否确认修改范围闭环”，并要求检查晚于最后一次写入和 staging。
4. 把现有 post-edit reminder 的 pending requirements 接入 final gate。
5. 建立 candidate final buffer，避免 gate 拦截后的错误完成声明进入 accepted final。

这三项覆盖用户最关心的两个场景，且不需要一次性重构所有工具。
