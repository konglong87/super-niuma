# 修复计划：交互卡片弹出时覆盖整轮对话

日期：2026-07-25
状态：已实施（2026-07-25）。生产改动只有 `internal/tui/app.go`，净增 8 行；真实 Terminal.app
截图 A/B 见文末「验收结果」。

## 现象

AskUserQuestion 卡片弹出时，本轮已产生的全部对话（数百行）从屏幕上消失，屏幕上只剩一张
约 12 行的卡片。用户反馈的对比基准是 Claude Code / Codex：交互 UI 只在输入框上方按需占用
几行（6~10 行），不影响上方内容。

实测 session：`~/.go-claude/projects/Users-example-GolandProjects-hall-of-fame/251b6688-1555-4ff5-bd9f-a964e10e8c4b.jsonl`
第 34–45 行有完整的三轮 AskUserQuestion 和一大段土耳其历史回答，但用户截图里这些内容全部
不在屏幕上——只有最后那张卡片。

权限卡片（`pendingPermission`）走同一条代码路径，症状相同。

## 根因（两个缺口叠加）

### 缺口 1：卡片「替换」live body，而不是「追加」

`internal/tui/app.go:3049` `liveTranscriptView()`：

```go
if m.pendingPermission != nil {
    return permissionPromptView(...)      // ← 直接 return
}
if m.pendingQuestion != nil {
    return questionPromptView(...)        // ← 整个 live body 被丢掉
}
...
return m.appendLiveWorkingLine(m.liveTranscriptBody())   // 本轮所有输出在这里
```

viewport 的内容就是这个函数的返回值（`refreshViewport()`，app.go:6422），高度按内容算：
`min(normalHeight, contentHeight)`（`viewportHeightForContent`，app.go:6490）。卡片一弹，
viewport 内容从数百行塌到约 12 行，高度同步塌缩。程序运行在 inline 模式（app.go:1700-1711，
没有 `tea.WithAltScreen`），bubbletea 会清掉上一帧占用的行再画新的小帧，视觉效果就是
「整轮对话被覆盖」。

### 缺口 2：交互卡片弹出前不 flush 到 scrollback

已完成的对话块通过 `tea.Println` 进真实终端滚动区（`commitTranscriptFlushCmd`，app.go:2759），
但触发时机只有 6 个（`transcriptFlushReason`，app.go:179-187）：

```
Initial / UserSubmit / TurnComplete / SessionResume / Background / Recap
```

**没有「弹交互卡片前」这一档。** 所以一个长 turn 内（实测那次是 3 轮 AskUserQuestion 加一段
长回答），从 `UserSubmit` 之后就再没 flush 过，被卡片顶掉的内容也不在 scrollback 里，等于
彻底不可见——连终端原生滚动都找不回来。

这就是 Claude Code / Codex 不会出现该问题的原因：它们把完成的输出持续推进滚动区。

## 方案（两步，缺一步效果都不完整）

### 第 1 步：卡片下沉到 bottom chrome

- 删掉 `liveTranscriptView()` 的两处早退（app.go:3053-3058）。
- 在 `bottomChromePartsForBudget()`（app.go:2349）中加入 `questionPromptView` /
  `permissionPromptView`，位置紧邻现有的 `slashSuggestionView` / `resumePickerView` /
  `rewindPickerView`——这三者已经是「按需占几行、不动上方内容」的同类 UI，本步骤等于把
  交互卡片归到它们同一层级。
- **高度不需要新逻辑**：`normalViewportHeight = height - fixedChromeHeight()`（app.go:6486），
  chrome 变高，viewport 自动少几行，对话继续显示、只是往上挤了十来行。

实施注意：`fixedChromeHeight()`（app.go:6504）内部调用 `liveTranscriptView()` 判空，而
`normalViewportHeight()` 依赖 `fixedChromeHeight()`。卡片渲染不依赖 viewport 高度，因此
不构成递归；但删早退后要确认 `fixedChromeHeight()` 的判空语义仍然成立。

### 第 2 步：新增 flush 时机，把上文推进真实滚动区

- `transcriptFlushReason` 增加 `transcriptFlushInteractivePrompt`（app.go:179-187）。
- 在卡片唯一的弹出点调用 `prepareTranscriptFlushCmd`：app.go:1929-1943 的 `streamEventMsg`
  分支，`StreamPermissionRequest`（1931）和 `StreamUserQuestion`（1936）两处。两处目前都是
  `return m, nil`，改为返回 flush cmd 即可；同一函数 app.go:1951 的 `StreamSessionResume`
  已有现成写法可照抄（`tea.Sequence(printCmd, ...)`）。
- **`prepareTranscriptFlushCmd`（app.go:2705）本身不用改**：它取 `pendingTranscriptBlocks()`
  / `readyTranscriptCount()`，本来就是「只推已完成的块、未完成的留在 live」的部分 flush
  设计。
