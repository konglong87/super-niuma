# Git 共享状态授权循环事故手册

- 适用范围：`git commit`、`git push`、`git tag`、`git add --force` 在授权、preflight、hook 或完成声明阶段反复被 gate 阻断
- 文档类型：Reference + Explanation + How-to
- 基线日期：2026-08-07
- 当前修复提交：`c2ca7615`（`fix: bound confirmed git authorization retries`）
- 首要事故：session `043b6031-0f52-4127-95be-9bcdfead90c7`
- 爆炸半径：`B5_SHARED_STATE`
- 详细根因：[Git 共享状态授权死循环根因分析与彻底修复方案](shared_state_git_authorization_loop_root_cause_and_fix_plan.md)
- 全局位置：[全局运行时拓扑与变更影响堪舆图](global_runtime_topology.md)

本文是同类事故的固定入口。它不替代详细根因文档，而是把 11 次历史复发得到的排障顺序、架构不变量、回归模板和发布门槛固化下来。以后发现类似循环，先按本文采证和分类，再决定是否修改代码。

## 1. 先看当前能力边界

下表是事故处理的事实基线。排查时不得把“目标架构”误报成“当前已经实现”。

| 能力 | 当前状态 | 代码或证据 |
| --- | --- | --- |
| 当前用户 turn 的直接 Git 授权 | 已实现 | `gitpolicy.ParseAuthorization` |
| 最新 assistant 明确授权询问 + 最近 shell fence/授权询问附近的行内命令 + 用户短确认 | 已实现 | `confirmedSharedStateEffects` |
| confirmed grant 按规范化 effect 一次性消费 | 已实现，作用域为单次 `Session.run` | `continuationAuthorizationGrant` |
| commit author/email/message 精确绑定 | 已实现 | `gitpolicy.AuthorizationFromEffects`、`EffectKey` |
| 生成的只读 preflight 不继承未消费 confirmed grant | 已实现 | `query.Session.run` |
| hook 改写后的最终输入重新做 Git policy 检查 | 已实现 | `runToolWithAuthorization` |
| authorization scope mismatch 指出最接近 scope 的差异字段和唯一恢复方式 | 已实现 | `authorizationMismatchReason`、`gitViolationDecision` |
| 相同 Git hard block 忽略 description/sandbox 展示差异 | 已实现 | `sharedStateGateRetryTracker` |
| 单次 run 内相同 gate/effect 最多阻断 2 次 | 已实现 | `sharedStateGateRetryLimit = 2` |
| abort transcript 保持 `tool_call` / `tool_result` 成对 | 已实现 | `TestSharedStateGateRetryStopsDescriptionVariantLoop` |
| challenge/grant 跨进程、跨 user turn 持久恢复 | 未实现 | 后续 Broker 工作 |
| grant 绑定 HEAD/index/branch/remote state digest | 未实现 | 后续 Broker/coordinator 工作 |
| heredoc、workspace script、`bash -c` 间接 Git effect resolver | 未实现 | 已知安全增强，不得误报完成 |
| 专用结构化 Git executor | 未引入 | 有 `B4_PROTOCOL` 兼容和成本副作用，单独评审 |

## 2. 复发事实与统一结论

历史扫描口径为：真实项目 transcript 中 `pre_commit_scope`、`shared_state_git_push`、`shared_state_git_tag` 或 `shared_state_authorization` 至少出现 2 次，排除临时 agent-eval 和显式 evaluation 仓库。

基线结果：

- 11 个真实项目 session。
- 93 次相关 gate 阻断。
- 139 次包含 `git add/commit/push/tag` 的 Bash tool call。
- 最严重两个旧样本分别有 40 次和 29 次 gate 阻断。
- session `043b6031` 的一个 `执行` turn 中，同一直接 commit 被阻断 15 次；完整 Bash JSON 因 description、sandbox flag 等字段变化而全部不同。
- 该 turn 共消耗 16 个模型轮次，累计 input 1,427,205 tokens，其中 cache read 1,166,198 tokens。

