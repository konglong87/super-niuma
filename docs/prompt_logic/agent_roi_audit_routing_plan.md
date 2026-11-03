# Agent ROI Audit Routing 修复与完善技术方案

更新时间：2026-07-07

## 2026-07-07 实施记录

状态：`IMPLEMENTED_TESTED_REPLAYED`

本轮已按最小高 ROI 路径落地 `repo_health_audit` 第一阶段能力，不复刻原版内部协议，不启动 APG 大 matrix。

实际改动：

- `internal/query/query.go`：在 code mode runtime status 中新增 `Repository health audit strategy` 动态策略卡。只有审计/优化/健康/发版准备类 prompt 触发；普通 README 总结不触发。
- 策略卡明确要求先建立仓库级事实表，再深读文件；事实表覆盖 git status/log/tags、package/version/engine metadata、README/INDEX/count consistency、hooks/slash command/user entrypoints、stale project memory/generated files。
- 策略卡内置 Claim Verification Loop：declared version vs git tags、README counts vs directories、documented commands vs registered skills/scripts、package metadata vs docs。
- 策略卡内置 ROI Ranking Contract：P0 为发布/安装/更新/入口/首次运行风险，P1 为上下文质量和可信文档误导，P2 为结构清理、自动化、维护性和风格一致性。
- `internal/tools/bash/bash.go`：保留普通任务优先 Read/Grep/Glob/LS 的原则，同时补充 repository-wide read-only audit 例外，允许 Bash 聚合 git tag/log/status、find/wc、目录计数循环和 read-only rg pipeline。
- `internal/query/query_test.go`：新增审计 prompt 注入策略卡、README 总结 prompt 不注入策略卡的回归测试。
- `internal/tools/bash/bash_test.go`：新增 Bash description 中审计例外的回归断言，并保留原有“普通读文件优先专用工具”的断言。

已运行验证：

```bash
go test ./internal/query -run 'TestSession(InjectsRepoHealthAuditStrategyForAuditPrompt|DoesNotInjectRepoHealthAuditStrategyForReadmeSummary)' -count=1
go test ./internal/tools/bash -run TestBashToolSchemaIncludesCompatibleBackgroundFields -count=1
go test ./internal/query -count=1
go test ./internal/tools/bash -count=1
go test ./... -count=1
```

当前边界：

- 本轮验证的是 request-level 策略注入和工具描述策略，不声称 open-world superiority。
- 本轮没有启动 APG 大批次，也没有机械复刻原版 Claude Code 私有提示协议。
- 已补充 `superPM` 只读 replay 验收；仍不声称 open-world superiority，后续如需量化稳定性应继续用多次 replay 或 synthetic fixture，而不是一次结果外推。

## 2026-07-08 superPM replay 验收记录

状态：`REPLAYED_WITH_FIX`

执行了两层 replay 验收：

1. **PTY prompt-dump smoke**
   使用本地 fake Anthropic SSE server + 真 CLI PTY 执行，覆盖：
   - 审计 prompt 命中 audit strategy。
   - 审计 prompt 包含 ROI contract。
   - README 总结 prompt 不注入 audit strategy。

