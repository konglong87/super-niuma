# TUI 流式渲染性能与可取消性修复方案 [Streaming Render Performance Fix Plan]

本文档记录「AI 回复长内容时持续输出十几分钟不结束、Ctrl+C 停不下来、流式期间只见尾部」这组问题的根因、修复方案和验收标准。属于 `docs/tui/` 系列：前序阶段（`tui_display_timeline_architecture_plan.md`、`tui_render_budget_architecture_fix_plan.md`、`tui_transcript_bottom_chrome_fix_plan.md`、`tui_completed_assistant_tail_anchor_fix_plan.md`、`tui_visual_line_height_overlap_fix_plan.md`）分别确立了 displayTimeline 单一真相源、render budget、scrollback 落盘与 tail anchor、可视行高等布局架构；**本阶段处理的是流式渲染的性能与可取消性，不改上述布局架构。**

## 背景

- 复现会话：`sessionId=cab17b58-0e87-49a1-9491-5081d2e17a6e`，项目 `superPM`，`model=gpt-5.5`，`provider=custom/openai-compatible`，`ui=tui`。
- transcript：`~/.go-claude/projects/Users-example-GolandProjects-superPM/cab17b58-0e87-49a1-9491-5081d2e17a6e.jsonl`（仅 14 行）。
- 用户提第 2 个问题「你有什么具体执行方案吗？优先级是什么」后，屏幕状态行 `Stopping... elapsed=15m53s`，用户手动强杀才结束。

**截图观感 vs. JSONL 真相：**

| 观察 | 截图/用户描述 | JSONL 记录（真相） |
|------|--------------|-------------------|
| 第 2 轮模型是否慢 | 持续输出 16 分钟 | `usage` 于 `14:30:21` 记录 `out=3219`，即**模型 ~59s 就已生成完毕**（`recordUsage` 在流结束后调用，见 `internal/query/query.go:1496`） |
| 卡在哪 | 一直在"输出" | 第 2 轮 assistant `message` **始终未写入**（`recordAssistant` 在 Replay 之后，见 `query.go:1595`）——卡点在**生成之后的回放+渲染** |
| token 计数 | 状态行停在 `in/out=15543/1487`（第 1 轮值） | 说明 TUI 事件处理被慢渲染堵住，尚未追上第 2 轮末尾的 usage 事件 |

对照第 1 轮：prompt `14:24:13` → `usage 14:24:49`（生成 ~36s）→ assistant `message 14:28:16`（渲染 ~3m27s）。**两轮都印证：生成快，渲染慢，且渲染耗时随输出长度超线性增长**（1487 tok → 207s；3219 tok → >900s 仍未完，约 2.16× 长度对应 ~4.7× 耗时，符合二次方）。

## 根因

### 1. 流式 markdown 渲染是 O(N²)

```text
模型流结束(usage, out=3219, ~59s)
  → query.Replay(query.go:1087) 把缓冲的 N 个文字块一次性推入 StreamEvent channel
  → TUI 每收到 1 个 streamEventMsg(app.go:1873) 就 refreshViewport 一次(app.go:1890)
  → refreshViewport(app.go:6130) → renderStreamingMarkdown(全量累积内容, app.go:6410)
  → renderMarkdownForWidth(app.go:6474): 每次 new glamour.NewTermRenderer(app.go:6485)
      + normalizeMarkdownHeadings / renderMarkdownTables / extractRichMarkdownInlineStyles 全文多遍正则
  → 无合并、无 renderer 复用、无缓存
  → N 个块 × 每次 O(全文长) = O(N²)
```

关键事实（已 grep 确认）：`internal/tui/` 内**不存在任何 markdown 渲染缓存或节流**；`renderMarkdownForWidth` 每次调用都新建 `glamour.NewTermRenderer`。流式事件经 `waitForStreamEvent`（`app.go:4716`）**逐个**投递，一个事件一次 Update 一次全量渲染，无合并。

### 2. Replay / 渲染循环不接收取消 → `Stopping...` 卡死

```text
用户 Ctrl+C(app.go:1712) → turnCancel() 取消 turnCtx(app.go:1839)
  但此刻模型早已结束，卡点在 Replay + 渲染，而非模型请求
  → query.Replay(query.go:1087) 不接收 ctx，循环(1106-1139)内无 ctx.Err()/ctx.Done() 检查
  → TUI Update 单线程，正忙于 glamour 渲染，无法处理后续按键
  → 状态行显示 "Stopping..." 但实际停不下来，只能强杀进程
```

