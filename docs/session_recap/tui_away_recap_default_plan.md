# TUI Away Recap 默认机制优化方案

## 背景

Go Claude 已经具备 session recap 能力：

- `/recap` 可以手动生成并展示 recap。
- `recap.mode=post_turn` 可以在每轮 assistant 完成后异步刷新 recap。
- `recap.mode=away` 可以在 TUI 空闲超过 `awayDelaySeconds` 后后台生成 recap。
- transcript 使用 `recap_summary` entry 持久化，且不会进入下一轮模型上下文。

当前问题是默认体验不接近 Claude Code。用户没有显式配置 recap 时，TUI 底部通常不会自动出现 recap；而 Claude Code 的 recap 更像 away summary：用户离开或长时间不输入后，系统自动在底部补一段会话总结。

## 当前根因

当前实现偏保守：

- `internal/recap/config.go` 中 `Config.WithDefaults` 在未配置 `mode` 时默认 `manual`。
- `internal/cli/cli.go` 只有在 `recap.enabled=true` 且 `mode=away` 时，才会给 TUI 注入 `RunAwayRecap` 和 `AwayRecapDelay`。
- `internal/tui/app.go` 已有 idle-away 触发逻辑，但它依赖 `awayRecapDelay > 0`、`runAwayRecap != nil` 和 `awayRecapArmed=true`。
- assistant turn 成功后会 arm away recap；用户键盘活动会 `noteActivity()` 并取消 armed 状态。

因此不是 TUI 不能显示 recap，而是默认配置链路没有把交互式 TUI 启动成 away recap 模式。

## 目标效果

TUI 交互模式默认接近 Claude Code 的 away summary：

```yaml
recap:
  enabled: true
  mode: away
  awayDelaySeconds: 90
```

具体行为：

- 用户完成一轮对话后，assistant 正常结束时开始进入 away recap 候选状态。
- 如果用户 90 秒内没有继续输入、没有打开 slash/picker/permission prompt，输入框为空，且 TUI 不在 busy 状态，则后台生成 recap。
- recap 生成完成后以现有 `※ recap:` 样式展示在底部，并写入 transcript 的 `recap_summary`。
- 同一轮 assistant 回复最多触发一次 away recap，避免重复模型调用和重复 UI 更新。
- 用户显式配置 `recap.enabled=false`、`mode=manual`、`mode=post_turn` 或自定义 delay 时，必须尊重用户配置。
- headless、`-p`、非 TUI interactive 模式不默认多一次模型调用，避免隐藏成本和脚本行为变化。

## `awayDelaySeconds` 计算语义

建议把 `awayDelaySeconds` 解释为“assistant turn 成功完成后，TUI 持续满足可生成 recap 条件的空闲时长”。

推荐状态模型：

```text
assistant success
  -> set awayRecapArmed=true
  -> set awayRecapArmedAt=now
  -> set awayRecapTurnID=current assistant turn id or message counter

user activity / input not empty / picker open / permission prompt / new turn starts
  -> disarm or postpone away recap

now - awayRecapArmedAt >= awayDelaySeconds
  and TUI idle
  and input empty
  and no modal/picker/slash suggestions
  and not busy
  and not already generated for this turn
  -> async generate recap
```

不要把 delay 直接绑定到进程启动时间或最后一次任意 repaint 时间。delay 的起点应该是“assistant 成功完成后进入可 recap 状态”的时间，否则会出现刚回答完就立即触发、或用户输入期间错误触发的问题。

## 配置优先级

实现时必须区分“用户没有配置 recap”和“用户显式配置了 recap”。

建议优先级：

1. 用户显式配置 `recap.enabled=false`：最高优先级，TUI 不自动生成 recap。
2. 用户显式配置 `recap.mode`：严格按 `manual`、`post_turn`、`away` 执行。
3. 用户显式配置 `awayDelaySeconds`：严格使用用户值。
4. 只有在 TUI interactive 模式且 recap 配置整体缺省时，才应用默认 away recap：
   - `enabled=true`
   - `mode=away`
   - `awayDelaySeconds=90`
5. 非 TUI 交互模式继续保持当前保守默认，不自动触发 recap。

