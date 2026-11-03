# Agent Repair Safety Closure 通用修复方案

更新时间：2026-07-08

状态：`IMPLEMENTED_FULL_TESTS_PASSING`

## 2026-07-08 实现进度与证据

本轮已按方案完成 P0/P1 的最小高 ROI 落地：

- `Edit` / `MultiEdit` 小文件路径现在保留现有文件 mode，不再把 `0755` 脚本重写成 `0644`。
- `Edit` / `MultiEdit` / `Write` 的 `FileChange` 记录 `BeforeMode`、`AfterMode`、`ModeChanged`，用于 trace/session 后续扩展。
- code-mode runtime 增加保守 repair classifier，覆盖 `targeted_repair`、`release_repair`、`entrypoint_repair`、`metadata_repair`。
- repair strategy card 要求改前识别不变量，改后运行 scope/metadata/semantic 验证，并在最终回答拆分 local edit、commit、branch push、tag push 和剩余动作。
- 成功执行 `Edit` / `MultiEdit` / `Write` / `NotebookEdit` / write-like `Bash` 后，tool result 自动追加 `Post-edit verification required` reminder。
- reminder 固定要求 `git status --short`、`git diff --name-status`、`git diff --summary`，并按脚本/hook、metadata、release/version、README/INDEX 文件追加语义检查。
- Bash 描述保留普通任务优先 Read/Grep/Glob/LS，同时补充 repo audit 与 release repair 的 read-only shell verification 例外。

已通过 targeted tests：

```bash
go test ./internal/files -run 'TestReplaceDetailedPreserves.*Mode|TestMultiReplaceDetailedPreserves.*Mode' -count=1
go test ./internal/tools/fileedit -run 'TestEditToolPreservesExecutableMode|TestMultiEditPreservesExecutableMode' -count=1
go test ./internal/tools/filewrite -count=1
go test ./internal/tools/bash -run TestBashToolSchemaIncludesCompatibleBackgroundFields -count=1
go test ./internal/query -run 'TestRepairStrategy|TestPostEdit|TestScriptEdit|TestPackageEdit|TestRuntimeTaskClassifierMatrix|TestSessionInjectsSpecificAuditStrategies|TestSessionDoesNotInject' -count=1
```

已通过最终验收：

```bash
go test ./internal/query -count=1
go test ./internal/tools/fileedit -count=1
go test ./internal/tools/filewrite -count=1
go test ./internal/tools/bash -count=1
go test ./... -count=1
git diff --check
```

PTY smoke 已覆盖：

```bash
go test ./internal/query -run 'TestRepairStrategyClassifierInjectsSafetyContract|TestScriptEditReminderRequiresExecutableVerification' -count=1
```

实现边界：

- 本轮没有自动执行 force push、移动/删除远端 tag，也没有新增会扩大默认权限的能力。
- Git release closure 当前通过 runtime contract 和 post-edit reminder 强制模型验证对象关系；还没有引入自动 tag object resolver 或 UI 专用 release 状态面板。
- TUI/WebUI 对 `ModeChanged` 的专门可视化仍属 P2；结构化字段已经落入 `FileChange`，后续可接入显示层。

## 2026-07-08 方案自查补充

上一版方案已经覆盖了文件 mode preservation、repair classifier、post-edit reminder 和 Git release closure，但还缺少一条必须显式工程化的闭环：

> 每次文件修改后，agent 必须立刻验证“这次实际改动是否 100% 落在本次修复范围内”，再继续下一步、提交或宣称完成。

这不是最后统一跑测试可以替代的事情。最后测试只能证明某些行为仍通过，不能证明本次 edit 没有引入 mode-only diff、无关文件修改、错误 tag 指向、生成文件漂移或未提交/未 push 的状态误判。

因此本方案新增 **Post-Edit Change Verification Loop**，作为 P0 必做项，位于工具层 mode preservation 和最终 release closure 之间。

## 背景

`repo_health_audit` / `release_readiness_audit` 优化后，go-claude 在项目审计任务中能更高 ROI 地发现版本、安装、入口链路问题。但 superPM replay 后续修复暴露出新缺口：