2026-08-07 修复后验收又发现一个恢复可用性问题：用户把示例中的 `"<原定提交信息>"` 原样粘贴进授权命令，agent 实际执行完整中文 commit message。精确 scope 正确地把两者判为不同 effect，但旧 gate 只返回泛化的“没有匹配授权”，导致 agent 错误声称只能手动执行。本次后续修复保留严格匹配，同时明确输出 `commit message` 等具体 mismatch 字段；placeholder 始终是字面量，不是通配符。

统一结论不是“模型太弱”，而是 Git 端到端流程曾缺少确定的恢复协议：

```text
用户意图
  -> task contract
  -> 精确 Git effect
  -> authorization/challenge
  -> pre-tool gate
  -> final hook input recheck
  -> one-time consume
  -> execute
  -> readback
  -> transcript/observability
```

只修其中一个局部会留下新的缝隙。历史上已经分别修过 preflight 顺序、短回复继续执行、TUI 噪声、通用 loop guard、current-turn authorization 和结构化 effect，但直到 `c2ca7615` 才把本次事故主路径上的 confirmation、consume、final-input check 和语义熔断一起闭环。

## 3. How to 排查新的 Git 授权循环

### 3.1 立即止损

出现同一 Git 操作连续三次被同一 gate 拦截时：

1. 停止继续发送“再试一次”。
2. 不修改 `permissions.allow` 试图绕过 Shared-State Gate。
3. 不改变 author、email、message、remote、ref 或 force 参数来碰运气。
4. 不把同一 Git 命令写入脚本、heredoc、`bash -c` 或其他解释器绕过检测。
5. 不把 `<原定提交信息>`、`<branch>` 等示例 placeholder 原样作为授权 scope；必须替换为将要执行的真实值。
6. 不读取或输出完整 `~/.golang-cc/settings.json` 寻找绕过项；该文件可能含 provider credential，tool result 会进入 transcript。只使用脱敏配置摘要。
7. 保存 transcript、当前 Git 只读状态和运行版本，再开始归因。

修复后的 runtime 应在第三次相同 hard block 返回 `shared_state_gate_retry_abort`。如果仍出现第四次相同 fingerprint，直接按熔断回归处理，不再归因于模型行为。

危险模式（`allow` / `bypassPermissions`）下，非破坏性的、已解析的 exact Git effect 会进入现有 TUI `PermissionPrompt`，仅提供一次性 Allow/Deny；批准不会写入 session、project 或 global 配置。破坏性操作、`git add --force`、参数漂移和无法解析的间接执行仍由 Shared-State Gate 硬拦截。普通模式继续要求当前聊天中的明确授权/确认。

### 3.2 定位 transcript

```bash
SESSION_ID=043b6031-0f52-4127-95be-9bcdfead90c7
find ~/.golang-cc/projects ~/.go-claude/projects \
  -type f -name "${SESSION_ID}.jsonl" -print 2>/dev/null
```

找到唯一文件后显式设置路径，不要用宽泛 glob 修改或删除历史记录：

```bash
TRANSCRIPT=~/.golang-cc/projects/<project-key>/<session-id>.jsonl
test -f "$TRANSCRIPT"
wc -l "$TRANSCRIPT"
```

### 3.3 提取最小证据

先按行号搜索，不复制完整私有对话到 issue 或文档：

```bash
rg -n 'shared_state_authorization|destructive_shared_state|force_add_ignored_path|pre_commit_scope|shared_state_git_push|shared_state_git_tag|shared_state_gate_retry' "$TRANSCRIPT"
rg -n 'git (add|commit|push|tag)|确认授权|authorize|执行|继续|再试' "$TRANSCRIPT"
```

检查 transcript 事件类型和 retry event：

```bash
jq -r '.type' "$TRANSCRIPT" | sort | uniq -c | sort -nr
jq -c 'select(.type == "shared_state_gate_retry") | {type, content}' "$TRANSCRIPT"
jq -c 'select(.type == "continuation_intent" or .type == "task_contract" or .type == "completion_gate") | {type, content}' "$TRANSCRIPT"
```

统计 Git tool call 时只输出工具名和行号，避免把可能含凭据的完整命令扩散到日志：

```bash
jq -r 'select(.type == "tool_call" and .tool_name == "Bash") | [.id, .tool_name] | @tsv' "$TRANSCRIPT"
```

