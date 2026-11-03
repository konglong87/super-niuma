# go-claude 提示词工程与驾驭工程优化方案

> **目标：** 在不更换底层模型（GLM 5.1）的前提下，通过优化提示词工程、工具描述、Agent 驾驭逻辑和上下文学管理，使本项目的任务执行能力接近或达到 Claude Code 的水平。

---

## 目录

1. [现状评估与差距分析](#1-现状评估与差距分析)
2. [优化一：工具描述深度扩展](#2-优化一工具描述深度扩展)
3. [优化二：系统提示词精炼](#3-优化二系统提示词精炼)
4. [优化三：行为约束提示词增强（基于真实案例缺陷修复）](#4-优化三行为约束提示词增强基于真实案例缺陷修复)
5. [优化四：Agent 驾驭逻辑增强](#5-优化四agent-驾驭逻辑增强)
6. [优化五：工具结果后处理与结构化反馈](#6-优化五工具结果后处理与结构化反馈)
7. [优化六：上下文管理与压缩优化](#7-优化六上下文管理与压缩优化)
8. [优化七：子 Agent 提示词增强](#8-优化七子-agent-提示词增强)
9. [测试方案](#9-测试方案)
10. [效果度量与验证指标](#10-效果度量与验证指标)
11. [风险控制与渐进式落地](#11-风险控制与渐进式落地)
12. [实施路线图](#12-实施路线图)

---

## 1. 现状评估与差距分析

### 1.1 差距总览

| 维度 | 当前状态 | Claude Code 水平 | 差距影响 |
|------|---------|-----------------|---------|
| 工具描述 | 1 句话，30-90 字符 | 2-4 句，含用例/限制/最佳实践 | 模型选工具决策盲 |
| 系统提示词 | 7 个节点 ~200 行 | 精调过的 ~500+ 行指令 | 执行质量、错误恢复差 |
| Agent 驾驭 | 简单循环：发消息→调工具→循环 | 含规划→执行→验证→纠错闭环 | 复杂任务成功率低 |
| 工具结果处理 | 原始输出 + 20KB 截断 | 结构化 + 事实提取 + 摘要 | 模型消费效率低 |
| 上下文压缩 | 基本可用，容错机制粗糙 | 精调过，质量高 | 长会话信息丢失 |
| 子 Agent 系统提示 | 一句话 | 继承父 Agent 行为规范 | 子任务执行质量差 |
| Prompt Caching | 有基本实现 | 优化过 | 性能/成本差异 |

### 1.2 根因分析

1. **工具描述过短** → 模型不知道工具的边界、限制、最佳配对使用方式 → 选错工具 / 用错参数
2. **系统提示词缺乏"指导密度"** → 模型在执行时缺少具体的步骤约束和决策框架 → 随意发挥
3. **Agent 循环没有"内省"机制** → 模型在复杂场景下不会自我纠正，一条路走到黑
4. **工具结果"裸奔"** → 模型要从大量原始输出中自己提取有用信息，浪费 token 且容易遗漏
5. **子 Agent 是"裸模型"** → 子任务几乎全靠模型"本能"，没有行为规范

### 1.3 真实案例验证：优化方向的必要性

以下案例直接对比了本项目与 Claude Code 在**同一任务、同一模型**下的执行差异，验证了上述根因分析的准确性。

#### 案例描述

**任务：** 向一个"智能查数" skill 中添加一个新表的定义。该 skill 的架构如下：

- **Skill 主文件 A**：包含表名、表简介、常用查询字段（少量细节）
- **公共表结构文件夹 B**：被 A 索引引用，包含完整的表结构（字段类型、默认值、连表信息、索引等）
- **依赖关系**：A 中的表名通过命名约定索引到 B 中的完整定义

**用户输入：** 提供新表的 CREATE TABLE SQL 语句（含 `NOT NULL`、`DEFAULT ''` 等精确字段规格）以及表简介。

#### 执行结果对比

| 维度 | 本项目 | Claude Code |
|------|--------|-------------|
| 主文件 A 更新 | ✅ 修改了 | ✅ 修改了 |
| 公共文件 B 更新 | ❌ **完全遗漏** — 模型不知道 A 依赖 B | ✅ 同步更新 |
| 字段默认值 | ❌ 写成了 `DEFAULT NULL`，与 SQL 定义 `DEFAULT ''` 不一致 | ✅ 精确复现 |
| 精度评级 | Claude Code review 认为 60% 正确 | 40% 细节丢失 |

#### 从本案例提取的 5 个系统性缺陷

| # | 缺陷 | 模型行为 | 根因 |
|---|------|---------|------|
| 1 | **缺乏横向依赖感知** | 只知道改当前文件 A，没检查 A 引用了哪些其他文件 | 提示词中无"变更影响范围分析"要求 |
| 2 | **缺乏精确复现约束** | 将 `DEFAULT ''` 简化为 `DEFAULT null`，用"合理值"替代了"精确值" | 提示词未强调"逐字复现用户规格" |
| 3 | **缺乏跨文件一致性护栏** | A 中的新表名在 B 中找不到对应定义，形成孤立引用 | 提示词无"所有关联文件同步更新"约束 |
| 4 | **缺乏结构化验证** | 改完直接认为自己完成了，没反向读 B 确认一致性 | 提示词的"验证"太笼统，无具体验证方法 |
| 5 | **模型知识覆盖用户规格** | 模型用自己的常识推断默认值，覆盖了用户明确指定的值 | 无"用户输入 > 模型知识 > 推测"信息优先级层级 |

#### 本案例的意义

这 5 个缺陷的修复**改动量极小**（纯文本添加），但影响极大——它们直接决定了模型在**涉及依赖关系、精确规格、多文件协同**等实际工程场景中的表现优劣。后续的 [优化三](#4-优化三行为约束提示词增强基于真实案例缺陷修复) 将专门针对这 5 个缺陷设计解决方案。

---

## 2. 优化一：工具描述深度扩展

### 2.1 修改位置

所有 `internal/tools/<tool>/<tool>.go` 文件中的 `Description()` 方法和 `InputSchema()` JSON 中的 `description` 字段。

### 2.2 工具描述模板（Standard Tool Description Template）

每个工具的描述应包含以下要素：

```
<一句话总结>

<用例指南>
- 何时使用此工具（优先于其他工具的时机）
- 何时不应使用此工具（应使用其他工具的时机）

<行为限制>
- 输出大小限制
- 超时限制
- 已知的行为约束

<错误处理>
- 常见失败原因
- 重试建议
```

### 2.3 具体工具描述优化

#### Bash（`internal/tools/bash/bash.go`）

```go
func (Tool) Description() string {
    return `Run a shell command in the current working directory and return combined stdout/stderr.

Use Bash for: running build/test tools, package managers (npm/go/pip), git operations,
starting dev servers, and terminal utilities that cannot be done with dedicated tools.
Prefer other tools over Bash: use Read for reading files, Write/Edit for changing files,
Glob for finding files, Grep for searching file contents, and LS for listing directories.

Output is limited to 200 KB. Commands timeout at 30s by default (configurable via timeout_ms).
When you need a long-running process, use '&' and monitor separately.

If a command fails, check stderr in the output for the actual error. Common failures:
command not found (install missing dependency), permission denied (check file modes),
network timeout (retry with longer timeout or check connectivity).`
}
```

同时优化 `command` 参数的 description：

```
"command": {"type": "string", "description": "Shell command to run. Use full paths when possible. For multi-line scripts, use && or ; to chain commands. Prefer simple commands over complex pipes."}
```

#### Read（`internal/tools/fileread/fileread.go`）

```go
func (Tool) Description() string {
    return `Read a UTF-8 text file from the local filesystem and display with line numbers.

Use this BEFORE making changes to any file you haven't recently inspected.
Always read a file before editing it, unless you just wrote it.

For large files, use offset/limit to read specific sections, or chunk_index
to read page-by-page. The default output shows up to 2000 lines.

Binary files, images, and very large files (>10MB) will be detected and may
show limited content. Use the chunk_index parameter to paginate through
large files.`
}
```

#### Write（`internal/tools/filewrite/filewrite.go`）

```go
func (Tool) Description() string {
    return `Write a complete UTF-8 text file. This replaces the file if it already exists.

Prefer Edit for small changes to existing files — Write replaces the entire file,
which is more error-prone for partial modifications. Use Write when:
- Creating a new file
- The file needs a complete rewrite (more than ~40% changed)
- The file is small enough to reproduce in full

The file path is relative to the current working directory.
Always verify the file path before writing — writing to the wrong path
can silently overwrite important files.`
}
```

#### Edit（`internal/tools/fileedit/fileedit.go`）

```go
func (Tool) Description() string {
    return `Replace one exact string in an existing UTF-8 text file.

Prefer Edit over Write for targeted changes — it's safer because it only modifies
the specified text. The old_string must appear exactly once in the file
(use replace_all:true to allow multiple occurrences).

White space and indentation in old_string MUST match the file exactly.
Read the file first to copy the exact text. Trailing spaces matter.

MultiEdit is preferred when you need to make multiple edits to the same file
— it applies all changes atomically so partial failures don't corrupt the file.`
}
```

#### Grep（`internal/tools/grep/grep.go`）

```go
func (Tool) Description() string {
    return `Search text files with a regular expression and return file:line:match results.

Use Grep instead of "grep" in Bash — it's faster and returns structured results.
The pattern is a Go regular expression (RE2 syntax). Use output_mode to control verbosity:
- "content" (default): shows matched lines with file and line numbers
- "files_with_matches": only lists file paths (great for finding which files match)
- "count": shows match count per file (great for quantifying)

Results are limited to 1000 matches. For broad searches, narrow with glob filtering
(e.g., glob:"**/*.go") or scope the path parameter. Directories like node_modules,
.git, vendor are skipped by default.`
}
```

#### Glob（`internal/tools/glob/glob.go`）

```go
func (Tool) Description() string {
    return `Find files by glob pattern. Supports *, ?, and ** across directories.

Use Glob to discover file paths before reading them, or to find files matching
a pattern. Results are sorted by modification time (newest first).

Results are limited to 1000 entries. Common patterns:
- **/*.go — all Go files recursively
- src/**/*.ts — all TypeScript files under src/
- *.json — JSON files in current directory only

For searching file CONTENTS, use Grep instead. For listing directories, use LS.`
}
```

#### WebSearch（`internal/tools/websearch/websearch.go`）

```go
func (Tool) Description() string {
    return `Search the web and return a concise list of result titles and URLs.

Use when you need current information not available in the local codebase:
documentation, news, package versions, API references, error solutions.

Results come from a web search engine. For full page content, follow up with WebFetch.
Domain filtering uses the current network sandbox policy.

For specialized searches (code, documentation, technical references),
consider WebFetch with known documentation URLs instead.`
}
```

### 2.4 优化预期效果

- **工具选择准确率提升** — 模型更清楚何时用 Bash 何时用 Glob/Grep
- **参数使用正确率提升** — 描述里明确说了 `output_mode` 的三种选择及其用途
- **错误处理能力提升** — 工具说清了常见失败原因和重试建议
- 估计可减少约 **20-30%** 的无效工具调用

### 2.5 回归风险

- ✅ 只改 `Description()` 和 `InputSchema` 中的 description 字段字符串
- ✅ 不改任何运行逻辑、参数结构、输入输出格式
- ✅ 所有现有测试不受影响（工具描述不在测试断言中）
- ⚠️ 如果 Description 跨行过长，需确认 JSON schema 中的 description 字段没有字符串长度限制

---

## 3. 优化二：系统提示词精炼

### 3.1 修改位置

`internal/query/query.go` 中的 `simpleSystemSection()`、`doingTasksSection()`、`usingToolsSection()`、`actionsSection()`、`toneAndStyleSection()`、`outputEfficiencySection()` 以及新增的 `planningSection()`、`verificationSection()`、`recoverySection()`。

### 3.2 新增提示词章节

#### 3.2.1 任务规划章节（新增 `planningSection()`）

在 `defaultSystemPromptParts()` 中 `doingTasksSection` 之后插入：

```go
func planningSection() string {
    return strings.TrimSpace(`
# Task Planning
- Before starting a multi-step task, briefly outline your approach. This helps you
  catch missing steps and avoid dead ends.
- Break complex tasks into small, verifiable steps. Each step should produce something
  that can be checked (file changed, test passed, output verified).
- If a step fails after 2 attempts, stop and reconsider your approach rather than
  retrying the same thing.
- Prefer tackling one file/component at a time rather than editing many files in
  parallel — this makes rollback easier if something goes wrong.
- When the task involves multiple independent sub-tasks, consider using the Task
  tool to delegate them to sub-agents for parallel execution.
`)
}
```

#### 3.2.2 验证章节（新增 `verificationSection()`）

```go
func verificationSection() string {
    return strings.TrimSpace(`
# Verification
- After making changes, always verify they work as intended before reporting completion.
- Run the most relevant test: if you changed Go code, run "go test ./..."; for npm,
  run "npm test"; for Python, run "pytest" or similar.
- If the project has no automated tests, do a manual verification: run the code,
  check the output, confirm the behavior change.
- If tests fail, analyze the failure output and fix the root cause — don't just
  retry hoping it passes.
- For refactoring tasks, verify that existing functionality is preserved by running
  the pre-refactoring tests and comparing results.
- Report test results honestly — never claim tests pass without running them.
`)
}
```

#### 3.2.3 错误恢复章节（新增 `recoverySection()`）

```go
func recoverySection() string {
    return strings.TrimSpace(`
# Error Recovery
When a tool call fails, follow this decision tree:
1. Parse the error message to identify the failure type
2. For syntax/input errors: fix the input and retry immediately
3. For timeout errors: consider increasing timeout_ms or splitting the work
4. For missing dependencies: install them first, then retry
5. For permission errors: do NOT retry the same call — explain the issue to the user
6. If retrying 2-3 times with the same approach still fails, try a different strategy

When using Bash:
- "command not found" → install the dependency
- "permission denied" → check file permissions
- Network errors → check connectivity, retry with longer timeout
- Compilation errors → fix the code first, don't retry the same command

When using Edit:
- "old_string not found" → re-read the file and match the exact text
- "multiple matches" → use replace_all flag or make old_string more specific
`)
}
```

### 3.3 强化现有章节

#### `usingToolsSection()` 扩展

```go
func usingToolsSection() string {
    return strings.TrimSpace(`
# Using Your Tools
- Prefer dedicated tools over Bash: use Read for reading files, Edit or Write for
  changing files, Glob for finding files, Grep for searching contents, LS for listing
  directories. Only use Bash for operations that truly need a shell.
- Read files before editing them unless you just wrote the file in the same session.
- Use MultiEdit for multiple changes to the same file — it's atomic and safer than
  sequential Edit calls.
- Call independent tools in parallel (same turn) when there's no data dependency
  between them. For example: reading multiple files, searching in different directories.
- For sequential work (read → edit → verify), use TodoWrite to track progress.
- For large or complex sub-tasks, delegate with the Task tool.
- Keep tool use efficient: one Bash command is better than three, one Edit is better
  than a Write that reproduces the entire file.
`)
}
```

#### `doingTasksSection()` 扩展

```go
func doingTasksSection() string {
    return strings.TrimSpace(`
# Doing Tasks
- The user primarily asks for software engineering work: debugging, implementing,
  refactoring, explaining, reviewing, and testing code.
- Before making changes, read the relevant files first. Never suggest changes to
  code you haven't inspected.
- Do not add features, files, abstractions, or compatibility shims beyond what the
  task requires. Solve the problem, don't engineer around it.
- Be vigilant about security: check for command injection, XSS, SQL injection,
  path traversal, and unsafe shell patterns in your suggestions. Fix any issues
  you notice, even if the user didn't ask.
- After making changes, verify with tests or manual inspection. If you cannot
  verify something, say so clearly.
- Report outcomes truthfully — failed tests, errors, and blockers should be
  reported, not hidden.
- If you encounter unexpected code, configuration, or behavior, investigate
  before making assumptions or taking destructive actions.
`)
}
```

#### `toneAndStyleSection()` 扩展

```go
func toneAndStyleSection() string {
    return strings.TrimSpace(`
# Tone And Style
- Be concise. Lead with the answer or action, not the preamble.
- Use GitHub-flavored Markdown for formatting code blocks, lists, and tables.
- Include file_path:line_number references when discussing code.
- No emojis unless the user uses them first.
- Avoid qualifying language ("I think", "maybe", "perhaps") — state your findings
  confidently, and if uncertain, say "I'm not sure about X because Y."
- When reporting work done, follow this structure: what changed → how verified →
  any remaining risks or edge cases.
- Keep user-facing output brief unless the task explicitly asks for explanation.
`)
}
```

### 3.4 系统提示词结构优化

当前结构（`defaultSystemPromptParts()`）的章节顺序建议调整：

**当前顺序：**
1. Identity
2. Intro
3. Simple System
4. Doing Tasks
5. Actions
6. Using Tools
7. Tone And Style
8. Output Efficiency

**建议顺序：**
1. Identity + System (你是谁、你如何工作)
2. **Task Planning** (新) — 规划方法论（在最前面，因为后面所有内容的"总纲"）
3. Doing Tasks — 任务执行规范
4. **Verification** (新) — 验证要求（紧接 Doing Tasks，自然而然的后续步骤）
5. **Error Recovery** (新) — 错误恢复策略（在 Instructions 后，遇到问题时参考）
6. Using Tools — 工具使用指南
7. Actions — 操作安全
8. Tone And Style — 沟通风格
9. Output Efficiency — 输出效率

修改 `defaultSystemPromptParts()` 中的调用顺序。

### 3.5 优化预期效果

- **任务完成率提升** — 模型有更清晰的步骤指南和验证要求
- **错误恢复能力提升** — 模型遇到错误时有决策树可循，而非反复重试同一方法
- **输出质量提升** — 更具体的格式要求减少废话
- 估计可减少约 **15-25%** 的无效回合

### 3.6 回归风险

- ✅ 只新增/修改字符串函数，不修改任何运行时逻辑
- ✅ 新增内容与现有提示词无冲突
- ⚠️ 提示词总长度增加约 500-800 字 → 首轮 token 消耗略增，但每次会话仅新增一次
- ✅ 可通过 Feature Flag 控制是否启用新章节，渐进式上线

---

## 4. 优化三：行为约束提示词增强（基于真实案例缺陷修复）

> **来源：** 本优化基于 [1.3 节](#13-真实案例验证优化方向的必要性) 中实际执行对比提取的 5 个系统性缺陷。这些缺陷的共性是：**模型不是能力不够，而是提示词中没有教它正确的行为边界**。

### 4.1 修改位置

`internal/query/query.go` 中的 `doingTasksSection()`、新增的 `planningSection()`、`verificationSection()`，以及新增的 `precisionSection()`、`dependencyAwarenessSection()`、`informationPrioritySection()`。

### 4.2 缺陷 1：横向依赖感知缺失 → 新增依赖扫描步骤

**问题：** 模型只改了当前文件，没检查文件引用了哪些其他文件，导致公共依赖文件 B 被遗漏。

**修复方案：** 在 `planningSection()`（参见 3.2.1 节）中增加依赖扫描要求：

```go
func planningSection() string {
    return strings.TrimSpace(`
# Task Planning
- Before starting a multi-step task, briefly outline your approach. This helps you
  catch missing steps and avoid dead ends.
- Break complex tasks into small, verifiable steps. Each step should produce something
  that can be checked (file changed, test passed, output verified).
- If a step fails after 2 attempts, stop and reconsider your approach rather than
  retrying the same thing.
- Prefer tackling one file/component at a time rather than editing many files in
  parallel — this makes rollback easier if something goes wrong.
- When the task involves multiple independent sub-tasks, consider using the Task
  tool to delegate them to sub-agents for parallel execution.

## Dependency Awareness
- **Before modifying any file, identify its dependencies.**
  Scan the file for imports, references to other files, shared configs, type definitions,
  or naming conventions that link it to other resources.
- If file A references or indexes file B (e.g., A stores a name key that B defines in detail),
  then modifying A may require updating B as well.
- Maintain a mental checklist: "What else needs to change when this changes?"
- After completing changes, verify there are no orphaned references — every name/key/identifier
  added to A should have a corresponding definition or entry in the files A depends on.
`)
}
```

### 4.3 缺陷 2：精确复现约束缺失 → 新增精确复现指南

**问题：** 模型将用户给的 `DEFAULT ''` 写成了 `DEFAULT null`，用自己的"合理推断"替代了用户的精确规格。

**修复方案：** 在 `doingTasksSection()` 之后新增 `precisionSection()`

```go
func precisionSection() string {
    return strings.TrimSpace(`
# Precision Requirements
- When the user provides explicit specifications (SQL definitions, JSON schemas,
  data structures, configuration values, exact strings), reproduce them VERBATIM.
- Do NOT infer, simplify, "correct", or "complete" the user's specifications.
  Examples of violations:
  - User writes DEFAULT '' → you write DEFAULT null ✗
  - User writes VARCHAR(255) → you write VARCHAR(100) ✗
  - User writes NOT NULL → you omit it ✗
  - User writes a specific key name → you rename it to a "more standard" form ✗
- If a specification seems ambiguous or incomplete, ASK the user — do not fill in
  the gaps yourself.
- When copying values from user input to output files, use mechanical copy-paste
  (manual verification) rather than recall from memory.
`)
}
```

### 4.4 缺陷 3：跨文件一致性护栏缺失 → 强化同步更新要求

**问题：** A 中的新表名在 B 中找不到对应定义，形成了孤立引用。

**修复方案：** 在 `doingTasksSection()` 中强化跨文件约束：

```go
func doingTasksSection() string {
    return strings.TrimSpace(`
# Doing Tasks
- The user primarily asks for software engineering work: debugging, implementing,
  refactoring, explaining, reviewing, and testing code.
- Before making changes, read the relevant files first. Never suggest changes to
  code you haven't inspected.
- Do not add features, files, abstractions, or compatibility shims beyond what the
  task requires. Solve the problem, don't engineer around it.
- Be vigilant about security: check for command injection, XSS, SQL injection,
  path traversal, and unsafe shell patterns in your suggestions. Fix any issues
  you notice, even if the user didn't ask.
- After making changes, verify with tests or manual inspection. If you cannot
  verify something, say so clearly.
- Report outcomes truthfully — failed tests, errors, and blockers should be
  reported, not hidden.
- If you encounter unexpected code, configuration, or behavior, investigate
  before making assumptions or taking destructive actions.

## Cross-File Consistency
- When a change involves multiple related files, ensure ALL of them are updated
  consistently. A partial update (file A changed but file B not) is a failed update.
- If there is a shared resource (public config, shared types, base definitions,
  dependency index files), update the shared resource FIRST, then update consumers.
- After updating, verify that no reference is orphaned — every identifier/name/alias
  added to any file should have a corresponding definition somewhere.
- For skills or modules that index external resources (e.g., a skill file that stores
  a name key pointing to a shared definition folder), modifying the index requires
  verifying the target resource exists and is consistent.
`)
}
```

### 4.5 缺陷 4：结构化验证缺失 → 细化验证方法

**问题：** 模型改完就认为完成了，没有反向验证。提示词中的"verify"太笼统。

**修复方案：** 在 `verificationSection()`（参见 3.2.2 节）中增加具体验证方法：

```go
func verificationSection() string {
    return strings.TrimSpace(`
# Verification
- After making changes, always verify they work as intended before reporting completion.
- Run the most relevant test: if you changed Go code, run "go test ./..."; for npm,
  run "npm test"; for Python, run "pytest" or similar.
- If the project has no automated tests, do a manual verification: run the code,
  check the output, confirm the behavior change.
- If tests fail, analyze the failure output and fix the root cause — don't just
  retry hoping it passes.
- For refactoring tasks, verify that existing functionality is preserved by running
  the pre-refactoring tests and comparing results.
- Report test results honestly — never claim tests pass without running them.

## Double-Read Verification (for data/config/definition changes)
- After writing or editing data definitions (SQL, JSON, YAML, config files,
  type definitions), perform a "double-read": re-read the modified file and the
  original specification, comparing field by field.
- For each field/entry, verify: name matches ✓, type matches ✓, constraints match ✓,
  default value matches ✓, documentation matches ✓.
- If the change involves a dependency chain (A → B), read B after editing A to
  confirm consistency.
`)
}
```

### 4.6 缺陷 5：模型知识覆盖用户规格 → 新增信息优先级层级

**问题：** 模型用自己的常识（`DEFAULT NULL` 是合理的默认值）覆盖了用户明确指定的值。

**修复方案：** 在 `simpleSystemSection()` 之后新增 `informationPrioritySection()`：

```go
func informationPrioritySection() string {
    return strings.TrimSpace(`
# Information Priority Hierarchy
When processing information, apply this priority (highest to lowest):

1. **User's current input** (highest) — What the user explicitly states in the
   current prompt. This is your primary source of truth. Reproduce it verbatim.
2. **Project existing code** — Code, configs, and definitions already in the
   project. These reflect the established convention.
3. **Your domain knowledge** — Your understanding of language syntax, framework
   conventions, library APIs, and common patterns. Use this only to interpret
   #1 and #2, never to override them.
4. **Inference and defaults** (lowest) — What "usually makes sense" or "common
   practice". AVOID using this level unless explicitly asked to fill gaps.

When these levels conflict, the higher level always wins. A user-specified
DEFAULT '' (level 1) must NOT be replaced with null (level 4) even if null
is a "valid default" in the database schema.
`)
}
```

### 4.7 优化预期效果

- **依赖感知能力大幅提升** — 模型在更改文件 A 前会主动扫描 A 的依赖关系
- **规格复现精度提升** — 模型不再用自己的推断替代用户的精确规格
- **跨文件一致性提升** — 多文件变更不再出现"漏改"情况
- **验证有效性提升** — 从笼统的"verify"变为具体的逐字段比对方法
- **信息冲突解决有章可循** — 模型知道用户输入 > 项目代码 > 模型知识 > 推测

根据真实案例评估，这 5 个缺陷的修复预计可将此类任务的完成精度从 **60% 提升至 85-95%**。

### 4.8 回归风险

- ✅ 纯文本更改，不改任何运行时逻辑
- ✅ 新增的 4 个独立 section（`precisionSection`、`informationPrioritySection`、依赖扫描内容、双读验证内容）与现有内容互补无冲突
- ⚠️ 提示词总长度增加约 600-800 字 → 首轮 token 略增
- ✅ 可通过 Feature Flag 独立控制
- ✅ 与现有的 `doingTasksSection`、`planningSection`、`verificationSection` 无缝融合

---

## 5. 优化四：Agent 驾驭逻辑增强

### 5.1 修改位置

`internal/query/query.go` 中的 `Session.run()` 方法（主 Agent 循环）。

### 5.2 当前循环缺陷

```go
// 当前结构（简化）
for turn <= MaxTurns {
    1. Stream messages to model
    2. Collect tool_use blocks
    3. If zero tool_uses: return (模型说完了)
    4. Execute all tools
    5. Append tool_results as user message
    6. Loop
}
```

**问题：** 循环只有"有无工具调用"这一个终止条件。如果模型说了废话但没有工具调用，循环直接结束，不管任务是否完成。

### 5.3 增强方案

#### 5.3.1 任务状态跟踪器（新增 `TaskTracker`）

在 `Session` 结构体中新增：

```go
type TaskTracker struct {
    // 当前任务摘要（从用户 prompt 中提取的关键目标）
    TaskGoal string

    // 已完成的关键操作列表
    CompletedActions []string

    // 待验证的断言列表
    PendingVerifications []string

    // 连续"只说不动"的回合计数
    IdleTextTurns int

    // 连续同一错误模式的计数
    RepeatedFailures map[string]int

    // 是否要求过模型"继续执行"
    PromptedContinue bool
}
```

#### 5.3.2 强化 Agent 循环逻辑

在现有循环基础上增加：

```go
for turn <= MaxTurns {
    // === [新增] 扼流圈：连续无工具调用的检测 ===
    // 如果模型在 2 个及以上连续回合中没有调用任何工具
    // （只是输出文本但没有实际行动），追加提示要求执行
    if tracker.IdleTextTurns >= 2 && !tracker.PromptedContinue {
        // 追加用户消息："Continue working on the task — don't just report status,
        // take the next concrete action."
        messages = append(messages, createContinuePrompt(tracker))
        tracker.PromptedContinue = true
        continue
    }

    // === [新增] 重复失败模式检测 ===
    // 如果同一工具同一错误类型出现 >= 3 次
    // 追加提示建议更换策略
    if hasRepeatedFailure(tracker, toolResults) {
        messages = append(messages, createStrategyChangePrompt(tracker))
        tracker.RepeatedFailures = clearCounter(tracker.RepeatedFailures)
    }

    // === 现有的：发送消息、接收回复、执行工具 ===
    ...

    // === [新增] 记录本回合行为特征 ===
    if len(toolUses) == 0 {
        tracker.IdleTextTurns++
    } else {
        tracker.IdleTextTurns = 0
        tracker.PromptedContinue = false // 模型有行动了，重置标志
    }

    // 记录失败模式
    for _, result := range toolResults {
        if result.IsError {
            failureKey = extractFailureKey(result)
            tracker.RepeatedFailures[failureKey]++
        }
    }
}
```

#### 5.3.3 中止条件增强

现有中止条件（`len(toolUses) == 0`）不变，但增加一个**"完成确认"**逻辑：

```go
// [新增] 在模型没有工具调用时，不直接返回，而是检查：
if len(toolUses) == 0 {
    if hasRemainingWork(tracker, messages) && turn < MaxTurns {
        // 模型以为完成了，但追踪器显示还有未完成的操作
        messages = append(messages, createCompletionCheckPrompt(tracker))
        continue // 再问一次让模型确认
    }
    // 确实完成了或超出最大轮次，返回
    return result
}
```

### 5.4 优化预期效果

- **任务完成率提升** — 模型不会在任务中途"以为完成了"就停下
- **无效循环减少** — 重复失败模式会被打断，迫使其换策略
- **用户体验提升** — 用户不需要反复说"继续"

### 5.5 回归风险

- ⚠️ 新增逻辑会改变循环行为 → 必须有 Feature Flag 控制
- ⚠️ 追加提示可能会引入额外的 token 消耗
- ✅ 所有现有 Api 调用格式不变，不影响底层通信
- ⚠️ 需要测试"正常完成"的场景不会被误判为"未完成"

---

## 6. 优化五：工具结果后处理与结构化反馈

### 6.1 修改位置

`internal/query/query.go` 中处理 tool results 的部分（`runTool()` 和结果拼接逻辑，约 1143-1168 行）。

### 6.2 当前缺陷

工具原始输出直接以字符串形式塞回给模型。例如：

```
// Bash 返回 10 万行编译输出 → 模型要从 10 万行中找最后 50 行的错误
// Grep 返回 1000 行匹配 → 模型要自己归纳模式
```

### 6.3 结构化后处理方案

#### 6.3.1 通用结果包装器

新增 `formatToolResult()` 函数：

```go
type FormattedToolResult struct {
    OriginalOutput string // 原始输出（如果需要）
    Summary        string // 自动生成的摘要
    Structured     string // 结构化后的内容
    Truncated      bool   // 是否被截断
}
```

```go
func formatToolResult(toolName string, result tools.Result, maxSize int) FormattedToolResult {
    output := result.Content
    truncated := len(output) > maxSize

    if truncated {
        output = output[:maxSize] + "\n...[output truncated]"
    }

    // 根据不同工具类型做不同的后处理
    switch toolName {
    case "Bash":
        return formatBashResult(output, truncated)
    case "Grep":
        return formatGrepResult(output, truncated)
    case "Glob":
        return formatGlobResult(output, truncated)
    case "Edit":
        return formatEditResult(output, truncated)
    case "Write":
        return formatWriteResult(output, truncated)
    default:
        return FormattedToolResult{OriginalOutput: output, Truncated: truncated}
    }
}
```

#### 6.3.2 Bash 结果处理器

```go
func formatBashResult(output string, truncated bool) FormattedToolResult {
    lines := strings.Split(output, "\n")
    result := FormattedToolResult{OriginalOutput: output, Truncated: truncated}

    // 提取最后几行（通常是错误或摘要信息）
    tailLines := len(lines)
    if tailLines > 20 {
        tailLines = 20
    }
    tail := strings.Join(lines[len(lines)-tailLines:], "\n")

    // 提取 stderr 前缀（如果存在）
    stderrContent := extractStderrLines(lines)

    var sb strings.Builder
    if stderrContent != "" {
        sb.WriteString("[stderr]\n")
        sb.WriteString(stderrContent)
        sb.WriteString("\n")
    }
    if tail != output && tail != "" {
        sb.WriteString(fmt.Sprintf("[last %d of %d lines]\n", tailLines, len(lines)))
        sb.WriteString(tail)
        sb.WriteString("\n")
    }

    result.Summary = sb.String()
    if result.Summary != "" {
        result.Structured = fmt.Sprintf("Command output (%d lines total):\n%s\n%s",
            len(lines), result.Summary, output)
    } else {
        result.Structured = output
    }

    return result
}
```

#### 6.3.3 Grep 结果处理器

```go
func formatGrepResult(output string, truncated bool) FormattedToolResult {
    lines := strings.Split(output, "\n")
    result := FormattedToolResult{OriginalOutput: output, Truncated: truncated}

    // 统计不同文件中的匹配数
    fileMatches := make(map[string]int)
    for _, line := range lines {
        if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
            fileMatches[parts[0]]++
        }
    }

    // 生成文件级别摘要
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("Matches across %d files:\n", len(fileMatches)))
    // 按匹配数降序排列
    type fc struct { name string; count int }
    sorted := make([]fc, 0, len(fileMatches))
    for f, c := range fileMatches {
        sorted = append(sorted, fc{f, c})
    }
    sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
    for _, f := range sorted {
        sb.WriteString(fmt.Sprintf("  %s: %d matches\n", f.name, f.count))
    }

    result.Summary = sb.String()
    result.Structured = result.Summary + "\n[full results]\n" + output

    return result
}
```

#### 6.3.4 结果替换逻辑

修改现有工具结果拼接代码（约 `query.go:1143-1168`）：

```go
// 之前（简化）
toolResults = append(toolResults, anthropic.ContentBlock{
    Content: trace.Output,  // ← 原始输出
})

// 之后
formatted := formatToolResult(block.Name, trace, options.ToolResultLimit)
toolResults = append(toolResults, anthropic.ContentBlock{
    Content: formatted.Structured,  // ← 结构化输出
})
```

### 6.4 优化预期效果

- **模型理解速度提升** — 摘要和结构化为模型省去了从原始数据中自己提取信息的工作
- **Token 使用效率提升** — 相同信息量占用更少的上下文（通过摘要替代冗余数据）
- **决策质量提升** — 模型看到的是"经过整理"的信息，而非原始"噪音"

### 6.5 回归风险

- ⚠️ 修改了 tool_result 的内容格式 → 需要验证模型对新的格式解析正常
- ✅ 不改任何工具的输入输出——只在结果展示层做包装
- ⚠️ 需要 A/B 测试验证新格式是否比原始格式更好（见第八节）
- ✅ 可以通过 Feature Flag 控制启用/回滚

---

## 7. 优化六：上下文管理与压缩优化

### 7.1 修改位置

`internal/compact/compactor.go`、`internal/compact/config.go`。

### 7.2 当前缺陷

| 问题 | 影响 |
|------|------|
| 压缩触发阈值单一（固定 75%） | 不能按场景动态调整 |
| 摘要 prompt 没有经过调优 | 压缩质量不稳定 |
| 3 次失败即跳过 | 在高复杂任务中容易放弃压缩 |
| 不保留代码变更的"差异快照" | 压缩后模型丢失了变更上下文 |

### 7.3 优化方案

#### 7.3.1 智能压缩触发（修改 `MaybeCompact()`）

```go
// 新增：动态阈值计算
func (c *Compactor) dynamicThreshold(messages []anthropic.MessageParam, model string) int {
    baseThreshold := cfg.ThresholdTokens(model) // 当前逻辑，如 150K

    // 如果最近几轮有密集的工具调用，降低阈值提前压缩
    toolCallRatio := calculateRecentToolCallRatio(messages, 5)
    if toolCallRatio > 0.5 {
        // 工具调用密集型会话 → 提前压缩以保留更长的历史
        baseThreshold = int(float64(baseThreshold) * 0.8)
    }

    // 如果消息数 > 30，强制压缩阈值降低
    if len(messages) > 30 {
        baseThreshold = int(float64(baseThreshold) * 0.9)
    }

    return baseThreshold
}
```

#### 7.3.2 压缩质量改进（修改 `generateSummary()`）

```go
// 当前 generateSummary() 中的 prompt（约 compactor.go:166）
const summaryPrompt = `Generate a structured summary of the conversation history for context continuation...`

// 改进版
const enhancedSummaryPrompt = `You are summarizing a conversation between a user and an AI coding assistant
for the purpose of continuing the work in a new context window.
The original conversation will be COMPLETELY REMOVED — the summary is all that remains.

Your summary MUST preserve:
1. PROJECT STRUCTURE: What files exist, what was their state before changes
2. CHANGES MADE: What files were modified, what changed (be specific — include function names,
   line numbers, and key code patterns)
3. COMMANDS RUN: Any commands executed and their results (test runs, builds, installs)
4. DECISIONS: Any architecture decisions, design choices, or trade-offs made
5. PENDING WORK: What is still to be done, blocked by what, next steps
6. ERRORS/BLOCKERS: Any errors encountered, current status, known issues
7. USER PREFERENCES: Any specific instructions or style preferences the user expressed

Format as markdown with clear headings. Be specific — vague summaries are useless.
BAD: "Modified the auth module"
GOOD: "Modified auth/login.go: added TokenRefresh() function at line 45, changed ValidateSession()
      to check expiry at line 72. Ran tests: auth_test.go PASS (3/3). Next: implement logout endpoint."`
```

#### 7.3.3 压缩失败回退策略改进

```go
// 当前：3 次失败后永久跳过
// 改进：分级降级

type CompactFailureMode int
const (
    FailureModeCircuitOpen  CompactFailureMode = iota // 彻底熔断，不再压缩
    FailureModeFallbackSimple                         // 降级到简单压缩（只保留最近消息，不生成摘要）
    FailureModeRetryLater                             // 下游回合再试
)

func (c *Compactor) handleFailure() CompactFailureMode {
    c.failures++
    switch {
    case c.failures >= cfg.MaxFailures:
        // 彻底熔断 — 意味着摘要生成完全不可用
        return FailureModeCircuitOpen
    case c.failures >= cfg.MaxFailures-1:
        // 倒数第二次失败 → 降级到简单压缩
        return FailureModeFallbackSimple
    default:
        return FailureModeRetryLater
    }
}

// 简单压缩（不生成摘要，只丢弃旧消息）
func (c *Compactor) simpleCompact(messages []anthropic.MessageParam) []anthropic.MessageParam {
    // 只保留最近 N 轮的消息，不生成摘要
    partition := partitionMessages(messages, cfg.PreserveRecentRounds)
    warning := anthropic.MessageParam{
        Role: "user",
        Content: []anthropic.ContentBlock{{
            Type: "text",
            Text: "[Context management: older conversation history has been removed due to compression failure. Key facts from earlier context: ...]",
        }},
    }
    return append([]anthropic.MessageParam{warning}, partition.keep...)
}
```

#### 7.3.4 保留关键文件状态快照（新增 `FileSnapshotTracker`）

```go
type FileSnapshotTracker struct {
    // 文件路径 → 摘要
    Snapshots map[string]FileSnapshot
}

type FileSnapshot struct {
    Path      string
    LineCount int
    KeyTypes  []string    // 顶层类型/函数名
    LastEdit  time.Time
    Checksum  string      // 内容哈希，用于检测变化
}

// 在工具执行后更新快照
func (t *FileSnapshotTracker) RecordToolUse(toolName string, input json.RawMessage, output string) {
    switch toolName {
    case "Edit", "MultiEdit", "Write":
        var params struct { FilePath string `json:"file_path"` }
        json.Unmarshal(input, &params)
        t.Snapshots[params.FilePath] = FileSnapshot{
            Path: params.FilePath,
            LastEdit: time.Now(),
        }
    }
}

// 在压缩摘要中追加文件状态
func (t *FileSnapshotTracker) FileStateBlock() string {
    if len(t.Snapshots) == 0 { return "" }
    var sb strings.Builder
    sb.WriteString("\n## File State (at compression time)\n")
    for path, snap := range t.Snapshots {
        sb.WriteString(fmt.Sprintf("- %s (last edited)\n", path))
    }
    return sb.String()
}
```

### 7.4 优化预期效果

- **长会话准确率提升** — 更好的压缩质量减少信息丢失
- **压缩成功率提升** — 降级策略确保不会完全失效
- **压缩时机更合理** — 动态阈值适应不同工作负载

### 7.5 回归风险

- ⚠️ 压缩逻辑修改较复杂 → 需要单独测试组件
- ✅ 现有压缩逻辑作为 fallback 保留（通过 Feature Flag 控制新逻辑）
- ✅ 所有配置项有默认值，不强制启用

---

## 8. 优化七：子 Agent 提示词增强

### 8.1 修改位置

`internal/agentruntime/runtime.go` 中构造子 Agent 系统提示词的部分（约 203-220 行）。

### 8.2 当前状态

```go
system := "You are a focused Claude Code sub-agent. Complete only the delegated task and return a concise result."
```

### 8.3 增强方案

```go
func buildSubAgentSystemPrompt(agent AgentConfig, req Request) string {
    var sb strings.Builder

    sb.WriteString(`You are a sub-agent designed to complete a specific delegated task.

## Core Rules
- Complete ONLY the task described below. Do not expand scope.
- Use tools effectively — prefer dedicated tools over shell commands.
- When you encounter an error, try once more with a different approach, then report the error.
- Keep your output focused on the task result. Don't narrate your process.
- Return a concise, structured result that the parent agent can directly use.
`)

    if agent.Prompt != "" {
        sb.WriteString("\n## Agent Instructions\n")
        sb.WriteString(agent.Prompt)
        sb.WriteString("\n")
    }

    // 新增：工具使用指南（简版）
    sb.WriteString(`
## Tool Usage Guidelines
- Use Read before editing any file
- Prefer Edit over Write for small changes
- Use Glob/Grep for finding files and content — avoid using Bash for this
- Call independent tools in parallel when possible
- Tool results may be large — focus on the key information needed for your task
`)

    if agent.CriticalSystemReminderExperimental != "" {
        sb.WriteString("\n## Critical Reminder\n")
        sb.WriteString(agent.CriticalSystemReminderExperimental)
        sb.WriteString("\n")
    }

    return strings.TrimSpace(sb.String())
}
```

并修改 `Runtime.Run()` 中的调用：

```go
// 之前
system := "You are a focused Claude Code sub-agent..."

// 之后
system := buildSubAgentSystemPrompt(agent, req)
```

### 8.4 优化预期效果

- **子任务执行质量提升** — 子 Agent 不再"裸奔"，有行为规范
- **错误恢复能力提升** — 子 Agent 知道遇到错误时换策略而非无限重试
- **输出质量提升** — 子 Agent 知道要返回结构化结果

### 8.5 回归风险

- ✅ 子 Agent 提示词变长增加少量 token 消耗
- ✅ 不改子 Agent 的运行逻辑，只改提示词内容
- ✅ 旧有子 Agent 行为不受影响——只是多了一些上下文指南

---

## 9. 测试方案

### 9.1 单元测试

| 测试项 | 文件 | 测试内容 |
|--------|------|---------|
| 工具描述格式 | `internal/tools/tool_test.go` | 验证所有工具的 Description 是否包含摘要、用例、限制三要素 |
| 提示词章节 | `internal/query/query_test.go` | 验证新增的 planningSection、verificationSection、recoverySection 输出非空 |
| 结果格式化 | `internal/query/result_format_test.go` | 验证 formatBashResult、formatGrepResult 正确提取摘要 |
| 压缩优化 | `internal/compact/compactor_test.go` | 验证 dynamicThreshold、simpleCompact、handleFailure 逻辑 |
| TaskTracker | `internal/query/tracker_test.go` | 验证空闲回合计数、重复失败检测、扼流圈触发逻辑 |
| 子 Agent 提示词 | `internal/agentruntime/runtime_test.go` | 验证 buildSubAgentSystemPrompt 输出结构 |

### 9.2 集成测试

```go
// TestToolDescriptionCompleteness — 验证所有工具描述符合规范
func TestToolDescriptionCompleteness(t *testing.T) {
    registry := coreRuntimeTools(...)
    for _, tool := range registry.List() {
        desc := tool.Description()
        // 验证包含用例指南关键词
        assert.Contains(t, desc, "Use")
        // 验证不包含 TODO/占位符
        assert.NotContains(t, desc, "TODO")
        assert.NotContains(t, desc, "FIXME")
    }
}

// TestPromptSectionCoherence — 验证所有提示词章节不自相矛盾
func TestPromptSectionCoherence(t *testing.T) {
    sections := []string{
        planningSection(),
        doingTasksSection(),
        verificationSection(),
        recoverySection(),
        usingToolsSection(),
        actionsSection(),
    }
    // 验证没有冲突的关键词（如一个说用 Bash 一个说不用）
    // 验证每个章节在语义上不重复
}
```

### 9.3 回归测试

| 测试场景 | 预期行为 | 验证方法 |
|---------|---------|---------|
| 简单问答（"今天几号"） | 不调用工具，直接回答 | 确认零工具调用 |
| 单文件读取 | 调用 Read，正确输出 | 确认 Read 被调用 |
| 单文件编辑 | 调用 Edit，文件内容正确变更 | 读取文件验证 |
| 多步骤任务 | 按顺序调用 Read→Edit→Bash(test) | 确认调用序列 |
| 编译失败时的行为 | 识别错误，修改代码，重试 | 确认重试逻辑 |
| 已是最新代码时 | 直接返回无需修改 | 确认无 Edit 调用 |

### 9.4 A/B 测试框架（重点）

为了验证优化是否真的有正向效果，建议引入"提示词变体"机制：

```go
// internal/query/prompt_variant.go
type PromptVariant string
const (
    VariantControl PromptVariant = "control" // 旧版提示词
    VariantV1      PromptVariant = "v1"      // 优化后的提示词
)

// 在 Session 初始化时，决定使用哪个变体
func (s *Session) resolvePromptVariant() PromptVariant {
    // 基于 session ID 的哈希取模，实现稳定分组
    // 同一 session 始终用同一变体
    hash := hashSessionID(s.sessionID)
    if hash % 2 == 0 {
        return VariantControl
    }
    return VariantV1
}

// 根据变体选择不同的提示词函数
func (s *Session) planningSection() string {
    if s.promptVariant == VariantControl {
        return "" // 旧版没有规划章节
    }
    return newPlanningSection() // 新版
}
```

### 9.5 效果对比测试

设计一组标准化测试任务，分别在旧版/新版下运行，记录指标：

```go
// internal/benchmark/tasks.go
type BenchmarkTask struct {
    Name        string
    Prompt      string
    CWD         string      // 工作目录（测试项目）
    MaxTurns    int
    Success     func([]anthropic.MessageParam) bool  // 如何判断成功
    Metrics     BenchmarkMetrics
}

var benchmarkTasks = []BenchmarkTask{
    {
        Name:   "fix_build_error",
        Prompt: "Fix the compilation error in this project",
        CWD:    "/tmp/test-project-broken",
        Success: func(msgs []anthropic.MessageParam) bool {
            // 验证错误已修复
            output := runCommand("go build ./...")
            return output == ""
        },
    },
    {
        Name:   "add_unit_test",
        Prompt: "Add unit tests for the StringUtils.Reverse function",
        CWD:    "/tmp/test-project",
        Success: func(msgs []anthropic.MessageParam) bool {
            // 验证测试文件存在且通过
            output := runCommand("go test -run TestReverse ./...")
            return strings.Contains(output, "PASS")
        },
    },
}
```

---

## 10. 效果度量与验证指标

### 10.1 核心指标

| 指标 | 计算方法 | 目标改善 |
|------|---------|---------|
| 任务成功率 | 成功完成的任务 / 总任务数 | +15-25% |
| 平均完成时间 | 从开始到完成的总时长 | -10-20% |
| 平均 Token 消耗 | 每任务消耗的输入+输出 Token 数 | -10-20%（同等质量下） |
| 无效工具调用率 | 被拒绝/失败的工具调用 / 总工具调用数 | -20-30% |
| 用户干预率 | 用户手动纠正/打断的次数 / 会话数 | -20-30% |
| 子任务完成率 | 子 Agent 成功返回结果 / 子 Agent 总调用数 | +20-30% |

### 10.2 可观测性埋点

在每个 Session 中记录：

```go
type SessionMetrics struct {
    // 回合数
    Turns          int
    ToolCalls      int
    FailedToolCalls int

    // Token
    InputTokens    int
    OutputTokens   int
    CacheWrites    int
    CacheReads     int

    // 行为特征
    IdleTextTurns     int      // 只说不动回合
    RepeatedFailures  int      // 重复失败次数
    Compactions       int      // 压缩次数
    SubAgentCalls     int      // 子 Agent 调用数

    // 提示词变体
    PromptVariant     string   // control / v1 / v2

    // 结果
    TaskCompleted     bool
    Duration          time.Duration
}
```

### 10.3 验证流程

```
1. 建立基线
   → 用当前版本运行 benchmark 测试 10 次，记录所有指标

2. 逐个启用优化
   → 每次只启用一个优化（从风险最低的"工具描述"开始）
   → 每个优化运行 benchmark 测试 10 次
   → 对比基线指标

3. 组合优化
   → 启用"工具描述 + 系统提示词"组合
   → 运行 benchmark 测试
   → 检查是否有叠加效应或冲突

4. 全量启用
   → 启用所有优化
   → 运行 benchmark 测试
   → 对比基线指标
```

### 10.4 成功标准

```
必要条件（必须满足）：
- 任务成功率不下降
- 无新增 error/panic
- 所有现有测试通过

期望条件（满足越多越好）：
- 任务成功率提升 >10%
- 无效工具调用率下降 >15%
- Token 消耗不显著增加（<10%）
- 用户可感知的输出质量提升
```

---

## 11. 风险控制与渐进式落地

### 11.1 Feature Flag 方案

每个优化都有独立的 Feature Flag：

| Flag | 对应优化 | 默认值 |
|------|---------|--------|
| `enhanced_tool_descriptions` | 工具描述扩展 | false |
| `enhanced_system_prompt` | 系统提示词精炼 | false |
| `agent_loop_enhancements` | Agent 驾驭增强 | false |
| `structured_tool_results` | 工具结果后处理 | false |
| `smart_compaction` | 上下文压缩优化 | false |
| `enhanced_subagent_prompt` | 子 Agent 提示词增强 | false |

每个 Flag 独立控制，可以按顺序逐个启用。

```go
// 在 query.go 中
func (s *Session) planningSection() string {
    if !s.featureEnabled("enhanced_system_prompt") {
        return ""
    }
    return newPlanningSection()
}
```

### 11.2 回滚计划

每个优化都设计为"零侵入"——改的是字符串和纯函数，不改核心流程。回滚只需切 Feature Flag 或 revert commit。

| 优化 | 回滚难度 | 回滚方式 |
|------|---------|---------|
| 工具描述 | ★☆☆ | 切 Feature Flag 或 revert 文件 |
| 系统提示词 | ★☆☆ | 切 Feature Flag |
| Agent 驾驭 | ★★☆ | 需要切 Flag + 检查副作用 |
| 工具结果后处理 | ★☆☆ | 切 Feature Flag |
| 上下文压缩 | ★★☆ | 切 Feature Flag |
| 子 Agent 提示词 | ★☆☆ | 切 Feature Flag |

### 11.3 上线顺序建议

```
Week 1:  工具描述扩展 + 子 Agent 提示词增强
         风险评估：最低，纯文本改动
         验证方式：A/B 测试

Week 2:  系统提示词精炼
         风险评估：低
         验证方式：A/B 测试

Week 3:  工具结果后处理
         风险评估：中（需验证模型对新格式的适应性）
         验证方式：A/B 测试 + benchmark

Week 4:  Agent 驾驭增强 + 上下文压缩优化
         风险评估：中高（涉及循环逻辑改动）
         验证方式：Feature Flag + benchmark + 灰度
```

---

## 12. 实施路线图

### Phase 1：基础优化（低风险，高回报）

| 序号 | 任务 | 文件 | 预计行数 |
|------|------|------|---------|
| 1.1 | 扩展 Bash 工具描述 | `internal/tools/bash/bash.go` | ~20 |
| 1.2 | 扩展 Read 工具描述 | `internal/tools/fileread/fileread.go` | ~15 |
| 1.3 | 扩展 Write 工具描述 | `internal/tools/filewrite/filewrite.go` | ~15 |
| 1.4 | 扩展 Edit/MultiEdit 工具描述 | `internal/tools/fileedit/fileedit.go` | ~15 |
| 1.5 | 扩展 Grep 工具描述 | `internal/tools/grep/grep.go` | ~15 |
| 1.6 | 扩展 Glob 工具描述 | `internal/tools/glob/glob.go` | ~10 |
| 1.7 | 扩展所有其他工具描述 | 各 `tools/*/` 文件 | ~80 |
| 1.8 | 子 Agent 提示词增强 | `internal/agentruntime/runtime.go` | ~30 |

### Phase 2：系统提示词优化（中风险，高回报）

| 序号 | 任务 | 文件 | 预计行数 |
|------|------|------|---------|
| 2.1 | 新增 planningSection() | `internal/query/query.go` | ~20 |
| 2.2 | 新增 verificationSection() | `internal/query/query.go` | ~25 |
| 2.3 | 新增 recoverySection() | `internal/query/query.go` | ~35 |
| 2.4 | 扩展 usingToolsSection() | `internal/query/query.go` | ~15 |
| 2.5 | 扩展 doingTasksSection() | `internal/query/query.go` | ~20 |
| 2.6 | 扩展 toneAndStyleSection() | `internal/query/query.go` | ~15 |
| 2.7 | 调整提示词章节顺序 | `internal/query/query.go` | ~5 |
| 2.8 | 新增 Feature Flag 控制 | `internal/query/query.go` + config | ~30 |

### Phase 3：行为约束提示词增强（低风险，极高回报 — 基于真实案例缺陷）

| 序号 | 任务 | 文件 | 预计行数 |
|------|------|------|---------|
| 3.1 | 在 planningSection 中增加依赖扫描步骤 | `internal/query/query.go` | ~15 |
| 3.2 | 新增 precisionSection() 精确复现指南 | `internal/query/query.go` | ~20 |
| 3.3 | 在 doingTasksSection 中强化跨文件约束 | `internal/query/query.go` | ~20 |
| 3.4 | 在 verificationSection 中细化验证方法 | `internal/query/query.go` | ~20 |
| 3.5 | 新增 informationPrioritySection() 信息优先级层级 | `internal/query/query.go` | ~20 |

### Phase 4：Agent 驾驭增强（高风险，高回报）

| 序号 | 任务 | 文件 | 预计行数 |
|------|------|------|---------|
| 4.1 | 新增 TaskTracker 结构体 | `internal/query/query.go` | ~40 |
| 4.2 | 实现空闲回合扼流圈 | `internal/query/query.go` | ~30 |
| 4.3 | 实现重复失败检测 | `internal/query/query.go` | ~25 |
| 4.4 | 实现完成确认逻辑 | `internal/query/query.go` | ~20 |
| 4.5 | TaskTracker 单元测试 | `internal/query/tracker_test.go` | ~80 |

### Phase 5：工具结果后处理（中风险，高回报）

| 序号 | 任务 | 文件 | 预计行数 |
|------|------|------|---------|
| 5.1 | 新增 formatToolResult 分发函数 | `internal/query/query.go` 或新文件 | ~20 |
| 5.2 | 实现 formatBashResult | 同上 | ~40 |
| 5.3 | 实现 formatGrepResult | 同上 | ~40 |
| 5.4 | 实现 formatGlobResult | 同上 | ~20 |
| 5.5 | 修改 tool_result 拼接逻辑 | `internal/query/query.go` | ~10 |
| 5.6 | 结果格式化单元测试 | 新测试文件 | ~100 |

### Phase 6：上下文压缩优化（高风险，中等回报）

| 序号 | 任务 | 文件 | 预计行数 |
|------|------|------|---------|
| 6.1 | 实现 dynamicThreshold | `internal/compact/compactor.go` | ~25 |
| 6.2 | 优化摘要 prompt | `internal/compact/compactor.go` | ~20 |
| 6.3 | 实现分级降级策略 | `internal/compact/compactor.go` | ~30 |
| 6.4 | 压缩优化单元测试 | `internal/compact/compactor_test.go` | ~80 |

---

## 附录：总工作量估计

| Phase | 预计行数 | 风险 | 回报 | 优先级 |
|-------|---------|------|------|--------|
| 1: 工具描述 | ~200 | 低 | 中高 | 1 |
| 2: 系统提示词 | ~165 | 低 | 高 | 2 |
| 3: 行为约束提示词 | ~95 | 低 | 极高 | 3 |
| 4: Agent 驾驭 | ~195 | 中高 | 高 | 5 |
| 5: 结果后处理 | ~230 | 中 | 高 | 4 |
| 6: 上下文压缩 | ~155 | 中 | 中 | 6 |
| **总计** | **~1040** | | | |

> **建议实施顺序：Phase 1 → Phase 2 → Phase 3 → Phase 5 → Phase 4 → Phase 6**
>
> 每个 Phase 独立可用 Feature Flag 控制。Phase 3（行为约束）改动量最小但回报最高，建议在 Phase 2 后立即进行，可在单个开发日内完成。