2. **真实模型只读 replay**
   在 `$HOME/GolandProjects/superPM` 执行只读审计：

   ```bash
   GOLANG_CLAUDE_CODE_DUMP_PROMPT_JSON=/tmp/go-claude-superpm-replay-20260707235651.jsonl \
   GOLANG_CLAUDE_CODE_DUMP_PROMPT_FULL=1 \
   go run ./cmd/golang-cc \
     --cwd $HOME/GolandProjects/superPM \
     --max-turns 8 \
     --tools Bash,Read,LS,Grep,Glob \
     --disallowedTools Write,Edit,MultiEdit,WebSearch,WebFetch,WebBrowser,Task,TaskOutput,AskUserQuestion,TodoWrite,NotebookEdit,Workflow,Worktree \
     --no-session-persistence \
     -p '熟悉当前项目，然后客观分析是否有需要优化的地方，并说出原因。只读审计，不要修改文件，不要联网；请按 P0/P1/P2 排序，优先关注发布、安装、更新、入口命令和首次体验风险。'
   ```

   replay 输出文件：`/tmp/go-claude-superpm-replay-20260707235651.txt`

   观察到的正向证据：
   - 模型先建立跨仓库事实表，而不是只总结 README。
   - 使用 Bash 聚合 `git tag`、版本、metadata、目录计数等 repo-wide 事实，并用 Read/LS 做关键文件跟进。
   - 最终 P0 第一项为 `VERSION v2.4.3` 但最新 git tag 为 `v2.4.2`。
   - 最终 P0 第二项为 `package.json` engines `>=0.15.0` 与 README badge `Claude Code >=2.0.0` 不一致。
   - 最终答案也识别了 README/INDEX/package 计数不一致、`/pm-status` 无对应 skill、CHANGELOG/gitignore 等问题。

   replay 暴露的边界：
   - 该混合 prompt 因包含“发布、安装、更新”被正确分类为 `release_readiness_audit`，实际注入的是 `Release readiness audit strategy`，不是泛化 `Repository health audit strategy`。
   - 原 `release_readiness_audit` 策略卡没有明确把“启动提示/文档化 slash command 不存在”作为高优先级入口风险，导致真实输出把 `/pm-status` 缺失排到 P2。
   - Bash 权限层把 `2>/dev/null` 识别为写 `/dev/null` 并阻止了一次只读 git diff 命令；这不影响策略命中，但说明 replay 命令应避免 shell 重定向到工作区外路径。

   已根据 replay 修正：
   - `Release readiness audit strategy` 的事实表扩展到 first-run entrypoint state。
   - release checklist 增加 documented startup/slash commands vs registered skills/scripts。
   - release ROI contract 明确：已文档化启动/入口命令无法执行属于 P0 口径；missing documented startup/slash-command findings 应优先于 CHANGELOG lag、stale docs、structural cleanup。
   - 新增 targeted regression test，锁住 replay 型混合 prompt 会命中 release readiness，并携带 entrypoint ROI contract。

## 2026-07-07 Task Classifier 架构落地方案

目标：把当前单一 `repo_health_audit` 判断升级成轻量、可测试、可扩展的 task classifier。第一阶段只做 runtime strategy routing，不引入新 agent 框架，不做大 prompt 重构。

### 目标架构

```text
User Prompt
  -> normalize prompt text
  -> negative/explicit-scope guard
  -> focused task guard
  -> task type classifier
  -> strategy card renderer
  -> runtime status injection
```

### 第一阶段 Task Type

| Task Type | 是否注入策略卡 | 第一阶段用途 |
| --- | --- | --- |
| `repo_health_audit` | 是 | 当前已验证的项目健康/优化/问题审计策略，强调仓库事实表、claim verification 和 ROI 排序。 |
| `release_readiness_audit` | 是 | 发版、版本、tag、安装、升级相关请求，优先检查版本源、tag、CHANGELOG、package metadata 和安装脚本。 |
| `entrypoint_audit` | 是 | 首次体验、hook、slash command、CLI/script/skill 入口相关请求，优先检查文档入口是否真实存在并可执行。 |
| `doc_review` | 否 | README 总结、文案润色、指定文档审阅，不默认跑仓库级审计。 |
| `code_change` | 否 | 修 bug、实现功能、重构、具体 issue 修复，保持现有最小改动和测试路径。 |
| `unknown` | 否 | 不确定时不注入审计策略，避免让普通任务多跑 repo-wide audit。 |

### 分类优先级

分类器必须按以下顺序判断，避免高风险误触发：

1. **用户显式否定优先**
   例如“不审计整个项目”“not a repo audit”时，不注入 repo audit 策略。

2. **聚焦代码任务优先排除**
   `fix bug`、`panic`、`implement`、`refactor`、`修复`、`实现`、`重构` 等请求优先归为 `code_change`，即使文本中出现 `project issue`。

3. **文档任务优先排除**
   README 总结、文案润色、翻译、格式调整等归为 `doc_review`，不触发 repo-wide audit。

4. **高置信审计类型优先**
   `release readiness` / `发版准备` 优先归为 `release_readiness_audit`；`entrypoint` / `hook` / `slash command` / `missing command` 相关审计优先归为 `entrypoint_audit`。

5. **通用项目健康审计兜底**
   只有同时具备项目/仓库作用域和审计/优化/风险/问题意图时，才归为 `repo_health_audit`。

### 策略卡设计原则

