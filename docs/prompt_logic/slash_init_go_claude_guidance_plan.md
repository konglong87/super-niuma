# Slash Init and go-claude Guidance File Plan

本文档记录 go-claude `/init` slash command 与项目指导文件的技术实现方案。目标是参考原版 Claude Code `/init` 的 prompt 型 slash command 设计，但在 go-claude 中使用可配置的自有标识与自有文件名。

## 背景

原版 Claude Code 的 `/init` 不是简单本地写文件逻辑，而是一个内置 prompt 型 slash command：命令展开为一段初始化 prompt，让模型扫描仓库并生成或改进项目指导文件。

当前 go-claude 已有 slash command 列表、TUI slash 补全、Web Agent slash command endpoint、custom command/skill 动态展开，以及普通 CLI `go-e2e init [--settings]`。缺口是：

- 内置 slash command 列表没有 `/init`。
- slash prompt 解析只覆盖 skills/custom commands，没有内置 prompt 型 command。
- 普通 CLI `init` 目前创建 `CLAUDE.md` 占位文件，不符合 go-claude 自有标识策略。
- code memory loader 当前优先读取 `CLAUDE.md`，尚未支持 go-claude 自有指导文件。

## 目标

1. 新增 `/init` slash command，行为参考原版 Claude Code：展开为初始化 prompt，由模型扫描当前 `cwd` 后生成或改进项目指导文件。
2. 默认项目指导文件名为 `go-e2e.md`，但文件名必须配置化，方便后续改名。
3. 新增或修改的 go-claude 自有配置、文件、文案、目录优先使用可配置的 `go-claude` 标识，不再新增 `Claude` 标识。
4. `go-claude` 标识本身也要配置化，避免产品名或目录名后续演进时散落在代码中。
5. `go-e2e init --settings` 创建 `go-e2e.md` 和 `.go-claude/settings.json`。
6. 项目指导加载优先级固定为：`go-e2e.md` -> `CLAUDE.md` -> `AGENTS.md` fallback。

## 非目标

- 不在本阶段实现原版新 `/init` 的完整 skills/hooks 多阶段问答流程。
- 不移除现有 `CLAUDE.md` 兼容能力。
- 不破坏 `AGENTS.md` 作为无项目指导文件时的 workflow fallback。
- 不把 `/init` 做成只在 TUI 可用的特例；TUI、Web Agent、loop/background 路径应复用同一解析逻辑。

## 配置模型

新增一个集中配置入口，建议放在 `internal/product` 或 `internal/branding`，避免 magic string 分散：

```go
type Identity struct {
	ProductName          string // default: "go-claude"
	ProductKey           string // default: "go-claude"
	ConfigDirName        string // default: ".go-claude"
	GuidanceFilename     string // default: "go-e2e.md"
	LegacyGuidanceFile   string // default: "CLAUDE.md"
	WorkflowFallbackFile string // default: "AGENTS.md"
}
```

默认值：

```text
ProductName:          go-claude
ProductKey:           go-claude
ConfigDirName:        .go-claude
GuidanceFilename:     go-e2e.md
LegacyGuidanceFile:   CLAUDE.md
WorkflowFallbackFile: AGENTS.md
```

配置来源建议分层：

1. 编译期默认值，保证开箱即用。
2. settings 中的可选 override，例如 `identity.product_key`、`identity.guidance_filename`、`identity.config_dir_name`。
3. 环境变量只作为高级调试/迁移入口，例如 `GOLANG_CLAUDE_CODE_PRODUCT_KEY`、`GOLANG_CLAUDE_CODE_GUIDANCE_FILE`。

要求：

- 所有新增 go-claude 自有文件名、目录名、prompt 文案、help 文案引用该集中配置。
- 兼容旧文件时显式称为 legacy compatibility，不把 `CLAUDE.md` 继续作为新增默认目标。
- 配置值要做基本校验：不能为空、不能含路径穿越、指导文件必须是相对文件名，配置目录必须是相对目录名。

## Slash Command 设计

### 注册

在 `internal/slashcommands` 中新增内置 command：

```text
Name: init
Description: Initialize go-e2e.md with project guidance
Source: builtin
```

`Description` 中的 `go-e2e.md` 来自 `Identity.GuidanceFilename`，不要写死。

### 解析

将现有 `internal/cli` 中的动态 slash prompt 逻辑下沉到 `internal/slashcommands`，形成共享 resolver：

```go
func ResolvePrompt(ctx context.Context, cwd string, input string, identity Identity) (prompt string, ok bool, err error)
```

解析顺序：

1. 内置 prompt 型 slash command，例如 `/init`。
2. user-invocable skill。
3. legacy `.claude/commands/*.md` custom command。

这样 TUI、print/loop、Web Agent、background runner 后续都能复用同一行为，避免 `/init` 只在某个入口有效。

### `/init` prompt

`/init` 展开的 prompt 应参考原版旧 `/init` 的保守版本，并替换为 go-claude 自有目标文件：

- 分析当前代码库并创建或改进 `${GuidanceFilename}`。
- 读取 README、manifest、build/test/lint 配置、CI、现有 `${GuidanceFilename}`、legacy `CLAUDE.md`、`AGENTS.md`、`.claude/rules/`、`.cursor/rules/`、`.cursorrules`、`.github/copilot-instructions.md` 等。
- 只写 go-claude 容易做错、且无法从代码直接推断的内容。
- 不列文件树，不写通用工程建议，不编造命令。
- 如果 `${GuidanceFilename}` 已存在：先读取并提出最小改进，不静默覆盖。
- 如果只有 legacy `CLAUDE.md` 存在：可读取并迁移重要内容，但输出目标仍是 `${GuidanceFilename}`。
- 文件头使用配置化 product name：