### 3. 流式期间只显示尾部（"被 header 遮住"的真相）

```text
流式期间内容只写入 live 层(displayTimeline 的 streaming 段)
  → refreshViewport 把全量 live 内容塞进 viewport
  → viewport 高度 = 终端高 − 固定chrome高(normalViewportHeight, app.go:6184)，bounded
  → stickToBottom=true → GotoBottom(app.go:6141)：只显示贴底的最后 N 行
  → 早出现的行既未落盘(scrollback)、又被挤出 bounded viewport → "消失"
  → 直到 responseMsg(本轮完成, app.go:1987) 才 tea.Println 一次性落盘 → 全部重现
```

**纠正一个误判：** 用户描述"被欢迎 header 遮住"，但 `shouldShowWelcome()`（`app.go:7451`）一旦出现任何 user/assistant 消息即返回 false，`shouldShowIdleWelcomeLive()`（`app.go:3016`）还额外要求 `!m.busy`；代码里也有防止 busy 期间把欢迎 header 落盘的注释（`app.go:1937`）。**busy 期间欢迎 header 并不显示**，用户所述实为 bounded viewport 只显示尾部的观感。故不做"隐藏欢迎 header"这类修复（会是 no-op）。

### 触发条件：为什么只在 gpt-5.5 上暴露（上游 SSE 分帧粒度）

渲染代码与模型无关，`custom/openai-compatible` 也只固定了**客户端侧**三件事：适配器（怎么解析 SSE）、线缆协议、乃至同一个自建网关（`settings.json` 的 `ANTHROPIC_BASE_URL` / `fallback.baseURL`）——`gpt-5.5` / `deepseek-v4-pro` / `glm-5.1` 走的都是同一个网关 URL。**它没有固定"谁在上游生成 token、以及那个引擎多久 flush 一次 SSE 帧"。** 该网关是**反向代理**，背后接三个不同的真实后端（OpenAI 系 / 智谱 / 月之暗面），只把各后端**原生 SSE 原样转发**。

```text
上游后端生成 token，按各自节奏 flush SSE 帧
  → internal/anthropic/client.go:421-437：每个 choice.Delta.Content(非空) → 一次 cb.OnText（1:1，零合并，textDeltas++）
  → 一个缓冲文本事件 → (Replay 后) 一个 StreamEvent → 一次 refreshViewport → 一次「全量」重渲
∴ TUI 全量重渲次数 = 上游这一轮吐出的正文 SSE 帧数(text_deltas)
```

客户端 1:1 转发、零合并，故重渲次数**完全继承上游分帧粒度**。真实遥测全量聚合（源：`~/.go-claude/debug/golang-claude-code-tui.log` 的 `model.phase.stream.read.finished` 事件，字段 `properties.text_deltas` / `chunks`）：

| 模型 | 采样调用 | 平均 chunks | 平均 text_deltas | 最大 text_deltas | chunks÷text_deltas |
|---|---|---|---|---|---|
| gpt-5.5 | 4 | 1147 | **1120.8** | **3067** | ~1.02 |
| glm-5.1 | 419 | 174 | **48.9**（中位 7） | 1198 | ~3.6 |
| kimi-k2.6 | 307 | 31 | **4.5**（中位 0） | 100 | ~7 |

- `gpt-5.5`：3067 text_deltas ≈ 该轮 3219 输出 token → **≈1 帧/token**（最细粒度），chunks 几乎全是正文帧。这正是让 `O(重渲次数 × 长度)` 爆炸的最坏输入。
- `glm-5.1` / `kimi`：分帧粗得多，且 chunks 远多于 text_deltas。

附带成因：`glm`/`deepseek` 是推理模型，会吐 `reasoning_content` 增量帧，但 `internal/anthropic` **全包无 `reasoning` 解析**，这些帧不进任何回调、不触发渲染 → 真正驱动重渲的正文帧更稀疏。（副作用：这两个模型的思考过程在本客户端也未显示，属独立缺口，不在本方案范围。）