- 每个 task type 的 strategy card 必须短，强调“先建事实表，再深读关键文件”。
- Evidence checklist 只作为方向，不要求模型逐字执行固定命令。
- Tool policy 只对 repository-wide read-only aggregate facts 放宽 Bash；focused file reads/edits 仍回到专用工具。
- Answer contract 必须围绕该 task type 的 P0/P1/P2 口径，不把所有问题都套进发布/入口风险。

### 第一阶段验收

本阶段不要求真实模型证明 open-world superiority，只要求完成以下可测试闭环：

- `TaskClassifier` table tests 覆盖正向、负向和冲突 prompt。
- 审计类 request 中出现对应 strategy card。
- `doc_review` / `code_change` request 中不出现审计 strategy card。
- Bash description 仍保留普通任务优先专用工具，同时允许 repo-wide read-only aggregate audit。
- `go test ./internal/query -count=1`、`go test ./internal/tools/bash -count=1`、`go test ./... -count=1`、`git diff --check` 通过。

### 2026-07-07 第一阶段实现记录

状态：`IMPLEMENTED_TESTED`

已按上述架构完成第一阶段工程化改造：

- `internal/query/query.go` 新增 `runtimeTaskType` 与 `runtimeTaskClassification`，把原来的单一 `isRepoHealthAuditPrompt(...)` 路径升级为 `classifyRuntimeTaskPrompt(...) -> runtimeTaskStrategySection(...)`。
- `repo_health_audit`、`release_readiness_audit`、`entrypoint_audit` 分别渲染独立 strategy card；`doc_review`、`code_change`、`unknown` 不注入审计策略。
- 分类优先级已按方案落地：显式否定优先，聚焦代码任务优先排除，文档任务优先排除，再识别 release readiness / entrypoint / repo health。
- 保留 `isRepoHealthAuditPrompt(...)` 作为兼容 helper，但它现在只代表 `repo_health_audit`，不再把 release readiness 或 entrypoint audit 混为 repo health。
- `internal/query/query_test.go` 新增 task classifier matrix，并新增 release readiness / entrypoint strategy card 注入测试；README 总结类任务断言不出现任何 audit strategy。
- `repo_health_audit` strategy card 已显式固定 synthetic ROI ranking contract：当 evidence 同时支持时，version/tag、package engine、missing entrypoint 必须优先于 CHANGELOG lag、stale docs 和 structural cleanup。

第一阶段已覆盖的可靠性边界：

- `Fix project issue #123: parser panics on empty input.` 被归为 `code_change`，不触发审计策略。
- `读取 README.md，总结项目做什么` 和 README 文案润色被归为 `doc_review`，不触发审计策略。
- `Check release readiness before we publish this package.` 触发 `Release readiness audit strategy`，不再落入泛化 repo health。
- `Audit this repository for user entrypoint risks and missing commands.` 触发 `User entrypoint audit strategy`。
- request-level synthetic acceptance 已覆盖高 ROI 排序合同，避免后续改 strategy card 时丢掉“版本/安装/入口优先于 CHANGELOG/结构清理”的核心目标。

剩余边界：

- 当前仍是轻量关键词/规则分类器，不是模型语义分类器；复杂混合意图仍可能需要后续 prompt dump / replay 证据调优。
- 第一阶段仍只验证 request-level routing 和 ROI contract，不证明真实模型最终答案稳定优先命中最高 ROI P0；后续仍应做真实 `superPM` replay。

## 2026-07-07 方案复审：通用性与下一步调整

结论：当前优化方案的通用性是 **中高**，但只应定位为 `repo_health_audit` 任务族的第一阶段策略，不应扩展成所有任务的默认行为。

### 2026-07-07 可靠性加固记录

为降低误触发并提升长期可扩展性，当前实现不再只依赖少量直接关键词，而是采用更保守的三层判断：

1. **明确审计类强触发**
   `repo health`、`project audit`、`repository audit`、`release readiness`、`项目健康`、`项目审计`、`项目体检`、`发版准备`、`熟悉当前项目` 等明确表达直接触发。

2. **项目/仓库作用域 + 审计意图组合触发**
   泛化词如“优化”“问题”“风险”“gap”“improve”必须与 `project/repository/repo/codebase/项目/仓库/代码库` 等作用域同时出现，避免“优化这个函数”“检查这个文件的问题”被误判为仓库审计。