必须保存的最小证据：

| 证据 | 目的 | 脱敏要求 |
| --- | --- | --- |
| session ID、runtime commit、provider/model | 确定运行版本与模型分组 | session ID 可保留；不记录 provider credential |
| 用户当前 turn 的 Git 意图摘要 | 判断 direct authorization | 摘要，不复制完整聊天正文 |
| 最新 assistant 是否有明确授权询问和 shell fence | 判断 continuation bridge | 命令中的 token、私有 URL、私有 remote 脱敏 |
| gate `rule_id`、repeat、effect fingerprint | 判断是否同一确定性失败 | fingerprint 可保留 |
| operation、remote/ref/tag、author/message 是否漂移 | 判断 scope mismatch | email 可改为 `<redacted>`，保留“空/非空/是否变化”事实 |
| HEAD、branch、index/remote 是否变化 | 判断 state drift | OID 可保留，diff 内容不进入事故台账 |
| tool call/result 是否配对 | 判断 transcript 完整性 | 只记录 tool ID 和计数 |
| 最终是否真实 commit/push/readback | 判断用户影响 | 记录 OID/ref 结果，不记录凭据 |

### 3.4 保存 Git 只读状态

在任何修复或重试前执行：

```bash
git status --short --branch
git rev-parse --show-toplevel
git rev-parse HEAD
git branch --show-current
git diff --name-status
git diff --cached --name-status
git remote -v
git branch -vv
```

如果 remote URL 可能含凭据，不要把 `git remote -v` 原样贴入文档；只保留 remote 名、主机和 ref 关系。

### 3.5 用决策树分类

```text
Git 操作反复被 gate 拦截
|
+-- rule 是 pre_commit_scope / shared_state_git_push / shared_state_git_tag?
|   +-- 是 -> 先查缺失的只读证据与 preflight 顺序，不要改 authorization parser
|
+-- rule 是 shared_state_authorization / destructive_shared_state / force_add_ignored_path?
    |
    +-- 当前 user turn 已明确写出相同 operation/scope?
    |   +-- 是 -> 查 author/message/remote/ref 的具体 mismatch；placeholder 按字面量比较
    |            若字段完全相同，再查 ParseAuthorization / EffectKey / scope normalization
    |
    +-- user 只是“执行/继续/yes”，最新 assistant 有明确授权询问和 shell fence?
    |   +-- 是 -> 查 latest-assistant recovery、fence 选择、grant count/consume
    |   +-- 否 -> 正确阻断；重新发起精确授权，不得从旧历史扩权
    |
    +-- 只变 description、tool_id 或 sandbox flag?
    |   +-- 是 -> 应命中同一 retry fingerprint；第三次出现就是回归
    |
    +-- hook 改写了最终命令?
    |   +-- 是 -> 必须按改写后 effect 重查，旧 grant 不得放行扩大后的命令
    |
    +-- 通过 heredoc/script/bash -c 间接执行?
        +-- 是 -> 命中尚未完成的 EffectResolver 安全边界，单独登记，禁止当成已修复主路径
```

### 3.6 判断根因所有者

| 现象 | 首要所有者 | 不应先改 |
| --- | --- | --- |
| 当前 turn 明确授权仍不匹配相同 command | `internal/gitpolicy` | system prompt、MaxTurns |
| gate 只说“未匹配”但不说明 author/message/remote/ref 差异 | `internal/gitpolicy`、`internal/toolpolicy` | 让用户手动执行 |
| 短确认没有恢复最新精确询问 | `internal/query/continuation_intent.go` | permissions settings |
| grant 可重复使用或一个 grant 放行两个 effect | `internal/query/continuation_authorization.go` | 通用 loop guard |
| description/sandbox 变化逃逸熔断 | `internal/query/shared_state_gate_retry.go` | 全局工具指纹 |
| hook 把授权命令改宽后仍执行 | `runToolWithAuthorization` / `internal/toolpolicy` | assistant reminder |
| preflight 被 hook 改成写操作并消费 grant | query preflight dispatch | Git parser 关键词 |
| tool call 没有对应 tool result | query abort recorder | UI renderer |
| 新 user turn 清空相同失败状态 | 后续持久化 Broker/retry state | 提高单 run 阈值 |
| heredoc/script/解释器隐藏 Git effect | 后续 `EffectResolver` / sandbox | 字符串搜索 `git commit` |
| commit 成功、push 失败后状态不清 | 后续 coordinator/readback | 把 compound Bash 当原子命令 |

