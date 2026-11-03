# TUI 语义连续多轮真实 PTY 验收方案

本文档定义 TUI 显示类问题的后续验收标准。目标不是证明模型能力，而是用真实的人类与 AI 对话形态验证 TUI 在连续多轮、语义承接、长回复、工具事件和底部 chrome 同时存在时，仍然不出现消息拆裂、覆盖、跳位、异常空白和历史错序。

结论：可以做到。验收不能再用 10 轮重复同一句问题。必须使用有上下文依赖的语义剧本，让每一轮都引用上一轮回复中的具体模块、句子或结论。

## 问题背景

之前的 TUI 显示验收已经覆盖了真实 PTY 和真实 Terminal 截图，但多轮输入大多是重复问题，例如连续问“这个项目是干嘛的”。这种验收能发现底部遮挡、scrollback flush、`Go Claude` 重复和 `内容覆盖：AI认知入门` 拆裂问题，但还不够接近真实使用。

真实用户对话通常是：

1. 第一轮问项目整体。
2. 第二轮引用第一轮里的某个模块，要求展开。
3. 第三轮引用第二轮里的某个判断，继续追问问题、风险或实现路径。
4. 后续多轮不断沿着同一主题链深入。

如果只用重复问题，TUI 可能在语义连续场景中仍然暴露新问题：

- 上一轮长回复尾部被新一轮 prompt 或底部 chrome 挤压。
- 第二轮引用上一轮内容时，旧回复残留在 live viewport。
- 多轮完成态 flush 后，`Go Claude` 数量和 assistant 消息数不一致。
- 工具事件插入后，上一轮/下一轮的语义块被视觉上拆开。
- 中间大空白让用户误以为回复断裂或历史丢失。

## 验收原则

1. 眼见为实：必须有真实 macOS Terminal 窗口截图，文字清楚可读。
2. 真实 PTY：输入必须通过 PTY 写入 TUI，不用直接调用 renderer 冒充终端。
3. 语义连续：每一轮 prompt 必须引用上一轮回复的具体内容。
4. 多轮连续：默认 10 轮，最低不得少于 6 轮。
5. 双证据：同时保留截图证据和 PTY 文本转储。
6. 可复现：语义剧本、输入、期望锚点和截图路径必须记录到报告。
7. 不污染仓库：临时 harness 和 driver 验收后删除；只有产品修复和正式测试进入提交。

## 语义剧本设计

验收剧本必须是一条递进链，而不是独立问题列表。

推荐基线剧本：

| 轮次 | 用户输入 | 回复必须包含的语义锚点 | 下一轮承接点 |
| --- | --- | --- | --- |
| 1 | `这是第一轮。这个项目是干嘛的？` | `Anything-AI`、`系统性AI知识索引`、`内容覆盖` | 第二轮追问系统性知识索引 |
| 2 | `第一轮里你说“系统性AI知识索引”，请详述它和普通资料合集的区别。` | `结构化路径`、`碎片化信息`、`学习阶段` | 第三轮追问碎片化信息 |
| 3 | `第二轮里你提到“碎片化信息”，具体会造成什么问题？` | `AI焦虑`、`误判能力边界`、`工具选择混乱` | 第四轮追问工具选择 |
| 4 | `第三轮里说到“工具选择混乱”，这个项目如何做工具选择矩阵？` | `场景`、`成本`、`能力边界`、`替代方案` | 第五轮追问能力边界 |
| 5 | `第四轮里的“能力边界”怎么判断？举一个AI工具不适合使用的例子。` | `高风险决策`、`事实核验`、`人类确认` | 第六轮追问人类确认 |
| 6 | `第五轮说需要“人类确认”，这个项目如何避免用户盲从AI？` | `不神话`、`不妖魔化`、`实践出真知` | 第七轮追问实践路径 |
| 7 | `第六轮里的“实践出真知”具体应该怎么落到学习路径？` | `入门`、`工具选择`、`角色案例`、`技能包` | 第八轮追问角色案例 |
| 8 | `第七轮提到“角色案例”，如果我是产品经理，应该看哪些模块？` | `产品经理`、`需求分析`、`原型`、`评审` | 第九轮追问模块组织 |
| 9 | `第八轮里的产品经理路径，放到VitePress文档结构里应该怎么组织？` | `目录`、`导航`、`中英文双语`、`索引` | 第十轮做总结 |
| 10 | `请基于前九轮，总结这个项目的核心价值、主要风险和下一步改进建议。` | `核心价值`、`主要风险`、`下一步`、`不要盲从` | 最终截图检查 |