3. **负向和聚焦代码任务优先排除**
   当用户明确说“不审计整个项目”，或请求更像 `fix bug`、`panic`、`implement`、`refactor`、`修复`、`实现`、`重构` 这类聚焦代码修改时，不注入 repo audit strategy。

新增测试矩阵覆盖：

- 正向触发：中文当前项目审计、英文 repo health audit、release readiness、项目优化审计、entrypoint 风险审计。
- 负向不触发：README 总结、函数级优化、具体 project issue bugfix、README 文案润色。

这组加固的目标是：

- **通用性**：保留跨软件仓库适用的审计链路，而不是绑定 `superPM`。
- **可靠性**：降低普通阅读、文档润色、单点 bugfix 的误触发。
- **稳定性**：用 table-driven tests 固定触发边界，避免后续添加关键词时回归。
- **可扩展性**：后续可以把三层判断自然扩展为 `release_readiness_audit`、`entrypoint_audit`、`security_audit` 等 task classifier，而不是继续堆单一策略卡。

### 为什么通用性较高

当前方案没有写死 `superPM` 的具体缺口，而是抽象成了维护者视角下常见的仓库审计链路：

```text
Task Type -> Audit Strategy -> Evidence Checklist -> Claim Verification -> ROI Ranking -> Answer Contract
```

这条链路对大量软件仓库都成立：

- 版本声明是否与 git tag / release 事实一致。
- README 安装要求是否与 `package.json`、`go.mod`、`pyproject.toml`、插件 manifest 等 metadata 一致。
- 文档、hook、slash command、script 中提到的用户入口是否真实存在且可执行。
- README / INDEX / skill catalog / docs 中的模块数量或索引是否与实际目录一致。
- 项目 memory、生成文件、历史文档是否会误导 agent 或维护者。
- 最终答案是否按用户影响面排序，而不是把文档洁癖、结构清理排在发布/安装/入口风险之前。

因此，它对以下任务族有较高复用价值：

| 任务族 | 通用性 | 说明 |
| --- | --- | --- |
| 项目健康审计 | 高 | 用户要求“熟悉项目、客观分析问题、找优化点”时，应先建立仓库级事实表。 |
| 发版/安装准备检查 | 高 | version、tag、engine、README、CHANGELOG、安装脚本天然需要交叉验证。 |
| 用户入口链路检查 | 高 | hook、slash command、script、skill、CLI 子命令缺失会直接影响首次体验。 |
| agent 上下文质量审计 | 中高 | stale memory、过期 docs、generated files 会影响模型判断，但不同项目证据源不同。 |

### 为什么不是全任务通用

当前策略不适合无条件注入到所有请求，原因如下：

1. **触发器仍是保守关键词分类**
   当前实现通过 `repo health`、`project audit`、`release readiness`、`项目健康`、`熟悉当前项目`、`需要优化的地方` 等词触发。它足够低风险，但不是完整语义分类器：含蓄审计请求可能漏触发，具体 bug 修复请求也可能因为出现 `project issue` 类词而误触发。

2. **Evidence checklist 偏软件工程仓库**
   `git tag`、package engine、README count、hook/slash command 对 Go/Node/plugin/skill repo ROI 很高；但对纯内容仓库、设计资产仓库、数据集仓库、论文仓库，最高 ROI 证据源可能是数据 freshness、schema、license、引用、生成流程或资产完整性。

3. **ROI 排序口径偏发布/安装/入口链路**
   对 repo health audit 这是正确优先级；但对 security audit、performance audit、architecture audit，P0 的定义不同。例如安全任务中 secret 泄露、权限扩大、供应链风险应高于 tag 不一致。

4. **当前实现是 request-level strategy，不是硬性 planner**
   测试证明策略卡会出现在审计类 request 中，普通 README 总结不会出现；但它不能保证模型每次都完整执行 checklist，也不能单独证明最终输出稳定优先命中最高 ROI P0。

### 方案调整方向

不建议把当前策略继续加长，也不建议只堆更多关键词。更好的演进方向是把它升级成轻量任务分类器，每个任务族有自己的策略卡、证据 checklist 和 ROI contract。

建议下一阶段拆成：