## 4. 架构不变量

任何修复必须逐项说明是否保持以下不变量：

1. 普通历史消息中的 Git 文本不能自动成为当前授权。
2. 短确认只绑定最新 assistant 的明确授权询问，不扫描任意旧消息；为兼容常见模型输出，授权询问附近的行内反引号命令可作为 shell fence 的补充格式。
3. 授权匹配规范化 effect，不匹配展示文案或 tool ID。
4. operation、remote/ref/tag、destructive、force-add path、author/email/message 约束不得扩大。
5. confirmed grant 对每个 effect 最多消费一次；direct current-turn authorization 的可复用语义保持不变。
6. generated preflight 不得携带未消费的 confirmed capability。
7. hook 改写后必须按最终输入重新分析和裁决。
8. 同 rule + 同 normalized effect 的 hard block 在单 run 内最多 3 次。
9. abort 前必须追加 synthetic failed tool result，保持 transcript 协议配对。
10. commit/push/tag 的完成声明必须基于真实 readback，不以模型文字为准。
11. 不能靠改 permissions、换参数或间接脚本绕过 Shared-State Gate。
12. 审计记录不得包含 credential、完整私聊正文或 diff 内容。

未来引入 Broker 后还必须增加：repo identity、HEAD、branch、index digest、remote OID 绑定；challenge expiry；跨 turn/restart/fork/rewind ownership；并发原子 consume。

## 5. 禁止的快捷修复

| 快捷方案 | 为什么禁止 |
| --- | --- |
| 把 `执行`、`好`、`继续` 直接解释为 commit/push 授权 | 没有确认对象和 scope，形成短词扩权 |
| 从多轮历史拼接 authorization | 回归历史授权漂移和 prompt injection 风险 |
| 给 settings 增加 `Bash(git commit)` / `Bash(git push)` | permission 和 shared-state authorization 是两层控制 |
| `cat ~/.golang-cc/settings.json` 查绕过配置 | 可能把 provider credential 持久化进 transcript，且仍不能放宽 Shared-State Gate |
| gate 失败后自动无限重放 | 状态不变时结论确定相同，只会浪费 turn/token |
| 改 author/email/message/remote 直到命中 | 违反用户约束，可能把安全阻断变成错误共享状态 |
| 把 `<message>`、`<branch>` 等 placeholder 当通配符 | 授权 scope 会被无边界扩大；placeholder 必须按字面量比较 |
| 提高 `MaxTurns` 或通用 loop guard limit | 增加事故成本，不修授权协议 |
| 通用 loop guard 全局忽略 description | 扩大到 `B3_GLOBAL_RUNTIME`，可能误杀其他工具 |
| 只加 system reminder | 软提示不能替代 hard authorization/consume |
| 写脚本或 `bash -c` 绕过 | 利用当前未完成的间接 effect 识别边界，属于安全回归 |
| 为本 bug 直接新增结构化 Git executor | executor 有真实协议、兼容、turn 和迁移成本，需独立 `B4_PROTOCOL` 提案 |

## 6. 回归测试模板

### 6.1 最小事故复刻

新修复必须先写能在旧代码上失败的 RED test，至少表达以下输入序列：

```text
assistant:
  明确说明被阻断原因
  给出一个 shell fence，包含精确 commit + push
  明确询问是否授权执行
user:
  执行
model calls:
  read-only preflight
  exact commit
  readback
  exact push
  remote readback
expected:
  commit/push 均 dispatch
  confirmed effect 各消费一次
  无 Shared-State Authorization Gate error
```

基准实现见 `internal/query/shared_state_confirmation_test.go` 的 `confirmedGitWorkflowProposal` 和 `TestConfirmedGitWorkflowExecutesCommitAndPushEndToEnd`。

### 6.2 必须覆盖的正负矩阵