- 审计阶段能正确发现 `/pm-status` 幽灵引用、`package.json` engines 冲突、版本/tag 不一致。
- 修复 `/pm-status` 时，`Edit` 工具把 `hooks/session-start.sh` 从 `100755` 改成 `100644`，导致 hook 脚本失去可执行权限。
- tag 修复阶段把“远端已有 `v2.4.3` tag”误判成“发布问题已修复”，但实际还需要校验 tag 指向、branch push 状态、远端 tag 是否包含修复 commit。

结论：上一阶段补齐的是 **Audit Strategy / Evidence Checklist / ROI Ranking**；本阶段要补齐的是 **Repair Action Safety / Post-Change Verification / Git Release Closure**。这不是针对 superPM 的特例，而是所有 agent 执行修复时都会遇到的通用稳定性问题。

## 本地证据

### 1. Edit 小文件路径会丢 mode

当前小文件 `Edit` 路径：

- `internal/tools/fileedit/fileedit.go::Tool.Run` 调用 `files.ReplaceDetailed(...)`。
- `internal/files/largefile.go::ReplaceDetailed` 在小文件路径用 `writeAtomic(path, []byte(after), 0644)` 写回。
- `internal/files/largefile.go::MultiReplaceDetailed` 在小文件路径也用 `writeAtomic(path, []byte(after), 0644)`。
- `writeAtomic(...)` 会按传入 mode `Chmod` 临时文件，再 `Rename` 覆盖原文件。

因此任何原本是 `0755` 的小文本文件，被 `Edit` / `MultiEdit` 修改后都会被重写成 `0644`。这类问题不会被内容 diff 发现，必须检查 file mode 或由工具层保留元数据。

### 2. 大文件路径已经有类似保护

`MultiReplaceDetailed` 大文件路径在最终 rename 前会：

```go
if info, err := os.Stat(path); err == nil {
    _ = os.Chmod(tmp, info.Mode())
}
```

说明正确设计不是复杂能力，而是小文件路径缺少同样的 mode preservation。

### 3. Git 发布闭环不完整

superPM 当前状态曾出现：

```text
HEAD=5cad1b5
origin/main=d26355e
local tag v2.4.3=d26355e
remote tag v2.4.3=72f7781...
status=main...origin/main [ahead 1]
```

这说明“tag 存在”不等于“release 已修复”：

- 修复 commit 可能还没 push 到 branch。
- local tag 可能不指向修复 commit。
- remote tag 可能已存在但指向另一个 commit。
- 如果 tag 已发布且指向错误对象，是否移动 tag 是高风险共享状态操作，不能静默处理。

## 根因分层

### R0：工具层破坏文件元数据

`Edit` / `MultiEdit` 修改文本内容时没有保留原文件 mode。模型即使选择了正确文件和正确替换，也会因工具实现引入副作用。

这类问题不能靠提示彻底避免；必须由工具层默认保护。

### R1：修复阶段没有 Action Verification Contract

审计 prompt 注入了 Claim Verification Loop，但用户后续说“修复 X”时进入普通 code-change 路径。修复阶段缺少下面的合同：

- 改动前识别被修改对象的关键不变量。
- 改动后验证内容、权限、可执行性、引用链路和 Git 状态。
- 最终回答只能声明已验证过的范围，不能把未 push / tag conflict / skipped test 说成完成。

### R2：Git 发布语义被过度简化

当前模型容易把 `git push origin tag` 失败中的“remote already exists”解释为成功。但发布语义需要比较对象关系：

- local tag object
- remote tag object
- HEAD / release commit
- origin/main
- version files / package metadata 所在 commit

只看 tag 名称存在会产生假阳性。

### R3：最终答案缺少“状态边界”

模型回答“P0 全部修复”时，没有把本地修改、commit、branch push、tag push、远端 tag 指向拆开说明。用户需要的是可执行状态，不是意图完成状态。

## 目标

本方案要让 go-claude 在所有修复任务中默认避免类似问题，而不只是修复 superPM 的 `/pm-status`：

1. **工具层不破坏元数据**
   编辑普通文本文件时保留原 mode、可执行位和常规权限位；除非用户明确要求 chmod。

2. **高风险文件改动有后验验证**
   hooks、scripts、package metadata、version files、CI config、release manifests、generated indexes 等文件被修改后，要检查内容 diff 和元数据 diff。

3. **发布/安装/入口链路有专门闭环**
   version/tag/package/install/entrypoint 类修复必须验证 local 与 remote 的事实一致性。