- 效果：上文永久留在终端滚动区，可以用终端原生滚动回看。

两步分工：第 1 步保证卡片不覆盖当前帧，第 2 步保证被移出 viewport 的内容进入真实滚动区。

## 明确不做（本次）

**卡片过高时的截断 / 内部滚动。**

`chromeModeForBudget()`（app.go:2329）在 `pendingQuestion != nil` 时无条件返回 `chromeFull`，
绕过了 `MaxChrome` 检查（上限 `maxBottomChromeHeight = 12`，app.go:155-156）；
`questionPromptView()`（app.go:8617）也不限制选项数量。问题文字换 3 行加 6 个选项约 14 行，
会把 viewport 压到 `minViewportHeight = 3`。

**本次维持现状**：即使被压到 3 行，也严格好于当前的整屏覆盖，不为此引入新的截断逻辑。
若实际使用中经常出现 5 个以上选项，再单独起一条做选项截断（显示「还有 N 项」）或卡片内
滚动。

## 测试计划

新增（改之前必须都是红的）：

1. `TestQuestionPromptKeepsLiveTranscript`：构造带长 live body 的 Model，置 `pendingQuestion`，
   断言 `liveTranscriptView()` 仍包含 live body 的内容。
2. `TestQuestionPromptRendersInBottomChrome`：断言卡片出现在 `bottomChromeParts` 里，而不是
   viewport 内容里。
3. `TestPermissionPromptKeepsLiveTranscript`：权限卡片同构，防止只修一半。
4. `TestInteractivePromptFlushesCompletedBlocks`：置 `pendingQuestion` 时产生 flush cmd，
   已完成块进入 printed 输出、未完成块仍留在 live。

需要检查的现有测试：搜索断言「卡片独占 viewport」或直接比较 `liveTranscriptView()` 与
`questionPromptView(...)` 相等的用例，随行为变更一起更新。

## 验证步骤

1. `go test ./internal/tui -count=1`
2. `go test ./... -count=1`、`go vet ./...`、`gofmt -l .`、`git diff --check`
3. 手动：长回答（超过一屏）后触发 AskUserQuestion，确认上文仍在、卡片只占底部若干行、
   终端原生滚动能回看被挤出去的内容
4. 手动：权限卡片同样验证一遍
5. 手动：窄终端（24 行）+ 5 个选项，确认 viewport 不塌到 0、输入框仍然可见

## 验收结果（2026-07-25）

单测：4 条改前全红、改后全绿。既有测试有 8 处失败，全部来自 2 个「弹卡片时 `cmd` 必须为
nil」断言（`app_test.go` 的 `newUserQuestionModel` helper 与权限测试）——第 2 步之后那里
返回的是 flush command，已按新行为更新断言。

真实终端：新增 `scripts/tui-interactive-prompt-terminal-acceptance.sh`，用真实 Terminal.app
＋真实 PTY（`script`）＋`screencapture` 拍图，场景是「先流出 40 行正文，再弹卡片并阻塞等
作答」。

- 修复后 `question-card-visible.png`：`ANSWER_LINE_25`–`ANSWER_LINE_40` 和
  `ANSWER_TAIL_SENTINEL` 都还在屏幕上，卡片在其下方只占约 10 行。
- 修复后 `after-answer.png`：40 行正文完整留在终端真实滚动区（可原生滚回），卡片消失回到
  `status Ready`，且正文**没有**被重复打印。
- A/B 对照：`git stash push internal/tui/app.go` 后用同一脚本重跑，截图显示用户输入下面
  直接就是卡片，40 行正文全部消失 —— 即修复前的现象。

脚本内的程序化断言：正文哨兵在 typescript 中必须出现 ≥2 次（一次流式渲染进 viewport，
一次 flush 到真实 scrollback）。实测修复前 1 次、修复后 3 次，因此这条断言确实能抓住回归。
但它守的是「内容进了 scrollback」；「卡片只占底部若干行」这个版式结论仍然依赖截图和单测。

`scripts/tui-visual-regression-sop.sh` 的 semantic-terminal / recap-terminal / ten-turn /
display-order 四步通过。session-replay 一步在本机无法通过：该脚本写死了 4 个本机已不存在的
session ID，在第 26 行 `exit 1`，发生在执行任何 Go 代码之前，与本次改动无关（已另行登记）。

## 相关文档

- [../askuserquestion_interactive_design.md](../askuserquestion_interactive_design.md) —— 卡片的四层链路设计
- [tui_natural_scrollback_design.md](tui_natural_scrollback_design.md) —— scrollback 架构
- [../tui_viewport_overlap_fix_plan.md](../tui_viewport_overlap_fix_plan.md) —— 同区域的历史修复
  （长回复末尾被工具栏遮挡）。该修复让 `View()` 改用 `m.viewport.View()`，本计划建立在它之上，
  不与其冲突