**诚实保留：** 遥测事件 `session_id` 未落值（=0），无法把每次调用精确对齐其输出 token 数，故不能完全排除"glm 这些轮回答更短"的混淆因素；但 `gpt-5.5 ≈1 帧/token` 结论确凿且自足，单凭它即可解释为何 O(N²) 只在 gpt-5.5 爆炸。若需无懈可击证据：用受控对比脚本（同 prompt 三模型各跑一轮，打印 `chunks`/`text_deltas`/`output_tokens`）分离"分帧粒度 vs 回答长度"。

**对修复的含义：** P0 的「合并渲染」使重渲次数与上游分帧粒度**解耦**——无论上游逐 token 还是粗分帧，客户端成本都拉回线性，gpt-5.5 会与 glm/kimi 一样快。

### 三者的关系（重要）

问题 3 的**痛感主要是问题 1 的下游症状**：一旦渲染变快，本轮从"16 分钟"缩短到"~1 秒"完成并落盘，"只见尾部→完成后全出现"的中间态一闪而过，用户基本无感（等同原生 Claude Code 快速流式的观感）。因此 **P0 聚焦问题 1、2；问题 3 作为条件性 Phase 2，待问题 1 修复后重新评估是否仍痛。**

## 修复目标

**P0 目标：**
1. 长响应流式渲染耗时随长度**近似线性**，不再二次增长；十几分钟的场景降到秒级。
2. 渲染/回放期间 Ctrl+C 可**即时中止**，`Stopping...` 名副其实。
3. 完全保持现有 markdown 视觉产出（rich-inline 颜色、表格、标题补空格等）不变。

**非目标：**
1. 不改 displayTimeline 作为显示单一真相源的架构。
2. 不把"逐 token 写入 permanent scrollback"作为 P0——这与既有"streaming 只走 live preview"决策冲突，仅在 Phase 2 条件性重开（见风险表）。
3. 不改 markdown 视觉样式管线：`extractRichMarkdownInlineStyles` / `restoreRichMarkdownInlineStyles` / `renderMarkdownTables` / `<span><font>` 颜色必须保持行为不变。
4. 不改 provider/模型层与 query loop 业务逻辑；对 `internal/query` 仅新增取消检查（见风险表说明边界）。

## 方案

### 1. 复用 glamour renderer（按宽度缓存 renderer 本身）

`renderMarkdownForWidth` 每次都 `glamour.NewTermRenderer(...)`，构造开销是重常数因子。renderer 仅依赖 `width`（`WithWordWrap(width)`）与恒定 style，可按宽度缓存复用。

```go
// internal/tui/app.go（示意）
var (
    glamourRendererMu    sync.Mutex
    glamourRendererByWidth = map[int]*glamour.TermRenderer{}
)

func glamourRendererForWidth(width int) (*glamour.TermRenderer, error) {
    glamourRendererMu.Lock()
    defer glamourRendererMu.Unlock()
    if r, ok := glamourRendererByWidth[width]; ok {
        return r, nil
    }
    r, err := glamour.NewTermRenderer(
        glamour.WithStyles(assistantMarkdownStyle),
        glamour.WithWordWrap(width),
        glamour.WithPreservedNewLines(),
    )
    if err != nil {
        return nil, err
    }
    glamourRendererByWidth[width] = r
    return r, nil
}
```

`renderMarkdownForWidth` 内 `// 修改前` 每次 New → `// 修改后` 取 `glamourRendererForWidth(width)`。表格/rich-inline 前后处理管线保持不动。

### 2. 流式渲染合并（drain channel，每个 Update 周期只渲一次）—— 治 O(N²) 的核心

当前一个 stream 事件触发一次全量渲染。改为：收到 `streamEventMsg` 后，**非阻塞 drain** channel 中已缓冲的同类事件全部 apply，最后 `refreshViewport()` **一次**，再 re-arm。Replay 突发的 N 个块被压成"每 Update 周期 1 次渲染"，把 N 次全量渲染降到常数级。

```go
// internal/tui/app.go：case streamEventMsg（示意）
m.applyStreamEvent(msg.event)
drained := 0
for drained < maxStreamDrainPerTick {          // 上限防饿死其它消息
    select {
    case ev, ok := <-msg.ch:
        if !ok { break }
        m.applyStreamEvent(ev)                  // 只累积内容，不在循环内渲染
        drained++
        continue
    default:
    }
    break
}
m.refreshViewport()                             // 合并后只渲一次
return m, waitForStreamEvent(msg.ch)
```