4. **最终回答只声明已验证事实**
   没有 push 就说“本地已提交，未 push”；tag 已存在但对象不一致就说“存在冲突，需要人工确认是否移动 tag”。

5. **方案可扩展**
   通过 risk classifier、tool metadata、verification contracts 扩展到更多任务族，而不是继续堆长 prompt。

## 总体架构

```text
Tool Execution
  -> preserve file metadata by default
  -> record before/after file metadata in FileChange
  -> immediately verify actual change scope after every write/edit
  -> classify changed files and commands into repair risk categories
  -> inject post-change verification guard when needed
  -> require final answer state boundary
```

### 核心原则

- **工具层能保证的，不交给模型记忆。**
  文件 mode preservation 必须在 `internal/files` 里解决。

- **模型能规划的，要给明确合同。**
  修复任务要看到 “改完后必须检查哪些不变量”。

- **发布状态必须用对象关系验证。**
  tag/branch/release 不看名字是否存在，要看 commit object 是否正确。

- **每次修改后先验证实际 diff，再继续。**
  agent 不能只验证“目标字符串已替换”，还要确认没有无关内容、权限、生成物、Git 状态副作用。

- **不扩大默认权限。**
  检查可以 read-only 自动建议；移动远端 tag、force push、删除 tag 仍需用户明确确认。

## P0 设计：文件编辑元数据保护

### 改动 1：小文件 Edit/MultiEdit 保留原 mode

目标文件：

- `internal/files/largefile.go`

设计：

1. 新增 helper：

```go
func existingFileMode(path string, fallback os.FileMode) os.FileMode {
    info, err := os.Stat(path)
    if err != nil {
        return fallback
    }
    return info.Mode().Perm()
}
```

2. `ReplaceDetailed` 小文件路径改为：

```go
mode := existingFileMode(path, 0644)
err = writeAtomic(path, []byte(after), mode)
```

3. `MultiReplaceDetailed` 小文件路径同样使用原 mode。

4. 大文件路径保持现有 preserve 行为，但建议统一调用同一个 helper，减少分叉。

5. `Write` 新建文件仍可默认 `0644`，因为 Write 是创建/覆盖语义；但如果 Write 未来允许覆盖已有文件，也应保留原 mode 或在描述里明确会重置 mode。

验收测试：

- `TestEditToolPreservesExecutableMode`
  - 创建 `script.sh`，mode `0755`。
  - 用 `Edit` 替换文本。
  - 断言内容变化且 mode 仍为 `0755`。

- `TestMultiEditPreservesExecutableMode`
  - 同上，覆盖 `MultiEdit`。

- `TestEditToolPreservesNonExecutableMode`
  - 创建 `config.json`，mode `0640` 或 `0600`。
  - 替换文本后 mode 不变。

- 若测试环境不稳定支持完整 POSIX mode，至少在非 Windows 下跑 mode 断言。

### 改动 2：FileChange 记录元数据

目标文件：

- `internal/tools/tool.go`
- `internal/tools/fileedit/fileedit.go`
- 使用 `FileChange` 的 TUI/WebUI/session 记录链路

设计：

给 `tools.FileChange` 增加可选字段：

```go
BeforeMode os.FileMode
AfterMode  os.FileMode
ModeChanged bool
```

要求：

- `Edit` / `MultiEdit` 产生 FileChange 时记录 before/after mode。
- 如果 mode changed，工具结果或后续 runtime reminder 必须显式暴露。
- 字段只做内部结构化记录，不改变模型工具 schema。

验收：

- 单测断言 `FileChange` callback 能拿到 mode。
- 若发生 mode change，TUI/日志/trace 至少一个可见面能显示 `mode changed 100755 -> 100644`。

### 改动 3：新增元数据回归 fixture

增加一个 synthetic repair fixture：

```text
repo/
  hooks/session-start.sh 0755
```

任务：

```text
Replace /old-command with /new-command in hooks/session-start.sh.
```

验收：

- 内容被替换。
- mode 仍是 `100755`。
- 最终回答不能声称只用内容验证完成，必须能看到 mode preservation 或 `git diff --summary` 无 mode change。

## P0 设计：Post-Edit Change Verification Loop

### 为什么必须独立成环

实际执行过程暴露了一个关键问题：agent 在 `Edit` 后验证了业务语义，例如 “`/pm-selfcheck` skill 存在”，但没有验证实际 diff 的完整范围，所以遗漏了 `100755 -> 100644` 的权限副作用。