通过标准：

- 第 N 轮回复必须能看出它理解了第 N-1 轮的问题。
- 截图里至少能看见最近一轮或最近两轮的完整语义链。
- 文本转储里 10 轮关键锚点都存在。
- 不允许用 10 个相互无关或完全重复的问题替代。

## 回复生成方式

验收有两种模式，优先级如下。

### 模式 A：确定性语义 harness

用于 TUI 显示回归的默认模式。

做法：

- 临时 harness 内置 10 轮语义剧本。
- 每轮根据 prompt 中的轮次返回对应语义连续回复。
- 回复内容固定，避免真实模型波动导致验收不稳定。
- 第 4、8 或 10 轮插入工具事件，覆盖 `assistant text -> tool -> assistant text`。
- 第 10 轮故意包含历史问题里的风险短语，例如 `内容覆盖：AI认知入门`，验证不会再次拆裂。

优点：

- 稳定、可重复、无需外部 provider。
- 能精确覆盖已知 TUI 显示风险。
- 失败时可以确认是 TUI 显示层问题，不是模型回答变化。

限制：

- 它证明的是 TUI 对语义连续对话形态的显示稳定性，不证明真实模型推理质量。

### 模式 B：真实模型语义会话

用于发版前或用户明确要求真实模型时。

做法：

- 使用真实 provider 和真实项目 cwd。
- 按同一份 10 轮语义剧本逐轮发送。
- 不要求模型逐字命中固定回复，但必须命中每轮语义锚点。
- 截图和文本转储同样保留。

风险：

- 模型可能回答漂移，导致语义锚点检查需要人工判断。
- 网络、限流、provider 输出格式都会引入非 TUI 变量。

因此，TUI 修复的日常回归默认使用模式 A；重大改动或发版前追加模式 B。

## PTY 驱动方案

真实验收必须通过 PTY 驱动 TUI：

1. 使用临时 driver 调用 `os.forkpty()`。
2. 子进程执行 `go run ./cmd/tui-semantic-pty-harness` 或正式验收二进制。
3. 父进程按剧本逐轮写入 prompt，并等待每轮输出稳定。
4. 父进程把 PTY 原始输出镜像到当前 Terminal stdout。
5. macOS `screencapture` 捕获真实 Terminal 窗口。

关键点：

- 不使用 AppleScript 键盘注入；它依赖 Accessibility 权限，不稳定。
- 不使用 renderer 生成的 PNG 代替真实屏幕。
- 不用纯文本日志冒充截图。
- 可以保留 ANSI 原始输出和去 ANSI 后的 `plain.txt` 作为辅助证据。

## 截图验收规范

截图必须满足：

1. 截图来自新打开的真实 Terminal 窗口，不能误截 Codex 窗口。
2. 图片中能看清中文正文、`Go Claude`、`Usage`、输入框和 `status`。
3. 至少两张：
   - `mid-readable.png`：第 5-8 轮运行中或刚完成。
   - `final-readable.png`：第 10 轮完成后。
4. 如果截图误抓到别的窗口，必须重截，不可作为通过证据。
5. 最终回复必须内嵌或链接截图路径，便于用户直接查看。

人工检查清单：