为避免误判，配置层需要能表达字段是否显式出现。仅靠 `Config.WithDefaults()` 后的 bool/string 值不够，因为 `false` 既可能是用户显式关闭，也可能是零值。

## 技术方案

### 1. 增加 TUI 专用默认解析

在 CLI/TUI 编排层增加一个 TUI 专用的 recap runtime config 入口，例如：

```go
func tuiRecapConfig(settings config.Settings, defaultModel string) recap.Config
```

职责：

- 读取原始 config，判断 recap block 是否缺省。
- 缺省时只在 TUI interactive 模式注入 away 默认值。
- 非缺省时调用现有 `recap.ConfigFromSettings`，不覆盖用户意图。

不要直接修改 `recap.Config.WithDefaults()` 的全局默认，否则会让 CLI/headless/API 也隐式自动生成 recap。

### 2. 在 TUI model 中显式记录 armed 时间

当前 `maybeStartAwayRecap` 使用 `lastActivity` 计算 idle 时间。建议新增：

```go
awayRecapArmedAt time.Time
awayRecapTurnID   string // 或递增 counter
awayRecapDoneFor  string // 或递增 counter
```

assistant success 时设置 `awayRecapArmedAt=now`。用户键盘活动、新 turn 开始、输入框变为非空、picker/modal 打开时取消或推迟。

好处：

- delay 起点明确。
- 不依赖 `lastActivity` 的多重语义。
- 更容易测试“刚回答完成不足 90 秒不触发”和“超过 90 秒触发”。

### 3. 保持现有阻塞条件

继续沿用并补强当前 `maybeStartAwayRecap` 的阻塞条件：

- `busy == false`
- `pendingPermission == nil`
- resume/rewind picker 未打开
- 输入框 trim 后为空
- slash suggestions 和 slash error 不存在
- 当前没有 away recap 正在运行
- 当前 turn 没有生成过 recap

这些条件是避免“AI 回复中弹 recap”“用户输入时抢模型调用”“授权弹窗时 UI 跳动”的关键保护。

### 4. 生成与展示复用现有 recap 管线

不要新增第二套 recap 存储或展示格式。继续复用：

- `generateRecapWithTelemetry`
- `recap.AppendToPath`
- `tui.StreamRecap`
- TUI `role="recap"` 渲染
- transcript `recap_summary`
- `MessagesFromTranscriptWithReport` 忽略 recap 的现有约束

这样可以减少新增 bug 面，确保 `/recap`、resume、trace、Mobile readonly latest recap 的语义一致。

### 5. 可观测性

沿用现有 `session.recap.started/finished/failed`，并确保 properties 中带：

- `mode=away`
- `trigger=tui_idle_away`
- `away_delay_seconds`
- `idle_elapsed_ms`
- `blocked_reason`，仅在 debug/trace 需要时记录，不记录完整正文

日志和 telemetry 不能记录完整 prompt 或完整 recap 内容。

## 测试计划

### 单元测试

配置层：

- TUI interactive 且 recap config 缺省时，默认 `enabled=true/mode=away/awayDelaySeconds=90`。
- headless 或非 TUI interactive 缺省时，仍保持不自动生成 recap。
- 显式 `recap.enabled=false` 不被 TUI 默认覆盖。
- 显式 `mode=manual/post_turn/away` 不被覆盖。
- 显式 `awayDelaySeconds` 不被覆盖。

TUI：

- assistant success 后设置 `awayRecapArmedAt`。
- idle 小于 90 秒不触发。
- idle 大于等于 90 秒触发一次。
- 用户输入、非空 textarea、slash suggestions、permission prompt、resume picker、busy 状态会阻塞或推迟。
- 同一 assistant turn 不重复触发。
- recap 生成完成后更新底部 `※ recap:`，不插入普通 assistant message。

CLI：

- `tuiAwayRecapDelay` 使用 TUI 默认配置。
- `/recap` 手动命令保持可用。
- `post_turn` 模式不被 away 默认抢占。

上下文安全：

- `recap_summary` 不进入下一轮模型 messages。
- resume 后展示最新 recap，但不会污染 prompt。

### 验证命令