这个问题不能只靠最终 `go test ./...` 解决：

- mode-only diff 可能不影响 Go 测试，但会让 hook/script 运行失败。
- README/package/VERSION 修改可能通过测试，但发布 metadata 仍不一致。
- tag/branch 可能本地看起来存在，但远端对象关系错误。
- 生成文件或索引文件可能被手改，测试未覆盖。

因此，每次 `Edit` / `MultiEdit` / `Write` / `NotebookEdit` / 可写 Bash 后，都要进入一个短的变更验证环。

### 验证环触发点

必须触发：

- `Edit`
- `MultiEdit`
- `Write`
- `NotebookEdit`
- `Bash` 中包含写操作，例如 `chmod`、`mv`、`cp`、`sed -i`、`perl -pi`、`git tag`、`git commit`、`git push`

不触发：

- 纯 `Read` / `Grep` / `Glob` / `LS`
- read-only Bash，例如 `git status`、`git diff`、`rg`、`find`、`wc`

### 四层验证

#### Layer 1：Scope Verification

目标：确认本次实际修改的文件集合和用户授权范围一致。

推荐命令：

```bash
git status --short
git diff --name-status
```

要求：

- 只允许出现本次修复需要的文件。
- 如果出现额外文件、生成文件、lockfile、权限变化、未跟踪文件，必须停下来解释，不能继续提交。
- 如果工作区本来不干净，必须区分“本轮改动”和“既有改动”，不能混入 commit。

#### Layer 2：Metadata Verification

目标：确认文件权限、类型和关键元数据没有被意外改变。

推荐命令：

```bash
git diff --summary
git diff --raw
```

重点检查：

- `old mode -> new mode`
- symlink 变 regular file，或 regular file 变 symlink
- rename/delete/create 是否符合预期
- executable bit 是否保留

阻断条件：

- 未经用户要求的 mode change。
- hook/script 失去 executable bit。
- symlink 形态变化。
- rename/delete 不在用户要求范围内。

#### Layer 3：Semantic Verification

目标：确认改动解决了目标问题，且没有破坏关联声明。

按文件类型选择验证：

| 改动类型 | 必须验证 |
| --- | --- |
| hook/script/entrypoint | 引用命令存在；脚本仍可执行；必要时 `bash -n` 或最小 dry run |
| package metadata | README / package / manifest / lockfile 声明一致 |
| version/tag/release | VERSION、package version、local tag、remote tag、HEAD、origin/main 对象关系一致 |
| README/INDEX/count | 文档计数与目录/metadata 实际值一致 |
| generated/index files | 确认是否应运行生成器，而不是手改 |

示例：

```bash
rg -n "/pm-selfcheck|/pm-status" hooks skills README.md
test -x hooks/session-start.sh
node -e 'const p=require("./package.json"); console.log(p.engines)'
```

#### Layer 4：Targeted Behavioral Verification

目标：用最小、确定性的测试证明修复有效。

规则：

- 优先跑最小 targeted test / lint / parser / dry-run。
- 再根据风险决定是否跑全量测试。
- 如果没有现成测试，使用 read-only deterministic shell pipeline 验证事实，不创建临时测试文件来伪造覆盖。

示例：

```bash
bash -n hooks/session-start.sh
git diff --check
go test ./internal/tools/fileedit -run TestEditToolPreservesExecutableMode -count=1
```

### 验证环状态机

```text
write/edit applied
  -> collect changed files
  -> verify scope
  -> verify metadata
  -> verify semantic target
  -> run targeted behavioral check
  -> only then continue / commit / final
```

如果任一环节失败：

```text
verification failed
  -> stop
  -> report exact failed invariant
  -> fix only that invariant or ask user
  -> rerun the same failed verification
```

### Prompt / Runtime Contract

修复类策略卡必须加入：

```text
After each Edit/MultiEdit/Write or write-like Bash command, verify actual change scope before continuing:
- git status --short
- git diff --name-status
- git diff --summary for mode/type changes
- targeted semantic check for the repaired invariant
Do not commit or claim completion if unrelated files, unexpected mode changes, unverified tag/branch state, or failed checks remain.
```

### Tool Result Reminder

当写工具执行后，下一轮 request 应追加结构化 reminder：

