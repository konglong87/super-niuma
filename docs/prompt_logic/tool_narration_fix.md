# 工具调用前口述计划问题分析与修复（v2）

## 问题现象

go-claude 在调用工具前，会先口述自己的执行计划，例如：

```
先看已有的文档和相关代码。
现在看下当前值 "178" 的实际色值，以及 git log 中之前的修改。
让我确认 256 色表中 178 的实际色值。
现在有完整数据了。以下是分析：
```

每一步都先说"我要做什么"，再调用工具，最后再说"我做完了"。这些口述对用户毫无价值——用户只需要看到最终结论，不需要知道中间读了哪些文件。

## Claude Code 的做法

Claude Code 直接调用工具，工具结果回来后直接给结论。中间过程不说话。用户看到的流程是：

```
[tool call: Read app.go:3585]
[tool call: Bash: echo ...]
[tool result returned]

Inline code 颜色过亮分析：
当前值 209（亮橙），建议改为 180（淡紫）。
```

干净、直接、无废话。

## 根因分析

### 提示词层面

`toneAndStyleSection()` 中已有规则：

```
- Be concise. Lead with the answer or action, not the preamble.
```

这条规则说"不要前言"，但"前言"和"口述计划"是两个不同的概念：

- **前言**：在回答前先说背景、铺垫、解释（"让我先解释一下..."）
- **口述计划**：在调用工具前先说"我要读 X 文件"、"我要查 Y 值"、"让我确认 Z"

"Be concise" 能抑制前言，但无法抑制口述计划——因为口述计划看起来像是"在行动"，LLM 认为自己是在"透明地展示工作过程"，而不是在"说废话"。

### LLM 行为层面

LLM 在 streaming 输出时，倾向于先生成一段文字（因为文字生成比工具调用更快启动），然后才生成工具调用。这种"先说后做"的模式在缺乏明确约束时，会自然演变为口述计划。

Claude Code 通过更严格的提示词约束 + 训练时的行为偏好，压制了这种倾向。go-claude 缺少同等强度的约束。

### 用户体验层面

口述计划的问题：

1. **浪费 token**：每一步口述消耗输出 token，增加成本和延迟
2. **干扰阅读**：用户需要跳过大量"我要做 X"、"让我看 Y"的废话才能找到结论
3. **降低信任**：频繁口述让 LLM 显得犹豫、不确定，而非自信、高效
4. **信息冗余**：工具调用本身已经展示了"做了什么"，口述只是重复

## 修复方案

### v1

```
- Do not narrate your tool-use plan before executing tools. Call tools directly; present results after.
```

v1 把规则加在 `toneAndStyleSection()`，能抑制一部分工具调用前口述，但边界不够清晰：项目级规则要求“执行之前先定好计划和实现方案”，而工具前口述问题实际只发生在 Read/Grep/LS 这类探索动作前。把它放在风格段，容易让模型误以为所有任务都不该先计划。

### v2

在 `usingToolsSection()` 中新增更具体的工具使用边界：

```
- If the next action is exploratory or read-only (Read, Glob, Grep, LS, or a simple
  inspection command), call the tool directly instead of first narrating that you will
  inspect, search, or read something. The tool call already shows the action.
- Share a short plan before acting only when the user asked for a plan, the task
  requires file edits or shared-state changes, or the next actions are risky or
  ambiguous enough that user-visible sequencing affects safety.
```

这两条规则明确区分了两种行为：
- **禁止**：在调用工具前口述"我要做什么"
- **要求**：探索/读查类动作直接调用工具，结果回来后再呈现结论
- **保留**：用户要求方案、需要改文件、会影响共享状态、动作有风险或歧义时，先给短计划

### 为什么从 `toneAndStyleSection()` 移到 `usingToolsSection()`？

1. 问题触发点是工具调用前的决策，不是所有输出风格。
2. `usingToolsSection()` 和 Read/Grep/LS/Bash/Edit 的选择、顺序最接近，模型更容易把规则应用到正确位置。
3. 明确保留“先计划”的适用场景，避免和项目级规则冲突。

### 为什么不依赖已有的 "Be concise" 规则？

"Be concise" 是泛化的风格约束，LLM 对它的理解是"不要说太多话"。但口述计划在 LLM 的认知中不是"说太多话"，而是"透明展示工作过程"——它认为这是好事，不是坏事。

需要一条**具体的、针对性的规则**来明确禁止口述计划行为。泛化规则无法覆盖这种 LLM 自认为"合理"的行为模式。

## 代码改动

`internal/query/query.go` — `usingToolsSection()`：

```go
// 修复前
func usingToolsSection() string {
    return strings.TrimSpace(`
# Using Your Tools
- Prefer dedicated tools over Bash: use Read for reading files, Edit or Write for
  changing files, Glob for finding files, Grep for searching contents, LS for listing
  directories. Only use Bash for operations that truly need a shell.
...
`)
}

// 修复后
func usingToolsSection() string {
    return strings.TrimSpace(`
# Using Your Tools
- Prefer dedicated tools over Bash: use Read for reading files, Edit or Write for
  changing files, Glob for finding files, Grep for searching contents, LS for listing
  directories. Only use Bash for operations that truly need a shell.
- If the next action is exploratory or read-only (Read, Glob, Grep, LS, or a simple
  inspection command), call the tool directly instead of first narrating that you will
  inspect, search, or read something. The tool call already shows the action.
- Share a short plan before acting only when the user asked for a plan, the task
  requires file edits or shared-state changes, or the next actions are risky or
  ambiguous enough that user-visible sequencing affects safety.
...
`)
}
```

新增规则放在工具选择规则之后，因为它不是泛化“少说话”，而是“工具前该不该说话”的行为边界。

## 验证方法

1. 编译：`$HOME/go/go1.26.4/bin/go build -o /tmp/golang-cc-test ./cmd/golang-cc/`
2. 启动 `/tmp/golang-cc-test`
3. 测试场景：让 go-claude 分析一个代码问题
4. 预期：go-claude 对 Read/Grep/LS 等探索动作直接调用工具，不先口述"我要读 X 文件"、"让我看 Y"
5. 对比：修复前的版本会在每步工具调用前口述计划
6. 回归：当用户明确要求“先说方案”或任务即将修改文件时，go-claude 仍应先给短计划
