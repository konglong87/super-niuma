# 记忆的作用域与字节预算

`internal/memory` 负责把 `CLAUDE.md` / `AGENTS.md` / 规则文件 / user / team / managed / 项目记忆装进提示词。
本文档说明两件此前**声称有、实际没有**的能力：`paths:` 作用域过滤和字节预算。

## `paths:` / `excludes:` frontmatter

```markdown
---
paths:
  - internal/memory/**
  - internal/**/*_test.go
excludes:
  - internal/memory/generated/**
---
```

### 匹配的是文件，不是 prompt 字符串

**旧行为（bug）**：`strings.Contains(prompt, needle)` —— 拿 pattern 去用户输入的 prompt 字符串里找子串。后果：

- `paths: [internal/memory/**]` 只有用户**字面输入了** `internal/memory` 才生效；
- 任何含该子串的散文都会误命中（`internal/memory-mapped/cache.go`、`docs/api/README.md` 对 `api/**`）；
- pattern 中间的通配符**永远匹配不上** —— 旧代码只剥掉结尾的 `/**` 和 `/*`，`internal/**/*_test.go` 会被整串当子串去找。

**现行为**：从 prompt 里抽出**路径形态的 token**（含 `/` 或以文件扩展名结尾），构成本轮文件集，再用真正的 glob 匹配：

| pattern | 语义 |
| --- | --- |
| `**` | 匹配全部 |
| `internal/memory/**` | 该目录整棵子树（`internal/memory-mapped/` **不**命中） |
| `internal/memory` | 无通配符的 pattern 也覆盖整棵子树，兼容裸目录写法 |
| `internal/**/*_test.go` | `**` 跨目录分隔符，其余段用 `path.Match` |
| `*.md` | 只匹配根目录下的 `.md`（`docs/README.md` 不命中） |

抽取会剥掉 `@` 前缀、`:行号[:列号]` 后缀、包裹用的引号/反引号/括号，并跳过 URL。

### 已知边界（**未做**，需要前置条件）

`paths:` 想表达的理想语义是"本轮**实际改到**的文件"。但 frontmatter 是在**装配提示词时**求值的 —— 那时模型还没跑，一次工具调用都没发生，工具将要读写哪些文件**尚不存在**。可用信息只有 prompt 本身。

因此以下情形 `paths:` 仍然管不到：用户说"修一下检索的 bug"（不提文件名），Agent 随后编辑 `internal/memory/memory.go`。

要覆盖它需要**跨层改动**，明显超出单条 bug 修复的范围：

1. 在 `query.Session` 里维护本轮的文件集（工具调用产生的 `tools.FileChange` + 读类工具的 input path）；
2. 把该文件集回灌给 `memory` 层；
3. **在一轮之内重新求值 frontmatter 并重建 system addendum** —— 这是最重的一步：当前 memory addendum 只在 turn 起点装配一次，改成中途可变会影响 prompt 缓存命中与 `codePromptReport` 的语义。

未做，另立条目更合适。

### 作用域未知时不隐藏

prompt 里识别不出任何路径 → 文件集为空 → **带 `paths:` 的文档照常加载**。
理由：作用域未知不等于不匹配，靠猜测隐藏用户写下的指导，代价高于多带一段上下文。

`excludes:` 反之从严：文件集里**任一**文件命中某条 exclude，该文档就被丢掉。

## 字节预算

**旧行为（bug）**：预算只对 `Type=="Workflow"` 生效。`CLAUDE.md` / user / team / managed / 项目记忆**从不截断** —— 一个 500KB 的 `CLAUDE.md` 会整个进 prompt，且全局没有总字节上限。

**现行为**：三层预算，都以字节计，设为 `0` 关闭。

| 环境变量 | 默认 | 作用 |
| --- | --- | --- |
| `GOLANG_CC_WORKFLOW_PROMPT_BUDGET_BYTES` | 4096 | workflow 文档的结构化压缩（章节索引 + 高优先级规则摘录），先于下面两条生效 |
| `GOLANG_CC_MEMORY_DOCUMENT_BUDGET_BYTES` | 16384 | **单个**文档的内容预算，对所有类型生效；至少保留完整定位提示 |
| `GOLANG_CC_MEMORY_PROMPT_BUDGET_BYTES` | 65536 | **所有**记忆文档合计的内容预算；耗尽后仍保留每份文档的定位提示 |

文档按优先级顺序（managed → user → project → local → team → auto → 项目记忆）消耗总预算：靠前的保留正文，预算耗尽后靠后的收缩成一行指针。

这不是最终请求的严格字节上限。若剩余额度不足以容纳定位提示，提示仍完整保留，因此 `PromptBytes` 可超过配置值；文档标题、路径包装、系统提示词和其他请求消息也不属于该内容预算。验收应分别核对正文、定位提示开销和请求包装，不应仅看到截断标记就判定总预算生效。上表环境变量由 memory 模块直接读取，使用 `GOLANG_CC_` 名称。

截断处一定有显式标记，并指出该读哪个文件：

```
[Memory truncated for prompt budget] 512000 bytes total; read /path/to/CLAUDE.md before relying on rules not shown here.
```

截断永远落在行边界或 rune 边界上，不会把中文规则切成半个字。

`golang-cc status --json` 会报告 `memoryDocumentBudgetBytes`、`memoryPromptBudgetBytes`、`memoryBudgetedDocuments`；`workflowBudgetedDocuments` 只统计 workflow 文档。