```text
## Post-edit verification required
- changed_path: hooks/session-start.sh
- required_scope_check: git diff --name-status
- required_metadata_check: git diff --summary
- required_semantic_check: verify referenced slash command exists and script remains executable
- completion_blocker: do not claim completion until these checks pass or the remaining risk is disclosed
```

这个 reminder 应由 runtime 基于文件路径和工具类型生成，而不是依赖模型自己记住。

### 验收测试

- `TestPostEditReminderRequiresScopeAndMetadataChecks`
  - fake model 执行 `Edit` 后，下一轮 request 必须包含 `git diff --name-status` 和 `git diff --summary`。

- `TestScriptEditReminderRequiresExecutableVerification`
  - 修改 `hooks/session-start.sh` 后，下一轮 request 必须包含 executable bit / script hook 验证要求。

- `TestPackageEditReminderRequiresMetadataConsistency`
  - 修改 `package.json` 后，下一轮 request 必须包含 README/manifest/source-of-truth consistency 验证要求。

- `TestFinalAnswerContractRejectsUnverifiedModeChange`
  - fake transcript 中出现 mode change 且未验证时，final contract reminder 不允许声明完成。

### 与最终测试的关系

Post-Edit Verification Loop 不替代最后验收；它负责“本次改动范围正确”。最后验收负责“整体项目仍通过质量门禁”。

推荐顺序：

```text
每次写后：scope + metadata + semantic + targeted behavioral check
提交前：git status --short + git diff --check + relevant targeted tests
最终：按项目规则跑 go test ./... -count=1 / full gate
```

## P0 设计：修复阶段 Action Verification Contract

### 新增 Repair Task Classifier

当前 classifier 主要服务审计任务。下一阶段要增加修复任务族：

| Task Type | 触发语义 | 策略 |
| --- | --- | --- |
| `targeted_repair` | 修复某个具体问题、替换引用、改配置 | 改前识别文件不变量，改后检查 diff/status/test |
| `release_repair` | 修复版本、tag、发布、安装、升级 | 校验 version/package/tag/remote/branch |
| `entrypoint_repair` | 修复 hook、slash command、CLI/script、首次使用 | 校验命令存在、脚本可执行、引用链路正确 |
| `metadata_repair` | 修复 package.json、README badge、INDEX/count、manifest | 校验源数据和声明一致 |

第一阶段不需要大模型分类器，仍用保守规则：

- 用户说“修复 / 改 / replace / update / align / 统一 / 修正”且包含 version/tag/release/install/package => `release_repair`。
- 包含 hook/slash command/script/entrypoint/首次体验/命令 => `entrypoint_repair`。
- 包含 package/README/INDEX/count/manifest/metadata => `metadata_repair`。
- 其他具体修复 => `targeted_repair`。

负向边界：

- 纯问答“有哪些问题”“解释原因”不进入 repair。
- 用户明确“先不改”时不进入 repair。
- review-only 不进入 repair。

### Repair Strategy Card

当识别为修复任务，在 runtime status 中注入短策略卡：

```text
## Repair safety strategy
- Before editing, identify file invariants that must not change: executable bit, symlink/regular-file status, generated-vs-source ownership, package/version/tag source of truth, and user entrypoints.
- After editing, verify both content and metadata: git diff --summary, git diff --check, focused tests or deterministic read-only checks.
- For scripts/hooks/entrypoints, preserve executable mode unless explicitly asked to chmod, and verify the referenced command exists.
- For release/version/tag fixes, verify local HEAD, origin/main, local tag object, remote tag object, and the version/package files are all consistent before claiming release completion.
- Final answer must separate local edit, commit, branch push, tag push, and remaining remote/manual actions.
```

该策略卡应短而硬，不替代工具层修复。

验收：

- “先只修复 /pm-status 幽灵引用” 命中 `entrypoint_repair`。
- “在修复 engines 问题” 命中 `metadata_repair` 或 `release_repair`。
- “创建 v2.4.3 tag 并推送” 命中 `release_repair`。
- “解释这个函数为什么慢” 不命中 repair。
- “先不改，只分析修复方案” 不命中 repair。

## P0 设计：Git Release Closure

### 发布不变量

对 release/version/tag/install 类任务，最终声明完成前必须检查：

```bash
git status --short --branch
git rev-parse --short HEAD
git rev-parse --short origin/main
git tag -l <tag> --format='%(refname:short) %(objectname:short)'
git ls-remote --tags origin refs/tags/<tag>
```