| 类别 | 用例 | 期望 |
| --- | --- | --- |
| 正向 | 当前 turn 精确 commit/push | 不增加确认 turn，正常执行 |
| 正向 | 最新 assistant 精确询问，用户 `执行` | 恢复 exact effects，逐个消费 |
| 负向 | 没有授权询问，仅有 Git 示例 | 不产生 grant |
| 负向 | 授权询问已不是最新 assistant 消息 | 不继承旧授权 |
| 负向 | 用户提问、否定或修改目标 | 不视为确认 |
| 负向 | 非 shell fence | 不解析为可执行 effect |
| scope | `origin/main` 被改为 `backup/dev` | 阻断 |
| scope | 空 email 被改为历史 email | 阻断 |
| scope | 授权 message 是 `<原定提交信息>`，执行实际 message | 阻断并明确 `commit message` mismatch；提示精确重新授权，不要求手动执行 |
| consume | 一个 confirmed effect 被执行两次 | 第二次阻断 |
| consume | 一个 grant 试图放行两个相同 commit | 整体不授权超额 effect |
| hook | hook 改 author/message/force/ref | 按最终输入阻断 |
| preflight | hook 把只读 preflight 改成 confirmed commit | 不执行、不消费 grant |
| retry | 同 command 只变 description/sandbox | 第三次 abort，实际 dispatch 为 0 |
| transcript | retry abort | `tool_call` / `tool_result` 数量按 tool ID 配对 |

### 6.3 定向测试

```bash
go test ./internal/query -run 'TestResolveContinuationIntent|TestContinuationAuthorizationGrant|TestConfirmedAuthorization|TestConfirmedGitWorkflow|TestConfirmedGrant|TestSharedStateGateRetry' -count=1
go test ./internal/gitpolicy -run 'Authorization|Effect|Fingerprint|Commit|Push|Tag' -count=1
go test ./internal/toolpolicy ./internal/agentruntime -count=1
```

### 6.4 完整验证

```bash
go test ./... -count=1
scripts/closure-gate-acceptance.sh
git diff --check
go run ./scripts/runtime-topology-check \
  --base HEAD --working-tree \
  --impact updated \
  --blast-radius B5_SHARED_STATE \
  --reason 'change Git shared-state authorization, gate retry, or evidence flow'
```

如果只改本手册和文档入口，没有修改 runtime 节点、锚点、因果边或图件，拓扑检查应使用：

```bash
go run ./scripts/runtime-topology-check \
  --base HEAD --working-tree \
  --impact none \
  --blast-radius B0_LOCAL \
  --reason 'add documentation for the existing Git shared-state authorization topology without changing runtime paths or causal edges'
```

真实 provider acceptance 不能用 scripted streamer 冒充。`scripts/prompt-acceptance-matrix.sh` 若因 provider/protocol/credential 配置失败，必须记录环境阻塞和已完成的替代验证，不能写成通过。

## 7. 验证结果的判定标准

### 7.1 发布阻断项

以下任一项失败都不得提交为“彻底修复”：

- 未授权 Git shared-state effect 被执行。
- 已消费 confirmed grant 可以重放。
- hook 改写后的扩大命令仍继承旧授权。
- preflight 可以消费或借用 confirmed grant。
- 同 run 相同 rule/effect 出现第 3 次 hard block。
- abort transcript 出现孤立 `tool_call`。
- 用户给出的 author/email/message/remote/ref 被静默改写。
- scope mismatch 只返回泛化错误，或引导用户手动绕过 runtime。
- `go test ./... -count=1` 或 topology check 失败且没有确认的无关环境原因。

### 7.2 目标指标和调查阈值

| 指标 | 目标 | 触发处理 |
| --- | --- | --- |
| 未授权 shared-state 放行 | 0 | 单次即 P0 安全事故 |
| consumed grant 重放成功 | 0 | 单次即 P0 安全事故 |
| hook/preflight 扩权执行 | 0 | 单次即 P0 安全事故 |
| 同 run 同 fingerprint hard block | `<= 2` | 第 3 次即熔断回归 |
| 最新有效询问的短确认恢复 | deterministic tests 100% | 单个可复现失败即登记回归 |
| 正常 direct authorization 额外确认 turn | 0 | 按 provider 分组调查 parser 假阴性 |
| abort transcript 配对率 | 100% | 任一孤立 tool call 阻断发布 |
| commit/push 完成声明 readback | 100% | 无 readback 不得声称完成 |
| unknown indirection | 先记录，不宣称已覆盖 | 单独进入 EffectResolver backlog |