| Task Type | 触发表达 | 核心 evidence | P0 排序口径 |
| --- | --- | --- | --- |
| `repo_health_audit` | 熟悉项目、找优化点、项目健康、客观分析问题 | git 状态、版本 metadata、README/INDEX、入口命令、stale memory | 发布/安装/入口/首次体验风险 |
| `release_readiness_audit` | 发版、升级、版本、tag、release、安装要求 | VERSION、tag、CHANGELOG、package metadata、安装脚本 | 不能安装、不能升级、版本发现错误 |
| `entrypoint_audit` | 命令不可用、hook、slash command、首次使用 | hooks、scripts、skills、CLI commands、README usage | 用户按文档执行失败 |
| `security_audit` | 安全、权限、secret、token、sandbox | permissions、env、logs、redaction、dangerous tools | 泄密、越权、破坏性操作 |
| `architecture_audit` | 架构、可扩展、高可用、模块边界 | module graph、storage/API contracts、shared abstractions、tests | 阻塞扩展、破坏边界、核心路径不可维护 |
| `doc_review` | 总结 README、润色文档、补文档 | 用户指定文档和相关引用 | 文档准确性和可读性，不默认跑 repo-wide audit |
| `code_change` | 修 bug、实现功能、改代码 | 相关文件、调用链、测试 | 行为正确性、回归风险、最小改动 |

### 下一步验收建议

下一步不应直接宣称该策略“通用有效”，而应补三类证据：

1. **触发矩阵测试**
   增加 prompt table：审计类 prompt 必须触发；README 总结、单点 bug 修复、文档润色不触发；release readiness / entrypoint audit 可先标为未来分类。

2. **synthetic fixture replay**
   构造最小仓库：version/tag 不一致、README engine 不一致、hook 引用不存在命令、CHANGELOG 滞后。验收最终排序必须把 tag/engine/entrypoint 排在 CHANGELOG 和结构清理之前。

3. **真实 `superPM` replay**
   使用同 cwd 和同类 prompt 做一次 prompt dump / transcript 复测，检查首轮工具倾向是否从普通文档阅读转向仓库级事实表，并检查最终 P0 是否优先命中 version/tag、engine mismatch、missing `/pm-status`、README count mismatch。

### 复审后的判断

当前方案应该保留，因为它修的是一个真实且高 ROI 的能力缺口：项目审计任务缺少动态策略层。它的价值不在于“多读几个文件”，而在于把模型从普通文档总结路由到维护者视角的事实核验和影响面排序。

但后续扩展必须保持边界：

- 不把 `repo_health_audit` 策略当作所有任务的默认策略。
- 不让 Bash 例外吞掉普通读文件、搜索和编辑任务。
- 不把 checklist 固化成所有仓库的唯一审计标准。
- 不用单次 replay 证明 open-world superiority，只证明该任务族上的策略路由改善。

## 背景

本轮对比任务：

```text
cwd: $HOME/GolandProjects/superPM
用户请求: 熟悉当前项目，然后客观分析是否有需要优化的地方，并说出原因
对照对象: go-claude vs 原版 Claude Code 入口 ccsd
```

用户观察到的关键现象：

- 同模型、同 cwd、同类提示下，`ccsd` 能优先找到最高 ROI 的项目缺口。
- `go-claude` 也能找到真实问题，但优先级更散，漏掉或弱化了更直接影响用户和发布链路的问题。

本文目标不是复刻某一段原版提示词，而是把差距抽象成可修复的 runtime 能力：

```text
Task Type -> Audit Strategy -> Evidence Checklist -> ROI Ranking -> Answer Contract
```

## 当前结论

### P0 根因：缺少任务类型到审计策略的动态路由

`熟悉当前项目并客观分析是否需要优化` 这类请求，本质不是普通读文档任务，而是维护者视角的 repo health / release readiness audit。

原版 Claude Code 在本轮表现中隐式选择了更高 ROI 的审计路径：

1. 发布链路：`VERSION` 与 git tag 是否一致。
2. 安装约束：`README` 与 `package.json engines` 是否一致。
3. 用户入口：hook / slash command 是否引用不存在命令。
4. 文档索引：README / INDEX / 实际目录数量是否一致。

`go-claude` 更像普通文档结构审计：

1. 读取 README / CHANGELOG / INDEX / SKILL。
2. 列出目录。
3. 总结文档过时、目录冗余、CHANGELOG 滞后等问题。