```md
# go-e2e.md

This file provides guidance to go-claude when working in this repository.
```

其中标题文件名与产品名均来自 identity 配置。

## CLI Init 设计

普通 CLI `go-e2e init [--settings]` 与 slash `/init` 分工如下：

- `go-e2e init`：创建最小 `${GuidanceFilename}` scaffold。
- `go-e2e init --settings`：同时创建 `${ConfigDirName}/settings.json`。
- `/init`：通过模型扫描仓库，生成或改进 `${GuidanceFilename}`。

默认行为：

```text
go-e2e init --settings
  creates go-e2e.md
  creates .go-claude/settings.json
```

`settings.json` 路径必须改为使用 `Identity.ConfigDirName`，默认 `.go-claude/settings.json`。如果当前配置加载体系仍需要兼容 `.claude/settings.json` 或 `.go-claude/settings.json` 之外的旧路径，兼容逻辑必须单独标注为 legacy，不作为新增默认输出。

## Guidance 加载优先级

`internal/memory` 的项目指导文件加载规则调整为同目录优先级：

```text
go-e2e.md -> CLAUDE.md -> AGENTS.md fallback
```

具体规则：

1. 对每个从 workspace root 到 cwd 的目录，先查 `${GuidanceFilename}`。
2. 如果存在 `${GuidanceFilename}`，加载为 Project guidance，并跳过同目录 `CLAUDE.md` 与 `AGENTS.md`。
3. 如果不存在 `${GuidanceFilename}`，再查 legacy `CLAUDE.md`。
4. 如果 legacy `CLAUDE.md` 存在，加载为 Project guidance，并跳过同目录 `AGENTS.md`。
5. 如果两者都不存在，才加载 `AGENTS.md` 作为 Workflow fallback。
6. `.claude/CLAUDE.md`、`.claude/rules/*.md`、workflow docs 的现有兼容路径先保持不变；后续可单独规划 `.go-claude/rules` 等 go-claude 自有路径。

这样既支持 go-claude 自有默认，又不破坏现有 Claude Code 项目和 AGENTS workflow fallback。

## Web Agent 与 TUI 集成

TUI：

- `listTUISlashCommands()` 自动显示 `/init`。
- 用户输入 `/init` 后，`handleInteractiveSlash()` 对内置 local command 不处理时，由共享 `slashcommands.ResolvePrompt()` 展开为 prompt。
- slash suggestion、Enter 补全、loop/background 不新增特例。

Web Agent：

- `/agent/slash-commands?prefix=in` 返回 `/init`。
- Web Agent 发送 `/init` 时，runner 应调用同一 shared resolver 展开 prompt。
- 如果当前 Web Agent 只列 slash commands、不执行内置 prompt command，需要在 agent task message 创建前补 resolver，避免 UI 显示可用但运行时当普通文本发送。

## 测试计划

单元测试：

- `internal/slashcommands`
  - builtins 包含 `init`。
  - prefix `in` 返回 `init`。
  - `/init` 展开 prompt，且 prompt 包含配置化 `${GuidanceFilename}`。
  - 修改 identity 后，prompt 与 description 使用新文件名。
- `internal/memory`
  - 只存在 `go-e2e.md` 时加载它。
  - 同目录同时存在 `go-e2e.md` 和 `CLAUDE.md` 时只加载 `go-e2e.md`。
  - 只存在 `CLAUDE.md` 时兼容加载。
  - 两者都不存在时加载 `AGENTS.md` fallback。
- `internal/cli`
  - `go-e2e init --settings` 创建 `go-e2e.md` 和 `.go-claude/settings.json`。
  - `/init` 在 TUI slash path 中能被 resolver 接管，不报 unknown。
  - `loopRunPrompt(cwd, "/init")` 展开为初始化 prompt。
- `internal/server`
  - `/agent/slash-commands?prefix=in` 返回 `init`。
  - Web Agent task/message 输入 `/init` 时传给 query runtime 的 prompt 已展开。

验证命令：

```bash
go test ./internal/slashcommands ./internal/memory ./internal/cli ./internal/server -count=1
go test ./... -count=1
git diff --check
```

如果涉及 Web Agent 前端显示或执行链路，再补：

```bash
npm --prefix web test
npm --prefix web run build
```

## 迁移策略

1. 默认只生成和加载 `go-e2e.md`，不再生成或读取旧产品规则文件名。
2. 文档和 help 文案统一使用 `go-e2e.md`、`.go-claude/settings.json`。
3. 对外部项目的 `CLAUDE.md` 和 `AGENTS.md` 保持读取能力。
4. `.claude/rules`、`.claude/commands`、`.claude/skills` 等外部兼容目录继续独立处理，不与项目规则文件名混用。

## 实施顺序

1. 新增 identity/config 常量与校验。
2. 修改 CLI `init` 输出目标为 `${GuidanceFilename}` 和 `${ConfigDirName}/settings.json`。
3. 修改 memory loader 项目指导优先级。
4. 新增 `/init` built-in prompt command。
5. 下沉并统一 slash prompt resolver。
6. 接入 TUI、loop、Web Agent runner。
7. 补齐测试与文档。

## 风险与约束

- 不能把 `go-e2e.md` 写成硬编码，否则后续改名成本高。
- 不能只改 slash 列表不改执行 resolver，否则 UI 会显示 `/init` 但执行无效。
- 不能只生成 `go-e2e.md` 不加载它，否则初始化结果不会进入后续上下文。
- 不能在同目录同时加载 `go-e2e.md` 和 `CLAUDE.md`，否则项目指导可能重复或冲突。
- 不能把 `AGENTS.md` 提升为同级项目指导；它仍是 fallback workflow rule。