> 注：permission/finished/session-resume 等特殊事件仍需保持既有单独处理路径，drain 仅针对 text/thinking 文本增量。

### 3.（可选，次要）渲染结果缓存 `(content,width) → string`

小容量缓存，惠及**内容不变的重复刷新**（窗口 resize、spinner tick 触发的 refreshViewport、完成态消息重渲、最终落盘）。**流式因内容每块都变、命中率低，不是主要收益**，故列为按需项：若方案 1+2 后 profiling 显示 tick 类重复渲染仍显著再加。缓存键必须含 `width`（render budget 下 resize 会改宽度）。

### 4. Replay 支持取消

```go
// internal/query/query.go（示意）
func (b *turnStreamCallbackBuffer) Replay(ctx context.Context, cb runCallbacks, accepted *anthropic.MessageParam) error {
    for _, event := range b.events {
        if err := ctx.Err(); err != nil {   // 修改后新增
            return err
        }
        // ... 原有 switch 回放逻辑不变
    }
    return nil
}
```

调用处（`query.go:1587` 成功路径、`query.go:1456` 错误路径）传入 `ctx`。TUI 侧 `turnStopping` 时对残留事件走 drain-and-discard，避免继续渲染。

### 5.（Phase 2，条件性）流式增量落盘 —— **需显式重开既有架构决策**

**仅当 Phase 1 修复后，长响应流式期间"看不到早期行/无法上翻"仍构成真实痛点时才做。** 既有决策明确"streaming 期间只展示 live preview，不逐 token 写 permanent scrollback"（`tui_transcript_bottom_chrome_fix_plan.md`）。若重开：
- 流式期间把当前 assistant 段中**已稳定的完整行**（除最后一行可能仍在写）通过 displayTimeline 的 **`printedSeq` 机制**增量落盘，live 层只保留未定的最后一行；
- **禁止**另起平行的 `tea.Println` 路径（会重蹈 live/transcript 重复与发散——displayTimeline 当初正为消灭该类回归而建）；
- 需新增 `transcriptFlushReason` 的 streaming 取值（当前枚举 `app.go:177` 无此值）。

## 实施步骤

### Phase 0：冻结回归测试（失败优先）
新增下列测试，先让它们在现状下**失败**：
- `TestRenderMarkdownReusesGlamourRenderer`
- `TestStreamingBurstCoalescesRenders`
- `TestReplayHonorsCancellation`

验收：
```bash
go test ./internal/tui ./internal/query -run 'ReusesGlamour|CoalescesRenders|ReplayHonorsCancellation' -count=1   # 预期失败
```

### Phase 1：性能 + 可取消（P0）
落地方案 1（renderer 复用）、2（合并渲染）、4（Replay 取消）。使 Phase 0 测试转绿。

验收：
```bash
go test ./internal/tui ./internal/query -run 'ReusesGlamour|CoalescesRenders|ReplayHonorsCancellation' -count=1
go test ./internal/tui -count=1
```

### Phase 2：重新评估问题 3（条件性）
真机复跑长响应，确认渲染已秒级完成、Ctrl+C 即时生效。若"流式中无法上翻查看早期行"仍痛，再评审方案 5（增量落盘），否则关闭该项。

### Phase 3：（如做方案 5）增量落盘 + 防重复/发散回归测试。

## 测试计划

**失败优先单测（Phase 0）：**
| 测试 | 构造 | 断言 |
|------|------|------|
| `TestRenderMarkdownReusesGlamourRenderer` | 用构造计数 hook，同宽度渲染两段 | renderer 只构造 1 次（修前=2，失败） |
| `TestStreamingBurstCoalescesRenders` | 向 channel 预灌 M 个 text 事件，跑一次 Update 周期 | 全量渲染次数 ≤ 常数（修前=M，失败） |
| `TestReplayHonorsCancellation` | 传入已取消的 ctx 调 Replay | 提前返回、剩余块不回放（修前全回放，失败） |

**性能观测（informational，不作硬断言防抖动）：**
- `BenchmarkStreamingMarkdownGrowth`：模拟 N 递增块流式，观察总耗时对 N 近似线性、不再二次增长。

**回归（保护视觉管线）：** 现有 render / 表格 / rich-inline / 标题补空格 相关测试全过。