这些问题多数真实，但不是本任务最高 ROI。

置信度：高。

证据：

- `go-claude` 本轮工具轨迹以 `Read` / `LS` 为主，没有优先执行 `git tag`、`git log`、横向目录计数、hook command existence check。用户提供的输出保存在 `$HOME/.codex/attachments/fe99e85d-37cb-4814-8bd3-616e6c18f4b9/pasted-text.txt`。
- `superPM` 当前 `VERSION` 为 `v2.4.3`，但最新 git tag 为 `v2.4.2`，这是发布/升级链路缺口。
- `superPM/package.json` 中 `engines.claude-code` 是 `>=0.15.0`，而 `README.md` badge 要求 `Claude Code >=2.0.0`。
- `superPM/hooks/session-start.sh` 提示用户使用 `/pm-status`，但仓库中没有对应 `pm-status` skill。
- `superPM/README.md` 方案设计模块写 `7个`，实际 `skills/02-solution-design` 有 8 个。

### P0 根因：工具策略偏置降低了审计效率

Go Claude 当前 Bash 工具描述强调：

- Bash 用于 build/test/package/git 等。
- 读文件、搜索、列目录时优先使用 Read/Grep/Glob/LS。
- 不要用 Bash 执行 `grep/rg/find/ls/sed/awk` 等专用工具能完成的动作。

这个策略对普通代码编辑是合理的，但对项目健康审计会产生副作用：

- 横向统计和一致性验证需要 `git tag`、`find ... | wc -l`、目录循环、组合 `rg`。
- 如果模型被强约束到 Read/LS/Grep，会更容易碎片化阅读，而不是先建立仓库级事实表。

证据：

- `internal/tools/bash/bash.go` 的 `Description()` 当前要求优先使用专用工具，并禁止用 Bash 做 `grep/rg/find/ls/sed/awk` 类动作。
- `internal/tools/grep/grep.go` 的 `Description()` 当前要求搜索任务使用 Grep，避免 Bash `rg`。
- `internal/query/query.go` 的 `runtimeToolPlanningStatus(...)` 在工具结果较多时提醒“如果已有足够证据就停止探索”，这对普通任务省 token，但对审计任务可能过早收束。

置信度：高。

### P1 根因：缺少 Claim Verification Loop

高质量项目审计不是“读到什么说什么”，而是验证仓库声明是否被实际状态支持。

本轮高 ROI 缺口都属于 claim verification：

| 声明 | 验证动作 | 结果 |
| --- | --- | --- |
| `VERSION=v2.4.3` | `git tag -l 'v*' --sort=-v:refname` | 最新 tag 仍是 `v2.4.2` |
| README 要求 Claude Code `>=2.0.0` | 读 `package.json engines` | `>=0.15.0` 不一致 |
| hook 提示 `/pm-status` | 搜索 skill/command 名称 | 无对应 skill |
| README 模块数量 | 实际目录计数 | 方案设计实际 8 个，不是 7 个 |

`go-claude` 当前没有把“声明 -> 反查事实 -> 影响面排序”作为通用审计框架。

置信度：高。

### P1 根因：缺少 ROI Ranking Contract

`go-claude` 找到问题后，没有强制按用户影响面排序。

建议排序维度：

1. 是否影响安装、升级、发布、tag、版本发现。
2. 是否影响首次使用、入口命令、hook、slash command。
3. 是否影响 agent 读取错误上下文或执行错误流程。
4. 是否影响核心文档可信度。
5. 是否只是长期结构清理、风格一致性或文档洁癖。

没有这个 ranking contract 时，模型容易把真实但低 ROI 的问题排成 P0。

置信度：中高。

### P2 影响因素：stale project memory/context

`superPM/CLAUDE.md` 当前仍写旧阶段和旧版本。Go Claude `status` 显示会加载该文件作为 Project memory，且该文件体积约 17KB。

这会让模型更容易关注“项目状态过时”“历史文档结构”等问题，而不是先从 git/tag/package/hook 建立当前事实。

该因素会放大偏差，但不是唯一根因。

置信度：中。

## 目标架构

新增一个轻量但明确的 Agent Capability Layer：

```text
User Request
  -> Task Classifier
  -> Strategy Card
  -> Evidence Checklist
  -> Tool Policy Override
  -> ROI Ranking
  -> Answer Contract
```

