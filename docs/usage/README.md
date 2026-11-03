# niuma 使用说明（分模式场景指南）

按**运行模式 × 使用场景**组织的使用说明。每个场景统一结构：**一句话定位 → 启动/命令 → 界面说明 → 要点**。

## 三种模式

| 模式 | 适合 | 入口 | 场景文档 |
| --- | --- | --- | --- |
| **TUI** | 本地交互写代码、读文件、跑工具、审批权限 | `go run ./cmd/niuma` | [tui.md](tui.md) · 11 个场景 |
| **CLI**（print/headless） | 脚本、CI、一次性自动化 | `-p, --print` | [cli.md](cli.md) · 6 个场景 |
| **agent-webui** | 浏览器界面、任务运行、全链路观测 | `scripts/web-agent-start.sh` | [webui.md](webui.md) · 7 个场景 |
| **Desktop Computer Use** | macOS 单屏开发者预览、受安全边界约束的真实桌面操作 | `scripts/build-desktop-v2.sh` | [computer-use.md](computer-use.md) |

想先装上并跑起来看根 [README 安装](../../README.md#安装)；运行架构和时序图见 [architecture/runtime_modes.md](../architecture/runtime_modes.md)。

## 场景速览

- **TUI**：简单对话 · 单个简单任务 · 代码修改（含权限审批）· Skills 使用与创建 · 网络搜索 · 多 subagent 并行 · 会话管理 · Goal/Loop · 自动压缩与 usage · Git 提交与推送 · 代码审查（Review 提交）
- **CLI**：一次性问答 · 结构化输出（json/stream-json）· 脚本自动化（`--cwd`）· `--bare` 最小 runtime · 后台长任务 · 诊断
- **agent-webui**：启动首屏 · 简单对话（SSE）· 单任务创建运行 · 任务取消/重试 · 多轮会话 · Trace 观测 · 桌面/移动响应式

CLI 和 TUI 都可叠加 [`--bare` runtime profile](bare.md)，只自动装配 `Read`、`Edit`、`Bash`，并关闭未显式请求的上下文与扩展发现。

## Desktop 个性化

Desktop/WebUI 的宠物、背景和视觉设置见 [桌面个性化指南](desktop-personalization.md)。当前内置陪伴角色为 Go 伙伴和中国龙；Desktop 额外支持本地背景上传。CLI/TUI 不依赖这些视觉能力。

---

## 为什么这里几乎没有截图

这三份文档曾按「每个场景配一张截图」的结构写成，一共引用了 29 张图，其中 **26 张从未进过仓库**
（AUDIT-P1-34）。渲染出来就是 26 个碎图标 —— 对读者来说这比没有截图更糟，因为它同时说明
「本该有图」和「这份文档没人维护」。

已经把那些引用删掉，改成文字描述，而不是补图或留「待补」占位。理由：

- **这个仓库的 UI 每天在改。** TUI 的显示架构、渲染预算、viewport 遮挡、交互卡片层级近期都在
  改（见 [tui/](../tui/) 下的一批修复方案）。今天拍的截图下周就和实际界面不一致，而**过期的截图
  比没有截图更能骗人** —— 它看上去是证据。
- **大部分场景拍不出诚实的截图。** 网络搜索、多 subagent、Goal/Loop、自动压缩都需要真实 key
  和真实模型轮次才会出现对应界面；为了配图去造一张假的界面图，是这套文档最不该做的事。
- **「截图待补」标记已经试过了，没用。** 上一版就在清单里逐行标了 `📷 待补`，26 个坏引用照样
  留在正文里。标记没有阻止文档骗人。

**保留下来的图只有真实存在、且有验收证据兜底的那些：**

| 图 | 用在哪 | 为什么留 |
| --- | --- | --- |
| `images/tui-11-code-review-a.png`、`images/tui-11-code-review-b.png` | [tui.md](tui.md) 场景 11 | 真实 review 运行的产物，展示的是"多步工具编排 + 错误恢复"这类不随皮肤变化的行为 |
| `images/review-compare-claude-code.png` | [tui.md](tui.md) 场景 11 的模型对比 | 同一 prompt 换模型的对照证据，是这一节的论点本身 |
| `../web_agent/images/` 下 6 张 | [webui.md](webui.md) 场景 1/3/4/7 | Web Agent 真机 E2E 验收留下的证据图，有 `scripts/web-agent-real-e2e.sh` 兜底 |

## 维护规则

- 新增场景：在对应模式文档里加一节，写清「命令 → 界面/输出说明 → 要点」。
- **不要为了让文档看起来完整而引用还不存在的图。** 引用一张图之前，先把它提交进仓库。
- 想加截图，先问一句「这张图描述的东西三个月后还成立吗」。答案是「不」就写成文字。
- 图片统一放 `docs/usage/images/`；能复用 `docs/web_agent/images/` 现有截图时优先复用，不重复提交同一张图。
- 文字部分只写已印证的真实能力，能力边界见 [docs/compatibility_matrix.md](../compatibility_matrix.md)。