当前已存在的可观测信号：

- transcript event：`shared_state_gate_retry`，字段含 `severity`、`rule_id`、`repeat`、`hard_limit`、`effect_fingerprint`。
- stop reason：`shared_state_gate_retry_abort`。
- observability event：`query.shared_state_gate_retry_abort`。
- gate/transcript events：`continuation_intent`、`task_contract`、`completion_gate`、`action_record`。

`shared_state.challenge_issued`、`grant_consumed`、`state_drift_blocked`、`partial_failure` 等事件属于后续持久化 Broker 目标，当前不存在时不能用它们作为已上线查询条件。

## 8. 发布、回滚与 readback 清单

### 8.1 发布前

- [ ] 记录本次事故 session、runtime commit、rule ID、normalized effect 和用户影响。
- [ ] RED test 在修复前按预期失败，GREEN 后通过。
- [ ] 正向、负向、scope、consume、hook、preflight、retry、transcript 矩阵通过。
- [ ] `go test ./... -count=1`、closure acceptance、`git diff --check` 通过。
- [ ] 根据实际影响填写 topology impact 和 blast radius。
- [ ] 文档明确正收益、负作用、恢复路径和未完成边界。
- [ ] staged diff 不含 token、credential、私有 URL 或完整聊天正文。

### 8.2 发布后

```bash
git status --short --branch
git rev-parse HEAD
git rev-parse origin/main
git ls-remote origin refs/heads/main
```

本地 HEAD、remote-tracking OID 和远端 advertised OID 必须一致；不能只凭 `git push` 的文字输出声称完成。

### 8.3 回滚原则

1. 回滚新增 recovery/retry wiring 时，保留 current-turn authorization hard gate。
2. 不得回滚到扫描历史对话获取长期授权的旧行为。
3. 保留未知 transcript event 的向后兼容读取。
4. 误拦增多时先收窄 parser/rule，或将尚无数据的 unknown-indirection 策略降为 shadow；不得放宽已证明的未授权执行不变量。
5. commit 已成功而 push 失败时不回滚 commit；记录 partial fact，只恢复未完成 push。

## 9. 新事故登记模板

在 [Bug Fix History](../bugs/README.md) 增加索引和详情，并填写：

```markdown
### BUG-YYYY-MM-DD-NNN: <一句话现象>

- 状态：OPEN | FIXED | VERIFIED | BLOCKED
- 首次发现：<date / session short id>
- 运行版本：<local HEAD / deployed commit>
- provider/model：<name>
- 影响范围：<entry/mode/repo/shared-state operation>
- 用户当前 turn 意图：<脱敏摘要>
- 最新 assistant challenge：<有/无；是否明确询问；是否有 shell fence>
- gate：<rule_id / repeat / hard_limit / effect fingerprint>
- Git effect：<operation / remote / ref / destructive / author-message constraints>
- Git state：<branch / HEAD / index changed? / remote changed?>
- 复现：<最小 turn + tool sequence>
- 根因层：<evidence | authorization | continuation | consume | retry | final-input | persistence | indirection | readback>
- 与历史差异：<为什么不是已有用例已经覆盖的同一个 bug>
- 修复方案：<owner / invariant / trade-off / rollback>
- RED/GREEN：<test names>
- 验证命令：<targeted / full / topology / acceptance>
- 观测结果：<blocks / turns / tool calls / tokens / duration / completion>
- 修复提交：<OID>
- 剩余风险：<明确未完成边界>
```

同时为单元测试保存脱敏 fixture，推荐结构：

