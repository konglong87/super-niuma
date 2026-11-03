# TUI Natural Command Aliases Plan

本文档记录 TUI 裸输入自然命令别名的技术方案。目标是在用户输入 `exit`、`退出`、`compact`、`压缩` 时，复用已有 slash command 行为，避免把这些控制意图发送给模型。

## 背景

当前 TUI 已支持 `/exit`、`/quit`、`/compact` 等 slash command。裸输入 `exit` 或 `压缩` 会被当作普通用户消息发送给模型，导致模型回复“再见”等文本，和用户期望的本地控制行为不一致。

## 目标

- `exit` 和 `退出` 与 `/exit` 一样直接退出 TUI 会话。
- `compact` 和 `压缩` 与 `/compact` 一样压缩当前会话。
- 用户界面保留用户原始输入语义，但执行路径复用现有 slash command。
- 不引入新的压缩实现，不绕过现有 session recorder、hooks、错误处理和输出格式。
- 保守匹配，只处理完整裸命令，避免误伤普通对话。

## 非目标

- 不支持自然语言句子解析，例如 `帮我压缩一下上下文`。
- 不把所有中文短语都映射成 slash command。
- 不改变现有 `/exit`、`/quit`、`/compact` 的行为。
- 不修改模型 prompt，让模型自己判断这些控制命令。

## 现有路径

- TUI Enter 提交路径位于 `internal/tui/app.go`，当前在消息追加和模型请求前直接拦截 `/exit`、`/quit`。
- TUI 后续会把 slash command 交给 `internal/cli/cli.go` 的 `handleInteractiveSlash`。
- `/compact` 已由 `compactSlashCommand` 处理，包含 active recorder 校验、session compact、PreCompact/PostCompact hooks 和输出文本。

## 方案

新增一个小型归一化 helper，将完整裸输入映射为已有 slash command：

```go
func normalizeBareInteractiveCommand(input string) string
```

初始映射：

| 用户输入 | 归一化结果 | 行为 |
| --- | --- | --- |
| `exit` | `/exit` | 直接退出 |
| `退出` | `/exit` | 直接退出 |
| `/exit` | `/exit` | 直接退出 |
| `quit` | `/quit` | 直接退出 |
| `/quit` | `/quit` | 直接退出 |
| `compact` | `/compact` | 压缩当前会话 |
| `压缩` | `/compact` | 压缩当前会话 |
| `/compact` | `/compact` | 压缩当前会话 |

匹配规则：

- 先 `strings.TrimSpace(input)`。
- 只匹配完整输入。
- 英文裸命令可用 `strings.EqualFold` 兼容大小写。
- 中文命令精确匹配。
- 带参数或句子不匹配，例如 `exit now`、`请退出`、`compact current session`、`帮我压缩一下`。

## TUI 执行流程

1. 用户按 Enter 后读取原始 `prompt`。
2. 调用 `normalizedPrompt := normalizeBareInteractiveCommand(prompt)`。
3. 如果 `normalizedPrompt` 是 `/exit` 或 `/quit`，直接 `tea.Quit`，不追加用户消息，不启动模型请求。
4. 如果归一化为 `/compact`，继续走现有 slash command 处理链路，调用 `handleInteractiveSlash`。
5. 展示层仍可保留原始 `prompt`，但执行使用 `normalizedPrompt`。

## 设计取舍

- 退出命令在 TUI 输入层拦截，因为 `/exit` 当前也是提交前退出；这能保证不会出现用户消息和模型回复。
- 压缩命令不在 TUI 层重写业务逻辑，而是复用 `/compact`，保证 recorder、hooks、session summary 和错误处理保持一致。
- 归一化 helper 只覆盖小范围高置信别名，后续要加 `状态 -> /status`、`清屏 -> /clear` 时只扩展映射表。

## 测试计划

TUI 回归测试：

- 输入 `exit` 后按 Enter，应返回 quit command，且不调用模型。
- 输入 `退出` 后按 Enter，应返回 quit command，且不调用模型。
- 输入 `compact` 后按 Enter，应执行 `/compact` 路径，且不调用模型。
- 输入 `压缩` 后按 Enter，应执行 `/compact` 路径，且不调用模型。
- 输入 `exit now`、`请退出`、`compact current session`、`帮我压缩一下` 应保持普通消息路径。

CLI/helper 测试：

- 覆盖 trim、大小写、中文精确匹配和不匹配边界。
- 确认已有 slash command 原样保持。

## 验证命令

```bash
go test ./internal/tui ./internal/cli -count=1
go test ./... -count=1
git diff --check
```

## 落地记录

- 2026-07-01 已实现 `normalizeBareInteractiveCommand` 并接入 TUI Enter 提交边界。
- 已覆盖 `exit` / `退出` -> `/exit`，`quit` -> `/quit`，`compact` / `压缩` -> `/compact`。
- TUI transcript 保留用户原始输入；实际执行 prompt 使用归一化后的 slash command。
- 已补回归测试覆盖完整裸命令、大小写/空白、中文别名，以及 `exit now`、`请退出`、`compact current session`、`帮我压缩一下` 等非命中边界。

## 风险

- 如果未来支持带参数的自然别名，需要重新设计参数解析，不应在本方案中扩大范围。
- 如果 TUI 展示层使用归一化后的 prompt 作为用户消息，可能导致用户看到 `/compact` 而不是 `压缩`；实现时应明确区分显示文本和执行文本。
- 如果 helper 被 CLI 非 TUI 路径复用，需要确认普通 stdin interactive 是否也应接受中文裸命令。
