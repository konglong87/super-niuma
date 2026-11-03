# Pre-Commit Scope Gate 恢复路径修复方案

## 背景

2026-07-11 的 `go-claude` 真实 transcript 显示：一次正常的 staged 文件提交流程里，`git commit` 被 Pre-Commit Scope Gate 反复拦截。可见对话里曾解释为 `git diff --cached --name-status` 被 runtime 当成 write-like Bash 并重置 gate，但本地 transcript 证据不支持这个结论：

- `git diff --cached --name-status` 成功执行，并被记录为 `bash_readonly`。
- `git diff --cached --name-status && git commit ...` 在 Bash 执行前就被拦截，因此前半段验证根本没有机会满足 gate。
- `git status --short --branch && git diff --name-status && git diff --cached --name-status` 作为独立 Bash tool call 成功完成。
- 后续独立的 `git commit` 成功执行。

## 根本原因

安全策略本身方向正确，但恢复契约不够明确：

1. reminder 没说清楚：scope 验证必须在 `git commit` 之前作为独立 Bash tool call 成功完成。
2. reminder 只列出了多个检查项，没有给出唯一、可复制的恢复命令。
3. 实现上对“已有 staged 改动”的场景允许 partial staged-only 证据通过，强度不够。
4. trace path 提取会扫描完整 `git commit --author=...` 命令，可能把邮箱片段记录成 path，污染诊断信息。

这会造成模型执行死循环：模型持续把“验证”和“提交”写在同一个 Bash 命令里，但 pre-tool gate 是在 Bash 执行前判断的，所以它永远观察不到这条命令里的验证结果。

## 目标行为

Pre-Commit Scope Gate 仍然应该是阻断型安全机制，不放宽 commit 边界。修复后应该满足：

- 所有 `git commit` 都必须先有当前会话里成功完成的 scope 验证证据。
- 该验证必须同时覆盖当前工作区和 staged scope，命令里要包含：
  - `git status --short`
  - `git diff --name-status`
  - `git diff --cached --name-status` 或 `git diff --staged --name-status`
- reminder 明确要求把验证作为独立 Bash tool call 运行。
- reminder 明确禁止把验证和 `git commit` 链在同一个 Bash 命令里。
- `git diff --cached --name-status` 继续保持 read-only 分类。
- `git commit --author=...` 不再把邮箱值记录成 path。

## 实现方案

1. 替换 pre-commit reminder，写入明确恢复步骤和精确命令：

   ```bash
   git status --short --branch && git diff --name-status && git diff --cached --name-status
   ```

2. 收紧 `preCommitScopeVerified`：只接受一次成功的、独立的 Bash scope verification call，且该命令必须同时包含 status、unstaged diff 和 staged diff。
3. 保持 pre-tool gate 的安全模型：`git diff --cached && git commit` 继续被阻断，因为 gate 无法在执行前观察到其中的验证结果。
4. 补回归测试：
   - partial staged-only 验证不能放行 commit；
   - chained verify-and-commit 被阻断，且 reminder 写明 separate tool-call 要求；
   - full standalone verification 后 standalone commit 可以执行；
   - `git diff --cached --name-status` 仍是 read-only；
   - `git commit --author=...` 的 action path 不含邮箱片段。
5. 跑聚焦测试、全量测试和真实 CLI smoke test，证明阻断路径和恢复路径都可验证。

## 验证命令

```bash
go test ./internal/query -run 'PreCommit|CommitScope|ShellIntent|PathsForTrace' -count=1
go test ./internal/query ./internal/sandbox -count=1
go test ./... -count=1
git diff --check
```

真实 CLI smoke test：

```bash
tmp="$(mktemp -d)"
git -C "$tmp" init
git -C "$tmp" config user.email test@example.com
git -C "$tmp" config user.name Test
printf 'base\n' > "$tmp/file.txt"
git -C "$tmp" add file.txt
git -C "$tmp" commit -m init
printf 'change\n' >> "$tmp/file.txt"
git -C "$tmp" add file.txt
go run ./cmd/golang-cc --cwd "$tmp" --max-turns 1 --model test "Run git commit -m test"
```

预期：输出必须包含精确 standalone verification 命令，以及 `Do not chain this verification with git commit`。

## 后续修复（2026-07-22）：消除 git add 死循环 + 复合命令自动 preflight

### 触发场景

真实 transcript（session `cfe0d0e2`，hall-of-fame 仓库把 README 版本号 `1.0.0 → 1.0.1` 再 push）显示：一个「改一行 + push」的任务被放大成 **16 次 git 调用、3 次拦截**（1 次 Shared-State Git Gate + 2 次 Pre-Commit Scope Gate）。上面 2026-07-11 的恢复契约修复解决了「链式验证观察不到」的问题，但暴露出下面两个新根因。

### 根因 A：git add 反复作废已完成的 scope 验证

