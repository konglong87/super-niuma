# 修复：完成度闸门 `<system-reminder>` 泄漏进 transcript 与 /rewind 选择器

> 状态：已修复并验证。发现于 v2 消息图默认开启后的真实 TUI 验收。

## 1. 现象

真实会话里「改了文件后执行 `/rewind`」，回退候选列表出现一条：

```
Rewind messages  1/2  up/down select  enter restore  esc cancel
> <system-reminder>Completion is blocked by Post-Action Delta Gate: files or shared state changed i...
    008e9e2d-…  message
  继续 P0-2（…按照 docs/plans/…）
    d42ccff4-…  message
```

用户合理地怀疑：**这是内部提示词泄露了吗？**

## 2. 结论：不是对外泄露，是运行时提醒被错误持久化

`<system-reminder>` 是**本地运行时**注入到「请求消息」里的临时提示——这里是**完成度闸门**（closure/completion gate）在「文件/共享状态改了但缺少校验」时，用来推动模型继续、先验证再收尾的续跑提醒（`Post-Action Delta Gate` 等）。

它**从不发往任何外部**；问题在于它**被当成一条 user 消息写进了持久化 transcript**，于是：

- resume / `session show` / inspect 里都能看到这条本不该落盘的提醒；
- `/rewind` 候选把它当成「一个可回退的用户轮次」列了出来，让人误以为是自己发的话/或提示词外泄。

## 3. 根因

两处叠加：

1. **该落盘吗——不该，但落了。** [`internal/query/query.go`](../../internal/query/query.go) 完成度闸门分支里，nudge 先 append 进 live `messages`（正确，驱动本次重试），随后又 `s.recordMessage(...)` **持久化**（错误）。
   对照物证：
   - 同文件里初始的 `continuationReminder` 只 append 进 `messages`、**不** record —— 说明设计意图就是「请求态提醒不落盘」。
   - transcript v2 schema 设计明确：**"request-only runtime reminder 不写入 transcript"**（final-turn / tool-planning / 各类 transient 提醒）。
   - 审计留痕本就由 `recordClosureEvent("completion_gate", …)` 这条**闸门事件**承担，和「把提醒复制成一条 user 消息」是重复的。
   ⇒ 这条 `recordMessage(nudge)` 是与设计不一致的疏漏。

2. **`/rewind` 候选不加过滤。** [`internal/cli/cli.go`](../../internal/cli/cli.go) `rewindCandidates` 把**任意** user 消息都当回退目标，没有排除运行时注入的提醒。即使停止持久化，历史已污染的 transcript 仍会在选择器里带出这条。

## 4. 方案（两处修复，一根一防）

- **根因修复（不再持久化）**：删除闸门分支里的 `s.recordMessage(nudge)`。nudge 仍留在 live `messages` 中驱动重试；审计仍由 `completion_gate` 闸门事件承担。新会话的 transcript 不再出现这条提醒。
- **防御式修复（历史兼容）**：`rewindCandidates` 跳过内容以 `<system-reminder>` 开头的 user 消息——它们是运行时注入、不是人类轮次，不应作为回退目标。这样**已经被污染的旧 transcript**（如本次验收的会话）在选择器里也干净了。

## 5. 建议与理由

- **两处都做，而不是只做其一。**
  - 只做根因修复：新会话干净，但用户手上已存在的会话仍会带出旧提醒——体验不闭环。
  - 只做候选过滤：选择器干净了，但提醒仍污染 resume / show / inspect，且违背「请求态提醒不落盘」的设计不变量。
  - 合起来：**源头不再产生 + 存量优雅降级**，且回归到既定设计。
- **为何不改成「记录但打特殊类型」**：完成度闸门的可观测性已由 `completion_gate` 闸门事件覆盖；再把提醒本身落成 user 消息是重复且会污染对话视图。保持「提醒＝请求态、审计＝闸门事件」的清晰分工最简洁。
- **兼容性**：不改 transcript schema、不动闸门判定与 live 重试行为；仅少写一条本不该写的行。

## 6. 影响面 / 非影响面

- **不影响**：完成度闸门的判定与重试（nudge 仍在 live 请求里）；`completion_gate` 审计事件；resume 的工具配对与上下文重建。
- **影响（正向）**：新会话 transcript 不再含闸门 `<system-reminder>`；`/rewind` 选择器只列真实用户轮次（新旧会话均然）。

## 7. 验证

- `TestQueryGoldenContinuationIntent`：golden 更新后 diff **只删除**了被持久化的 nudge（一条 rebuilt-message + 一条 transcript message 条目），`completion_gate` 审计事件保留；`req.Messages` 断言（闸门提醒仍在 live 请求）全绿。
- 新增 `TestRewindCandidatesSkipRuntimeReminders`（[internal/cli](../../internal/cli)）：构造「真实用户消息 + `<system-reminder>` 消息」，断言候选只含真实用户消息。
- `go test ./...` 全量绿。

## 8. 落点

| 改动 | 位置 |
|---|---|
| 不再持久化闸门 nudge | [internal/query/query.go](../../internal/query/query.go) 完成度闸门分支（删除 `recordMessage(nudge)`） |
| /rewind 候选过滤运行时提醒 | [internal/cli/cli.go](../../internal/cli/cli.go) `rewindCandidates` |
| 回归测试 | [internal/cli/cli_test.go](../../internal/cli/cli_test.go)、`internal/query` golden |