### 1. Task Classifier

识别以下请求类型：

| 类型 | 用户表达 | 默认策略 |
| --- | --- | --- |
| repo_health_audit | 熟悉项目、看看哪里可优化、客观分析问题、项目是否健康 | 先做仓库级事实审计，再读关键文件 |
| release_readiness_audit | 发版、版本、安装、更新、tag、marketplace | 优先版本/tag/package/changelog/update script |
| user_entrypoint_audit | 首次体验、命令不可用、hook、slash command | 优先入口命令和注册链路 |
| code_change | 修 bug、实现功能、改代码 | 读相关文件、最小实现、测试 |
| doc_review | 检查文档、润色、补文档 | 文档结构和一致性优先 |

第一阶段只需要支持 `repo_health_audit`，不要做大而全分类器。

### 2. Strategy Card

当识别为 `repo_health_audit` 时，向模型注入短策略卡：

```text
You are doing a repository health audit, not a normal document summary.
First build a cross-repo fact table before reading deeply:
- git status, recent commits, tags
- package/version/engine metadata
- README/INDEX/count consistency
- hooks/slash commands/user entrypoints
- stale project memory or generated files
Prefer read-only aggregate shell commands for repository-wide facts.
Rank findings by user impact and release/install risk before documentation cleanliness.
```

### 3. Evidence Checklist

对 `repo_health_audit` 建议最小 checklist：

```bash
git status --short --branch
git log --oneline -30
git tag -l 'v*' --sort=-v:refname | head -20
find . -maxdepth 3 -name SKILL.md | sort
rg -n "version|engines|claude-code|DEPRECATED|TODO|pm-status|/pm-" README.md package.json CHANGELOG.md CLAUDE.md hooks skills .claude-plugin
```

注意：这不是强制每次逐字执行，而是 runtime/prompt 层应该鼓励模型先形成仓库级事实表。

### 4. Tool Policy Override

在 repo audit 场景中，局部放宽 Bash 描述：

```text
For repository-wide read-only audits, Bash is appropriate for aggregate facts:
git status/log/tag, find/wc, directory loops, and read-only rg pipelines.
Use dedicated tools for focused file reads and edits after the fact table is built.
```

这不是否定现有专用工具策略，而是补一个上下文条件：

- 普通代码编辑：仍优先 Read/Grep/Glob/LS。
- 仓库审计：允许 Bash 聚合事实。

### 5. ROI Ranking

最终回答必须区分：

| 层级 | 定义 |
| --- | --- |
| P0 | 影响安装、发布、更新、入口命令、用户首次体验或直接导致错误执行 |
| P1 | 影响 agent 上下文质量、文档可信度、维护者判断 |
| P2 | 结构清理、长期演进、风格一致性、自动化增强 |

并要求每条 finding 至少包含：

- 证据文件/命令。
- 影响面。
- 为什么是该优先级。
- 最小修复建议。

## 最小高 ROI 改动建议

### 改动 1：新增 repo audit strategy section

位置候选：

- `internal/query/query.go`：根据 prompt 文本生成 runtime planning section。
- 或新增小函数 `repoAuditStrategySection(prompt string) string`，在 code mode request 中追加到 runtime status / system reminder。

触发词第一版可保守：

```text
熟悉当前项目
客观分析是否有需要优化
项目是否有问题
需要优化的地方
repo health
project audit
release readiness
```

预期影响：

- 让模型先建立仓库级事实表。
- 提升发布/入口/版本类缺口命中率。

风险：

- 可能让普通“读 README 总结项目”任务多跑 git/tag。

缓解：

- 只在包含“优化/问题/缺口/健康/客观分析”等词时触发。

验证：

- prompt dump/golden 检查该 section 是否存在。
- fake model acceptance：当用户请求项目优化审计时，首轮应偏向 `Bash git tag` 或 read-only aggregate audit。

### 改动 2：Bash 工具描述增加 repo audit 例外

位置：

- `internal/tools/bash/bash.go`

新增语义：

```text
For repository-wide audits, Bash is appropriate for read-only aggregate commands such as git tag/log/status, find/wc, and directory-count loops.
```

预期影响：

- 减少模型在审计任务中过度使用 Read/LS 的倾向。