```bash
go test ./internal/recap ./internal/tui ./internal/cli ./internal/config ./internal/session ./internal/query -count=1
scripts/session-recap-smoke.sh
go test ./... -count=1
git diff --check
```

### 真机金标

1. 启动 TUI，完成一轮普通对话。
2. 不输入任何内容，等待 90 秒。
3. 观察底部出现 `※ recap:`，且输入框没有跳动、没有抢焦点、没有覆盖 usage 行。
4. 输入新消息，确认新一轮对话正常进行。
5. 退出后 resume，确认最新 recap 能展示。
6. 检查 transcript 有 `recap_summary`，下一轮模型请求不包含 recap 正文。

## 风险与保护

| 风险 | 保护策略 |
| --- | --- |
| 默认增加一次模型调用成本 | 只在 TUI interactive 且 recap config 缺省时启用；headless 不默认启用；用户可 `recap.enabled=false` 关闭。 |
| 用户正在输入时后台生成 recap | 输入框非空、slash/picker/permission/busy 状态全部阻塞。 |
| 同一轮重复生成 | 记录 turn id 或 message counter，确保每轮最多一次。 |
| recap 污染下一轮 prompt | 继续由 query transcript builder 忽略 `recap_summary`，并补回归测试。 |
| 终端 focus/blur 不稳定 | P1 不依赖 terminal focus reporting，只用 TUI activity 和 idle 条件；后续可单独评估真实 focus 事件。 |
| UI 闪烁或覆盖底部 usage | 生成完成后复用现有 stream event 和 render 管线，并做真机金标验证。 |

## 分阶段实施

## 落地记录

- TUI 专用配置解析已落地在 CLI 编排层，不修改全局 `recap.Config.WithDefaults()`，因此 headless、`-p`、API 等非 TUI 路径不会因为默认值多触发一次模型调用。
- TUI 无 recap 配置时默认 `enabled=true/mode=away/awayDelaySeconds=90`。
- 用户只配置 `recap.awayDelaySeconds` 时，TUI 仍按默认 away 模式启用，并使用该自定义等待秒数。
- 用户显式 `recap.enabled=false` 会关闭 TUI away recap；显式 `mode=manual` 或 `mode=post_turn` 不会被 away 默认覆盖；显式 `mode=away` 会启用 away recap。
- TUI 状态机已记录 `awayRecapArmedAt`，等待时间从 assistant turn 成功完成后开始计算；busy、权限弹窗、resume/rewind picker、非空输入框、slash suggestions 和 slash error 都会阻止触发。

### 跨轮异步竞争修复

真实会话曾出现上一轮 away recap 已开始生成、用户在模型返回前提交下一轮，旧 recap 随后插入新一轮 assistant/tool 时间线的问题。启动前的 idle gate 无法覆盖“任务已经运行”的窗口，因此自动 recap 现在同时具备三层保护：

- TUI 为 away/post-turn recap 共用一个可取消 job；任何新用户活动或新 turn 都会取消当前 job。
- 每个 job 携带单调递增的 generation；即使 provider 在取消边界仍返回文本，旧 generation 的 Bubble Tea 消息也会被丢弃。
- 自动 recap 写入 `recap_summary` 前重新加载当前 conversation，并校验 head 仍等于生成结果的 `summarizes_entry_id`；head 已变化时既不落盘，也不发送 UI 事件。

手动 `/recap` 是用户当前明确发起的同步命令，继续使用原有写入语义，不受自动 recap 过期过滤影响。recap 仍不进入模型上下文，transcript schema 与现有 resume/trace 消费方式不变。

### Phase 1：默认配置与状态机

- 增加 TUI 专用 recap 默认解析。
- 增加 `awayRecapArmedAt` 和去重标记。
- 补配置和 TUI idle 单元测试。

### Phase 2：回归与观测

- 补 CLI、query transcript、session recap smoke 回归。
- 确认 telemetry properties 完整。
- 执行全量测试和真机金标。

### Phase 3：可选增强

- 如后续需要进一步接近 Claude Code，可评估 terminal focus reporting。
- focus/blur 必须作为 opt-in 或经过真机验证后再默认启用，避免影响复制、鼠标滚动、输入法和权限弹窗。