根据任务还要检查：

```bash
cat VERSION
node -e 'console.log(require("./package.json").version)'
git show --raw --format=short <tag> -- VERSION package.json
```

### 结果判定

| 状态 | 能否宣称完成 | 正确表述 |
| --- | --- | --- |
| local edit done, uncommitted | 否 | “本地已修改，尚未提交” |
| committed, branch ahead | 否 | “本地已提交，尚未 push branch” |
| local tag exists but not pushed | 否 | “本地 tag 已创建，远端未同步” |
| remote tag exists same object | 是，仅 tag 维度 | “远端 tag 指向目标提交” |
| remote tag exists different object | 否 | “远端 tag 冲突，需要确认是否移动/重建 tag” |
| tag points old commit before fix | 否 | “tag 存在但不包含修复 commit” |

### 高风险操作保护

以下操作必须显式请求确认，不能由模型自动执行：

- `git tag -f`
- `git push --force` / `git push -f`
- `git push origin :refs/tags/<tag>`
- 删除远端 tag
- 重写已发布 release tag

如果用户只说“推送”，默认允许普通 `git push` 或 `git push origin <tag>`；如果失败且远端对象不一致，必须停止并报告。

## P1 设计：高风险改动后自动提醒

仅靠初始 strategy card 仍可能在多轮修复中衰减。建议增加 post-tool reminder：

当 `Edit` / `MultiEdit` / `Write` 修改以下文件时，在下一轮 request 的 tool result 后追加系统提醒：

| 文件/路径 | reminder |
| --- | --- |
| `*.sh`, `hooks/**`, `scripts/**`, `.github/workflows/**` | 检查 `git diff --summary`、可执行 bit、语法或最小运行 |
| `package.json`, `go.mod`, `pyproject.toml`, `Cargo.toml`, plugin manifests | 检查 metadata 与 docs/source-of-truth 一致 |
| `VERSION`, `CHANGELOG*`, release docs | 检查 tag/release/version source of truth |
| README/INDEX/catalog | 检查声明 vs 实际目录/metadata |
| generated files | 确认是否应手改，或运行生成器 |

示例 reminder：

```text
<system-reminder>
You edited a script or hook. Before claiming completion, verify file metadata and entrypoint behavior:
- run git diff --summary for mode changes
- ensure executable bit is preserved unless the user requested chmod
- verify referenced commands/scripts exist
</system-reminder>
```

验收：

- 修改 `hooks/session-start.sh` 后，下一轮 request 包含 script/hook reminder。
- 修改 `package.json` 后，下一轮 request 包含 metadata consistency reminder。
- 修改普通 `.md` 文案不触发高风险 reminder。

## P1 设计：最终答案状态合同

修复类任务最终回答必须按实际状态拆分：

```text
Changed:
- ...

Verified:
- ...

Not done / boundary:
- branch push: not done
- tag push: failed because remote tag points ...
- tests: not run
```

禁止：

- 只因为命令名存在就宣称入口链路已修复。
- 只因为 tag 名称存在就宣称 release 已修复。
- 忽略 `git status` ahead/dirty 状态。
- 忽略 mode-only diff。

可通过 prompt-dump / fake-model acceptance 测试锁住。

## P1 设计：工具描述更新

### Edit/MultiEdit 描述

在工具描述中补充：

- Edit/MultiEdit preserve existing file mode.
- For scripts/hooks, verify executable bit if it matters.

注意：这只是辅助。真正保证仍在工具层。

### Bash 描述

保持现有 read-only audit 例外，同时增加 release repair 允许的 read-only verification：

- `git status --short --branch`
- `git diff --summary`
- `git rev-parse`
- `git ls-remote --tags`
- `git show --raw --format=short`

不要鼓励用 Bash 代替简单 Read/Edit。

## P2 设计：可观测和 UI

### Trace / TUI / WebUI 显示 mode-only changes

如果 `FileChange.ModeChanged` 为 true：

- TUI tool result 展示 `mode 100755 -> 100644`。
- WebUI local trace 展示 mode-only diff。
- 递交给模型的 tool result 也应可见。

### Diff summary helper

可考虑增加内部 helper，用于在 commit 前或 final 前生成简洁状态：

```text
files changed:
- hooks/session-start.sh content changed, mode preserved 100755
- package.json content changed
git:
- branch ahead origin/main by 1
- tag v2.4.3 local d26355e, remote 72f7781, HEAD 5cad1b5
```