- 第 10 轮回复完整可读。
- `内容覆盖：AI认知入门` 不能被拆成 `内容覆盖` + 新 `Go Claude` + `: AI认知入门`。
- `Go Claude` 数量不能异常增加。
- 底部 `Usage/input/status/controls` 不覆盖正文尾部。
- 中间没有大段异常空白把同一条回复拆开。
- 工具事件如果出现，位置固定在相关 assistant 片段之间，不跳到底部。

## 自动文本断言

截图是最终证据，但还要有文本断言帮助快速定位问题。

去 ANSI 后的 `plain.txt` 至少检查：

```text
OK  turn 01 anchor
OK  turn 02 references turn 01
OK  turn 03 references turn 02
OK  turn 10 final summary
OK  contiguous content anchor
OK  no split before Go Claude
OK  ready status
```

建议断言：

- 包含 `第10轮`。
- 包含 `核心价值`、`主要风险`、`下一步`。
- 包含 `内容覆盖：AI认知入门`。
- 不包含 `内容覆盖\n\nGo Claude\n:` 或等价 CRLF 形态。
- 不包含重复的底部工具块。
- `status  Ready` 出现在最终状态。

## 失败判定

出现以下任一情况即失败：

1. 任一轮回复明显不承接上一轮。
2. 同一条 assistant 回复被拆成两个 `Go Claude` 块。
3. 历史消息被新消息覆盖或替换为空白。
4. 用户消息下方出现不合理大空白。
5. assistant 尾部被 `Usage/input/status/controls` 覆盖。
6. 工具命令块完成后位置跳变。
7. 截图不可读、截错窗口或只提供像素生成图。
8. 临时 harness/driver 被误提交。

## 推荐产物目录

每次验收输出到临时目录：

```text
/tmp/gocc-tui-semantic-pty-<timestamp>/
  mid-readable.png
  final-readable.png
  plain.txt
  raw.ansi
  report.json
```

`report.json` 建议字段：

```json
{
  "scenario": "semantic-10-turn-anything-ai",
  "mode": "deterministic-harness",
  "turns": 10,
  "screenshots": {
    "mid": "/tmp/.../mid-readable.png",
    "final": "/tmp/.../final-readable.png"
  },
  "checks": {
    "semantic_chain": true,
    "contiguous_content_anchor": true,
    "no_split_before_go_claude": true,
    "bottom_chrome_not_covering_tail": true,
    "ready_status": true
  }
}
```

## 后续实施建议

P0：

- 新增正式脚本 `scripts/tui-semantic-pty-acceptance.sh`。
- 新增可复用 harness，避免每次手写临时代码。
- 脚本自动创建新 Terminal 窗口、运行 PTY driver、截图、生成 report。
- 脚本最后提醒人工查看截图，不自动把像素判断当最终通过。

P1：

- 增加真实模型模式：同一语义剧本走真实 provider。
- 支持 `--scenario anything-ai`、`--scenario tool-heavy`、`--scenario resume-history`。
- 支持读取历史问题 session 并 replay 成语义连续剧本。

P2：

- 将截图路径和 report 接入 release gate。
- 对截图做 OCR 辅助检查，但 OCR 只做辅助，不替代人眼验收。

## 最小执行命令形态

未来脚本完成后，推荐命令形态：

```bash
scripts/tui-semantic-pty-acceptance.sh --scenario anything-ai --turns 10 --screenshots
```

输出必须包含：

```text
mid_screenshot=/tmp/gocc-tui-semantic-pty-.../mid-readable.png
final_screenshot=/tmp/gocc-tui-semantic-pty-.../final-readable.png
plain_text=/tmp/gocc-tui-semantic-pty-.../plain.txt
report=/tmp/gocc-tui-semantic-pty-.../report.json
```

## 当前结论

后续 TUI 显示修复验收必须升级为：

```text
语义连续 10 轮剧本
+ 真实 PTY 驱动
+ 新 Terminal 窗口
+ 可读真实截图
+ 文本锚点断言
+ 人工检查底部遮挡/空白/跳位
```

这套验收能更接近真实人类与 AI 的连续对话，避免只用重复 prompt 得到虚假的通过信号。
