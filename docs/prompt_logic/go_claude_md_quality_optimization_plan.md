# go-e2e.md Quality Optimization Plan

## 背景

现有 `/init` 已能创建或改进 `go-e2e.md`，并且默认使用 go-claude 自有标识、可配置文件名，以及 `go-e2e.md -> CLAUDE.md -> AGENTS.md` 的加载优先级。

实际与原版 Claude Code `/init` 对比后发现：

- 原版生成的 `CLAUDE.md` 更像项目说明书，会包含 commands、核心类型、核心函数和行为摘要。
- 当前 go-claude 生成的 `go-e2e.md` 更像操作守则，强调验证、架构约束和边界，内容更短。
- 完全照搬原版会增加上下文噪音和过期风险；完全不写核心入口又会让后续 agent 缺少快速定位和业务契约提示。

本方案目标是把 `go-e2e.md` 从“简短提示文件”升级为“精炼的项目级 agent 操作手册”，吸收 Codex `AGENTS.md` 的质量标准：规则明确、验证闭环、风险边界清楚，同时保持 go-claude 的轻量和自有标识。

## 目标

1. `/init` 默认生成 balanced 风格的 `go-e2e.md`。
2. `go-e2e.md` 不变成 README，不镜像源码，不列文件树。
3. 允许在默认模式下写少量稳定的核心入口或公共 API surface，帮助后续 agent 避免误改。
4. 新增 `init.detailLevel` 配置，方便用户在 minimal、balanced、detailed 之间切换。
5. 保持所有新增文案使用 go-claude 自有标识和可配置 guidance filename。

## 非目标

- 不实现原版 Claude Code 新 `/init` 的多阶段 AskUserQuestion、skills/hooks 引导流程。
- 不默认生成 `.claude/skills`、hooks、personal guidance 文件。
- 不把 `go-e2e.md` 扩展成完整架构文档、API 文档或 README。
- 不移除 `CLAUDE.md` 和 `AGENTS.md` 兼容读取。

## 配置设计

新增 settings：

```json
{
  "init": {
    "detailLevel": "balanced"
  }
}
```

配置位置：

- 项目级：`.go-claude/settings.json`
- 全局级：`~/.go-claude/settings.json`

优先级：

1. `GOLANG_CLAUDE_CODE_INIT_DETAIL_LEVEL` 环境变量，适合临时 A/B 或调试覆盖。
2. settings 中的 `init.detailLevel`，项目级配置可覆盖全局配置。
3. 默认值 `balanced`。

取值：

| detailLevel | 行为 |
| --- | --- |
| `minimal` | 只写操作规则、非显然命令、验证门槛、边界；不生成 `Key Entrypoints`。 |
| `balanced` | 默认值。在 minimal 基础上，允许短小的 `Key Entrypoints` / `Core API surface`，只记录稳定且会影响 agent 行为判断的入口、职责和非显然行为。 |
| `detailed` | 接近原版 Claude Code 的说明密度。适合 SDK、框架、复杂后端项目；可记录核心类型、函数、handler、CLI、模块关系，但仍禁止镜像源码或列全量函数清单。 |

无效值回退到 `balanced`，避免配置错误导致 prompt 失控。

## go-e2e.md 推荐结构

`/init` 不强制每个项目都有所有 section，但应优先生成以下结构：

```md
# go-e2e.md

This file provides guidance to go-claude when working in this repository.

## Project Goal

一句话说明项目目标、产品边界或兼容目标。

## Working Rules

只写本项目特有的开发规则、修改边界、兼容要求和禁止项。

## Key Entrypoints

仅 balanced/detailed 且确有必要时生成。每条只写路径、职责和非显然行为。

## Commands

只写 agent 需要知道的命令，以及什么时候运行它们。

## Verification Gate

改完必须如何验证。根据项目类型写单测、全量测试、`git diff --check`、API curl、TUI 真机截图、浏览器 E2E、数据库落库检查等。

## Safety / Permissions

仅在项目涉及 Bash、文件写入、sandbox、auth、MCP、hooks、DB migration、多租户等高风险能力时生成。

## Known Boundaries

写容易被 agent 误判的限制、未实现功能、fallback 顺序、legacy 兼容或 prompt-only 与 hard enforcement 的区别。
```

## 核心入口写入规则

允许写：

```md
- `src/index.ts` exports `normalizeOrder(order)`: validates SKU and quantity, uppercases SKU, and defaults `channel` to `web`.
```

不建议写：

```md
- `Order` has `sku`, `quantity`, and `channel`.
```

除非该类型是稳定公共契约、跨模块 API、外部协议或模型容易误改的业务边界。

## Prompt 规则

`/init` prompt 应明确要求：

- 写 agent operating guide，不写 README summary。
- 只写 durable facts：项目特有工作规则、非显然命令、验证门槛、架构约束、核心入口、安全/权限/数据/兼容边界。
- 根据 `init.detailLevel` 决定核心入口和类型/函数细节密度。
- 禁止镜像源码、列每个文件或函数、写通用工程建议。
- 长内容或经常变化内容使用 `@docs/path.md` 引用。

## 验收标准

使用同一个 TypeScript fixture 对比：

- `minimal`：只生成验证、架构约束、编码规则、已知边界。
- `balanced`：额外生成短小 `Key Entrypoints`，说明 `src/index.ts` 和 `normalizeOrder` 的非显然行为。
- `detailed`：可以比 balanced 多写核心类型/函数/模块关系，但仍不生成文件树或泛泛 TypeScript 建议。

验证命令：

```bash
go test ./internal/config ./internal/slashcommands ./internal/cli -count=1
go test ./... -count=1
git diff --check
```

实际操作验收：

```bash
go run ./cmd/golang-cc --cwd <fixture> -p "/init" --permission-mode bypassPermissions --max-turns 8 --output-format json --no-session-persistence
go run ./cmd/golang-cc --cwd <fixture> init --settings
```

`/init` 产物应只创建或改进配置化 guidance 文件，不应修改业务代码、依赖或测试文件。