- `git add` 命中 `looksLikeWriteLikeBashCommand` 的 `"git add"` 标记，被 `classifyShellIntent` 归为 `shellIntentWorkspaceWrite`；因此 `traceCreatesDelta` 与 `traceStagesChanges` 对它都返回 true。
- 旧实现 `preCommitScopeVerified` 用 `lastDeltaIndex`（含 git add）定位「最近一次 delta」，要求三件套验证出现在它**之后**。
- 但三件套里的 `git diff --cached` 只有 staged 之后才有意义 → 必须先 `git add`；而 `git add` 又被算成新 delta → 把刚跑完的验证作废。于是「编辑 → 验证 → git add → commit」这一最自然顺序**永远过不了**，模型只能反复重排、反复被拦。

**修复**：`preCommitScopeVerified` 改用新增的 `lastContentDeltaIndex`，它显式跳过 `git add`（暂存已改内容不产生新的工作区内容变更），只认真正的内容变更（Edit/Write/写类 bash）。post-action delta gate 仍用原 `lastDeltaIndex`（git add 仍算 delta），行为不变。净效果：只要在最近一次**内容编辑**之后跑过一次完整三件套验证，后续 `git add` 不再作废它；无任何验证的裸 commit 仍被拦截。

### 根因 B：复合命令整个关闭了自动 preflight

- runtime 本可「拦截 → 自动帮跑只读 preflight（**原命令不执行**）→ 模型重发原命令」一步收敛，但 `canAutoPreflightGitCommand` 旧实现要求命令**单段**（`len(segments)==1`）。
- 模型的自然写法是 `cd repo && git add … && git commit … && git push`（多段）→ 自动 preflight 全程不触发 → 每次拦截都弹回模型手动处理。

**修复**：`canAutoPreflightGitCommand` 改为接受「`cd` 前缀 + 纯 git 暂存/共享状态操作（add/commit/push/tag）」的流水线；新增白名单 `autoPreflightSafeGitSubcommand`。刻意**排除只读 git**（status/diff/log/rev-parse）——「验证 && commit」链式写法仍保留原有「不要 chain」教学拦截；混入任何非 git 命令（如 `rm -rf`）也拒绝。

**安全边界（重要）**：Fix B 只是把 2026-07-16 已存在的**安全** auto-preflight 协议（帮跑只读检查、**原命令不执行**、模型重发前先看到 preflight 输出）扩展到复合命令，**不是**已否决的「方案 2」（系统替模型自动重放原命令）。模型仍会在重发前检视 preflight 结果，gate 价值不被架空。参见 [../superpowers/plans/2026-07-16-closure-gate-friction-reduction.md](../superpowers/plans/2026-07-16-closure-gate-friction-reduction.md) §五。

### 提示词一致性说明（有意保留的松紧差）

`actionsSection()`（[../../internal/query/query.go](../../internal/query/query.go)）仍写着「Before running git commit, first run … after your latest edit or **staging change**」，即比修复后的代码更严（引导在 git add 之后再验证）。**这是有意保留、不做对齐的**，不是待修项：

- 三件套里的 `git diff --cached` 只有在 `git add` 之后才显示真正会被提交的内容；提示词引导「暂存后再验证」是语义正确的最佳实践。若放宽成「latest edit 之后」，反而可能诱导模型在暂存前验证、看到空的 staged diff，**降低**验证质量。
- 死循环的根因在代码（`preCommitScopeVerified` 曾把 git add 当作作废验证的 delta），已由 Fix A 修掉，与提示词措辞无关；两种写法在调用数/轮次上也无差别。
- 「代码比提示词宽松」本身是健康的防御纵深：提示词引导最佳实践，代码容忍合理偏差、不再因此死锁。二者不必逐字对齐。

### 效果

同样的 README 版本号 +1 再 push：
- 改前：16 次 git 调用 / 3 次拦截（含 git add 死循环）。
- 改后：模型的 `git add && git commit && git push` 一行 → 1 次自动 push preflight → 模型重发一次 → 全部执行。塌缩为 **1 preflight + 1 执行**（端到端测试实测 `tool_calls=2, turns=3`）。

### 改动与测试

- 代码：[../../internal/query/closure_gate.go](../../internal/query/closure_gate.go)（`preCommitScopeVerified`、新增 `lastContentDeltaIndex`；重写 `canAutoPreflightGitCommand`、新增 `autoPreflightSafeGitSubcommand`）。
- 测试：[../../internal/query/query_test.go](../../internal/query/query_test.go)
  - `TestPreCommitScopeGateAllowsCommitWhenGitAddFollowsFullVerification`（A）
  - `TestPreCommitScopeGateStillBlocksCommitWithoutAnyVerification`（A 防回归）
  - `TestCanAutoPreflightGitCommandHandlesGitOnlyCompoundCommands`（B，6 子例）
  - `TestCompoundGitPushOneLinerCollapsesToSinglePreflightAndRetry`（A+B 端到端）
- 全量 `go test ./...` 退出码 0（69 包 ok），原有 5 个 gate 测试（chained/partial/staged/blocks）全部仍通过。