**真机 PTY 验收：** tmux/PTY 跑一个长响应会话，证据存 `/tmp/gocc-tui-*`：
1. 流式流畅、无十几分钟卡顿；
2. 流式中 Ctrl+C 立即停止（`Stopping...` 即时生效）；
3. 完成后 scrollback 内容完整、顺序正确、视觉样式无回归。

**闭环三连：**
```bash
go test ./internal/tui -count=1
go test ./... -count=1
git diff --check
```

## 风险与控制

| 风险 | 控制 |
|------|------|
| 合并渲染削弱"逐字/逐段"动画感 | drain 只合并**同一 Update 周期内已缓冲**的事件；正常慢速流式仍逐段推进；`maxStreamDrainPerTick` 设上限防其它消息饿死 |
| glamour renderer 复用的并发安全 | 用 `sync.Mutex` 串行化；渲染本就在 TUI 单线程 Update 中调用，无真并发 |
| 渲染结果缓存内存增长 | 方案 3 为可选、小容量、按 width 清理；P0 只做复用+合并，可不引入缓存 |
| 改动 `internal/query` 触碰既有"query 层不改"边界 | 该边界原为 **layout 修复**设定（见 render-budget / bottom-chrome 文档）；本次为**取消/性能**且改动极小、仅新增 `ctx.Err()` 检查，属加法不改语义，在此显式声明并评审 |
| Phase 2 增量落盘引入 live/transcript 重复或发散 | 仅 Phase 1 不足时才做；强制走 `printedSeq`、禁平行 `tea.Println`；附防重复/发散回归测试；否则不做 |

## 完成定义

1. 长响应流式渲染耗时随长度近似线性，十几分钟场景降到秒级（benchmark 佐证趋势）。
2. 渲染/回放期间 Ctrl+C 即时中止。
3. Phase 0 三个失败优先测试转绿；现有 render/表格/rich-inline/视觉样式回归全过。
4. `go test ./internal/tui -count=1`、`go test ./... -count=1`、`git diff --check` 通过；PTY 真机验收留证 `/tmp/gocc-tui-*`。
5.（若做 Phase 2/3）流式可上翻查看早期行，且无 live/transcript 重复或发散。

## 实施状态（2026-07-25）

- **Phase 0/1 已实施并通过测试**（`fix: TUI 流式渲染按时间节流 + Replay 支持取消`）：
  - 方案 2（流式渲染节流）：文本/思考 delta 改为按 `streamRenderThrottleInterval`（50ms）时间门限渲染，连续 burst 内合并为一次全量渲染；阶段切换（工具等非文本事件）时重置门限，使其后首个 delta 立即渲染。
  - 方案 4（Replay 取消）：`Replay(ctx, ...)` 在回放循环内检查 `ctx.Err()`，`Ctrl+C` 可即时中止渲染/回放。
  - 失败优先测试：`TestStreamingRenderThrottledByInterval`（`internal/tui`）、`TestReplayHonorsCancellation`（`internal/query`），均 RED→GREEN；受影响的既有测试同步更新（`TestModelStreamsPromptOutput` 改为跨门限推进时钟）；`go test ./... -count=1` 全绿、`go vet`、`git diff --check` 通过。