```json
{
  "incident_id": "BUG-YYYY-MM-DD-NNN",
  "runtime_commit": "<oid>",
  "user_prompt_class": "short_confirmation",
  "assistant_request": {
    "explicit_authorization_question": true,
    "shell_fence_count": 1
  },
  "effects": [
    {"operation": "commit", "author_email": "empty", "message": "<exact-placeholder>"},
    {"operation": "push", "remote": "origin", "ref": "main"}
  ],
  "attempts": [
    {"rule_id": "shared_state_authorization", "effect_fingerprint": "<hash>", "presentation_variant": 1},
    {"rule_id": "shared_state_authorization", "effect_fingerprint": "<same-hash>", "presentation_variant": 2}
  ],
  "expected": {
    "max_identical_blocks": 2,
    "stop_reason": "shared_state_gate_retry_abort",
    "git_dispatches": 0,
    "paired_tool_records": true
  }
}
```

fixture 不保存完整 transcript、真实 email、credential、私有 remote URL 或 diff 内容。若行为依赖具体命令，使用无敏感信息的最小 synthetic command。

## 10. 结构化 Git executor 的决策边界

结构化 executor 有长期正收益：类型化输入、降低 command injection、稳定 author/message/remote/ref 解析，以及更清晰的 effect consume/readback。

它也有已确认的负向影响：

- 新 tool schema 改变模型工具选择和 prompt/tool bytes。
- Bash 复合命令、hooks、subagent 和用户习惯需要兼容迁移。
- commit/push 拆分会增加 tool call、延迟和 partial-failure 编排。
- 影响 `RT-PRETOOL`、`RT-TOOLS`、`RT-SUBAGENT`、`RT-EVIDENCE`、`RT-COMPLETION`、`RT-PERSIST`、`RT-OBSERVE`。
- 爆炸半径从本次 `B5_SHARED_STATE` 修复扩大为 `B4_PROTOCOL`。

因此它不是本类 bug 的默认补丁。只有独立提案提供 tool selection 数据、Bash 兼容策略、迁移矩阵、partial-failure 状态机和回滚方案后才评审引入。

## 11. 从 11 次复发提炼的工程经验

1. **先按端到端状态机建模，再修局部。** commit/push 不是一个 Bash 字符串，而是 authorization、evidence、dispatch、readback 和 audit 的组合工作流。
2. **Hard Gate 的重复必须按语义计数。** 对确定性 gate，description、tool ID 和展示 flags 不是进展；第三次相同失败即终止，同时保留两次恢复/重试机会。
3. **授权与任务意图必须分离。** continuation 可以说明“用户要继续”，但只有精确 challenge/effect 才能说明“允许改变共享状态”。
4. **安全收紧必须同时设计合法恢复路径。** 只把历史授权改成 current-turn，会修越权但制造短确认死结；hard gate 上线前必须验证被拦后怎样唯一恢复。
5. **最终输入才是裁决对象。** preflight、hook、subagent 或 wrapper 任何改写都不能沿用改写前的授权结论。
6. **一次性能力要在 dispatch 前消费。** 成功后才消费会给并发/重放留下窗口；preflight 又不能提前消费真实 effect。
7. **通用熔断和领域熔断各司其职。** 通用 loop guard 看工具输入和结果是否有进展；Git gate tracker 看 rule/effect 是否确定性重复，不能互相替代。
8. **测试必须复刻逃逸方式。** 只测两次完全相同 JSON 不够，必须改变 description、sandbox flag、tool ID、author 表达和 hook 输入。
9. **文档必须标注“已实现/目标”。** Broker、state binding、间接 resolver 尚未落地，明确边界比笼统宣称“彻底修复”更能防止下一次事故从零开始。
10. **提交与 push 也要做 readback。** 测试通过不是发布完成；local HEAD、origin tracking ref 和远端 advertised OID 一致才闭环。

## 12. 相关资料

- [详细根因、11 次 session 证据、目标架构与分阶段方案](shared_state_git_authorization_loop_root_cause_and_fix_plan.md)
- [全局运行时拓扑与变更影响堪舆图](global_runtime_topology.md)
- [Loop Guard](../loop_guard.md)
- [Pre-Commit Scope Gate 恢复方案](../prompt_logic/pre_commit_scope_gate_recovery_plan.md)
- [运行时消息流与 Closure Gate](runtime_message_flow_and_closure.md)
- [Bug Fix History](../bugs/README.md)