风险：

- 模型可能更频繁用 Bash 做简单读文件。

缓解：

- 明确只限 repository-wide read-only aggregate audit。

验证：

- 单测固定 Bash description 包含该例外。
- APG 或本地 fake task 检查审计类任务可以选择 aggregate Bash。

### 改动 3：新增 Claim Verification reminder

位置候选：

- `internal/query/query.go`

内容：

```text
When auditing a project, verify claims against source of truth:
- declared version vs git tags
- README counts vs directories
- documented commands vs registered skills/scripts
- package metadata vs docs
Do not rank a finding high until you can state the concrete user impact.
```

预期影响：

- 让模型主动做“声明 vs 事实”交叉验证。

风险：

- 可能增加 1-2 轮工具调用。

验证：

- 构造 fixture：README 声明 3 个 skill，实际 4 个；hook 引用不存在命令；期望最终 P0/P1 排序命中。

### 改动 4：新增 ROI answer contract

位置候选：

- runtime status section。
- 或作为 `repo_health_audit` strategy card 的最后一段。

内容：

```text
Rank findings by user impact:
P0 release/install/update/entrypoint breakage
P1 stale context or trusted docs misleading agents/users
P2 cleanup/automation/maintainability
```

预期影响：

- 解决“找到了真实问题但不是最高优先级”的核心问题。

验证：

- golden/fake acceptance 检查最终输出把缺 tag、engine mismatch、missing command 排在 CHANGELOG cleanup 前。

## 建议验收用例

### 用例 1：superPM repo audit replay

输入：

```text
熟悉当前项目，然后客观分析是否有需要优化的地方，并说出原因
```

cwd：

```text
$HOME/GolandProjects/superPM
```

期望 P0 至少命中：

1. `VERSION=v2.4.3` 但 git tag 最新只有 `v2.4.2`。
2. `package.json engines.claude-code >=0.15.0` 与 README `>=2.0.0` 不一致。
3. `hooks/session-start.sh` 提示 `/pm-status`，但无对应 skill。
4. README 方案设计模块数量与实际目录不一致。

期望 P1/P2 可命中：

1. CHANGELOG 滞后。
2. `CLAUDE.md` 过时。
3. 废弃/临时文件和目录清理。
4. 双语 docs 目录策略不清。

通过标准：

- P0 排序必须优先于 CHANGELOG/CLAUDE.md/结构清理。
- 每个 P0 都必须有本地证据。
- 不得声称 open-world superiority，只能说明当前任务上的策略改进。

### 用例 2：最小 synthetic repo audit fixture

构造临时仓库：

- `VERSION=v1.2.0`
- git tag 只有 `v1.1.0`
- README 写 3 个 commands
- 实际 commands 只有 2 个
- package engine 与 README 不一致
- CHANGELOG 缺当前版本

期望：

- 模型先查 git/tag/package/README，而不是只读 README。
- P0 为 tag/engine/missing command。
- CHANGELOG 为 P1 或 P2。

### 用例 3：非审计任务不触发

输入：

```text
读取 README.md，总结项目做什么
```

期望：

- 不注入 repo audit strategy。
- 不强制跑 git tag/find/wc。

## 实施顺序

1. 文档冻结本方案。
2. 加最小 repo audit strategy section 和单测。
3. Bash description 增加 repo audit 例外和单测。
4. 加 synthetic prompt/golden acceptance。
5. 用 superPM 任务做一次 live replay，对比是否命中高 ROI P0。
6. 若有效，再考虑把 strategy router 扩展到 release readiness、entrypoint audit、doc review 等更多任务类型。

## 暂不做

- 不大改系统 prompt。
- 不默认所有任务都跑 git/tag/find。
- 不机械复刻原版 Claude Code 内部协议。
- 不用 APG 大 matrix 验证这个阶段；先用 targeted prompt dump / synthetic fixture / superPM replay 验证。

## 成功标准

本阶段成功不是“输出更长”，而是：

1. 能识别项目审计类任务。
2. 能先建立仓库级事实表。
3. 能验证声明与事实是否一致。
4. 能按用户影响面和发布/入口风险排序。
5. 能把真实但低 ROI 的问题降级。

最终目标：

```text
go-claude 在项目审计任务中，不只是找到更多问题，而是先找到最值得修的问题。
```