- **方案 1（glamour renderer 复用）：暂不做。** 节流把重渲次数从"每 delta 一次"降到"每 ~50ms 一次"（个位数量级）后，renderer 构造成本已可忽略；且按宽度缓存 renderer 有主题样式失效风险。按"简单至上"未纳入本次，确有需要再评估。
- **补充修复（2026-07-25，真机验收发现）：`View()` 每帧全量渲染旁路了节流。** 含节流的新二进制在 gpt-5.5 长响应（1161 text_deltas）下仍有 23s 渲染间隔（session `3d50e323`）。根因：Bubble Tea 每个 delta 触发一次 `View()`，而 `View()` 无条件调用 `liveTranscriptView()`（完整 glamour 渲染）仅为判断非空，渲染结果被丢弃、改用 viewport 缓存显示——节流只堵了 `refreshViewport`，没堵这条每帧路径。修复：busy 且无 pendingPermission 时，`View()` 直接复用 `refreshViewport` 已渲染进 viewport 的内容（`hasLiveViewContent` 标志），空闲/权限路径保持原每帧决策不变。失败优先测试：`TestViewDoesNotReRenderMarkdownEveryFrame`（修前 5 帧=5 次渲染，修后 0 次）。
- **第二次真机验收（session `94a17fb1`，3222 text_deltas）仍卡死 → 找到最终真凶：spinner tick 旁路。** 含前两个修复的二进制（`go run`，00:47 构建）在 out=3467 的长响应上 usage 后 5 分半仍未落 assistant message，用户手杀。用户观察到"前面快、后面变慢"= 每次渲染成本随全文增长的 O(N²) 特征。静态定位：`spinnerTickMsg`（每 120ms）在 busy 期间无条件调 `refreshViewport()` 全量渲染，完全绕过 delta 节流——文末一次渲染 500ms+ 时，10Hz 的动画需求把事件队列压垮，delta 与 Ctrl+C 全部饿死。`tickMsg`（1s）同病。
- **最终修复：自适应节流 `refreshViewportThrottled`。** 渲染间隔 = clamp(3×上次渲染实测耗时, 50ms, 2s)，spinner tick / 1s tick / 流式 delta 三条热路径共用同一门限（渲染开销被约束在事件循环的 ~1/3 以内，无论文档多大）；完成/阶段切换等低频路径仍走未节流 `refreshViewport` 保证即时。测试：`TestSpinnerTickRenderThrottledWhileBusy`（修前 5 tick=5 渲染，修后 0）、`TestLiveRenderIntervalScalesWithCost`（3×成本 + 上下限钳制）。
- **Phase 2/3（流式增量落盘）：条件性、待评估。** 需观察 Phase 1 后长响应"流式中看不到早期行/无法上翻"是否仍构成真实痛点再决定；若做，必须走 displayTimeline 的 `printedSeq`（见非目标 2）。
- **真流式（Option A，2026-07-25 实施）：打字机效果的架构性修复。** 性能修好后用户反馈"打字机效果消失"——根因：query 层原为"缓冲到完成→Replay 回放"，旧打字机观感其实是 O(N²) bug 在给回放配速；渲染修快后回放 ~1s 灌完，文字整段出现。实施：
  - **能撤回才直播**：`liveStreaming = cb.onTextAmended != nil`。TUI（经 `TextAmendSink`）直播生成中的文本 delta（真打字机，反映真实生成节奏）；stdout/`-p` 管道/`RunStreamJSON`/server 等无撤回能力的 sink 保持原缓冲契约（gate 撤回的废稿永不进入不可回收的输出），golden 逐字节不变。
  - **直播的正确性配套**：① 完成门重试（`continue`）前发 `onTextAmended(本轮已直播文本, "")` 撤回废稿；② 接受文本与直播文本不一致时 `reconcileStreamedText` 修正——前缀扩展只补尾巴（exact-output 补全对所有 sink 生效）、其余走 amend；③ 禁止输出字面量经每轮新建的 `forbiddenOutputGuard.OnText` 在直播流内实时消音（含 `Flush` 尾部释放），不再依赖"回放时替换"。
  - **TUI 侧**：新增 `StreamTextAmended` 事件（`PrevText`→`Text`），`amendLiveAssistantText` 对消息与 displayTimeline 段做精确后缀替换/整段撤回。
  - 测试：`TestStreamTextReachesSinkDuringGeneration`（生成期间文本即达 sink 且不重复）、`TestPlainSinkKeepsBufferedContract`（纯 writer 仍缓冲）、`TestReconcileStreamedText`（匹配/无delta/前缀扩展/分歧/错误五分支）、`TestStreamTextAmendedReplacesLiveTail`（TUI 替换与撤回）；query/tui 全包及 `./...` 全绿（含流式 JSON golden 逐事件顺序回归）。
  - **真机 PTY 验收（2026-07-25，gpt-5.5，证据 `/tmp/gocc-tui-live-streaming-verify/`）**：① 打字机——600 字长答案在生成期间 t=16s→25s 连续 10 秒逐步流出（每秒 +134~+742 可见字符），完成后状态干净回到 `Ready`；前置 ~9s 空窗为网关首 token 延迟，非渲染问题。② 流式中途 Ctrl+C——发送后 **54ms** 内容停止增长，已收部分正常落盘 scrollback，TUI 即刻回 `Ready` 保持响应（对比修复前 `Stopping...` 挂起数分钟需强杀）。验收方式：Python PTY 驱动注入输入、按时间戳截帧、统计可见中文字数增长曲线（`tui_drive.py`）。