该 helper 不替代 `git`，只把关键信息结构化给模型。

## 验收矩阵

### Unit Tests

```bash
go test ./internal/files -run 'TestReplaceDetailedPreserves.*Mode|TestMultiReplaceDetailedPreserves.*Mode' -count=1
go test ./internal/tools/fileedit -run 'TestEditToolPreservesExecutableMode|TestMultiEditPreservesExecutableMode' -count=1
go test ./internal/query -run 'TestRepairStrategy|TestPostEditHighRiskReminder|TestReleaseRepair' -count=1
```

### Targeted CLI Smoke

使用 fake provider + prompt dump：

1. 输入 “先只修复 /pm-status 幽灵引用”。
2. fake model 调用 Edit 修改 `hooks/session-start.sh`。
3. 检查：
   - 文件 mode 仍是 `100755`。
   - 下一轮 prompt 包含 script/hook verification reminder。
   - 下一轮 prompt 包含 Post-Edit Verification Loop 的 scope / metadata / semantic checks。
   - final contract 不允许跳过 `git diff --name-status`、`git diff --summary` 和 targeted semantic check。

### Synthetic Git Release Fixture

构造 fixture：

```text
VERSION=v2.4.3
package.json version=2.4.3
HEAD contains fix
origin/main behind
local tag v2.4.3 points old commit
remote tag v2.4.3 points another old commit
```

验收：

- agent 不能说 release fixed。
- 必须报告 tag object mismatch。
- 必须要求用户确认是否移动远端 tag。

### Real replay

对 superPM 重新执行：

1. 审计 replay：确认仍能找到高 ROI P0。
2. 修复 replay：
   - 修 `/pm-status` 后 mode preserved。
   - 修 engines 后 metadata 一致。
   - 处理 tag 时不把 remote already exists 当成功。

## 实施顺序

### Phase 1：工具层硬修复

1. 修 `ReplaceDetailed` / `MultiReplaceDetailed` 小文件 mode preservation。
2. 加 `internal/files` 和 `internal/tools/fileedit` mode regression tests。
3. 更新 Edit/MultiEdit 描述。
4. 跑 targeted tests、`go test ./... -count=1`、`git diff --check`。

这是最高 ROI，因为它直接消灭无提示也无法可靠避免的副作用。

### Phase 2：修复任务策略与提醒

1. 新增 repair task classifier。
2. 新增 repair strategy card。
3. 新增 Post-Edit Change Verification Loop reminder，覆盖每次写后的 scope / metadata / semantic / targeted behavioral verification。
4. 新增 high-risk post-edit reminder。
5. 加 prompt dump / unit tests，覆盖 entrypoint/package/release 三类。

### Phase 3：Git release closure

1. 增加 release repair contract。
2. 增加 synthetic git fixture / fake model acceptance。
3. 明确 remote tag object mismatch 的 stop behavior。

### Phase 4：可观测性增强

1. `FileChange` 增加 before/after mode。
2. TUI/WebUI/trace 显示 mode-only changes。
3. 可选增加 compact status helper，避免长会话丢掉 Git 发布状态。

## 明确不做

- 不复刻原版 Claude Code 私有协议。
- 不靠更长的全局 system prompt 解决工具层 bug。
- 不允许自动 force push、删除远端 tag、移动已发布 tag。
- 不把所有修复任务都强制跑全量测试；根据文件风险选择 targeted verification。
- 不把一次 replay 成功宣传成 100% 稳定。

## 完成定义

本方案完成时必须满足：

1. `Edit` / `MultiEdit` 修改任何现有普通文件时不会改变原 mode。
2. 每次 `Edit` / `MultiEdit` / `Write` / write-like Bash 后，下一轮 request 都能看到 Post-Edit Verification Loop：scope、metadata、semantic、targeted behavioral checks。
3. 修改 hooks/scripts 后，模型能看到并执行 mode/diff summary 和 executable verification 要求。
4. release/tag 修复不再把“tag 名存在”当成完成，必须验证对象指向。
5. 最终回答能区分 local edit、commit、branch push、tag push、remote conflict，以及哪些验证已跑/未跑。
6. superPM 真实 replay 不再出现 executable bit 丢失、tag false-success 或“未验证就宣称修复完成”。
